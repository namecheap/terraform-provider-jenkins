package jenkins

import (
	"context"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type credentialSSHResourceModel struct {
	ID                  types.String `tfsdk:"id"`
	Name                types.String `tfsdk:"name"`
	Folder              types.String `tfsdk:"folder"`
	Description         types.String `tfsdk:"description"`
	Domain              types.String `tfsdk:"domain"`
	Scope               types.String `tfsdk:"scope"`
	Username            types.String `tfsdk:"username"`
	PrivateKey          types.String `tfsdk:"privatekey"`
	PrivateKeyWo        types.String `tfsdk:"privatekey_wo"`
	PrivateKeyWoVersion types.String `tfsdk:"privatekey_wo_version"`
	Passphrase          types.String `tfsdk:"passphrase"`
}

type credentialSSHResource struct {
	*credentialCRUD[credentialSSHResourceModel]
}

// Ensure the implementation satisfies the desired interfaces.
var _ resource.ResourceWithConfigure = &credentialSSHResource{}
var _ resource.ResourceWithImportState = &credentialSSHResource{}
var _ resource.ResourceWithConfigValidators = &credentialSSHResource{}

func newCredentialSSHResource() resource.Resource {
	return &credentialSSHResource{
		credentialCRUD: newCredentialCRUD(sshCredentialSpec()),
	}
}

// sshCredentialSpec supplies the type-specific mapping for the shared credential
// CRUD flow (see credential_crud.go).
func sshCredentialSpec() credentialSpec[credentialSSHResourceModel] {
	return credentialSpec[credentialSSHResourceModel]{
		identity: func(m *credentialSSHResourceModel) (string, string, string) {
			return m.Folder.ValueString(), m.Domain.ValueString(), m.Name.ValueString()
		},
		setID: func(m *credentialSSHResourceModel, id string) {
			m.ID = types.StringValue(id)
		},
		secretFields: []credentialSecretField[credentialSSHResourceModel]{
			{
				// The private-key source is structural and must be present in every
				// create/update payload, so it is always sent (its value may come
				// from the write-only companion).
				name:         "privatekey",
				hasWriteOnly: true,
				alwaysSend:   true,
				plainValue:   func(m *credentialSSHResourceModel) types.String { return m.PrivateKey },
				woVersion:    func(m *credentialSSHResourceModel) types.String { return m.PrivateKeyWoVersion },
			},
			{
				// The passphrase has no write-only companion; it is sent on create
				// and, on update, only when it changed.
				name:       "passphrase",
				plainValue: func(m *credentialSSHResourceModel) types.String { return m.Passphrase },
			},
		},
		build: func(m *credentialSSHResourceModel, secrets map[string]string) interface{} {
			cred := &jenkins.SSHCredentials{
				ID:          m.Name.ValueString(),
				Scope:       m.Scope.ValueString(),
				Description: m.Description.ValueString(),
				Username:    m.Username.ValueString(),
				PrivateKeySource: &jenkins.PrivateKey{
					Class: jenkins.KeySourceDirectEntryType,
					Value: secrets["privatekey"],
				},
			}
			if p, ok := secrets["passphrase"]; ok {
				cred.Passphrase = p
			}
			return cred
		},
		newAPIValue: func() interface{} { return &jenkins.SSHCredentials{} },
		fromAPI: func(api interface{}, m *credentialSSHResourceModel) {
			// NOTE: the secrets are intentionally not read back — GetSingle returns
			// placeholders. Only Create/Update send them.
			cred := api.(*jenkins.SSHCredentials)
			m.Scope = types.StringValue(cred.Scope)
			m.Description = types.StringValue(cred.Description)
			m.Username = types.StringValue(cred.Username)
		},
	}
}

// Metadata should return the full name of the resource.
func (r *credentialSSHResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_ssh"
}

// Schema should return the schema for this resource.
func (r *credentialSSHResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `
Manages a SSH credential within Jenkins. This SSH credential may then be referenced within jobs that are created.

~> The "passphrase" and "privatekey" properties may leave plain-text values in your state file. Ensure that your state file is properly secured and encrypted at rest.`,
		Attributes: r.schemaCredential(addWriteOnlySecret(map[string]schema.Attribute{
			"username": schema.StringAttribute{
				MarkdownDescription: "Username",
				Required:            true,
			},
			"privatekey": schema.StringAttribute{
				MarkdownDescription: "Private SSH key, can be given as string or read from file with 'file()' terraform function.",
				Optional:            true,
				Sensitive:           true,
			},
			"passphrase": schema.StringAttribute{
				MarkdownDescription: "Passphrase for privatekey. This has to be skipped if private key was created without passphrase.",
				Optional:            true,
				Sensitive:           true,
			},
		}, "privatekey", "Private SSH key, can be given as string or read from file with 'file()' terraform function.")),
	}
}

// ConfigValidators enforces the plain/write-only secret constraints.
func (r *credentialSSHResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return writeOnlySecretConfigValidators("privatekey")
}
