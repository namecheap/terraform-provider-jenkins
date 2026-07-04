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

// Parse-error and not-found branches: feed malformed config XML (so parseFolder /
// configXML unmarshalling fails) and 404s on read-back.

func covPRead(ctx context.Context, t *testing.T, r resource.Resource, model any) resource.ReadResponse {
	t.Helper()
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	st := tfsdk.State{Schema: sr.Schema}
	st.Set(ctx, model)
	resp := resource.ReadResponse{State: tfsdk.State{Schema: sr.Schema}}
	r.Read(ctx, resource.ReadRequest{State: st}, &resp)
	return resp
}

func TestCovP_ParseAndNotFound(t *testing.T) {
	ctx := context.Background()
	badJob := func(base string) *mockJenkinsClient {
		job := covKJob(t, base, "<broken")
		return &mockJenkinsClient{mockGetJob: func(context.Context, string, ...string) (*jenkins.Job, error) { return job, nil }}
	}
	nf := &mockJenkinsClient{mockGetJob: func(context.Context, string, ...string) (*jenkins.Job, error) {
		return nil, fmt.Errorf("404 not found")
	}}

	// folder Read: malformed config → parseFolder error
	fModel := &folderResourceModel{
		Name: types.StringValue("f"), Folder: types.StringNull(),
		DisplayName: types.StringValue(""), Description: types.StringValue(""),
		Security: types.SetNull(folderSecurityObjectType), Template: types.StringNull(),
	}
	if resp := covPRead(ctx, t, &folderResource{resourceHelper: &resourceHelper{client: badJob("/job/f")}}, fModel); !resp.Diagnostics.HasError() {
		t.Error("folder Read should error on malformed config")
	}
	// folder Read: not found → resource removed (no error)
	covPRead(ctx, t, &folderResource{resourceHelper: &resourceHelper{client: nf}}, fModel)

	// folder Update: malformed config → parseFolder error
	{
		r := &folderResource{resourceHelper: &resourceHelper{client: badJob("/job/f")}}
		var sr resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &sr)
		plan := tfsdk.Plan{Schema: sr.Schema}
		plan.Set(ctx, fModel)
		st := tfsdk.State{Schema: sr.Schema}
		st.Set(ctx, fModel)
		uResp := resource.UpdateResponse{State: tfsdk.State{Schema: sr.Schema}}
		r.Update(ctx, resource.UpdateRequest{Plan: plan, State: st}, &uResp)
		if !uResp.Diagnostics.HasError() {
			t.Error("folder Update should error on malformed config")
		}
	}

	// pipeline Read: malformed config → populate parse error
	pModel := &pipelineJobResourceModel{
		Name: types.StringValue("p"), Folder: types.StringNull(), Description: types.StringValue("d"),
		Script: types.StringValue("x"), Sandbox: types.BoolValue(true), Disabled: types.BoolValue(false),
	}
	if resp := covPRead(ctx, t, &pipelineJobResource{resourceHelper: &resourceHelper{client: badJob("/job/p")}}, pModel); !resp.Diagnostics.HasError() {
		t.Error("pipeline Read should error on malformed config")
	}
	covPRead(ctx, t, &pipelineJobResource{resourceHelper: &resourceHelper{client: nf}}, pModel)

	// multibranch Read: malformed config → populate parse error
	mModel := &multibranchPipelineResourceModel{
		Name: types.StringValue("m"), Folder: types.StringNull(), Description: types.StringValue("d"),
		Remote: types.StringValue("https://g/x.git"), CredentialsID: types.StringValue(""), ScriptPath: types.StringValue("Jenkinsfile"),
	}
	if resp := covPRead(ctx, t, &multibranchPipelineResource{resourceHelper: &resourceHelper{client: badJob("/job/m")}}, mModel); !resp.Diagnostics.HasError() {
		t.Error("multibranch Read should error on malformed config")
	}
	covPRead(ctx, t, &multibranchPipelineResource{resourceHelper: &resourceHelper{client: nf}}, mModel)
}
