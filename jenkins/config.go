package jenkins

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	jenkins "github.com/bndr/gojenkins"
)

type jenkinsClient interface {
	CreateJobInFolder(ctx context.Context, config string, jobName string, parentIDs ...string) (*jenkins.Job, error)
	Credentials() *jenkins.CredentialsManager
	DeleteJobInFolder(ctx context.Context, name string, parentIDs ...string) (bool, error)
	GetJob(ctx context.Context, id string, parentIDs ...string) (*jenkins.Job, error)
	GetFolder(ctx context.Context, id string, parents ...string) (*jenkins.Folder, error)
	GetView(ctx context.Context, name string) (*jenkins.View, error)
}

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

func newJenkinsClient(c *Config) (*jenkinsAdapter, error) {
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
	httpClient := &http.Client{Transport: &jenkinsPostRedirectTransport{base: transport}}
	client := jenkins.CreateJenkins(httpClient, c.ServerURL, c.Username, c.Password)
	return &jenkinsAdapter{Jenkins: client}, nil
}

func (j *jenkinsAdapter) Credentials() *jenkins.CredentialsManager {
	return &jenkins.CredentialsManager{
		J: j.Jenkins,
	}
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
