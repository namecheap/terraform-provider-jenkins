package jenkins

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	fwpath "github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Regression tests for issue #250: credential domains and IDs are interpolated
// into credentials-store URLs, so a value such as "../../..." or one ending in
// "?" sent a folder-scoped request to a different store.

// craftedCredentialDomain is the issue #250 reproduction: from a folder store it
// climbs to the system store and overwrites the credential prod-deploy-key; the
// trailing "?" turns the rest of the gojenkins-built path into a query string.
const craftedCredentialDomain = "../../../../../../credentials/store/system/domain/_/credential/prod-deploy-key/config.xml?"

// credentialPathRecorder starts a Jenkins stand-in that answers every request
// with 200 and records the still-escaped path of each POST, so a test can
// resolve dot-segments the way Jetty does (an encoded %2F is data, not a
// separator).
func credentialPathRecorder(t *testing.T) (*jenkinsAdapter, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var posted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			mu.Lock()
			posted = append(posted, r.URL.EscapedPath())
			mu.Unlock()
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "crumbIssuer") {
			_, _ = io.WriteString(w, `{"crumb":"abc","crumbRequestField":"Jenkins-Crumb"}`)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(srv.Close)
	c, err := newJenkinsClient(&Config{ServerURL: srv.URL})
	if err != nil {
		t.Fatalf("newJenkinsClient: %v", err)
	}
	return c, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), posted...)
	}
}

func TestCredentialPathValidators(t *testing.T) {
	ctx := context.Background()

	type pathCase struct {
		name  string
		value string
		ok    bool
	}
	values := []pathCase{
		{"default domain", "_", true},
		{"plain", "team", true},
		{"dashes", "prod-deploy-key", true},
		{"space", "my cred", true},
		{"inner dot", "a.b", true},
		{"leading dot", ".hidden", true},
		{"inner dot-dot", "a..b", true},
		{"three dots", "...", true},
		{"slash", "team/a", false},
		{"question mark", "a?", false},
		{"hash", "a#b", false},
		{"percent", "50%", false},
		{"semicolon", "a;b", false},
		{"backslash", `a\b`, false},
		{"dot", ".", false},
		{"dot-dot", "..", false},
		{"crafted", craftedCredentialDomain, false},
	}

	// Every credential resource gets domain and name from schemaCredential; the
	// domain resource has its own name attribute, which must not be empty.
	type target struct {
		resource string
		attr     string
		emptyOK  bool
		schema   rschema.Schema
	}
	var targets []target
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
		if meta.TypeName == "jenkins_credential_domain" {
			targets = append(targets, target{meta.TypeName, "name", false, sr.Schema})
			continue
		}
		targets = append(targets, target{meta.TypeName, "domain", true, sr.Schema}, target{meta.TypeName, "name", true, sr.Schema})
	}

	validate := func(vs []validator.String, v string) diag.Diagnostics {
		var diags diag.Diagnostics
		for _, vv := range vs {
			var resp validator.StringResponse
			vv.ValidateString(ctx, validator.StringRequest{Path: fwpath.Root("x"), ConfigValue: types.StringValue(v)}, &resp)
			diags.Append(resp.Diagnostics...)
		}
		return diags
	}

	for _, tg := range targets {
		attr, ok := tg.schema.Attributes[tg.attr].(rschema.StringAttribute)
		if !ok {
			t.Fatalf("%s.%s is not a string attribute", tg.resource, tg.attr)
		}
		t.Run(tg.resource+"."+tg.attr, func(t *testing.T) {
			cases := append(values[:len(values):len(values)], pathCase{"empty", "", tg.emptyOK})
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					diags := validate(attr.Validators, tc.value)
					if got := !diags.HasError(); got != tc.ok {
						t.Errorf("value %q: accepted = %v, want %v (diags: %v)", tc.value, got, tc.ok, diags)
					}
				})
			}
		})
	}
}

// TestCredentialCreateStaysInFolderStore drives a folder-scoped credential
// create through the resource and checks where the POST lands once the server
// has resolved its dot-segments. Plan-time validators do not run here, so this
// covers values that reach Create without them.
func TestCredentialCreateStaysInFolderStore(t *testing.T) {
	ctx := context.Background()
	const store = "/job/team-a/credentials/store/folder/"

	for _, tc := range []struct {
		name    string
		domain  string
		wantErr bool
	}{
		{"benign domain", "team", false},
		{"crafted domain", craftedCredentialDomain, false},
		{"dot-dot domain", "..", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, posted := credentialPathRecorder(t)
			r, s := covSTResource(client)
			model := covSTModel()
			model.Folder = types.StringValue("team-a")
			model.Domain = types.StringValue(tc.domain)
			model.Name = types.StringValue("prod-deploy-key")

			plan := tfsdk.Plan{Schema: s}
			if d := plan.Set(ctx, model); d.HasError() {
				t.Fatalf("plan.Set: %v", d)
			}
			resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
			r.Create(ctx, resource.CreateRequest{Plan: plan, Config: tfsdk.Config{Schema: s, Raw: plan.Raw}}, &resp)

			if tc.wantErr {
				if !resp.Diagnostics.HasError() {
					t.Errorf("Create with domain %q: want an error, got none", tc.domain)
				}
				if got := posted(); len(got) != 0 {
					t.Errorf("Create with domain %q sent %v, want no POST", tc.domain, got)
				}
				return
			}
			if resp.Diagnostics.HasError() {
				t.Fatalf("Create: %v", resp.Diagnostics)
			}
			got := posted()
			if len(got) != 1 {
				t.Fatalf("want one POST, got %v", got)
			}
			if effective := path.Clean(got[0]); !strings.HasPrefix(effective, store) {
				t.Errorf("folder-scoped credential create reached %s (raw %s), want a path under %s", effective, got[0], store)
			}
		})
	}
}

// TestCredentialDomainPathEscaping covers the provider's own credential-domain
// URLs: the domain name must stay one path segment of the folder's store.
func TestCredentialDomainPathEscaping(t *testing.T) {
	ctx := context.Background()
	const domainBase = "/job/team-a/credentials/store/folder/domain/"

	ops := []struct {
		name   string
		call   func(*jenkinsAdapter, string) error
		suffix string
	}{
		{"update", func(c *jenkinsAdapter, name string) error {
			return c.UpdateCredentialDomain(ctx, "team-a", name, "d")
		}, "/config.xml"},
		{"delete", func(c *jenkinsAdapter, name string) error {
			return c.DeleteCredentialDomain(ctx, "team-a", name)
		}, "/doDelete"},
	}
	for _, op := range ops {
		for _, tc := range []struct {
			name     string
			domain   string
			wantPath string
		}{
			{"benign", "team", domainBase + "team" + op.suffix},
			{"crafted", "../../../credentials/store/system/domain/prod?", domainBase + "..%2F..%2F..%2Fcredentials%2Fstore%2Fsystem%2Fdomain%2Fprod%3F" + op.suffix},
			{"dot", ".", ""},
			{"dot-dot", "..", ""},
		} {
			t.Run(op.name+"/"+tc.name, func(t *testing.T) {
				client, posted := credentialPathRecorder(t)
				err := op.call(client, tc.domain)
				if tc.wantPath == "" {
					if err == nil {
						t.Errorf("domain %q: want an error, got none", tc.domain)
					}
					if got := posted(); len(got) != 0 {
						t.Errorf("domain %q sent %v, want no POST", tc.domain, got)
					}
					return
				}
				if err != nil {
					t.Fatalf("domain %q: %v", tc.domain, err)
				}
				got := posted()
				if len(got) != 1 || got[0] != tc.wantPath {
					t.Fatalf("domain %q posted %v, want [%s]", tc.domain, got, tc.wantPath)
				}
				if effective := path.Clean(got[0]); effective != tc.wantPath {
					t.Errorf("domain %q resolves to %s, want %s", tc.domain, effective, tc.wantPath)
				}
			})
		}
	}
}
