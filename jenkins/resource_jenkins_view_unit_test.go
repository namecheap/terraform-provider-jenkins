package jenkins

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// TestViewResource_Delete_escapesName verifies, via the injectable frameworkClient
// seam, that Delete URL-escapes the view name in the doDelete endpoint (defence in
// depth) and issues the expected request. A raw name with spaces would produce an
// unescaped path without url.PathEscape.
func TestViewResource_Delete_escapesName(t *testing.T) {
	ctx := context.Background()

	var gotEndpoint string
	mock := &mockJenkinsClient{
		mockPostRequest: func(_ context.Context, endpoint string, _ io.Reader, _ interface{}, _ map[string]string) (*http.Response, error) {
			gotEndpoint = endpoint
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{}"))}, nil
		},
	}

	r := &ViewResource{resourceHelper: &resourceHelper{client: mock}}

	schemaResp := &resource.SchemaResponse{}
	r.Schema(ctx, resource.SchemaRequest{}, schemaResp)

	objType := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object)
	attrs := make(map[string]tftypes.Value, len(objType.AttributeTypes))
	for name, typ := range objType.AttributeTypes {
		attrs[name] = tftypes.NewValue(typ, nil)
	}
	attrs["name"] = tftypes.NewValue(objType.AttributeTypes["name"], "my view name")
	state := tftypes.NewValue(objType, attrs)

	req := resource.DeleteRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: state}}
	resp := &resource.DeleteResponse{State: tfsdk.State{Schema: schemaResp.Schema, Raw: state}}

	r.Delete(ctx, req, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete returned diagnostics: %v", resp.Diagnostics.Errors())
	}

	want := "/view/" + url.PathEscape("my view name") + "/doDelete"
	if gotEndpoint != want {
		t.Errorf("Delete endpoint = %q, want %q", gotEndpoint, want)
	}
}
