package jenkins

import (
	"context"
	"net/http"
	"net/http/httptest"
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
	if c.Requester.Client == http.DefaultClient {
		t.Error("Expected custom HTTP client when CACert is set")
	}

	c = newJenkinsClient(&Config{
		Insecure: true,
	})
	if c.Requester.Client == http.DefaultClient {
		t.Error("Expected custom HTTP client when Insecure is set")
	}
}

func TestNewJenkinsClient_UserAgent(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newJenkinsClient(&Config{ServerURL: srv.URL, UserAgent: "terraform-provider-jenkins my-org"})
	// make any request to trigger the transport
	//nolint:errcheck
	c.Requester.Get(context.Background(), "/", nil, nil)

	if got != "terraform-provider-jenkins my-org" {
		t.Errorf("User-Agent = %q, want %q", got, "terraform-provider-jenkins my-org")
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
