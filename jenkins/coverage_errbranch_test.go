package jenkins

import (
	"context"
	"fmt"
	"testing"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Error-branch coverage for resource CRUD methods (mock returns errors), plus a
// live-client harness for the credential CRUD (credential_crud.go + certificate)
// so their gojenkins CredentialsManager paths are exercised without a refactor.

func covEBoom() error { return fmt.Errorf("boom") }

func covEUpdateErr(ctx context.Context, t *testing.T, r resource.Resource, model any) {
	t.Helper()
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	plan := tfsdk.Plan{Schema: sr.Schema}
	plan.Set(ctx, model)
	st := tfsdk.State{Schema: sr.Schema}
	st.Set(ctx, model)
	resp := resource.UpdateResponse{State: tfsdk.State{Schema: sr.Schema}}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: st}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Error("expected Update error")
	}
}

func covEDeleteErr(ctx context.Context, t *testing.T, r resource.Resource, model any) {
	t.Helper()
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	st := tfsdk.State{Schema: sr.Schema}
	st.Set(ctx, model)
	resp := resource.DeleteResponse{State: st}
	r.Delete(ctx, resource.DeleteRequest{State: st}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Error("expected Delete error")
	}
}

func covEReadErr(ctx context.Context, t *testing.T, r resource.Resource, model any) {
	t.Helper()
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	st := tfsdk.State{Schema: sr.Schema}
	st.Set(ctx, model)
	resp := resource.ReadResponse{State: tfsdk.State{Schema: sr.Schema}}
	r.Read(ctx, resource.ReadRequest{State: st}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Error("expected Read error")
	}
}

func TestCovE_RoleErrors(t *testing.T) {
	ctx := context.Background()
	model := &roleResourceModel{
		Type: types.StringValue("global"), Name: types.StringValue("r"),
		Permissions: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("p")}),
		Assignments: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("u")}),
	}
	// Update error (AddRole overwrite fails)
	covEUpdateErr(ctx, t, &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockAddRole: func(context.Context, string, string, []string, string, bool) error { return covEBoom() },
	}}}, model)
	// Update error (assign fails after AddRole ok)
	covEUpdateErr(ctx, t, &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockAddRole:    func(context.Context, string, string, []string, string, bool) error { return nil },
		mockAssignRole: func(context.Context, string, string, string) error { return covEBoom() },
	}}}, model)
	// Delete error
	covEDeleteErr(ctx, t, &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockRemoveRole: func(context.Context, string, string) error { return covEBoom() },
	}}}, model)
	// Read error (GetRole returns a real error)
	covEReadErr(ctx, t, &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockGetRole: func(context.Context, string, string, interface{}) error { return covEBoom() },
	}}}, model)
	// ImportState bad id
	var sr resource.SchemaResponse
	(&roleResource{resourceHelper: newResourceHelper()}).Schema(ctx, resource.SchemaRequest{}, &sr)
	isr := resource.ImportStateResponse{State: tfsdk.State{Schema: sr.Schema, Raw: covMNull(ctx, sr.Schema)}}
	(&roleResource{resourceHelper: newResourceHelper()}).ImportState(ctx, resource.ImportStateRequest{ID: "noslash"}, &isr)
	if !isr.Diagnostics.HasError() {
		t.Error("role ImportState should reject id without a slash")
	}
	// validate: item role missing pattern default path + agent
	for _, ty := range []string{"item", "agent"} {
		m := *model
		m.Type = types.StringValue(ty)
		m.Pattern = types.StringNull()
		covEUpdateErr(ctx, t, &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockAddRole: func(context.Context, string, string, []string, string, bool) error { return covEBoom() },
		}}}, &m)
	}
}

func TestCovE_UserErrors(t *testing.T) {
	ctx := context.Background()
	model := &userResourceModel{
		Username: types.StringValue("u"), Password: types.StringValue("p"),
		FullName: types.StringValue("U"), Email: types.StringValue("u@e.com"),
	}
	// Update error (GetUser fails during populate)
	covEUpdateErr(ctx, t, &userResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockGetUser: func(context.Context, string, interface{}) error { return covEBoom() },
	}}}, model)
	// Read error
	covEReadErr(ctx, t, &userResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockGetUser: func(context.Context, string, interface{}) error { return covEBoom() },
	}}}, model)
	// Read not-found (404) → resource removed (no error)
	rr := &userResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockGetUser: func(context.Context, string, interface{}) error { return fmt.Errorf("404 not found") },
	}}}
	var sr resource.SchemaResponse
	rr.Schema(ctx, resource.SchemaRequest{}, &sr)
	st := tfsdk.State{Schema: sr.Schema}
	st.Set(ctx, model)
	rr.Read(ctx, resource.ReadRequest{State: st}, &resource.ReadResponse{State: tfsdk.State{Schema: sr.Schema}})
}

func TestCovE_PluginErrors(t *testing.T) {
	ctx := context.Background()
	model := &pluginResourceModel{Name: types.StringValue("git"), Version: types.StringValue("1.0"), UninstallOnDestroy: types.BoolValue(true)}
	// Create error: HasPlugin fails
	var sr resource.SchemaResponse
	(&pluginResource{resourceHelper: newResourceHelper()}).Schema(ctx, resource.SchemaRequest{}, &sr)
	plan := tfsdk.Plan{Schema: sr.Schema}
	plan.Set(ctx, model)
	rc := &pluginResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockHasPlugin: func(context.Context, string) (*jenkins.Plugin, error) { return nil, covEBoom() },
	}}}
	cResp := resource.CreateResponse{State: tfsdk.State{Schema: sr.Schema}}
	rc.Create(ctx, resource.CreateRequest{Plan: plan}, &cResp)
	if !cResp.Diagnostics.HasError() {
		t.Error("plugin Create should error when HasPlugin fails")
	}
	// Read error
	covEReadErr(ctx, t, &pluginResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockHasPlugin: func(context.Context, string) (*jenkins.Plugin, error) { return nil, covEBoom() },
	}}}, model)
	// Update error: version changes → InstallPlugin fails
	ru := &pluginResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockHasPlugin:     func(context.Context, string) (*jenkins.Plugin, error) { return nil, nil },
		mockInstallPlugin: func(context.Context, string, string) error { return covEBoom() },
	}}}
	planM := &pluginResourceModel{Name: types.StringValue("git"), Version: types.StringValue("2.0"), UninstallOnDestroy: types.BoolValue(true)}
	stateM := &pluginResourceModel{Name: types.StringValue("git"), Version: types.StringValue("1.0"), UninstallOnDestroy: types.BoolValue(true)}
	plan2 := tfsdk.Plan{Schema: sr.Schema}
	plan2.Set(ctx, planM)
	st2 := tfsdk.State{Schema: sr.Schema}
	st2.Set(ctx, stateM)
	uResp := resource.UpdateResponse{State: tfsdk.State{Schema: sr.Schema}}
	ru.Update(ctx, resource.UpdateRequest{Plan: plan2, State: st2}, &uResp)
	if !uResp.Diagnostics.HasError() {
		t.Error("plugin Update should error when InstallPlugin fails on version change")
	}
}

func TestCovE_NodeCascErrors(t *testing.T) {
	ctx := context.Background()
	// node Delete error
	covEDeleteErr(ctx, t, &nodeResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockDeleteNode: func(context.Context, string) (bool, error) { return false, covEBoom() },
	}}}, &nodeResourceModel{
		Name: types.StringValue("n"), NumExecutors: types.Int64Value(1),
		Description: types.StringValue(""), RemoteFS: types.StringValue("/x"), Labels: types.StringValue(""),
	})
	// casc Update error (ApplyCASC fails) + Read export error
	cascModel := &configurationAsCodeResourceModel{Section: types.StringValue("jenkins"), YAML: types.StringValue("systemMessage: hi")}
	covEUpdateErr(ctx, t, &configurationAsCodeResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockApplyCASC: func(context.Context, string) error { return covEBoom() },
	}}}, cascModel)
	covEReadErr(ctx, t, &configurationAsCodeResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockExportCASC: func(context.Context) (string, error) { return "", covEBoom() },
	}}}, cascModel)
}
