package jenkins

import (
	"context"
	"fmt"
	"testing"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// covJCreate runs Create and asserts an error diagnostic (used for the
// "created but not read back" and invalid-folder branches).
func covJCreate(ctx context.Context, t *testing.T, label string, r resource.Resource, model any) {
	t.Helper()
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	plan := tfsdk.Plan{Schema: sr.Schema}
	if d := plan.Set(ctx, model); d.HasError() {
		t.Fatalf("%s plan.Set: %v", label, d)
	}
	resp := resource.CreateResponse{State: tfsdk.State{Schema: sr.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Errorf("%s Create: expected error", label)
	}
}

func TestCovJ_NotReadBackAndFolder(t *testing.T) {
	ctx := context.Background()
	notFound := func() error { return fmt.Errorf("404 not found") }

	// multibranch: created but GetJob 404 on read-back
	covJCreate(ctx, t, "multibranch not-read-back", &multibranchPipelineResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockCreateJobInFolder: func(context.Context, string, string, ...string) (*jenkins.Job, error) { return nil, nil },
		mockGetJob:            func(context.Context, string, ...string) (*jenkins.Job, error) { return nil, notFound() },
	}}}, &multibranchPipelineResourceModel{
		Name: types.StringValue("m"), Folder: types.StringNull(), Description: types.StringValue("d"),
		Remote: types.StringValue("https://g/x.git"), CredentialsID: types.StringValue(""), ScriptPath: types.StringValue("Jenkinsfile"),
	})

	// multibranch: invalid folder
	covJCreate(ctx, t, "multibranch bad-folder", &multibranchPipelineResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockGetFolder: func(context.Context, string, ...string) (*jenkins.Folder, error) { return nil, fmt.Errorf("boom") },
	}}}, &multibranchPipelineResourceModel{
		Name: types.StringValue("m"), Folder: types.StringValue("parent"), Description: types.StringValue("d"),
		Remote: types.StringValue("https://g/x.git"), CredentialsID: types.StringValue(""), ScriptPath: types.StringValue("Jenkinsfile"),
	})

	// pipeline: created but GetJob 404 on read-back
	covJCreate(ctx, t, "pipeline not-read-back", &pipelineJobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockCreateJobInFolder: func(context.Context, string, string, ...string) (*jenkins.Job, error) { return nil, nil },
		mockGetJob:            func(context.Context, string, ...string) (*jenkins.Job, error) { return nil, notFound() },
	}}}, &pipelineJobResourceModel{
		Name: types.StringValue("p"), Folder: types.StringNull(), Description: types.StringValue("d"),
		Script: types.StringValue("pipeline { agent any }"), Sandbox: types.BoolValue(true), Disabled: types.BoolValue(false),
	})

	// pipeline: invalid folder
	covJCreate(ctx, t, "pipeline bad-folder", &pipelineJobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockGetFolder: func(context.Context, string, ...string) (*jenkins.Folder, error) { return nil, fmt.Errorf("boom") },
	}}}, &pipelineJobResourceModel{
		Name: types.StringValue("p"), Folder: types.StringValue("parent"), Description: types.StringValue("d"),
		Script: types.StringValue("pipeline { agent any }"), Sandbox: types.BoolValue(true), Disabled: types.BoolValue(false),
	})

	// folder: invalid parent folder
	covJCreate(ctx, t, "folder bad-folder", &folderResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockGetFolder: func(context.Context, string, ...string) (*jenkins.Folder, error) { return nil, fmt.Errorf("boom") },
	}}}, &folderResourceModel{
		Name: types.StringValue("f"), Folder: types.StringValue("parent"),
		DisplayName: types.StringValue(""), Description: types.StringValue(""),
		Security: types.SetNull(folderSecurityObjectType), Template: types.StringNull(),
	})

	// user: created but GetUser reports not found on read-back
	covJCreate(ctx, t, "user not-read-back", &userResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockCreateUser: func(context.Context, string, string, string, string) error { return nil },
		mockGetUser:    func(context.Context, string, interface{}) error { return notFound() },
	}}}, &userResourceModel{
		Username: types.StringValue("u"), Password: types.StringValue("p"),
		FullName: types.StringValue("U"), Email: types.StringValue("u@e.com"),
	})
}
