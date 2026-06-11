package jenkins

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

type credentialAzureServicePrincipalDataSourceModel struct {
	ID                      types.String `tfsdk:"id"`
	Name                    types.String `tfsdk:"name"`
	Folder                  types.String `tfsdk:"folder"`
	Description             types.String `tfsdk:"description"`
	Domain                  types.String `tfsdk:"domain"`
	Scope                   types.String `tfsdk:"scope"`
	SubscriptionId          types.String `tfsdk:"subscription_id"`
	ClientId                types.String `tfsdk:"client_id"`
	Tenant                  types.String `tfsdk:"tenant"`
	AzureEnvironmentName    types.String `tfsdk:"azure_environment_name"`
	ServiceManagementURL    types.String `tfsdk:"service_management_url"`
	AuthenticationEndpoint  types.String `tfsdk:"authentication_endpoint"`
	ResourceManagerEndpoint types.String `tfsdk:"resource_manager_endpoint"`
	GraphEndpoint           types.String `tfsdk:"graph_endpoint"`
}

type credentialAzureServicePrincipalDataSource struct {
	*dataSourceHelper
}

// Ensure the implementation satisfies the desired interfaces.
var _ datasource.DataSourceWithConfigure = &credentialAzureServicePrincipalDataSource{}

func newCredentialAzureServicePrincipalDataSource() datasource.DataSource {
	return &credentialAzureServicePrincipalDataSource{
		dataSourceHelper: newDataSourceHelper(),
	}
}

func (d *credentialAzureServicePrincipalDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_azure_service_principal"
}

func (d *credentialAzureServicePrincipalDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Get the attributes of an Azure Service Principal credential within Jenkins.",
		Attributes: d.schemaCredential(map[string]schema.Attribute{
			"subscription_id": schema.StringAttribute{
				MarkdownDescription: "The Azure subscription id mapped to the Azure Service Principal.",
				Computed:            true,
			},
			"client_id": schema.StringAttribute{
				MarkdownDescription: "The client id (application id) of the Azure Service Principal.",
				Computed:            true,
			},
			"tenant": schema.StringAttribute{
				MarkdownDescription: "The Azure Tenant ID of the Azure Service Principal.",
				Computed:            true,
			},
			"azure_environment_name": schema.StringAttribute{
				MarkdownDescription: `The Azure Cloud environment name. Allowed values are "Azure", "Azure China", "Azure Germany", "Azure US Government".`,
				Computed:            true,
			},
			"service_management_url": schema.StringAttribute{
				MarkdownDescription: "Override the Azure management endpoint URL for the selected Azure environment.",
				Computed:            true,
			},
			"authentication_endpoint": schema.StringAttribute{
				MarkdownDescription: "Override the Azure Active Directory endpoint for the selected Azure environment.",
				Computed:            true,
			},
			"resource_manager_endpoint": schema.StringAttribute{
				MarkdownDescription: "Override the Azure resource manager endpoint URL for the selected Azure environment.",
				Computed:            true,
			},
			"graph_endpoint": schema.StringAttribute{
				MarkdownDescription: "Override the Azure graph endpoint URL for the selected Azure environment.",
				Computed:            true,
			},
		}),
	}
}

func (d *credentialAzureServicePrincipalDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data credentialAzureServicePrincipalDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cm := d.client.Credentials()
	cm.Folder = formatFolderName(data.Folder.ValueString())

	if data.Domain.IsNull() {
		data.Domain = basetypes.NewStringValue(defaultCredentialDomain)
	}

	cred := AzureServicePrincipalCredentials{}
	err := cm.GetSingle(ctx, data.Domain.ValueString(), data.Name.ValueString(), &cred)
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Read Data Source",
			"An unexpected error occurred while parsing the data source read response. "+
				"Please report this issue to the provider developers.\n\n"+
				"Error: "+err.Error(),
		)

		return
	}

	data.ID = types.StringValue(generateCredentialID(data.Folder.ValueString(), cred.ID))
	data.Scope = types.StringValue(cred.Scope)
	data.Description = types.StringValue(cred.Description)
	data.SubscriptionId = types.StringValue(cred.Data.SubscriptionId)
	data.ClientId = types.StringValue(cred.Data.ClientId)
	data.Tenant = types.StringValue(cred.Data.Tenant)
	data.AzureEnvironmentName = types.StringValue(cred.Data.AzureEnvironmentName)
	data.ServiceManagementURL = types.StringValue(cred.Data.ServiceManagementURL)
	data.AuthenticationEndpoint = types.StringValue(cred.Data.AuthenticationEndpoint)
	data.ResourceManagerEndpoint = types.StringValue(cred.Data.ResourceManagerEndpoint)
	data.GraphEndpoint = types.StringValue(cred.Data.GraphEndpoint)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
