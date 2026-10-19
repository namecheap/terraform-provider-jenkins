package jenkins

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
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
		{name: "comma between spaces", value: types.StringValue("zz-team , admin"), wantError: true},
		{name: "two commas", value: types.StringValue("zz-team,,admin"), wantError: true},
		{name: "comma in non-ASCII name", value: types.StringValue("équipe,admin"), wantError: true},
		// A per-line match would accept the first line alone.
		{name: "comma after newline", value: types.StringValue("zz-team\n,admin"), wantError: true},
		// role-strategy splits on the ASCII comma only, and the adapter
		// form-encodes the name, so these reach it unchanged.
		{name: "percent-encoded comma", value: types.StringValue("zz-team%2Cadmin")},
		{name: "fullwidth comma", value: types.StringValue("zz-team\uff0cadmin")},
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
			if !tt.wantError {
				return
			}
			// The error must tell a user upgrading with such a role in config
			// how to stop managing it.
			detail := diags.Errors()[0].Detail()
			for _, want := range []string{"removed block", "terraform state rm", "delete the role in Jenkins"} {
				if !strings.Contains(detail, want) {
					t.Errorf("detail %q does not mention %q", detail, want)
				}
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
		{name: "comma only", perms: []string{","}, wantError: true},
		{name: "comma between spaces", perms: []string{"hudson.model.Item.Read , hudson.model.Hudson.Administer"}, wantError: true},
		{name: "comma after newline", perms: []string{"hudson.model.Item.Read\n,hudson.model.Hudson.Administer"}, wantError: true},
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
	// role is the JSON getRole answers with; empty means no such role. It is
	// set before server is called, and only read afterwards.
	role string
}

type roleRequest struct {
	method, path string
	query, form  url.Values
}

func (l *roleRequestLog) server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		form, _ := url.ParseQuery(string(b))
		l.mu.Lock()
		l.reqs = append(l.reqs, roleRequest{method: r.Method, path: r.URL.Path, query: r.URL.Query(), form: form})
		l.mu.Unlock()
		if strings.Contains(r.URL.Path, "crumbIssuer") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/getRole") {
			// For no such role, getRole answers with an empty object.
			body := l.role
			if body == "" {
				body = "{}"
			}
			_, _ = io.WriteString(w, body)
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

func (l *roleRequestLog) requests() []roleRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]roleRequest(nil), l.reqs...)
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
		errHas   []string // for a refused name: text the error must contain
		errLacks []string // for a refused name: text the error must not contain
		action   string
		param    string
	}{
		{
			name:     "remove comma name",
			roleName: "zz-team,admin",
			call: func(c *jenkinsAdapter, name string) error {
				return c.RemoveRole(context.Background(), "globalRoles", name)
			},
			errHas: []string{"comma", "no request was sent", "remain in Jenkins", "removed block", "terraform state rm"},
		},
		{
			name:     "remove leading comma",
			roleName: ",admin",
			call: func(c *jenkinsAdapter, name string) error {
				return c.RemoveRole(context.Background(), "globalRoles", name)
			},
			errHas: []string{"comma", "no request was sent", "remain in Jenkins"},
		},
		{
			// Create and Update must not get the delete remediation.
			name:     "add comma name",
			roleName: "a,b",
			call: func(c *jenkinsAdapter, name string) error {
				return c.AddRole(context.Background(), "globalRoles", name, []string{"hudson.model.Hudson.Read"}, "", false)
			},
			errHas:   []string{"comma", "no request was sent", "could not be deleted later", "Use a name without a comma"},
			errLacks: []string{"state rm", "removed block", "remain in Jenkins", "deleting it"},
		},
		{
			// Update overwrites the role.
			name:     "overwrite comma name",
			roleName: "a,b",
			call: func(c *jenkinsAdapter, name string) error {
				return c.AddRole(context.Background(), "globalRoles", name, []string{"hudson.model.Hudson.Read"}, "", true)
			},
			errHas:   []string{"comma", "no request was sent", "Use a name without a comma"},
			errLacks: []string{"state rm", "removed block", "remain in Jenkins"},
		},
		{
			name:     "remove comma item role",
			roleName: "zz-team,admin",
			call: func(c *jenkinsAdapter, name string) error {
				return c.RemoveRole(context.Background(), "projectRoles", name)
			},
			errHas: []string{"comma", "no request was sent"},
		},
		{
			name:     "remove comma agent role",
			roleName: "zz-team,admin",
			call: func(c *jenkinsAdapter, name string) error {
				return c.RemoveRole(context.Background(), "slaveRoles", name)
			},
			errHas: []string{"comma", "no request was sent"},
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
		{
			// The name passes plan-time validation; were it sent unencoded,
			// role-strategy would decode it into "zz-team,admin".
			name:     "remove percent-encoded comma",
			roleName: "zz-team%2Cadmin",
			call: func(c *jenkinsAdapter, name string) error {
				return c.RemoveRole(context.Background(), "globalRoles", name)
			},
			action: "removeRoles",
			param:  "roleNames",
		},
		{
			name:     "add percent-encoded comma",
			roleName: "zz-team%2Cadmin",
			call: func(c *jenkinsAdapter, name string) error {
				return c.AddRole(context.Background(), "globalRoles", name, []string{"hudson.model.Hudson.Read"}, "", false)
			},
			action: "addRole",
			param:  "roleName",
		},
		{
			name:     "remove fullwidth comma",
			roleName: "zz-team\uff0cadmin",
			call: func(c *jenkinsAdapter, name string) error {
				return c.RemoveRole(context.Background(), "globalRoles", name)
			},
			action: "removeRoles",
			param:  "roleNames",
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
				} else {
					for _, want := range tt.errHas {
						if !strings.Contains(err.Error(), want) {
							t.Errorf("error = %q, want it to contain %q", err, want)
						}
					}
					for _, unwanted := range tt.errLacks {
						if strings.Contains(err.Error(), unwanted) {
							t.Errorf("error = %q, want it not to contain %q", err, unwanted)
						}
					}
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

// addRole receives the permission IDs joined with ",", so role-strategy reads
// the single ID "hudson.model.Hudson.Read,hudson.model.Hudson.Administer" as
// Read plus Administer. Plan-time validation rejects such an ID; the adapter
// refuses it as well, before sending anything, as it does a comma role name.
func TestRoleAdapterRefusesCommaPermission(t *testing.T) {
	const (
		read  = "hudson.model.Hudson.Read"
		admin = "hudson.model.Hudson.Administer"
		build = "hudson.model.Item.Build"
	)
	tests := []struct {
		name        string
		roleType    string
		pattern     string
		overwrite   bool
		permissions []string
		refused     string // the permission ID the error must name; "" means addRole is sent
	}{
		{name: "global joined pair", roleType: "globalRoles", permissions: []string{read + "," + admin}, refused: read + "," + admin},
		{name: "global joined pair overwrite", roleType: "globalRoles", overwrite: true, permissions: []string{read + "," + admin}, refused: read + "," + admin},
		{name: "item joined pair", roleType: "projectRoles", pattern: "^zz-.*", permissions: []string{build + "," + admin}, refused: build + "," + admin},
		{name: "item joined pair overwrite", roleType: "projectRoles", pattern: "^zz-.*", overwrite: true, permissions: []string{build + "," + admin}, refused: build + "," + admin},
		{name: "agent joined pair", roleType: "slaveRoles", pattern: "zz-.*", permissions: []string{build + "," + admin}, refused: build + "," + admin},
		{name: "agent joined pair overwrite", roleType: "slaveRoles", pattern: "zz-.*", overwrite: true, permissions: []string{build + "," + admin}, refused: build + "," + admin},
		{name: "comma in a later ID", roleType: "globalRoles", permissions: []string{read, build + "," + admin}, refused: build + "," + admin},
		{name: "trailing comma", roleType: "globalRoles", permissions: []string{read + ","}, refused: read + ","},
		{name: "comma only", roleType: "globalRoles", permissions: []string{","}, refused: ","},
		{name: "global clean", roleType: "globalRoles", permissions: []string{read, build}},
		{name: "global clean overwrite", roleType: "globalRoles", overwrite: true, permissions: []string{read}},
		{name: "item clean", roleType: "projectRoles", pattern: "^zz-.*", permissions: []string{read, build}},
		{name: "agent clean overwrite", roleType: "slaveRoles", pattern: "zz-.*", overwrite: true, permissions: []string{build}},
		// The form value is encoded, so role-strategy receives "%2C" as is.
		{name: "percent-encoded comma", roleType: "globalRoles", permissions: []string{read + "%2C" + admin}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var log roleRequestLog
			c, err := newJenkinsClient(&Config{ServerURL: log.server(t).URL})
			if err != nil {
				t.Fatalf("newJenkinsClient error = %v", err)
			}

			err = c.AddRole(context.Background(), tt.roleType, "zz-team", tt.permissions, tt.pattern, tt.overwrite)

			if tt.refused != "" {
				if err == nil {
					t.Error("expected an error for a permission ID containing a comma")
				} else {
					for _, want := range []string{strconv.Quote(tt.refused), "comma", "no request was sent"} {
						if !strings.Contains(err.Error(), want) {
							t.Errorf("error = %q, want it to contain %q", err, want)
						}
					}
					if strings.Contains(err.Error(), "role name") {
						t.Errorf("error = %q blames the role name", err)
					}
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
			if want := roleStrategyBase + "/addRole"; posts[0].path != want {
				t.Errorf("path = %q, want %q", posts[0].path, want)
			}
			for param, want := range map[string]string{
				"type":          tt.roleType,
				"permissionIds": strings.Join(tt.permissions, ","),
				"overwrite":     strconv.FormatBool(tt.overwrite),
			} {
				if got := posts[0].form[param]; len(got) != 1 || got[0] != want {
					t.Errorf("%s = %q, want exactly [%q]", param, got, want)
				}
			}
		})
	}
}

// roleStateWithID builds jenkins_role state the way ImportState fills it, so a
// test can put a role name in state that ImportState itself would refuse, as
// an older release could.
func roleStateWithID(ctx context.Context, t *testing.T, s schema.Schema, roleType, name string) tfsdk.State {
	t.Helper()
	st := tfsdk.State{Schema: s, Raw: covMNull(ctx, s)}
	var d diag.Diagnostics
	d.Append(st.SetAttribute(ctx, path.Root("type"), roleType)...)
	d.Append(st.SetAttribute(ctx, path.Root("name"), name)...)
	d.Append(st.SetAttribute(ctx, path.Root("id"), roleType+"/"+name)...)
	if d.HasError() {
		t.Fatalf("building state: %v", d)
	}
	return st
}

// Plan-time validation does not run on import or destroy, so the resource
// itself must keep a comma role name from reaching removeRoles: ImportState
// refuses one, and Delete of one already in state sends no request at all.
// Create and Update, reached with a name that skipped validation, send no
// addRole either. The resource runs on the real adapter here, because
// mockJenkinsClient does not apply the adapter's guard.
func TestRoleResourceCommaName(t *testing.T) {
	ctx := context.Background()
	roleModel := func(name string, permissions ...string) *roleResourceModel {
		perms := make([]attr.Value, 0, len(permissions))
		for _, p := range permissions {
			perms = append(perms, types.StringValue(p))
		}
		none := types.SetValueMust(types.StringType, []attr.Value{})
		return &roleResourceModel{
			Type: types.StringValue("global"), Name: types.StringValue(name), Pattern: types.StringNull(),
			Permissions: types.SetValueMust(types.StringType, perms), Users: none, Groups: none, Assignments: none,
		}
	}

	tests := []struct {
		name      string
		op        string // "import", "delete", "create" or "update"
		roleName  string
		wantError string // substring of the error detail; "" means no error
		wantPOST  bool   // exactly one removeRoles POST carrying roleNames=[roleName]
		wantGETs  int    // getRole requests, which take the name whole
	}{
		{name: "import comma name", op: "import", roleName: "zz-team,admin", wantError: "comma"},
		{name: "import comma in nested name", op: "import", roleName: "folder/zz-team,admin", wantError: "comma"},
		{name: "import trailing comma", op: "import", roleName: "zz-team,", wantError: "comma"},
		{name: "import comma only", op: "import", roleName: ",", wantError: "comma"},
		{name: "import plain name", op: "import", roleName: "zz-team"},
		{name: "delete comma name", op: "delete", roleName: "zz-team,admin", wantError: "remain in Jenkins"},
		{name: "delete plain name", op: "delete", roleName: "zz-team", wantPOST: true},
		// Create first checks, by name, that the role does not exist yet.
		{name: "create comma name", op: "create", roleName: "zz-team,admin", wantError: "no request was sent", wantGETs: 1},
		{name: "update comma name", op: "update", roleName: "zz-team,admin", wantError: "no request was sent"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var log roleRequestLog
			c, err := newJenkinsClient(&Config{ServerURL: log.server(t).URL})
			if err != nil {
				t.Fatalf("newJenkinsClient error = %v", err)
			}
			r := &roleResource{resourceHelper: &resourceHelper{client: c}}
			s := covMSchema(ctx, r)

			var diags diag.Diagnostics
			var state tfsdk.State
			switch tt.op {
			case "import":
				resp := &resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: covMNull(ctx, s)}}
				r.ImportState(ctx, resource.ImportStateRequest{ID: "global/" + tt.roleName}, resp)
				diags, state = resp.Diagnostics, resp.State
			case "delete":
				st := roleStateWithID(ctx, t, s, "global", tt.roleName)
				resp := &resource.DeleteResponse{State: st}
				r.Delete(ctx, resource.DeleteRequest{State: st}, resp)
				diags = resp.Diagnostics
			case "create":
				plan := tfsdk.Plan{Schema: s}
				if d := plan.Set(ctx, roleModel(tt.roleName, "hudson.model.Hudson.Read")); d.HasError() {
					t.Fatalf("building plan: %v", d)
				}
				resp := &resource.CreateResponse{State: tfsdk.State{Schema: s, Raw: covMNull(ctx, s)}}
				r.Create(ctx, resource.CreateRequest{Plan: plan}, resp)
				diags, state = resp.Diagnostics, resp.State
			case "update":
				// A permission change makes Update overwrite the role.
				st := tfsdk.State{Schema: s}
				if d := st.Set(ctx, roleModel(tt.roleName, "hudson.model.Hudson.Read")); d.HasError() {
					t.Fatalf("building state: %v", d)
				}
				plan := tfsdk.Plan{Schema: s}
				if d := plan.Set(ctx, roleModel(tt.roleName, "hudson.model.Hudson.Read", "hudson.model.Item.Read")); d.HasError() {
					t.Fatalf("building plan: %v", d)
				}
				resp := &resource.UpdateResponse{State: st}
				r.Update(ctx, resource.UpdateRequest{Plan: plan, State: st}, resp)
				diags = resp.Diagnostics
			}

			if tt.wantError == "" {
				if diags.HasError() {
					t.Fatalf("unexpected diagnostics: %v", diags)
				}
			} else if !diags.HasError() {
				t.Error("expected an error diagnostic")
			} else if detail := diags.Errors()[0].Detail(); !strings.Contains(detail, tt.wantError) {
				t.Errorf("detail = %q, want it to contain %q", detail, tt.wantError)
			}

			if tt.op == "import" || tt.op == "create" {
				var got types.String
				if d := state.GetAttribute(ctx, path.Root("name"), &got); d.HasError() {
					t.Fatalf("reading name from state: %v", d)
				}
				if tt.wantError != "" && !got.IsNull() {
					t.Errorf("name in state = %q after a refused %s, want null", got.ValueString(), tt.op)
				}
				if tt.wantError == "" && got.ValueString() != tt.roleName {
					t.Errorf("name in state = %q, want %q", got.ValueString(), tt.roleName)
				}
			}

			if !tt.wantPOST {
				if n, posts := log.count(), log.posts(); n != tt.wantGETs || len(posts) != 0 {
					t.Errorf("sent %d request(s), want %d getRole GET(s) and no POST; POSTs: %v", n, tt.wantGETs, posts)
				}
				return
			}
			posts := log.posts()
			if len(posts) != 1 {
				t.Fatalf("sent %d POST(s), want exactly one", len(posts))
			}
			if want := roleStrategyBase + "/removeRoles"; posts[0].path != want {
				t.Errorf("path = %q, want %q", posts[0].path, want)
			}
			if got := posts[0].form["roleNames"]; len(got) != 1 || got[0] != tt.roleName {
				t.Errorf("roleNames = %q, want exactly [%q]", got, tt.roleName)
			}
		})
	}
}

// An Update that changes only users, groups or assignments leaves the role in
// place: it sends no addRole and reconciles the entries one request at a time.
// With a comma role name in state, each of those requests carries the name
// whole in roleName, which role-strategy looks up as one role, so only that
// role's entries change, and no addRole or removeRoles is sent.
func TestRoleResourceCommaNameEntriesOnly(t *testing.T) {
	ctx := context.Background()
	const name = "zz-team,admin"
	type post struct{ action, param, sid string }
	roleModel := func(roleType, pattern, permission string, entries []roleEntry) *roleResourceModel {
		sids := map[string][]attr.Value{}
		for _, e := range entries {
			sids[e.Type] = append(sids[e.Type], types.StringValue(e.Sid))
		}
		set := func(typ string) types.Set {
			return types.SetValueMust(types.StringType, append([]attr.Value{}, sids[typ]...))
		}
		p := types.StringNull()
		if pattern != "" {
			p = types.StringValue(pattern)
		}
		return &roleResourceModel{
			ID: types.StringValue(roleType + "/" + name), Type: types.StringValue(roleType), Name: types.StringValue(name), Pattern: p,
			Permissions: types.SetValueMust(types.StringType, []attr.Value{types.StringValue(permission)}),
			Users:       set(roleEntryUser), Groups: set(roleEntryGroup), Assignments: set(roleEntryEither),
		}
	}

	tests := []struct {
		name       string
		roleType   string
		apiType    string
		pattern    string // in state, plan and Jenkins alike; "" for a global role
		permission string
		held       []roleEntry // in state and in Jenkins
		want       []roleEntry // in the plan
		wantPOSTs  []post      // in order
	}{
		{
			name: "global user swapped", roleType: "global", apiType: "globalRoles", permission: "hudson.model.Hudson.Read",
			held:      []roleEntry{{Type: roleEntryUser, Sid: "alice"}},
			want:      []roleEntry{{Type: roleEntryUser, Sid: "bob"}},
			wantPOSTs: []post{{"assignUserRole", "user", "bob"}, {"unassignUserRole", "user", "alice"}},
		},
		{
			name: "global entries all removed", roleType: "global", apiType: "globalRoles", permission: "hudson.model.Hudson.Read",
			held:      []roleEntry{{Type: roleEntryUser, Sid: "alice"}, {Type: roleEntryGroup, Sid: "devs"}},
			wantPOSTs: []post{{"unassignUserRole", "user", "alice"}, {"unassignGroupRole", "group", "devs"}},
		},
		{
			name: "item group added", roleType: "item", apiType: "projectRoles", pattern: ".*", permission: "hudson.model.Item.Read",
			held:      []roleEntry{{Type: roleEntryGroup, Sid: "devs"}},
			want:      []roleEntry{{Type: roleEntryGroup, Sid: "devs"}, {Type: roleEntryGroup, Sid: "ops"}},
			wantPOSTs: []post{{"assignGroupRole", "group", "ops"}},
		},
		{
			name: "agent ambiguous entry made a user", roleType: "agent", apiType: "slaveRoles", pattern: "linux-.*", permission: "hudson.model.Computer.Connect",
			held:      []roleEntry{{Type: roleEntryEither, Sid: "carol"}},
			want:      []roleEntry{{Type: roleEntryUser, Sid: "carol"}},
			wantPOSTs: []post{{"assignUserRole", "user", "carol"}, {"unassignRole", "sid", "carol"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			role, err := json.Marshal(roleStrategyRoleResponse{
				PermissionIDs: map[string]bool{tt.permission: true}, Pattern: tt.pattern, Sids: tt.held,
			})
			if err != nil {
				t.Fatalf("encoding getRole answer: %v", err)
			}
			log := roleRequestLog{role: string(role)}
			c, err := newJenkinsClient(&Config{ServerURL: log.server(t).URL})
			if err != nil {
				t.Fatalf("newJenkinsClient error = %v", err)
			}
			r := &roleResource{resourceHelper: &resourceHelper{client: c}}
			s := covMSchema(ctx, r)

			st := tfsdk.State{Schema: s}
			if d := st.Set(ctx, roleModel(tt.roleType, tt.pattern, tt.permission, tt.held)); d.HasError() {
				t.Fatalf("building state: %v", d)
			}
			plan := tfsdk.Plan{Schema: s}
			if d := plan.Set(ctx, roleModel(tt.roleType, tt.pattern, tt.permission, tt.want)); d.HasError() {
				t.Fatalf("building plan: %v", d)
			}
			resp := &resource.UpdateResponse{State: st}
			r.Update(ctx, resource.UpdateRequest{Plan: plan, State: st}, resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
			}

			var posts []roleRequest
			gets := 0
			for _, req := range log.requests() {
				switch {
				case strings.Contains(req.path, "crumbIssuer"):
				case req.method == http.MethodGet && req.path == roleStrategyBase+"/getRole":
					gets++
					if got := req.query["roleName"]; len(got) != 1 || got[0] != name {
						t.Errorf("getRole roleName = %q, want exactly [%q]", got, name)
					}
				case req.method == http.MethodPost:
					posts = append(posts, req)
				default:
					t.Errorf("unexpected request %s %s", req.method, req.path)
				}
			}
			// One read to reconcile the entries, one to read the role back.
			if gets != 2 {
				t.Errorf("sent %d getRole GET(s), want 2", gets)
			}
			if len(posts) != len(tt.wantPOSTs) {
				t.Fatalf("sent POSTs %v, want %v", posts, tt.wantPOSTs)
			}
			for i, want := range tt.wantPOSTs {
				got := posts[i]
				if p := roleStrategyBase + "/" + want.action; got.path != p {
					t.Errorf("POST %d path = %q, want %q", i, got.path, p)
				}
				if v := got.form["roleName"]; len(v) != 1 || v[0] != name {
					t.Errorf("POST %d roleName = %q, want exactly [%q]", i, v, name)
				}
				if v := got.form.Get("type"); v != tt.apiType {
					t.Errorf("POST %d type = %q, want %q", i, v, tt.apiType)
				}
				if v := got.form[want.param]; len(v) != 1 || v[0] != want.sid {
					t.Errorf("POST %d %s = %q, want exactly [%q]", i, want.param, v, want.sid)
				}
			}
		})
	}
}
