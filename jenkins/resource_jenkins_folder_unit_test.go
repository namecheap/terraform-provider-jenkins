package jenkins

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
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
