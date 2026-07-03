package jenkins

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

type nodeDataSourceModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	NumExecutors types.Int64  `tfsdk:"num_executors"`
	Description  types.String `tfsdk:"description"`
	RemoteFS     types.String `tfsdk:"remote_fs"`
	Labels       types.String `tfsdk:"labels"`
	Online       types.Bool   `tfsdk:"online"`
}

type nodeDataSource struct {
	*dataSourceHelper
}

var _ datasource.DataSourceWithConfigure = &nodeDataSource{}

func newNodeDataSource() datasource.DataSource {
	return &nodeDataSource{
		dataSourceHelper: newDataSourceHelper(),
	}
}

func (d *nodeDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_node"
}

func (d *nodeDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Get the attributes of a permanent agent node within Jenkins.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The name of the node, which is also its unique identifier.",
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The name of the node to look up.",
			},
			"num_executors": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "The number of concurrent builds the node can run.",
			},
			"description": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "A human readable description of the node.",
			},
			"remote_fs": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The absolute path to the agent's root/working directory on the agent machine.",
			},
			"labels": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "A space-separated list of labels assigned to the node.",
			},
			"online": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether the node is currently connected and online.",
			},
		},
	}
}

func (d *nodeDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	tflog.Debug(ctx, "nodeDataSource.Read")
	var data nodeDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	node, err := d.client.GetNode(ctx, data.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Read Node",
			"An unexpected error occurred while reading the node.\n\nError: "+err.Error(),
		)
		return
	}

	var cfg nodeConfig
	if err := d.client.GetNodeConfig(ctx, data.Name.ValueString(), &cfg); err != nil {
		resp.Diagnostics.AddError(
			"Unable to Read Node",
			"An unexpected error occurred while reading the node configuration.\n\nError: "+err.Error(),
		)
		return
	}

	data.ID = data.Name
	data.NumExecutors = types.Int64Value(cfg.NumExecutors)
	data.Description = types.StringValue(cfg.Description)
	data.RemoteFS = types.StringValue(cfg.RemoteFS)
	data.Labels = types.StringValue(cfg.Label)
	data.Online = types.BoolValue(node != nil && node.Raw != nil && !node.Raw.Offline)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
