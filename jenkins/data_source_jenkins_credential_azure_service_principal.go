package jenkins

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type credentialAzureServicePrincipalDataSourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Folder      types.String `tfsdk:"folder"`
	Description types.String `tfsdk:"description"`
	Domain      types.String `tfsdk:"domain"`
	Scope       types.String `tfsdk:"scope"`
}

type credentialAzureServicePrincipalDataSource struct {
	*credentialDataSource[credentialAzureServicePrincipalDataSourceModel]
}

// Ensure the implementation satisfies the desired interfaces.
var _ datasource.DataSourceWithConfigure = &credentialAzureServicePrincipalDataSource{}

func newCredentialAzureServicePrincipalDataSource() datasource.DataSource {
	return &credentialAzureServicePrincipalDataSource{
		credentialDataSource: newCredentialDataSource(azureServicePrincipalCredentialDataSourceReader()),
	}
}

func azureServicePrincipalCredentialDataSourceReader() credentialDataSourceReader[credentialAzureServicePrincipalDataSourceModel] {
	return credentialDataSourceReader[credentialAzureServicePrincipalDataSourceModel]{
		folder:      func(m *credentialAzureServicePrincipalDataSourceModel) types.String { return m.Folder },
		name:        func(m *credentialAzureServicePrincipalDataSourceModel) types.String { return m.Name },
		domain:      func(m *credentialAzureServicePrincipalDataSourceModel) types.String { return m.Domain },
		setDomain:   func(m *credentialAzureServicePrincipalDataSourceModel, v string) { m.Domain = types.StringValue(v) },
		setID:       func(m *credentialAzureServicePrincipalDataSourceModel, id string) { m.ID = types.StringValue(id) },
		newAPIValue: func() interface{} { return &AzureServicePrincipalCredentials{} },
		fromAPI: func(api interface{}, m *credentialAzureServicePrincipalDataSourceModel) {
			cred := api.(*AzureServicePrincipalCredentials)
			m.Scope = types.StringValue(cred.Scope)
			m.Description = types.StringValue(cred.Description)
		},
	}
}

func (d *credentialAzureServicePrincipalDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_azure_service_principal"
}

func (d *credentialAzureServicePrincipalDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Get the attributes of an Azure Service Principal credential within Jenkins.",
		Attributes:          d.schemaCredential(map[string]schema.Attribute{}),
	}
}
