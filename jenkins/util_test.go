package jenkins

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	gojenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestFormatFolderName(t *testing.T) {
	inputSimple, inputFolder, inputNested, inputDuped := "job-name", "folder/job-name", "parent/child/job-name", "parent/job/child/job/job-name"

	actual := formatFolderName(inputSimple)
	if actual != inputSimple {
		t.Errorf("Expected %s but received %s", inputSimple, actual)
	}

	actual = formatFolderName(inputFolder)
	if actual != "folder/job/job-name" {
		t.Errorf("Expected %s but received %s", inputFolder, actual)
	}

	actual = formatFolderName(inputNested)
	if actual != "parent/job/child/job/job-name" {
		t.Errorf("Expected %s but received %s", inputNested, actual)
	}

	actual = formatFolderName(inputDuped)
	if actual != "parent/job/child/job/job-name" {
		t.Errorf("Expected %s but received %s", inputDuped, actual)
	}
}

func TestFormatFolderID(t *testing.T) {
	inputSimple := []string{"folder-id"}
	inputNested := []string{"folder-parent", "folder-id"}
	inputDuped := []string{"folder-parent", "job", "folder-id"}

	actual := formatFolderID(inputSimple)
	if actual != "/job/folder-id" {
		t.Errorf("Expected /job/folder-id but received %s", actual)
	}

	actual = formatFolderID(inputNested)
	if actual != "/job/folder-parent/job/folder-id" {
		t.Errorf("Expected /job/folder-parent/job/folder-id but received %s", actual)
	}

	actual = formatFolderID(inputDuped)
	if actual != "/job/folder-parent/job/folder-id" {
		t.Errorf("Expected /job/folder-parent/job/folder-id but received %s", actual)
	}
}

func TestFormatFolderID_Empty(t *testing.T) {
	if got := formatFolderID(nil); got != "" {
		t.Errorf("formatFolderID(nil) = %q, want \"\"", got)
	}
	if got := formatFolderID([]string{}); got != "" {
		t.Errorf("formatFolderID([]) = %q, want \"\"", got)
	}
}

func TestParseCanonicalJobID(t *testing.T) {
	inputSimple, inputFolder, inputNested := "job-name", "folder/job-name", "parent/child/job-name"

	actual, actualFolders := parseCanonicalJobID(inputSimple)
	if actual != inputSimple || len(actualFolders) != 0 {
		t.Errorf("Expected %s with empty folder array but received %s %s", inputSimple, actual, actualFolders)
	}

	actual, actualFolders = parseCanonicalJobID(inputFolder)
	if actual != inputSimple || len(actualFolders) != 1 || actualFolders[0] != "folder" {
		t.Errorf("Expected %s with single folder array but received %s %s", inputSimple, actual, actualFolders)
	}

	actual, actualFolders = parseCanonicalJobID(inputNested)
	if actual != inputSimple || len(actualFolders) != 2 || actualFolders[0] != "parent" || actualFolders[1] != "child" {
		t.Errorf("Expected %s with double folder array but received %s %s", inputSimple, actual, actualFolders)
	}
}

func TestParseCanonicalJobID_Empty(t *testing.T) {
	name, folders := parseCanonicalJobID("")
	if name != "" || len(folders) != 0 {
		t.Errorf("parseCanonicalJobID(\"\") = %q, %v, want \"\", nil", name, folders)
	}
}

func TestExtractFolders(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{input: "", want: nil},
		{input: "folder", want: []string{"folder"}},
		{input: "/job/folder", want: []string{"folder"}},
		{input: "/job/parent/job/child", want: []string{"parent", "child"}},
		{input: "parent/job/child", want: []string{"parent", "child"}},
	}
	for _, tt := range tests {
		got := extractFolders(tt.input)
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("extractFolders(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestTemplatesEqual(t *testing.T) {
	cases := []struct {
		name        string
		left, right string
		equal       bool
	}{
		// XML declaration presence / version.
		{"declaration stripped", "<?xml version=\"1.0\" encoding=\"UTF-8\"?><root>Test Case</root>", "<root>Test Case</root>", true},
		{"declaration 1.1 variant", `<?xml version='1.1'?><a><b/></a>`, `<a><b/></a>`, true},
		{"different content", "<?xml version=\"1.0\" encoding=\"UTF-8\"?><root>Test Incorrect</root>", "<root>Test Case</root>", false},
		{"even more different content", "<root>Test Incorrect</root>", "<root>Test Even More Incorrect</root>", false},
		// HTML entities (from #8).
		{"html entity apos", "<root>&apos;/&apos;</root>", "<root>'/'</root>", true},
		{"html entity apos reversed", "<root>'/'</root>", "<root>&apos;/&apos;</root>", true},
		// Plugin versions (from #8).
		{"plugin version differs", `<flow-definition plugin="workflow-job@2.25"><description>test</description></flow-definition>`, `<flow-definition plugin="workflow-job@1571.1580.v18e46842c125"><description>test</description></flow-definition>`, true},
		{"multiple plugin versions differ", `<a plugin="x@1"><b plugin="y@2">text</b></a>`, `<a plugin="x@99"><b plugin="y@100">text</b></a>`, true},
		{"content differs beyond plugins", `<flow-definition plugin="workflow-job@2.25"><description>old</description></flow-definition>`, `<flow-definition plugin="workflow-job@2.25"><description>new</description></flow-definition>`, false},
		// Canonical XML.
		{"attribute order", `<a x="1" y="2"><b/></a>`, `<a y="2" x="1"><b/></a>`, true},
		{"empty element form", `<a><b></b></a>`, `<a><b/></a>`, true},
		{"insignificant whitespace", "<a>\n\t<b/>\n</a>", "<a><b/></a>", true},
		{"genuine text change", `<a><b>one</b></a>`, `<a><b>two</b></a>`, false},
		{"genuine element added", `<a><b/></a>`, `<a><b/><c/></a>`, false},
		{"genuine child reorder", `<a><b/><c/></a>`, `<a><c/><b/></a>`, false},
		{"malformed falls back to string inequality", `<a><b></a>`, `<a><b/></a>`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := templatesEqual(tc.left, tc.right); got != tc.equal {
				t.Errorf("templatesEqual(%q, %q) = %t, want %t", tc.left, tc.right, got, tc.equal)
			}
		})
	}
}

func TestReDisabledElement(t *testing.T) {
	cases := []struct{ in, want string }{
		{`<project><disabled>true</disabled></project>`, `<project></project>`},
		{`<project><disabled>false</disabled></project>`, `<project></project>`},
		{`<project><x/></project>`, `<project><x/></project>`},
	}
	for _, c := range cases {
		if got := reDisabledElement.ReplaceAllString(c.in, ""); got != c.want {
			t.Errorf("strip(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestJobXMLValidator(t *testing.T) {
	cases := []struct {
		name     string
		xml      string
		wantErr  bool
		wantWarn bool
	}{
		{"valid project", `<project><description>x</description></project>`, false, false},
		{"valid self-closing", `<flow-definition plugin="workflow-job@2.25"/>`, false, false},
		{"jenkins 1.1 declaration", "<?xml version=\"1.1\" encoding=\"UTF-8\"?><project><description>x</description></project>", false, false},
		{"jenkins 1.1 malformed", "<?xml version=\"1.1\" encoding=\"UTF-8\"?><project><description>x</project>", true, false},
		{"empty string", ``, false, false},
		{"whitespace only", "  \n  ", false, false},
		{"mismatched tags", `<project><description>x</project>`, true, false},
		{"unclosed root", `<project>`, true, false},
		{"no root element", `just text`, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := &validator.StringResponse{}
			jobXMLValidatorAttr().ValidateString(context.Background(), validator.StringRequest{
				Path:        path.Root("template"),
				ConfigValue: types.StringValue(tc.xml),
			}, resp)

			if got := resp.Diagnostics.HasError(); got != tc.wantErr {
				t.Errorf("ValidateString(%q) error = %t, want %t (%v)", tc.xml, got, tc.wantErr, resp.Diagnostics)
			}
			if got := len(resp.Diagnostics.Warnings()) > 0; got != tc.wantWarn {
				t.Errorf("ValidateString(%q) warning = %t, want %t (%v)", tc.xml, got, tc.wantWarn, resp.Diagnostics)
			}
		})
	}
}

func TestGenerateCredentialID(t *testing.T) {
	inputFolder, inputName := "test-folder", "test-name"
	actual := generateCredentialID(inputFolder, inputName)
	if actual != "test-folder/test-name" {
		t.Errorf("Expected %s/%s but got: %s", inputFolder, inputName, actual)
	}
}

func TestIsNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"404 prefix", errors.New("404 Not Found"), true},
		{"404 suffix", errors.New("error: 404"), true},
		{"404 in middle", errors.New("got 404 response"), true},
		{"200 ok", errors.New("200 OK"), false},
		{"500 error", errors.New("500 Internal Server Error"), false},
		{"unrelated", errors.New("connection refused"), false},
		{"nil error", nil, false},
	}
	for _, tt := range tests {
		if got := isNotFound(tt.err); got != tt.want {
			t.Errorf("isNotFound(%q) = %v, want %v", tt.err.Error(), got, tt.want)
		}
	}
}

func TestFolderExists(t *testing.T) {
	ctx := context.Background()

	t.Run("empty name skips lookup", func(t *testing.T) {
		client := &mockJenkinsClient{}
		if err := folderExists(ctx, client, ""); err != nil {
			t.Errorf("unexpected error for empty folder name: %v", err)
		}
	})

	t.Run("existing folder returns nil", func(t *testing.T) {
		client := &mockJenkinsClient{
			mockGetFolder: func(_ context.Context, _ string, _ ...string) (*gojenkins.Folder, error) {
				return &gojenkins.Folder{}, nil
			},
		}
		if err := folderExists(ctx, client, "my-folder"); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("missing folder returns error", func(t *testing.T) {
		want := fmt.Errorf("404 not found")
		client := &mockJenkinsClient{
			mockGetFolder: func(_ context.Context, _ string, _ ...string) (*gojenkins.Folder, error) {
				return nil, want
			},
		}
		if err := folderExists(ctx, client, "missing-folder"); err == nil {
			t.Error("expected error, got nil")
		} else if err.Error() != want.Error() {
			t.Errorf("got %v, want %v", err, want)
		}
	})
}
