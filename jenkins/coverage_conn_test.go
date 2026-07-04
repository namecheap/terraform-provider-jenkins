package jenkins

import (
	"context"
	"net/http/httptest"
	"testing"
)

// covClosedClient returns a client pointed at an already-closed server, so every
// adapter method's transport call fails — exercising the "request failed" error
// branch in each. Retries are disabled so the failures are immediate.
func covClosedClient(t *testing.T) *jenkinsAdapter {
	t.Helper()
	srv := httptest.NewServer(nil)
	url := srv.URL
	srv.Close()
	c, err := newJenkinsClient(&Config{ServerURL: url, RetryMax: 0})
	if err != nil {
		t.Fatalf("newJenkinsClient: %v", err)
	}
	return c
}

func TestCovH_AdapterConnErrors(t *testing.T) {
	ctx := context.Background()
	c := covClosedClient(t)
	var out struct{}

	check := func(label string, err error) {
		if err == nil {
			t.Errorf("%s: expected a connection error", label)
		}
	}

	// Only the direct-GET adapter methods are exercised here: the POST-based ones
	// call gojenkins SetCrumb, which panics on a nil response from a closed server
	// (upstream bug). Their error paths are covered elsewhere via HTTP status codes.
	check("GetCredentialDomain", c.GetCredentialDomain(ctx, "", "d", &out))
	check("GetRole", c.GetRole(ctx, "globalRoles", "r", &out))
	check("GetUser", c.GetUser(ctx, "u", &out))
	check("GetNodeConfig", c.GetNodeConfig(ctx, "n", &out))
}
