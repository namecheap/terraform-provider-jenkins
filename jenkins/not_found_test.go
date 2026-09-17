package jenkins

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestJenkinsNotFoundTransport pins the body rewrite that lets a Jenkins 404
// reach the caller as a status. Jenkins answers a missing job with an HTML
// error page, and gojenkins decodes the body before it inspects the status, so
// without this the 404 surfaces as `invalid character '<' looking for beginning
// of value` and every resource treats a deleted object as an unreadable one.
func TestJenkinsNotFoundTransport(t *testing.T) {
	const htmlBody = "<!DOCTYPE html><html><head><title>Error 404 Not Found</title></head></html>"

	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
		wantBody    string
	}{
		{
			name:        "html 404 becomes json",
			status:      http.StatusNotFound,
			contentType: "text/html;charset=utf-8",
			body:        htmlBody,
			wantBody:    "{}",
		},
		{
			// A Jenkins endpoint that answers 404 in JSON is already decodable;
			// leave its payload intact.
			name:        "json 404 is left alone",
			status:      http.StatusNotFound,
			contentType: "application/json",
			body:        `{"error":"nope"}`,
			wantBody:    `{"error":"nope"}`,
		},
		{
			name:        "success is untouched",
			status:      http.StatusOK,
			contentType: "text/html",
			body:        htmlBody,
			wantBody:    htmlBody,
		},
		{
			// 500s keep their body: enrichErrorHandler quotes an excerpt of it
			// in the error, which is the only diagnostic a failing call leaves.
			name:        "server error is untouched",
			status:      http.StatusInternalServerError,
			contentType: "text/html",
			body:        htmlBody,
			wantBody:    htmlBody,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			client := &http.Client{Transport: &jenkinsNotFoundTransport{base: http.DefaultTransport}}
			resp, err := client.Get(srv.URL)
			if err != nil {
				t.Fatalf("GET: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != tt.status {
				t.Errorf("status = %d, want %d (the status must survive the rewrite)", resp.StatusCode, tt.status)
			}
			body := make([]byte, len(tt.wantBody)+16)
			n, _ := resp.Body.Read(body)
			if got := string(body[:n]); got != tt.wantBody {
				t.Errorf("body = %q, want %q", got, tt.wantBody)
			}
		})
	}
}

// TestAdapterGetViewNotFound covers the adapter's GetView. gojenkins' own
// GetView discards the poll status and returns a non-nil view whatever the
// server said, so a missing view would come back as an empty one with no
// error — worse than an error, because the resource then reports a view that
// is not there.
func TestAdapterGetViewNotFound(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		wantErr   bool
		wantIs404 bool
	}{
		{name: "found", status: http.StatusOK},
		{name: "missing", status: http.StatusNotFound, wantErr: true, wantIs404: true},
		{name: "server error", status: http.StatusInternalServerError, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte("{}"))
			}))
			defer srv.Close()

			c, err := newJenkinsClient(&Config{ServerURL: srv.URL})
			if err != nil {
				t.Fatalf("newJenkinsClient: %v", err)
			}

			view, err := c.GetView(context.Background(), "some-view")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("GetView returned view %v and no error, want an error", view)
				}
				if got := isNotFound(err); got != tt.wantIs404 {
					t.Errorf("isNotFound(%v) = %v, want %v", err, got, tt.wantIs404)
				}
				return
			}
			if err != nil {
				t.Fatalf("GetView: %v", err)
			}
		})
	}
}

// TestAccJenkinsClientNotFoundErrors asserts that a missing object reports an
// error isNotFound recognises, for every client call a resource Read uses to
// decide whether its object still exists. A miss here means the resource never
// drops from state: refresh fails instead, and so does destroy.
func TestAccJenkinsClientNotFoundErrors(t *testing.T) {
	if os.Getenv("TF_ACC") == "" {
		t.Skip("TF_ACC not set; this checks error shapes from a live Jenkins")
	}
	testAccPreCheck(t)

	ctx := context.Background()
	c, err := newJenkinsClient(&Config{
		ServerURL: os.Getenv("JENKINS_URL"),
		Username:  os.Getenv("JENKINS_USERNAME"),
		Password:  os.Getenv("JENKINS_PASSWORD"),
	})
	if err != nil {
		t.Fatalf("newJenkinsClient: %v", err)
	}

	missing := "tf-acc-does-not-exist"
	check := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s: expected an error for a missing object, got nil", name)
			return
		}
		if !isNotFound(err) {
			t.Errorf("%s: isNotFound = false, want true (err = %q)", name, err)
		}
	}

	_, err = c.GetJob(ctx, missing)
	check("GetJob", err)

	_, err = c.GetFolder(ctx, missing)
	check("GetFolder", err)

	_, err = c.GetView(ctx, missing)
	check("GetView", err)

	_, err = c.GetPlugin(ctx, missing)
	check("GetPlugin", err)

	var node jenkins.Node
	check("GetNodeConfig", c.GetNodeConfig(ctx, missing, &node))

	var user map[string]interface{}
	check("GetUser", c.GetUser(ctx, missing, &user))

	cm := c.Credentials()
	cm.Folder = ""
	check("Credentials.GetSingle", cm.GetSingle(ctx, "_", missing, &CertificateCredentials{}))
}

// TestAccJenkinsFolder_disappears deletes the folder in Jenkins behind
// Terraform's back and re-plans. Refresh must drop it from state and the apply
// must recreate it. Before the 404 fix this step failed with "Unable to Refresh
// Resource: invalid character '<' looking for beginning of value", and the
// subsequent destroy failed the same way, leaving an entry that could only be
// removed with `terraform state rm`.
func TestAccJenkinsFolder_disappears(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	name := "tf-acc-test-" + randString
	config := fmt.Sprintf(`
		resource jenkins_folder foo {
		  name = %q
		}`, name)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsFolderDestroy,
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig: func() {
					if _, err := testAccClient.DeleteJobInFolder(context.Background(), name); err != nil {
						t.Fatalf("deleting folder out of band: %v", err)
					}
				},
				Config: config,
				Check:  resource.TestCheckResourceAttr("jenkins_folder.foo", "id", "/job/"+name),
			},
		},
	})
}
