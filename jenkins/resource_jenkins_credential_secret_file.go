package jenkins

import (
	"context"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type credentialSecretFileResourceModel struct {
	ID                   types.String `tfsdk:"id"`
	Name                 types.String `tfsdk:"name"`
	Folder               types.String `tfsdk:"folder"`
	Description          types.String `tfsdk:"description"`
	Domain               types.String `tfsdk:"domain"`
	Scope                types.String `tfsdk:"scope"`
	Filename             types.String `tfsdk:"filename"`
	SecretBytes          types.String `tfsdk:"secretbytes"`
	SecretBytesWo        types.String `tfsdk:"secretbytes_wo"`
	SecretBytesWoVersion types.String `tfsdk:"secretbytes_wo_version"`
}

type credentialSecretFileResource struct {
	*credentialCRUD[credentialSecretFileResourceModel]
}

// Ensure the implementation satisfies the desired interfaces.
var _ resource.ResourceWithConfigure = &credentialSecretFileResource{}
var _ resource.ResourceWithImportState = &credentialSecretFileResource{}
var _ resource.ResourceWithConfigValidators = &credentialSecretFileResource{}

func newCredentialSecretFileResource() resource.Resource {
	return &credentialSecretFileResource{
		credentialCRUD: newCredentialCRUD(secretFileCredentialSpec()),
	}
}

// secretFileCredentialSpec supplies the type-specific mapping for the shared
// credential CRUD flow (see credential_crud.go).
func secretFileCredentialSpec() credentialSpec[credentialSecretFileResourceModel] {
	return credentialSpec[credentialSecretFileResourceModel]{
		identity: func(m *credentialSecretFileResourceModel) (string, string, string) {
			return m.Folder.ValueString(), m.Domain.ValueString(), m.Name.ValueString()
		},
		setID: func(m *credentialSecretFileResourceModel, id string) {
			m.ID = types.StringValue(id)
		},
		secretFields: []credentialSecretField[credentialSecretFileResourceModel]{{
			name:         "secretbytes",
			hasWriteOnly: true,
			plainValue:   func(m *credentialSecretFileResourceModel) types.String { return m.SecretBytes },
			woVersion:    func(m *credentialSecretFileResourceModel) types.String { return m.SecretBytesWoVersion },
		}},
		build: func(m *credentialSecretFileResourceModel, secrets map[string]string) interface{} {
			cred := &jenkins.FileCredentials{
				ID:          m.Name.ValueString(),
				Scope:       m.Scope.ValueString(),
				Description: m.Description.ValueString(),
				Filename:    m.Filename.ValueString(),
			}
			if s, ok := secrets["secretbytes"]; ok {
				cred.SecretBytes = s
			}
			return cred
		},
		newAPIValue: func() interface{} { return &jenkins.FileCredentials{} },
		fromAPI: func(api interface{}, m *credentialSecretFileResourceModel) {
			// NOTE: the secret bytes are intentionally not read back — GetSingle
			// returns a placeholder. Only Create/Update send them.
			cred := api.(*jenkins.FileCredentials)
			m.Scope = types.StringValue(cred.Scope)
			m.Description = types.StringValue(cred.Description)
			m.Filename = types.StringValue(cred.Filename)
		},
	}
}

// Metadata should return the full name of the resource.
func (r *credentialSecretFileResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_secret_file"
}

// Schema should return the schema for this resource.
func (r *credentialSecretFileResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `
Manages a secret file credential within Jenkins. This secret file may then be referenced within jobs that are created.`,
		Attributes: r.schemaCredential(addWriteOnlySecret(map[string]schema.Attribute{
			"filename": schema.StringAttribute{
				MarkdownDescription: "The secret file filename on jenkins server side.",
				Required:            true,
			},
			"secretbytes": schema.StringAttribute{
				MarkdownDescription: "The secret file, base64 encoded content. It can be sourced directly from local file with filebase64(path) TF function or given directly.",
				Optional:            true,
				Sensitive:           true,
			},
		}, "secretbytes", "The secret file, base64 encoded content.")),
	}
}

// ConfigValidators enforces the plain/write-only secret constraints.
func (r *credentialSecretFileResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return writeOnlySecretConfigValidators("secretbytes")
}
