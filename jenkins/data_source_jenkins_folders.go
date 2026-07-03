package jenkins

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

type foldersDataSourceModel struct {
	ID      types.String `tfsdk:"id"`
	Folder  types.String `tfsdk:"folder"`
	Folders types.Set    `tfsdk:"folders"`
}

type foldersDataSource struct {
	*dataSourceHelper
}

var _ datasource.DataSourceWithConfigure = &foldersDataSource{}

func newFoldersDataSource() datasource.DataSource {
	return &foldersDataSource{dataSourceHelper: newDataSourceHelper()}
}

func (d *foldersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_folders"
}

func (d *foldersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the names of the folders directly within a Jenkins folder (or the root).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The parent folder whose subfolders are listed, or `/` for the root.",
			},
			"folder": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The parent folder to list subfolders from. If not set, the Jenkins root is listed.",
			},
			"folders": schema.SetAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "The names of the folders directly within the parent folder.",
			},
		},
	}
}

func (d *foldersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	tflog.Debug(ctx, "foldersDataSource.Read")
	var data foldersDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	inner, err := listInnerJobs(ctx, d.client, data.Folder.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to List Folders", err.Error())
		return
	}

	names := make([]string, 0, len(inner))
	for _, j := range inner {
		if isFolderClass(j.Class) {
			names = append(names, j.Name)
		}
	}

	folders, diags := types.SetValueFrom(ctx, types.StringType, names)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Folders = folders
	if id := formatFolderID(extractFolders(data.Folder.ValueString())); id != "" {
		data.ID = types.StringValue(id)
	} else {
		data.ID = types.StringValue("/")
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
