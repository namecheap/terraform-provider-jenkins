package jenkins

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
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

	custom := schema.StringAttribute{
		MarkdownDescription: "custom id",
		Optional:            true,
		Computed:            true,
	}
	s := r.schema(map[string]schema.Attribute{"id": custom})

	got, ok := s["id"]
	if !ok {
		t.Fatal("schema() removed custom id attribute")
	}
	gotAttr, ok := got.(schema.StringAttribute)
	if !ok {
		t.Fatalf("schema() attribute \"id\" has unexpected type %T", got)
	}
	if gotAttr.MarkdownDescription != "custom id" {
		t.Error("schema() overwrote custom id MarkdownDescription")
	}
	if !gotAttr.Optional {
		t.Error("schema() overwrote custom id Optional")
	}
	if !gotAttr.Computed {
		t.Error("schema() overwrote custom id Computed")
	}
	if gotAttr.Required {
		t.Error("schema() overwrote custom id Required")
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

func TestResourceHelperImportState_validID(t *testing.T) {
	ctx := context.Background()
	r := newResourceHelper()

	// ImportState writes the "name", "domain", "folder" and "id" attributes, so
	// build the state from the credential schema (the only one that defines all
	// of them) and seed Raw with a known all-null object. Without a real schema
	// and a non-null object value, State.SetAttribute would panic.
	s := schema.Schema{Attributes: r.schemaCredential(map[string]schema.Attribute{})}
	objType := s.Type().TerraformType(ctx).(tftypes.Object)
	attrs := make(map[string]tftypes.Value, len(objType.AttributeTypes))
	for name, typ := range objType.AttributeTypes {
		attrs[name] = tftypes.NewValue(typ, nil)
	}
	resp := &resource.ImportStateResponse{
		State: tfsdk.State{
			Schema: s,
			Raw:    tftypes.NewValue(objType, attrs),
		},
	}

	r.ImportState(ctx, resource.ImportStateRequest{ID: "my-folder/my-domain/my-name"}, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState(%q) returned error diagnostics: %v", "my-folder/my-domain/my-name", resp.Diagnostics)
	}

	want := map[string]string{
		"folder": "my-folder",
		"domain": "my-domain",
		"name":   "my-name",
		"id":     generateCredentialID("my-folder", "my-name"),
	}
	for attr, wantVal := range want {
		var got types.String
		if diags := resp.State.GetAttribute(ctx, path.Root(attr), &got); diags.HasError() {
			t.Errorf("GetAttribute(%q) returned diagnostics: %v", attr, diags)
			continue
		}
		if got.ValueString() != wantVal {
			t.Errorf("ImportState(%q) set %q = %q, want %q", "my-folder/my-domain/my-name", attr, got.ValueString(), wantVal)
		}
	}
}
