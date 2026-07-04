package jenkins

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type credentialVaultAppRoleDataSourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Folder      types.String `tfsdk:"folder"`
	Description types.String `tfsdk:"description"`
	Domain      types.String `tfsdk:"domain"`
	Scope       types.String `tfsdk:"scope"`
	Namespace   types.String `tfsdk:"namespace"`
	Path        types.String `tfsdk:"path"`
	RoleID      types.String `tfsdk:"role_id"`
}

type credentialVaultAppRoleDataSource struct {
	*credentialDataSource[credentialVaultAppRoleDataSourceModel]
}

// Ensure the implementation satisfies the desired interfaces.
var _ datasource.DataSourceWithConfigure = &credentialVaultAppRoleDataSource{}

func newCredentialVaultAppRoleDataSource() datasource.DataSource {
	return &credentialVaultAppRoleDataSource{
		credentialDataSource: newCredentialDataSource(vaultAppRoleCredentialDataSourceReader()),
	}
}

func vaultAppRoleCredentialDataSourceReader() credentialDataSourceReader[credentialVaultAppRoleDataSourceModel] {
	return credentialDataSourceReader[credentialVaultAppRoleDataSourceModel]{
		folder:      func(m *credentialVaultAppRoleDataSourceModel) types.String { return m.Folder },
		name:        func(m *credentialVaultAppRoleDataSourceModel) types.String { return m.Name },
		domain:      func(m *credentialVaultAppRoleDataSourceModel) types.String { return m.Domain },
		setDomain:   func(m *credentialVaultAppRoleDataSourceModel, v string) { m.Domain = types.StringValue(v) },
		setID:       func(m *credentialVaultAppRoleDataSourceModel, id string) { m.ID = types.StringValue(id) },
		newAPIValue: func() interface{} { return &VaultAppRoleCredentials{} },
		fromAPI: func(api interface{}, m *credentialVaultAppRoleDataSourceModel) {
			cred := api.(*VaultAppRoleCredentials)
			m.Scope = types.StringValue(cred.Scope)
			m.Description = types.StringValue(cred.Description)
			m.Namespace = types.StringValue(cred.Namespace)
			m.Path = types.StringValue(cred.Path)
			m.RoleID = types.StringValue(cred.RoleID)
		},
	}
}

func (d *credentialVaultAppRoleDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_vault_approle"
}

func (d *credentialVaultAppRoleDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Get the attributes of a vault approle credential within Jenkins.",
		Attributes: d.schemaCredential(map[string]schema.Attribute{
			"namespace": schema.StringAttribute{
				MarkdownDescription: "The Vault namespace of the approle credential.",
				Computed:            true,
			},
			"path": schema.StringAttribute{
				MarkdownDescription: "The unique name of the approle auth backend.",
				Computed:            true,
			},
			"role_id": schema.StringAttribute{
				MarkdownDescription: "The role_id associated with the credentials.",
				Computed:            true,
			},
		}),
	}
}
