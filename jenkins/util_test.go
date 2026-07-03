package jenkins

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	gojenkins "github.com/bndr/gojenkins"
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

func TestTemplateDiff(t *testing.T) {
	inputLeft := "<?xml version=\"1.0\" encoding=\"UTF-8\"?><root>Test Case</root>"
	inputRight := "<root>Test Case</root>"

	job := resourceJenkinsJob()
	bag := job.TestResourceData()

	if actual := templateDiff("", inputLeft, inputRight, bag); !actual {
		t.Errorf("Expected %s to be considered equal to %s", inputLeft, inputRight)
	}

	inputLeft = "<?xml version=\"1.0\" encoding=\"UTF-8\"?><root>Test Incorrect</root>"
	if actual := templateDiff("", inputLeft, inputRight, bag); actual {
		t.Errorf("Expected %s to be considered inequal to %s", inputLeft, inputRight)
	}

	inputRight = "<root>Test Incorrect</root>"
	if actual := templateDiff("", inputLeft, inputRight, bag); !actual {
		t.Errorf("Expected %s to be considered equal to %s", inputLeft, inputRight)
	}

	inputRight = "<root>Test Even More Incorrect</root>"
	if actual := templateDiff("", inputLeft, inputRight, bag); actual {
		t.Errorf("Expected %s to be considered inequal to %s", inputLeft, inputRight)
	}
}

func TestTemplateDiff_HTMLEntities(t *testing.T) {
	job := resourceJenkinsFolder()
	bag := job.TestResourceData()
	_ = bag.Set("description", "Case")

	inputLeft := "<root>&apos;/&apos;</root>"
	inputRight := "<root>'/'</root>"
	if actual := templateDiff("", inputLeft, inputRight, bag); !actual {
		t.Errorf("Expected %s to be considered equal to %s", inputLeft, inputRight)
	}

	inputLeft = "<root>'/'</root>"
	inputRight = "<root>&apos;/&apos;</root>"
	if actual := templateDiff("", inputLeft, inputRight, bag); !actual {
		t.Errorf("Expected %s to be considered equal to %s", inputLeft, inputRight)
	}
}

func TestTemplateDiff_PluginVersions(t *testing.T) {
	job := resourceJenkinsJob()
	bag := job.TestResourceData()

	// Different plugin versions on identical content must be equal.
	old := `<flow-definition plugin="workflow-job@2.25"><description>test</description></flow-definition>`
	newVal := `<flow-definition plugin="workflow-job@1571.1580.v18e46842c125"><description>test</description></flow-definition>`
	if !templateDiff("", old, newVal, bag) {
		t.Errorf("different plugin versions should be equal after normalization: %q vs %q", old, newVal)
	}

	// Multiple plugin attributes with differing versions must also be equal.
	old = `<a plugin="x@1"><b plugin="y@2">text</b></a>`
	newVal = `<a plugin="x@99"><b plugin="y@100">text</b></a>`
	if !templateDiff("", old, newVal, bag) {
		t.Errorf("multiple plugin version differences should be equal")
	}

	// Content that differs beyond plugin versions must remain unequal.
	old = `<flow-definition plugin="workflow-job@2.25"><description>old</description></flow-definition>`
	newVal = `<flow-definition plugin="workflow-job@2.25"><description>new</description></flow-definition>`
	if templateDiff("", old, newVal, bag) {
		t.Errorf("different content should remain unequal after plugin stripping")
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
