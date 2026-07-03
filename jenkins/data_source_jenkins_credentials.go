package jenkins

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

type credentialsDataSourceModel struct {
	ID          types.String `tfsdk:"id"`
	Folder      types.String `tfsdk:"folder"`
	Domain      types.String `tfsdk:"domain"`
	Credentials types.Set    `tfsdk:"credentials"`
}

type credentialsDataSource struct {
	*dataSourceHelper
}

var _ datasource.DataSourceWithConfigure = &credentialsDataSource{}

func newCredentialsDataSource() datasource.DataSource {
	return &credentialsDataSource{dataSourceHelper: newDataSourceHelper()}
}

func (d *credentialsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credentials"
}

func (d *credentialsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the IDs of the credentials in a Jenkins credential store domain.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Identifier for this data source, derived from the folder and domain.",
			},
			"folder": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The folder whose credential store is read. If not set, the global store is used.",
			},
			"domain": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "The domain within the credential store. Defaults to the global domain.",
			},
			"credentials": schema.SetAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "The IDs of the credentials in the store domain.",
			},
		},
	}
}

func (d *credentialsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	tflog.Debug(ctx, "credentialsDataSource.Read")
	var data credentialsDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if data.Domain.IsNull() {
		data.Domain = basetypes.NewStringValue(defaultCredentialDomain)
	}

	cm := d.client.Credentials()
	cm.Folder = formatFolderName(data.Folder.ValueString())

	ids, err := cm.List(ctx, data.Domain.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to List Credentials", err.Error())
		return
	}

	list, diags := types.SetValueFrom(ctx, types.StringType, ids)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Credentials = list
	data.ID = types.StringValue(generateCredentialID(data.Folder.ValueString(), data.Domain.ValueString()))

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
