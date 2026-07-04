package jenkins

import (
	"context"
	"encoding/xml"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// GitHubAppCredentials represents a Jenkins GitHub App credential.
// It requires the GitHub Branch Source plugin.
// Jenkins/XStream encodes underscores in package names as double underscores, hence github__branch__source.
type GitHubAppCredentials struct {
	XMLName     xml.Name `xml:"org.jenkinsci.plugins.github__branch__source.GitHubAppCredentials"`
	ID          string   `xml:"id"`
	Scope       string   `xml:"scope"`
	Description string   `xml:"description"`
	AppID       string   `xml:"appID"`
	PrivateKey  string   `xml:"privateKey"`
}

type credentialGitHubAppResourceModel struct {
	ID                  types.String `tfsdk:"id"`
	Name                types.String `tfsdk:"name"`
	Folder              types.String `tfsdk:"folder"`
	Description         types.String `tfsdk:"description"`
	Domain              types.String `tfsdk:"domain"`
	Scope               types.String `tfsdk:"scope"`
	AppID               types.String `tfsdk:"app_id"`
	PrivateKey          types.String `tfsdk:"private_key"`
	PrivateKeyWo        types.String `tfsdk:"private_key_wo"`
	PrivateKeyWoVersion types.String `tfsdk:"private_key_wo_version"`
}

type credentialGitHubAppResource struct {
	*credentialCRUD[credentialGitHubAppResourceModel]
}

// Ensure the implementation satisfies the desired interfaces.
var _ resource.ResourceWithConfigure = &credentialGitHubAppResource{}
var _ resource.ResourceWithImportState = &credentialGitHubAppResource{}
var _ resource.ResourceWithConfigValidators = &credentialGitHubAppResource{}

func newCredentialGitHubAppResource() resource.Resource {
	return &credentialGitHubAppResource{
		credentialCRUD: newCredentialCRUD(gitHubAppCredentialSpec()),
	}
}

// gitHubAppCredentialSpec supplies the type-specific mapping for the shared
// credential CRUD flow (see credential_crud.go). Uses the local
// GitHubAppCredentials XML struct (the GitHub Branch Source plugin type).
func gitHubAppCredentialSpec() credentialSpec[credentialGitHubAppResourceModel] {
	return credentialSpec[credentialGitHubAppResourceModel]{
		identity: func(m *credentialGitHubAppResourceModel) (string, string, string) {
			return m.Folder.ValueString(), m.Domain.ValueString(), m.Name.ValueString()
		},
		setID: func(m *credentialGitHubAppResourceModel, id string) {
			m.ID = types.StringValue(id)
		},
		secretFields: []credentialSecretField[credentialGitHubAppResourceModel]{{
			name:         "private_key",
			hasWriteOnly: true,
			plainValue:   func(m *credentialGitHubAppResourceModel) types.String { return m.PrivateKey },
			woVersion:    func(m *credentialGitHubAppResourceModel) types.String { return m.PrivateKeyWoVersion },
		}},
		build: func(m *credentialGitHubAppResourceModel, secrets map[string]string) interface{} {
			cred := &GitHubAppCredentials{
				ID:          m.Name.ValueString(),
				Scope:       m.Scope.ValueString(),
				Description: m.Description.ValueString(),
				AppID:       m.AppID.ValueString(),
			}
			if k, ok := secrets["private_key"]; ok {
				cred.PrivateKey = k
			}
			return cred
		},
		newAPIValue: func() interface{} { return &GitHubAppCredentials{} },
		fromAPI: func(api interface{}, m *credentialGitHubAppResourceModel) {
			// NOTE: the private key is intentionally not read back — GetSingle returns
			// a placeholder. Only Create/Update send it.
			cred := api.(*GitHubAppCredentials)
			m.Scope = types.StringValue(cred.Scope)
			m.Description = types.StringValue(cred.Description)
			m.AppID = types.StringValue(cred.AppID)
		},
	}
}

// Metadata should return the full name of the resource.
func (r *credentialGitHubAppResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_github_app"
}

// Schema should return the schema for this resource.
func (r *credentialGitHubAppResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `
Manages a GitHub App credential within Jenkins. This credential may then be referenced within jobs that are created.

~> The "private_key" property may leave a plain-text private key in your state file. If using the property to manage the private key in Terraform, ensure that your state file is properly secured and encrypted at rest.

~> The Jenkins installation that uses this resource is expected to have the [GitHub Branch Source Plugin](https://plugins.jenkins.io/github-branch-source/) installed.`,
		Attributes: r.schemaCredential(addWriteOnlySecret(map[string]schema.Attribute{
			"app_id": schema.StringAttribute{
				MarkdownDescription: "The numeric GitHub App ID.",
				Required:            true,
			},
			"private_key": schema.StringAttribute{
				MarkdownDescription: "The RSA private key in PKCS#1 PEM format for the GitHub App.",
				Optional:            true,
				Sensitive:           true,
			},
		}, "private_key", "The RSA private key in PKCS#1 PEM format for the GitHub App.")),
	}
}

// ConfigValidators enforces the plain/write-only secret constraints.
func (r *credentialGitHubAppResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return writeOnlySecretConfigValidators("private_key")
}
