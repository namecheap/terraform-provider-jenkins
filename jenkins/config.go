package jenkins

import (
	"context"
	"crypto/tls"
	"crypto/x509"
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
}

func newJenkinsClient(c *Config) *jenkinsAdapter {
	var httpClient *http.Client
	tlsCfg := &tls.Config{}
	if c.Insecure {
		tlsCfg.InsecureSkipVerify = true //nolint:gosec // user-opted-in skip
	} else if len(c.CACert) > 0 {
		certPool := x509.NewCertPool()
		certPool.AppendCertsFromPEM(c.CACert)
		tlsCfg.RootCAs = certPool
	}
	if c.Insecure || len(c.CACert) > 0 {
		httpClient = &http.Client{
			Transport: &http.Transport{TLSClientConfig: tlsCfg},
		}
	}
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
