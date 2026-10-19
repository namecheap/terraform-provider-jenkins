package jenkins

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// TestCredentialImportThenUpdate imports a credential whose secrets were set
// within Jenkins, reads it as terraform import does, and then changes its
// description, against a store that redacts secrets on GET the way the
// credentials plugin does. Jenkins never returns a secret, so after an import
// state holds none: the update must not take a secret Jenkins holds for one the
// configuration dropped, and must not post a secret Terraform does not know.
func TestCredentialImportThenUpdate(t *testing.T) {
	ctx := context.Background()
	str, null := types.StringValue, types.StringNull()

	type tc struct {
		name     string
		resource func() resource.Resource
		// stored is the credential as Jenkins holds it, secrets in clear.
		stored string
		// config is the configuration after the import, with description
		// "new". ignore lists attributes under lifecycle.ignore_changes: the
		// plan keeps their value from state.
		config any
		ignore []string
		// wantGet: the update must read the stored credential first.
		wantGet bool
		// wantErr / wantErrAttr: the update must fail without posting.
		wantErr, wantErrAttr string
		// wantSent, wantUnsent, wantInBody: see checkPosted.
		wantSent   map[string]string
		wantUnsent []string
		wantInBody []string
	}

	username := func(password, wo, ver types.String) *credentialUsernameResourceModel {
		return &credentialUsernameResourceModel{
			Name: str("x"), Folder: str(""), Description: str("new"), Domain: str("_"), Scope: str("GLOBAL"),
			Username: str("u"), Password: password, PasswordWo: wo, PasswordWoVersion: ver,
		}
	}
	vault := func(secretID types.String) *credentialVaultAppRoleResourceModel {
		return &credentialVaultAppRoleResourceModel{
			Name: str("x"), Folder: str(""), Description: str("new"), Domain: str("_"), Scope: str("GLOBAL"),
			Namespace: str(""), Path: str("approle"), RoleID: str("role"),
			SecretID: secretID, SecretIDWo: null, SecretIDWoVersion: null,
		}
	}
	ssh := func(passphrase types.String) *credentialSSHResourceModel {
		return &credentialSSHResourceModel{
			Name: str("x"), Folder: str(""), Description: str("new"), Domain: str("_"), Scope: str("GLOBAL"),
			Username: str("u"), PrivateKey: str("KEY"), PrivateKeyWo: null, PrivateKeyWoVersion: null,
			Passphrase: passphrase,
		}
	}
	secretText := &credentialSecretTextResourceModel{
		Name: str("x"), Folder: str(""), Description: str("new"), Domain: str("_"), Scope: str("GLOBAL"),
		Secret: str("placeholder"), SecretWo: null, SecretWoVersion: null,
	}
	certificate := &credentialCertificateResourceModel{
		Name: str("x"), Folder: str(""), Description: str("new"), Domain: str("_"), Scope: str("GLOBAL"),
		Keystore: str("S1M="), KeystoreWo: null, KeystoreWoVersion: null, Password: str("changeit"),
	}

	const (
		storedUsername = "<" + usernameRoot + "><scope>GLOBAL</scope><id>x</id><description>old</description>" +
			"<username>u</username><password>set-in-jenkins</password></" + usernameRoot + ">"
		storedVault = "<" + vaultRoot + "><scope>GLOBAL</scope><id>x</id><description>old</description>" +
			"<namespace></namespace><secretId>set-in-jenkins</secretId><roleId>role</roleId><path>approle</path></" + vaultRoot + ">"
		storedSSHKey = "<privateKeySource class=\"com.cloudbees.jenkins.plugins.sshcredentials.impl.BasicSSHUserPrivateKey$DirectEntryPrivateKeySource\">" +
			"<privateKey>KEY-IN-JENKINS</privateKey></privateKeySource>"
		storedSSH = "<" + sshRoot + "><scope>GLOBAL</scope><id>x</id><description>old</description><username>u</username>" +
			"<passphrase>set-in-jenkins</passphrase>" + storedSSHKey + "</" + sshRoot + ">"
		storedSSHNoPassphrase = "<" + sshRoot + "><scope>GLOBAL</scope><id>x</id><description>old</description><username>u</username>" +
			storedSSHKey + "</" + sshRoot + ">"
		storedSecretText = "<org.jenkinsci.plugins.plaincredentials.impl.StringCredentialsImpl><scope>GLOBAL</scope><id>x</id>" +
			"<description>old</description><secret>set-in-jenkins</secret></org.jenkinsci.plugins.plaincredentials.impl.StringCredentialsImpl>"
		storedCertificate = "<" + certRoot + "><scope>GLOBAL</scope><id>x</id><description>old</description>" +
			"<keyStoreSource class=\"com.cloudbees.plugins.credentials.impl.CertificateCredentialsImpl$UploadedKeyStoreSource\">" +
			"<uploadedKeystore>S1M=</uploadedKeystore></keyStoreSource><password>set-in-jenkins</password></" + certRoot + ">"
	)

	cases := []tc{
		// An optional secret the configuration leaves unset is checked against
		// what Jenkins holds.
		{
			name: "username/password set within Jenkins is not erased", resource: newCredentialUsernameResource,
			stored:  storedUsername,
			config:  username(null, null, null),
			wantGet: true, wantErr: eraseErr, wantErrAttr: "password",
		},
		{
			name: "vault approle/secret id set within Jenkins is not erased", resource: newCredentialVaultAppRoleResource,
			stored:  storedVault,
			config:  vault(null),
			wantGet: true, wantErr: eraseErr, wantErrAttr: "secret_id",
		},
		{
			name: "ssh/passphrase set within Jenkins is not erased", resource: newCredentialSSHResource,
			stored:  storedSSH,
			config:  ssh(null),
			wantGet: true, wantErr: eraseErr, wantErrAttr: "passphrase",
		},
		{
			name: "ssh/credential without a passphrase is updated", resource: newCredentialSSHResource,
			stored:     storedSSHNoPassphrase,
			config:     ssh(null),
			wantGet:    true,
			wantSent:   map[string]string{"description": "new"},
			wantUnsent: []string{"passphrase"},
			wantInBody: []string{"<privateKey>KEY</privateKey>"},
		},

		// A secret the configuration sets is sent as configured.
		{
			name: "username/configured password replaces the one set within Jenkins", resource: newCredentialUsernameResource,
			stored:   storedUsername,
			config:   username(str("pw"), null, null),
			wantSent: map[string]string{"description": "new", "password": "pw"},
		},
		{
			name: "username/configured write-only password replaces the one set within Jenkins", resource: newCredentialUsernameResource,
			stored:   storedUsername,
			config:   username(null, str("wo-pw"), str("1")),
			wantSent: map[string]string{"description": "new", "password": "wo-pw"},
		},

		// ignore_changes keeps the null value that the import left in state.
		{
			name: "secret text/ignored secret is not posted as the configured placeholder or empty", resource: newCredentialSecretTextResource,
			stored:  storedSecretText,
			config:  secretText,
			ignore:  []string{"secret"},
			wantErr: notKnownErr, wantErrAttr: "secret",
		},
		{
			name: "certificate/ignored password is not posted empty", resource: newCredentialCertificateResource,
			stored:  storedCertificate,
			config:  certificate,
			ignore:  []string{"password"},
			wantErr: notKnownErr, wantErrAttr: "password",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &credentialStoreFake{stateful: true, stored: tc.stored}
			r, s := configuredCredentialResource(ctx, t, tc.resource, startCredentialStoreFake(t, fake))

			imported := resource.ImportStateResponse{State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}
			r.(resource.ResourceWithImportState).ImportState(ctx, resource.ImportStateRequest{ID: "_/x"}, &imported)
			if imported.Diagnostics.HasError() {
				t.Fatalf("ImportState: %v", imported.Diagnostics)
			}
			read := resource.ReadResponse{State: imported.State}
			r.Read(ctx, resource.ReadRequest{State: imported.State}, &read)
			if read.Diagnostics.HasError() {
				t.Fatalf("Read: %v", read.Diagnostics)
			}

			// Read must leave every secret out of state: a value there would
			// make the update treat a secret Jenkins holds as one Terraform
			// manages.
			for name, a := range s.Attributes {
				if !a.IsSensitive() {
					continue
				}
				var v types.String
				if d := read.State.GetAttribute(ctx, path.Root(name), &v); d.HasError() {
					t.Fatalf("state.GetAttribute(%s): %v", name, d)
				}
				if !v.IsNull() {
					t.Errorf("state after import has %s = %q, want null", name, v.ValueString())
				}
			}

			config := toPlan(ctx, t, s, tc.config)
			plan := tfsdk.Plan{Schema: s, Raw: config.Raw.Copy()}
			for _, attr := range tc.ignore {
				var v types.String
				if d := read.State.GetAttribute(ctx, path.Root(attr), &v); d.HasError() {
					t.Fatalf("state.GetAttribute(%s): %v", attr, d)
				}
				if d := plan.SetAttribute(ctx, path.Root(attr), v); d.HasError() {
					t.Fatalf("plan.SetAttribute(%s): %v", attr, d)
				}
			}
			resp := resource.UpdateResponse{State: tfsdk.State{Schema: s}}
			r.Update(ctx, resource.UpdateRequest{
				Plan:   plan,
				State:  read.State,
				Config: tfsdk.Config{Schema: s, Raw: config.Raw},
			}, &resp)

			got := fake.requests()
			const credPath = "/credentials/store/system/domain/_/credential/x/config.xml"
			want := []string{http.MethodGet + " " + credPath + "/"}
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
