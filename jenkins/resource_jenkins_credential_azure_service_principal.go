package jenkins

import (
	"context"
	"encoding/xml"

	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// AzureServicePrincipalCredentials struct representing credential for storing Azure service credentials
type AzureServicePrincipalCredentials struct {
	XMLName     xml.Name                             `xml:"com.microsoft.azure.util.AzureCredentials"`
	ID          string                               `xml:"id"`
	Scope       string                               `xml:"scope"`
	Description string                               `xml:"description"`
	Data        AzureServicePrincipalCredentialsData `xml:"data"`
}

type AzureServicePrincipalCredentialsData struct {
	SubscriptionId          string `xml:"subscriptionId"`
	ClientId                string `xml:"clientId"`
	ClientSecret            string `xml:"clientSecret"`
	CertificateId           string `xml:"certificateId"`
	Tenant                  string `xml:"tenant"`
	AzureEnvironmentName    string `xml:"azureEnvironmentName"`
	ServiceManagementURL    string `xml:"serviceManagementURL"`
	AuthenticationEndpoint  string `xml:"authenticationEndpoint"`
	ResourceManagerEndpoint string `xml:"resourceManagerEndpoint"`
	GraphEndpoint           string `xml:"graphEndpoint"`
}

type credentialAzureServicePrincipalResourceModel struct {
	ID                      types.String `tfsdk:"id"`
	Name                    types.String `tfsdk:"name"`
	Folder                  types.String `tfsdk:"folder"`
	Description             types.String `tfsdk:"description"`
	Domain                  types.String `tfsdk:"domain"`
	Scope                   types.String `tfsdk:"scope"`
	SubscriptionId          types.String `tfsdk:"subscription_id"`
	ClientId                types.String `tfsdk:"client_id"`
	ClientSecret            types.String `tfsdk:"client_secret"`
	ClientSecretWo          types.String `tfsdk:"client_secret_wo"`
	ClientSecretWoVersion   types.String `tfsdk:"client_secret_wo_version"`
	CertificateId           types.String `tfsdk:"certificate_id"`
	Tenant                  types.String `tfsdk:"tenant"`
	AzureEnvironmentName    types.String `tfsdk:"azure_environment_name"`
	ServiceManagementURL    types.String `tfsdk:"service_management_url"`
	AuthenticationEndpoint  types.String `tfsdk:"authentication_endpoint"`
	ResourceManagerEndpoint types.String `tfsdk:"resource_manager_endpoint"`
	GraphEndpoint           types.String `tfsdk:"graph_endpoint"`
}

type credentialAzureServicePrincipalResource struct {
	*credentialCRUD[credentialAzureServicePrincipalResourceModel]
}

// Ensure the implementation satisfies the desired interfaces.
var _ resource.ResourceWithConfigure = &credentialAzureServicePrincipalResource{}
var _ resource.ResourceWithImportState = &credentialAzureServicePrincipalResource{}
var _ resource.ResourceWithConfigValidators = &credentialAzureServicePrincipalResource{}

func newCredentialAzureServicePrincipalResource() resource.Resource {
	return &credentialAzureServicePrincipalResource{
		credentialCRUD: newCredentialCRUD(azureServicePrincipalCredentialSpec()),
	}
}

// azureServicePrincipalCredentialSpec supplies the type-specific mapping for the
// shared credential CRUD flow (see credential_crud.go). Two mutually-exclusive
// authentication secrets are possible (enforced by ConfigValidators):
//
//   - client_secret — a true secret with a write-only companion; sent on create
//     and, on update, only when it changed (plain) or its version trigger bumped
//     (write-only). Modeled as the single secretField.
//   - certificate_id — a reference to a Jenkins certificate credential. It is not
//     write-only and is always set from the model. Setting it unconditionally in
//     build reproduces the fixed behavior of re-sending it whenever the
//     credential is certificate-based (an empty value == not certificate-based),
//     which the #95 fix introduced to stop description-only edits from wiping the
//     reference; it is written to CertificateId (not ClientId — the #95 bug).
//
// Read only refreshes id/scope/description (Azure does not read its data fields
// back), so fromAPI sets scope+description only.
func azureServicePrincipalCredentialSpec() credentialSpec[credentialAzureServicePrincipalResourceModel] {
	return credentialSpec[credentialAzureServicePrincipalResourceModel]{
		identity: func(m *credentialAzureServicePrincipalResourceModel) (string, string, string) {
			return m.Folder.ValueString(), m.Domain.ValueString(), m.Name.ValueString()
		},
		setID: func(m *credentialAzureServicePrincipalResourceModel, id string) {
			m.ID = types.StringValue(id)
		},
		secretFields: []credentialSecretField[credentialAzureServicePrincipalResourceModel]{{
			name:         "client_secret",
			hasWriteOnly: true,
			plainValue:   func(m *credentialAzureServicePrincipalResourceModel) types.String { return m.ClientSecret },
			woVersion:    func(m *credentialAzureServicePrincipalResourceModel) types.String { return m.ClientSecretWoVersion },
		}},
		build: func(m *credentialAzureServicePrincipalResourceModel, secrets map[string]string) interface{} {
			cred := &AzureServicePrincipalCredentials{
				ID:          m.Name.ValueString(),
				Scope:       m.Scope.ValueString(),
				Description: m.Description.ValueString(),
				Data: AzureServicePrincipalCredentialsData{
					SubscriptionId:          m.SubscriptionId.ValueString(),
					ClientId:                m.ClientId.ValueString(),
					CertificateId:           m.CertificateId.ValueString(),
					Tenant:                  m.Tenant.ValueString(),
					AzureEnvironmentName:    m.AzureEnvironmentName.ValueString(),
					ServiceManagementURL:    m.ServiceManagementURL.ValueString(),
					AuthenticationEndpoint:  m.AuthenticationEndpoint.ValueString(),
					ResourceManagerEndpoint: m.ResourceManagerEndpoint.ValueString(),
					GraphEndpoint:           m.GraphEndpoint.ValueString(),
				},
			}
			if s, ok := secrets["client_secret"]; ok {
				cred.Data.ClientSecret = s
			}
			return cred
		},
		newAPIValue: func() interface{} { return &AzureServicePrincipalCredentials{} },
		fromAPI: func(api interface{}, m *credentialAzureServicePrincipalResourceModel) {
			// Azure does not read its data fields back (GetSingle returns
			// placeholders); only id/scope/description are refreshed.
			cred := api.(*AzureServicePrincipalCredentials)
			m.Scope = types.StringValue(cred.Scope)
			m.Description = types.StringValue(cred.Description)
		},
	}
}

// Metadata should return the full name of the resource.
func (r *credentialAzureServicePrincipalResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_credential_azure_service_principal"
}

// Schema should return the schema for this resource.
func (r *credentialAzureServicePrincipalResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `
Manages an Azure Service Principal credential within Jenkins. This credential may then be referenced within jobs that are created.

~> The "client_secret" property may leave plain-text secret id in your state file. If using the property to manage the secret id in Terraform, ensure that your state file is properly secured and encrypted at rest.

~> The Jenkins installation that uses this resource is expected to have the [Azure Credentials Plugin](https://plugins.jenkins.io/azure-credentials/) installed in their system.`,
		Attributes: r.schemaCredential(addWriteOnlySecret(map[string]schema.Attribute{
			"subscription_id": schema.StringAttribute{
				MarkdownDescription: "The Azure subscription id mapped to the Azure Service Principal.",
				Required:            true,
			},
			"client_id": schema.StringAttribute{
				MarkdownDescription: "The client id (application id) of the Azure Service Principal.",
				Required:            true,
			},
			"client_secret": schema.StringAttribute{
				MarkdownDescription: "The client secret of the Azure Service Principal. Cannot be used with `certificate_id` or `client_secret_wo`. Has to be specified, if neither `certificate_id` nor `client_secret_wo` is specified.",
				Sensitive:           true,
				Optional:            true,
			},
			"certificate_id": schema.StringAttribute{
				MarkdownDescription: "The certificate reference of the Azure Service Principal, pointing to a Jenkins certificate credential. Cannot be used with `client_secret` or `client_secret_wo`. Has to be specified, if neither `client_secret` nor `client_secret_wo` is specified.",
				Sensitive:           true,
				Optional:            true,
			},
			"tenant": schema.StringAttribute{
				MarkdownDescription: "The Azure Tenant ID of the Azure Service Principal.",
				Required:            true,
			},
			"azure_environment_name": schema.StringAttribute{
				MarkdownDescription: `The Azure Cloud enviroment name. Allowed values are "Azure", "Azure China", "Azure Germany", "Azure US Government".`,
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("Azure"),
				Validators: []validator.String{
					stringvalidator.OneOf("Azure", "Azure China", "Azure Germany", "Azure US Government"),
				},
			},
			"service_management_url": schema.StringAttribute{
				MarkdownDescription: "Override the Azure management endpoint URL for the selected Azure environment.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
			"authentication_endpoint": schema.StringAttribute{
				MarkdownDescription: "Override the Azure Active Directory endpoint for the selected Azure environment.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
			"resource_manager_endpoint": schema.StringAttribute{
				MarkdownDescription: "Override the Azure resource manager endpoint URL for the selected Azure environment.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
			"graph_endpoint": schema.StringAttribute{
				MarkdownDescription: "Override the Azure graph endpoint URL for the selected Azure environment.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
		}, "client_secret", "The client secret of the Azure Service Principal.")),
	}
}

// ConfigValidators enforces that exactly one of client_secret, client_secret_wo,
// or certificate_id is set, and that the write-only secret is paired with its
// version trigger.
func (r *credentialAzureServicePrincipalResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.ExactlyOneOf(
			path.MatchRoot("client_secret"),
			path.MatchRoot("client_secret_wo"),
			path.MatchRoot("certificate_id"),
		),
		resourcevalidator.RequiredTogether(
			path.MatchRoot("client_secret_wo"),
			path.MatchRoot("client_secret_wo_version"),
		),
	}
}

// buildAzureServicePrincipalUpdate constructs the plain-secret update payload the
// same way the shared CRUD flow does (resolveSecretsUpdate's plain case, then
// build): client_secret is included only when it changed, and certificate_id is
// always taken from the model (an empty value means not certificate-based) and
// written to CertificateId — never ClientId (the #95 bug). It delegates to the
// spec's build so the #95 regression test (TestBuildAzureServicePrincipalUpdate)
// exercises the real production mapping.
func buildAzureServicePrincipalUpdate(data, state credentialAzureServicePrincipalResourceModel) AzureServicePrincipalCredentials {
	secrets := map[string]string{}
	if !data.ClientSecret.Equal(state.ClientSecret) {
		secrets["client_secret"] = data.ClientSecret.ValueString()
	}
	return *azureServicePrincipalCredentialSpec().build(&data, secrets).(*AzureServicePrincipalCredentials)
}
