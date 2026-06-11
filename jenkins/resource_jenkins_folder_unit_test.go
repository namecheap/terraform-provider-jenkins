package jenkins

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func TestExpandSecurity(t *testing.T) {
	tests := []struct {
		name  string
		input []interface{}
		want  *folderSecurity
	}{
		{
			name:  "nil-returns-nil",
			input: nil,
			want:  nil,
		},
		{
			name:  "empty-returns-nil",
			input: []interface{}{},
			want:  nil,
		},
		{
			name: "basic",
			input: []interface{}{
				map[string]interface{}{
					"inheritance_strategy": "org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy",
					"permissions":          []interface{}{"hudson.model.Item.Build:dev"},
				},
			},
			want: &folderSecurity{
				InheritanceStrategy: folderPermissionInheritanceStrategy{
					Class: "org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy",
				},
				Permission: []string{"hudson.model.Item.Build:dev"},
			},
		},
		{
			name: "multiple-permissions",
			input: []interface{}{
				map[string]interface{}{
					"inheritance_strategy": "org.jenkinsci.plugins.matrixauth.inheritance.NonInheritingStrategy",
					"permissions": []interface{}{
						"hudson.model.Item.Build:dev",
						"hudson.model.Item.Read:authenticated",
						"hudson.model.Item.Cancel:admin",
					},
				},
			},
			want: &folderSecurity{
				InheritanceStrategy: folderPermissionInheritanceStrategy{
					Class: "org.jenkinsci.plugins.matrixauth.inheritance.NonInheritingStrategy",
				},
				Permission: []string{
					"hudson.model.Item.Build:dev",
					"hudson.model.Item.Read:authenticated",
					"hudson.model.Item.Cancel:admin",
				},
			},
		},
		{
			name: "empty-permissions-list",
			input: []interface{}{
				map[string]interface{}{
					"inheritance_strategy": "org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy",
					"permissions":          []interface{}{},
				},
			},
			want: &folderSecurity{
				InheritanceStrategy: folderPermissionInheritanceStrategy{
					Class: "org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy",
				},
				Permission: []string{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := expandSecurity(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("expandSecurity() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestFlattenSecurity(t *testing.T) {
	tests := []struct {
		name  string
		input *folderSecurity
		want  []map[string]interface{}
	}{
		{
			name:  "nil-returns-empty-slice",
			input: nil,
			want:  []map[string]interface{}{},
		},
		{
			name: "basic",
			input: &folderSecurity{
				InheritanceStrategy: folderPermissionInheritanceStrategy{
					Class: "org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy",
				},
				Permission: []string{"hudson.model.Item.Build:dev"},
			},
			want: []map[string]interface{}{
				{
					"inheritance_strategy": "org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy",
					"permissions":          []string{"hudson.model.Item.Build:dev"},
				},
			},
		},
		{
			name: "multiple-permissions",
			input: &folderSecurity{
				InheritanceStrategy: folderPermissionInheritanceStrategy{
					Class: "org.jenkinsci.plugins.matrixauth.inheritance.NonInheritingStrategy",
				},
				Permission: []string{
					"hudson.model.Item.Build:dev",
					"hudson.model.Item.Read:authenticated",
				},
			},
			want: []map[string]interface{}{
				{
					"inheritance_strategy": "org.jenkinsci.plugins.matrixauth.inheritance.NonInheritingStrategy",
					"permissions":          []string{"hudson.model.Item.Build:dev", "hudson.model.Item.Read:authenticated"},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := flattenSecurity(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("flattenSecurity() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestExpandFlattenSecurity_Roundtrip(t *testing.T) {
	original := &folderSecurity{
		InheritanceStrategy: folderPermissionInheritanceStrategy{
			Class: "org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy",
		},
		Permission: []string{"hudson.model.Item.Build:dev", "hudson.model.Item.Read:dev"},
	}
	flat := flattenSecurity(original)

	// Simulate Terraform reading []string back into []interface{} before calling expandSecurity.
	input := make([]interface{}, len(flat))
	for i, m := range flat {
		perms := m["permissions"].([]string)
		ifacePerms := make([]interface{}, len(perms))
		for j, p := range perms {
			ifacePerms[j] = p
		}
		input[i] = map[string]interface{}{
			"inheritance_strategy": m["inheritance_strategy"],
			"permissions":          ifacePerms,
		}
	}

	got := expandSecurity(input)
	if !reflect.DeepEqual(got, original) {
		t.Errorf("roundtrip: got %+v, want %+v", got, original)
	}
}

func Test_resourceJenkinsFolderDelete(t *testing.T) {
	tests := []struct {
		name string
		meta jenkinsClient
		want diag.Diagnostics
	}{
		{
			name: "success",
			meta: &mockJenkinsClient{
				mockDeleteJobInFolder: func(_ context.Context, _ string, _ ...string) (bool, error) {
					return true, nil
				},
			},
			want: nil,
		},
		{
			name: "error",
			meta: &mockJenkinsClient{
				mockDeleteJobInFolder: func(_ context.Context, _ string, _ ...string) (bool, error) {
					return false, fmt.Errorf("api error")
				},
			},
			want: diag.Diagnostics{
				{Summary: "api error"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := schema.TestResourceDataRaw(t, resourceJenkinsFolder().Schema, map[string]interface{}{})
			got := resourceJenkinsFolderDelete(context.Background(), d, tt.meta)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("resourceJenkinsFolderDelete() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_resourceJenkinsFolderRead(t *testing.T) {
	tests := []struct {
		name string
		meta jenkinsClient
		want diag.Diagnostics
	}{
		{
			name: "not-found-clears-id",
			meta: &mockJenkinsClient{
				mockGetJob: func(_ context.Context, _ string, _ ...string) (*jenkins.Job, error) {
					return nil, fmt.Errorf("404")
				},
			},
			want: nil,
		},
		{
			name: "server-error",
			meta: &mockJenkinsClient{
				mockGetJob: func(_ context.Context, _ string, _ ...string) (*jenkins.Job, error) {
					return nil, fmt.Errorf("503 Service Unavailable")
				},
			},
			want: diag.Diagnostics{
				{Summary: `jenkins::read - Job "" does not exist: 503 Service Unavailable`},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := schema.TestResourceDataRaw(t, resourceJenkinsFolder().Schema, map[string]interface{}{})
			got := resourceJenkinsFolderRead(context.Background(), d, tt.meta)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("resourceJenkinsFolderRead() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_resourceJenkinsFolderCreate_folderNotFound(t *testing.T) {
	meta := &mockJenkinsClient{
		mockGetFolder: func(_ context.Context, _ string, _ ...string) (*jenkins.Folder, error) {
			return nil, fmt.Errorf("404 Not Found")
		},
	}
	d := schema.TestResourceDataRaw(t, resourceJenkinsFolder().Schema, map[string]interface{}{
		"name":   "test-job",
		"folder": "/job/parent",
	})
	got := resourceJenkinsFolderCreate(context.Background(), d, meta)
	want := diag.Diagnostics{
		{Summary: "jenkins::create - Could not find folder '/job/parent': 404 Not Found"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("resourceJenkinsFolderCreate() = %v, want %v", got, want)
	}
}
