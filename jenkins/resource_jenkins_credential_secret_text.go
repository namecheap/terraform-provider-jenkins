package jenkins

import (
	"context"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type credentialSecretTextResourceModel struct {
	ID              types.String `tfsdk:"id"`
	Name            types.String `tfsdk:"name"`
	Folder          types.String `tfsdk:"folder"`
	Description     types.String `tfsdk:"description"`
	Domain          types.String `tfsdk:"domain"`
	Scope           types.String `tfsdk:"scope"`
	Secret          types.String `tfsdk:"secret"`
	SecretWo        types.String `tfsdk:"secret_wo"`
	SecretWoVersion types.String `tfsdk:"secret_wo_version"`
}

type credentialSecretTextResource struct {
	*credentialCRUD[credentialSecretTextResourceModel]
}

// Ensure the implementation satisfies the desired interfaces.
var _ resource.ResourceWithConfigure = &credentialSecretTextResource{}
var _ resource.ResourceWithImportState = &credentialSecretTextResource{}
var _ resource.ResourceWithConfigValidators = &credentialSecretTextResource{}

func newCredentialSecretTextResource() resource.Resource {
	return &credentialSecretTextResource{
		credentialCRUD: newCredentialCRUD(secretTextCredentialSpec()),
	}
}

// secretTextCredentialSpec supplies the type-specific mapping for the shared
// credential CRUD flow (see credential_crud.go).
func secretTextCredentialSpec() credentialSpec[credentialSecretTextResourceModel] {
	return credentialSpec[credentialSecretTextResourceModel]{
		identity: func(m *credentialSecretTextResourceModel) (string, string, string) {
			return m.Folder.ValueString(), m.Domain.ValueString(), m.Name.ValueString()
		},
		setID: func(m *credentialSecretTextResourceModel, id string) {
			m.ID = types.StringValue(id)
		},
		secretFields: []credentialSecretField[credentialSecretTextResourceModel]{{
			name:       "secret",
			plainValue: func(m *credentialSecretTextResourceModel) types.String { return m.Secret },
			woVersion:  func(m *credentialSecretTextResourceModel) types.String { return m.SecretWoVersion },
		}},
		build: func(m *credentialSecretTextResourceModel, secrets map[string]string) interface{} {
			cred := &jenkins.StringCredentials{
				ID:          m.Name.ValueString(),
				Scope:       m.Scope.ValueString(),
				Description: m.Description.ValueString(),
			}
			if s, ok := secrets["secret"]; ok {
				cred.Secret = s
			}
			return cred
		},
		newAPIValue: func() interface{} { return &jenkins.StringCredentials{} },
		fromAPI: func(api interface{}, m *credentialSecretTextResourceModel) {
			// NOTE: the secret is intentionally not read back — GetSingle returns
			// a placeholder for it. Only Create/Update send the secret.
			cred := api.(*jenkins.StringCredentials)
			m.Scope = types.StringValue(cred.Scope)
			m.Description = types.StringValue(cred.Description)
		},
	}
}

// Metadata should return the full name of the resource.
func (r *credentialSecretTextResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_secret_text"
}

// Schema should return the schema for this resource.
func (r *credentialSecretTextResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `
Manages a secret text credential within Jenkins. This secret text may then be referenced within jobs that are created.`,
		Attributes: r.schemaCredential(addWriteOnlySecret(map[string]schema.Attribute{
			"secret": schema.StringAttribute{
				MarkdownDescription: "The secret text to be associated with the credentials.",
				Optional:            true,
				Sensitive:           true,
			},
		}, "secret", "The secret text to be associated with the credentials.")),
	}
}

// ConfigValidators enforces the plain/write-only secret constraints.
func (r *credentialSecretTextResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return writeOnlySecretConfigValidators("secret")
}
