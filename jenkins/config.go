package jenkins

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/hashicorp/terraform-plugin-log/tflog"
)

type jenkinsClient interface {
	CreateJobInFolder(ctx context.Context, config string, jobName string, parentIDs ...string) (*jenkins.Job, error)
	Credentials() *jenkins.CredentialsManager
	DeleteJobInFolder(ctx context.Context, name string, parentIDs ...string) (bool, error)
	GetJob(ctx context.Context, id string, parentIDs ...string) (*jenkins.Job, error)
	GetFolder(ctx context.Context, id string, parents ...string) (*jenkins.Folder, error)
	GetView(ctx context.Context, name string) (*jenkins.View, error)
}

// frameworkClient is the Jenkins client surface used by the terraform-plugin-framework
// resources and data sources. It extends jenkinsClient (shared with the SDKv2 path) with
// the operations only the framework side calls, so the framework helpers can depend on an
// interface (enabling a mock in tests) instead of the concrete *jenkinsAdapter.
type frameworkClient interface {
	jenkinsClient
	GetPlugin(ctx context.Context, name string) (*jenkins.Plugin, error)
	CreateView(ctx context.Context, name string, viewType string) (*jenkins.View, error)
	// PostRequest wraps the underlying Requester.Post so callers do not reach through to
	// the embedded gojenkins client's Requester field, which cannot be mocked.
	PostRequest(ctx context.Context, endpoint string, payload io.Reader, responseStruct interface{}, querystring map[string]string) (*http.Response, error)

	// Listing operations back the jenkins_jobs/_folders/_nodes list data sources.
	GetAllNodes(ctx context.Context) ([]*jenkins.Node, error)
	GetAllJobNames(ctx context.Context) ([]jenkins.InnerJob, error)

	// Node operations back the jenkins_node resource and data source.
	CreateNode(ctx context.Context, name string, numExecutors int, description string, remoteFS string, label string, options ...interface{}) (*jenkins.Node, error)
	GetNode(ctx context.Context, name string) (*jenkins.Node, error)
	DeleteNode(ctx context.Context, name string) (bool, error)
	// GetNodeConfig fetches /computer/<name>/config.xml and XML-decodes it into out.
	// Returned wrapped so callers depend on the interface rather than the embedded
	// gojenkins Requester, whose Do() appends a trailing slash that breaks config.xml.
	GetNodeConfig(ctx context.Context, name string, out interface{}) error

	// Credential-domain operations back the jenkins_credential_domain resource.
	// gojenkins has no domain support, so these are implemented directly against
	// the credentials store REST endpoints.
	CreateCredentialDomain(ctx context.Context, folder, name, description string) error
	GetCredentialDomain(ctx context.Context, folder, name string, out interface{}) error
	UpdateCredentialDomain(ctx context.Context, folder, name, description string) error
	DeleteCredentialDomain(ctx context.Context, folder, name string) error

	// Plugin management backs the jenkins_plugin resource. HasPlugin performs a
	// fresh (uncached) lookup, unlike GetPlugin which memoizes the manifest for
	// the data source; the resource must observe its own installs.
	InstallPlugin(ctx context.Context, name, version string) error
	UninstallPlugin(ctx context.Context, name string) error
	HasPlugin(ctx context.Context, name string) (*jenkins.Plugin, error)
}

// Ensure the concrete adapter satisfies the framework client surface.
var _ frameworkClient = (*jenkinsAdapter)(nil)

// jenkinsAdapter wraps the Jenkins client, enabling additional functionality
type jenkinsAdapter struct {
	*jenkins.Jenkins

	// pluginsOnce/plugins/pluginsErr memoize the full plugin manifest so that
	// multiple jenkins_plugin lookups within a single provider lifetime
	// (one plan/apply) share a single GET of /pluginManager instead of
	// re-fetching and decoding the whole inventory per lookup.
	pluginsOnce sync.Once
	plugins     *jenkins.Plugins
	pluginsErr  error
}

// Config is the set of parameters needed to configure the Jenkins provider.
type Config struct {
	ServerURL string
	CACert    []byte
	Username  string
	Password  string
	Insecure  bool
	UserAgent string

	// RequestTimeout bounds each provider HTTP operation, including any retries.
	// The zero value means no timeout (the historical behaviour).
	RequestTimeout time.Duration
	// RetryMax is the number of additional attempts made for a failed idempotent
	// request. Zero disables retries.
	RetryMax int
	// RetryWaitMin and RetryWaitMax bound the exponential backoff between retries.
	RetryWaitMin time.Duration
	RetryWaitMax time.Duration
}

// Resilience defaults and the environment variables that override the matching
// provider attributes. These are applied by the provider Configure functions so
// the framework and SDKv2 entry points behave identically.
const (
	// defaultCredentialDomain is the default domain that all credentials go into.
	// The value represents "All domains" in the Jenkins system.
	defaultCredentialDomain = "_"

	envRequestTimeout = "JENKINS_REQUEST_TIMEOUT"
	envRetryMax       = "JENKINS_RETRY_MAX"
	envRetryWaitMin   = "JENKINS_RETRY_WAIT_MIN"
	envRetryWaitMax   = "JENKINS_RETRY_WAIT_MAX"

	// defaultRetryMax retries a failed idempotent request four times by default,
	// smoothing over brief controller unavailability (proxy 502/504, 429, or a
	// restart) without masking a genuine outage.
	defaultRetryMax = 4

	// maxErrorBodyExcerpt bounds how many bytes of a failed response body are
	// echoed into an error diagnostic, so a large proxy error page does not
	// flood the Terraform output.
	maxErrorBodyExcerpt = 512
)

// defaultRetryWaitMin and defaultRetryWaitMax bound the exponential backoff
// between retry attempts when not overridden.
var (
	defaultRetryWaitMin = 1 * time.Second
	defaultRetryWaitMax = 30 * time.Second
)

// resolveDuration returns the duration parsed from attr when it is non-empty,
// else from the named environment variable when set, else def. An unparseable
// value yields an error identifying the source.
func resolveDuration(attr, envKey string, def time.Duration) (time.Duration, error) {
	if attr != "" {
		d, err := time.ParseDuration(attr)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", attr, err)
		}
		return d, nil
	}
	if v := os.Getenv(envKey); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q in %s: %w", v, envKey, err)
		}
		return d, nil
	}
	return def, nil
}

// resolveRetryMax returns the retry count: attrVal when attrSet is true, else
// JENKINS_RETRY_MAX when set, else defaultRetryMax. Negative values are rejected.
func resolveRetryMax(attrVal int, attrSet bool) (int, error) {
	if attrSet {
		if attrVal < 0 {
			return 0, fmt.Errorf("retry_max must be >= 0, got %d", attrVal)
		}
		return attrVal, nil
	}
	if v := os.Getenv(envRetryMax); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("invalid integer %q in %s: %w", v, envRetryMax, err)
		}
		if n < 0 {
			return 0, fmt.Errorf("%s must be >= 0, got %d", envRetryMax, n)
		}
		return n, nil
	}
	return defaultRetryMax, nil
}

// userAgentTransport injects a User-Agent header on every outbound request.
type userAgentTransport struct {
	inner     http.RoundTripper
	userAgent string
}

func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("User-Agent", t.userAgent)
	return t.inner.RoundTrip(req)
}

// jenkinsPostRedirectTransport converts HTTP 302 responses to POST requests into
// 200+{} responses. Jenkins uses 302 redirects as success signals for state-changing
// operations (createView, addJobToView, doDelete, etc.). gojenkins v1.2.0's
// ReadJSONResponse now returns JSON decode errors, breaking the old behaviour where
// HTML redirect pages were silently ignored.
type jenkinsPostRedirectTransport struct {
	base http.RoundTripper
}

func (t *jenkinsPostRedirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	if resp.StatusCode == http.StatusFound && req.Method == http.MethodPost {
		_ = resp.Body.Close()
		resp.StatusCode = http.StatusOK
		resp.Body = io.NopCloser(strings.NewReader("{}"))
	}
	return resp, nil
}

// idempotentMethods lists the HTTP methods that are safe to transparently retry:
// repeating them has the same effect as a single successful call. POST and PATCH
// are excluded because Jenkins drives its state-changing operations (createView,
// doDelete, config submission) over POST, and replaying those could duplicate or
// corrupt state.
var idempotentMethods = map[string]bool{
	http.MethodGet:     true,
	http.MethodHead:    true,
	http.MethodOptions: true,
	http.MethodPut:     true,
	http.MethodDelete:  true,
	http.MethodTrace:   true,
}

// retryRoutingTransport sends idempotent requests through the retrying transport
// and every other request straight to the direct transport, guaranteeing that
// non-idempotent requests (notably POST) are issued exactly once.
type retryRoutingTransport struct {
	retrying http.RoundTripper
	direct   http.RoundTripper
}

func (t *retryRoutingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if idempotentMethods[req.Method] {
		return t.retrying.RoundTrip(req)
	}
	return t.direct.RoundTrip(req)
}

// enrichErrorHandler is the retryablehttp.ErrorHandler invoked once retries are
// exhausted. gojenkins surfaces a failed request as a bare status string; this
// rebuilds the error with the request method, URL, final status code, and a
// truncated response body so CI failures are diagnosable.
func enrichErrorHandler(resp *http.Response, err error, numTries int) (*http.Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("jenkins API request failed after %d attempt(s): %w", numTries, err)
	}
	defer func() { _ = resp.Body.Close() }()

	method, reqURL := "", ""
	if resp.Request != nil {
		method = resp.Request.Method
		reqURL = resp.Request.URL.String()
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyExcerpt))
	excerpt := strings.TrimSpace(string(body))
	if excerpt == "" {
		excerpt = "(empty response body)"
	}
	return nil, fmt.Errorf(
		"jenkins API request failed after %d attempt(s): %s %s: status %d: %s",
		numTries, method, reqURL, resp.StatusCode, excerpt,
	)
}

// retryLogHook emits a DEBUG line via terraform-plugin-log on each retry (the
// initial attempt, retryNum 0, is not logged). The request context carries the
// provider's tflog configuration, so these lines appear under TF_LOG=DEBUG.
func retryLogHook(_ retryablehttp.Logger, req *http.Request, retryNum int) {
	if retryNum == 0 {
		return
	}
	tflog.Debug(req.Context(), "retrying Jenkins API request", map[string]interface{}{
		"method":  req.Method,
		"url":     req.URL.String(),
		"attempt": retryNum + 1,
	})
}

func newJenkinsClient(c *Config) (*jenkinsAdapter, error) {
	httpClient, err := newHTTPClient(c)
	if err != nil {
		return nil, err
	}
	client := jenkins.CreateJenkins(httpClient, c.ServerURL, c.Username, c.Password)
	return &jenkinsAdapter{Jenkins: client}, nil
}

// newHTTPClient assembles the *http.Client used to talk to Jenkins. From the
// wire outward the layers are: the base transport (TLS trust or an opted-in
// insecure skip), a User-Agent injector, the POST-302→200 redirect shim, then
// automatic retries for idempotent requests, and finally an optional
// per-operation request timeout. It is separated from newJenkinsClient so the
// retry and timeout behaviour can be unit-tested with httptest.
func newHTTPClient(c *Config) (*http.Client, error) {
	transport := http.RoundTripper(http.DefaultTransport)
	tlsCfg := &tls.Config{}
	if c.Insecure {
		tlsCfg.InsecureSkipVerify = true //nolint:gosec // user-opted-in skip
	} else if len(c.CACert) > 0 {
		certPool := x509.NewCertPool()
		if !certPool.AppendCertsFromPEM(c.CACert) {
			return nil, fmt.Errorf("ca_cert does not contain any valid PEM-encoded certificates")
		}
		tlsCfg.RootCAs = certPool
	}
	if c.Insecure || len(c.CACert) > 0 {
		transport = &http.Transport{TLSClientConfig: tlsCfg}
	}
	if c.UserAgent != "" {
		transport = &userAgentTransport{inner: transport, userAgent: c.UserAgent}
	}
	transport = &jenkinsPostRedirectTransport{base: transport}

	retryMax := c.RetryMax
	if retryMax < 0 {
		retryMax = 0
	}
	retryClient := retryablehttp.NewClient()
	retryClient.HTTPClient = &http.Client{Transport: transport}
	retryClient.RetryMax = retryMax
	retryClient.RetryWaitMin = c.RetryWaitMin
	retryClient.RetryWaitMax = c.RetryWaitMax
	retryClient.CheckRetry = retryablehttp.DefaultRetryPolicy
	retryClient.ErrorHandler = enrichErrorHandler
	retryClient.RequestLogHook = retryLogHook
	// Silence retryablehttp's own stderr logger; retries are reported through
	// tflog via retryLogHook instead.
	retryClient.Logger = nil

	return &http.Client{
		Transport: &retryRoutingTransport{
			retrying: &retryablehttp.RoundTripper{Client: retryClient},
			direct:   transport,
		},
		Timeout: c.RequestTimeout,
	}, nil
}

func (j *jenkinsAdapter) Credentials() *jenkins.CredentialsManager {
	return &jenkins.CredentialsManager{
		J: j.Jenkins,
	}
}

// PostRequest issues a POST to the given Jenkins endpoint via the underlying requester.
// It exists so framework resources can depend on the frameworkClient interface rather
// than reaching into the embedded gojenkins client's Requester field.
func (j *jenkinsAdapter) PostRequest(ctx context.Context, endpoint string, payload io.Reader, responseStruct interface{}, querystring map[string]string) (*http.Response, error) {
	return j.Requester.Post(ctx, endpoint, payload, responseStruct, querystring)
}

// GetNodeConfig fetches /computer/<name>/config.xml and XML-decodes it into out.
//
// It issues the GET directly rather than via the gojenkins Requester because
// Requester.Do appends a trailing slash to non-POST endpoints, turning
// ".../config.xml" into ".../config.xml/" which Jenkins does not serve. The
// request reuses the adapter's authenticated, retry-wrapped HTTP client.
func (j *jenkinsAdapter) GetNodeConfig(ctx context.Context, name string, out interface{}) error {
	r, ok := j.Requester.(*jenkins.Requester)
	if !ok {
		return fmt.Errorf("unexpected requester type %T", j.Requester)
	}

	endpoint := strings.TrimRight(r.Base, "/") + "/computer/" + url.PathEscape(name) + "/config.xml"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	if r.BasicAuth != nil {
		req.SetBasicAuth(r.BasicAuth.Username, r.BasicAuth.Password)
	}

	resp, err := r.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("404 node %q not found", name)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("unexpected status %d fetching config for node %q", resp.StatusCode, name)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	// Jenkins emits its config.xml with an XML 1.1 declaration
	// (<?xml version='1.1' ...?>), which Go's encoding/xml rejects ("unsupported
	// version"). Strip the leading declaration before decoding; only child
	// elements are read, so it is unnecessary.
	return xml.Unmarshal(stripXMLDeclaration(body), out)
}

// stripXMLDeclaration removes a leading <?xml ... ?> processing instruction from
// b, if present, so a document declared as XML 1.1 can be parsed by encoding/xml
// (which supports only 1.0).
func stripXMLDeclaration(b []byte) []byte {
	trimmed := bytes.TrimSpace(b)
	if bytes.HasPrefix(trimmed, []byte("<?xml")) {
		if idx := bytes.Index(trimmed, []byte("?>")); idx != -1 {
			return trimmed[idx+2:]
		}
	}
	return b
}

// credentialDomainXML is the config payload for a credentials domain. An empty
// specifications set makes the domain match any credential (a plain named store).
type credentialDomainXML struct {
	XMLName        xml.Name `xml:"com.cloudbees.plugins.credentials.domains.Domain"`
	Name           string   `xml:"name"`
	Description    string   `xml:"description"`
	Specifications string   `xml:"specifications"`
}

// credentialStoreBase returns the REST base path of the credentials store for the
// given (unformatted) folder: the global system store, or a folder-scoped store.
func credentialStoreBase(folder string) string {
	if folder == "" {
		return "/credentials/store/system"
	}
	return "/job/" + formatFolderName(folder) + "/credentials/store/folder"
}

// CreateCredentialDomain creates a credentials domain in the given folder's store.
func (j *jenkinsAdapter) CreateCredentialDomain(ctx context.Context, folder, name, description string) error {
	payload, err := xml.Marshal(credentialDomainXML{Name: name, Description: description})
	if err != nil {
		return err
	}
	resp, err := j.Requester.PostXML(ctx, credentialStoreBase(folder)+"/createDomain", string(payload), j.Raw, map[string]string{})
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusConflict {
		return fmt.Errorf("credential domain %q already exists", name)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("invalid response code %d creating credential domain %q", resp.StatusCode, name)
	}
	return nil
}

// UpdateCredentialDomain rewrites a domain's config.xml (e.g. its description).
func (j *jenkinsAdapter) UpdateCredentialDomain(ctx context.Context, folder, name, description string) error {
	payload, err := xml.Marshal(credentialDomainXML{Name: name, Description: description})
	if err != nil {
		return err
	}
	resp, err := j.Requester.PostXML(ctx, credentialStoreBase(folder)+"/domain/"+name+"/config.xml", string(payload), j.Raw, map[string]string{})
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("invalid response code %d updating credential domain %q", resp.StatusCode, name)
	}
	return nil
}

// DeleteCredentialDomain removes a domain (and any credentials it contains).
func (j *jenkinsAdapter) DeleteCredentialDomain(ctx context.Context, folder, name string) error {
	resp, err := j.Requester.Post(ctx, credentialStoreBase(folder)+"/domain/"+name+"/doDelete", nil, j.Raw, map[string]string{})
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("invalid response code %d deleting credential domain %q", resp.StatusCode, name)
	}
	return nil
}

// GetCredentialDomain fetches a domain's config.xml and XML-decodes it into out.
// It issues the GET directly to avoid the gojenkins Requester trailing-slash quirk.
func (j *jenkinsAdapter) GetCredentialDomain(ctx context.Context, folder, name string, out interface{}) error {
	r, ok := j.Requester.(*jenkins.Requester)
	if !ok {
		return fmt.Errorf("unexpected requester type %T", j.Requester)
	}

	endpoint := strings.TrimRight(r.Base, "/") + credentialStoreBase(folder) + "/domain/" + url.PathEscape(name) + "/config.xml"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	if r.BasicAuth != nil {
		req.SetBasicAuth(r.BasicAuth.Username, r.BasicAuth.Password)
	}

	resp, err := r.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("404 credential domain %q not found", name)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("unexpected status %d fetching credential domain %q", resp.StatusCode, name)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	return xml.Unmarshal(stripXMLDeclaration(body), out)
}

// DeleteJobInFolder assists in running DeleteJob funcs, as DeleteJob is not folder aware
// and cannot take a canonical job ID without mishandling it.
func (j *jenkinsAdapter) DeleteJobInFolder(ctx context.Context, name string, parentIDs ...string) (bool, error) {
	return j.DeleteJob(ctx, strings.Join(append(parentIDs, name), "/job/"))
}

// GetPlugin returns the installed plugin with the given short name, or an error if not found.
//
// The full plugin manifest is fetched at most once per adapter (i.e. once per
// provider configuration): the first call performs the GET /pluginManager and
// caches the result, and subsequent lookups filter the cached inventory. This
// avoids a redundant full-manifest fetch for every jenkins_plugin data source
// in a configuration.
func (j *jenkinsAdapter) GetPlugin(ctx context.Context, name string) (*jenkins.Plugin, error) {
	j.pluginsOnce.Do(func() {
		j.plugins, j.pluginsErr = j.GetPlugins(ctx, 1)
	})
	if j.pluginsErr != nil {
		return nil, j.pluginsErr
	}
	p := j.plugins.Contains(name)
	if p == nil {
		return nil, fmt.Errorf("404 plugin %q not installed", name)
	}
	return p, nil
}
