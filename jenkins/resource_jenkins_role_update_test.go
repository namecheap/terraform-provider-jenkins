package jenkins

import (
	"context"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
)

// TestRoleUpdateReconcilesEntries is the regression test for #248: from
// role-strategy 918.v91e5468d8db_2 on, addRole with overwrite keeps the role's
// entries, so an Update that relied on the overwrite to drop them never
// revoked a removed member. Update must leave the role with exactly the
// planned entries, whether or not it overwrites the role and whichever
// overwrite semantics the plugin has, and read back the plan, so Terraform
// reports no inconsistent result and a refresh plans no further change.
func TestRoleUpdateReconcilesEntries(t *testing.T) {
	ctx := context.Background()
	read := []string{"hudson.model.Item.Read"}
	readConfigure := []string{"hudson.model.Item.Read", "hudson.model.Item.Configure"}
	adminRead := []string{"hudson.model.Hudson.Read"}

	tests := []struct {
		name        string
		prior, plan *roleResourceModel
		unmanaged   []roleEntry // added outside Terraform since the last refresh
	}{
		{
			// The reproduction from #248.
			name:  "remove the only member",
			prior: roleFakeModel("item", "ops-repo", "ops-.*", readConfigure, nil, nil, []string{"dev"}),
			plan:  roleFakeModel("item", "ops-repo", "ops-.*", readConfigure, nil, nil, nil),
		},
		{
			name:  "remove one user",
			prior: roleFakeModel("global", "readers", "", adminRead, []string{"alice", "bob"}, nil, nil),
			plan:  roleFakeModel("global", "readers", "", adminRead, []string{"bob"}, nil, nil),
		},
		{
			// The permissions change, so Update overwrites the role first.
			name:  "remove a user while the permissions change",
			prior: roleFakeModel("item", "ops-repo", "ops-.*", read, []string{"alice", "bob"}, nil, nil),
			plan:  roleFakeModel("item", "ops-repo", "ops-.*", readConfigure, []string{"bob"}, nil, nil),
		},
		{
			name:  "remove a group while the pattern changes",
			prior: roleFakeModel("item", "ops-repo", "ops-.*", read, nil, []string{"ops", "devs"}, nil),
			plan:  roleFakeModel("item", "ops-repo", "ops/.*", read, nil, []string{"devs"}, nil),
		},
		{
			name:  "move a SID from assignments to users",
			prior: roleFakeModel("agent", "builders", "linux-.*", []string{"hudson.model.Computer.Build"}, nil, nil, []string{"dev"}),
			plan:  roleFakeModel("agent", "builders", "linux-.*", []string{"hudson.model.Computer.Build"}, []string{"dev"}, nil, nil),
		},
		{
			// Assignments are authoritative: an entry Terraform does not
			// manage is removed too.
			name:      "remove an entry added outside Terraform",
			prior:     roleFakeModel("global", "readers", "", adminRead, []string{"alice"}, nil, nil),
			plan:      roleFakeModel("global", "readers", "", adminRead, []string{"alice"}, nil, nil),
			unmanaged: []roleEntry{{Type: roleEntryGroup, Sid: "intruders"}},
		},
		{
			name:  "add a member",
			prior: roleFakeModel("item", "ops-repo", "ops-.*", read, []string{"alice"}, nil, nil),
			plan:  roleFakeModel("item", "ops-repo", "ops-.*", read, []string{"alice", "bob"}, nil, nil),
		},
	}
	for _, plugin := range []struct {
		name                   string
		dropEntriesOnOverwrite bool
	}{
		{"role-strategy 918 and later", false},
		{"role-strategy before 918", true},
	} {
		for _, tt := range tests {
			t.Run(plugin.name+"/"+tt.name, func(t *testing.T) {
				var diags diag.Diagnostics
				apiType := roleTypeAPI[tt.prior.Type.ValueString()]
				name := tt.prior.Name.ValueString()
				f := newRoleStrategyFake()
				f.dropEntriesOnOverwrite = plugin.dropEntriesOnOverwrite
				f.put(apiType, name, tt.prior.Pattern.ValueString(), setStrings(ctx, tt.prior.Permissions, &diags),
					append(roleEntries(ctx, tt.prior, &diags), tt.unmanaged...)...)
				want := roleEntries(ctx, tt.plan, &diags)
				sortRoleEntries(want)
				if diags.HasError() {
					t.Fatal(diags)
				}

				r, s := f.resource(ctx)
				plan := tfsdk.Plan{Schema: s}
				state := tfsdk.State{Schema: s}
				if d := plan.Set(ctx, tt.plan); d.HasError() {
					t.Fatalf("plan.Set: %v", d)
				}
				if d := state.Set(ctx, tt.prior); d.HasError() {
					t.Fatalf("state.Set: %v", d)
				}
				resp := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
				r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &resp)
				if resp.Diagnostics.HasError() {
					t.Fatalf("Update: %v", resp.Diagnostics)
				}

				got := f.role(t, apiType, name)
				if !slices.Equal(got.Sids, want) {
					t.Errorf("role entries = %v, want %v", got.Sids, want)
				}
				checkRoleFakeState(t, resp.State, tt.plan)

				refreshed := resource.ReadResponse{State: resp.State}
				r.Read(ctx, resource.ReadRequest{State: resp.State}, &refreshed)
				if refreshed.Diagnostics.HasError() {
					t.Fatalf("Read: %v", refreshed.Diagnostics)
				}
				checkRoleFakeState(t, refreshed.State, tt.plan)
			})
		}
	}
}
