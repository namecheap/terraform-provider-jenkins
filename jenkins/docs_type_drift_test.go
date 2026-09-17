package jenkins

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// The docs pages are generated from templates, and tfplugindocs renders
// attribute types straight from the schema — a bullet it produces reads
// "- `permissions` (Set of String) …" and cannot disagree with the code.
//
// Hand-written prose can. The pages for jenkins_folder and jenkins_job used to
// spell their arguments out by hand in the form
//
//	* `permissions` - (Required) A list of strings containing …
//
// so when #188 turned that attribute into a Set the page kept saying "list"
// through three releases (#196). The docs CI job only diffs template output
// against docs/, which cannot see a disagreement between prose and schema.
//
// This test closes that gap: any hand-written attribute bullet that names a
// type must agree with the schema. It is deliberately narrow — it matches the
// hand-written bullet shape, not every sentence containing the word "list" —
// so prose like the v1.2.2 upgrade note, which describes the *former* type on
// purpose, is not flagged.

// handwrittenAttrBullet matches a hand-written argument bullet:
//
//   - `name` - (Required) A set of strings …
//
// tfplugindocs' own bullets are "- `name` (String) …" with no " - " separator,
// so generated content never matches.
var handwrittenAttrBullet = regexp.MustCompile("(?m)^\\s*[-*]\\s+`([a-z0-9_]+)`\\s+-\\s+(.*)$")

// typeClaim finds the first type word a bullet's prose commits to.
var typeClaim = regexp.MustCompile(`(?i)\b(list|set|map|string|boolean|bool|number|integer)\b`)

// canonicalTypeWord maps the words prose uses onto the schema categories.
var canonicalTypeWord = map[string]string{
	"list": "list", "set": "set", "map": "map",
	"string": "string", "boolean": "bool", "bool": "bool",
	"number": "number", "integer": "number",
}

func TestDocsDoNotContradictSchemaTypes(t *testing.T) {
	types, err := providerAttributeTypes(context.Background())
	if err != nil {
		t.Fatalf("collecting schema types: %v", err)
	}

	var pages int
	for _, dir := range []string{"../docs/resources", "../docs/data-sources"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("reading %s: %v", dir, err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			content, err := os.ReadFile(path) //nolint:gosec // test-controlled path
			if err != nil {
				t.Fatalf("reading %s: %v", path, err)
			}
			pages++

			key := filepath.Base(dir) + ":jenkins_" + strings.TrimSuffix(entry.Name(), ".md")
			schemaTypes, ok := types[key]
			if !ok {
				t.Errorf("%s: no schema found for %q; the page would be checked against nothing", path, key)
				continue
			}
			for _, problem := range typeClaimMismatches(schemaTypes, string(content)) {
				t.Errorf("%s: %s", path, problem)
			}
		}
	}

	if pages == 0 {
		t.Fatal("no docs pages were scanned; the check would pass vacuously")
	}
}

// TestTypeClaimMismatches exercises the matcher itself, including the exact
// bullet that carried #196: the folder page called `permissions` "a list of
// strings" for three releases after #188 turned it into a Set.
func TestTypeClaimMismatches(t *testing.T) {
	schemaTypes := map[string]string{
		"permissions": "set",
		"template":    "string",
		"disabled":    "bool",
		"security":    "set",
	}

	tests := []struct {
		name        string
		content     string
		wantProblem bool
	}{
		{
			name:        "the #196 bullet",
			content:     "* `permissions` - (Required) A list of strings containing Jenkins permissions assigments to users and groups for the folder.",
			wantProblem: true,
		},
		{
			name:    "corrected bullet",
			content: "* `permissions` - (Required) A set of strings containing Jenkins permission assignments.",
		},
		{
			// What tfplugindocs emits. It has no " - " separator, so the
			// matcher never looks at generated content.
			name:    "generated bullet",
			content: "- `permissions` (Set of String) The Jenkins permission assignments granting access to this folder.",
		},
		{
			// The upgrade guide names the former type deliberately. It is not
			// an attribute bullet, so it must not be flagged.
			name:    "historical note",
			content: "`permissions` was a list before **v1.2.2** and is a set from v1.2.2 onward.",
		},
		{
			name:        "wrong scalar type",
			content:     "* `disabled` - (Optional) A string indicating whether the job is disabled.",
			wantProblem: true,
		},
		{
			name:    "prose with no type word",
			content: "* `template` - (Required) The configuration used to communicate with Jenkins.",
		},
		{
			name:    "attribute the schema does not have",
			content: "* `not_an_attribute` - (Optional) A list of things.",
		},
		{
			name:        "block described as a list",
			content:     "* `security` - (Optional) A list of blocks defining project-based authorization.",
			wantProblem: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			problems := typeClaimMismatches(schemaTypes, tt.content)
			if got := len(problems) > 0; got != tt.wantProblem {
				t.Fatalf("problems = %v, want a problem: %t", problems, tt.wantProblem)
			}
		})
	}
}

// typeClaimMismatches reports every hand-written attribute bullet in content
// whose stated type disagrees with schemaTypes. Attributes the schema does not
// know are ignored: a page may document a nested field or an example variable
// that is not a top-level attribute.
func typeClaimMismatches(schemaTypes map[string]string, content string) []string {
	var problems []string

	for _, m := range handwrittenAttrBullet.FindAllStringSubmatch(content, -1) {
		name, prose := m[1], m[2]

		want, known := schemaTypes[name]
		if !known {
			continue
		}

		claim := typeClaim.FindString(prose)
		if claim == "" {
			continue
		}
		got, ok := canonicalTypeWord[strings.ToLower(claim)]
		if !ok || got == want {
			continue
		}

		problems = append(problems, fmt.Sprintf(
			"`%s` is described as %q but the schema declares %s; "+
				"let tfplugindocs render the type via {{ .SchemaMarkdown }} instead of restating it",
			name, claim, want))
	}

	return problems
}

// providerAttributeTypes returns the schema category of every attribute, block
// and nested block attribute, keyed by "<kind>:<type name>" — a resource and a
// data source can share a type name (jenkins_folder is both), and their schemas
// differ, so the pages must be checked against the right one.
func providerAttributeTypes(ctx context.Context) (map[string]map[string]string, error) {
	out := map[string]map[string]string{}
	p := &JenkinsProvider{}

	for _, newResource := range p.Resources(ctx) {
		r := newResource()

		metaResp := &resource.MetadataResponse{}
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "jenkins"}, metaResp)

		schemaResp := &resource.SchemaResponse{}
		r.Schema(ctx, resource.SchemaRequest{}, schemaResp)
		if schemaResp.Diagnostics.HasError() {
			return nil, fmt.Errorf("%s schema: %v", metaResp.TypeName, schemaResp.Diagnostics)
		}

		top := map[string]attr.Type{}
		for name, a := range schemaResp.Schema.Attributes {
			top[name] = a.GetType()
		}
		for name, b := range schemaResp.Schema.Blocks {
			top[name] = b.Type()
		}
		out["resources:"+metaResp.TypeName] = flattenTypeNames(top)
	}

	for _, newDataSource := range p.DataSources(ctx) {
		d := newDataSource()

		metaResp := &datasource.MetadataResponse{}
		d.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "jenkins"}, metaResp)

		schemaResp := &datasource.SchemaResponse{}
		d.Schema(ctx, datasource.SchemaRequest{}, schemaResp)
		if schemaResp.Diagnostics.HasError() {
			return nil, fmt.Errorf("%s schema: %v", metaResp.TypeName, schemaResp.Diagnostics)
		}

		top := map[string]attr.Type{}
		for name, a := range schemaResp.Schema.Attributes {
			top[name] = a.GetType()
		}
		for name, b := range schemaResp.Schema.Blocks {
			top[name] = b.Type()
		}
		out["data-sources:"+metaResp.TypeName] = flattenTypeNames(top)
	}

	return out, nil
}

// flattenTypeNames maps every attribute and block name to its schema category,
// descending through collections and object types so nested block attributes
// are included: the prose refers to them by their plain name ("`permissions` -
// (Required) …" inside a security block). A top-level name always wins over a
// nested one.
func flattenTypeNames(top map[string]attr.Type) map[string]string {
	names := make(map[string]string, len(top))
	for name, t := range top {
		names[name] = typeCategory(t)
	}
	for _, t := range top {
		collectNestedTypeNames(names, t)
	}
	return names
}

// collectNestedTypeNames walks a type tree through the public attr interfaces —
// the schema's own nested-object accessors live in an internal package — and
// records any attribute name it has not seen.
func collectNestedTypeNames(names map[string]string, t attr.Type) {
	switch tt := t.(type) {
	case attr.TypeWithAttributeTypes:
		for name, nested := range tt.AttributeTypes() {
			if _, seen := names[name]; !seen {
				names[name] = typeCategory(nested)
			}
			collectNestedTypeNames(names, nested)
		}
	case attr.TypeWithElementType:
		collectNestedTypeNames(names, tt.ElementType())
	}
}

// typeCategory reduces a framework type to the word prose would use for it.
// Collection types are tested before the element-bearing interfaces they also
// satisfy, so a Set of String reports "set" rather than "string".
func typeCategory(t attr.Type) string {
	switch t.(type) {
	case basetypes.SetTypable:
		return "set"
	case basetypes.ListTypable:
		return "list"
	case basetypes.MapTypable:
		return "map"
	case basetypes.ObjectTypable:
		return "object"
	case basetypes.BoolTypable:
		return "bool"
	case basetypes.NumberTypable, basetypes.Int64Typable, basetypes.Float64Typable:
		return "number"
	case basetypes.StringTypable:
		return "string"
	default:
		return "unknown"
	}
}

// Compile-time assertions that the schema packages this test reasons about are
// the ones the provider uses.
var (
	_ rschema.Attribute  = rschema.StringAttribute{}
	_ dsschema.Attribute = dsschema.StringAttribute{}
)
