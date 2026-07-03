package jenkins

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// TestCredentialWriteOnlySchema verifies that every credential resource exposes a
// correctly-wired write-only secret pair (`<secret>_wo` + `<secret>_wo_version`),
// keeps its plain secret optional so the write-only alternative can be chosen, and
// registers the conflict/version config validators. This locally-verifiable check
// guards the uniform write-only wiring across all eight resources.
func TestCredentialWriteOnlySchema(t *testing.T) {
	cases := []struct {
		name    string
		factory func() resource.Resource
		secret  string
	}{
		{"secret_text", newCredentialSecretTextResource, "secret"},
		{"username", newCredentialUsernameResource, "password"},
		{"secret_file", newCredentialSecretFileResource, "secretbytes"},
		{"ssh", newCredentialSSHResource, "privatekey"},
		{"aws", newCredentialAwsResource, "secret_key"},
		{"azure_service_principal", newCredentialAzureServicePrincipalResource, "client_secret"},
		{"vault_approle", newCredentialVaultAppRoleResource, "secret_id"},
		{"github_app", newCredentialGitHubAppResource, "private_key"},
		{"certificate", newCredentialCertificateResource, "keystore"},
	}

	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.factory()

			sr := &resource.SchemaResponse{}
			r.Schema(ctx, resource.SchemaRequest{}, sr)
			attrs := sr.Schema.Attributes

			wo, ok := attrs[tc.secret+"_wo"]
			if !ok {
				t.Fatalf("missing %s_wo attribute", tc.secret)
			}
			if !wo.IsWriteOnly() {
				t.Errorf("%s_wo must be write-only", tc.secret)
			}
			if !wo.IsSensitive() {
				t.Errorf("%s_wo must be sensitive", tc.secret)
			}
			if !wo.IsOptional() {
				t.Errorf("%s_wo must be optional", tc.secret)
			}

			ver, ok := attrs[tc.secret+"_wo_version"]
			if !ok {
				t.Fatalf("missing %s_wo_version attribute", tc.secret)
			}
			if ver.IsWriteOnly() {
				t.Errorf("%s_wo_version must be stored in state (not write-only)", tc.secret)
			}

			// The plain secret must be optional so a config can supply the
			// write-only variant instead without a schema-level "required" error.
			plain, ok := attrs[tc.secret]
			if !ok {
				t.Fatalf("missing %s attribute", tc.secret)
			}
			if plain.IsRequired() {
				t.Errorf("%s must be optional so the write-only alternative can be used", tc.secret)
			}

			cv, ok := r.(resource.ResourceWithConfigValidators)
			if !ok {
				t.Fatalf("%s must implement ResourceWithConfigValidators", tc.name)
			}
			if got := len(cv.ConfigValidators(ctx)); got < 2 {
				t.Errorf("expected at least 2 config validators, got %d", got)
			}
		})
	}
}
