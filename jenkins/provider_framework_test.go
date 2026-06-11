package jenkins

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
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
