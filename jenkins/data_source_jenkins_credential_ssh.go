package jenkins

import (
	"context"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type credentialSSHDataSourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Folder      types.String `tfsdk:"folder"`
	Description types.String `tfsdk:"description"`
	Domain      types.String `tfsdk:"domain"`
	Scope       types.String `tfsdk:"scope"`
	Username    types.String `tfsdk:"username"`
}

type credentialSSHDataSource struct {
	*credentialDataSource[credentialSSHDataSourceModel]
}

// Ensure the implementation satisfies the desired interfaces.
var _ datasource.DataSourceWithConfigure = &credentialSSHDataSource{}

func newCredentialSSHDataSource() datasource.DataSource {
	return &credentialSSHDataSource{
		credentialDataSource: newCredentialDataSource(sshCredentialDataSourceReader()),
	}
}

func sshCredentialDataSourceReader() credentialDataSourceReader[credentialSSHDataSourceModel] {
	return credentialDataSourceReader[credentialSSHDataSourceModel]{
		folder:      func(m *credentialSSHDataSourceModel) types.String { return m.Folder },
		name:        func(m *credentialSSHDataSourceModel) types.String { return m.Name },
		domain:      func(m *credentialSSHDataSourceModel) types.String { return m.Domain },
		setDomain:   func(m *credentialSSHDataSourceModel, v string) { m.Domain = types.StringValue(v) },
		setID:       func(m *credentialSSHDataSourceModel, id string) { m.ID = types.StringValue(id) },
		newAPIValue: func() interface{} { return &jenkins.SSHCredentials{} },
		fromAPI: func(api interface{}, m *credentialSSHDataSourceModel) {
			cred := api.(*jenkins.SSHCredentials)
			m.Scope = types.StringValue(cred.Scope)
			m.Description = types.StringValue(cred.Description)
			m.Username = types.StringValue(cred.Username)
		},
	}
}

func (d *credentialSSHDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_ssh"
}

func (d *credentialSSHDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Get the attributes of an SSH credential within Jenkins.",
		Attributes: d.schemaCredential(map[string]schema.Attribute{
			"username": schema.StringAttribute{
				MarkdownDescription: "The username associated with the SSH credential.",
				Computed:            true,
			},
		}),
	}
}
