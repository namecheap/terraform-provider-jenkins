package jenkins

import (
	"context"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type credentialUsernameDataSourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Folder      types.String `tfsdk:"folder"`
	Description types.String `tfsdk:"description"`
	Domain      types.String `tfsdk:"domain"`
	Scope       types.String `tfsdk:"scope"`
	Username    types.String `tfsdk:"username"`
}

type credentialUsernameDataSource struct {
	*credentialDataSource[credentialUsernameDataSourceModel]
}

// Ensure the implementation satisfies the desired interfaces.
var _ datasource.DataSourceWithConfigure = &credentialUsernameDataSource{}

func newCredentialUsernameDataSource() datasource.DataSource {
	return &credentialUsernameDataSource{
		credentialDataSource: newCredentialDataSource(usernameCredentialDataSourceReader()),
	}
}

func usernameCredentialDataSourceReader() credentialDataSourceReader[credentialUsernameDataSourceModel] {
	return credentialDataSourceReader[credentialUsernameDataSourceModel]{
		folder:      func(m *credentialUsernameDataSourceModel) types.String { return m.Folder },
		name:        func(m *credentialUsernameDataSourceModel) types.String { return m.Name },
		domain:      func(m *credentialUsernameDataSourceModel) types.String { return m.Domain },
		setDomain:   func(m *credentialUsernameDataSourceModel, v string) { m.Domain = types.StringValue(v) },
		setID:       func(m *credentialUsernameDataSourceModel, id string) { m.ID = types.StringValue(id) },
		newAPIValue: func() interface{} { return &jenkins.UsernameCredentials{} },
		fromAPI: func(api interface{}, m *credentialUsernameDataSourceModel) {
			cred := api.(*jenkins.UsernameCredentials)
			m.Scope = types.StringValue(cred.Scope)
			m.Description = types.StringValue(cred.Description)
			m.Username = types.StringValue(cred.Username)
		},
	}
}

func (d *credentialUsernameDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_username"
}

func (d *credentialUsernameDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Get the attributes of a username credential within Jenkins.",
		Attributes: d.schemaCredential(map[string]schema.Attribute{
			"username": schema.StringAttribute{
				MarkdownDescription: "The username associated with the credentials.",
				Computed:            true,
			},
		}),
	}
}
