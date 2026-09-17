package jenkins

import (
	"context"
	"testing"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// TestSecurityRoundtrip exercises the securityToSet/securityFromModel pair that
// replaces the SDKv2 flatten/expand helpers: a folderSecurity converted into the
// "security" set block and back must be unchanged.
func TestSecurityRoundtrip(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		in   *folderSecurity
	}{
		{name: "nil", in: nil},
		{
			// Reproduces what encoding/xml actually produces when Jenkins persists
			// an AuthorizationMatrixProperty with zero <permission> elements: a nil
			// (not empty-but-non-nil) slice. securityToSet must still yield a
			// non-null "permissions" Set, matching a "permissions = []" config —
			// a null Set there causes Terraform's post-apply consistency check to
			// fail with "does not correlate with any element in actual".
			name: "nil-permissions",
			in: &folderSecurity{
				InheritanceStrategy: folderPermissionInheritanceStrategy{Class: defaultFolderInheritanceStrategy},
				Permission:          nil,
			},
		},
		{
			// Distinct from "nil-permissions" above: an empty-but-non-nil slice.
			// Both must produce the same non-null empty Set, so this pins down
			// that the fix isn't accidentally keyed off nil-ness specifically.
			name: "empty-permissions",
			in: &folderSecurity{
				InheritanceStrategy: folderPermissionInheritanceStrategy{Class: defaultFolderInheritanceStrategy},
				Permission:          []string{},
			},
		},
		{
			name: "single-permission",
			in: &folderSecurity{
				InheritanceStrategy: folderPermissionInheritanceStrategy{Class: defaultFolderInheritanceStrategy},
				Permission:          []string{"hudson.model.Item.Build:dev"},
			},
		},
		{
			name: "multiple-permissions",
			in: &folderSecurity{
				InheritanceStrategy: folderPermissionInheritanceStrategy{Class: "org.jenkinsci.plugins.matrixauth.inheritance.NonInheritingStrategy"},
				Permission: []string{
					"hudson.model.Item.Build:dev",
					"hudson.model.Item.Read:authenticated",
					"hudson.model.Item.Cancel:admin",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var diags diag.Diagnostics
			set := securityToSet(ctx, tt.in, &diags)
			if diags.HasError() {
				t.Fatalf("securityToSet: %v", diags)
			}

			if tt.in != nil {
				// A Required attribute must never come back null: Terraform's
				// post-apply consistency check treats a null Set and an empty
				// (non-null) Set as non-correlating values, so a "permissions = []"
				// config would fail apply if this ever regresses to null.
				elems := set.Elements()
				if len(elems) != 1 {
					t.Fatalf("expected exactly 1 set element, got %d", len(elems))
				}
				obj, ok := elems[0].(types.Object)
				if !ok {
					t.Fatalf("expected set element to be types.Object, got %T", elems[0])
				}
				if perms := obj.Attributes()["permissions"]; perms.IsNull() {
					t.Fatalf("permissions attribute is null, want non-null (possibly empty) Set")
				}
			}

			got := securityFromModel(ctx, set, &diags)
			if diags.HasError() {
				t.Fatalf("securityFromModel: %v", diags)
			}

			if tt.in == nil {
				if got != nil {
					t.Fatalf("expected nil security, got %+v", got)
				}
				if len(set.Elements()) != 0 {
					t.Fatalf("expected empty set for nil security, got %d elements", len(set.Elements()))
				}
				return
			}

			if got == nil {
				t.Fatalf("expected non-nil security")
			}
			if got.InheritanceStrategy.Class != tt.in.InheritanceStrategy.Class {
				t.Errorf("inheritance_strategy = %q, want %q", got.InheritanceStrategy.Class, tt.in.InheritanceStrategy.Class)
			}
			if len(got.Permission) != len(tt.in.Permission) {
				t.Fatalf("permissions len = %d, want %d", len(got.Permission), len(tt.in.Permission))
			}
			for i := range got.Permission {
				if got.Permission[i] != tt.in.Permission[i] {
					t.Errorf("permission[%d] = %q, want %q", i, got.Permission[i], tt.in.Permission[i])
				}
			}
		})
	}
}

// TestSecurityPermissionsOrderIndependent pins down the reason permissions is
// a Set rather than a List: Jenkins re-groups permissions by permission type
// when it persists config.xml, so the order read back after apply rarely
// matches the order configured in HCL. If permissions were order-sensitive,
// that reordering alone would make Terraform report "Provider produced
// inconsistent result after apply" even though the same permissions are
// granted.
func TestSecurityPermissionsOrderIndependent(t *testing.T) {
	ctx := context.Background()

	configured := &folderSecurity{
		InheritanceStrategy: folderPermissionInheritanceStrategy{Class: defaultFolderInheritanceStrategy},
		Permission: []string{
			"hudson.model.Item.Build:dev",
			"hudson.model.Item.Read:authenticated",
			"hudson.model.Item.Cancel:admin",
		},
	}
	// Same permissions, reordered and regrouped the way Jenkins actually
	// returns them (by permission type) after a save/reload round trip.
	returnedByJenkins := &folderSecurity{
		InheritanceStrategy: folderPermissionInheritanceStrategy{Class: defaultFolderInheritanceStrategy},
		Permission: []string{
			"hudson.model.Item.Cancel:admin",
			"hudson.model.Item.Build:dev",
			"hudson.model.Item.Read:authenticated",
		},
	}

	var diags diag.Diagnostics
	planned := securityToSet(ctx, configured, &diags)
	actual := securityToSet(ctx, returnedByJenkins, &diags)
	if diags.HasError() {
		t.Fatalf("securityToSet: %v", diags)
	}

	if !planned.Equal(actual) {
		t.Fatalf("planned and actual security sets should be equal regardless of permission order:\nplanned: %#v\nactual:  %#v", planned, actual)
	}
}

// TestSecurityPermissionsDeduplicates documents that securityToSet collapses
// duplicate permissions before building the Set. The framework's SetValueFrom
// does not deduplicate — it rejects duplicates with a "Duplicate Set Element"
// diagnostic — so if Jenkins' config.xml ever contains the same permission
// twice (e.g. hand-edited), the provider must dedupe or every refresh fails.
func TestSecurityPermissionsDeduplicates(t *testing.T) {
	ctx := context.Background()

	sec := &folderSecurity{
		InheritanceStrategy: folderPermissionInheritanceStrategy{Class: defaultFolderInheritanceStrategy},
		Permission: []string{
			"hudson.model.Item.Build:dev",
			"hudson.model.Item.Build:dev",
			"hudson.model.Item.Read:authenticated",
		},
	}

	var diags diag.Diagnostics
	set := securityToSet(ctx, sec, &diags)
	if diags.HasError() {
		t.Fatalf("securityToSet: %v", diags)
	}

	got := securityFromModel(ctx, set, &diags)
	if diags.HasError() {
		t.Fatalf("securityFromModel: %v", diags)
	}

	if len(got.Permission) != 2 {
		t.Fatalf("expected duplicate permission to collapse to 2 entries, got %d: %v", len(got.Permission), got.Permission)
	}
}

// TestFolderPopulateReportsServerSecurity pins the refresh path to the server's
// answer. populate() used to substitute the desired security value whenever the
// server reported no AuthorizationMatrixProperty and the desired block had zero
// permissions — and on Read the "desired" value is the prior state, so the
// substitution re-justified itself on every refresh and the block could never
// drift. Verified against jenkins/jenkins:2.579 + matrix-auth: a zero-permission
// property IS persisted, so an absent property genuinely means absent.
func TestFolderPopulateReportsServerSecurity(t *testing.T) {
	ctx := context.Background()

	const noSecurity = `<?xml version='1.1' encoding='UTF-8'?>
<com.cloudbees.hudson.plugins.folder.Folder>
  <description>d</description>
  <properties/>
</com.cloudbees.hudson.plugins.folder.Folder>`

	// What Jenkins actually writes for permissions = [] — the property is
	// present, it just has no <permission> children.
	const emptyPermissions = `<?xml version='1.1' encoding='UTF-8'?>
<com.cloudbees.hudson.plugins.folder.Folder>
  <description>d</description>
  <properties>
    <com.cloudbees.hudson.plugins.folder.properties.AuthorizationMatrixProperty>
      <inheritanceStrategy class="org.jenkinsci.plugins.matrixauth.inheritance.NonInheritingStrategy"/>
    </com.cloudbees.hudson.plugins.folder.properties.AuthorizationMatrixProperty>
  </properties>
</com.cloudbees.hudson.plugins.folder.Folder>`

	tests := []struct {
		name            string
		config          string
		wantBlocks      int
		wantStrategy    string
		wantPermissions int
	}{
		{
			// The regression: prior state carries a NonInheritingStrategy block,
			// the server reports no property at all. Refresh must report the
			// block as gone so `terraform plan` offers to put it back.
			name:       "absent property drifts",
			config:     noSecurity,
			wantBlocks: 0,
		},
		{
			name:            "zero-permission property round-trips",
			config:          emptyPermissions,
			wantBlocks:      1,
			wantStrategy:    "org.jenkinsci.plugins.matrixauth.inheritance.NonInheritingStrategy",
			wantPermissions: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := covcLiveJob(t, "/job/f", tt.config)
			r := &folderResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
				mockGetJob: func(context.Context, string, ...string) (*jenkins.Job, error) { return job, nil },
			}}}

			// Prior state asserts a block the server may not have.
			data := &folderResourceModel{
				Name:   types.StringValue("f"),
				Folder: types.StringNull(),
				Security: types.SetValueMust(folderSecurityObjectType, []attr.Value{
					folderSecurityBlockValue(t, "org.jenkinsci.plugins.matrixauth.inheritance.NonInheritingStrategy"),
				}),
			}

			var diags diag.Diagnostics
			if ok := r.populate(ctx, data, &diags); !ok || diags.HasError() {
				t.Fatalf("populate failed: ok=%v diags=%v", ok, diags)
			}

			var blocks []folderSecurityBlockModel
			if d := data.Security.ElementsAs(ctx, &blocks, false); d.HasError() {
				t.Fatalf("reading security blocks: %v", d)
			}
			if len(blocks) != tt.wantBlocks {
				t.Fatalf("security blocks = %d, want %d (%v)", len(blocks), tt.wantBlocks, data.Security)
			}
			if tt.wantBlocks == 0 {
				return
			}

			if got := blocks[0].InheritanceStrategy.ValueString(); got != tt.wantStrategy {
				t.Errorf("inheritance_strategy = %q, want %q", got, tt.wantStrategy)
			}
			if blocks[0].Permissions.IsNull() {
				t.Error("permissions is null; a `permissions = []` config cannot correlate with a null set")
			}
			if got := len(blocks[0].Permissions.Elements()); got != tt.wantPermissions {
				t.Errorf("permissions = %d elements, want %d", got, tt.wantPermissions)
			}
		})
	}
}

// folderSecurityBlockValue builds one "security" block object matching
// folderSecurityObjectType.
func folderSecurityBlockValue(t *testing.T, strategy string, permissions ...string) attr.Value {
	t.Helper()

	perms, d := types.SetValueFrom(context.Background(), types.StringType, permissions)
	if d.HasError() {
		t.Fatalf("building permissions set: %v", d)
	}

	obj, d := types.ObjectValue(folderSecurityObjectType.AttrTypes, map[string]attr.Value{
		"inheritance_strategy": types.StringValue(strategy),
		"permissions":          perms,
	})
	if d.HasError() {
		t.Fatalf("building security block object: %v", d)
	}

	return obj
}
