package jenkins

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

type nodeSecretEphemeralResource struct {
	client frameworkClient
}

var _ ephemeral.EphemeralResourceWithConfigure = &nodeSecretEphemeralResource{}

func newNodeSecretEphemeralResource() ephemeral.EphemeralResource {
	return &nodeSecretEphemeralResource{}
}

func (e *nodeSecretEphemeralResource) Metadata(_ context.Context, req ephemeral.MetadataRequest, resp *ephemeral.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_node_secret"
}

func (e *nodeSecretEphemeralResource) Schema(_ context.Context, _ ephemeral.SchemaRequest, resp *ephemeral.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Get the connection secret of an inbound (JNLP) agent node, as passed to `agent.jar -secret`, " +
			"without storing it in the plan or the state. Requires the Agent/Connect permission on the node.\n\n" +
			"Use it to feed a write-only argument, such as `secret_string_wo` of `aws_secretsmanager_secret_version`.",
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

func (e *nodeSecretEphemeralResource) Configure(_ context.Context, req ephemeral.ConfigureRequest, resp *ephemeral.ConfigureResponse) {
	client, diags := clientFromProviderData(req.ProviderData)
	resp.Diagnostics.Append(diags...)
	e.client = client
}

func (e *nodeSecretEphemeralResource) Open(ctx context.Context, req ephemeral.OpenRequest, resp *ephemeral.OpenResponse) {
	tflog.Debug(ctx, "nodeSecretEphemeralResource.Open")
	var data nodeSecretModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(readNodeSecret(ctx, e.client, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.Result.Set(ctx, &data)...)
}
