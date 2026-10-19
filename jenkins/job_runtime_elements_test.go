package jenkins

import (
	"context"
	"strings"
	"testing"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
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

func TestStripJobRuntimeElements(t *testing.T) {
	const nav = "io.jenkins.plugins.gitlabbranchsource.GitLabSCMNavigator"
	cases := map[string]struct {
		in, want string
	}{
		"filled set":           {orgFolderXML(filledProjects), orgFolderXML("")},
		"self-closing set":     {orgFolderXML(emptyProjects), orgFolderXML("")},
		"empty start/end pair": {orgFolderXML("\n      <navigatorProjects></navigatorProjects>"), orgFolderXML("")},
		"CRLF line endings": {
			"<" + nav + ">\r\n  <a/>\r\n  <navigatorProjects/>\r\n</" + nav + ">",
			"<" + nav + ">\r\n  <a/>\r\n</" + nav + ">",
		},
		"on the line of another element": {
			"<" + nav + "><a/><navigatorProjects><string>p</string></navigatorProjects><b/></" + nav + ">",
			"<" + nav + "><a/><b/></" + nav + ">",
		},
		"same name under another parent": {
			"<other.Navigator><navigatorProjects/></other.Navigator>",
			"<other.Navigator><navigatorProjects/></other.Navigator>",
		},
		"deeper than a direct child": {
			"<" + nav + "><x><navigatorProjects/></x></" + nav + ">",
			"<" + nav + "><x><navigatorProjects/></x></" + nav + ">",
		},
		"in CDATA": {
			"<" + nav + "><x><![CDATA[<navigatorProjects/>]]></x></" + nav + ">",
			"<" + nav + "><x><![CDATA[<navigatorProjects/>]]></x></" + nav + ">",
		},
		"not well-formed": {
			"<" + nav + "><navigatorProjects>",
			"<" + nav + "><navigatorProjects>",
		},
		"no runtime element": {covNJobXML, covNJobXML},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := stripJobRuntimeElements(tc.in); got != tc.want {
				t.Errorf("stripJobRuntimeElements:\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}

// TestTemplatePlanModifierIgnoresRuntimeElements: a scan refilling the set
// on the controller must not plan a template change, and a real change next
// to it still must.
func TestTemplatePlanModifierIgnoresRuntimeElements(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		config, state string
		keepState     bool
	}{
		"set filled by a scan":              {orgFolderXML(emptyProjects), orgFolderXML(filledProjects), true},
		"state stored without the set":      {orgFolderXML(emptyProjects), orgFolderXML(""), true},
		"template copied with a stale set":  {orgFolderXML(filledProjects), orgFolderXML(""), true},
		"real change next to the set":       {strings.Replace(orgFolderXML(emptyProjects), "reader", "other", 1), orgFolderXML(filledProjects), false},
		"template drops a configured child": {strings.Replace(orgFolderXML(emptyProjects), "<isGroup>true</isGroup>", "", 1), orgFolderXML(filledProjects), false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg, state := types.StringValue(tc.config), types.StringValue(tc.state)
			req := planmodifier.StringRequest{
				Path:        path.Root("template"),
				ConfigValue: cfg,
				StateValue:  state,
				Plan:        buildDisabledPlan(t, ctx, nil),
			}
			resp := &planmodifier.StringResponse{PlanValue: cfg}
			templatePlanModifier{}.PlanModifyString(ctx, req, resp)
			if got := resp.PlanValue.Equal(state); got != tc.keepState {
				t.Errorf("plan kept the state = %v, want %v", got, tc.keepState)
			}
		})
	}
}

func TestJobReadLeavesOutRuntimeElements(t *testing.T) {
	ctx := context.Background()
	live := covcLiveJob(t, "/job/apps", orgFolderXML(filledProjects))
	r := &jobResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockGetJob: func(context.Context, string, ...string) (*jenkins.Job, error) { return live, nil },
	}}}
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	st := tfsdk.State{Schema: sr.Schema}
	if d := st.Set(ctx, &jobResourceModel{
		Name: types.StringValue("apps"), Folder: types.StringNull(),
		Template: types.StringValue(orgFolderXML(emptyProjects)), Disabled: types.BoolNull(),
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
	if want := orgFolderXML(""); got.Template.ValueString() != want {
		t.Errorf("stored template:\n got: %q\nwant: %q", got.Template.ValueString(), want)
	}
}
