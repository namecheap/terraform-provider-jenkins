package jenkins

import (
	"context"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type credentialUsernameResourceModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Folder            types.String `tfsdk:"folder"`
	Description       types.String `tfsdk:"description"`
	Domain            types.String `tfsdk:"domain"`
	Scope             types.String `tfsdk:"scope"`
	Username          types.String `tfsdk:"username"`
	Password          types.String `tfsdk:"password"`
	PasswordWo        types.String `tfsdk:"password_wo"`
	PasswordWoVersion types.String `tfsdk:"password_wo_version"`
}

type credentialUsernameResource struct {
	*credentialCRUD[credentialUsernameResourceModel]
}

// Ensure the implementation satisfies the desired interfaces.
var _ resource.ResourceWithConfigure = &credentialUsernameResource{}
var _ resource.ResourceWithImportState = &credentialUsernameResource{}
var _ resource.ResourceWithConfigValidators = &credentialUsernameResource{}

func newCredentialUsernameResource() resource.Resource {
	return &credentialUsernameResource{
		credentialCRUD: newCredentialCRUD(usernameCredentialSpec()),
	}
}

// usernameCredentialSpec supplies the type-specific mapping for the shared
// credential CRUD flow (see credential_crud.go).
func usernameCredentialSpec() credentialSpec[credentialUsernameResourceModel] {
	return credentialSpec[credentialUsernameResourceModel]{
		identity: func(m *credentialUsernameResourceModel) (string, string, string) {
			return m.Folder.ValueString(), m.Domain.ValueString(), m.Name.ValueString()
		},
		setID: func(m *credentialUsernameResourceModel, id string) {
			m.ID = types.StringValue(id)
		},
		secretFields: []credentialSecretField[credentialUsernameResourceModel]{{
			name:         "password",
			hasWriteOnly: true,
			plainValue:   func(m *credentialUsernameResourceModel) types.String { return m.Password },
			woVersion:    func(m *credentialUsernameResourceModel) types.String { return m.PasswordWoVersion },
		}},
		build: func(m *credentialUsernameResourceModel, secrets map[string]string) interface{} {
			cred := &jenkins.UsernameCredentials{
				ID:          m.Name.ValueString(),
				Scope:       m.Scope.ValueString(),
				Description: m.Description.ValueString(),
				Username:    m.Username.ValueString(),
			}
			if p, ok := secrets["password"]; ok {
				cred.Password = p
			}
			return cred
		},
		newAPIValue: func() interface{} { return &jenkins.UsernameCredentials{} },
		fromAPI: func(api interface{}, m *credentialUsernameResourceModel) {
			// NOTE: the password is intentionally not read back — GetSingle returns
			// a placeholder. Only Create/Update send it.
			cred := api.(*jenkins.UsernameCredentials)
			m.Scope = types.StringValue(cred.Scope)
			m.Description = types.StringValue(cred.Description)
			m.Username = types.StringValue(cred.Username)
		},
	}
}

// Metadata should return the full name of the resource.
func (r *credentialUsernameResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_username"
}

// Schema should return the schema for this resource.
func (r *credentialUsernameResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `
Manages a username credential within Jenkins. This username may then be referenced within jobs that are created.

~> The "password" property may leave plain-text passwords in your state file. If using the property to manage the password in Terraform, ensure that your state file is properly secured and encrypted at rest.`,
		Attributes: r.schemaCredential(addWriteOnlySecret(map[string]schema.Attribute{
			"username": schema.StringAttribute{
				MarkdownDescription: "The username to be associated with the credentials.",
				Required:            true,
			},
			"password": schema.StringAttribute{
				MarkdownDescription: "The password to be associated with the credentials. If empty then the password property will become unmanaged and expected to be set manually within Jenkins. If set then the password will be updated only upon changes -- if the password is set manually within Jenkins then it will not reconcile this drift until the next time the password property is changed.",
				Optional:            true,
				Sensitive:           true,
			},
		}, "password", "The password to be associated with the credentials.")),
	}
}

// ConfigValidators enforces the plain/write-only secret constraints.
func (r *credentialUsernameResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return optionalWriteOnlySecretConfigValidators("password")
}
