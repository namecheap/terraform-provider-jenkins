package jenkins

import (
	"context"
	"testing"

	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
)

func TestAllResourcesMetadata(t *testing.T) {
	tests := []struct {
		typeName string
		newFn    func() fwresource.Resource
	}{
		{"jenkins_credential_aws", newCredentialAwsResource},
		{"jenkins_credential_azure_service_principal", newCredentialAzureServicePrincipalResource},
		{"jenkins_credential_github_app", newCredentialGitHubAppResource},
		{"jenkins_credential_secret_file", newCredentialSecretFileResource},
		{"jenkins_credential_secret_text", newCredentialSecretTextResource},
		{"jenkins_credential_ssh", newCredentialSSHResource},
		{"jenkins_credential_username", newCredentialUsernameResource},
		{"jenkins_credential_vault_approle", newCredentialVaultAppRoleResource},
		{"jenkins_view", newViewResource},
	}
	for _, tt := range tests {
		t.Run(tt.typeName, func(t *testing.T) {
			r := tt.newFn()
			if r == nil {
				t.Fatal("constructor returned nil")
			}
			resp := &fwresource.MetadataResponse{}
			r.Metadata(context.Background(), fwresource.MetadataRequest{ProviderTypeName: "jenkins"}, resp)
			if resp.TypeName != tt.typeName {
				t.Errorf("TypeName = %q, want %q", resp.TypeName, tt.typeName)
			}
		})
	}
}

func TestAllResourcesSchema(t *testing.T) {
	tests := []struct {
		typeName   string
		newFn      func() fwresource.Resource
		attributes []string
	}{
		{
			"jenkins_credential_aws",
			newCredentialAwsResource,
			[]string{"id", "name", "folder", "description", "domain", "scope", "access_key", "secret_key"},
		},
		{
			"jenkins_credential_azure_service_principal",
			newCredentialAzureServicePrincipalResource,
			[]string{"id", "name", "folder", "description", "domain", "scope", "subscription_id", "client_id", "tenant"},
		},
		{
			"jenkins_credential_github_app",
			newCredentialGitHubAppResource,
			[]string{"id", "name", "folder", "description", "domain", "scope", "app_id", "private_key"},
		},
		{
			"jenkins_credential_secret_file",
			newCredentialSecretFileResource,
			[]string{"id", "name", "folder", "description", "domain", "scope", "filename", "secretbytes"},
		},
		{
			"jenkins_credential_secret_text",
			newCredentialSecretTextResource,
			[]string{"id", "name", "folder", "description", "domain", "scope", "secret"},
		},
		{
			"jenkins_credential_ssh",
			newCredentialSSHResource,
			[]string{"id", "name", "folder", "description", "domain", "scope", "username", "privatekey"},
		},
		{
			"jenkins_credential_username",
			newCredentialUsernameResource,
			[]string{"id", "name", "folder", "description", "domain", "scope", "username", "password"},
		},
		{
			"jenkins_credential_vault_approle",
			newCredentialVaultAppRoleResource,
			[]string{"id", "name", "folder", "description", "domain", "scope", "role_id", "secret_id"},
		},
		{
			"jenkins_view",
			newViewResource,
			[]string{"id", "name", "folder", "assigned_projects", "description", "url"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.typeName, func(t *testing.T) {
			r := tt.newFn()
			resp := &fwresource.SchemaResponse{}
			r.Schema(context.Background(), fwresource.SchemaRequest{}, resp)
			for _, attr := range tt.attributes {
				a, ok := resp.Schema.Attributes[attr]
				if !ok {
					t.Errorf("Schema() missing attribute %q", attr)
					continue
				}
				if a == nil {
					t.Errorf("Schema() attribute %q is nil", attr)
				}
			}
		})
	}
}

// TestAllResourcesSchemaBaseTypes verifies that base credential string attributes
// have the correct type and Computed flag on every credential resource.
func TestAllResourcesSchemaBaseTypes(t *testing.T) {
	credentialResources := []struct {
		typeName string
		newFn    func() fwresource.Resource
	}{
		{"jenkins_credential_aws", newCredentialAwsResource},
		{"jenkins_credential_azure_service_principal", newCredentialAzureServicePrincipalResource},
		{"jenkins_credential_github_app", newCredentialGitHubAppResource},
		{"jenkins_credential_secret_file", newCredentialSecretFileResource},
		{"jenkins_credential_secret_text", newCredentialSecretTextResource},
		{"jenkins_credential_ssh", newCredentialSSHResource},
		{"jenkins_credential_username", newCredentialUsernameResource},
		{"jenkins_credential_vault_approle", newCredentialVaultAppRoleResource},
	}
	for _, tt := range credentialResources {
		t.Run(tt.typeName, func(t *testing.T) {
			r := tt.newFn()
			resp := &fwresource.SchemaResponse{}
			r.Schema(context.Background(), fwresource.SchemaRequest{}, resp)
			s := resp.Schema.Attributes

			// id must be Computed (set by provider).
			if idAttr, ok := s["id"].(schema.StringAttribute); ok {
				if !idAttr.Computed {
					t.Error("attribute \"id\" should be Computed")
				}
			}
			// name must be Required and not Computed.
			if nameAttr, ok := s["name"].(schema.StringAttribute); ok {
				if !nameAttr.Required {
					t.Error("attribute \"name\" should be Required")
				}
				if nameAttr.Computed {
					t.Error("attribute \"name\" should not be Computed")
				}
			}
			// description/domain/scope carry static defaults → Optional+Computed.
			for _, key := range []string{"description", "domain", "scope"} {
				if attr, ok := s[key].(schema.StringAttribute); ok {
					if !attr.Computed {
						t.Errorf("attribute %q should be Computed (carries a static default)", key)
					}
				}
			}
		})
	}
}
