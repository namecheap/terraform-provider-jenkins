package jenkins

import (
	"context"
	"testing"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Error-branch coverage for data-source Reads (acceptance covers only the happy
// path). Each data source is read with a client that fails, asserting the error
// diagnostic branch runs.

func covDSErr(ctx context.Context, t *testing.T, label string, d datasource.DataSource, model any) {
	t.Helper()
	var sr datasource.SchemaResponse
	d.Schema(ctx, datasource.SchemaRequest{}, &sr)
	st := tfsdk.State{Schema: sr.Schema}
	if dg := st.Set(ctx, model); dg.HasError() {
		t.Fatalf("%s config set: %v", label, dg)
	}
	cfg := tfsdk.Config{Schema: sr.Schema, Raw: st.Raw}
	resp := datasource.ReadResponse{State: tfsdk.State{Schema: sr.Schema}}
	d.Read(ctx, datasource.ReadRequest{Config: cfg}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Errorf("%s: expected Read error", label)
	}
}

func TestCovF_DataSourceErrors(t *testing.T) {
	ctx := context.Background()
	boom := func() error { return covEBoom() }

	// job / folder: GetJob fails
	covDSErr(ctx, t, "ds job", &jobDataSource{dataSourceHelper: &dataSourceHelper{client: &mockJenkinsClient{
		mockGetJob: func(context.Context, string, ...string) (*jenkins.Job, error) { return nil, boom() },
	}}}, &jobDataSourceModel{Name: types.StringValue("j"), Folder: types.StringNull(), ID: types.StringNull(), Template: types.StringNull()})

	covDSErr(ctx, t, "ds folder", &folderDataSource{dataSourceHelper: &dataSourceHelper{client: &mockJenkinsClient{
		mockGetJob: func(context.Context, string, ...string) (*jenkins.Job, error) { return nil, boom() },
	}}}, &folderDataSourceModel{Name: types.StringValue("f"), Folder: types.StringNull(), ID: types.StringNull(), Description: types.StringNull(), DisplayName: types.StringNull(), Template: types.StringNull()})

	// view: GetView fails
	covDSErr(ctx, t, "ds view", &ViewDataSource{dataSourceHelper: &dataSourceHelper{client: &mockJenkinsClient{
		mockGetView: func(context.Context, string) (*jenkins.View, error) { return nil, boom() },
	}}}, &ViewDataSourceModel{Name: types.StringValue("v"), Folder: types.StringNull(), ID: types.StringNull(), Description: types.StringNull(), URL: types.StringNull()})

	// plugin: GetPlugin fails
	covDSErr(ctx, t, "ds plugin", &pluginDataSource{dataSourceHelper: &dataSourceHelper{client: &mockJenkinsClient{
		mockGetPlugin: func(context.Context, string) (*jenkins.Plugin, error) { return nil, boom() },
	}}}, &pluginDataSourceModel{Name: types.StringValue("p"), ID: types.StringNull(), Version: types.StringNull(), LongName: types.StringNull(), URL: types.StringNull(), Active: types.BoolNull(), Enabled: types.BoolNull()})

	// nodes: GetAllNodes fails
	covDSErr(ctx, t, "ds nodes", &nodesDataSource{dataSourceHelper: &dataSourceHelper{client: &mockJenkinsClient{
		mockGetAllNodes: func(context.Context) ([]*jenkins.Node, error) { return nil, boom() },
	}}}, &nodesDataSourceModel{ID: types.StringNull(), Nodes: types.SetNull(types.StringType)})

	// jobs (root): GetAllJobNames fails
	covDSErr(ctx, t, "ds jobs", &jobsDataSource{dataSourceHelper: &dataSourceHelper{client: &mockJenkinsClient{
		mockGetAllJobNames: func(context.Context) ([]jenkins.InnerJob, error) { return nil, boom() },
	}}}, &jobsDataSourceModel{ID: types.StringNull(), Folder: types.StringNull(), Jobs: types.SetNull(types.StringType)})

	// credentials: cm.List fails (live client returns 500)
	covDSErr(ctx, t, "ds credentials", &credentialsDataSource{dataSourceHelper: &dataSourceHelper{client: covCredClientCode(t, 200, 500)}},
		&credentialsDataSourceModel{ID: types.StringNull(), Folder: types.StringNull(), Domain: types.StringValue("_"), Credentials: types.SetNull(types.StringType)})

	// a credential data source (shared read): GetSingle fails (live client 500)
	dsSecret := &credentialSecretTextDataSource{credentialDataSource: newCredentialDataSource(secretTextCredentialDataSourceReader())}
	dsSecret.client = covCredClientCode(t, 200, 500)
	covDSErr(ctx, t, "ds credential_secret_text", dsSecret,
		&credentialSecretTextDataSourceModel{Name: types.StringValue("tok"), Folder: types.StringNull(), ID: types.StringNull(), Description: types.StringNull(), Domain: types.StringValue("_"), Scope: types.StringNull()})
}

func TestCovF_CertificateErrors(t *testing.T) {
	ctx := context.Background()
	model := &credentialCertificateResourceModel{
		Name: types.StringValue("c"), Folder: types.StringNull(), Description: types.StringValue("d"),
		Domain: types.StringValue("_"), Scope: types.StringValue("GLOBAL"),
		Keystore: types.StringValue("YmFzZTY0"), KeystoreWo: types.StringNull(), KeystoreWoVersion: types.StringNull(),
		Password: types.StringValue("pw"),
	}
	s := covMSchema(ctx, &credentialCertificateResource{resourceHelper: newResourceHelper()})

	// Create error (Add POST 500)
	rC := &credentialCertificateResource{resourceHelper: &resourceHelper{client: covCredClientCode(t, 500, 200)}}
	plan := tfsdk.Plan{Schema: s}
	plan.Set(ctx, model)
	stForCfg := tfsdk.State{Schema: s}
	stForCfg.Set(ctx, model)
	cfg := tfsdk.Config{Schema: s, Raw: stForCfg.Raw}
	cResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	rC.Create(ctx, resource.CreateRequest{Plan: plan, Config: cfg}, &cResp)
	if !cResp.Diagnostics.HasError() {
		t.Error("certificate Create should error on 500")
	}

	st := tfsdk.State{Schema: s}
	st.Set(ctx, model)

	// Read not-found (GetSingle 404 → removed, no error)
	rNF := &credentialCertificateResource{resourceHelper: &resourceHelper{client: covCredClientCode(t, 200, 404)}}
	rNF.Read(ctx, resource.ReadRequest{State: st}, &resource.ReadResponse{State: tfsdk.State{Schema: s}})

	// Read error (GetSingle 500)
	rErr := &credentialCertificateResource{resourceHelper: &resourceHelper{client: covCredClientCode(t, 200, 500)}}
	re := resource.ReadResponse{State: tfsdk.State{Schema: s}}
	rErr.Read(ctx, resource.ReadRequest{State: st}, &re)
	if !re.Diagnostics.HasError() {
		t.Error("certificate Read should error on 500")
	}

	// Delete error (doDelete POST 500)
	rDel := &credentialCertificateResource{resourceHelper: &resourceHelper{client: covCredClientCode(t, 500, 200)}}
	de := resource.DeleteResponse{State: st}
	rDel.Delete(ctx, resource.DeleteRequest{State: st}, &de)
	if !de.Diagnostics.HasError() {
		t.Error("certificate Delete should error on 500")
	}
}
