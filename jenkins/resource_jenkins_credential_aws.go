package jenkins

import (
	"context"
	"encoding/xml"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type credentialAws struct {
	XMLName            xml.Name `xml:"com.cloudbees.jenkins.plugins.awscredentials.AWSCredentialsImpl"`
	ID                 string   `xml:"id"`
	Scope              string   `xml:"scope"`
	Description        string   `xml:"description"`
	AccessKey          string   `xml:"accessKey"`
	SecretKey          string   `xml:"secretKey"`
	IamRoleArn         string   `xml:"iamRoleArn"`
	IamMfaSerialNumber string   `xml:"iamMfaSerialNumber"`
}

type credentialAwsResourceModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	Folder             types.String `tfsdk:"folder"`
	Description        types.String `tfsdk:"description"`
	Domain             types.String `tfsdk:"domain"`
	Scope              types.String `tfsdk:"scope"`
	AccessKey          types.String `tfsdk:"access_key"`
	SecretKey          types.String `tfsdk:"secret_key"`
	SecretKeyWo        types.String `tfsdk:"secret_key_wo"`
	SecretKeyWoVersion types.String `tfsdk:"secret_key_wo_version"`
	IamRoleArn         types.String `tfsdk:"iam_role_arn"`
	IamMfaSerialNumber types.String `tfsdk:"iam_mfa_serial_number"`
}

type credentialAwsResource struct {
	*credentialCRUD[credentialAwsResourceModel]
}

// Ensure the implementation satisfies the desired interfaces.
var _ resource.ResourceWithConfigure = &credentialAwsResource{}
var _ resource.ResourceWithImportState = &credentialAwsResource{}
var _ resource.ResourceWithConfigValidators = &credentialAwsResource{}

func newCredentialAwsResource() resource.Resource {
	return &credentialAwsResource{
		credentialCRUD: newCredentialCRUD(awsCredentialSpec()),
	}
}

// awsCredentialSpec supplies the type-specific mapping for the shared credential
// CRUD flow (see credential_crud.go). Uses the local credentialAws XML struct
// (the AWS Credentials plugin type) rather than a gojenkins type.
func awsCredentialSpec() credentialSpec[credentialAwsResourceModel] {
	return credentialSpec[credentialAwsResourceModel]{
		identity: func(m *credentialAwsResourceModel) (string, string, string) {
			return m.Folder.ValueString(), m.Domain.ValueString(), m.Name.ValueString()
		},
		setID: func(m *credentialAwsResourceModel, id string) {
			m.ID = types.StringValue(id)
		},
		secretFields: []credentialSecretField[credentialAwsResourceModel]{{
			name:         "secret_key",
			hasWriteOnly: true,
			plainValue:   func(m *credentialAwsResourceModel) types.String { return m.SecretKey },
			woVersion:    func(m *credentialAwsResourceModel) types.String { return m.SecretKeyWoVersion },
		}},
		build: func(m *credentialAwsResourceModel, secrets map[string]string) interface{} {
			cred := &credentialAws{
				ID:                 m.Name.ValueString(),
				Scope:              m.Scope.ValueString(),
				Description:        m.Description.ValueString(),
				AccessKey:          m.AccessKey.ValueString(),
				IamRoleArn:         m.IamRoleArn.ValueString(),
				IamMfaSerialNumber: m.IamMfaSerialNumber.ValueString(),
			}
			if s, ok := secrets["secret_key"]; ok {
				cred.SecretKey = s
			}
			return cred
		},
		newAPIValue: func() interface{} { return &credentialAws{} },
		fromAPI: func(api interface{}, m *credentialAwsResourceModel) {
			// NOTE: the secret key is intentionally not read back — GetSingle returns
			// a placeholder. Only Create/Update send it.
			cred := api.(*credentialAws)
			m.Scope = types.StringValue(cred.Scope)
			m.Description = types.StringValue(cred.Description)
			m.AccessKey = types.StringValue(cred.AccessKey)
			m.IamRoleArn = types.StringValue(cred.IamRoleArn)
			m.IamMfaSerialNumber = types.StringValue(cred.IamMfaSerialNumber)
		},
	}
}

// Metadata should return the full name of the resource.
func (r *credentialAwsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_aws"
}

// Schema should return the schema for this resource.
func (r *credentialAwsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `
Manages an AWS credential within Jenkins.

~> The "secret_key" property may leave plain-text secret id in your state file. If using the property to manage the secret id in Terraform, ensure that your state file is properly secured and encrypted at rest.

~> The Jenkins installation that uses this resource is expected to have the [AWS Credentials Plugin](https://plugins.jenkins.io/aws-credentials/) installed in their system.`,
		Attributes: r.schemaCredential(addWriteOnlySecret(map[string]schema.Attribute{
			"access_key": schema.StringAttribute{
				MarkdownDescription: "An AWS access key ID. This is the public part of the key pair used to authenticate with AWS services.",
				Optional:            true,
				Sensitive:           true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
			"secret_key": schema.StringAttribute{
				MarkdownDescription: "An AWS secret access key. This is the private part of the key pair used to authenticate with AWS services.",
				Optional:            true,
				Sensitive:           true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
			"iam_role_arn": schema.StringAttribute{
				MarkdownDescription: "An ARN specifying the IAM role to assume. The format should be something like: \"arn:aws:iam::123456789012:role/MyIAMRoleName\".",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
			"iam_mfa_serial_number": schema.StringAttribute{
				MarkdownDescription: "The identifier for an MFA device. Either a serial number for hardware MFA devices, or an ARN for virtual devices.\n This is only required if the trust policy of the role being assumed includes a condition that requires MFA authentication.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
		}, "secret_key", "An AWS secret access key. This is the private part of the key pair used to authenticate with AWS services.")),
	}
}

// ConfigValidators enforces the plain/write-only secret constraints.
func (r *credentialAwsResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return optionalWriteOnlySecretConfigValidators("secret_key")
}
