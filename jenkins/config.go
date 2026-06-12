package jenkins

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"strings"

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

// doDeleteRoundTripper converts Jenkins doDelete 302 redirects to 200 so that
// gojenkins v1.2.0's ReadJSONResponse (which now returns JSON decode errors)
// doesn't fail when Jenkins returns HTML after following the redirect.
type doDeleteRoundTripper struct {
	base http.RoundTripper
}

func (t *doDeleteRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err
	}
	if resp.StatusCode == http.StatusFound && strings.HasSuffix(req.URL.Path, "/doDelete") {
		_ = resp.Body.Close()
		resp.StatusCode = http.StatusOK
		resp.Body = io.NopCloser(strings.NewReader("{}"))
	}
	return resp, nil
}

func newJenkinsClient(c *Config) *jenkinsAdapter {
	transport := http.RoundTripper(http.DefaultTransport)
	tlsCfg := &tls.Config{}
	if c.Insecure {
		tlsCfg.InsecureSkipVerify = true //nolint:gosec // user-opted-in skip
	} else if len(c.CACert) > 0 {
		certPool := x509.NewCertPool()
		certPool.AppendCertsFromPEM(c.CACert)
		tlsCfg.RootCAs = certPool
	}
	if c.Insecure || len(c.CACert) > 0 {
		transport = &http.Transport{TLSClientConfig: tlsCfg}
	}
	if c.UserAgent != "" {
		transport = &userAgentTransport{inner: transport, userAgent: c.UserAgent}
	}
	httpClient := &http.Client{Transport: &doDeleteRoundTripper{base: transport}}
	client := jenkins.CreateJenkins(httpClient, c.ServerURL, c.Username, c.Password)
	return &jenkinsAdapter{Jenkins: client}
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
func (j *jenkinsAdapter) GetPlugin(ctx context.Context, name string) (*jenkins.Plugin, error) {
	plugins, err := j.GetPlugins(ctx, 1)
	if err != nil {
		return nil, err
	}
	p := plugins.Contains(name)
	if p == nil {
		return nil, fmt.Errorf("404 plugin %q not installed", name)
	}
	return p, nil
}
