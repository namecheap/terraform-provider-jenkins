package jenkins

import (
	"context"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type credentialSecretTextDataSourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Folder      types.String `tfsdk:"folder"`
	Description types.String `tfsdk:"description"`
	Domain      types.String `tfsdk:"domain"`
	Scope       types.String `tfsdk:"scope"`
}

type credentialSecretTextDataSource struct {
	*credentialDataSource[credentialSecretTextDataSourceModel]
}

// Ensure the implementation satisfies the desired interfaces.
var _ datasource.DataSourceWithConfigure = &credentialSecretTextDataSource{}

func newCredentialSecretTextDataSource() datasource.DataSource {
	return &credentialSecretTextDataSource{
		credentialDataSource: newCredentialDataSource(secretTextCredentialDataSourceReader()),
	}
}

func secretTextCredentialDataSourceReader() credentialDataSourceReader[credentialSecretTextDataSourceModel] {
	return credentialDataSourceReader[credentialSecretTextDataSourceModel]{
		folder:      func(m *credentialSecretTextDataSourceModel) types.String { return m.Folder },
		name:        func(m *credentialSecretTextDataSourceModel) types.String { return m.Name },
		domain:      func(m *credentialSecretTextDataSourceModel) types.String { return m.Domain },
		setDomain:   func(m *credentialSecretTextDataSourceModel, v string) { m.Domain = types.StringValue(v) },
		setID:       func(m *credentialSecretTextDataSourceModel, id string) { m.ID = types.StringValue(id) },
		newAPIValue: func() interface{} { return &jenkins.StringCredentials{} },
		fromAPI: func(api interface{}, m *credentialSecretTextDataSourceModel) {
			cred := api.(*jenkins.StringCredentials)
			m.Scope = types.StringValue(cred.Scope)
			m.Description = types.StringValue(cred.Description)
		},
	}
}

func (d *credentialSecretTextDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_secret_text"
}

func (d *credentialSecretTextDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Get the attributes of a secret text credential within Jenkins.",
		Attributes:          d.schemaCredential(map[string]schema.Attribute{}),
	}
}
