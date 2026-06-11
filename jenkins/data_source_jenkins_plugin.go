package jenkins

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type pluginDataSourceModel struct {
	ID       types.String `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	Version  types.String `tfsdk:"version"`
	LongName types.String `tfsdk:"long_name"`
	URL      types.String `tfsdk:"url"`
	Active   types.Bool   `tfsdk:"active"`
	Enabled  types.Bool   `tfsdk:"enabled"`
}

type pluginDataSource struct {
	*dataSourceHelper
}

// Ensure the implementation satisfies the desired interfaces.
var _ datasource.DataSourceWithConfigure = &pluginDataSource{}

func newPluginDataSource() datasource.DataSource {
	return &pluginDataSource{
		dataSourceHelper: newDataSourceHelper(),
	}
}

func (d *pluginDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_plugin"
}

func (d *pluginDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Get information about an installed Jenkins plugin by its short name.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "The short name of the plugin, used as the resource identifier.",
				Computed:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "The short name of the plugin to look up (e.g. `workflow-multibranch`).",
				Required:            true,
			},
			"version": schema.StringAttribute{
				MarkdownDescription: "The installed version of the plugin (e.g. `756.v891d88f2cd46`).",
				Computed:            true,
			},
			"long_name": schema.StringAttribute{
				MarkdownDescription: "The human-readable display name of the plugin.",
				Computed:            true,
			},
			"url": schema.StringAttribute{
				MarkdownDescription: "The URL of the plugin's homepage.",
				Computed:            true,
			},
			"active": schema.BoolAttribute{
				MarkdownDescription: "Whether the plugin is currently active.",
				Computed:            true,
			},
			"enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the plugin is enabled.",
				Computed:            true,
			},
		},
	}
}

func (d *pluginDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data pluginDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	plugin, err := d.client.GetPlugin(ctx, data.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Unable to Read Data Source",
			"An unexpected error occurred while reading the Jenkins plugin. "+
				"Please report this issue to the provider developers.\n\n"+
				"Error: "+err.Error(),
		)

		return
	}

	data.ID = types.StringValue(plugin.ShortName)
	data.Version = types.StringValue(plugin.Version)
	data.LongName = types.StringValue(plugin.LongName)
	data.URL = types.StringValue(plugin.URL)
	data.Active = types.BoolValue(plugin.Active)
	data.Enabled = types.BoolValue(plugin.Enabled)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
