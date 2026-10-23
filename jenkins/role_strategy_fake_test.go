package jenkins

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// roleStrategyFake is an in-memory role-strategy plugin for jenkins_role unit
// tests. Unlike the per-call mocks it keeps the roles, so a test can check
// what a role holds after Create or Update. It follows the plugin semantics
// the resource has to cope with:
//
//   - addRole without overwrite is a silent no-op on an existing role;
//   - addRole with overwrite replaces the permissions and pattern, and from
//     role-strategy 918.v91e5468d8db_2 on keeps the role's entries; older
//     releases drop them, which dropEntriesOnOverwrite models;
//   - getRole answers {} for a missing role, and "permissionIds":{} for a role
//     without permissions.
type roleStrategyFake struct {
	roles                  map[string]*roleStrategyFakeRole
	dropEntriesOnOverwrite bool
	writes                 int
}

type roleStrategyFakeRole struct {
	permissions []string
	pattern     string
	entries     []roleEntry
}

func newRoleStrategyFake() *roleStrategyFake {
	return &roleStrategyFake{roles: map[string]*roleStrategyFakeRole{}}
}

func roleStrategyFakeKey(apiType, name string) string { return apiType + "/" + name }

// put stores a role as if it was made outside Terraform.
func (f *roleStrategyFake) put(apiType, name, pattern string, permissions []string, entries ...roleEntry) {
	f.roles[roleStrategyFakeKey(apiType, name)] = &roleStrategyFakeRole{
		permissions: slices.Clone(permissions),
		pattern:     pattern,
		entries:     slices.Clone(entries),
	}
}

// role returns what getRole answers for the role, with the permissions and
// entries sorted so tests can compare it.
func (f *roleStrategyFake) role(t *testing.T, apiType, name string) roleStrategyRoleResponse {
	t.Helper()
	var out roleStrategyRoleResponse
	if err := f.getRole(context.Background(), apiType, name, &out); err != nil {
		t.Fatal(err)
	}
	sortRoleEntries(out.Sids)
	return out
}

func sortRoleEntries(entries []roleEntry) {
	slices.SortFunc(entries, func(a, b roleEntry) int {
		return strings.Compare(a.Type+"/"+a.Sid, b.Type+"/"+b.Sid)
	})
}

func (f *roleStrategyFake) addRole(_ context.Context, apiType, name string, permissionIDs []string, pattern string, overwrite bool) error {
	f.writes++
	r, exists := f.roles[roleStrategyFakeKey(apiType, name)]
	if exists && !overwrite {
		return nil
	}
	if pattern == "" && apiType != "globalRoles" {
		pattern = ".*"
	}
	if !exists || f.dropEntriesOnOverwrite {
		f.put(apiType, name, pattern, permissionIDs)
		return nil
	}
	r.permissions, r.pattern = slices.Clone(permissionIDs), pattern
	return nil
}

func (f *roleStrategyFake) assignRole(_ context.Context, apiType, name string, entry roleEntry) error {
	f.writes++
	r, ok := f.roles[roleStrategyFakeKey(apiType, name)]
	if !ok {
		return fmt.Errorf("role %s does not exist", name)
	}
	if !slices.Contains(r.entries, entry) {
		r.entries = append(r.entries, entry)
	}
	return nil
}

func (f *roleStrategyFake) unassignRole(_ context.Context, apiType, name string, entry roleEntry) error {
	f.writes++
	r, ok := f.roles[roleStrategyFakeKey(apiType, name)]
	if !ok {
		return fmt.Errorf("role %s does not exist", name)
	}
	r.entries = slices.DeleteFunc(r.entries, func(e roleEntry) bool { return e == entry })
	return nil
}

// getRole goes through JSON, as the real client does, so a missing role and
// a role without permissions decode the way they do against the plugin.
func (f *roleStrategyFake) getRole(_ context.Context, apiType, name string, out interface{}) error {
	body := []byte(`{}`)
	if r, ok := f.roles[roleStrategyFakeKey(apiType, name)]; ok {
		resp := roleStrategyRoleResponse{
			PermissionIDs: map[string]bool{},
			Pattern:       r.pattern,
			Sids:          append([]roleEntry{}, r.entries...),
		}
		for _, p := range r.permissions {
			resp.PermissionIDs[p] = true
		}
		var err error
		if body, err = json.Marshal(resp); err != nil {
			return err
		}
	}
	return json.Unmarshal(body, out)
}

func (f *roleStrategyFake) removeRole(_ context.Context, apiType, name string) error {
	f.writes++
	delete(f.roles, roleStrategyFakeKey(apiType, name))
	return nil
}

// resource returns a jenkins_role resource backed by the fake, and its schema.
func (f *roleStrategyFake) resource(ctx context.Context) (*roleResource, rschema.Schema) {
	r := &roleResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockAddRole:      f.addRole,
		mockAssignRole:   f.assignRole,
		mockUnassignRole: f.unassignRole,
		mockGetRole:      f.getRole,
		mockRemoveRole:   f.removeRole,
	}}}
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	return r, sr.Schema
}

// roleFakeModel builds a jenkins_role model; an empty pattern is planned as
// rolePatternPlanModifier plans it.
func roleFakeModel(roleType, name, pattern string, permissions, users, groups, assignments []string) *roleResourceModel {
	set := func(v []string) types.Set {
		s, _ := types.SetValueFrom(context.Background(), types.StringType, append([]string{}, v...))
		return s
	}
	m := &roleResourceModel{
		Type:        types.StringValue(roleType),
		Name:        types.StringValue(name),
		Pattern:     types.StringNull(),
		Permissions: set(permissions),
		Users:       set(users),
		Groups:      set(groups),
		Assignments: set(assignments),
	}
	if pattern == "" && roleType != "global" {
		pattern = ".*"
	}
	if pattern != "" {
		m.Pattern = types.StringValue(pattern)
	}
	return m
}

// checkRoleFakeState reports each attribute of the state that differs from
// the plan, which Terraform would report as an inconsistent result, and
// returns the state.
func checkRoleFakeState(t *testing.T, st tfsdk.State, plan *roleResourceModel) *roleResourceModel {
	t.Helper()
	var got roleResourceModel
	if d := st.Get(context.Background(), &got); d.HasError() {
		t.Fatalf("state.Get: %v", d)
	}
	for name, pair := range map[string][2]attr.Value{
		"pattern":     {got.Pattern, plan.Pattern},
		"permissions": {got.Permissions, plan.Permissions},
		"users":       {got.Users, plan.Users},
		"groups":      {got.Groups, plan.Groups},
		"assignments": {got.Assignments, plan.Assignments},
	} {
		if !pair[0].Equal(pair[1]) {
			t.Errorf("state %s = %v, want the planned %v", name, pair[0], pair[1])
		}
	}
	return &got
}
