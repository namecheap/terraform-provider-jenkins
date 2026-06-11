package jenkins

import (
	"testing"

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
