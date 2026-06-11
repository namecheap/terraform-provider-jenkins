package jenkins

import (
	"context"
	"net/http"
	"testing"

	jenkins "github.com/bndr/gojenkins"
)

type mockJenkinsClient struct {
	mockCreateJobInFolder func(ctx context.Context, config string, jobName string, parentIDs ...string) (*jenkins.Job, error)
	mockDeleteJobInFolder func(ctx context.Context, name string, parentIDs ...string) (bool, error)
	mockGetJob            func(ctx context.Context, id string, parentIDs ...string) (*jenkins.Job, error)
	mockGetFolder         func(ctx context.Context, id string, parentIDs ...string) (*jenkins.Folder, error)
	mockGetView           func(ctx context.Context, name string) (*jenkins.View, error)
}

func (m *mockJenkinsClient) CreateJobInFolder(ctx context.Context, config string, jobName string, parentIDs ...string) (*jenkins.Job, error) {
	return m.mockCreateJobInFolder(ctx, config, jobName, parentIDs...)
}

func (m *mockJenkinsClient) Credentials() *jenkins.CredentialsManager {
	return &jenkins.CredentialsManager{}
}

func (m *mockJenkinsClient) DeleteJobInFolder(ctx context.Context, name string, parentIDs ...string) (bool, error) {
	return m.mockDeleteJobInFolder(ctx, name, parentIDs...)
}

func (m *mockJenkinsClient) GetJob(ctx context.Context, id string, parentIDs ...string) (*jenkins.Job, error) {
	return m.mockGetJob(ctx, id, parentIDs...)
}

func (m *mockJenkinsClient) GetFolder(ctx context.Context, id string, parentIDs ...string) (*jenkins.Folder, error) {
	return m.mockGetFolder(ctx, id, parentIDs...)
}

func (m *mockJenkinsClient) GetView(ctx context.Context, name string) (*jenkins.View, error) {
	return m.mockGetView(ctx, name)
}

func TestNewJenkinsClient(t *testing.T) {
	c := newJenkinsClient(&Config{})
	if c == nil {
		t.Errorf("Expected populated client")
	}

	c = newJenkinsClient(&Config{
		CACert: []byte("certificate"),
	})
	// When CACert is provided, a custom http.Client (not http.DefaultClient) must be used
	// so the TLS config with the CA cert pool is active.
	if r, ok := c.Requester.(*jenkins.Requester); !ok {
		t.Fatal("Expected *jenkins.Requester")
	} else if r.Client == http.DefaultClient {
		t.Error("Expected custom HTTP client when CACert is set")
	}
}

func TestJenkinsAdapter_Credentials(t *testing.T) {
	c := newJenkinsClient(&Config{})
	cm := c.Credentials()

	if cm == nil {
		t.Errorf("Expected populated client")
	} else if cm.J != c.Jenkins {
		t.Error("Expected credentials client to match client")
	}
}
