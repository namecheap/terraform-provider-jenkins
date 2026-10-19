package jenkins

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"testing"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
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
	// The role exists once AddRole ran, so Create's existence check passes.
	var created bool
	okGet := func(_ context.Context, _, _ string, out interface{}) error {
		if created {
			out.(*roleStrategyRoleResponse).PermissionIDs = map[string]bool{"hudson.model.Item.Build": true}
		}
		return nil
	}
	model := &roleResourceModel{
		Type: types.StringValue("global"), Name: types.StringValue("reader"),
		Permissions: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("hudson.model.Item.Build")}),
		Users:       types.SetValueMust(types.StringType, []attr.Value{}),
		Groups:      types.SetValueMust(types.StringType, []attr.Value{types.StringValue("authenticated")}),
		Assignments: types.SetValueMust(types.StringType, []attr.Value{}),
	}
	r := &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockAddRole:    func(_ context.Context, _, _ string, _ []string, _ string, _ bool) error { created = true; return nil },
		mockAssignRole: func(_ context.Context, _, _ string, _ roleEntry) error { return nil },
		mockGetRole:    okGet,
		mockRemoveRole: func(_ context.Context, _, _ string) error { return nil },
	}}}
	s := covMSchema(ctx, r)
	covMResourceCRUD(ctx, t, "role", r, s, model)

	// Create refuses a role that already exists instead of taking it over.
	var adopted bool
	rExists := &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockGetRole: func(_ context.Context, _, _ string, out interface{}) error {
			out.(*roleStrategyRoleResponse).PermissionIDs = map[string]bool{"hudson.model.Hudson.Administer": true}
			return nil
		},
		mockAddRole:      func(context.Context, string, string, []string, string, bool) error { adopted = true; return nil },
		mockUnassignRole: func(context.Context, string, string, roleEntry) error { adopted = true; return nil },
	}}}
	existsPlan := tfsdk.Plan{Schema: s}
	existsPlan.Set(ctx, model)
	existsResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	rExists.Create(ctx, resource.CreateRequest{Plan: existsPlan}, &existsResp)
	if !existsResp.Diagnostics.HasError() || adopted {
		t.Errorf("role Create on an existing role: diags=%v, modified=%v; want an error and no change", existsResp.Diagnostics, adopted)
	}

	// role-strategy allows a role with entries but no permissions, and getRole
	// answers "permissionIds":{} for it but {} for a missing role. Create must
	// treat it as existing, and Read must keep it in state.
	zeroPerms := func(_ context.Context, _, _ string, out interface{}) error {
		return json.Unmarshal([]byte(`{"permissionIds":{},"sids":[{"type":"USER","sid":"bob"}]}`), out)
	}
	var stripped bool
	rZero := &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockGetRole:      zeroPerms,
		mockAddRole:      func(context.Context, string, string, []string, string, bool) error { stripped = true; return nil },
		mockUnassignRole: func(context.Context, string, string, roleEntry) error { stripped = true; return nil },
		mockRemoveRole:   func(context.Context, string, string) error { stripped = true; return nil },
	}}}
	zeroResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	rZero.Create(ctx, resource.CreateRequest{Plan: existsPlan}, &zeroResp)
	if !zeroResp.Diagnostics.HasError() || stripped {
		t.Errorf("role Create on an existing zero-permission role: diags=%v, modified=%v; want an error and no change", zeroResp.Diagnostics, stripped)
	}
	zeroState := tfsdk.State{Schema: s}
	zeroState.Set(ctx, model)
	zeroRead := resource.ReadResponse{State: zeroState}
	rZero.Read(ctx, resource.ReadRequest{State: zeroState}, &zeroRead)
	if zeroRead.Diagnostics.HasError() || zeroRead.State.Raw.IsNull() {
		t.Errorf("role Read of a zero-permission role: diags=%v, removed=%v; want it kept", zeroRead.Diagnostics, zeroRead.State.Raw.IsNull())
	}

	// Create stops when the existence check itself fails.
	var added bool
	rGetErr := &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockGetRole: func(context.Context, string, string, interface{}) error { return covMErr() },
		mockAddRole: func(context.Context, string, string, []string, string, bool) error { added = true; return nil },
	}}}
	getErrResp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	rGetErr.Create(ctx, resource.CreateRequest{Plan: existsPlan}, &getErrResp)
	if !getErrResp.Diagnostics.HasError() || added {
		t.Errorf("role Create with a failing existence check: diags=%v, added=%v; want an error and no addRole", getErrResp.Diagnostics, added)
	}

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

// TestCovM_RoleUpdateOverwrite checks that Update only overwrites the role when
// its permissions or pattern change: role-strategy releases before 927 drop
// every entry of an overwritten role.
func TestCovM_RoleCreateRollback(t *testing.T) {
	ctx := context.Background()
	model := &roleResourceModel{
		Type: types.StringValue("global"), Name: types.StringValue("reader"),
		Permissions: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("hudson.model.Hudson.Read")}),
		Users:       types.SetValueMust(types.StringType, []attr.Value{types.StringValue("alice")}),
		Groups:      types.SetValueMust(types.StringType, []attr.Value{}),
		Assignments: types.SetValueMust(types.StringType, []attr.Value{}),
	}

	tests := []struct {
		name            string
		assignErr       error
		readBackErr     error // returned by populate's GetRole, the second after addRole
		readBackMissing bool  // populate's GetRole finds no role
		removeErr       error
		wantRollback    bool
		wantState       bool
		wantDiags       []string
	}{
		{name: "success keeps the role", wantState: true},
		{
			name:         "reconcile failure deletes the role",
			assignErr:    covMErr(),
			wantRollback: true,
			wantDiags:    []string{"Unable to Assign Role"},
		},
		{
			// The role is correct, only reading it failed: keep it and save
			// state, so Terraform taints it instead of the role being deleted.
			name:        "read-back failure keeps the role",
			readBackErr: covMErr(),
			wantState:   true,
			wantDiags:   []string{"Unable to Refresh Resource"},
		},
		{
			name:            "role missing on read-back deletes it",
			readBackMissing: true,
			wantRollback:    true,
			wantDiags:       []string{"Unable to Read Created Resource"},
		},
		{
			name:         "failed rollback reports the orphan",
			assignErr:    covMErr(),
			removeErr:    covMErr(),
			wantRollback: true,
			wantDiags:    []string{"Unable to Assign Role", "Role Left Orphaned After Failed Create"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var created, removed bool
			var reads int
			r := &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
				mockGetRole: func(_ context.Context, _, _ string, out interface{}) error {
					if !created {
						return nil
					}
					if reads++; reads == 2 && tt.readBackErr != nil {
						return tt.readBackErr
					}
					if reads == 2 && tt.readBackMissing {
						return nil
					}
					resp := out.(*roleStrategyRoleResponse)
					resp.PermissionIDs = map[string]bool{"hudson.model.Hudson.Read": true}
					if !tt.wantRollback {
						resp.Sids = []roleEntry{{Type: roleEntryUser, Sid: "alice"}}
					}
					return nil
				},
				mockAddRole:    func(context.Context, string, string, []string, string, bool) error { created = true; return nil },
				mockAssignRole: func(context.Context, string, string, roleEntry) error { return tt.assignErr },
				mockRemoveRole: func(context.Context, string, string) error { removed = true; return tt.removeErr },
			}}}
			s := covMSchema(ctx, r)
			plan := tfsdk.Plan{Schema: s}
			plan.Set(ctx, model)
			resp := resource.CreateResponse{State: tfsdk.State{Schema: s, Raw: covMNull(ctx, s)}}
			r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

			if removed != tt.wantRollback {
				t.Errorf("RemoveRole called = %v, want %v", removed, tt.wantRollback)
			}
			if got := !resp.State.Raw.IsNull(); got != tt.wantState {
				t.Errorf("state saved = %v, want %v", got, tt.wantState)
			}
			var got []string
			for _, d := range resp.Diagnostics.Errors() {
				got = append(got, d.Summary())
			}
			if !reflect.DeepEqual(got, tt.wantDiags) {
				t.Errorf("error summaries = %v, want %v", got, tt.wantDiags)
			}
		})
	}
}

func TestCovM_RoleUpdateOverwrite(t *testing.T) {
	ctx := context.Background()
	strs := func(v ...string) types.Set {
		vals := make([]attr.Value, 0, len(v))
		for _, s := range v {
			vals = append(vals, types.StringValue(s))
		}
		return types.SetValueMust(types.StringType, vals)
	}
	model := func(pattern string, perms, users types.Set) *roleResourceModel {
		return &roleResourceModel{
			Type: types.StringValue("item"), Name: types.StringValue("dev"), Pattern: types.StringValue(pattern),
			Permissions: perms, Users: users, Groups: strs(), Assignments: strs(),
		}
	}
	build, read := strs("hudson.model.Item.Build"), strs("hudson.model.Item.Read")

	tests := []struct {
		name          string
		prior, plan   *roleResourceModel
		addRoleErr    error
		wantOverwrite bool
		wantErr       bool
	}{
		{
			name:  "entries only",
			prior: model("team/.*", build, strs("alice")),
			plan:  model("team/.*", build, strs("bob")),
		},
		{
			// ".*" is what rolePatternPlanModifier plans for an item role
			// without a configured pattern.
			name:  "unset pattern, entries only",
			prior: model(".*", build, strs("alice")),
			plan:  model(".*", build, strs("bob")),
		},
		{
			name:          "permissions change",
			prior:         model("team/.*", build, strs("alice")),
			plan:          model("team/.*", read, strs("alice")),
			wantOverwrite: true,
		},
		{
			name:          "pattern change",
			prior:         model("team/.*", build, strs("alice")),
			plan:          model("other/.*", build, strs("alice")),
			wantOverwrite: true,
		},
		{
			name:          "overwrite error",
			prior:         model("team/.*", build, strs("alice")),
			plan:          model("team/.*", read, strs("alice")),
			addRoleErr:    covMErr(),
			wantOverwrite: true,
			wantErr:       true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var overwritten bool
			r := &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
				mockAddRole: func(_ context.Context, _, _ string, _ []string, _ string, _ bool) error {
					overwritten = true
					return tt.addRoleErr
				},
				mockGetRole: func(_ context.Context, _, _ string, out interface{}) error {
					out.(*roleStrategyRoleResponse).PermissionIDs = map[string]bool{"hudson.model.Item.Build": true}
					return nil
				},
			}}}
			s := covMSchema(ctx, r)
			plan := tfsdk.Plan{Schema: s}
			plan.Set(ctx, tt.plan)
			st := tfsdk.State{Schema: s}
			st.Set(ctx, tt.prior)
			resp := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
			r.Update(ctx, resource.UpdateRequest{Plan: plan, State: st}, &resp)
			if resp.Diagnostics.HasError() != tt.wantErr {
				t.Errorf("Update diagnostics = %v, want error %v", resp.Diagnostics, tt.wantErr)
			}
			if overwritten != tt.wantOverwrite {
				t.Errorf("AddRole called = %v, want %v", overwritten, tt.wantOverwrite)
			}
		})
	}
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
		mockGetNodeConfig:    func(_ context.Context, _ string, _ interface{}) error { return nil },
		mockUpdateNodeConfig: func(_ context.Context, _ string, _ []byte) error { return nil },
		mockDeleteNode:       func(_ context.Context, _ string) (bool, error) { return true, nil },
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

func TestRolePatternPlanModifier(t *testing.T) {
	ctx := context.Background()
	r := &roleResource{resourceHelper: &resourceHelper{}}
	s := covMSchema(ctx, r)

	tests := []struct {
		name     string
		roleType tftypes.Value
		config   types.String
		want     types.String
	}{
		{"item without pattern", tftypes.NewValue(tftypes.String, "item"), types.StringNull(), types.StringValue(".*")},
		{"agent without pattern", tftypes.NewValue(tftypes.String, "agent"), types.StringNull(), types.StringValue(".*")},
		{"global without pattern", tftypes.NewValue(tftypes.String, "global"), types.StringNull(), types.StringNull()},
		{"configured pattern kept", tftypes.NewValue(tftypes.String, "item"), types.StringValue("team/.*"), types.StringValue("team/.*")},
		{"unknown type left unknown", tftypes.NewValue(tftypes.String, tftypes.UnknownValue), types.StringNull(), types.StringUnknown()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objType := s.Type().TerraformType(ctx).(tftypes.Object)
			vals := map[string]tftypes.Value{}
			for k, at := range objType.AttributeTypes {
				vals[k] = tftypes.NewValue(at, nil)
			}
			vals["type"] = tt.roleType
			plan := tfsdk.Plan{Schema: s, Raw: tftypes.NewValue(objType, vals)}

			planValue := tt.config
			if tt.config.IsNull() {
				planValue = types.StringUnknown()
			}
			req := planmodifier.StringRequest{Plan: plan, ConfigValue: tt.config, PlanValue: planValue}
			resp := &planmodifier.StringResponse{PlanValue: planValue}
			rolePatternPlanModifier{}.PlanModifyString(ctx, req, resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("diagnostics: %v", resp.Diagnostics)
			}
			if !resp.PlanValue.Equal(tt.want) {
				t.Errorf("plan = %v, want %v", resp.PlanValue, tt.want)
			}
		})
	}
}

func TestRolePatternValidators(t *testing.T) {
	ctx := context.Background()
	s := covMSchema(ctx, &roleResource{resourceHelper: &resourceHelper{}})
	patternAttr := s.Attributes["pattern"].(rschema.StringAttribute)

	tests := []struct {
		name    string
		value   types.String
		wantErr bool
	}{
		// role-strategy stores "" as ".*", so the apply would end in an
		// inconsistent result.
		{"empty", types.StringValue(""), true},
		{"pattern", types.StringValue("team/.*"), false},
		{"unset", types.StringNull(), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validator.StringRequest{Path: path.Root("pattern"), ConfigValue: tt.value}
			resp := &validator.StringResponse{}
			for _, v := range patternAttr.Validators {
				v.ValidateString(ctx, req, resp)
			}
			if got := resp.Diagnostics.HasError(); got != tt.wantErr {
				t.Errorf("error = %v, want %v: %v", got, tt.wantErr, resp.Diagnostics)
			}
		})
	}
}
