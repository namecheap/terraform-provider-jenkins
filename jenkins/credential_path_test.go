package jenkins

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	fwpath "github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Regression tests for issue #250: credential domains, IDs and folders are
// interpolated into credentials-store URLs, so a value such as "../../..." or
// one ending in "?" sent a folder-scoped request to a different store.

const (
	// craftedCredentialDomain is the issue #250 reproduction: from a folder
	// store it climbs to the system store and overwrites the credential
	// prod-deploy-key; the trailing "?" turns the rest of the gojenkins-built
	// path into a query string.
	craftedCredentialDomain = "../../../../../../credentials/store/system/domain/_/credential/prod-deploy-key/config.xml?"
	escapedCraftedDomain    = "..%2F..%2F..%2F..%2F..%2F..%2Fcredentials%2Fstore%2Fsystem%2Fdomain%2F_%2Fcredential%2Fprod-deploy-key%2Fconfig.xml%3F"

	// dotlessCredentialDomain needs no dot-segment: unescaped, it overwrites the
	// credential "victim" in the same store's global domain.
	dotlessCredentialDomain = "_/credential/victim/config.xml?"
	escapedDotlessDomain    = "_%2Fcredential%2Fvictim%2Fconfig.xml%3F"

	teamAStore = "/job/team-a/credentials/store/folder/"

	// listQuery is the query gojenkins adds when listing a domain's credentials.
	listQuery = "tree=credentials%5Bid%5D"
)

// recordedRequest is one request seen by credentialPathRecorder. path is the
// still-escaped path, which is what Jetty resolves dot-segments on (an encoded
// %2F is data, not a separator).
type recordedRequest struct {
	method, path, query string
}

func (r recordedRequest) String() string {
	if r.query == "" {
		return r.method + " " + r.path
	}
	return r.method + " " + r.path + "?" + r.query
}

// credentialPathRecorder starts a Jenkins stand-in that records every request
// except the CSRF-crumb fetch. serve picks each response's status and body;
// nil answers 200 with "{}".
func credentialPathRecorder(t *testing.T, serve func(*http.Request) (int, string)) (*jenkinsAdapter, func() []recordedRequest) {
	t.Helper()
	var mu sync.Mutex
	var seen []recordedRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "crumbIssuer") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"crumb":"abc","crumbRequestField":"Jenkins-Crumb"}`)
			return
		}
		mu.Lock()
		seen = append(seen, recordedRequest{r.Method, r.URL.EscapedPath(), r.URL.RawQuery})
		mu.Unlock()
		code, body := http.StatusOK, `{}`
		if serve != nil {
			code, body = serve(r)
		}
		if strings.HasPrefix(body, "<") {
			w.Header().Set("Content-Type", "application/xml")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	c, err := newJenkinsClient(&Config{ServerURL: srv.URL})
	if err != nil {
		t.Fatalf("newJenkinsClient: %v", err)
	}
	return c, func() []recordedRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]recordedRequest(nil), seen...)
	}
}

// assertRequests compares the recorded requests with want, in order.
func assertRequests(t *testing.T, got []recordedRequest, want ...recordedRequest) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("requests = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestEscapePathSegment(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		wantErr  bool
	}{
		{in: "team", want: "team"},
		{in: "_", want: "_"},
		{in: "my cred", want: "my%20cred"},
		{in: "...", want: "..."},
		{in: "%2e%2e", want: "%252e%252e"},
		{in: "..;x", want: "..%3Bx"},
		{in: "a#b", want: "a%23b"},
		{in: `a\b`, want: "a%5Cb"},
		{in: "a;b", want: "a%3Bb"},
		{in: "a/b?", want: "a%2Fb%3F"},
		{in: craftedCredentialDomain, want: escapedCraftedDomain},
		{in: ".", wantErr: true},
		{in: "..", wantErr: true},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, err := escapePathSegment(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Errorf("escapePathSegment(%q) = %q, want an error", tc.in, got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Errorf("escapePathSegment(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
			}
		})
	}
}

// credentialSchemas returns the schema of every credential resource and data
// source, keyed by type name.
func credentialSchemas(t *testing.T) (map[string]rschema.Schema, map[string]dschema.Schema) {
	t.Helper()
	ctx := context.Background()
	resources := map[string]rschema.Schema{}
	for _, newResource := range []func() resource.Resource{
		newCredentialAzureServicePrincipalResource,
		newCredentialGitHubAppResource,
		newCredentialSecretFileResource,
		newCredentialSecretTextResource,
		newCredentialSSHResource,
		newCredentialUsernameResource,
		newCredentialVaultAppRoleResource,
		newCredentialAwsResource,
		newCredentialCertificateResource,
		newCredentialDomainResource,
	} {
		r := newResource()
		var meta resource.MetadataResponse
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "jenkins"}, &meta)
		var sr resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &sr)
		resources[meta.TypeName] = sr.Schema
	}
	dataSources := map[string]dschema.Schema{}
	for _, newDataSource := range []func() datasource.DataSource{
		newCredentialUsernameDataSource,
		newCredentialVaultAppRoleDataSource,
		newCredentialAwsDataSource,
		newCredentialSecretTextDataSource,
		newCredentialSecretFileDataSource,
		newCredentialSSHDataSource,
		newCredentialCertificateDataSource,
		newCredentialAzureServicePrincipalDataSource,
		newCredentialsDataSource,
	} {
		d := newDataSource()
		var meta datasource.MetadataResponse
		d.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "jenkins"}, &meta)
		var sr datasource.SchemaResponse
		d.Schema(ctx, datasource.SchemaRequest{}, &sr)
		dataSources[meta.TypeName] = sr.Schema
	}
	return resources, dataSources
}

// runValidators runs vs against value the way the framework does at plan time.
func runValidators(vs []validator.String, value string) diag.Diagnostics {
	var diags diag.Diagnostics
	for _, v := range vs {
		var resp validator.StringResponse
		v.ValidateString(context.Background(), validator.StringRequest{Path: fwpath.Root("x"), ConfigValue: types.StringValue(value)}, &resp)
		diags.Append(resp.Diagnostics...)
	}
	return diags
}

func TestCredentialPathValidators(t *testing.T) {
	resources, dataSources := credentialSchemas(t)

	// nameOK is the rule for a credential ID; domainOK for a domain name, where
	// "?" and "#" round-trip through escapePathSegment and stay allowed.
	identifiers := []struct {
		name, value      string
		nameOK, domainOK bool
	}{
		{"default domain", "_", true, true},
		{"plain", "team", true, true},
		{"dashes", "prod-deploy-key", true, true},
		{"space", "my cred", true, true},
		{"inner dot", "a.b", true, true},
		{"leading dot", ".hidden", true, true},
		{"inner dot-dot", "a..b", true, true},
		{"three dots", "...", true, true},
		{"question mark", "who?", false, true},
		{"hash", "C# builds", false, true},
		{"slash", "team/a", false, false},
		{"percent", "50%", false, false},
		{"semicolon", "a;b", false, false},
		{"backslash", `a\b`, false, false},
		{"dot", ".", false, false},
		{"dot-dot", "..", false, false},
		{"crafted", craftedCredentialDomain, false, false},
	}
	folders := []struct {
		name, value string
		ok          bool
	}{
		{"unset-equivalent empty", "", true},
		{"plain", "team-a", true},
		{"nested", "team-a/sub", true},
		{"nested with job", "team-a/job/sub", true},
		{"space", "My Folder", true},
		{"job", "job", false},
		{"slash", "/", false},
		{"job slashes", "/job/", false},
		{"job job", "job/job", false},
		{"question mark", "a?b", false},
		{"hash", "a#b", false},
		{"percent", "a%b", false},
		{"semicolon", "a;b", false},
		{"backslash", `a\b`, false},
		{"dot-dot segment", "team-a/..", false},
		{"dot segment", "./team-a", false},
	}

	check := func(t *testing.T, vs []validator.String, value string, wantOK bool) {
		t.Helper()
		diags := runValidators(vs, value)
		if got := !diags.HasError(); got != wantOK {
			t.Errorf("value %q: accepted = %v, want %v (diags: %v)", value, got, wantOK, diags)
		}
	}

	for typeName, s := range resources {
		t.Run(typeName, func(t *testing.T) {
			attrValidators := func(attr string) []validator.String {
				a, ok := s.Attributes[attr].(rschema.StringAttribute)
				if !ok {
					t.Fatalf("%s.%s is not a string attribute", typeName, attr)
				}
				return a.Validators
			}
			if typeName == "jenkins_credential_domain" {
				name := attrValidators("name")
				for _, tc := range identifiers {
					t.Run("name/"+tc.name, func(t *testing.T) { check(t, name, tc.value, tc.domainOK) })
				}
				t.Run("name/empty", func(t *testing.T) { check(t, name, "", false) })
			} else {
				name, domain := attrValidators("name"), attrValidators("domain")
				for _, tc := range identifiers {
					t.Run("name/"+tc.name, func(t *testing.T) { check(t, name, tc.value, tc.nameOK) })
					t.Run("domain/"+tc.name, func(t *testing.T) { check(t, domain, tc.value, tc.domainOK) })
				}
			}
			folder := attrValidators("folder")
			for _, tc := range folders {
				t.Run("folder/"+tc.name, func(t *testing.T) { check(t, folder, tc.value, tc.ok) })
			}
		})
	}

	for typeName, s := range dataSources {
		t.Run(typeName, func(t *testing.T) {
			a, ok := s.Attributes["folder"].(dschema.StringAttribute)
			if !ok {
				t.Fatalf("%s.folder is not a string attribute", typeName)
			}
			for _, tc := range folders {
				t.Run("folder/"+tc.name, func(t *testing.T) { check(t, a.Validators, tc.value, tc.ok) })
			}
		})
	}
}

// TestCredentialDataSourcePreEncodedWarning: data sources now send domain and
// ID literally, so a value written pre-encoded for the old unescaped URLs gets
// a warning rather than a silent 404.
func TestCredentialDataSourcePreEncodedWarning(t *testing.T) {
	_, dataSources := credentialSchemas(t)
	for typeName, s := range dataSources {
		for _, attr := range []string{"domain", "name"} {
			a, ok := s.Attributes[attr].(dschema.StringAttribute)
			if !ok {
				if attr == "name" && typeName == "jenkins_credentials" {
					continue
				}
				t.Fatalf("%s.%s is not a string attribute", typeName, attr)
			}
			for _, tc := range []struct {
				value        string
				wantWarnings int
			}{
				{"C%23 builds", 1},
				{"C# builds", 0},
				{"_", 0},
				{"50%", 0},
			} {
				t.Run(typeName+"."+attr+"/"+tc.value, func(t *testing.T) {
					diags := runValidators(a.Validators, tc.value)
					if diags.HasError() || diags.WarningsCount() != tc.wantWarnings {
						t.Errorf("value %q: %d errors, %d warnings; want 0 errors, %d warnings (%v)",
							tc.value, diags.ErrorsCount(), diags.WarningsCount(), tc.wantWarnings, diags)
					}
				})
			}
		}
	}
}

// credentialResourceKind builds one credential resource type for
// TestCredentialResourcePathEscaping.
type credentialResourceKind struct {
	name     string
	resource func() resource.Resource
	model    func(folder, domain, name string) any
}

var credentialResourceKinds = []credentialResourceKind{
	{"secret text", newCredentialSecretTextResource, func(folder, domain, name string) any {
		m := covSTModel()
		m.Folder, m.Domain, m.Name = types.StringValue(folder), types.StringValue(domain), types.StringValue(name)
		return m
	}},
	{"certificate", newCredentialCertificateResource, func(folder, domain, name string) any {
		return &credentialCertificateResourceModel{
			Name: types.StringValue(name), Folder: types.StringValue(folder), Domain: types.StringValue(domain),
			Description: types.StringValue("d"), Scope: types.StringValue("GLOBAL"),
			Keystore: types.StringValue("a2V5c3RvcmU="), KeystoreWo: types.StringNull(), KeystoreWoVersion: types.StringNull(),
			Password: types.StringValue("p"),
		}
	}},
}

// runCredentialOp drives one CRUD method of r with model as plan, state and
// config, and returns its diagnostics.
func runCredentialOp(ctx context.Context, t *testing.T, r resource.Resource, op string, model any) diag.Diagnostics {
	t.Helper()
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	s := sr.Schema
	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(ctx, model); d.HasError() {
		t.Fatalf("plan.Set: %v", d)
	}
	state := tfsdk.State{Schema: s, Raw: plan.Raw}
	cfg := tfsdk.Config{Schema: s, Raw: plan.Raw}

	switch op {
	case "create":
		resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
		r.Create(ctx, resource.CreateRequest{Plan: plan, Config: cfg}, &resp)
		return resp.Diagnostics
	case "read":
		resp := resource.ReadResponse{State: state}
		r.Read(ctx, resource.ReadRequest{State: state}, &resp)
		return resp.Diagnostics
	case "update":
		resp := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
		r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state, Config: cfg}, &resp)
		return resp.Diagnostics
	case "delete":
		resp := resource.DeleteResponse{State: state}
		r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)
		return resp.Diagnostics
	}
	t.Fatalf("unknown op %q", op)
	return nil
}

// TestCredentialResourcePathEscaping drives every CRUD method of a credential
// resource on the shared flow (secret text) and of the hand-written certificate
// resource, and checks the exact request each one sends. Plan-time validators
// do not run here, so this covers values that reach the provider without them,
// such as state written before the validators existed.
func TestCredentialResourcePathEscaping(t *testing.T) {
	ctx := context.Background()
	// folderLookup is the GET Create issues to check that the folder exists.
	folderLookup := recordedRequest{method: http.MethodGet, path: "/job/team-a/api/json"}

	// wantPath is the request each op sends for an escaped domain d and ID n.
	wantRequest := func(op, d, n string) recordedRequest {
		base := teamAStore + "domain/" + d + "/"
		switch op {
		case "create":
			return recordedRequest{method: http.MethodPost, path: base + "createCredentials"}
		case "read":
			return recordedRequest{method: http.MethodGet, path: base + "credential/" + n + "/config.xml/"}
		case "update":
			return recordedRequest{method: http.MethodPost, path: base + "credential/" + n + "/config.xml"}
		default:
			return recordedRequest{method: http.MethodPost, path: base + "credential/" + n + "/doDelete"}
		}
	}

	cases := []struct {
		name, folder, domain, id string
		// escDomain and escID are the expected path segments; empty means the
		// op must fail before sending any request.
		escDomain, escID string
		// errSummary, when set, is the expected diagnostic summary.
		errSummary string
	}{
		{name: "benign", folder: "team-a", domain: "team", id: "x", escDomain: "team", escID: "x"},
		{name: "crafted domain", folder: "team-a", domain: craftedCredentialDomain, id: "x", escDomain: escapedCraftedDomain, escID: "x"},
		{name: "dotless domain", folder: "team-a", domain: dotlessCredentialDomain, id: "x", escDomain: escapedDotlessDomain, escID: "x"},
		{name: "question mark id", folder: "team-a", domain: "_", id: "a?b", escDomain: "_", escID: "a%3Fb"},
		{name: "dot-dot id", folder: "team-a", domain: "team", id: ".."},
		{name: "dot id", folder: "team-a", domain: "team", id: "."},
		{name: "dot-dot domain", folder: "team-a", domain: "..", id: "x"},
		{name: "folder job", folder: "job", domain: "_", id: "x", errSummary: "Invalid Folder"},
		{name: "folder slash", folder: "/", domain: "_", id: "x", errSummary: "Invalid Folder"},
		{name: "folder job slashes", folder: "/job/", domain: "_", id: "x", errSummary: "Invalid Folder"},
		{name: "folder job job", folder: "job/job", domain: "_", id: "x", errSummary: "Invalid Folder"},
		{name: "folder question mark", folder: "team-a?x", domain: "_", id: "x", errSummary: "Invalid Folder"},
		{name: "folder dot-dot", folder: "team-a/..", domain: "_", id: "x", errSummary: "Invalid Folder"},
	}

	for _, kind := range credentialResourceKinds {
		for _, op := range []string{"create", "read", "update", "delete"} {
			for _, tc := range cases {
				t.Run(kind.name+"/"+op+"/"+tc.name, func(t *testing.T) {
					// Read gets a 404 so it ends cleanly by dropping the resource.
					client, requests := credentialPathRecorder(t, func(r *http.Request) (int, string) {
						if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/config.xml/") {
							return http.StatusNotFound, ""
						}
						return http.StatusOK, `{}`
					})
					r := kind.resource()
					var cr resource.ConfigureResponse
					r.(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: client}, &cr)

					diags := runCredentialOp(ctx, t, r, op, kind.model(tc.folder, tc.domain, tc.id))
					got := requests()

					if tc.escDomain == "" {
						if !diags.HasError() {
							t.Errorf("want an error, got none")
						}
						if tc.errSummary != "" && (diags.HasError() && diags.Errors()[0].Summary() != tc.errSummary) {
							t.Errorf("error summary = %q, want %q", diags.Errors()[0].Summary(), tc.errSummary)
						}
						assertRequests(t, got)
						return
					}
					if diags.HasError() {
						t.Fatalf("%s: %v", op, diags)
					}
					want := wantRequest(op, tc.escDomain, tc.escID)
					if op == "create" {
						assertRequests(t, got, folderLookup, want)
					} else {
						assertRequests(t, got, want)
					}
				})
			}
		}
	}
}

// TestCredentialDataSourcePathEscaping: data sources have no plan-time checks
// on domain or ID, so runtime escaping is their only defense.
func TestCredentialDataSourcePathEscaping(t *testing.T) {
	ctx := context.Background()

	type dataSourceKind struct {
		name  string
		new   func() datasource.DataSource
		model func(folder, domain, id string) any
		// path and query are the GET the data source sends for escaped domain d
		// and ID n.
		path  func(d, n string) string
		query string
	}
	kinds := []dataSourceKind{
		{"credentials list", newCredentialsDataSource, func(folder, domain, _ string) any {
			return &credentialsDataSourceModel{
				ID: types.StringNull(), Folder: types.StringValue(folder), Domain: types.StringValue(domain),
				Credentials: types.SetNull(types.StringType),
			}
		}, func(d, _ string) string { return teamAStore + "domain/" + d + "/api/json/" }, listQuery},
		{"username", newCredentialUsernameDataSource, func(folder, domain, id string) any {
			return &credentialUsernameDataSourceModel{
				ID: types.StringNull(), Name: types.StringValue(id), Folder: types.StringValue(folder),
				Domain: types.StringValue(domain), Description: types.StringNull(), Scope: types.StringNull(),
				Username: types.StringNull(),
			}
		}, func(d, n string) string { return teamAStore + "domain/" + d + "/credential/" + n + "/config.xml/" }, ""},
	}

	const climbing = "../../../../../../credentials/store/system/domain/_"
	cases := []struct {
		name, folder, domain, id string
		escDomain, escID         string
	}{
		{name: "benign", folder: "team-a", domain: "team", id: "prod-deploy-key", escDomain: "team", escID: "prod-deploy-key"},
		{name: "climbing domain", folder: "team-a", domain: climbing, id: "prod-deploy-key",
			escDomain: "..%2F..%2F..%2F..%2F..%2F..%2Fcredentials%2Fstore%2Fsystem%2Fdomain%2F_", escID: "prod-deploy-key"},
		{name: "hash domain", folder: "team-a", domain: "C# builds", id: "prod-deploy-key", escDomain: "C%23%20builds", escID: "prod-deploy-key"},
		{name: "dot domain", folder: "team-a", domain: ".", id: "prod-deploy-key"},
		{name: "dot-dot domain", folder: "team-a", domain: "..", id: "prod-deploy-key"},
		{name: "folder job", folder: "job", domain: "_", id: "prod-deploy-key"},
	}

	for _, kind := range kinds {
		for _, tc := range cases {
			t.Run(kind.name+"/"+tc.name, func(t *testing.T) {
				client, requests := credentialPathRecorder(t, nil)
				diags := readCredentialDataSource(ctx, t, kind.new(), client, kind.model(tc.folder, tc.domain, tc.id))
				got := requests()
				if tc.escDomain == "" {
					if !diags.HasError() {
						t.Errorf("want an error, got none")
					}
					assertRequests(t, got)
					return
				}
				assertRequests(t, got, recordedRequest{method: http.MethodGet, path: kind.path(tc.escDomain, tc.escID), query: kind.query})
			})
		}
	}

	// The username source also escapes the credential ID.
	for _, id := range []string{".", ".."} {
		t.Run("username/id "+id, func(t *testing.T) {
			client, requests := credentialPathRecorder(t, nil)
			diags := readCredentialDataSource(ctx, t, newCredentialUsernameDataSource(), client, kinds[1].model("team-a", "_", id))
			if !diags.HasError() {
				t.Errorf("want an error, got none")
			}
			assertRequests(t, requests())
		})
	}
}

// TestCredentialDataSourceLiteralDomain: a domain is now sent literally, so
// the real name works and the old pre-encoded workaround no longer matches.
func TestCredentialDataSourceLiteralDomain(t *testing.T) {
	ctx := context.Background()
	const served = teamAStore + "domain/C%23%20builds/"
	for _, tc := range []struct {
		domain, wantPath string
		wantErr          bool
	}{
		{"C# builds", served + "api/json/", false},
		{"C%23 builds", teamAStore + "domain/C%2523%20builds/api/json/", true},
	} {
		t.Run(tc.domain, func(t *testing.T) {
			client, requests := credentialPathRecorder(t, func(r *http.Request) (int, string) {
				if strings.HasPrefix(r.URL.EscapedPath(), served) {
					return http.StatusOK, `{"credentials":[{"id":"a"}]}`
				}
				return http.StatusNotFound, ""
			})
			model := &credentialsDataSourceModel{
				ID: types.StringNull(), Folder: types.StringValue("team-a"), Domain: types.StringValue(tc.domain),
				Credentials: types.SetNull(types.StringType),
			}
			diags := readCredentialDataSource(ctx, t, newCredentialsDataSource(), client, model)
			if diags.HasError() != tc.wantErr {
				t.Errorf("error = %v, want %v (%v)", diags.HasError(), tc.wantErr, diags)
			}
			assertRequests(t, requests(), recordedRequest{method: http.MethodGet, path: tc.wantPath, query: listQuery})
		})
	}
}

// readCredentialDataSource configures d with client and reads it with model as
// its config.
func readCredentialDataSource(ctx context.Context, t *testing.T, d datasource.DataSource, client *jenkinsAdapter, model any) diag.Diagnostics {
	t.Helper()
	var cr datasource.ConfigureResponse
	d.(datasource.DataSourceWithConfigure).Configure(ctx, datasource.ConfigureRequest{ProviderData: client}, &cr)
	var sr datasource.SchemaResponse
	d.Schema(ctx, datasource.SchemaRequest{}, &sr)
	cfg := tfsdk.State{Schema: sr.Schema}
	if diags := cfg.Set(ctx, model); diags.HasError() {
		t.Fatalf("config.Set: %v", diags)
	}
	resp := datasource.ReadResponse{State: tfsdk.State{Schema: sr.Schema}}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: sr.Schema, Raw: cfg.Raw}}, &resp)
	return resp.Diagnostics
}

// TestCredentialDomainPathEscaping covers the provider's own credential-domain
// URLs: the domain name must stay one path segment of the folder's store, and
// "?" and "#" must reach Jenkins as part of the name.
func TestCredentialDomainPathEscaping(t *testing.T) {
	ctx := context.Background()
	const domainBase = teamAStore + "domain/"
	const domainXML = `<com.cloudbees.plugins.credentials.domains.Domain><name>n</name></com.cloudbees.plugins.credentials.domains.Domain>`

	ops := []struct {
		name   string
		call   func(*jenkinsAdapter, string) error
		method string
		suffix string
	}{
		{"get", func(c *jenkinsAdapter, name string) error {
			var out credentialDomainXML
			return c.GetCredentialDomain(ctx, "team-a", name, &out)
		}, http.MethodGet, "/config.xml"},
		{"update", func(c *jenkinsAdapter, name string) error {
			return c.UpdateCredentialDomain(ctx, "team-a", name, "d")
		}, http.MethodPost, "/config.xml"},
		{"delete", func(c *jenkinsAdapter, name string) error {
			return c.DeleteCredentialDomain(ctx, "team-a", name)
		}, http.MethodPost, "/doDelete"},
	}
	for _, op := range ops {
		for _, tc := range []struct {
			name, domain, escaped string
		}{
			{"benign", "team", "team"},
			{"crafted", "../../../credentials/store/system/domain/prod?", "..%2F..%2F..%2Fcredentials%2Fstore%2Fsystem%2Fdomain%2Fprod%3F"},
			{"hash", "C# builds", "C%23%20builds"},
			{"question mark", "who?", "who%3F"},
			{"dot", ".", ""},
			{"dot-dot", "..", ""},
		} {
			t.Run(op.name+"/"+tc.name, func(t *testing.T) {
				client, requests := credentialPathRecorder(t, func(r *http.Request) (int, string) {
					if r.Method == http.MethodGet {
						return http.StatusOK, domainXML
					}
					return http.StatusOK, `{}`
				})
				err := op.call(client, tc.domain)
				if tc.escaped == "" {
					if err == nil {
						t.Errorf("domain %q: want an error, got none", tc.domain)
					}
					assertRequests(t, requests())
					return
				}
				if err != nil {
					t.Fatalf("domain %q: %v", tc.domain, err)
				}
				assertRequests(t, requests(), recordedRequest{method: op.method, path: domainBase + tc.escaped + op.suffix})
			})
		}
	}
}
