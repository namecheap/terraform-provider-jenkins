package jenkins

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestCovQ_CertWriteOnly(t *testing.T) {
	ctx := context.Background()
	credXML := `<com.cloudbees.plugins.credentials.impl.CertificateCredentialsImpl><id>c</id><scope>GLOBAL</scope><description>d</description></com.cloudbees.plugins.credentials.impl.CertificateCredentialsImpl>`
	r := &credentialCertificateResource{resourceHelper: &resourceHelper{client: covCredClient(t, credXML)}}
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	s := sr.Schema

	// plan/state carry no keystore; config supplies the write-only keystore.
	planModel := &credentialCertificateResourceModel{
		Name: types.StringValue("c"), Folder: types.StringNull(), Description: types.StringValue("d"),
		Domain: types.StringValue("_"), Scope: types.StringValue("GLOBAL"),
		Keystore: types.StringNull(), KeystoreWo: types.StringNull(), KeystoreWoVersion: types.StringValue("1"),
		Password: types.StringValue("pw"),
	}
	cfgModel := &credentialCertificateResourceModel{
		Name: types.StringValue("c"), Folder: types.StringNull(), Description: types.StringValue("d"),
		Domain: types.StringValue("_"), Scope: types.StringValue("GLOBAL"),
		Keystore: types.StringNull(), KeystoreWo: types.StringValue("d29rZXk="), KeystoreWoVersion: types.StringValue("1"),
		Password: types.StringValue("pw"),
	}
	plan := tfsdk.Plan{Schema: s}
	plan.Set(ctx, planModel)
	cfgState := tfsdk.State{Schema: s}
	cfgState.Set(ctx, cfgModel)
	cfg := tfsdk.Config{Schema: s, Raw: cfgState.Raw}
	cResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: plan, Config: cfg}, &cResp)
	if cResp.Diagnostics.HasError() {
		t.Errorf("certificate Create (write-only keystore): %v", cResp.Diagnostics)
	}
}

func TestCovQ_RoleItem(t *testing.T) {
	ctx := context.Background()
	r := &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockAddRole:    func(context.Context, string, string, []string, string, bool) error { return nil },
		mockAssignRole: func(context.Context, string, string, string) error { return nil },
		mockGetRole: func(_ context.Context, _, _ string, out interface{}) error {
			resp := out.(*roleStrategyRoleResponse)
			resp.PermissionIDs = map[string]bool{"hudson.model.Item.Build": true}
			resp.Pattern = "team/.*"
			return nil
		},
	}}}
	s := covMSchema(ctx, r)
	model := &roleResourceModel{
		Type: types.StringValue("item"), Name: types.StringValue("dev"), Pattern: types.StringValue("team/.*"),
		Permissions: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("hudson.model.Item.Build")}),
		Assignments: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("alice")}),
	}
	covMResourceCRUD(ctx, t, "role item", r, s, model)
}

func TestCovQ_Extra(t *testing.T) {
	ctx := context.Background()

	// certificate Read: malformed credential XML → xml.Unmarshal error
	client := covCredClient(t, "<broken")
	rc := &credentialCertificateResource{resourceHelper: &resourceHelper{client: client}}
	var sr resource.SchemaResponse
	rc.Schema(ctx, resource.SchemaRequest{}, &sr)
	certModel := &credentialCertificateResourceModel{
		Name: types.StringValue("c"), Folder: types.StringNull(), Description: types.StringValue("d"),
		Domain: types.StringValue("_"), Scope: types.StringValue("GLOBAL"),
		Keystore: types.StringValue("YmFzZTY0"), KeystoreWo: types.StringNull(), KeystoreWoVersion: types.StringNull(),
		Password: types.StringValue("pw"),
	}
	stc := tfsdk.State{Schema: sr.Schema}
	stc.Set(ctx, certModel)
	reResp := resource.ReadResponse{State: tfsdk.State{Schema: sr.Schema}}
	rc.Read(ctx, resource.ReadRequest{State: stc}, &reResp)
	if !reResp.Diagnostics.HasError() {
		t.Error("certificate Read should error on malformed credential XML")
	}

	// configuration_as_code Read: export returns invalid YAML → compare error
	rcasc := &configurationAsCodeResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockExportCASC: func(context.Context) (string, error) { return "a: b: c: [", nil },
	}}}
	var sca resource.SchemaResponse
	rcasc.Schema(ctx, resource.SchemaRequest{}, &sca)
	cascModel := &configurationAsCodeResourceModel{Section: types.StringValue("jenkins"), YAML: types.StringValue("systemMessage: hi")}
	stca := tfsdk.State{Schema: sca.Schema}
	stca.Set(ctx, cascModel)
	rr := resource.ReadResponse{State: tfsdk.State{Schema: sca.Schema}}
	rcasc.Read(ctx, resource.ReadRequest{State: stca}, &rr)
	if !rr.Diagnostics.HasError() {
		t.Error("casc Read should error when the export is invalid YAML")
	}

	// configuration_as_code ImportState: export error
	isr := resource.ImportStateResponse{State: tfsdk.State{Schema: sca.Schema, Raw: covMNull(ctx, sca.Schema)}}
	rImp := &configurationAsCodeResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockExportCASC: func(context.Context) (string, error) { return "", fmt.Errorf("boom") },
	}}}
	rImp.ImportState(ctx, resource.ImportStateRequest{ID: "jenkins"}, &isr)
	if !isr.Diagnostics.HasError() {
		t.Error("casc ImportState should error when export fails")
	}
}
