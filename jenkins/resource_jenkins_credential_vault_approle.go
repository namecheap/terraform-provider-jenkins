package jenkins

import (
	"context"
	"encoding/xml"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// VaultAppRoleCredentials struct representing credential for storing Vault AppRole role id and secret id
type VaultAppRoleCredentials struct {
	XMLName     xml.Name `xml:"com.datapipe.jenkins.vault.credentials.VaultAppRoleCredential"`
	ID          string   `xml:"id"`
	Scope       string   `xml:"scope"`
	Description string   `xml:"description"`
	Namespace   string   `xml:"namespace"`
	Path        string   `xml:"path"`
	RoleID      string   `xml:"roleId"`
	SecretID    string   `xml:"secretId"`
}

type credentialVaultAppRoleResourceModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Folder            types.String `tfsdk:"folder"`
	Description       types.String `tfsdk:"description"`
	Domain            types.String `tfsdk:"domain"`
	Scope             types.String `tfsdk:"scope"`
	Namespace         types.String `tfsdk:"namespace"`
	Path              types.String `tfsdk:"path"`
	RoleID            types.String `tfsdk:"role_id"`
	SecretID          types.String `tfsdk:"secret_id"`
	SecretIDWo        types.String `tfsdk:"secret_id_wo"`
	SecretIDWoVersion types.String `tfsdk:"secret_id_wo_version"`
}

type credentialVaultAppRoleResource struct {
	*credentialCRUD[credentialVaultAppRoleResourceModel]
}

// Ensure the implementation satisfies the desired interfaces.
var _ resource.ResourceWithConfigure = &credentialVaultAppRoleResource{}
var _ resource.ResourceWithImportState = &credentialVaultAppRoleResource{}
var _ resource.ResourceWithConfigValidators = &credentialVaultAppRoleResource{}

func newCredentialVaultAppRoleResource() resource.Resource {
	return &credentialVaultAppRoleResource{
		credentialCRUD: newCredentialCRUD(vaultAppRoleCredentialSpec()),
	}
}

// vaultAppRoleCredentialSpec supplies the type-specific mapping for the shared
// credential CRUD flow (see credential_crud.go). Uses the local
// VaultAppRoleCredentials XML struct (the HashiCorp Vault plugin type).
func vaultAppRoleCredentialSpec() credentialSpec[credentialVaultAppRoleResourceModel] {
	return credentialSpec[credentialVaultAppRoleResourceModel]{
		identity: func(m *credentialVaultAppRoleResourceModel) (string, string, string) {
			return m.Folder.ValueString(), m.Domain.ValueString(), m.Name.ValueString()
		},
		setID: func(m *credentialVaultAppRoleResourceModel, id string) {
			m.ID = types.StringValue(id)
		},
		secretFields: []credentialSecretField[credentialVaultAppRoleResourceModel]{{
			name:         "secret_id",
			hasWriteOnly: true,
			plainValue:   func(m *credentialVaultAppRoleResourceModel) types.String { return m.SecretID },
			woVersion:    func(m *credentialVaultAppRoleResourceModel) types.String { return m.SecretIDWoVersion },
		}},
		build: func(m *credentialVaultAppRoleResourceModel, secrets map[string]string) interface{} {
			cred := &VaultAppRoleCredentials{
				ID:          m.Name.ValueString(),
				Scope:       m.Scope.ValueString(),
				Description: m.Description.ValueString(),
				Namespace:   m.Namespace.ValueString(),
				Path:        m.Path.ValueString(),
				RoleID:      m.RoleID.ValueString(),
			}
			if s, ok := secrets["secret_id"]; ok {
				cred.SecretID = s
			}
			return cred
		},
		newAPIValue: func() interface{} { return &VaultAppRoleCredentials{} },
		fromAPI: func(api interface{}, m *credentialVaultAppRoleResourceModel) {
			// NOTE: the secret id is intentionally not read back — GetSingle returns
			// a placeholder. Only Create/Update send it.
			cred := api.(*VaultAppRoleCredentials)
			m.Scope = types.StringValue(cred.Scope)
			m.Description = types.StringValue(cred.Description)
			m.Namespace = types.StringValue(cred.Namespace)
			m.Path = types.StringValue(cred.Path)
			m.RoleID = types.StringValue(cred.RoleID)
		},
	}
}

// Metadata should return the full name of the resource.
func (r *credentialVaultAppRoleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_vault_approle"
}

// Schema should return the schema for this resource.
func (r *credentialVaultAppRoleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `
Manages a Vault AppRole credential within Jenkins. This credential may then be referenced within jobs that are created.

~> The "secret_id" property may leave plain-text secret id in your state file. If using the property to manage the secret id in Terraform, ensure that your state file is properly secured and encrypted at rest.

~> The Jenkins installation that uses this resource is expected to have the [Hashicorp Vault Plugin](https://plugins.jenkins.io/hashicorp-vault-plugin/) installed in their system.`,
		Attributes: r.schemaCredential(addWriteOnlySecret(map[string]schema.Attribute{
			"namespace": schema.StringAttribute{
				MarkdownDescription: "The Vault namespace of the approle credential.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
			"path": schema.StringAttribute{
				MarkdownDescription: "The unique name of the approle auth backend. Defaults to `approle`.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("approle"),
			},
			"role_id": schema.StringAttribute{
				MarkdownDescription: "The role_id to be associated with the credentials.",
				Required:            true,
			},
			"secret_id": schema.StringAttribute{
				MarkdownDescription: "The secret_id to be associated with the credentials. If empty then the secret_id property will become unmanaged and expected to be set manually within Jenkins. If set then the secret_id will be updated only upon changes -- if the secret_id is set manually within Jenkins then it will not reconcile this drift until the next time the secret_id property is changed.",
				Optional:            true,
				Sensitive:           true,
			},
		}, "secret_id", "The secret_id to be associated with the credentials.")),
	}
}

// ConfigValidators enforces the plain/write-only secret constraints.
func (r *credentialVaultAppRoleResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return optionalWriteOnlySecretConfigValidators("secret_id")
}
