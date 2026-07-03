package jenkins

import (
	"context"
	"encoding/xml"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// uploadedKeyStoreSourceClass is the XStream class for a certificate credential
// whose keystore bytes are uploaded inline (as opposed to referenced from a file
// on the controller).
const uploadedKeyStoreSourceClass = "com.cloudbees.plugins.credentials.impl.CertificateCredentialsImpl$UploadedKeyStoreSource"

type certificateKeyStoreSource struct {
	Class            string `xml:"class,attr"`
	UploadedKeystore string `xml:"uploadedKeystore"`
}

// CertificateCredentials represents a Jenkins certificate credential backed by an
// uploaded PKCS#12 keystore.
type CertificateCredentials struct {
	XMLName        xml.Name                  `xml:"com.cloudbees.plugins.credentials.impl.CertificateCredentialsImpl"`
	ID             string                    `xml:"id"`
	Scope          string                    `xml:"scope"`
	Description    string                    `xml:"description"`
	Password       string                    `xml:"password"`
	KeyStoreSource certificateKeyStoreSource `xml:"keyStoreSource"`
}

type credentialCertificateResourceModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Folder            types.String `tfsdk:"folder"`
	Description       types.String `tfsdk:"description"`
	Domain            types.String `tfsdk:"domain"`
	Scope             types.String `tfsdk:"scope"`
	Keystore          types.String `tfsdk:"keystore"`
	KeystoreWo        types.String `tfsdk:"keystore_wo"`
	KeystoreWoVersion types.String `tfsdk:"keystore_wo_version"`
	Password          types.String `tfsdk:"password"`
}

type credentialCertificateResource struct {
	*resourceHelper
}

var _ resource.ResourceWithConfigure = &credentialCertificateResource{}
var _ resource.ResourceWithImportState = &credentialCertificateResource{}
var _ resource.ResourceWithConfigValidators = &credentialCertificateResource{}

func newCredentialCertificateResource() resource.Resource {
	return &credentialCertificateResource{
		resourceHelper: newResourceHelper(),
	}
}

func (r *credentialCertificateResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_certificate"
}

func (r *credentialCertificateResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `
Manages a certificate credential within Jenkins, backed by an uploaded PKCS#12 keystore. This credential may then be referenced within jobs that are created.`,
		Attributes: r.schemaCredential(addWriteOnlySecret(map[string]schema.Attribute{
			"keystore": schema.StringAttribute{
				MarkdownDescription: "The base64-encoded contents of a PKCS#12 keystore holding the certificate and its private key. Load one from disk with the `filebase64(...)` Terraform function.",
				Optional:            true,
				Sensitive:           true,
			},
			"password": schema.StringAttribute{
				MarkdownDescription: "The password protecting the keystore. Omit for a keystore with no password.",
				Optional:            true,
				Sensitive:           true,
			},
		}, "keystore", "The base64-encoded contents of a PKCS#12 keystore holding the certificate and its private key.")),
	}
}

// ConfigValidators enforces the plain/write-only keystore constraints.
func (r *credentialCertificateResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return writeOnlySecretConfigValidators("keystore")
}

func (r *credentialCertificateResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	tflog.Debug(ctx, "credentialCertificateResource.Create")
	var data credentialCertificateResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cm := r.credentialManagerForFolder(ctx, data.Folder.ValueString(), &resp.Diagnostics)
	if cm == nil {
		return
	}

	keystore := data.Keystore.ValueString()
	if keystoreWo := r.readWriteOnly(ctx, req.Config, "keystore_wo", &resp.Diagnostics); !keystoreWo.IsNull() {
		keystore = keystoreWo.ValueString()
	}
	if resp.Diagnostics.HasError() {
		return
	}

	cred := CertificateCredentials{
		ID:          data.Name.ValueString(),
		Scope:       data.Scope.ValueString(),
		Description: data.Description.ValueString(),
		Password:    data.Password.ValueString(),
		KeyStoreSource: certificateKeyStoreSource{
			Class:            uploadedKeyStoreSourceClass,
			UploadedKeystore: keystore,
		},
	}

	if err := cm.Add(ctx, data.Domain.ValueString(), cred); err != nil {
		resp.Diagnostics.AddError(
			"Unable to Create Resource",
			"An unexpected error occurred while creating the resource. "+
				"Please report this issue to the provider developers.\n\n"+
				"Error: "+err.Error(),
		)
		return
	}

	data.ID = types.StringValue(generateCredentialID(data.Folder.ValueString(), cred.ID))

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *credentialCertificateResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	tflog.Debug(ctx, "credentialCertificateResource.Read")
	var data credentialCertificateResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cm := r.credentialManager(data.Folder.ValueString())

	cred := CertificateCredentials{}
	err := cm.GetSingle(ctx, data.Domain.ValueString(), data.Name.ValueString(), &cred)
	if err != nil {
		if isNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Unable to Refresh Resource",
			"An unexpected error occurred while parsing the resource read response. "+
				"Please report this issue to the provider developers.\n\n"+
				"Error: "+err.Error(),
		)
		return
	}

	data.ID = types.StringValue(generateCredentialID(data.Folder.ValueString(), cred.ID))
	data.Scope = types.StringValue(cred.Scope)
	data.Description = types.StringValue(cred.Description)

	// NOTE: keystore and password are not refreshed here; Jenkins does not return
	// usable secret material, so they are only sent on Create/Update.

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *credentialCertificateResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	tflog.Debug(ctx, "credentialCertificateResource.Update")
	var data credentialCertificateResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cm := r.credentialManager(data.Folder.ValueString())

	// The keystore bytes must always accompany a certificate update (omitting the
	// keyStoreSource would clear it). The value is available on every apply: from
	// the plan for the plain attribute, or from config for the write-only one.
	keystore := data.Keystore.ValueString()
	if keystoreWo := r.readWriteOnly(ctx, req.Config, "keystore_wo", &resp.Diagnostics); !keystoreWo.IsNull() {
		keystore = keystoreWo.ValueString()
	}
	if resp.Diagnostics.HasError() {
		return
	}

	cred := CertificateCredentials{
		ID:          data.Name.ValueString(),
		Scope:       data.Scope.ValueString(),
		Description: data.Description.ValueString(),
		Password:    data.Password.ValueString(),
		KeyStoreSource: certificateKeyStoreSource{
			Class:            uploadedKeyStoreSourceClass,
			UploadedKeystore: keystore,
		},
	}

	if err := cm.Update(ctx, data.Domain.ValueString(), data.Name.ValueString(), &cred); err != nil {
		resp.Diagnostics.AddError(
			"Unable to Update Resource",
			"An unexpected error occurred while attempting to update the resource. "+
				"Please retry the operation or report this issue to the provider developers.\n\n"+
				"Error: "+err.Error(),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *credentialCertificateResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data credentialCertificateResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	r.deleteCredential(ctx, data.Folder.ValueString(), data.Domain.ValueString(), data.Name.ValueString(), &resp.Diagnostics)
}
