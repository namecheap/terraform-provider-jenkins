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

	if idAttr, ok := s["id"].(schema.StringAttribute); !ok {
		t.Fatalf("schema() attribute \"id\" has unexpected type %T", s["id"])
	} else {
		if !idAttr.Computed {
			t.Error("schema() attribute \"id\" should be Computed")
		}
		if idAttr.Required {
			t.Error("schema() attribute \"id\" should not be Required")
		}
	}

	if nameAttr, ok := s["name"].(schema.StringAttribute); !ok {
		t.Fatalf("schema() attribute \"name\" has unexpected type %T", s["name"])
	} else {
		if !nameAttr.Required {
			t.Error("schema() attribute \"name\" should be Required")
		}
		if nameAttr.Computed {
			t.Error("schema() attribute \"name\" should not be Computed")
		}
	}

	if folderAttr, ok := s["folder"].(schema.StringAttribute); !ok {
		t.Fatalf("schema() attribute \"folder\" has unexpected type %T", s["folder"])
	} else {
		if folderAttr.Required {
			t.Error("schema() attribute \"folder\" should not be Required")
		}
		if folderAttr.Computed {
			t.Error("schema() attribute \"folder\" should not be Computed")
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

	// All credential attributes must be StringAttribute.
	for _, key := range []string{"id", "name", "folder", "description", "domain", "scope"} {
		if _, ok := s[key].(schema.StringAttribute); !ok {
			t.Errorf("schemaCredential() attribute %q should be schema.StringAttribute, got %T", key, s[key])
		}
	}

	// id must be Computed (set by provider after creation).
	if idAttr, ok := s["id"].(schema.StringAttribute); ok {
		if !idAttr.Computed {
			t.Error("schemaCredential() attribute \"id\" should be Computed")
		}
	}

	// description/domain/scope are Optional+Computed because they carry static defaults.
	for _, key := range []string{"description", "domain", "scope"} {
		if attr, ok := s[key].(schema.StringAttribute); ok {
			if !attr.Computed {
				t.Errorf("schemaCredential() attribute %q should be Computed (has a static default)", key)
			}
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

func TestResourceHelperImportState_invalidID(t *testing.T) {
	r := newResourceHelper()
	resp := &resource.ImportStateResponse{}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "no-slash"}, resp)
	if !resp.Diagnostics.HasError() {
		t.Error("ImportState() with ID missing slash should return an error")
	}
}
