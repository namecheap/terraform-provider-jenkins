package jenkins

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Credential CRUD is exercised end-to-end against a live httptest-backed client
// so the gojenkins CredentialsManager paths (Add/GetSingle/Update/Delete) — which
// are not behind the mockable interface — are covered without a refactor. The
// server answers the CSRF-crumb GET with JSON, credential POSTs with 200, and a
// config.xml GET with the supplied credential XML for read-back.
func covCredClient(t *testing.T, credXML string) *jenkinsAdapter {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "crumbIssuer"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"crumb":"abc","crumbRequestField":"Jenkins-Crumb"}`)
		case r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "config.xml"):
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, credXML)
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{}`)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := newJenkinsClient(&Config{ServerURL: srv.URL})
	if err != nil {
		t.Fatalf("newJenkinsClient: %v", err)
	}
	return c
}

// covLCredCRUD drives Create/Read/Update/Delete with Config populated (needed for
// the write-only secret read) and asserts no diagnostics.
func covLCredCRUD(ctx context.Context, t *testing.T, label string, r resource.Resource, s rschema.Schema, model any) {
	t.Helper()
	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(ctx, model); d.HasError() {
		t.Fatalf("%s plan.Set: %v", label, d)
	}
	cfgState := tfsdk.State{Schema: s}
	cfgState.Set(ctx, model)
	cfg := tfsdk.Config{Schema: s, Raw: cfgState.Raw}

	cResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: plan, Config: cfg}, &cResp)
	if cResp.Diagnostics.HasError() {
		t.Errorf("%s Create: %v", label, cResp.Diagnostics)
	}

	st := tfsdk.State{Schema: s}
	st.Set(ctx, model)
	rResp := resource.ReadResponse{State: tfsdk.State{Schema: s}}
	r.Read(ctx, resource.ReadRequest{State: st}, &rResp)
	if rResp.Diagnostics.HasError() {
		t.Errorf("%s Read: %v", label, rResp.Diagnostics)
	}

	uResp := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: st, Config: cfg}, &uResp)
	if uResp.Diagnostics.HasError() {
		t.Errorf("%s Update: %v", label, uResp.Diagnostics)
	}

	dResp := resource.DeleteResponse{State: st}
	r.Delete(ctx, resource.DeleteRequest{State: st}, &dResp)
	if dResp.Diagnostics.HasError() {
		t.Errorf("%s Delete: %v", label, dResp.Diagnostics)
	}
}

// covCredClientCode returns a client whose server responds with the given status
// for credential POSTs (Add/Update/Delete) and config.xml GETs (read-back),
// so error and not-found branches can be exercised.
func covCredClientCode(t *testing.T, postCode, getCode int) *jenkinsAdapter {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "crumbIssuer"):
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"crumb":"abc","crumbRequestField":"Jenkins-Crumb"}`)
		case r.Method == http.MethodPost:
			w.WriteHeader(postCode)
		default: // any GET (config.xml read-back, credential list, etc.)
			w.WriteHeader(getCode)
		}
	}))
	t.Cleanup(srv.Close)
	c, err := newJenkinsClient(&Config{ServerURL: srv.URL})
	if err != nil {
		t.Fatalf("newJenkinsClient: %v", err)
	}
	return c
}

func covSTResource(client *jenkinsAdapter) (*credentialSecretTextResource, rschema.Schema) {
	r := &credentialSecretTextResource{credentialCRUD: newCredentialCRUD(secretTextCredentialSpec())}
	r.client = client
	var sr resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &sr)
	return r, sr.Schema
}

func covSTModel() *credentialSecretTextResourceModel {
	return &credentialSecretTextResourceModel{
		Name: types.StringValue("tok"), Folder: types.StringNull(), Description: types.StringValue("d"),
		Domain: types.StringValue("_"), Scope: types.StringValue("GLOBAL"),
		Secret: types.StringValue("s"), SecretWo: types.StringNull(), SecretWoVersion: types.StringNull(),
	}
}

func TestCovL_CredentialErrors(t *testing.T) {
	ctx := context.Background()
	model := covSTModel()

	// Create error: Add POST returns 500
	r, s := covSTResource(covCredClientCode(t, 500, 200))
	plan := tfsdk.Plan{Schema: s}
	plan.Set(ctx, model)
	cfgState := tfsdk.State{Schema: s}
	cfgState.Set(ctx, model)
	cfg := tfsdk.Config{Schema: s, Raw: cfgState.Raw}
	cResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: plan, Config: cfg}, &cResp)
	if !cResp.Diagnostics.HasError() {
		t.Error("credential Create should error on Add 500")
	}

	// Read not-found: GetSingle config.xml returns 404 → resource removed (no error)
	rNF, sNF := covSTResource(covCredClientCode(t, 200, 404))
	stNF := tfsdk.State{Schema: sNF}
	stNF.Set(ctx, model)
	rnfResp := resource.ReadResponse{State: tfsdk.State{Schema: sNF}}
	rNF.Read(ctx, resource.ReadRequest{State: stNF}, &rnfResp)
	if rnfResp.Diagnostics.HasError() {
		t.Errorf("credential Read 404 should not error (removed): %v", rnfResp.Diagnostics)
	}

	// Read error: GetSingle returns 500 → error
	rErr, sErr := covSTResource(covCredClientCode(t, 200, 500))
	stErr := tfsdk.State{Schema: sErr}
	stErr.Set(ctx, model)
	reResp := resource.ReadResponse{State: tfsdk.State{Schema: sErr}}
	rErr.Read(ctx, resource.ReadRequest{State: stErr}, &reResp)
	if !reResp.Diagnostics.HasError() {
		t.Error("credential Read should error on GetSingle 500")
	}

	// Update error: Update POST returns 500
	rU, sU := covSTResource(covCredClientCode(t, 500, 200))
	planU := tfsdk.Plan{Schema: sU}
	planU.Set(ctx, model)
	stU := tfsdk.State{Schema: sU}
	stU.Set(ctx, model)
	cfgU := tfsdk.Config{Schema: sU, Raw: stU.Raw}
	uResp := resource.UpdateResponse{State: tfsdk.State{Schema: sU}}
	rU.Update(ctx, resource.UpdateRequest{Plan: planU, State: stU, Config: cfgU}, &uResp)
	if !uResp.Diagnostics.HasError() {
		t.Error("credential Update should error on Update 500")
	}

	// Delete error: doDelete POST returns 500
	rD, sD := covSTResource(covCredClientCode(t, 500, 200))
	stD := tfsdk.State{Schema: sD}
	stD.Set(ctx, model)
	dResp := resource.DeleteResponse{State: stD}
	rD.Delete(ctx, resource.DeleteRequest{State: stD}, &dResp)
	if !dResp.Diagnostics.HasError() {
		t.Error("credential Delete should error on doDelete 500")
	}
}

func TestCovL_SecretTextLive(t *testing.T) {
	ctx := context.Background()
	credXML := `<org.jenkinsci.plugins.plaincredentials.impl.StringCredentialsImpl><id>tok</id><scope>GLOBAL</scope><description>d</description></org.jenkinsci.plugins.plaincredentials.impl.StringCredentialsImpl>`
	client := covCredClient(t, credXML)
	r := &credentialSecretTextResource{credentialCRUD: newCredentialCRUD(secretTextCredentialSpec())}
	r.client = client
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	model := &credentialSecretTextResourceModel{
		Name: types.StringValue("tok"), Folder: types.StringNull(), Description: types.StringValue("d"),
		Domain: types.StringValue("_"), Scope: types.StringValue("GLOBAL"),
		Secret: types.StringValue("s"), SecretWo: types.StringNull(), SecretWoVersion: types.StringNull(),
	}
	covLCredCRUD(ctx, t, "secret_text", r, sr.Schema, model)
}

func TestCovL_CertificateLive(t *testing.T) {
	ctx := context.Background()
	credXML := `<com.cloudbees.plugins.credentials.impl.CertificateCredentialsImpl><id>c</id><scope>GLOBAL</scope><description>d</description><password>pw</password></com.cloudbees.plugins.credentials.impl.CertificateCredentialsImpl>`
	client := covCredClient(t, credXML)
	r := &credentialCertificateResource{resourceHelper: &resourceHelper{client: client}}
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	model := &credentialCertificateResourceModel{
		Name: types.StringValue("c"), Folder: types.StringNull(), Description: types.StringValue("d"),
		Domain: types.StringValue("_"), Scope: types.StringValue("GLOBAL"),
		Keystore: types.StringValue("YmFzZTY0"), KeystoreWo: types.StringNull(), KeystoreWoVersion: types.StringNull(),
		Password: types.StringValue("pw"),
	}
	covLCredCRUD(ctx, t, "certificate", r, sr.Schema, model)
}
