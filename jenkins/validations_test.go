package jenkins

import (
	"testing"

	"github.com/hashicorp/go-cty/cty"
)

func TestValidateJobName(t *testing.T) {

	input, ctyPath := "job_name", make(cty.Path, 0)
	actual := validateJobName(input, ctyPath)

	if actual.HasError() {
		t.Errorf("Error, validation failed for input: %s", input)
	}

	// Test if we fail when we should
	input = "job_name/second_level"
	actual = validateJobName(input, ctyPath)
	if !actual.HasError() {
		t.Errorf("Error, validation failed for input: %s", input)
	}
}

func TestValidateFolderName(t *testing.T) {
	ctyPath := make(cty.Path, 0)

	for _, input := range []string{"folder_name", "parent/child", "a/b/c"} {
		if actual := validateFolderName(input, ctyPath); actual.HasError() {
			t.Errorf("unexpected error for valid input %q", input)
		}
	}

	// backslashes are not valid Jenkins path separators
	if actual := validateFolderName(`parent\child`, ctyPath); !actual.HasError() {
		t.Error("expected error for backslash in folder path, got none")
	}
}

func TestValidateCredentialScope(t *testing.T) {

	input, ctyPath := "GLOBAL", make(cty.Path, 0)
	actual := validateCredentialScope(input, ctyPath)
	if actual.HasError() {
		t.Errorf("Error, validation failed for input: %s", input)
	}

	input = "SYSTEM"
	actual = validateCredentialScope(input, ctyPath)
	if actual.HasError() {
		t.Errorf("Error, validation failed for input: %s", input)
	}

	// Test if we fail when we should
	input = "WRONG_INPUT"
	actual = validateCredentialScope(input, ctyPath)
	if !actual.HasError() {
		t.Errorf("Error, negative validation failed for input: %s", input)
	}
}
