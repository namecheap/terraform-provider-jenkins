package jenkins

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestFolderNameValidator(t *testing.T) {
	for _, in := range []string{"folder_name", "parent/child", "a/b/c", ""} {
		req := validator.StringRequest{Path: path.Root("folder"), ConfigValue: types.StringValue(in)}
		resp := &validator.StringResponse{}
		folderNameValidator{}.ValidateString(context.Background(), req, resp)
		if resp.Diagnostics.HasError() {
			t.Errorf("unexpected error for valid folder %q: %v", in, resp.Diagnostics)
		}
	}

	// backslashes are not valid Jenkins path separators
	req := validator.StringRequest{Path: path.Root("folder"), ConfigValue: types.StringValue(`parent\child`)}
	resp := &validator.StringResponse{}
	folderNameValidator{}.ValidateString(context.Background(), req, resp)
	if !resp.Diagnostics.HasError() {
		t.Error("expected error for backslash in folder path, got none")
	}
}
