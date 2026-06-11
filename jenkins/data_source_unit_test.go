package jenkins

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
)

func TestDataSourceHelperSchema(t *testing.T) {
	d := newDataSourceHelper()
	s := d.schema(map[string]datasourceschema.Attribute{})

	for _, key := range []string{"id", "name", "folder"} {
		if _, ok := s[key]; !ok {
			t.Errorf("schema() missing attribute %q", key)
		}
	}
}

func TestDataSourceHelperSchema_noOverwrite(t *testing.T) {
	d := newDataSourceHelper()

	custom := datasourceschema.StringAttribute{MarkdownDescription: "custom id"}
	s := d.schema(map[string]datasourceschema.Attribute{"id": custom})

	got, ok := s["id"]
	if !ok {
		t.Fatal("schema() removed custom id attribute")
	}
	if got.(datasourceschema.StringAttribute).MarkdownDescription != "custom id" {
		t.Error("schema() overwrote custom id attribute")
	}
}

func TestDataSourceHelperSchemaCredential(t *testing.T) {
	d := newDataSourceHelper()
	s := d.schemaCredential(map[string]datasourceschema.Attribute{})

	for _, key := range []string{"id", "name", "folder", "description", "domain", "scope"} {
		if _, ok := s[key]; !ok {
			t.Errorf("schemaCredential() missing attribute %q", key)
		}
	}
}

func TestDataSourceHelperConfigure_nilData(t *testing.T) {
	d := newDataSourceHelper()
	resp := &datasource.ConfigureResponse{}
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: nil}, resp)
	if resp.Diagnostics.HasError() {
		t.Errorf("Configure() with nil ProviderData should not return error, got: %v", resp.Diagnostics)
	}
	if d.client != nil {
		t.Error("Configure() with nil ProviderData should leave client nil")
	}
}

func TestDataSourceHelperConfigure_wrongType(t *testing.T) {
	d := newDataSourceHelper()
	resp := &datasource.ConfigureResponse{}
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: "unexpected-string"}, resp)
	if !resp.Diagnostics.HasError() {
		t.Error("Configure() with wrong type should return an error")
	}
}

func TestDataSourceHelperConfigure_valid(t *testing.T) {
	d := newDataSourceHelper()
	client := newJenkinsClient(&Config{})
	resp := &datasource.ConfigureResponse{}
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: client}, resp)
	if resp.Diagnostics.HasError() {
		t.Errorf("Configure() with valid client should not return error, got: %v", resp.Diagnostics)
	}
	if d.client != client {
		t.Error("Configure() should set client on data source helper")
	}
}
