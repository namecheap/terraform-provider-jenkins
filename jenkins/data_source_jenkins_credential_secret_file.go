package jenkins

import (
	"context"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type credentialSecretFileDataSourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Folder      types.String `tfsdk:"folder"`
	Description types.String `tfsdk:"description"`
	Domain      types.String `tfsdk:"domain"`
	Scope       types.String `tfsdk:"scope"`
	Filename    types.String `tfsdk:"filename"`
}

type credentialSecretFileDataSource struct {
	*credentialDataSource[credentialSecretFileDataSourceModel]
}

// Ensure the implementation satisfies the desired interfaces.
var _ datasource.DataSourceWithConfigure = &credentialSecretFileDataSource{}

func newCredentialSecretFileDataSource() datasource.DataSource {
	return &credentialSecretFileDataSource{
		credentialDataSource: newCredentialDataSource(secretFileCredentialDataSourceReader()),
	}
}

func secretFileCredentialDataSourceReader() credentialDataSourceReader[credentialSecretFileDataSourceModel] {
	return credentialDataSourceReader[credentialSecretFileDataSourceModel]{
		folder:      func(m *credentialSecretFileDataSourceModel) types.String { return m.Folder },
		name:        func(m *credentialSecretFileDataSourceModel) types.String { return m.Name },
		domain:      func(m *credentialSecretFileDataSourceModel) types.String { return m.Domain },
		setDomain:   func(m *credentialSecretFileDataSourceModel, v string) { m.Domain = types.StringValue(v) },
		setID:       func(m *credentialSecretFileDataSourceModel, id string) { m.ID = types.StringValue(id) },
		newAPIValue: func() interface{} { return &jenkins.FileCredentials{} },
		fromAPI: func(api interface{}, m *credentialSecretFileDataSourceModel) {
			cred := api.(*jenkins.FileCredentials)
			m.Scope = types.StringValue(cred.Scope)
			m.Description = types.StringValue(cred.Description)
			m.Filename = types.StringValue(cred.Filename)
		},
	}
}

func (d *credentialSecretFileDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_secret_file"
}

func (d *credentialSecretFileDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Get the attributes of a secret file credential within Jenkins.",
		Attributes: d.schemaCredential(map[string]schema.Attribute{
			"filename": schema.StringAttribute{
				MarkdownDescription: "The filename of the secret file as stored on the Jenkins server.",
				Computed:            true,
			},
		}),
	}
}
