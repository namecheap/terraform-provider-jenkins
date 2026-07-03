package jenkins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestIsNotFoundPlugin(t *testing.T) {
	err := errors.New("404 plugin \"missing\" not installed")
	if !isNotFound(err) {
		t.Error("expected isNotFound to return true for plugin 404 error")
	}
}

func TestJenkinsAdapter_GetPlugin(t *testing.T) {
	var manifestFetches int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		manifestFetches++
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

	// The manifest must be fetched only once across multiple lookups.
	if manifestFetches != 1 {
		t.Errorf("plugin manifest fetched %d times, want 1 (results should be cached per adapter)", manifestFetches)
	}
}

// TestPluginDataSource_Read_withMock is a pilot unit test demonstrating that the
// injectable frameworkClient seam lets framework data sources be tested without a
// live Jenkins: it injects a mockJenkinsClient and verifies plugin data flows into
// state. It exercises the seam introduced for the framework resources/data sources.
func TestPluginDataSource_Read_withMock(t *testing.T) {
	ctx := context.Background()

	mock := &mockJenkinsClient{
		mockGetPlugin: func(_ context.Context, name string) (*jenkins.Plugin, error) {
			if name != "git" {
				return nil, fmt.Errorf("404 plugin %q not installed", name)
			}
			return &jenkins.Plugin{
				ShortName: "git",
				Version:   "5.2.0",
				LongName:  "Git plugin",
				URL:       "https://example.com",
				Active:    true,
				Enabled:   true,
			}, nil
		},
	}

	d := &pluginDataSource{dataSourceHelper: &dataSourceHelper{client: mock}}

	schemaResp := &datasource.SchemaResponse{}
	d.Schema(ctx, datasource.SchemaRequest{}, schemaResp)

	objType := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object)
	attrs := make(map[string]tftypes.Value, len(objType.AttributeTypes))
	for name, typ := range objType.AttributeTypes {
		attrs[name] = tftypes.NewValue(typ, nil)
	}
	attrs["name"] = tftypes.NewValue(objType.AttributeTypes["name"], "git")
	cfg := tftypes.NewValue(objType, attrs)

	req := datasource.ReadRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: cfg}}
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema, Raw: cfg}}

	d.Read(ctx, req, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Read returned diagnostics: %v", resp.Diagnostics.Errors())
	}

	var got pluginDataSourceModel
	resp.Diagnostics.Append(resp.State.Get(ctx, &got)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get diagnostics: %v", resp.Diagnostics.Errors())
	}
	if got.ID.ValueString() != "git" {
		t.Errorf("id = %q, want %q", got.ID.ValueString(), "git")
	}
	if got.Version.ValueString() != "5.2.0" {
		t.Errorf("version = %q, want %q", got.Version.ValueString(), "5.2.0")
	}
	if !got.Enabled.ValueBool() {
		t.Error("enabled = false, want true")
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
