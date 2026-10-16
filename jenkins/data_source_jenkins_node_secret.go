package jenkins

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// nodeSecretModel backs both the jenkins_node_secret data source and the
// ephemeral resource, so their schemas must keep the same attributes.
type nodeSecretModel struct {
	Name   types.String `tfsdk:"name"`
	Secret types.String `tfsdk:"secret"`
}

const (
	nodeSecretNameDescription   = "The name of the agent node."
	nodeSecretSecretDescription = "The inbound agent connection secret."
)

// nodeSecretNameValidators rejects an empty name at validate time rather
// than when the secret is fetched.
var nodeSecretNameValidators = []validator.String{stringvalidator.LengthAtLeast(1)}

// readNodeSecret fills data.Secret for the node named in data.Name.
func readNodeSecret(ctx context.Context, client frameworkClient, data *nodeSecretModel) diag.Diagnostics {
	var diags diag.Diagnostics
	if client == nil {
		diags.AddError(
			"Unconfigured Jenkins Client",
			"The provider was not configured before the node secret was read. Please report this issue to the provider developers.",
		)
		return diags
	}

	secret, err := client.GetNodeSecret(ctx, data.Name.ValueString())
	if err != nil {
		diags.AddError(
			"Unable to Read Node Secret",
			"An unexpected error occurred while reading the node secret.\n\nError: "+err.Error(),
		)
		return diags
	}

	data.Secret = types.StringValue(secret)
	return diags
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
			"The secret is marked sensitive but is stored in plain text in the Terraform state; secure the state backend accordingly, " +
			"or use the `jenkins_node_secret` ephemeral resource to keep it out of the state.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: nodeSecretNameDescription,
				Validators:          nodeSecretNameValidators,
			},
			"secret": schema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: nodeSecretSecretDescription,
			},
		},
	}
}

func (d *nodeSecretDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	tflog.Debug(ctx, "nodeSecretDataSource.Read")
	var data nodeSecretModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(readNodeSecret(ctx, d.client, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
