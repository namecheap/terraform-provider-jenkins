package jenkins

import (
	"context"
	"testing"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Exercise the nested-folder (parent) code paths — every other test uses a
// top-level resource (folder unset), which skips extractFolders / folderExists.
func covNestCreateRead(ctx context.Context, t *testing.T, label string, r resource.Resource, model any) {
	t.Helper()
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	plan := tfsdk.Plan{Schema: sr.Schema}
	if d := plan.Set(ctx, model); d.HasError() {
		t.Fatalf("%s plan.Set: %v", label, d)
	}
	cResp := resource.CreateResponse{State: tfsdk.State{Schema: sr.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &cResp)
	if cResp.Diagnostics.HasError() {
		t.Errorf("%s Create: %v", label, cResp.Diagnostics)
	}
	st := tfsdk.State{Schema: sr.Schema}
	st.Set(ctx, model)
	rResp := resource.ReadResponse{State: tfsdk.State{Schema: sr.Schema}}
	r.Read(ctx, resource.ReadRequest{State: st}, &rResp)
	if rResp.Diagnostics.HasError() {
		t.Errorf("%s Read: %v", label, rResp.Diagnostics)
	}
}

func TestCovNest_ParentFolder(t *testing.T) {
	ctx := context.Background()
	okFolder := func(context.Context, string, ...string) (*jenkins.Folder, error) { return &jenkins.Folder{}, nil }

	// folder nested in a parent
	fjob := covKJob(t, "/job/team/job/f", covNFolderXML)
	covNestCreateRead(ctx, t, "folder nested", &folderResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockGetFolder:         okFolder,
		mockCreateJobInFolder: func(context.Context, string, string, ...string) (*jenkins.Job, error) { return nil, nil },
		mockGetJob:            func(context.Context, string, ...string) (*jenkins.Job, error) { return fjob, nil },
	}}}, &folderResourceModel{
		Name: types.StringValue("f"), Folder: types.StringValue("team"),
		DisplayName: types.StringValue(""), Description: types.StringValue(""),
		Security: types.SetNull(folderSecurityObjectType), Template: types.StringNull(),
	})

	// job nested in a parent (disabled unset → no IsEnabled)
	jjob := covKJob(t, "/job/team/job/j", covNJobXML)
	covNestCreateRead(ctx, t, "job nested", &jobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockGetFolder:         okFolder,
		mockCreateJobInFolder: func(context.Context, string, string, ...string) (*jenkins.Job, error) { return jjob, nil },
		mockGetJob:            func(context.Context, string, ...string) (*jenkins.Job, error) { return jjob, nil },
	}}}, &jobResourceModel{
		Name: types.StringValue("j"), Folder: types.StringValue("team"),
		Template: types.StringValue(covNJobXML), Disabled: types.BoolNull(),
	})

	// pipeline nested in a parent
	pModel := &pipelineJobResourceModel{
		Name: types.StringValue("p"), Folder: types.StringValue("team"), Description: types.StringValue("d"),
		Script: types.StringValue("pipeline { agent any }"), Sandbox: types.BoolValue(true), Disabled: types.BoolValue(false),
	}
	pcfg, err := pModel.configXML()
	if err != nil {
		t.Fatalf("pipeline configXML: %v", err)
	}
	pjob := covKJob(t, "/job/team/job/p", pcfg)
	covNestCreateRead(ctx, t, "pipeline nested", &pipelineJobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockGetFolder:         okFolder,
		mockCreateJobInFolder: func(context.Context, string, string, ...string) (*jenkins.Job, error) { return nil, nil },
		mockGetJob:            func(context.Context, string, ...string) (*jenkins.Job, error) { return pjob, nil },
	}}}, pModel)

	// multibranch nested in a parent
	mModel := &multibranchPipelineResourceModel{
		Name: types.StringValue("m"), Folder: types.StringValue("team"), Description: types.StringValue("d"),
		Remote: types.StringValue("https://g/x.git"), CredentialsID: types.StringValue(""), ScriptPath: types.StringValue("Jenkinsfile"),
	}
	mcfg, err := mModel.configXML()
	if err != nil {
		t.Fatalf("multibranch configXML: %v", err)
	}
	mjob := covKJob(t, "/job/team/job/m", mcfg)
	covNestCreateRead(ctx, t, "multibranch nested", &multibranchPipelineResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockGetFolder:         okFolder,
		mockCreateJobInFolder: func(context.Context, string, string, ...string) (*jenkins.Job, error) { return nil, nil },
		mockGetJob:            func(context.Context, string, ...string) (*jenkins.Job, error) { return mjob, nil },
	}}}, mModel)
}
