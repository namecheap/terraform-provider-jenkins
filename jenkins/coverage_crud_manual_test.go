package jenkins

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// This file adds CRUD-harness unit tests for the resources whose framework
// Create/Read/Update/Delete methods were otherwise exercised only by acceptance
// tests (role, user, node, plugin, view, job). Each method is driven with the
// nil-safe mockJenkinsClient, asserting the happy path plus error / not-found
// branches.

func covMErr() error { return fmt.Errorf("boom") }

// covMResourceCRUD runs Create → Read → Update → Delete on a happy-path mock and
// asserts no diagnostics.
func covMResourceCRUD(ctx context.Context, t *testing.T, label string, r resource.Resource, s rschema.Schema, model any) {
	t.Helper()
	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(ctx, model); d.HasError() {
		t.Fatalf("%s plan.Set: %v", label, d)
	}
	cResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &cResp)
	if cResp.Diagnostics.HasError() {
		t.Errorf("%s Create: %v", label, cResp.Diagnostics)
	}

	st := tfsdk.State{Schema: s}
	if d := st.Set(ctx, model); d.HasError() {
		t.Fatalf("%s state.Set: %v", label, d)
	}
	rResp := resource.ReadResponse{State: tfsdk.State{Schema: s}}
	r.Read(ctx, resource.ReadRequest{State: st}, &rResp)
	if rResp.Diagnostics.HasError() {
		t.Errorf("%s Read: %v", label, rResp.Diagnostics)
	}

	uResp := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: st}, &uResp)
	if uResp.Diagnostics.HasError() {
		t.Errorf("%s Update: %v", label, uResp.Diagnostics)
	}

	dResp := resource.DeleteResponse{State: st}
	r.Delete(ctx, resource.DeleteRequest{State: st}, &dResp)
	if dResp.Diagnostics.HasError() {
		t.Errorf("%s Delete: %v", label, dResp.Diagnostics)
	}
}

// covMNull returns a typed null object for a schema, so ImportState's
// SetAttribute has an object to write into.
func covMNull(ctx context.Context, s rschema.Schema) tftypes.Value {
	return tftypes.NewValue(s.Type().TerraformType(ctx), nil)
}

func covMSchema(ctx context.Context, r resource.Resource) rschema.Schema {
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	return sr.Schema
}

// --- role ---

func TestCovM_Role(t *testing.T) {
	ctx := context.Background()
	okGet := func(_ context.Context, _, _ string, out interface{}) error {
		out.(*roleStrategyRoleResponse).PermissionIDs = map[string]bool{"hudson.model.Item.Build": true}
		return nil
	}
	model := &roleResourceModel{
		Type: types.StringValue("global"), Name: types.StringValue("reader"),
		Permissions: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("hudson.model.Item.Build")}),
		Assignments: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("authenticated")}),
	}
	r := &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockAddRole:    func(_ context.Context, _, _ string, _ []string, _ string, _ bool) error { return nil },
		mockAssignRole: func(_ context.Context, _, _, _ string) error { return nil },
		mockGetRole:    okGet,
		mockRemoveRole: func(_ context.Context, _, _ string) error { return nil },
	}}}
	s := covMSchema(ctx, r)
	covMResourceCRUD(ctx, t, "role", r, s, model)

	// Create error
	rErr := &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockAddRole: func(_ context.Context, _, _ string, _ []string, _ string, _ bool) error { return covMErr() },
	}}}
	plan := tfsdk.Plan{Schema: s}
	plan.Set(ctx, model)
	cResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	rErr.Create(ctx, resource.CreateRequest{Plan: plan}, &cResp)
	if !cResp.Diagnostics.HasError() {
		t.Error("role Create should error when AddRole fails")
	}

	// Read not-found (empty permissions) → removed
	rNF := &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockGetRole: func(_ context.Context, _, _ string, _ interface{}) error { return nil },
	}}}
	st := tfsdk.State{Schema: s}
	st.Set(ctx, model)
	rNF.Read(ctx, resource.ReadRequest{State: st}, &resource.ReadResponse{State: tfsdk.State{Schema: s}})

	// ImportState
	isResp := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: covMNull(ctx, s)}}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "global/reader"}, &isResp)
}

// --- user ---

func TestCovM_User(t *testing.T) {
	ctx := context.Background()
	okGet := func(_ context.Context, _ string, out interface{}) error {
		u := out.(*jenkinsUserResponse)
		u.ID = "alice"
		u.FullName = "Alice"
		u.Property = []jenkinsUserProperty{{Address: "alice@example.com"}}
		return nil
	}
	model := &userResourceModel{
		Username: types.StringValue("alice"), Password: types.StringValue("pw"),
		FullName: types.StringValue("Alice"), Email: types.StringValue("alice@example.com"),
	}
	r := &userResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockCreateUser: func(_ context.Context, _, _, _, _ string) error { return nil },
		mockGetUser:    okGet,
		mockDeleteUser: func(_ context.Context, _ string) error { return nil },
	}}}
	s := covMSchema(ctx, r)
	covMResourceCRUD(ctx, t, "user", r, s, model)

	// Create error
	rErr := &userResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockCreateUser: func(_ context.Context, _, _, _, _ string) error { return covMErr() },
	}}}
	plan := tfsdk.Plan{Schema: s}
	plan.Set(ctx, model)
	cResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	rErr.Create(ctx, resource.CreateRequest{Plan: plan}, &cResp)
	if !cResp.Diagnostics.HasError() {
		t.Error("user Create should error when CreateUser fails")
	}

	// Delete error
	rDel := &userResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockDeleteUser: func(_ context.Context, _ string) error { return covMErr() },
	}}}
	st := tfsdk.State{Schema: s}
	st.Set(ctx, model)
	dResp := resource.DeleteResponse{State: st}
	rDel.Delete(ctx, resource.DeleteRequest{State: st}, &dResp)
	if !dResp.Diagnostics.HasError() {
		t.Error("user Delete should error when DeleteUser fails")
	}

	// ImportState
	isResp := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: covMNull(ctx, s)}}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "alice"}, &isResp)
}

// --- node ---

func TestCovM_Node(t *testing.T) {
	ctx := context.Background()
	model := &nodeResourceModel{
		Name: types.StringValue("agent1"), NumExecutors: types.Int64Value(2),
		Description: types.StringValue("d"), RemoteFS: types.StringValue("/home/jenkins"),
		Labels: types.StringValue("linux"),
	}
	r := &nodeResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockCreateNode: func(_ context.Context, _ string, _ int, _, _, _ string, _ ...interface{}) (*jenkins.Node, error) {
			return nil, nil
		},
		mockGetNodeConfig: func(_ context.Context, _ string, _ interface{}) error { return nil },
		mockDeleteNode:    func(_ context.Context, _ string) (bool, error) { return true, nil },
	}}}
	s := covMSchema(ctx, r)
	covMResourceCRUD(ctx, t, "node", r, s, model)

	// Create error
	rErr := &nodeResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockCreateNode: func(_ context.Context, _ string, _ int, _, _, _ string, _ ...interface{}) (*jenkins.Node, error) {
			return nil, covMErr()
		},
	}}}
	plan := tfsdk.Plan{Schema: s}
	plan.Set(ctx, model)
	cResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	rErr.Create(ctx, resource.CreateRequest{Plan: plan}, &cResp)
	if !cResp.Diagnostics.HasError() {
		t.Error("node Create should error when CreateNode fails")
	}

	// Read not-found + error
	st := tfsdk.State{Schema: s}
	st.Set(ctx, model)
	for _, e := range []error{fmt.Errorf("404 node not found"), covMErr()} {
		rr := &nodeResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetNodeConfig: func(_ context.Context, _ string, _ interface{}) error { return e },
		}}}
		rr.Read(ctx, resource.ReadRequest{State: st}, &resource.ReadResponse{State: tfsdk.State{Schema: s}})
	}
}

// --- plugin ---

func TestCovM_Plugin(t *testing.T) {
	ctx := context.Background()
	model := &pluginResourceModel{
		Name: types.StringValue("git"), Version: types.StringValue("5.2.0"),
		UninstallOnDestroy: types.BoolValue(true),
	}
	has := func(_ context.Context, _ string) (*jenkins.Plugin, error) {
		return &jenkins.Plugin{Active: true, Version: "5.2.0", ShortName: "git"}, nil
	}
	r := &pluginResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockInstallPlugin:   func(_ context.Context, _, _ string) error { return nil },
		mockHasPlugin:       has,
		mockUninstallPlugin: func(_ context.Context, _ string) error { return nil },
	}}}
	s := covMSchema(ctx, r)
	covMResourceCRUD(ctx, t, "plugin", r, s, model)

	// Create error (not installed → InstallPlugin fails)
	rErr := &pluginResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockHasPlugin:     func(_ context.Context, _ string) (*jenkins.Plugin, error) { return nil, nil },
		mockInstallPlugin: func(_ context.Context, _, _ string) error { return covMErr() },
	}}}
	plan := tfsdk.Plan{Schema: s}
	plan.Set(ctx, model)
	cResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	rErr.Create(ctx, resource.CreateRequest{Plan: plan}, &cResp)
	if !cResp.Diagnostics.HasError() {
		t.Error("plugin Create should error when InstallPlugin fails")
	}

	// Delete error
	st := tfsdk.State{Schema: s}
	st.Set(ctx, model)
	rDel := &pluginResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockUninstallPlugin: func(_ context.Context, _ string) error { return covMErr() },
	}}}
	rDel.Delete(ctx, resource.DeleteRequest{State: st}, &resource.DeleteResponse{State: st})
}

// --- view ---

func TestCovM_View(t *testing.T) {
	ctx := context.Background()
	viewObj := &jenkins.View{Raw: &jenkins.ViewResponse{Name: "v", Description: "d", URL: "u"}}
	model := &ViewResourceModel{
		Name: types.StringValue("v"), Folder: types.StringNull(), Description: types.StringNull(),
		AssignedProjects: types.ListNull(types.StringType), URL: types.StringNull(),
	}
	newR := func(m *mockJenkinsClient) *ViewResource {
		return &ViewResource{resourceHelper: &resourceHelper{client: m}}
	}
	r := newR(&mockJenkinsClient{
		mockCreateView: func(_ context.Context, _, _ string) (*jenkins.View, error) { return viewObj, nil },
		mockGetView:    func(_ context.Context, _ string) (*jenkins.View, error) { return viewObj, nil },
		mockPostRequest: func(_ context.Context, _ string, _ io.Reader, _ interface{}, _ map[string]string) (*http.Response, error) {
			return &http.Response{StatusCode: 200}, nil
		},
	})
	s := covMSchema(ctx, r)

	plan := tfsdk.Plan{Schema: s}
	plan.Set(ctx, model)
	cResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &cResp)
	if cResp.Diagnostics.HasError() {
		t.Errorf("view Create happy errored: %v", cResp.Diagnostics)
	}

	st := tfsdk.State{Schema: s}
	st.Set(ctx, model)
	r.Read(ctx, resource.ReadRequest{State: st}, &resource.ReadResponse{State: tfsdk.State{Schema: s}})
	for _, e := range []error{fmt.Errorf("404 not found"), covMErr()} {
		rr := newR(&mockJenkinsClient{mockGetView: func(_ context.Context, _ string) (*jenkins.View, error) { return nil, e }})
		rr.Read(ctx, resource.ReadRequest{State: st}, &resource.ReadResponse{State: tfsdk.State{Schema: s}})
	}

	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: st}, &resource.UpdateResponse{State: tfsdk.State{Schema: s}})
	r.Delete(ctx, resource.DeleteRequest{State: st}, &resource.DeleteResponse{State: st})

	rDel := newR(&mockJenkinsClient{mockPostRequest: func(_ context.Context, _ string, _ io.Reader, _ interface{}, _ map[string]string) (*http.Response, error) {
		return nil, covMErr()
	}})
	dResp := resource.DeleteResponse{State: st}
	rDel.Delete(ctx, resource.DeleteRequest{State: st}, &dResp)
	if !dResp.Diagnostics.HasError() {
		t.Error("view Delete should error when PostRequest fails")
	}

	// Create error: CreateView fails and readback also fails
	origT, origI := createViewRetryTimeout, createViewRetryInterval
	createViewRetryTimeout, createViewRetryInterval = 0, 0
	rErr := newR(&mockJenkinsClient{
		mockCreateView: func(_ context.Context, _, _ string) (*jenkins.View, error) { return nil, covMErr() },
		mockGetView:    func(_ context.Context, _ string) (*jenkins.View, error) { return nil, covMErr() },
	})
	ceResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	rErr.Create(ctx, resource.CreateRequest{Plan: plan}, &ceResp)
	createViewRetryTimeout, createViewRetryInterval = origT, origI
	if !ceResp.Diagnostics.HasError() {
		t.Error("view Create should error when CreateView and readback fail")
	}
}

// --- job (error branches; happy Read needs a live gojenkins Job) ---

func TestCovM_Job(t *testing.T) {
	ctx := context.Background()
	jr := &jobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockCreateJobInFolder: func(_ context.Context, _, _ string, _ ...string) (*jenkins.Job, error) { return nil, covMErr() },
		mockDeleteJobInFolder: func(_ context.Context, _ string, _ ...string) (bool, error) { return true, nil },
		mockGetJob:            func(_ context.Context, _ string, _ ...string) (*jenkins.Job, error) { return nil, covMErr() },
	}}}
	s := covMSchema(ctx, jr)
	model := &jobResourceModel{Name: types.StringValue("j"), Folder: types.StringNull(), Template: types.StringValue("<x/>"), Disabled: types.BoolValue(false)}

	plan := tfsdk.Plan{Schema: s}
	plan.Set(ctx, model)
	jc := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	jr.Create(ctx, resource.CreateRequest{Plan: plan}, &jc)
	if !jc.Diagnostics.HasError() {
		t.Error("job Create should error when CreateJobInFolder fails")
	}

	st := tfsdk.State{Schema: s}
	st.Set(ctx, model)
	jr.Read(ctx, resource.ReadRequest{State: st}, &resource.ReadResponse{State: tfsdk.State{Schema: s}})
	jr.Delete(ctx, resource.DeleteRequest{State: st}, &resource.DeleteResponse{State: st})

	// Delete error
	jrDel := &jobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockDeleteJobInFolder: func(_ context.Context, _ string, _ ...string) (bool, error) { return false, covMErr() },
	}}}
	jde := resource.DeleteResponse{State: st}
	jrDel.Delete(ctx, resource.DeleteRequest{State: st}, &jde)
}

// --- folder (error branches; happy paths need a live gojenkins Job) ---

func TestCovM_Folder(t *testing.T) {
	ctx := context.Background()
	fr := &folderResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockGetFolder:         func(_ context.Context, _ string, _ ...string) (*jenkins.Folder, error) { return nil, covMErr() },
		mockCreateJobInFolder: func(_ context.Context, _, _ string, _ ...string) (*jenkins.Job, error) { return nil, covMErr() },
		mockGetJob:            func(_ context.Context, _ string, _ ...string) (*jenkins.Job, error) { return nil, covMErr() },
		mockDeleteJobInFolder: func(_ context.Context, _ string, _ ...string) (bool, error) { return true, nil },
	}}}
	s := covMSchema(ctx, fr)
	model := &folderResourceModel{
		Name: types.StringValue("f"), Folder: types.StringNull(),
		DisplayName: types.StringValue(""), Description: types.StringValue(""),
		Security: types.SetNull(folderSecurityObjectType), Template: types.StringNull(),
	}

	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(ctx, model); d.HasError() {
		t.Fatalf("folder plan.Set: %v", d)
	}
	fc := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	fr.Create(ctx, resource.CreateRequest{Plan: plan}, &fc)
	if !fc.Diagnostics.HasError() {
		t.Error("folder Create should error when CreateJobInFolder fails")
	}

	st := tfsdk.State{Schema: s}
	st.Set(ctx, model)
	fr.Read(ctx, resource.ReadRequest{State: st}, &resource.ReadResponse{State: tfsdk.State{Schema: s}})

	uResp := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	fr.Update(ctx, resource.UpdateRequest{Plan: plan, State: st}, &uResp)
	if !uResp.Diagnostics.HasError() {
		t.Error("folder Update should error when GetJob fails")
	}

	fr.Delete(ctx, resource.DeleteRequest{State: st}, &resource.DeleteResponse{State: st})

	frDel := &folderResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockDeleteJobInFolder: func(_ context.Context, _ string, _ ...string) (bool, error) { return false, covMErr() },
	}}}
	fdResp := resource.DeleteResponse{State: st}
	frDel.Delete(ctx, resource.DeleteRequest{State: st}, &fdResp)
}
