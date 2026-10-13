package jenkins

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

type nodeSecretDataSourceModel struct {
	Name   types.String `tfsdk:"name"`
	Secret types.String `tfsdk:"secret"`
}

type nodeSecretDataSource struct {
	*dataSourceHelper
}

var _ datasource.DataSourceWithConfigure = &nodeSecretDataSource{}

func newNodeSecretDataSource() datasource.DataSource {
	return &nodeSecretDataSource{
		dataSourceHelper: newDataSourceHelper(),
	}
}

func (d *nodeSecretDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_node_secret"
}

func (d *nodeSecretDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Get the connection secret of an inbound (JNLP) agent node, as passed to `agent.jar -secret`. " +
			"Requires the Agent/Connect permission on the node.\n\n" +
			"The secret is marked sensitive but is stored in plain text in the Terraform state; secure the state backend accordingly.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The name of the agent node.",
			},
			"secret": schema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "The inbound agent connection secret.",
			},
		},
	}
}

func (d *nodeSecretDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	tflog.Debug(ctx, "nodeSecretDataSource.Read")
	var data nodeSecretDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	secret, err := d.client.GetNodeSecret(ctx, data.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Read Node Secret",
			"An unexpected error occurred while reading the node secret.\n\nError: "+err.Error(),
		)
		return
	}

	data.Secret = types.StringValue(secret)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
