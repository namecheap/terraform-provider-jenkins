package jenkins

import (
	"context"
	"fmt"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func New() provider.Provider {
	return &JenkinsProvider{}
}

// Ensure the implementation satisfies the provider.Provider interface.
var _ provider.Provider = &JenkinsProvider{}

type JenkinsProvider struct{}

// Metadata satisfies the provider.Provider interface for JenkinsProvider
func (p *JenkinsProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "jenkins"
}

// Schema satisfies the provider.Provider interface for JenkinsProvider.
func (p *JenkinsProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"server_url": schema.StringAttribute{
				Optional:    true,
				Description: "The URL of the Jenkins server to connect to. It should be fully qualified (e.g. `https://...`) and point to the root of the Jenkins server location.",
			},
			"ca_cert": schema.StringAttribute{
				Optional:    true,
				Description: "The path to the Jenkins self-signed certificate. It may be required in order to authenticate to your Jenkins instance.",
			},
			"username": schema.StringAttribute{
				Optional:    true, // Needs to be optional to be able to run terraform validate without providing credentials
				Description: "The username to authenticate to Jenkins.",
			},
			"password": schema.StringAttribute{
				Optional:    true, // Needs to be optional to be able to run terraform validate without providing credentials
				Sensitive:   true,
				Description: "The password to authenticate to Jenkins. If you are using the GitHub OAuth authentication method, enter your Personal Access Token here.",
			},
			"insecure": schema.BoolAttribute{
				Optional:    true,
				Description: "Disables TLS certificate verification. Set to true only for non-production Jenkins instances with self-signed certificates when `ca_cert` cannot be used.",
			},
			"request_timeout": schema.StringAttribute{
				Optional:    true,
				Description: "Maximum duration for each Jenkins API operation, including retries, as a Go duration string (e.g. `30s`, `2m`). Overridable via `JENKINS_REQUEST_TIMEOUT`. Defaults to no timeout.",
			},
			"retry_max": schema.Int64Attribute{
				Optional:    true,
				Description: "Number of times to retry a failed idempotent request (GET/HEAD/OPTIONS/PUT/DELETE) on connection errors, HTTP 429, or 5xx responses. POST requests are never retried. Overridable via `JENKINS_RETRY_MAX`. Defaults to `4`; set to `0` to disable retries.",
			},
			"retry_wait_min": schema.StringAttribute{
				Optional:    true,
				Description: "Minimum wait between retries as a Go duration string (e.g. `1s`). Overridable via `JENKINS_RETRY_WAIT_MIN`. Defaults to `1s`.",
			},
			"retry_wait_max": schema.StringAttribute{
				Optional:    true,
				Description: "Maximum wait between retries as a Go duration string (e.g. `30s`). Overridable via `JENKINS_RETRY_WAIT_MAX`. Defaults to `30s`.",
			},
		},
	}
}

type JenkinsProviderModel struct {
	ServerURL      types.String `tfsdk:"server_url"`
	CACert         types.String `tfsdk:"ca_cert"`
	Username       types.String `tfsdk:"username"`
	Password       types.String `tfsdk:"password"`
	Insecure       types.Bool   `tfsdk:"insecure"`
	RequestTimeout types.String `tfsdk:"request_timeout"`
	RetryMax       types.Int64  `tfsdk:"retry_max"`
	RetryWaitMin   types.String `tfsdk:"retry_wait_min"`
	RetryWaitMax   types.String `tfsdk:"retry_wait_max"`
}

// Configure satisfies the provider.Provider interface for JenkinsProvider.
func (p *JenkinsProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data JenkinsProviderModel

	// Read configuration data into model
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)

	serverURL := os.Getenv("JENKINS_URL")
	if data.ServerURL.ValueString() != "" {
		serverURL = data.ServerURL.ValueString()
	}
	if serverURL == "" {
		resp.Diagnostics.AddError(
			"server_url is required",
			"server_url is required and must be provided in the provider config or the JENKINS_URL environment variable",
		)
		// Return before constructing the client: without a server_url, client.Init
		// below would dial the network (and fail with an opaque connection error)
		// instead of surfacing this validation diagnostic. Mirrors the SDKv2 provider.
		return
	}

	caCert := os.Getenv("JENKINS_CA_CERT")
	if data.CACert.ValueString() != "" {
		caCert = data.CACert.ValueString()
	}

	username := os.Getenv("JENKINS_USERNAME")
	if data.Username.ValueString() != "" {
		username = data.Username.ValueString()
	}

	password := os.Getenv("JENKINS_PASSWORD")
	if data.Password.ValueString() != "" {
		password = data.Password.ValueString()
	}

	userAgent := "terraform-provider-jenkins"
	if extra := os.Getenv("TF_APPEND_USER_AGENT"); extra != "" {
		userAgent = userAgent + " " + extra
	}

	requestTimeout, err := resolveDuration(data.RequestTimeout.ValueString(), envRequestTimeout, 0)
	if err != nil {
		resp.Diagnostics.AddError("Invalid request_timeout", err.Error())
		return
	}
	retryWaitMin, err := resolveDuration(data.RetryWaitMin.ValueString(), envRetryWaitMin, defaultRetryWaitMin)
	if err != nil {
		resp.Diagnostics.AddError("Invalid retry_wait_min", err.Error())
		return
	}
	retryWaitMax, err := resolveDuration(data.RetryWaitMax.ValueString(), envRetryWaitMax, defaultRetryWaitMax)
	if err != nil {
		resp.Diagnostics.AddError("Invalid retry_wait_max", err.Error())
		return
	}
	retryMax, err := resolveRetryMax(int(data.RetryMax.ValueInt64()), !data.RetryMax.IsNull())
	if err != nil {
		resp.Diagnostics.AddError("Invalid retry_max", err.Error())
		return
	}

	config := Config{
		ServerURL:      serverURL,
		Username:       username,
		Password:       password,
		Insecure:       data.Insecure.ValueBool(),
		UserAgent:      userAgent,
		RequestTimeout: requestTimeout,
		RetryMax:       retryMax,
		RetryWaitMin:   retryWaitMin,
		RetryWaitMax:   retryWaitMax,
	}

	// Read the certificate
	if caCert != "" {
		config.CACert, err = os.ReadFile(caCert)
		if err != nil {
			resp.Diagnostics.AddError(
				"Unable to read certificate file",
				fmt.Sprintf("Unable to read certificate file %s: %s", caCert, err.Error()),
			)
			return
		}
	}

	client, err := newJenkinsClient(&config)
	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid ca_cert",
			err.Error(),
		)
		return
	}
	if _, err := client.Init(ctx); err != nil {
		resp.Diagnostics.AddError(
			"Unable to initialize client",
			err.Error(),
		)
	}
	resp.ResourceData = client
	resp.DataSourceData = client
}

// DataSources satisfies the provider.Provider interface for JenkinsProvider.
func (p *JenkinsProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		newCredentialUsernameDataSource,
		newCredentialVaultAppRoleDataSource,
		newCredentialAwsDataSource,
		newCredentialSecretTextDataSource,
		newCredentialSecretFileDataSource,
		newCredentialSSHDataSource,
		newCredentialCertificateDataSource,
		newCredentialAzureServicePrincipalDataSource,
		newViewDataSource,
		newJobDataSource,
		newFolderDataSource,
		newPluginDataSource,
		newNodeDataSource,
		newJobsDataSource,
		newFoldersDataSource,
		newNodesDataSource,
		newCredentialsDataSource,
	}
}

// Resources satisfies the provider.Provider interface for JenkinsProvider.
func (p *JenkinsProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		newCredentialAzureServicePrincipalResource,
		newCredentialGitHubAppResource,
		newCredentialSecretFileResource,
		newCredentialSecretTextResource,
		newCredentialSSHResource,
		newCredentialUsernameResource,
		newCredentialVaultAppRoleResource,
		newCredentialAwsResource,
		newCredentialCertificateResource,
		newCredentialDomainResource,
		newViewResource,
		newNodeResource,
		newPipelineJobResource,
		newMultibranchPipelineResource,
		newPluginResource,
	}
}
