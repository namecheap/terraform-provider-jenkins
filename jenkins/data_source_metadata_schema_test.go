package jenkins

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
)

func TestAllDataSourcesMetadata(t *testing.T) {
	tests := []struct {
		typeName string
		newFn    func() datasource.DataSource
	}{
		{"jenkins_credential_aws", newCredentialAwsDataSource},
		{"jenkins_credential_azure_service_principal", newCredentialAzureServicePrincipalDataSource},
		{"jenkins_credential_secret_file", newCredentialSecretFileDataSource},
		{"jenkins_credential_secret_text", newCredentialSecretTextDataSource},
		{"jenkins_credential_ssh", newCredentialSSHDataSource},
		{"jenkins_credential_username", newCredentialUsernameDataSource},
		{"jenkins_credential_vault_approle", newCredentialVaultAppRoleDataSource},
		{"jenkins_folder", newFolderDataSource},
		{"jenkins_job", newJobDataSource},
		{"jenkins_plugin", newPluginDataSource},
		{"jenkins_view", newViewDataSource},
	}
	for _, tt := range tests {
		t.Run(tt.typeName, func(t *testing.T) {
			ds := tt.newFn()
			if ds == nil {
				t.Fatal("constructor returned nil")
			}
			resp := &datasource.MetadataResponse{}
			ds.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "jenkins"}, resp)
			if resp.TypeName != tt.typeName {
				t.Errorf("TypeName = %q, want %q", resp.TypeName, tt.typeName)
			}
		})
	}
}

func TestAllDataSourcesSchema(t *testing.T) {
	tests := []struct {
		typeName   string
		newFn      func() datasource.DataSource
		attributes []string
	}{
		{
			"jenkins_credential_aws",
			newCredentialAwsDataSource,
			[]string{"id", "name", "folder", "description", "domain", "scope", "access_key", "iam_role_arn"},
		},
		{
			"jenkins_credential_azure_service_principal",
			newCredentialAzureServicePrincipalDataSource,
			[]string{"id", "name", "folder", "description", "domain", "scope"},
		},
		{
			"jenkins_credential_secret_file",
			newCredentialSecretFileDataSource,
			[]string{"id", "name", "folder", "description", "domain", "scope", "filename"},
		},
		{
			"jenkins_credential_secret_text",
			newCredentialSecretTextDataSource,
			[]string{"id", "name", "folder", "description", "domain", "scope"},
		},
		{
			"jenkins_credential_ssh",
			newCredentialSSHDataSource,
			[]string{"id", "name", "folder", "description", "domain", "scope", "username"},
		},
		{
			"jenkins_credential_username",
			newCredentialUsernameDataSource,
			[]string{"id", "name", "folder", "description", "domain", "scope", "username"},
		},
		{
			"jenkins_credential_vault_approle",
			newCredentialVaultAppRoleDataSource,
			[]string{"id", "name", "folder", "description", "domain", "scope", "role_id", "namespace"},
		},
		{
			"jenkins_folder",
			newFolderDataSource,
			[]string{"id", "name", "folder", "description", "template"},
		},
		{
			"jenkins_job",
			newJobDataSource,
			[]string{"id", "name", "folder", "template"},
		},
		{
			"jenkins_plugin",
			newPluginDataSource,
			[]string{"id", "name", "version"},
		},
		{
			"jenkins_view",
			newViewDataSource,
			[]string{"id", "name", "description", "url"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.typeName, func(t *testing.T) {
			ds := tt.newFn()
			resp := &datasource.SchemaResponse{}
			ds.Schema(context.Background(), datasource.SchemaRequest{}, resp)
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
