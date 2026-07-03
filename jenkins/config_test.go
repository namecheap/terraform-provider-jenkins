package jenkins

import (
	"context"
	"io"
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
	mockGetPlugin         func(ctx context.Context, name string) (*jenkins.Plugin, error)
	mockCreateView        func(ctx context.Context, name string, viewType string) (*jenkins.View, error)
	mockPostRequest       func(ctx context.Context, endpoint string, payload io.Reader, responseStruct interface{}, querystring map[string]string) (*http.Response, error)
	mockCreateNode        func(ctx context.Context, name string, numExecutors int, description string, remoteFS string, label string, options ...interface{}) (*jenkins.Node, error)
	mockGetNode           func(ctx context.Context, name string) (*jenkins.Node, error)
	mockDeleteNode        func(ctx context.Context, name string) (bool, error)
	mockGetNodeConfig     func(ctx context.Context, name string, out interface{}) error
}

// mockJenkinsClient implements the full framework client surface so it can be
// injected into framework resources and data sources under test.
var _ frameworkClient = (*mockJenkinsClient)(nil)

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

func (m *mockJenkinsClient) GetPlugin(ctx context.Context, name string) (*jenkins.Plugin, error) {
	return m.mockGetPlugin(ctx, name)
}

func (m *mockJenkinsClient) CreateView(ctx context.Context, name string, viewType string) (*jenkins.View, error) {
	return m.mockCreateView(ctx, name, viewType)
}

func (m *mockJenkinsClient) PostRequest(ctx context.Context, endpoint string, payload io.Reader, responseStruct interface{}, querystring map[string]string) (*http.Response, error) {
	return m.mockPostRequest(ctx, endpoint, payload, responseStruct, querystring)
}

func (m *mockJenkinsClient) CreateNode(ctx context.Context, name string, numExecutors int, description string, remoteFS string, label string, options ...interface{}) (*jenkins.Node, error) {
	return m.mockCreateNode(ctx, name, numExecutors, description, remoteFS, label, options...)
}

func (m *mockJenkinsClient) GetNode(ctx context.Context, name string) (*jenkins.Node, error) {
	return m.mockGetNode(ctx, name)
}

func (m *mockJenkinsClient) DeleteNode(ctx context.Context, name string) (bool, error) {
	return m.mockDeleteNode(ctx, name)
}

func (m *mockJenkinsClient) GetNodeConfig(ctx context.Context, name string, out interface{}) error {
	return m.mockGetNodeConfig(ctx, name, out)
}

// testCACertPEM is a self-signed certificate used only to exercise the CACert
// code path. AppendCertsFromPEM parses the structure without checking validity
// dates, so expiry is irrelevant to these tests.
const testCACertPEM = `-----BEGIN CERTIFICATE-----
MIIDNTCCAh2gAwIBAgIUfAh5DLdoeHg4eFl3RnQ7qbDjtgIwDQYJKoZIhvcNAQEL
BQAwKjEoMCYGA1UEAwwfdGVycmFmb3JtLXByb3ZpZGVyLWplbmtpbnMtdGVzdDAe
Fw0yNjA3MDMwNjU4NDZaFw0zNjA2MzAwNjU4NDZaMCoxKDAmBgNVBAMMH3RlcnJh
Zm9ybS1wcm92aWRlci1qZW5raW5zLXRlc3QwggEiMA0GCSqGSIb3DQEBAQUAA4IB
DwAwggEKAoIBAQDTzGkXXYJDGxChQg6m+bFnB5ykdvP5MOsnhP/XP+gzweoI+p2D
EmOtt2z6Ny/6rm9a1ISPSFjblUZa4gz4355SV2v0gZXIZbq405OGjk8z4RoFEesX
VAzsDQAr08woaF1e/FNTLtPNs61HHQpCXmeFsUmNQRlAMEphyIMQSobE9WzGsMIZ
XSIsvnA7MKTkzE8jMSvouC26gU9ZchPR5jCNNtMbpabfk5+HCmkpevtqmdeg+Y91
ZM/V0i28YbQhQ7db8Fk0cfX5s/hgj5R/WNqRZJJ0t0DMYsbLSsN0FXGi9scjW+R0
JveknqbMPOq8avpq3j0oXGyEoJNFX1FbSq8pAgMBAAGjUzBRMB0GA1UdDgQWBBTK
50xNLR+YRwHxTBF/AgtP5ReepDAfBgNVHSMEGDAWgBTK50xNLR+YRwHxTBF/AgtP
5ReepDAPBgNVHRMBAf8EBTADAQH/MA0GCSqGSIb3DQEBCwUAA4IBAQAXzfoTPrsG
8EAjhqPOhMEnB8L8Xt7gYquk4lL0wCW5TW2gim/EQCXxQx6BOxgb7LtoeqWGuq18
6FWvy8z0DJccx2qgJcdR1rUTjL9YeHVW6aHZ57bSvfx3+GqXPkCXLxyhzrVVDQZg
UdxVsfqaVpcO8T8mY7AsJ1KTltLtEW/rQfWZGrlEV9cZ9MINYmQx2wMthd/PyXGP
vE/k6y7v7bIuFdOYrj9BJ9afRP1wOdpC0xfhgOtu4qNs25ObWRrzIu7c7zwJs7uN
LhYMHmektYUK2kVMw8q4kwjogHLpaFVUT5UzI4eRoL4DscZ9UtxmjG2HddI20EI1
kBtu3QWHJ858
-----END CERTIFICATE-----
`

func TestNewJenkinsClient(t *testing.T) {
	c, err := newJenkinsClient(&Config{})
	if err != nil {
		t.Errorf("Unexpected error: %s", err)
	}
	if c == nil {
		t.Errorf("Expected populated client")
	}

	c, err = newJenkinsClient(&Config{
		CACert: []byte(testCACertPEM),
	})
	if err != nil {
		t.Fatalf("Unexpected error with valid CACert: %s", err)
	}
	// When CACert is provided, a custom http.Client (not http.DefaultClient) must be used
	// so the TLS config with the CA cert pool is active.
	// gojenkins v1.2.0 stores Requester as a JenkinsRequester interface; type-assert to
	// the concrete *jenkins.Requester to inspect the underlying http.Client.
	if r, ok := c.Requester.(*jenkins.Requester); !ok {
		t.Error("Expected Requester to be *jenkins.Requester")
	} else if r.Client == http.DefaultClient {
		t.Error("Expected custom HTTP client when CACert is set")
	}

	c, err = newJenkinsClient(&Config{
		Insecure: true,
	})
	if err != nil {
		t.Fatalf("Unexpected error with Insecure: %s", err)
	}
	if r, ok := c.Requester.(*jenkins.Requester); !ok {
		t.Error("Expected Requester to be *jenkins.Requester")
	} else if r.Client == http.DefaultClient {
		t.Error("Expected custom HTTP client when Insecure is set")
	}
}

func TestNewJenkinsClient_invalidCACert(t *testing.T) {
	c, err := newJenkinsClient(&Config{CACert: []byte("not a valid pem certificate")})
	if err == nil {
		t.Error("Expected an error for a ca_cert with no valid PEM certificates, got nil")
	}
	if c != nil {
		t.Error("Expected a nil client when ca_cert is invalid")
	}
}

func TestNewJenkinsClient_UserAgent(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, _ := newJenkinsClient(&Config{ServerURL: srv.URL, UserAgent: "terraform-provider-jenkins my-org"})
	// make any request to trigger the transport
	//nolint:errcheck
	c.Requester.Get(context.Background(), "/", nil, nil)

	if got != "terraform-provider-jenkins my-org" {
		t.Errorf("User-Agent = %q, want %q", got, "terraform-provider-jenkins my-org")
	}
}

func TestJenkinsPostRedirectTransport_POST302becomesOK(t *testing.T) {
	for _, path := range []string{"/doDelete", "/createView", "/addJobToView"} {
		t.Run(path, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/", http.StatusFound)
			}))
			defer srv.Close()

			transport := &jenkinsPostRedirectTransport{base: http.DefaultTransport}
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+path, nil)
			resp, err := transport.RoundTrip(req)
			if err != nil {
				t.Fatalf("RoundTrip() error = %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("POST 302 on %s: StatusCode = %d, want 200", path, resp.StatusCode)
			}
		})
	}
}

func TestJenkinsPostRedirectTransport_GET302passesThrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/", http.StatusFound)
	}))
	defer srv.Close()

	transport := &jenkinsPostRedirectTransport{base: http.DefaultTransport}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/any", nil)
	resp, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusFound {
		t.Errorf("GET 302 should pass through: StatusCode = %d, want 302", resp.StatusCode)
	}
}

func TestJenkinsAdapter_Credentials(t *testing.T) {
	c, _ := newJenkinsClient(&Config{})
	cm := c.Credentials()

	if cm == nil {
		t.Errorf("Expected populated client")
	} else if cm.J != c.Jenkins {
		t.Error("Expected credentials client to match client")
	}
}
