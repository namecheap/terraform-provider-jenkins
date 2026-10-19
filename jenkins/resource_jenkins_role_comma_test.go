package jenkins

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// roleSchemaAttribute returns one attribute of the jenkins_role schema.
func roleSchemaAttribute(t *testing.T, name string) schema.Attribute {
	t.Helper()
	resp := &resource.SchemaResponse{}
	newRoleResource().Schema(context.Background(), resource.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Schema() produced diagnostics: %v", resp.Diagnostics)
	}
	a, ok := resp.Schema.Attributes[name]
	if !ok {
		t.Fatalf("jenkins_role schema has no %q attribute", name)
	}
	return a
}

// role-strategy's removeRoles splits roleNames on ",", so destroying a role
// named "zz-team,admin" deletes the global "admin" role. The name has to be
// rejected at plan time, before the role is ever created.
func TestRoleNameRejectsComma(t *testing.T) {
	a, ok := roleSchemaAttribute(t, "name").(schema.StringAttribute)
	if !ok {
		t.Fatal("name is not a schema.StringAttribute")
	}

	tests := []struct {
		name      string
		value     types.String
		wantError bool
	}{
		{name: "plain", value: types.StringValue("team-a-developer")},
		{name: "spaces and dots", value: types.StringValue("Team A.dev")},
		{name: "null", value: types.StringNull()},
		{name: "unknown", value: types.StringUnknown()},
		{name: "comma inside", value: types.StringValue("zz-team,admin"), wantError: true},
		{name: "leading comma", value: types.StringValue(",admin"), wantError: true},
		{name: "trailing comma", value: types.StringValue("admin,"), wantError: true},
		{name: "comma only", value: types.StringValue(","), wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var diags diag.Diagnostics
			for _, v := range a.Validators {
				resp := &validator.StringResponse{}
				v.ValidateString(context.Background(), validator.StringRequest{
					Path:        path.Root("name"),
					ConfigValue: tt.value,
				}, resp)
				diags.Append(resp.Diagnostics...)
			}
			if got := diags.HasError(); got != tt.wantError {
				t.Fatalf("validation error = %t, want %t (diagnostics: %v)", got, tt.wantError, diags)
			}
		})
	}
}

// addRole receives the permission IDs joined with ",", so an element holding
// a comma would be read by role-strategy as several permissions.
func TestRolePermissionsRejectComma(t *testing.T) {
	a, ok := roleSchemaAttribute(t, "permissions").(schema.SetAttribute)
	if !ok {
		t.Fatal("permissions is not a schema.SetAttribute")
	}

	tests := []struct {
		name      string
		perms     []string
		wantError bool
	}{
		{name: "one", perms: []string{"hudson.model.Item.Read"}},
		{name: "several", perms: []string{"hudson.model.Item.Read", "hudson.model.Item.Build"}},
		{name: "joined pair", perms: []string{"hudson.model.Item.Read,hudson.model.Hudson.Administer"}, wantError: true},
		{name: "one of several", perms: []string{"hudson.model.Item.Read", "hudson.model.Item.Build,"}, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			elems := make([]attr.Value, 0, len(tt.perms))
			for _, p := range tt.perms {
				elems = append(elems, types.StringValue(p))
			}
			value, d := types.SetValue(types.StringType, elems)
			if d.HasError() {
				t.Fatalf("building permissions set: %v", d)
			}

			var diags diag.Diagnostics
			for _, v := range a.Validators {
				resp := &validator.SetResponse{}
				v.ValidateSet(context.Background(), validator.SetRequest{
					Path:        path.Root("permissions"),
					ConfigValue: value,
				}, resp)
				diags.Append(resp.Diagnostics...)
			}
			if got := diags.HasError(); got != tt.wantError {
				t.Fatalf("validation error = %t, want %t (diagnostics: %v)", got, tt.wantError, diags)
			}
		})
	}
}

// roleRequestLog records every request an httptest Jenkins receives, crumb
// probes included, so a test can prove that no request was sent at all.
type roleRequestLog struct {
	mu   sync.Mutex
	reqs []roleRequest
}

type roleRequest struct {
	method, path string
	form         url.Values
}

func (l *roleRequestLog) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(b))
		l.mu.Lock()
		l.reqs = append(l.reqs, roleRequest{method: r.Method, path: r.URL.Path, form: form})
		l.mu.Unlock()
		if strings.Contains(r.URL.Path, "crumbIssuer") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (l *roleRequestLog) posts() []roleRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []roleRequest
	for _, r := range l.reqs {
		if r.method == http.MethodPost {
			out = append(out, r)
		}
	}
	return out
}

func (l *roleRequestLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.reqs)
}

// The adapter refuses a role name holding a comma before sending anything:
// a role created by an older release, or imported, can still carry one in
// state, and plan-time validation does not run on destroy.
func TestRoleAdapterRefusesCommaName(t *testing.T) {
	tests := []struct {
		name     string
		roleName string
		call     func(c *jenkinsAdapter, name string) error
		action   string
		param    string
	}{
		{
			name:     "remove comma name",
			roleName: "zz-team,admin",
			call: func(c *jenkinsAdapter, name string) error {
				return c.RemoveRole(context.Background(), "globalRoles", name)
			},
		},
		{
			name:     "remove leading comma",
			roleName: ",admin",
			call: func(c *jenkinsAdapter, name string) error {
				return c.RemoveRole(context.Background(), "globalRoles", name)
			},
		},
		{
			name:     "add comma name",
			roleName: "a,b",
			call: func(c *jenkinsAdapter, name string) error {
				return c.AddRole(context.Background(), "globalRoles", name, []string{"hudson.model.Hudson.Read"}, "", false)
			},
		},
		{
			name:     "remove plain name",
			roleName: "zz-team",
			call: func(c *jenkinsAdapter, name string) error {
				return c.RemoveRole(context.Background(), "globalRoles", name)
			},
			action: "removeRoles",
			param:  "roleNames",
		},
		{
			name:     "add plain name",
			roleName: "zz-team",
			call: func(c *jenkinsAdapter, name string) error {
				return c.AddRole(context.Background(), "globalRoles", name, []string{"hudson.model.Hudson.Read"}, "", false)
			},
			action: "addRole",
			param:  "roleName",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var log roleRequestLog
			c, err := newJenkinsClient(&Config{ServerURL: log.server(t).URL})
			if err != nil {
				t.Fatalf("newJenkinsClient error = %v", err)
			}

			err = tt.call(c, tt.roleName)

			if tt.action == "" {
				if err == nil {
					t.Error("expected an error for a role name containing a comma")
				} else if !strings.Contains(err.Error(), "comma") {
					t.Errorf("error = %q, want it to name the comma", err)
				}
				if n := log.count(); n != 0 {
					t.Errorf("sent %d request(s), want none", n)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			posts := log.posts()
			if len(posts) != 1 {
				t.Fatalf("sent %d POST(s), want exactly one", len(posts))
			}
			if want := roleStrategyBase + "/" + tt.action; posts[0].path != want {
				t.Errorf("path = %q, want %q", posts[0].path, want)
			}
			if got := posts[0].form[tt.param]; len(got) != 1 || got[0] != tt.roleName {
				t.Errorf("%s = %q, want exactly [%q]", tt.param, got, tt.roleName)
			}
		})
	}
}
