package jenkins

import (
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Regression tests for credential updates erasing secrets. Jenkins' config.xml
// update replaces the whole credential: a secret element that is left out,
// empty or <secret-redacted/> is stored as empty. Update used to send only the
// secrets whose value changed, so any other change (a new description, say)
// erased the stored secret.

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

// credentialUpdateRecorder starts a Jenkins stand-in that answers a GET of a
// credential's config.xml with stored and records every other request with its
// body (the crumb fetch excepted).
func credentialUpdateRecorder(t *testing.T, stored string) (*jenkinsAdapter, func() []recordedBody) {
	t.Helper()
	var mu sync.Mutex
	var seen []recordedBody
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "crumbIssuer") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"crumb":"abc","crumbRequestField":"Jenkins-Crumb"}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, recordedBody{method: r.Method, path: r.URL.EscapedPath(), body: string(body)})
		mu.Unlock()
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, stored)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(srv.Close)
	c, err := newJenkinsClient(&Config{ServerURL: srv.URL})
	if err != nil {
		t.Fatalf("newJenkinsClient: %v", err)
	}
	return c, func() []recordedBody {
		mu.Lock()
		defer mu.Unlock()
		return append([]recordedBody(nil), seen...)
	}
}

type recordedBody struct {
	method, path, body string
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

// runCredentialUpdate drives r.Update with plan and config built from the given
// models (config carries write-only values; plan and state never do). The prior
// state is the plan with description "old": a description-only change.
func runCredentialUpdate(ctx context.Context, t *testing.T, r resource.Resource, plan, config any) resource.UpdateResponse {
	t.Helper()
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	s := sr.Schema
	p := tfsdk.Plan{Schema: s}
	if d := p.Set(ctx, plan); d.HasError() {
		t.Fatalf("plan.Set: %v", d)
	}
	c := tfsdk.Plan{Schema: s}
	if d := c.Set(ctx, config); d.HasError() {
		t.Fatalf("config.Set: %v", d)
	}
	state := tfsdk.State{Schema: s, Raw: p.Raw.Copy()}
	if d := state.SetAttribute(ctx, path.Root("description"), "old"); d.HasError() {
		t.Fatalf("state.SetAttribute: %v", d)
	}
	resp := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
	r.Update(ctx, resource.UpdateRequest{
		Plan:   p,
		State:  state,
		Config: tfsdk.Config{Schema: s, Raw: c.Raw},
	}, &resp)
	return resp
}

func TestCredentialUpdateResendsSecrets(t *testing.T) {
	ctx := context.Background()
	str, null := types.StringValue, types.StringNull()

	type tc struct {
		name     string
		resource func() resource.Resource
		// plan is the planned model (a description-only change: the secrets
		// equal the prior state's). config is the same model with any
		// write-only value set; nil means config equals plan.
		plan, config any
		// escDomain is the escaped domain in request paths ("_" when empty).
		escDomain string
		// stored is the GET config.xml answer; it is only requested when an
		// optional secret is left unset.
		stored string
		// wantGet: Update must read the stored credential first.
		wantGet bool
		// wantErr: Update must fail with this summary without posting;
		// wantErrAttr is the attribute the error must name.
		wantErr, wantErrAttr string
		// wantSent maps posted elements to their exact inner XML; wantUnsent
		// lists elements the payload must not contain; wantInBody lists
		// substrings of the payload (for nested elements).
		wantSent   map[string]string
		wantUnsent []string
		wantInBody []string
	}

	const (
		eraseErr  = "Update Would Erase Credential Secret"
		updateErr = "Unable to Update Resource"
	)

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
	ssh := func(passphrase types.String) *credentialSSHResourceModel {
		return &credentialSSHResourceModel{
			Name: str("x"), Folder: null, Description: str("new"), Domain: str("_"), Scope: str("GLOBAL"),
			Username: str("u"), PrivateKey: str("KEY"), PrivateKeyWo: null, PrivateKeyWoVersion: null,
			Passphrase: passphrase,
		}
	}
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

	const (
		usernameRoot = "com.cloudbees.plugins.credentials.impl.UsernamePasswordCredentialsImpl"
		sshRoot      = "com.cloudbees.jenkins.plugins.sshcredentials.impl.BasicSSHUserPrivateKey"
		certRoot     = "com.cloudbees.plugins.credentials.impl.CertificateCredentialsImpl"
		vaultRoot    = "com.datapipe.jenkins.vault.credentials.VaultAppRoleCredential"
	)

	cases := []tc{
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
			name: "username/unset password that Jenkins holds fails without posting", resource: newCredentialUsernameResource,
			plan:    username(null, null, null),
			stored:  storedCredentialXML(usernameRoot, "password"),
			wantGet: true, wantErr: eraseErr, wantErrAttr: "password",
		},
		{
			name: "username/unset password that Jenkins does not hold proceeds", resource: newCredentialUsernameResource,
			plan:     username(null, null, null),
			stored:   storedCredentialXML(usernameRoot),
			wantGet:  true,
			wantSent: map[string]string{"description": "new", "password": ""},
		},
		{
			name: "username/unreadable stored credential fails without posting", resource: newCredentialUsernameResource,
			plan:    username(null, null, null),
			stored:  "not xml",
			wantGet: true, wantErr: updateErr,
		},
		{
			name: "username/stored-credential read keeps the domain escaped", resource: newCredentialUsernameResource,
			plan:      usernameIn(dotlessCredentialDomain, null, null, null),
			escDomain: escapedDotlessDomain,
			stored:    storedCredentialXML(usernameRoot, "password"),
			wantGet:   true, wantErr: eraseErr, wantErrAttr: "password",
		},
		{
			name: "ssh/description-only update re-sends the passphrase", resource: newCredentialSSHResource,
			plan:       ssh(str("pp")),
			wantSent:   map[string]string{"description": "new", "passphrase": "pp"},
			wantInBody: []string{"<privateKey>KEY</privateKey>"},
		},
		{
			name: "ssh/unset passphrase that Jenkins holds fails without posting", resource: newCredentialSSHResource,
			plan:    ssh(null),
			stored:  storedCredentialXML(sshRoot, "passphrase"),
			wantGet: true, wantErr: eraseErr, wantErrAttr: "passphrase",
		},
		{
			name: "ssh/unset passphrase that Jenkins does not hold proceeds", resource: newCredentialSSHResource,
			plan:       ssh(null),
			stored:     storedCredentialXML(sshRoot),
			wantGet:    true,
			wantSent:   map[string]string{"description": "new"},
			wantUnsent: []string{"passphrase"},
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
			name: "certificate/unset password that Jenkins holds fails without posting", resource: newCredentialCertificateResource,
			plan:    certificate(str("S1M="), null, null, null),
			stored:  storedCredentialXML(certRoot, "password"),
			wantGet: true, wantErr: eraseErr, wantErrAttr: "password",
		},
		{
			name: "certificate/unset password that Jenkins does not hold proceeds", resource: newCredentialCertificateResource,
			plan:       certificate(str("S1M="), null, null, null),
			stored:     storedCredentialXML(certRoot),
			wantGet:    true,
			wantSent:   map[string]string{"description": "new", "password": ""},
			wantInBody: []string{"<uploadedKeystore>S1M=</uploadedKeystore>"},
		},
		{
			name: "vault approle/description-only update re-sends the secret id", resource: newCredentialVaultAppRoleResource,
			plan:     vault(str("sid")),
			wantSent: map[string]string{"description": "new", "secretId": "sid"},
		},
		{
			name: "vault approle/unset secret id that Jenkins holds fails without posting", resource: newCredentialVaultAppRoleResource,
			plan:    vault(null),
			stored:  storedCredentialXML(vaultRoot, "secretId"),
			wantGet: true, wantErr: eraseErr, wantErrAttr: "secret_id",
		},
		{
			name: "secret file/description-only update re-sends the content", resource: newCredentialSecretFileResource,
			plan: &credentialSecretFileResourceModel{
				Name: str("x"), Folder: null, Description: str("new"), Domain: str("_"), Scope: str("GLOBAL"),
				Filename: str("f"), SecretBytes: str("Ynl0ZXM="), SecretBytesWo: null, SecretBytesWoVersion: null,
			},
			wantSent: map[string]string{"description": "new", "secretBytes": "Ynl0ZXM="},
		},
		{
			name: "aws/description-only update re-sends the secret key", resource: newCredentialAwsResource,
			plan: &credentialAwsResourceModel{
				Name: str("x"), Folder: null, Description: str("new"), Domain: str("_"), Scope: str("GLOBAL"),
				AccessKey: str("ak"), SecretKey: str("sk"), SecretKeyWo: null, SecretKeyWoVersion: null,
				IamRoleArn: str(""), IamMfaSerialNumber: str(""),
			},
			wantSent: map[string]string{"description": "new", "secretKey": "sk"},
		},
		{
			name: "azure/description-only update re-sends the client secret", resource: newCredentialAzureServicePrincipalResource,
			plan: &credentialAzureServicePrincipalResourceModel{
				Name: str("x"), Folder: null, Description: str("new"), Domain: str("_"), Scope: str("GLOBAL"),
				SubscriptionId: str("sub"), ClientId: str("cid"), ClientSecret: str("cs"), ClientSecretWo: null,
				ClientSecretWoVersion: null, CertificateId: null, Tenant: str("t"), AzureEnvironmentName: str("Azure"),
				ServiceManagementURL: str(""), AuthenticationEndpoint: str(""), ResourceManagerEndpoint: str(""), GraphEndpoint: str(""),
			},
			wantSent:   map[string]string{"description": "new"},
			wantInBody: []string{"<clientSecret>cs</clientSecret>"},
		},
		{
			name: "github app/description-only update re-sends the private key", resource: newCredentialGitHubAppResource,
			plan: &credentialGitHubAppResourceModel{
				Name: str("x"), Folder: null, Description: str("new"), Domain: str("_"), Scope: str("GLOBAL"),
				AppID: str("1"), PrivateKey: str("PEM"), PrivateKeyWo: null, PrivateKeyWoVersion: null,
			},
			wantSent: map[string]string{"description": "new", "privateKey": "PEM"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, requests := credentialUpdateRecorder(t, tc.stored)
			r := tc.resource()
			var cr resource.ConfigureResponse
			r.(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: client}, &cr)

			config := tc.config
			if config == nil {
				config = tc.plan
			}
			resp := runCredentialUpdate(ctx, t, r, tc.plan, config)
			got := requests()

			escDomain := tc.escDomain
			if escDomain == "" {
				escDomain = "_"
			}
			credPath := "/credentials/store/system/domain/" + escDomain + "/credential/x/config.xml"
			var want, gotReqs []string
			if tc.wantGet {
				want = append(want, http.MethodGet+" "+credPath+"/")
			}
			if tc.wantErr == "" {
				want = append(want, http.MethodPost+" "+credPath)
			}
			for _, g := range got {
				gotReqs = append(gotReqs, g.method+" "+g.path)
			}
			if strings.Join(gotReqs, "\n") != strings.Join(want, "\n") {
				t.Fatalf("requests = %q, want %q", gotReqs, want)
			}

			if tc.wantErr != "" {
				errs := resp.Diagnostics.Errors()
				if len(errs) != 1 || errs[0].Summary() != tc.wantErr {
					t.Fatalf("diagnostics = %v, want one %q error", resp.Diagnostics, tc.wantErr)
				}
				if tc.wantErrAttr != "" && !strings.Contains(errs[0].Detail(), `"`+tc.wantErrAttr+`"`) {
					t.Errorf("error detail %q does not name %q", errs[0].Detail(), tc.wantErrAttr)
				}
				return
			}
			if resp.Diagnostics.HasError() {
				t.Fatalf("Update: %v", resp.Diagnostics)
			}
			body := got[len(got)-1].body
			sent := sentElements(t, body)
			for el, v := range tc.wantSent {
				if gotV, ok := sent[el]; !ok {
					t.Errorf("posted credential has no <%s>, want %q", el, v)
				} else if gotV != v {
					t.Errorf("posted <%s> = %q, want %q", el, gotV, v)
				}
			}
			for _, el := range tc.wantUnsent {
				if _, ok := sent[el]; ok {
					t.Errorf("posted credential has <%s>, want none", el)
				}
			}
			for _, sub := range tc.wantInBody {
				if !strings.Contains(body, sub) {
					t.Errorf("posted credential lacks %q:\n%s", sub, body)
				}
			}
		})
	}
}
