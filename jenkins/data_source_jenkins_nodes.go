package jenkins

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

type nodesDataSourceModel struct {
	ID    types.String `tfsdk:"id"`
	Nodes types.Set    `tfsdk:"nodes"`
}

type nodesDataSource struct {
	*dataSourceHelper
}

var _ datasource.DataSourceWithConfigure = &nodesDataSource{}

func newNodesDataSource() datasource.DataSource {
	return &nodesDataSource{dataSourceHelper: newDataSourceHelper()}
}

func (d *nodesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_nodes"
}

func (d *nodesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the names of all agent nodes known to Jenkins (including the built-in node).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Static identifier for this data source.",
			},
			"nodes": schema.SetAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "The names of all nodes known to Jenkins.",
			},
		},
	}
}

func (d *nodesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	tflog.Debug(ctx, "nodesDataSource.Read")
	var data nodesDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	nodes, err := d.client.GetAllNodes(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to List Nodes", err.Error())
		return
	}

	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		names = append(names, n.GetName())
	}

	list, diags := types.SetValueFrom(ctx, types.StringType, names)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Nodes = list
	data.ID = types.StringValue("nodes")

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
