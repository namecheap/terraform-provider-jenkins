package jenkins

import (
	"context"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

type credentialSecretTextDataSourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Folder      types.String `tfsdk:"folder"`
	Description types.String `tfsdk:"description"`
	Domain      types.String `tfsdk:"domain"`
	Scope       types.String `tfsdk:"scope"`
}

type credentialSecretTextDataSource struct {
	*dataSourceHelper
}

// Ensure the implementation satisfies the desired interfaces.
var _ datasource.DataSourceWithConfigure = &credentialSecretTextDataSource{}

func newCredentialSecretTextDataSource() datasource.DataSource {
	return &credentialSecretTextDataSource{
		dataSourceHelper: newDataSourceHelper(),
	}
}

func (d *credentialSecretTextDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_secret_text"
}

func (d *credentialSecretTextDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Get the attributes of a secret text credential within Jenkins.",
		Attributes:          d.schemaCredential(map[string]schema.Attribute{}),
	}
}

func (d *credentialSecretTextDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	tflog.Debug(ctx, "credentialSecretTextDataSource.Read")
	var data credentialSecretTextDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cm := d.client.Credentials()
	cm.Folder = formatFolderName(data.Folder.ValueString())

	if data.Domain.IsNull() {
		data.Domain = basetypes.NewStringValue(defaultCredentialDomain)
	}

	cred := jenkins.StringCredentials{}
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

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
