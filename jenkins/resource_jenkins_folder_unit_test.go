package jenkins

import (
	"context"
	"testing"

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
