package jenkins

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

const (
	// defaultCredentialDomain is the default domain that all credentials go into.
	//
	// The value represents "All domains" in the Jenkins system.
	defaultCredentialDomain = "_"
)

// Provider creates a new Jenkins provider.
//
// Deprecated: Use the provider-framework version of the provider for all new resources.
func Provider() *schema.Provider {
	return &schema.Provider{
		Schema: map[string]*schema.Schema{
			"server_url": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The URL of the Jenkins server to connect to. It should be fully qualified (e.g. `https://...`) and point to the root of the Jenkins server location.",
			},
			"ca_cert": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "The path to the Jenkins self-signed certificate. It may be required in order to authenticate to your Jenkins instance.",
			},
			"username": {
				Type:        schema.TypeString,
				Optional:    true, // Needs to be optional to be able to run terraform validate without providing credentials
				Description: "The username to authenticate to Jenkins.",
			},
			"password": {
				Type:        schema.TypeString,
				Optional:    true, // Needs to be optional to be able to run terraform validate without providing credentials
				Sensitive:   true,
				Description: "The password to authenticate to Jenkins. If you are using the GitHub OAuth authentication method, enter your Personal Access Token here.",
			},
			"insecure": {
				Type:        schema.TypeBool,
				Optional:    true,
				Description: "Disables TLS certificate verification. Set to true only for non-production Jenkins instances with self-signed certificates when `ca_cert` cannot be used.",
			},
			"request_timeout": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Maximum duration for each Jenkins API operation, including retries, as a Go duration string (e.g. `30s`, `2m`). Overridable via `JENKINS_REQUEST_TIMEOUT`. Defaults to no timeout.",
			},
			"retry_max": {
				Type:        schema.TypeInt,
				Optional:    true,
				Description: "Number of times to retry a failed idempotent request (GET/HEAD/OPTIONS/PUT/DELETE) on connection errors, HTTP 429, or 5xx responses. POST requests are never retried. Overridable via `JENKINS_RETRY_MAX`. Defaults to `4`; set to `0` to disable retries.",
			},
			"retry_wait_min": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Minimum wait between retries as a Go duration string (e.g. `1s`). Overridable via `JENKINS_RETRY_WAIT_MIN`. Defaults to `1s`.",
			},
			"retry_wait_max": {
				Type:        schema.TypeString,
				Optional:    true,
				Description: "Maximum wait between retries as a Go duration string (e.g. `30s`). Overridable via `JENKINS_RETRY_WAIT_MAX`. Defaults to `30s`.",
			},
		},

		DataSourcesMap: map[string]*schema.Resource{},

		ResourcesMap: map[string]*schema.Resource{
			"jenkins_folder": resourceJenkinsFolder(),
		},

		ConfigureContextFunc: configureProvider,
	}
}

// Deprecated: Use the provider-framework version of the provider for all new resources.
func configureProvider(ctx context.Context, d *schema.ResourceData) (interface{}, diag.Diagnostics) {
	serverURL := os.Getenv("JENKINS_URL")
	if d.Get("server_url").(string) != "" {
		serverURL = d.Get("server_url").(string)
	}
	if serverURL == "" {
		return nil, diag.Errorf("server_url is required and must be provided in the provider config or the JENKINS_URL environment variable")
	}

	caCert := os.Getenv("JENKINS_CA_CERT")
	if d.Get("ca_cert").(string) != "" {
		caCert = d.Get("ca_cert").(string)
	}

	username := os.Getenv("JENKINS_USERNAME")
	if d.Get("username").(string) != "" {
		username = d.Get("username").(string)
	}

	password := os.Getenv("JENKINS_PASSWORD")
	if d.Get("password").(string) != "" {
		password = d.Get("password").(string)
	}

	requestTimeout, err := resolveDuration(d.Get("request_timeout").(string), envRequestTimeout, 0)
	if err != nil {
		return nil, diag.Errorf("Invalid request_timeout: %s", err.Error())
	}
	retryWaitMin, err := resolveDuration(d.Get("retry_wait_min").(string), envRetryWaitMin, defaultRetryWaitMin)
	if err != nil {
		return nil, diag.Errorf("Invalid retry_wait_min: %s", err.Error())
	}
	retryWaitMax, err := resolveDuration(d.Get("retry_wait_max").(string), envRetryWaitMax, defaultRetryWaitMax)
	if err != nil {
		return nil, diag.Errorf("Invalid retry_wait_max: %s", err.Error())
	}

	// Distinguish an explicit retry_max = 0 (disable retries) from an unset
	// attribute via the raw config: d.GetOk cannot tell 0 from absent.
	retryMaxVal, retryMaxSet := 0, false
	if raw := d.GetRawConfig(); !raw.IsNull() && raw.Type().HasAttribute("retry_max") {
		if a := raw.GetAttr("retry_max"); !a.IsNull() && a.IsKnown() {
			n, _ := a.AsBigFloat().Int64()
			retryMaxVal, retryMaxSet = int(n), true
		}
	}
	retryMax, err := resolveRetryMax(retryMaxVal, retryMaxSet)
	if err != nil {
		return nil, diag.Errorf("Invalid retry_max: %s", err.Error())
	}

	config := Config{
		ServerURL:      serverURL,
		Username:       username,
		Password:       password,
		Insecure:       d.Get("insecure").(bool),
		RequestTimeout: requestTimeout,
		RetryMax:       retryMax,
		RetryWaitMin:   retryWaitMin,
		RetryWaitMax:   retryWaitMax,
	}

	// Read the certificate
	if caCert != "" {
		config.CACert, err = os.ReadFile(caCert)
		if err != nil {
			return nil, diag.Errorf("Unable to read certificate file %s: %s", caCert, err.Error())
		}
	}

	client, err := newJenkinsClient(&config)
	if err != nil {
		return nil, diag.Errorf("Invalid ca_cert: %s", err.Error())
	}
	if _, err = client.Init(ctx); err != nil {
		return nil, diag.FromErr(err)
	}

	return client, nil
}
