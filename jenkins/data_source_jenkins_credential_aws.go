package jenkins

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type credentialAwsDataSourceModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	Folder             types.String `tfsdk:"folder"`
	Description        types.String `tfsdk:"description"`
	Domain             types.String `tfsdk:"domain"`
	Scope              types.String `tfsdk:"scope"`
	AccessKey          types.String `tfsdk:"access_key"`
	IamRoleArn         types.String `tfsdk:"iam_role_arn"`
	IamMfaSerialNumber types.String `tfsdk:"iam_mfa_serial_number"`
}

type credentialAwsDataSource struct {
	*credentialDataSource[credentialAwsDataSourceModel]
}

// Ensure the implementation satisfies the desired interfaces.
var _ datasource.DataSourceWithConfigure = &credentialAwsDataSource{}

func newCredentialAwsDataSource() datasource.DataSource {
	return &credentialAwsDataSource{
		credentialDataSource: newCredentialDataSource(awsCredentialDataSourceReader()),
	}
}

func awsCredentialDataSourceReader() credentialDataSourceReader[credentialAwsDataSourceModel] {
	return credentialDataSourceReader[credentialAwsDataSourceModel]{
		folder:      func(m *credentialAwsDataSourceModel) types.String { return m.Folder },
		name:        func(m *credentialAwsDataSourceModel) types.String { return m.Name },
		domain:      func(m *credentialAwsDataSourceModel) types.String { return m.Domain },
		setDomain:   func(m *credentialAwsDataSourceModel, v string) { m.Domain = types.StringValue(v) },
		setID:       func(m *credentialAwsDataSourceModel, id string) { m.ID = types.StringValue(id) },
		newAPIValue: func() interface{} { return &credentialAws{} },
		fromAPI: func(api interface{}, m *credentialAwsDataSourceModel) {
			cred := api.(*credentialAws)
			m.Scope = types.StringValue(cred.Scope)
			m.Description = types.StringValue(cred.Description)
			m.AccessKey = types.StringValue(cred.AccessKey)
			m.IamRoleArn = types.StringValue(cred.IamRoleArn)
			m.IamMfaSerialNumber = types.StringValue(cred.IamMfaSerialNumber)
		},
	}
}

func (d *credentialAwsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_aws"
}

func (d *credentialAwsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Get the attributes of an AWS credential within Jenkins.",
		Attributes: d.schemaCredential(map[string]schema.Attribute{
			"access_key": schema.StringAttribute{
				MarkdownDescription: "An AWS access key ID. This is the public part of the key pair used to authenticate with AWS services.",
				Sensitive:           true,
				Computed:            true,
			},
			"iam_role_arn": schema.StringAttribute{
				MarkdownDescription: "An ARN specifying the IAM role to assume. The format should be something like: \"arn:aws:iam::123456789012:role/MyIAMRoleName\".",
				Computed:            true,
			},
			"iam_mfa_serial_number": schema.StringAttribute{
				MarkdownDescription: "The identifier for an MFA device. Either a serial number for hardware MFA devices, or an ARN for virtual devices.\n This is only required if the trust policy of the role being assumed includes a condition that requires MFA authentication.",
				Computed:            true,
			},
		}),
	}
}
