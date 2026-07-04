package jenkins

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// covStatusClient returns a client whose server answers the CSRF-crumb probe with
// a valid JSON crumb and every other request with the given (non-retryable)
// status code, so the status-code branches in the adapter's GET/POST helpers run
// without the retry layer nulling the response.
func covStatusClient(t *testing.T, code int) *jenkinsAdapter {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "crumbIssuer") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"crumb":"abc","crumbRequestField":"Jenkins-Crumb"}`)
			return
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(srv.Close)
	c, err := newJenkinsClient(&Config{ServerURL: srv.URL, RetryMax: 0})
	if err != nil {
		t.Fatalf("newJenkinsClient: %v", err)
	}
	return c
}

func TestCovS_ConfigStatusBranches(t *testing.T) {
	ctx := context.Background()
	var out struct{}
	must := func(label string, err error) {
		if err == nil {
			t.Errorf("%s: expected error", label)
		}
	}

	// 404 → not-found branches (non-retryable, response preserved)
	c404 := covStatusClient(t, 404)
	must("GetCredentialDomain 404", c404.GetCredentialDomain(ctx, "", "d", &out))
	must("GetUser 404", c404.GetUser(ctx, "u", &out))
	must("GetNodeConfig 404", c404.GetNodeConfig(ctx, "n", &out))

	// 403 → generic >=400 error branches (non-retryable)
	c403 := covStatusClient(t, 403)
	must("GetCredentialDomain 403", c403.GetCredentialDomain(ctx, "", "d", &out))
	must("GetRole 403", c403.GetRole(ctx, "globalRoles", "r", &out))
	must("GetUser 403", c403.GetUser(ctx, "u", &out))
	must("GetNodeConfig 403", c403.GetNodeConfig(ctx, "n", &out))
	must("UpdateCredentialDomain 403", c403.UpdateCredentialDomain(ctx, "", "d", "desc"))
	must("DeleteCredentialDomain 403", c403.DeleteCredentialDomain(ctx, "", "d"))
	must("AddRole 403", c403.AddRole(ctx, "globalRoles", "r", []string{"p"}, "", false))
	must("DeleteUser 403", c403.DeleteUser(ctx, "u"))

	// 409 → credential-domain conflict branch
	must("CreateCredentialDomain 409", covStatusClient(t, 409).CreateCredentialDomain(ctx, "", "d", "desc"))
}
