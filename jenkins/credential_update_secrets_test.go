package jenkins

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// Tests for credential updates erasing secrets. Jenkins' config.xml update
// replaces the whole credential: a secret element that is left out, empty or
// <secret-redacted/> is stored as empty. Update used to send only the secrets
// whose value changed, so any other change (a new description, say) erased the
// stored secret.

// storedCredentialXML is a config.xml as Jenkins returns it on GET: the secrets
// it holds are redacted, and a secret it does not hold has no element.
func storedCredentialXML(root string, secrets ...string) string {
	var b strings.Builder
	b.WriteString("<" + root + "><scope>GLOBAL</scope><id>x</id><description>old</description>")
	for _, s := range secrets {
		b.WriteString("<" + s + "><secret-redacted/></" + s + ">")
	}
	b.WriteString("</" + root + ">")
	return b.String()
}

type recordedBody struct {
	method, path, body string
}

// credentialStoreFake is a stand-in for a Jenkins credential store. It records
// every request with its body (the crumb fetch excepted). A GET of a
// credential's config.xml answers stored. When stateful, a create or update
// POST replaces stored with the posted credential and a GET returns it the way
// the credentials plugin does: every secret element the credential has is
// rendered as <secret-redacted/>, even an empty one. XStream reads an empty
// element such as <password></password> as a non-null, empty Secret, and the
// redacting GET writes every non-null Secret; only an element left out of the
// post (SSH's empty passphrase) stays null and has no element. A non-zero
// getStatus answers every GET with that status instead.
type credentialStoreFake struct {
	mu        sync.Mutex
	stateful  bool
	stored    string
	getStatus int
	seen      []recordedBody
}

// fakeSecretElements are the Secret-typed elements of the credentials under test.
var fakeSecretElements = func() []*regexp.Regexp {
	var res []*regexp.Regexp
	for _, el := range []string{"password", "passphrase", "secretId", "secret", "privateKey", "secretBytes", "clientSecret", "uploadedKeystore", "secretKey"} {
		res = append(res, regexp.MustCompile(`<`+el+`>[^<]*</`+el+`>|<`+el+`/>`))
	}
	return res
}()

func redactSecrets(credential string) string {
	for _, re := range fakeSecretElements {
		credential = re.ReplaceAllStringFunc(credential, func(m string) string {
			el := strings.TrimSuffix(strings.SplitN(m[1:], ">", 2)[0], "/")
			return "<" + el + "><secret-redacted/></" + el + ">"
		})
	}
	return credential
}

func (f *credentialStoreFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.URL.Path, "crumbIssuer") {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"crumb":"abc","crumbRequestField":"Jenkins-Crumb"}`)
		return
	}
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, recordedBody{method: r.Method, path: r.URL.EscapedPath(), body: string(body)})
	if r.Method == http.MethodGet && f.getStatus != 0 {
		w.WriteHeader(f.getStatus)
		return
	}
	if r.Method == http.MethodGet {
		stored := f.stored
		if f.stateful {
			stored = redactSecrets(stored)
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, stored)
		return
	}
	if f.stateful {
		f.stored = string(body)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{}`)
}

func (f *credentialStoreFake) requests() []recordedBody {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recordedBody(nil), f.seen...)
}

// startCredentialStoreFake serves f and returns a client for it.
func startCredentialStoreFake(t *testing.T, f *credentialStoreFake) *jenkinsAdapter {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := newJenkinsClient(&Config{ServerURL: srv.URL})
	if err != nil {
		t.Fatalf("newJenkinsClient: %v", err)
	}
	return c
}

// configuredCredentialResource returns a resource from newResource configured
// with client, and its schema.
func configuredCredentialResource(ctx context.Context, t *testing.T, newResource func() resource.Resource, client *jenkinsAdapter) (resource.Resource, schema.Schema) {
	t.Helper()
	r := newResource()
	var cr resource.ConfigureResponse
	r.(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: client}, &cr)
	if cr.Diagnostics.HasError() {
		t.Fatalf("Configure: %v", cr.Diagnostics)
	}
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	return r, sr.Schema
}

// sentElements returns the direct children of the posted credential, keyed by
// element name, with their inner XML.
func sentElements(t *testing.T, body string) map[string]string {
	t.Helper()
	var doc struct {
		Children []struct {
			XMLName xml.Name
			Inner   string `xml:",innerxml"`
		} `xml:",any"`
	}
	if err := xml.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("posted body is not XML: %v\n%s", err, body)
	}
	out := make(map[string]string, len(doc.Children))
	for _, c := range doc.Children {
		out[c.XMLName.Local] = c.Inner
	}
	return out
}

// toPlan builds a plan holding model under s.
func toPlan(ctx context.Context, t *testing.T, s schema.Schema, model any) tfsdk.Plan {
	t.Helper()
	p := tfsdk.Plan{Schema: s}
	if d := p.Set(ctx, model); d.HasError() {
		t.Fatalf("plan.Set: %v", d)
	}
	return p
}

// runCredentialUpdate drives r.Update with plan, config and prior state built
// from the given models (config carries write-only values; plan and state never
// do). A nil state means the plan with description "old": a description-only
// change.
func runCredentialUpdate(ctx context.Context, t *testing.T, r resource.Resource, s schema.Schema, plan, config, state any) resource.UpdateResponse {
	t.Helper()
	p := toPlan(ctx, t, s, plan)
	prior := tfsdk.State{Schema: s, Raw: p.Raw.Copy()}
	if state != nil {
		prior.Raw = toPlan(ctx, t, s, state).Raw
	} else if d := prior.SetAttribute(ctx, path.Root("description"), "old"); d.HasError() {
		t.Fatalf("state.SetAttribute: %v", d)
	}
	resp := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	r.Update(ctx, resource.UpdateRequest{
		Plan:   p,
		State:  prior,
		Config: tfsdk.Config{Schema: s, Raw: toPlan(ctx, t, s, config).Raw},
	}, &resp)
	return resp
}

// checkSingleError checks that diags holds exactly one error, with the given
// summary and, when attr is set, attached to that attribute and naming it.
func checkSingleError(t *testing.T, diags diag.Diagnostics, summary, attr string) {
	t.Helper()
	errs := diags.Errors()
	if len(errs) != 1 || errs[0].Summary() != summary {
		t.Fatalf("diagnostics = %v, want one %q error", diags, summary)
	}
	if attr == "" {
		return
	}
	if dp, ok := errs[0].(diag.DiagnosticWithPath); !ok {
		t.Errorf("error has no attribute path, want %q", attr)
	} else if !dp.Path().Equal(path.Root(attr)) {
		t.Errorf("error path = %s, want %q", dp.Path(), attr)
	}
	if !strings.Contains(errs[0].Detail(), `"`+attr+`"`) {
		t.Errorf("error detail %q does not name %q", errs[0].Detail(), attr)
	}
}

// checkPosted checks the posted credential body: wantSent maps direct children
// to their exact inner XML, wantUnsent lists children it must not have, and
// wantInBody lists substrings it must contain (for nested elements).
func checkPosted(t *testing.T, body string, wantSent map[string]string, wantUnsent, wantInBody []string) {
	t.Helper()
	sent := sentElements(t, body)
	for el, v := range wantSent {
		if gotV, ok := sent[el]; !ok {
			t.Errorf("posted credential has no <%s>, want %q", el, v)
		} else if gotV != v {
			t.Errorf("posted <%s> = %q, want %q", el, gotV, v)
		}
	}
	for _, el := range wantUnsent {
		if _, ok := sent[el]; ok {
			t.Errorf("posted credential has <%s>, want none", el)
		}
	}
	for _, sub := range wantInBody {
		if !strings.Contains(body, sub) {
			t.Errorf("posted credential lacks %q:\n%s", sub, body)
		}
	}
}

// withNewDescription returns raw, an object of schema s, with description "new".
func withNewDescription(ctx context.Context, t *testing.T, s schema.Schema, raw tftypes.Value) tftypes.Value {
	t.Helper()
	p := tfsdk.Plan{Schema: s, Raw: raw.Copy()}
	if d := p.SetAttribute(ctx, path.Root("description"), "new"); d.HasError() {
		t.Fatalf("SetAttribute: %v", d)
	}
	return p.Raw
}

func requestLines(reqs []recordedBody) []string {
	var out []string
	for _, r := range reqs {
		out = append(out, r.method+" "+r.path)
	}
	return out
}

const (
	eraseErr    = "Update Would Erase Credential Secret"
	notKnownErr = "Credential Secret Not Known to Terraform"
	updateErr   = "Unable to Update Resource"

	usernameRoot = "com.cloudbees.plugins.credentials.impl.UsernamePasswordCredentialsImpl"
	sshRoot      = "com.cloudbees.jenkins.plugins.sshcredentials.impl.BasicSSHUserPrivateKey"
	certRoot     = "com.cloudbees.plugins.credentials.impl.CertificateCredentialsImpl"
	vaultRoot    = "com.datapipe.jenkins.vault.credentials.VaultAppRoleCredential"
	azureRoot    = "com.microsoft.azure.util.AzureCredentials"
)

func TestCredentialUpdateResendsSecrets(t *testing.T) {
	ctx := context.Background()
	str, null := types.StringValue, types.StringNull()

	type tc struct {
		name     string
		resource func() resource.Resource
		// plan is the planned model. config is the configuration (it carries
		// write-only values); nil means config equals plan. state is the prior
		// state; nil means the plan with description "old" (a description-only
		// change).
		plan, config, state any
		// folder is the credential's folder ("" for the system store) and
		// escDomain the escaped domain in request paths ("_" when empty).
		folder, escDomain string
		// stored is the GET config.xml answer; it is only requested for an
		// optional secret that neither the configuration nor state sets.
		// getStatus, when set, is the GET status instead.
		stored    string
		getStatus int
		// wantGet: Update must read the stored credential first.
		wantGet bool
		// wantErr: Update must fail with this summary without posting;
		// wantErrAttr is the attribute the error must be attached to and name.
		wantErr, wantErrAttr string
		// wantSent, wantUnsent, wantInBody: see checkPosted.
		wantSent   map[string]string
		wantUnsent []string
		wantInBody []string
	}

	secretText := func(secret, wo, ver types.String) *credentialSecretTextResourceModel {
		return &credentialSecretTextResourceModel{
			Name: str("x"), Folder: null, Description: str("new"), Domain: str("_"), Scope: str("GLOBAL"),
			Secret: secret, SecretWo: wo, SecretWoVersion: ver,
		}
	}
	usernameIn := func(domain string, password, wo, ver types.String) *credentialUsernameResourceModel {
		return &credentialUsernameResourceModel{
			Name: str("x"), Folder: null, Description: str("new"), Domain: str(domain), Scope: str("GLOBAL"),
			Username: str("u"), Password: password, PasswordWo: wo, PasswordWoVersion: ver,
		}
	}
	username := func(password, wo, ver types.String) *credentialUsernameResourceModel {
		return usernameIn("_", password, wo, ver)
	}
	sshKey := func(key, passphrase types.String) *credentialSSHResourceModel {
		return &credentialSSHResourceModel{
			Name: str("x"), Folder: null, Description: str("new"), Domain: str("_"), Scope: str("GLOBAL"),
			Username: str("u"), PrivateKey: key, PrivateKeyWo: null, PrivateKeyWoVersion: null,
			Passphrase: passphrase,
		}
	}
	ssh := func(passphrase types.String) *credentialSSHResourceModel { return sshKey(str("KEY"), passphrase) }
	certificate := func(keystore, wo, ver, password types.String) *credentialCertificateResourceModel {
		return &credentialCertificateResourceModel{
			Name: str("x"), Folder: null, Description: str("new"), Domain: str("_"), Scope: str("GLOBAL"),
			Keystore: keystore, KeystoreWo: wo, KeystoreWoVersion: ver, Password: password,
		}
	}
	vault := func(secretID types.String) *credentialVaultAppRoleResourceModel {
		return &credentialVaultAppRoleResourceModel{
			Name: str("x"), Folder: null, Description: str("new"), Domain: str("_"), Scope: str("GLOBAL"),
			Namespace: str(""), Path: str("approle"), RoleID: str("role"),
			SecretID: secretID, SecretIDWo: null, SecretIDWoVersion: null,
		}
	}
	secretFile := func(content types.String) *credentialSecretFileResourceModel {
		return &credentialSecretFileResourceModel{
			Name: str("x"), Folder: null, Description: str("new"), Domain: str("_"), Scope: str("GLOBAL"),
			Filename: str("f"), SecretBytes: content, SecretBytesWo: null, SecretBytesWoVersion: null,
		}
	}
	aws := func(secretKey types.String) *credentialAwsResourceModel {
		return &credentialAwsResourceModel{
			Name: str("x"), Folder: null, Description: str("new"), Domain: str("_"), Scope: str("GLOBAL"),
			AccessKey: str("ak"), SecretKey: secretKey, SecretKeyWo: null, SecretKeyWoVersion: null,
			IamRoleArn: str(""), IamMfaSerialNumber: str(""),
		}
	}
	azure := func(clientSecret, certificateID types.String) *credentialAzureServicePrincipalResourceModel {
		return &credentialAzureServicePrincipalResourceModel{
			Name: str("x"), Folder: null, Description: str("new"), Domain: str("_"), Scope: str("GLOBAL"),
			SubscriptionId: str("sub"), ClientId: str("cid"), ClientSecret: clientSecret, ClientSecretWo: null,
			ClientSecretWoVersion: null, CertificateId: certificateID, Tenant: str("t"), AzureEnvironmentName: str("Azure"),
			ServiceManagementURL: str(""), AuthenticationEndpoint: str(""), ResourceManagerEndpoint: str(""), GraphEndpoint: str(""),
		}
	}
	githubApp := func(key types.String) *credentialGitHubAppResourceModel {
		return &credentialGitHubAppResourceModel{
			Name: str("x"), Folder: null, Description: str("new"), Domain: str("_"), Scope: str("GLOBAL"),
			AppID: str("1"), PrivateKey: key, PrivateKeyWo: null, PrivateKeyWoVersion: null,
		}
	}
	cases := []tc{
		// Every secret Terraform knows is re-sent.
		{
			name: "secret text/description-only update re-sends the plain secret", resource: newCredentialSecretTextResource,
			plan:     secretText(str("s3cret"), null, null),
			wantSent: map[string]string{"description": "new", "secret": "s3cret"},
		},
		{
			name: "secret text/description-only update re-sends the write-only secret", resource: newCredentialSecretTextResource,
			plan:     secretText(null, null, str("1")),
			config:   secretText(null, str("wo-s3cret"), str("1")),
			wantSent: map[string]string{"description": "new", "secret": "wo-s3cret"},
		},
		{
			name: "username/description-only update re-sends the plain password", resource: newCredentialUsernameResource,
			plan:     username(str("pw"), null, null),
			wantSent: map[string]string{"description": "new", "password": "pw"},
		},
		{
			name: "username/description-only update re-sends the write-only password", resource: newCredentialUsernameResource,
			plan:     username(null, null, str("1")),
			config:   username(null, str("wo-pw"), str("1")),
			wantSent: map[string]string{"description": "new", "password": "wo-pw"},
		},
		{
			name: "username/explicitly empty password is sent without a check", resource: newCredentialUsernameResource,
			plan:     username(str(""), null, null),
			wantSent: map[string]string{"description": "new", "password": ""},
		},
		{
			name: "ssh/description-only update re-sends the passphrase", resource: newCredentialSSHResource,
			plan:       ssh(str("pp")),
			wantSent:   map[string]string{"description": "new", "passphrase": "pp"},
			wantInBody: []string{"<privateKey>KEY</privateKey>"},
		},
		{
			name: "certificate/description-only update re-sends keystore and password", resource: newCredentialCertificateResource,
			plan:       certificate(str("S1M="), null, null, str("pw")),
			wantSent:   map[string]string{"description": "new", "password": "pw"},
			wantInBody: []string{"<uploadedKeystore>S1M=</uploadedKeystore>"},
		},
		{
			name: "certificate/description-only update re-sends the write-only keystore", resource: newCredentialCertificateResource,
			plan:       certificate(null, null, str("1"), str("pw")),
			config:     certificate(null, str("V08="), str("1"), str("pw")),
			wantSent:   map[string]string{"description": "new", "password": "pw"},
			wantInBody: []string{"<uploadedKeystore>V08=</uploadedKeystore>"},
		},
		{
			name: "vault approle/description-only update re-sends the secret id", resource: newCredentialVaultAppRoleResource,
			plan:     vault(str("sid")),
			wantSent: map[string]string{"description": "new", "secretId": "sid"},
		},
		{
			name: "secret file/description-only update re-sends the content", resource: newCredentialSecretFileResource,
			plan:     secretFile(str("Ynl0ZXM=")),
			wantSent: map[string]string{"description": "new", "secretBytes": "Ynl0ZXM="},
		},
		{
			name: "aws/description-only update re-sends the secret key", resource: newCredentialAwsResource,
			plan:     aws(str("sk")),
			wantSent: map[string]string{"description": "new", "secretKey": "sk"},
		},
		{
			name: "azure/description-only update re-sends the client secret", resource: newCredentialAzureServicePrincipalResource,
			plan:       azure(str("cs"), null),
			wantSent:   map[string]string{"description": "new"},
			wantInBody: []string{"<clientSecret>cs</clientSecret>"},
		},
		{
			name: "github app/description-only update re-sends the private key", resource: newCredentialGitHubAppResource,
			plan:     githubApp(str("PEM")),
			wantSent: map[string]string{"description": "new", "privateKey": "PEM"},
		},
		{
			name: "username/whitespace-only password is re-sent verbatim without a check", resource: newCredentialUsernameResource,
			plan:     username(str("  "), null, null),
			wantSent: map[string]string{"description": "new", "password": "  "},
		},
		{
			// Under ignore_changes the plan keeps the value from state.
			name: "secret text/ignored secret re-sends the applied value, not the configured one", resource: newCredentialSecretTextResource,
			plan:     secretText(str("applied"), null, null),
			config:   secretText(str("configured"), null, null),
			wantSent: map[string]string{"description": "new", "secret": "applied"},
		},

		// A write-only secret is re-sent from the configuration on every update,
		// not only when its version changes (the version is unchanged here).
		{
			name: "ssh/description-only update re-sends the write-only private key", resource: newCredentialSSHResource,
			plan: func() any {
				m := ssh(str("pp"))
				m.PrivateKey, m.PrivateKeyWoVersion = null, str("1")
				return m
			}(),
			config: func() any {
				m := ssh(str("pp"))
				m.PrivateKey, m.PrivateKeyWo, m.PrivateKeyWoVersion = null, str("WO-KEY"), str("1")
				return m
			}(),
			wantSent:   map[string]string{"description": "new", "passphrase": "pp"},
			wantInBody: []string{"<privateKey>WO-KEY</privateKey>"},
		},
		{
			name: "vault approle/description-only update re-sends the write-only secret id", resource: newCredentialVaultAppRoleResource,
			plan: func() any {
				m := vault(null)
				m.SecretIDWoVersion = str("1")
				return m
			}(),
			config: func() any {
				m := vault(null)
				m.SecretIDWo, m.SecretIDWoVersion = str("wo-sid"), str("1")
				return m
			}(),
			wantSent: map[string]string{"description": "new", "secretId": "wo-sid"},
		},
		{
			name: "secret file/description-only update re-sends the write-only content", resource: newCredentialSecretFileResource,
			plan: func() any {
				m := secretFile(null)
				m.SecretBytesWoVersion = str("1")
				return m
			}(),
			config: func() any {
				m := secretFile(null)
				m.SecretBytesWo, m.SecretBytesWoVersion = str("V08="), str("1")
				return m
			}(),
			wantSent: map[string]string{"description": "new", "secretBytes": "V08="},
		},
		{
			name: "aws/description-only update re-sends the write-only secret key", resource: newCredentialAwsResource,
			plan: func() any {
				m := aws(null)
				m.SecretKeyWoVersion = str("1")
				return m
			}(),
			config: func() any {
				m := aws(null)
				m.SecretKeyWo, m.SecretKeyWoVersion = str("wo-sk"), str("1")
				return m
			}(),
			wantSent: map[string]string{"description": "new", "secretKey": "wo-sk"},
		},
		{
			name: "azure/description-only update re-sends the write-only client secret", resource: newCredentialAzureServicePrincipalResource,
			plan: func() any {
				m := azure(null, null)
				m.ClientSecretWoVersion = str("1")
				return m
			}(),
			config: func() any {
				m := azure(null, null)
				m.ClientSecretWo, m.ClientSecretWoVersion = str("wo-cs"), str("1")
				return m
			}(),
			wantSent:   map[string]string{"description": "new"},
			wantInBody: []string{"<clientSecret>wo-cs</clientSecret>"},
		},
		{
			name: "github app/description-only update re-sends the write-only private key", resource: newCredentialGitHubAppResource,
			plan: func() any {
				m := githubApp(null)
				m.PrivateKeyWoVersion = str("1")
				return m
			}(),
			config: func() any {
				m := githubApp(null)
				m.PrivateKeyWo, m.PrivateKeyWoVersion = str("WO-PEM"), str("1")
				return m
			}(),
			wantSent: map[string]string{"description": "new", "privateKey": "WO-PEM"},
		},

		// A secret that is empty by design is sent empty without reading
		// Jenkins, whatever Jenkins holds.
		{
			name: "azure/certificate-based credential sends an empty client secret without a check", resource: newCredentialAzureServicePrincipalResource,
			plan:       azure(null, str("cert-1")),
			stored:     storedCredentialXML(azureRoot, "clientSecret"),
			wantSent:   map[string]string{"description": "new"},
			wantInBody: []string{"<clientSecret></clientSecret>", "<certificateId>cert-1</certificateId>"},
		},
		{
			name: "aws/role-only credential sends an empty secret key without a check", resource: newCredentialAwsResource,
			plan:     aws(str("")),
			wantSent: map[string]string{"description": "new", "secretKey": ""},
		},
		{
			name: "certificate/unset password is sent empty without a check", resource: newCredentialCertificateResource,
			plan:       certificate(str("S1M="), null, null, null),
			stored:     storedCredentialXML(certRoot, "password"),
			wantSent:   map[string]string{"description": "new", "password": ""},
			wantInBody: []string{"<uploadedKeystore>S1M=</uploadedKeystore>"},
		},
		{
			name: "certificate/folder credential is updated in the folder store", resource: newCredentialCertificateResource,
			plan: func() any {
				m := certificate(str("S1M="), null, null, null)
				m.Folder = str("f1")
				return m
			}(),
			folder:     "f1",
			wantSent:   map[string]string{"description": "new", "password": ""},
			wantInBody: []string{"<uploadedKeystore>S1M=</uploadedKeystore>"},
		},

		// An optional secret that neither the configuration nor state sets is
		// sent only when Jenkins reports no value for it.
		{
			name: "username/password never set in Terraform that Jenkins reports fails without posting", resource: newCredentialUsernameResource,
			plan:    username(null, null, null),
			stored:  storedCredentialXML(usernameRoot, "password"),
			wantGet: true, wantErr: eraseErr, wantErrAttr: "password",
		},
		{
			name: "username/unreadable stored credential fails without posting", resource: newCredentialUsernameResource,
			plan:    username(null, null, null),
			stored:  "not xml",
			wantGet: true, wantErr: updateErr,
		},
		{
			// A read cut short before the secret's element must not count as
			// Jenkins holding no value.
			name: "username/truncated stored credential fails without posting", resource: newCredentialUsernameResource,
			plan:    username(null, null, null),
			stored:  "<" + usernameRoot + "><scope>GLOBAL</scope><id>x</id>",
			wantGet: true, wantErr: updateErr,
		},
		{
			name: "ssh/stored credential truncated inside an earlier element fails without posting", resource: newCredentialSSHResource,
			plan:    ssh(null),
			stored:  "<" + sshRoot + "><scope>GLOBAL</scope><privateKeySource class=\"k\"><privateKey><secret-redacted/>",
			wantGet: true, wantErr: updateErr,
		},
		{
			name: "username/server error reading the stored credential fails without posting", resource: newCredentialUsernameResource,
			plan:      username(null, null, null),
			getStatus: http.StatusInternalServerError,
			wantGet:   true, wantErr: updateErr,
		},
		{
			name: "username/stored-credential read keeps the domain escaped", resource: newCredentialUsernameResource,
			plan:      usernameIn(dotlessCredentialDomain, null, null, null),
			escDomain: escapedDotlessDomain,
			stored:    storedCredentialXML(usernameRoot, "password"),
			wantGet:   true, wantErr: eraseErr, wantErrAttr: "password",
		},
		{
			name: "username/stored-credential read uses the folder store", resource: newCredentialUsernameResource,
			plan: func() any {
				m := username(null, null, null)
				m.Folder = str("f1")
				return m
			}(),
			folder:  "f1",
			stored:  storedCredentialXML(usernameRoot, "password"),
			wantGet: true, wantErr: eraseErr, wantErrAttr: "password",
		},
		{
			name: "ssh/passphrase never set in Terraform that Jenkins holds fails without posting", resource: newCredentialSSHResource,
			plan:    ssh(null),
			stored:  storedCredentialXML(sshRoot, "passphrase"),
			wantGet: true, wantErr: eraseErr, wantErrAttr: "passphrase",
		},
		{
			name: "ssh/passphrase never set in Terraform that Jenkins does not hold proceeds", resource: newCredentialSSHResource,
			plan:       ssh(null),
			stored:     storedCredentialXML(sshRoot),
			wantGet:    true,
			wantSent:   map[string]string{"description": "new"},
			wantUnsent: []string{"passphrase"},
			wantInBody: []string{"<privateKey>KEY</privateKey>"},
		},
		{
			name: "vault approle/secret id never set in Terraform that Jenkins reports fails without posting", resource: newCredentialVaultAppRoleResource,
			plan:    vault(null),
			stored:  storedCredentialXML(vaultRoot, "secretId"),
			wantGet: true, wantErr: eraseErr, wantErrAttr: "secret_id",
		},

		// Removing an optional secret that Terraform set (state has a value or a
		// write-only version) clears it, as the plan shows, without reading
		// Jenkins.
		{
			name: "ssh/removing a managed passphrase clears it", resource: newCredentialSSHResource,
			plan:       sshKey(str("KEY2"), null),
			state:      ssh(str("pp")),
			stored:     storedCredentialXML(sshRoot, "passphrase"),
			wantSent:   map[string]string{"description": "new"},
			wantUnsent: []string{"passphrase"},
			wantInBody: []string{"<privateKey>KEY2</privateKey>"},
		},
		{
			name: "username/removing a managed password clears it", resource: newCredentialUsernameResource,
			plan:     username(null, null, null),
			state:    username(str("pw"), null, null),
			stored:   storedCredentialXML(usernameRoot, "password"),
			wantSent: map[string]string{"description": "new", "password": ""},
		},
		{
			name: "username/removing a managed write-only password clears it", resource: newCredentialUsernameResource,
			plan:     username(null, null, null),
			state:    username(null, null, str("1")),
			stored:   storedCredentialXML(usernameRoot, "password"),
			wantSent: map[string]string{"description": "new", "password": ""},
		},
		{
			name: "vault approle/removing a managed secret id clears it", resource: newCredentialVaultAppRoleResource,
			plan:     vault(null),
			state:    vault(str("sid")),
			stored:   storedCredentialXML(vaultRoot, "secretId"),
			wantSent: map[string]string{"description": "new", "secretId": ""},
		},
		{
			name: "vault approle/removing a managed write-only secret id clears it", resource: newCredentialVaultAppRoleResource,
			plan: vault(null),
			state: func() any {
				m := vault(null)
				m.SecretIDWoVersion = str("1")
				return m
			}(),
			stored:   storedCredentialXML(vaultRoot, "secretId"),
			wantSent: map[string]string{"description": "new", "secretId": ""},
		},

		// lifecycle.ignore_changes keeps a secret's null value from state (after
		// an import) while the configuration sets it: Terraform has no value to
		// send, so the update fails instead of erasing the secret.
		{
			name: "secret text/ignored secret with no value in state fails without posting", resource: newCredentialSecretTextResource,
			plan:    secretText(null, null, null),
			config:  secretText(str("placeholder"), null, null),
			wantErr: notKnownErr, wantErrAttr: "secret",
		},
		{
			name: "secret text/ignored secret configured empty with no value in state fails without posting", resource: newCredentialSecretTextResource,
			plan:    secretText(null, null, null),
			config:  secretText(str(""), null, null),
			wantErr: notKnownErr, wantErrAttr: "secret",
		},
		{
			name: "username/ignored password with no value in state fails without posting", resource: newCredentialUsernameResource,
			plan:    username(null, null, null),
			config:  username(str("placeholder"), null, null),
			stored:  storedCredentialXML(usernameRoot, "password"),
			wantErr: notKnownErr, wantErrAttr: "password",
		},
		{
			name: "ssh/ignored private key with no value in state fails without posting", resource: newCredentialSSHResource,
			plan:    sshKey(null, str("pp")),
			config:  sshKey(str("placeholder"), str("pp")),
			wantErr: notKnownErr, wantErrAttr: "privatekey",
		},
		{
			name: "ssh/ignored passphrase with no value in state fails without posting", resource: newCredentialSSHResource,
			plan:    ssh(null),
			config:  ssh(str("placeholder")),
			stored:  storedCredentialXML(sshRoot, "passphrase"),
			wantErr: notKnownErr, wantErrAttr: "passphrase",
		},
		{
			name: "certificate/ignored keystore with no value in state fails without posting", resource: newCredentialCertificateResource,
			plan:    certificate(null, null, null, str("pw")),
			config:  certificate(str("S1M="), null, null, str("pw")),
			wantErr: notKnownErr, wantErrAttr: "keystore",
		},
		{
			name: "certificate/ignored password with no value in state fails without posting", resource: newCredentialCertificateResource,
			plan:    certificate(str("S1M="), null, null, null),
			config:  certificate(str("S1M="), null, null, str("pw")),
			wantErr: notKnownErr, wantErrAttr: "password",
		},
		{
			name: "vault approle/ignored secret id with no value in state fails without posting", resource: newCredentialVaultAppRoleResource,
			plan:    vault(null),
			config:  vault(str("placeholder")),
			stored:  storedCredentialXML(vaultRoot, "secretId"),
			wantErr: notKnownErr, wantErrAttr: "secret_id",
		},
		{
			name: "secret file/ignored content with no value in state fails without posting", resource: newCredentialSecretFileResource,
			plan:    secretFile(null),
			config:  secretFile(str("Ynl0ZXM=")),
			wantErr: notKnownErr, wantErrAttr: "secretbytes",
		},
		{
			name: "aws/ignored secret key with no value in state fails without posting", resource: newCredentialAwsResource,
			plan:    aws(null),
			config:  aws(str("sk")),
			wantErr: notKnownErr, wantErrAttr: "secret_key",
		},
		{
			name: "azure/ignored client secret with no value in state fails without posting", resource: newCredentialAzureServicePrincipalResource,
			plan:    azure(null, null),
			config:  azure(str("cs"), null),
			wantErr: notKnownErr, wantErrAttr: "client_secret",
		},
		{
			name: "github app/ignored private key with no value in state fails without posting", resource: newCredentialGitHubAppResource,
			plan:    githubApp(null),
			config:  githubApp(str("PEM")),
			wantErr: notKnownErr, wantErrAttr: "private_key",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &credentialStoreFake{stored: tc.stored, getStatus: tc.getStatus}
			r, s := configuredCredentialResource(ctx, t, tc.resource, startCredentialStoreFake(t, fake))

			config := tc.config
			if config == nil {
				config = tc.plan
			}
			resp := runCredentialUpdate(ctx, t, r, s, tc.plan, config, tc.state)
			got := fake.requests()

			escDomain := tc.escDomain
			if escDomain == "" {
				escDomain = "_"
			}
			store := "/credentials/store/system"
			if tc.folder != "" {
				store = "/job/" + tc.folder + "/credentials/store/folder"
			}
			credPath := store + "/domain/" + escDomain + "/credential/x/config.xml"
			var want []string
			if tc.wantGet {
				want = append(want, http.MethodGet+" "+credPath+"/")
			}
			if tc.wantErr == "" {
				want = append(want, http.MethodPost+" "+credPath)
			}
			if gotReqs := requestLines(got); strings.Join(gotReqs, "\n") != strings.Join(want, "\n") {
				if n := len(got); n > 0 && got[n-1].method == http.MethodPost {
					t.Logf("posted: %s", got[n-1].body)
				}
				t.Fatalf("requests = %q, want %q", gotReqs, want)
			}

			if tc.wantErr != "" {
				checkSingleError(t, resp.Diagnostics, tc.wantErr, tc.wantErrAttr)
				return
			}
			if resp.Diagnostics.HasError() {
				t.Fatalf("Update: %v", resp.Diagnostics)
			}
			checkPosted(t, got[len(got)-1].body, tc.wantSent, tc.wantUnsent, tc.wantInBody)
		})
	}
}

// TestCredentialCreateThenUpdate creates a credential through the resource and
// then changes only its description, against a store that answers GET with
// what Create posted, redacted the way the credentials plugin does. It pins the
// behavior for an optional secret left out of the configuration: Create posts
// an empty element for a username password, a Vault secret_id and a
// certificate password, which Jenkins then reports as a stored value, and
// leaves the element out for an SSH passphrase.
func TestCredentialCreateThenUpdate(t *testing.T) {
	ctx := context.Background()
	str, null := types.StringValue, types.StringNull()

	type tc struct {
		name     string
		resource func() resource.Resource
		// model is the created resource. config carries its write-only values;
		// nil means config equals model.
		model, config any
		// wantGet: the update must read the stored credential first.
		wantGet bool
		// wantErr / wantErrAttr: the update must fail without posting.
		wantErr, wantErrAttr string
		// wantSent, wantUnsent, wantInBody: see checkPosted.
		wantSent   map[string]string
		wantUnsent []string
		wantInBody []string
	}

	cases := []tc{
		{
			name: "username without a password cannot be updated", resource: newCredentialUsernameResource,
			model: &credentialUsernameResourceModel{
				Name: str("x"), Folder: null, Description: str("old"), Domain: str("_"), Scope: str("GLOBAL"),
				Username: str("u"), Password: null, PasswordWo: null, PasswordWoVersion: null,
			},
			wantGet: true, wantErr: eraseErr, wantErrAttr: "password",
		},
		{
			name: "username with an empty password is updated", resource: newCredentialUsernameResource,
			model: &credentialUsernameResourceModel{
				Name: str("x"), Folder: null, Description: str("old"), Domain: str("_"), Scope: str("GLOBAL"),
				Username: str("u"), Password: str(""), PasswordWo: null, PasswordWoVersion: null,
			},
			wantSent: map[string]string{"description": "new", "password": ""},
		},
		{
			name: "username with a write-only password re-sends it", resource: newCredentialUsernameResource,
			model: &credentialUsernameResourceModel{
				Name: str("x"), Folder: null, Description: str("old"), Domain: str("_"), Scope: str("GLOBAL"),
				Username: str("u"), Password: null, PasswordWo: null, PasswordWoVersion: str("1"),
			},
			config: &credentialUsernameResourceModel{
				Name: str("x"), Folder: null, Description: str("old"), Domain: str("_"), Scope: str("GLOBAL"),
				Username: str("u"), Password: null, PasswordWo: str("wo-pw"), PasswordWoVersion: str("1"),
			},
			wantSent: map[string]string{"description": "new", "password": "wo-pw"},
		},
		{
			name: "vault approle without a secret id cannot be updated", resource: newCredentialVaultAppRoleResource,
			model: &credentialVaultAppRoleResourceModel{
				Name: str("x"), Folder: null, Description: str("old"), Domain: str("_"), Scope: str("GLOBAL"),
				Namespace: str(""), Path: str("approle"), RoleID: str("role"),
				SecretID: null, SecretIDWo: null, SecretIDWoVersion: null,
			},
			wantGet: true, wantErr: eraseErr, wantErrAttr: "secret_id",
		},
		{
			name: "ssh without a passphrase is updated", resource: newCredentialSSHResource,
			model: &credentialSSHResourceModel{
				Name: str("x"), Folder: null, Description: str("old"), Domain: str("_"), Scope: str("GLOBAL"),
				Username: str("u"), PrivateKey: str("KEY"), PrivateKeyWo: null, PrivateKeyWoVersion: null,
				Passphrase: null,
			},
			wantGet:    true,
			wantSent:   map[string]string{"description": "new"},
			wantUnsent: []string{"passphrase"},
			wantInBody: []string{"<privateKey>KEY</privateKey>"},
		},
		{
			name: "certificate without a password is updated", resource: newCredentialCertificateResource,
			model: &credentialCertificateResourceModel{
				Name: str("x"), Folder: null, Description: str("old"), Domain: str("_"), Scope: str("GLOBAL"),
				Keystore: str("S1M="), KeystoreWo: null, KeystoreWoVersion: null, Password: null,
			},
			wantSent:   map[string]string{"description": "new", "password": ""},
			wantInBody: []string{"<uploadedKeystore>S1M=</uploadedKeystore>"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &credentialStoreFake{stateful: true}
			r, s := configuredCredentialResource(ctx, t, tc.resource, startCredentialStoreFake(t, fake))

			configModel := tc.config
			if configModel == nil {
				configModel = tc.model
			}
			createConfig := tfsdk.Config{Schema: s, Raw: toPlan(ctx, t, s, configModel).Raw}
			created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
			r.Create(ctx, resource.CreateRequest{Plan: toPlan(ctx, t, s, tc.model), Config: createConfig}, &created)
			if created.Diagnostics.HasError() {
				t.Fatalf("Create: %v", created.Diagnostics)
			}

			// The update changes only the description.
			plan := tfsdk.Plan{Schema: s, Raw: withNewDescription(ctx, t, s, created.State.Raw)}
			config := tfsdk.Config{Schema: s, Raw: withNewDescription(ctx, t, s, createConfig.Raw)}
			resp := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
			r.Update(ctx, resource.UpdateRequest{Plan: plan, State: created.State, Config: config}, &resp)

			got := fake.requests()
			const store = "/credentials/store/system/domain/_/"
			want := []string{http.MethodPost + " " + store + "createCredentials"}
			if tc.wantGet {
				want = append(want, http.MethodGet+" "+store+"credential/x/config.xml/")
			}
			if tc.wantErr == "" {
				want = append(want, http.MethodPost+" "+store+"credential/x/config.xml")
			}
			if gotReqs := requestLines(got); strings.Join(gotReqs, "\n") != strings.Join(want, "\n") {
				t.Fatalf("requests = %q, want %q", gotReqs, want)
			}

			if tc.wantErr != "" {
				checkSingleError(t, resp.Diagnostics, tc.wantErr, tc.wantErrAttr)
				return
			}
			if resp.Diagnostics.HasError() {
				t.Fatalf("Update: %v", resp.Diagnostics)
			}
			checkPosted(t, got[len(got)-1].body, tc.wantSent, tc.wantUnsent, tc.wantInBody)
		})
	}
}
