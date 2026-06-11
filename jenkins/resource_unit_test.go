package jenkins

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
)

func TestResourceHelperSchema(t *testing.T) {
	r := newResourceHelper()
	s := r.schema(map[string]schema.Attribute{})

	for _, key := range []string{"id", "name", "folder"} {
		if _, ok := s[key]; !ok {
			t.Errorf("schema() missing attribute %q", key)
		}
	}
}

func TestResourceHelperSchema_noOverwrite(t *testing.T) {
	r := newResourceHelper()

	custom := schema.StringAttribute{MarkdownDescription: "custom id"}
	s := r.schema(map[string]schema.Attribute{"id": custom})

	got, ok := s["id"]
	if !ok {
		t.Fatal("schema() removed custom id attribute")
	}
	if got.(schema.StringAttribute).MarkdownDescription != "custom id" {
		t.Error("schema() overwrote custom id attribute")
	}
}

func TestResourceHelperSchemaCredential(t *testing.T) {
	r := newResourceHelper()
	s := r.schemaCredential(map[string]schema.Attribute{})

	for _, key := range []string{"id", "name", "folder", "description", "domain", "scope"} {
		if _, ok := s[key]; !ok {
			t.Errorf("schemaCredential() missing attribute %q", key)
		}
	}
}

func TestResourceHelperConfigure_nilData(t *testing.T) {
	r := newResourceHelper()
	resp := &resource.ConfigureResponse{}
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: nil}, resp)
	if resp.Diagnostics.HasError() {
		t.Errorf("Configure() with nil ProviderData should not return error, got: %v", resp.Diagnostics)
	}
	if r.client != nil {
		t.Error("Configure() with nil ProviderData should leave client nil")
	}
}

func TestResourceHelperConfigure_wrongType(t *testing.T) {
	r := newResourceHelper()
	resp := &resource.ConfigureResponse{}
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "unexpected-string"}, resp)
	if !resp.Diagnostics.HasError() {
		t.Error("Configure() with wrong type should return an error")
	}
}

func TestResourceHelperConfigure_valid(t *testing.T) {
	r := newResourceHelper()
	client := newJenkinsClient(&Config{})
	resp := &resource.ConfigureResponse{}
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: client}, resp)
	if resp.Diagnostics.HasError() {
		t.Errorf("Configure() with valid client should not return error, got: %v", resp.Diagnostics)
	}
	if r.client != client {
		t.Error("Configure() should set client on resource helper")
	}
}
