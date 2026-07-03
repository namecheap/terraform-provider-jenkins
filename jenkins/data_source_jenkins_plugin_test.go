package jenkins

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestIsNotFoundPlugin(t *testing.T) {
	err := errors.New("404 plugin \"missing\" not installed")
	if !isNotFound(err) {
		t.Error("expected isNotFound to return true for plugin 404 error")
	}
}

func TestJenkinsAdapter_GetPlugin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"plugins": []map[string]interface{}{
				{"shortName": "git", "version": "5.2.0", "active": true, "enabled": true, "longName": "Git plugin", "url": "https://example.com"},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		//nolint:errcheck
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c, _ := newJenkinsClient(&Config{ServerURL: srv.URL})

	p, err := c.GetPlugin(context.Background(), "git")
	if err != nil {
		t.Fatalf("GetPlugin returned unexpected error: %v", err)
	}
	if p.ShortName != "git" {
		t.Errorf("ShortName = %q, want %q", p.ShortName, "git")
	}
	if p.Version != "5.2.0" {
		t.Errorf("Version = %q, want %q", p.Version, "5.2.0")
	}

	_, err = c.GetPlugin(context.Background(), "nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent plugin, got nil")
	}
	if !isNotFound(err) {
		t.Errorf("expected 404 error for nonexistent plugin, got: %v", err)
	}
}

func TestAccJenkinsPluginDataSource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				// "git" is always installed in the test Jenkins image
				Config: `data "jenkins_plugin" "git" { name = "git" }`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("data.jenkins_plugin.git", "id", "git"),
					resource.TestCheckResourceAttrSet("data.jenkins_plugin.git", "version"),
					resource.TestCheckResourceAttr("data.jenkins_plugin.git", "enabled", "true"),
				),
			},
		},
	})
}
