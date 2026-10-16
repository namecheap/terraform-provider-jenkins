package jenkins

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// fakeNodeSecret stands in for a real agent secret. Every error-path test
// asserts it never appears in the returned error.
const fakeNodeSecret = "44c1919f02aa15f668353012dc77ba3c362d3b61577f38344e839c83d63f9f76"

// jnlpDocument renders the descriptor Jenkins 2.583 serves at
// /computer/<name>/jenkins-agent.jnlp.
func jnlpDocument(secret, name string) string {
	return "<jnlp><application-desc>" +
		"<argument>" + secret + "</argument>" +
		"<argument>" + name + "</argument>" +
		"<argument>-workDir</argument><argument>/home/jenkins/agent</argument>" +
		"<argument>-internalDir</argument><argument>remoting</argument>" +
		"<argument>-url</argument><argument>http://localhost:8080/</argument>" +
		"</application-desc></jnlp>"
}

func TestGetNodeSecret(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		cases := []struct {
			name     string
			node     string
			ctxPath  string
			wantPath string
		}{
			{"plain name", "agent1", "", "/computer/agent1/jenkins-agent.jnlp"},
			{"escaped name", "linux agent", "", "/computer/linux%20agent/jenkins-agent.jnlp"},
			{"context path", "agent1", "/jenkins", "/jenkins/computer/agent1/jenkins-agent.jnlp"},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				var gotPath, gotUser string
				srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
					gotPath = r.URL.EscapedPath()
					gotUser, _, _ = r.BasicAuth()
					_, _ = io.WriteString(w, jnlpDocument(fakeNodeSecret, tc.node))
				})
				defer srv.Close()

				c, err := newJenkinsClient(&Config{ServerURL: srv.URL + tc.ctxPath, Username: "admin", Password: "pw"})
				if err != nil {
					t.Fatalf("newJenkinsClient: %v", err)
				}
				got, err := c.GetNodeSecret(context.Background(), tc.node)
				if err != nil {
					t.Fatalf("GetNodeSecret error = %v", err)
				}
				if got != fakeNodeSecret {
					t.Errorf("secret = %q, want %q", got, fakeNodeSecret)
				}
				if gotPath != tc.wantPath {
					t.Errorf("path = %q, want %q", gotPath, tc.wantPath)
				}
				if gotUser != "admin" {
					t.Errorf("basic auth user = %q, want admin", gotUser)
				}
			})
		}
	})

	t.Run("whitespace around the secret", func(t *testing.T) {
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, jnlpDocument("\n  "+fakeNodeSecret+"\n", "agent1"))
		})
		defer srv.Close()

		got, err := covBClient(t, srv).GetNodeSecret(context.Background(), "agent1")
		if err != nil || got != fakeNodeSecret {
			t.Fatalf("GetNodeSecret = %q, %v", got, err)
		}
	})

	t.Run("XML declaration", func(t *testing.T) {
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, "<?xml version='1.1' encoding='UTF-8'?>\n"+jnlpDocument(fakeNodeSecret, "agent1"))
		})
		defer srv.Close()

		got, err := covBClient(t, srv).GetNodeSecret(context.Background(), "agent1")
		if err != nil || got != fakeNodeSecret {
			t.Fatalf("GetNodeSecret = %q, %v", got, err)
		}
	})

	errCases := []struct {
		name    string
		node    string
		status  int
		body    string
		wantErr string
	}{
		{"empty name", "", http.StatusOK, "", "must not be empty"},
		{"unauthorized", "agent1", http.StatusUnauthorized, fakeNodeSecret, "401"},
		{"forbidden", "agent1", http.StatusForbidden, fakeNodeSecret, "Agent/Connect"},
		{"not found", "agent1", http.StatusNotFound, "<html>" + fakeNodeSecret + "</html>", "not an inbound (JNLP) agent"},
		{"server error", "agent1", http.StatusBadRequest, fakeNodeSecret, "unexpected status 400"},
		// An UnmarshalError names element types only, so it is kept: it is what
		// tells a user they got a login or proxy page instead of the descriptor.
		{"HTML login page", "agent1", http.StatusOK, "<html><body>" + fakeNodeSecret + "</body></html>", "expected element type <jnlp> but have <html>"},
		{"malformed XML", "agent1", http.StatusOK, "<jnlp><argument>" + fakeNodeSecret, "invalid JNLP document"},
		// A SyntaxError quotes document text ("invalid character entity &x...;"),
		// so it must be replaced by the fixed message.
		{"syntax error quoting the body", "agent1", http.StatusOK, "<jnlp>&x" + fakeNodeSecret + ";</jnlp>", "invalid JNLP document"},
		{"no arguments", "agent1", http.StatusOK, "<jnlp><application-desc/></jnlp>", "contains no agent secret"},
		{"blank secret", "agent1", http.StatusOK, jnlpDocument(" ", "agent1"), "contains no agent secret"},
		{"other node", "agent1", http.StatusOK, jnlpDocument(fakeNodeSecret, "agent2"), "belongs to a different node"},
		{"oversized", "agent1", http.StatusOK, jnlpDocument(fakeNodeSecret, "agent1") + strings.Repeat(" ", maxNodeSecretDocumentSize), "exceeds"},
	}
	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			defer srv.Close()

			_, err := covBClient(t, srv).GetNodeSecret(context.Background(), tc.node)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.wantErr)
			}
			if strings.Contains(err.Error(), fakeNodeSecret) {
				t.Errorf("error leaks the secret: %v", err)
			}
		})
	}

	// A retried 5xx comes back from enrichErrorHandler with a body excerpt;
	// GetNodeSecret must report the status alone.
	t.Run("retries exhausted", func(t *testing.T) {
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, fakeNodeSecret)
		})
		defer srv.Close()

		c, err := newJenkinsClient(&Config{ServerURL: srv.URL, RetryMax: 1})
		if err != nil {
			t.Fatalf("newJenkinsClient: %v", err)
		}
		_, err = c.GetNodeSecret(context.Background(), "agent1")
		if err == nil || !strings.Contains(err.Error(), "unexpected status 503") {
			t.Fatalf("error = %v, want unexpected status 503", err)
		}
		if strings.Contains(err.Error(), fakeNodeSecret) {
			t.Errorf("error leaks the secret: %v", err)
		}
	})

	t.Run("connection error", func(t *testing.T) {
		srv := httptest.NewServer(nil)
		srv.Close()
		c, err := newJenkinsClient(&Config{ServerURL: srv.URL})
		if err != nil {
			t.Fatalf("newJenkinsClient: %v", err)
		}
		_, err = c.GetNodeSecret(context.Background(), "agent1")
		// The transport error carries no body, so it is wrapped as is and
		// keeps the cause (refused connection, TLS failure, timeout).
		var urlErr *url.Error
		if !errors.As(err, &urlErr) {
			t.Fatalf("error = %v, want it to wrap the *url.Error", err)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		srv := covBServer(func(w http.ResponseWriter, r *http.Request) {})
		defer srv.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := covBClient(t, srv).GetNodeSecret(ctx, "agent1")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
	})
}

func TestNodeSecretDataSourceRead(t *testing.T) {
	ctx := context.Background()

	run := func(t *testing.T, client *mockJenkinsClient) (datasource.ReadResponse, nodeSecretModel) {
		t.Helper()
		d := &nodeSecretDataSource{dataSourceHelper: &dataSourceHelper{client: client}}
		var sr datasource.SchemaResponse
		d.Schema(ctx, datasource.SchemaRequest{}, &sr)

		cfg := tfsdk.State{Schema: sr.Schema}
		if diags := cfg.Set(ctx, nodeSecretModel{
			Name:   types.StringValue("agent1"),
			Secret: types.StringNull(),
		}); diags.HasError() {
			t.Fatalf("config: %v", diags)
		}
		resp := datasource.ReadResponse{State: tfsdk.State{Schema: sr.Schema}}
		d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: sr.Schema, Raw: cfg.Raw}}, &resp)

		var got nodeSecretModel
		if !resp.Diagnostics.HasError() {
			resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
		}
		return resp, got
	}

	t.Run("schema", func(t *testing.T) {
		var sr datasource.SchemaResponse
		newNodeSecretDataSource().Schema(ctx, datasource.SchemaRequest{}, &sr)
		secret := sr.Schema.Attributes["secret"]
		if !secret.IsSensitive() || !secret.IsComputed() {
			t.Errorf("secret: sensitive=%v computed=%v, want both", secret.IsSensitive(), secret.IsComputed())
		}
	})

	t.Run("happy", func(t *testing.T) {
		var gotName string
		resp, got := run(t, &mockJenkinsClient{
			mockGetNodeSecret: func(_ context.Context, name string) (string, error) {
				gotName = name
				return fakeNodeSecret, nil
			},
		})
		if resp.Diagnostics.HasError() {
			t.Fatalf("unexpected diags: %v", resp.Diagnostics)
		}
		if gotName != "agent1" {
			t.Errorf("looked up %q, want agent1", gotName)
		}
		if got.Secret.ValueString() != fakeNodeSecret || got.Name.ValueString() != "agent1" {
			t.Errorf("state = %+v", got)
		}
	})

	t.Run("client error", func(t *testing.T) {
		resp, _ := run(t, &mockJenkinsClient{
			mockGetNodeSecret: func(context.Context, string) (string, error) { return "", fmt.Errorf("boom") },
		})
		if !resp.Diagnostics.HasError() {
			t.Error("expected an error diagnostic")
		}
	})
}

func TestAccJenkinsNodeSecretDataSource_basic(t *testing.T) {
	name := "tf-acc-node-secret-" + acctest.RandStringFromCharSet(8, acctest.CharSetAlphaNum)
	config := fmt.Sprintf(`
resource jenkins_node foo {
  name      = %q
  remote_fs = "/home/jenkins/agent"
}

data jenkins_node_secret foo {
  name = jenkins_node.foo.id
}`, name)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsNodeDestroy,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("data.jenkins_node_secret.foo", "name", name),
					// Jenkins derives the secret as a hex HMAC-SHA256 of the node name.
					resource.TestMatchResourceAttr("data.jenkins_node_secret.foo", "secret", regexp.MustCompile(`^[0-9a-f]{64}$`)),
				),
			},
			{
				Config:   config,
				PlanOnly: true,
			},
		},
	})
}

func TestAccJenkinsNodeSecretDataSource_missing(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: `
data jenkins_node_secret foo {
  name = "tf-acc-node-secret-does-not-exist"
}`,
				ExpectError: regexp.MustCompile(`not found or not an\s+inbound \(JNLP\) agent`),
			},
			{
				Config: `
data jenkins_node_secret foo {
  name = ""
}`,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`string length must be at least 1`),
			},
		},
	})
}
