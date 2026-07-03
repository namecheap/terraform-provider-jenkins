package jenkins

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestJenkinsProvider_Metadata(t *testing.T) {
	p := &JenkinsProvider{}
	resp := &provider.MetadataResponse{}
	p.Metadata(context.Background(), provider.MetadataRequest{}, resp)
	if resp.TypeName != "jenkins" {
		t.Errorf("TypeName = %q, want %q", resp.TypeName, "jenkins")
	}
}

func TestJenkinsProvider_Schema(t *testing.T) {
	p := &JenkinsProvider{}
	resp := &provider.SchemaResponse{}
	p.Schema(context.Background(), provider.SchemaRequest{}, resp)

	for _, attr := range []string{"server_url", "ca_cert", "username", "password", "insecure"} {
		if _, ok := resp.Schema.Attributes[attr]; !ok {
			t.Errorf("Schema() missing attribute %q", attr)
		}
	}
}

// TestJenkinsProvider_Configure_missingServerURL is a regression test for the bug
// where Configure() added a "server_url is required" diagnostic but did not return,
// falling through to construct the client and dial the network via client.Init.
// With no server_url provided (config all-null and JENKINS_URL unset), Configure
// must surface the validation diagnostic and must not initialize the client.
func TestJenkinsProvider_Configure_missingServerURL(t *testing.T) {
	// Ensure the environment does not supply a server URL for this test.
	t.Setenv("JENKINS_URL", "")

	ctx := context.Background()
	p := &JenkinsProvider{}

	schemaResp := &provider.SchemaResponse{}
	p.Schema(ctx, provider.SchemaRequest{}, schemaResp)

	// Build an all-null config object from the provider schema (mirrors the
	// pattern in resource_unit_test.go); every attribute, including server_url,
	// is therefore unset.
	objType := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object)
	attrs := make(map[string]tftypes.Value, len(objType.AttributeTypes))
	for name, typ := range objType.AttributeTypes {
		attrs[name] = tftypes.NewValue(typ, nil)
	}

	req := provider.ConfigureRequest{
		Config: tfsdk.Config{
			Schema: schemaResp.Schema,
			Raw:    tftypes.NewValue(objType, attrs),
		},
	}
	resp := &provider.ConfigureResponse{}

	p.Configure(ctx, req, resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("Configure() with missing server_url should return an error diagnostic")
	}

	var sawServerURL bool
	for _, d := range resp.Diagnostics.Errors() {
		if strings.Contains(d.Summary(), "server_url is required") {
			sawServerURL = true
		}
		if strings.Contains(d.Summary(), "Unable to initialize client") {
			t.Errorf("Configure() attempted to initialize the client despite missing server_url: %s", d.Detail())
		}
	}
	if !sawServerURL {
		t.Errorf("Configure() should surface the 'server_url is required' diagnostic, got: %v", resp.Diagnostics.Errors())
	}

	// The client must not be handed to resources/data sources when config failed.
	if resp.ResourceData != nil {
		t.Error("Configure() set ResourceData despite missing server_url")
	}
	if resp.DataSourceData != nil {
		t.Error("Configure() set DataSourceData despite missing server_url")
	}
}

func TestJenkinsProvider_DataSources(t *testing.T) {
	p := &JenkinsProvider{}
	sources := p.DataSources(context.Background())
	if len(sources) == 0 {
		t.Fatal("DataSources() returned empty slice")
	}
	for i, fn := range sources {
		if fn == nil {
			t.Errorf("DataSources()[%d] is nil", i)
			continue
		}
		if ds := fn(); ds == nil {
			t.Errorf("DataSources()[%d]() returned nil", i)
		}
	}
}

func TestJenkinsProvider_Resources(t *testing.T) {
	p := &JenkinsProvider{}
	resources := p.Resources(context.Background())
	if len(resources) == 0 {
		t.Fatal("Resources() returned empty slice")
	}
	for i, fn := range resources {
		if fn == nil {
			t.Errorf("Resources()[%d] is nil", i)
			continue
		}
		if r := fn(); r == nil {
			t.Errorf("Resources()[%d]() returned nil", i)
		}
	}
}
