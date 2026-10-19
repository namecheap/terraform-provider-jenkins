package jenkins

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
)

// TestRoleCreateExistingRole is the regression test for #247: addRole without
// overwrite is a no-op on an existing role, so a Create that went ahead would
// assign the configured SIDs to that role, granting its permissions rather
// than the planned ones, and a later replace would delete it. Create must
// refuse, leave the role exactly as it was, and save no state.
func TestRoleCreateExistingRole(t *testing.T) {
	ctx := context.Background()
	administer := []string{"hudson.model.Hudson.Administer"}
	read := []string{"hudson.model.Hudson.Read"}

	tests := []struct {
		name  string
		seed  func(f *roleStrategyFake)
		model *roleResourceModel
	}{
		{
			// The reproduction from #247: the admin role from
			// integration/jenkins.yaml, configured as a read-only role.
			name: "global role with permissions",
			seed: func(f *roleStrategyFake) {
				f.put("globalRoles", "admin", "", administer,
					roleEntry{Type: roleEntryUser, Sid: "admin"},
					roleEntry{Type: roleEntryUser, Sid: "tf"},
				)
			},
			model: roleFakeModel("global", "admin", "", read, nil, nil, []string{"dev"}),
		},
		{
			// getRole answers "permissionIds":{} for a role without
			// permissions; it exists all the same.
			name: "role without permissions",
			seed: func(f *roleStrategyFake) {
				f.put("globalRoles", "empty", "", nil, roleEntry{Type: roleEntryUser, Sid: "bob"})
			},
			model: roleFakeModel("global", "empty", "", read, []string{"alice"}, nil, nil),
		},
		{
			name: "item role with entries of every type",
			seed: func(f *roleStrategyFake) {
				f.put("projectRoles", "ops", "ops-.*", []string{"hudson.model.Item.Configure"},
					roleEntry{Type: roleEntryUser, Sid: "alice"},
					roleEntry{Type: roleEntryGroup, Sid: "ops"},
					roleEntry{Type: roleEntryEither, Sid: "carol"},
				)
			},
			model: roleFakeModel("item", "ops", "ops-.*", []string{"hudson.model.Item.Read"}, []string{"alice"}, nil, nil),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newRoleStrategyFake()
			tt.seed(f)
			apiType := roleTypeAPI[tt.model.Type.ValueString()]
			name := tt.model.Name.ValueString()
			before := f.role(t, apiType, name)

			r, s := f.resource(ctx)
			plan := tfsdk.Plan{Schema: s}
			if d := plan.Set(ctx, tt.model); d.HasError() {
				t.Fatalf("plan.Set: %v", d)
			}
			resp := resource.CreateResponse{State: tfsdk.State{Schema: s, Raw: covMNull(ctx, s)}}
			r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)

			var summaries []string
			for _, d := range resp.Diagnostics.Errors() {
				summaries = append(summaries, d.Summary())
			}
			if want := []string{"Role Already Exists"}; !reflect.DeepEqual(summaries, want) {
				t.Errorf("error summaries = %v, want %v", summaries, want)
			}
			if !resp.State.Raw.IsNull() {
				t.Error("state saved for a role Create refused, want none")
			}
			if after := f.role(t, apiType, name); !reflect.DeepEqual(after, before) {
				t.Errorf("existing role changed:\n got %+v\nwant %+v", after, before)
			}
		})
	}
}

// TestRoleCreateNewRole is the benign control for TestRoleCreateExistingRole:
// a role that does not exist yet, including one whose name is taken by a role
// of another type, is created with exactly the planned permissions and
// entries.
func TestRoleCreateNewRole(t *testing.T) {
	ctx := context.Background()
	f := newRoleStrategyFake()
	administer := []string{"hudson.model.Hudson.Administer"}
	f.put("globalRoles", "admin", "", administer, roleEntry{Type: roleEntryUser, Sid: "admin"})
	globalBefore := f.role(t, "globalRoles", "admin")

	r, s := f.resource(ctx)
	model := roleFakeModel("item", "admin", "dev-.*", []string{"hudson.model.Item.Read"}, []string{"dev"}, []string{"devs"}, nil)
	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(ctx, model); d.HasError() {
		t.Fatalf("plan.Set: %v", d)
	}
	resp := resource.CreateResponse{State: tfsdk.State{Schema: s, Raw: covMNull(ctx, s)}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}

	want := roleStrategyRoleResponse{
		PermissionIDs: map[string]bool{"hudson.model.Item.Read": true},
		Pattern:       "dev-.*",
		Sids:          []roleEntry{{Type: roleEntryGroup, Sid: "devs"}, {Type: roleEntryUser, Sid: "dev"}},
	}
	if got := f.role(t, "projectRoles", "admin"); !reflect.DeepEqual(got, want) {
		t.Errorf("created role:\n got %+v\nwant %+v", got, want)
	}
	if got := f.role(t, "globalRoles", "admin"); !reflect.DeepEqual(got, globalBefore) {
		t.Errorf("global role of the same name changed:\n got %+v\nwant %+v", got, globalBefore)
	}

	if got := checkRoleFakeState(t, resp.State, model); got.ID.ValueString() != "item/admin" {
		t.Errorf("id = %q, want item/admin", got.ID.ValueString())
	}
}
