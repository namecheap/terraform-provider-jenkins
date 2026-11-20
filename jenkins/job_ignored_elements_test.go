package jenkins

import (
	"context"
	"strings"
	"testing"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// orgFolderXML is a GitLab organization folder as Jenkins saves it after a
// scan, with projects standing in for the set the scan filled.
func orgFolderXML(projects string) string {
	return `<?xml version='1.1' encoding='UTF-8'?>
<jenkins.branch.OrganizationFolder plugin="branch-api@2.1208.vf528356feca_4">
  <description>Team jobs</description>
  <navigators>
    <io.jenkins.plugins.gitlabbranchsource.GitLabSCMNavigator plugin="gitlab-branch-source@718.v40b_3d0cc8d4b_">
      <projectOwner>apps/team</projectOwner>
      <serverName>gitlab</serverName>
      <credentialsId>reader</credentialsId>
      <traits/>` + projects + `
      <isGroup>true</isGroup>
      <wantSubGroupProjects>true</wantSubGroupProjects>
    </io.jenkins.plugins.gitlabbranchsource.GitLabSCMNavigator>
  </navigators>
</jenkins.branch.OrganizationFolder>`
}

const (
	filledProjects = `
      <navigatorProjects>
        <string>apps/team/one</string>
        <string>apps/team/two</string>
      </navigatorProjects>`
	emptyProjects = `
      <navigatorProjects/>`
)

// navProjectsPath is the ignored_elements entry for the projects a GitLab
// organization folder scan records.
const navProjectsPath = "/jenkins.branch.OrganizationFolder/navigators/io.jenkins.plugins.gitlabbranchsource.GitLabSCMNavigator/navigatorProjects"

func TestStripIgnoredElements(t *testing.T) {
	const nav = "io.jenkins.plugins.gitlabbranchsource.GitLabSCMNavigator"
	navPath := []string{"/" + nav + "/navigatorProjects"}
	org := []string{navProjectsPath}
	cases := map[string]struct {
		in, want string
		paths    []string
	}{
		"filled set":           {orgFolderXML(filledProjects), orgFolderXML(""), org},
		"self-closing set":     {orgFolderXML(emptyProjects), orgFolderXML(""), org},
		"empty start/end pair": {orgFolderXML("\n      <navigatorProjects></navigatorProjects>"), orgFolderXML(""), org},
		"wildcard segment":     {orgFolderXML(filledProjects), orgFolderXML(""), []string{"/*/navigators/*/navigatorProjects"}},
		"no paths":             {orgFolderXML(filledProjects), orgFolderXML(filledProjects), nil},
		"CRLF line endings": {
			"<" + nav + ">\r\n  <a/>\r\n  <navigatorProjects/>\r\n</" + nav + ">",
			"<" + nav + ">\r\n  <a/>\r\n</" + nav + ">",
			navPath,
		},
		"on the line of another element": {
			"<" + nav + "><a/><navigatorProjects><string>p</string></navigatorProjects><b/></" + nav + ">",
			"<" + nav + "><a/><b/></" + nav + ">",
			navPath,
		},
		"no line break before": {"<nav><a/>  <navigatorProjects/></nav>", "<nav><a/>  </nav>", []string{"/nav/navigatorProjects"}},
		"same name under another parent": {
			"<other.Navigator><navigatorProjects/></other.Navigator>",
			"<other.Navigator><navigatorProjects/></other.Navigator>",
			navPath,
		},
		"deeper than the path": {
			"<" + nav + "><x><navigatorProjects/></x></" + nav + ">",
			"<" + nav + "><x><navigatorProjects/></x></" + nav + ">",
			navPath,
		},
		"in CDATA": {
			"<" + nav + "><x><![CDATA[<navigatorProjects/>]]></x></" + nav + ">",
			"<" + nav + "><x><![CDATA[<navigatorProjects/>]]></x></" + nav + ">",
			[]string{"/" + nav + "/x/navigatorProjects"},
		},
		"not well-formed": {
			"<" + nav + "><navigatorProjects>",
			"<" + nav + "><navigatorProjects>",
			navPath,
		},
		// The element closes before the syntax error: stripping it would
		// make the comparison looser than without ignored_elements.
		"element before a parse error": {"<nav><navigatorProjects/><a></b></nav>", "<nav><navigatorProjects/><a></b></nav>", []string{"/nav/navigatorProjects"}},
		"later ?> on the declaration line": {
			"<?xml version='1.1'?><nav><?pi x?><navigatorProjects/></nav>",
			"<?xml version='1.1'?><nav><?pi x?></nav>",
			[]string{"/nav/navigatorProjects"},
		},
		// XML 1.1, which Jenkins serves, allows references to control
		// characters that encoding/xml rejects.
		"control character reference": {"<nav><d>a&#x1b;b</d><navigatorProjects/></nav>", "<nav><d>a&#x1b;b</d></nav>", []string{"/nav/navigatorProjects"}},
		"root-only path is skipped":   {"<nav/>", "<nav/>", []string{"/nav"}},
		// A template file saved with one, as some Windows editors do.
		"byte order mark before the declaration": {
			"\uFEFF<?xml version='1.1'?><nav><navigatorProjects/></nav>",
			"\uFEFF<?xml version='1.1'?><nav></nav>",
			[]string{"/nav/navigatorProjects"},
		},
		"no ignored element": {covNJobXML, covNJobXML, org},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := stripIgnoredElements(tc.in, tc.paths); got != tc.want {
				t.Errorf("stripIgnoredElements:\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

func TestParseElementPath(t *testing.T) {
	valid := map[string]elementPath{
		"/a/b":                              {"a", "b"},
		"/*/navigators/*/navigatorProjects": {"*", "navigators", "*", "navigatorProjects"},
		"/a/FolderCredentialsProvider_-FolderCredentialsProperty": {"a", "FolderCredentialsProvider_-FolderCredentialsProperty"},
	}
	for in, want := range valid {
		got, err := parseElementPath(in)
		if err != nil {
			t.Errorf("parseElementPath(%q): %v", in, err)
			continue
		}
		if strings.Join(got, "/") != strings.Join(want, "/") {
			t.Errorf("parseElementPath(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"", "a/b", "/a", "/a//b", "/a/b/", "/a/**", "/a/*", "/*/*", "/a/b[1]", "/a/@x", "//a/b", "/a/1b", "/a/b c"} {
		if _, err := parseElementPath(in); err == nil {
			t.Errorf("parseElementPath(%q) succeeded, want an error", in)
		}
	}
}

func TestElementPathMatches(t *testing.T) {
	p := elementPath{"*", "navigators", "*", "navigatorProjects"}
	if !p.matches([]string{"jenkins.branch.OrganizationFolder", "navigators", "x.Nav", "navigatorProjects"}) {
		t.Error("wildcards did not match")
	}
	for _, stack := range [][]string{
		{"r", "navigators", "x.Nav"},
		{"r", "navigators", "x.Nav", "navigatorProjects", "string"},
		{"r", "other", "x.Nav", "navigatorProjects"},
	} {
		if p.matches(stack) {
			t.Errorf("matched %q", stack)
		}
	}
}

// ignoredSet returns paths as an ignored_elements value.
func ignoredSet(paths ...string) tftypes.Value {
	elems := make([]tftypes.Value, 0, len(paths))
	for _, p := range paths {
		elems = append(elems, tftypes.NewValue(tftypes.String, p))
	}
	return tftypes.NewValue(ignoredElementsTFType, elems)
}

// TestTemplatePlanModifierIgnoredElements: a scan refilling the set on the
// controller must not plan a template change while ignored_elements names
// it, and a real change next to it still must.
func TestTemplatePlanModifierIgnoredElements(t *testing.T) {
	ctx := context.Background()
	org := ignoredSet(navProjectsPath)
	cases := map[string]struct {
		config, state string
		ignored       tftypes.Value
		keepState     bool
	}{
		"set filled by a scan": {orgFolderXML(emptyProjects), orgFolderXML(filledProjects), org, true},
		// State right after a Create from a template without the set.
		"state stored without the set":      {orgFolderXML(emptyProjects), orgFolderXML(""), org, true},
		"template copied with a stale set":  {orgFolderXML(filledProjects), orgFolderXML(""), org, true},
		"real change next to the set":       {strings.Replace(orgFolderXML(emptyProjects), "reader", "other", 1), orgFolderXML(filledProjects), org, false},
		"template drops a configured child": {strings.Replace(orgFolderXML(emptyProjects), "<isGroup>true</isGroup>", "", 1), orgFolderXML(filledProjects), org, false},
		// Nothing is ignored by default.
		"no ignored_elements": {orgFolderXML(emptyProjects), orgFolderXML(filledProjects), tftypes.NewValue(ignoredElementsTFType, nil), false},
		// The element is managed again once its entry is removed.
		"entry removed, template keeps it": {orgFolderXML(emptyProjects), orgFolderXML(filledProjects), ignoredSet(), false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, state := types.StringValue(tc.config), types.StringValue(tc.state)
			req := planmodifier.StringRequest{
				Path:        path.Root("template"),
				ConfigValue: cfg,
				StateValue:  state,
				Plan:        buildTemplatePlan(t, ctx, nil, tc.ignored),
			}
			resp := &planmodifier.StringResponse{PlanValue: cfg}
			templatePlanModifier{}.PlanModifyString(ctx, req, resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("PlanModifyString: %v", resp.Diagnostics)
			}
			if got := resp.PlanValue.Equal(state); got != tc.keepState {
				t.Errorf("plan kept the state = %v, want %v", got, tc.keepState)
			}
		})
	}
}

// TestTemplatePlanModifierUnknownIgnoredElements: with ignored_elements built
// from a value known only at apply, the plan cannot tell whether a difference
// is in an ignored element. Planning the template and then, once the list is
// known, the state would make Terraform reject the apply, so that is an
// error; a template equal to the state stays equal whatever the list.
func TestTemplatePlanModifierUnknownIgnoredElements(t *testing.T) {
	ctx := context.Background()
	unknown := tftypes.NewValue(ignoredElementsTFType, tftypes.UnknownValue)
	partlyUnknown := tftypes.NewValue(ignoredElementsTFType, []tftypes.Value{
		tftypes.NewValue(tftypes.String, navProjectsPath),
		tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
	})
	cases := map[string]struct {
		config, state string
		ignored       tftypes.Value
		wantErr       bool
	}{
		"unknown, template differs":        {orgFolderXML(emptyProjects), orgFolderXML(filledProjects), unknown, true},
		"partly unknown, template differs": {orgFolderXML(emptyProjects), orgFolderXML(filledProjects), partlyUnknown, true},
		"unknown, template unchanged":      {orgFolderXML(filledProjects), orgFolderXML(filledProjects), unknown, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, state := types.StringValue(tc.config), types.StringValue(tc.state)
			req := planmodifier.StringRequest{
				Path:        path.Root("template"),
				ConfigValue: cfg,
				StateValue:  state,
				Plan:        buildTemplatePlan(t, ctx, nil, tc.ignored),
			}
			resp := &planmodifier.StringResponse{PlanValue: cfg}
			templatePlanModifier{}.PlanModifyString(ctx, req, resp)
			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("PlanModifyString diagnostics = %v, want error %v", resp.Diagnostics, tc.wantErr)
			}
			if !tc.wantErr && !resp.PlanValue.Equal(state) {
				t.Errorf("plan did not keep the state")
			}
		})
	}
}

// TestJobReadIgnoredElements: Read stores the rest of config.xml as Jenkins
// has it, with the copy of each ignored element the template applied last
// had, so a later plan shows no change in an element the update will not
// change.
func TestJobReadIgnoredElements(t *testing.T) {
	ctx := context.Background()
	drifted := func(projects string) string {
		return strings.Replace(orgFolderXML(projects), "Team jobs", "Edited in Jenkins", 1)
	}
	cases := map[string]struct {
		state   types.String
		ignored []string
		want    string
	}{
		"listed":                   {types.StringValue(orgFolderXML(emptyProjects)), []string{navProjectsPath}, drifted(emptyProjects)},
		"unlisted":                 {types.StringValue(orgFolderXML(emptyProjects)), nil, drifted(filledProjects)},
		"template without the set": {types.StringValue(orgFolderXML("")), []string{navProjectsPath}, drifted(filledProjects)},
		"no template yet (import)": {types.StringNull(), []string{navProjectsPath}, drifted(filledProjects)},
		"state is not well-formed": {types.StringValue("<r>"), []string{navProjectsPath}, drifted(filledProjects)},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			live := covcLiveJob(t, "/job/apps", drifted(filledProjects))
			r := &jobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
				mockGetJob: func(context.Context, string, ...string) (*jenkins.Job, error) { return live, nil },
			}}}
			var sr resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &sr)
			st := tfsdk.State{Schema: sr.Schema}
			if d := st.Set(ctx, &jobResourceModel{
				Name: types.StringValue("apps"), Folder: types.StringNull(),
				Template: tc.state, Disabled: types.BoolNull(),
				IgnoredElements: tc.ignored,
			}); d.HasError() {
				t.Fatalf("state: %v", d)
			}
			resp := resource.ReadResponse{State: st}
			r.Read(ctx, resource.ReadRequest{State: st}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Read: %v", resp.Diagnostics)
			}
			var got jobResourceModel
			resp.State.Get(ctx, &got)
			if got.Template.ValueString() != tc.want {
				t.Errorf("stored template:\n got: %q\nwant: %q", got.Template.ValueString(), tc.want)
			}
			if strings.Join(got.IgnoredElements, ",") != strings.Join(tc.ignored, ",") {
				t.Errorf("ignored_elements = %q, want %q", got.IgnoredElements, tc.ignored)
			}
		})
	}
}

func TestIgnoredElementPathValidator(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		value   types.String
		wantErr bool
	}{
		"path":          {types.StringValue(navProjectsPath), false},
		"not a path":    {types.StringValue("navigatorProjects"), true},
		"wildcard last": {types.StringValue("/jenkins.branch.OrganizationFolder/*"), true},
		// A null entry cannot be read into the resource model at apply.
		"null":    {types.StringNull(), true},
		"unknown": {types.StringUnknown(), false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			set := types.SetValueMust(types.StringType, []attr.Value{tc.value})
			resp := &validator.SetResponse{}
			setvalidator.ValueStringsAre(ignoredElementPathValidator{}).ValidateSet(ctx,
				validator.SetRequest{Path: path.Root("ignored_elements"), ConfigValue: set}, resp)
			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Errorf("error = %v, want %v", resp.Diagnostics, tc.wantErr)
			}
		})
	}
}

// Read merges into Jenkins' own document, which is never posted: a parent
// that cannot be paired is kept as Jenkins has it, without an error.
func TestWithStateIgnoredElements_UnpairedParents(t *testing.T) {
	config := "<r><ns><n><id>1</id><np><s>a</s></np></n><n><id>2</id></n></ns></r>"
	state := "<r><ns><n><id>1</id><np/></n></ns></r>"
	if got := withStateIgnoredElements(config, state, []string{"/r/ns/n/np"}); got != config {
		t.Errorf("withStateIgnoredElements:\n got: %q\nwant: %q", got, config)
	}
}

func TestWithLiveIgnoredElements(t *testing.T) {
	const nav = "io.jenkins.plugins.gitlabbranchsource.GitLabSCMNavigator"
	org := []string{navProjectsPath}
	liveSet := strings.TrimSpace(filledProjects)
	cases := map[string]struct {
		template, live, want string
		paths                []string
		wantErr              bool
	}{
		"template keeps an empty set": {orgFolderXML(emptyProjects), orgFolderXML(filledProjects), orgFolderXML(filledProjects), org, false},
		"template without the element": {
			orgFolderXML(""), orgFolderXML(filledProjects),
			strings.Replace(orgFolderXML(""), "</"+nav+">", liveSet+"</"+nav+">", 1),
			org, false,
		},
		"self-closing parent":          {"<r><p/></r>", "<r><p><x>1</x></p></r>", "<r><p><x>1</x></p></r>", []string{"/r/p/x"}, false},
		"self-closing prefixed parent": {"<r><y:p a='1/'/></r>", "<r><y:p><x>1</x></y:p></r>", "<r><y:p a='1/'><x>1</x></y:p></r>", []string{"/r/p/x"}, false},
		// A navigator Jenkins has not saved yet: its set must not go.
		"live has no copy": {orgFolderXML(emptyProjects), orgFolderXML(""), orgFolderXML(emptyProjects), org, false},
		"navigators of the same type unchanged": {
			"<r><ns><n><id>1</id><np/></n><n><id>2</id><np/></n></ns></r>",
			"<r><ns><n><id>1</id><np><s>a</s></np></n><n><id>2</id><np><s>b</s></np></n></ns></r>",
			"<r><ns><n><id>1</id><np><s>a</s></np></n><n><id>2</id><np><s>b</s></np></n></ns></r>",
			[]string{"/r/ns/n/np"}, false,
		},
		// Positions no longer pair the same navigators: whichever way one
		// was added or removed, the template's copies are posted.
		"navigator of the same type added after": {
			"<r><ns><n><id>1</id><np/></n><n><id>2</id><np/></n></ns></r>",
			"<r><ns><n><id>1</id><np><s>a</s></np></n></ns></r>",
			"<r><ns><n><id>1</id><np/></n><n><id>2</id><np/></n></ns></r>",
			[]string{"/r/ns/n/np"}, false,
		},
		"navigator of the same type added before": {
			"<r><ns><n><id>new</id><np/></n><n><id>old</id><np/></n></ns></r>",
			"<r><ns><n><id>old</id><np><s>x</s></np></n></ns></r>",
			"<r><ns><n><id>new</id><np/></n><n><id>old</id><np/></n></ns></r>",
			[]string{"/r/ns/n/np"}, false,
		},
		"navigator of the same type removed before": {
			"<r><ns><n><id>2</id><np/></n></ns></r>",
			"<r><ns><n><id>1</id><np><s>a</s></np></n><n><id>2</id><np><s>b</s></np></n></ns></r>",
			"<r><ns><n><id>2</id><np/></n></ns></r>",
			[]string{"/r/ns/n/np"}, false,
		},
		// Kept as written, these navigators would be posted without the
		// element Jenkins has in them.
		"navigator of the same type added, template without the element": {
			"<r><ns><n><id>1</id></n><n><id>2</id></n></ns></r>",
			"<r><ns><n><id>1</id><np><s>a</s></np></n></ns></r>",
			"", []string{"/r/ns/n/np"}, true,
		},
		"navigator of the same type added without the element": {
			"<r><ns><n><id>1</id><np/></n><n><id>2</id></n></ns></r>",
			"<r><ns><n><id>1</id><np><s>a</s></np></n></ns></r>",
			"", []string{"/r/ns/n/np"}, true,
		},
		"navigator of the same type removed, template without the element": {
			"<r><ns><n><id>2</id></n></ns></r>",
			"<r><ns><n><id>1</id><np><s>a</s></np></n><n><id>2</id><np><s>b</s></np></n></ns></r>",
			"", []string{"/r/ns/n/np"}, true,
		},
		"navigator added under a wildcard, template without the element": {
			"<r><ns><n><id>1</id></n><n><id>2</id></n></ns></r>",
			"<r><ns><n><id>1</id><np><s>a</s></np></n></ns></r>",
			"", []string{"/r/*/n/np"}, true,
		},
		// Jenkins has no such element in any of them: nothing is lost.
		"navigator of the same type added, no element in Jenkins": {
			"<r><ns><n><id>1</id></n><n><id>2</id></n></ns></r>",
			"<r><ns><n><id>1</id></n></ns></r>",
			"<r><ns><n><id>1</id></n><n><id>2</id></n></ns></r>",
			[]string{"/r/ns/n/np"}, false,
		},
		// Parents are paired by position alone: nothing in a document says
		// which navigator is which.
		"navigator replaced by one of the same type": {
			"<r><ns><n><id>new</id><np/></n></ns></r>",
			"<r><ns><n><id>old</id><np><s>x</s></np></n></ns></r>",
			"<r><ns><n><id>new</id><np><s>x</s></np></n></ns></r>",
			[]string{"/r/ns/n/np"}, false,
		},
		"navigators of the same type reordered": {
			"<r><ns><n><id>2</id><np/></n><n><id>1</id><np/></n></ns></r>",
			"<r><ns><n><id>1</id><np><s>a</s></np></n><n><id>2</id><np><s>b</s></np></n></ns></r>",
			"<r><ns><n><id>2</id><np><s>a</s></np></n><n><id>1</id><np><s>b</s></np></n></ns></r>",
			[]string{"/r/ns/n/np"}, false,
		},
		"navigator of another type added": {
			"<r><ns><m><np/></m><n><np/></n></ns></r>",
			"<r><ns><n><np><s>a</s></np></n></ns></r>",
			"<r><ns><m><np/></m><n><np><s>a</s></np></n></ns></r>",
			[]string{"/r/ns/*/np"}, false,
		},
		"template removed the parent": {
			"<r><navigators/></r>",
			"<r><navigators><n><navigatorProjects><string>a</string></navigatorProjects></n></navigators></r>",
			"<r><navigators/></r>",
			[]string{"/r/navigators/n/navigatorProjects"}, false,
		},
		// Each name is placed on its own: Jenkins having an <a> says nothing
		// about the template's <b>.
		"two entries under one parent":     {"<r><p><a>t</a><b>t</b></p></r>", "<r><p><a>l</a></p></r>", "<r><p><a>l</a><b>t</b></p></r>", []string{"/r/p/a", "/r/p/b"}, false},
		"several matches under one parent": {"<r><p><a>t</a><b>t</b></p></r>", "<r><p><a>l</a><c>l</c></p></r>", "<r><p><a>l</a><b>t</b><c>l</c></p></r>", []string{"/r/p/a", "/r/p/c"}, false},
		"repeated element":                 {"<r><p><s>1</s><s>2</s></p></r>", "<r><p><s>3</s><s>4</s><s>5</s></p></r>", "<r><p><s>3</s><s>4</s><s>5</s></p></r>", []string{"/r/p/s"}, false},
		"control character reference":      {"<r><p/></r>", "<r><p><x>a&#x1b;b</x></p></r>", "<r><p><x>a&#x1b;b</x></p></r>", []string{"/r/p/x"}, false},
		"template with a byte order mark": {
			"\uFEFF<?xml version='1.1'?><r><p/></r>", "<?xml version='1.1'?><r><p><x>1</x></p></r>",
			"\uFEFF<?xml version='1.1'?><r><p><x>1</x></p></r>", []string{"/r/p/x"}, false,
		},
		"no paths":                 {"<r/>", "<r>", "<r/>", nil, false},
		"live not well-formed":     {"<r><p/></r>", "<r><p>", "", []string{"/r/p/x"}, true},
		"template not well-formed": {"<r></x>", "<r><p><x>1</x></p></r>", "", []string{"/r/p/x"}, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := withLiveIgnoredElements(tc.template, tc.live, tc.paths)
			if (err != nil) != tc.wantErr {
				t.Fatalf("withLiveIgnoredElements error = %v, want error %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("withLiveIgnoredElements:\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// TestJobResourceUpdateKeepsIgnoredElements: Update posts the copy Jenkins
// has of each ignored element, also when the planned template is the
// refreshed state (a disabled-only change, ignore_changes) and a scan changed
// the element since.
func TestJobResourceUpdateKeepsIgnoredElements(t *testing.T) {
	ctx := context.Background()
	const folder = "com.cloudbees.hudson.plugins.folder.Folder"
	tests := []struct {
		name            string
		created, server string
		ignored         []string
		// rescanned, when set, replaces the server's config.xml after the
		// refresh.
		rescanned string
		// dropGet makes config.xml unreadable after the refresh.
		dropGet      bool
		wantErr      bool
		wantContains []string
		wantStores   int
	}{
		{
			name:         "planned template from state",
			created:      orgFolderXML(emptyProjects),
			server:       orgFolderXML(filledProjects),
			ignored:      []string{navProjectsPath},
			rescanned:    orgFolderXML("\n      <navigatorProjects><string>apps/team/three</string></navigatorProjects>"),
			wantContains: []string{"<string>apps/team/three</string>"},
		},
		{
			// Both mechanisms would add the store: only one may.
			name:         "ignored path is the folder credentials store",
			created:      redactFolderNoCredentials,
			server:       redactFolderWithCredentials(redactCipherToken),
			ignored:      []string{"/" + folder + "/properties/" + folderCredentialsPropertyElementName},
			wantContains: []string{redactCipherToken},
			wantStores:   1,
		},
		{
			// XML 1.1 lets Jenkins write a control character as a reference;
			// the ignored description brings it into the posted template.
			name:    "ignored element with a control character, unmanaged store",
			created: redactFolderNoCredentials,
			server: strings.Replace(redactFolderWithCredentials(redactCipherToken),
				"<description>team folder</description>", "<description>team &#x1b;[1mfolder</description>", 1),
			ignored:      []string{"/" + folder + "/description"},
			wantContains: []string{"<description>team &#x1b;[1mfolder</description>", redactCipherToken},
			wantStores:   1,
		},
		{
			// The template manages its store, so only ignored_elements needs
			// the live config.xml.
			name:    "config.xml unreadable",
			created: redactFolderWithCredentials(redactCipherToken),
			server:  redactFolderWithCredentials(redactCipherToken),
			ignored: []string{"/" + folder + "/description"},
			dropGet: true,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &redactFolderServer{config: tt.server}
			job := srv.job(t, "/job/team")
			r := &jobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
				mockCreateJobInFolder: func(context.Context, string, string, ...string) (*jenkins.Job, error) { return job, nil },
				mockGetJob:            func(context.Context, string, ...string) (*jenkins.Job, error) { return job, nil },
			}}}
			var sr resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &sr)
			model := func(template string, disabled types.Bool, ignored []string) *jobResourceModel {
				return &jobResourceModel{
					ID: types.StringValue("/job/team"), Name: types.StringValue("team"), Folder: types.StringNull(),
					Template: types.StringValue(template), Disabled: disabled, IgnoredElements: ignored,
				}
			}

			plan := tfsdk.Plan{Schema: sr.Schema}
			if d := plan.Set(ctx, model(tt.created, types.BoolNull(), tt.ignored)); d.HasError() {
				t.Fatalf("plan.Set: %v", d)
			}
			cResp := resource.CreateResponse{State: tfsdk.State{Schema: sr.Schema}}
			initPrivateState(t, &cResp)
			r.Create(ctx, resource.CreateRequest{Plan: plan}, &cResp)
			if cResp.Diagnostics.HasError() {
				t.Fatalf("Create: %v", cResp.Diagnostics)
			}
			srv.posted = nil
			rResp := resource.ReadResponse{State: cResp.State}
			r.Read(ctx, resource.ReadRequest{State: cResp.State, Private: cResp.Private}, &rResp)
			if rResp.Diagnostics.HasError() {
				t.Fatalf("Read: %v", rResp.Diagnostics)
			}
			var refreshed jobResourceModel
			if d := rResp.State.Get(ctx, &refreshed); d.HasError() {
				t.Fatalf("state.Get: %v", d)
			}

			srv.mu.Lock()
			srv.dropGet = tt.dropGet
			if tt.rescanned != "" {
				srv.config = tt.rescanned
			}
			srv.mu.Unlock()
			if d := plan.Set(ctx, model(refreshed.Template.ValueString(), types.BoolValue(false), tt.ignored)); d.HasError() {
				t.Fatalf("plan.Set: %v", d)
			}
			uResp := resource.UpdateResponse{State: tfsdk.State{Schema: sr.Schema}, Private: cResp.Private}
			r.Update(ctx, resource.UpdateRequest{Plan: plan, State: rResp.State, Private: cResp.Private}, &uResp)
			if uResp.Diagnostics.HasError() != tt.wantErr {
				t.Fatalf("Update diagnostics = %v, want error %v", uResp.Diagnostics, tt.wantErr)
			}
			if tt.wantErr {
				if len(srv.posted) != 0 {
					t.Errorf("Update posted config.xml %d times, want 0", len(srv.posted))
				}
				return
			}
			if len(srv.posted) != 1 {
				t.Fatalf("Update posted config.xml %d times, want 1", len(srv.posted))
			}
			posted := srv.posted[0]
			for _, want := range tt.wantContains {
				if !strings.Contains(posted, want) {
					t.Errorf("posted config.xml is missing %q:\n%s", want, posted)
				}
			}
			if n := strings.Count(posted, "</"+folderCredentialsPropertyElementName); n != tt.wantStores {
				t.Errorf("posted config.xml has %d folder credentials stores, want %d:\n%s", n, tt.wantStores, posted)
			}
		})
	}
}
