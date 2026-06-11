package jenkins

import (
	"testing"

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
