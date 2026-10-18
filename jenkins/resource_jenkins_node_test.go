package jenkins

import (
	"context"
	"encoding/xml"
	"fmt"
	"regexp"
	"strings"
	"testing"

	jenkins "github.com/bndr/gojenkins"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccJenkinsNode_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsNodeDestroy,
		Steps: []resource.TestStep{
			{
				Config: `
				resource jenkins_node foo {
				  name          = "tf-acc-node"
				  num_executors = 2
				  remote_fs     = "/home/jenkins/agent"
				  labels        = "linux docker"
				  description   = "managed by terraform"
				}`,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsNodeExists("jenkins_node.foo"),
					resource.TestCheckResourceAttr("jenkins_node.foo", "id", "tf-acc-node"),
					resource.TestCheckResourceAttr("jenkins_node.foo", "num_executors", "2"),
					resource.TestCheckResourceAttr("jenkins_node.foo", "remote_fs", "/home/jenkins/agent"),
					resource.TestCheckResourceAttr("jenkins_node.foo", "labels", "linux docker"),
					resource.TestCheckResourceAttr("jenkins_node.foo", "description", "managed by terraform"),
					resource.TestCheckResourceAttr("jenkins_node.foo", "mode", "NORMAL"),
				),
			},
			{
				ResourceName:      "jenkins_node.foo",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccJenkinsNode_exclusive(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsNodeDestroy,
		Steps: []resource.TestStep{
			{
				Config: `
				resource jenkins_node foo {
				  name      = "tf-acc-node-exclusive"
				  remote_fs = "/home/jenkins/agent"
				  labels    = "team-a"
				  mode      = "EXCLUSIVE"
				}

				data jenkins_node foo {
				  name = jenkins_node.foo.name
				}`,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsNodeExists("jenkins_node.foo"),
					resource.TestCheckResourceAttr("jenkins_node.foo", "mode", "EXCLUSIVE"),
					resource.TestCheckResourceAttr("data.jenkins_node.foo", "mode", "EXCLUSIVE"),
				),
			},
			{
				ResourceName:      "jenkins_node.foo",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// TestAccJenkinsNode_inPlace walks a node through every attribute change
// short of a rename and asserts each one plans an update, not a replacement,
// and that Jenkins kept what the plan says. A node property added out of band
// must survive the updates.
func TestAccJenkinsNode_inPlace(t *testing.T) {
	const name = "tf-acc-node-in-place"
	restricted := `
	resource jenkins_node foo {
	  name       = "` + name + `"
	  remote_fs  = "/home/jenkins/agent"
	  labels     = "team-a"
	  web_socket = true

	  job_restriction = {
	    regex = "^team-a/.*"
	  }
	}`
	changed := `
	resource jenkins_node foo {
	  name          = "` + name + `"
	  num_executors = 2
	  description   = "updated in place"
	  remote_fs     = "/srv/agent"
	  labels        = "team-a linux"
	  mode          = "EXCLUSIVE"

	  job_restriction = {
	    regex            = "^(build|deploy)$"
	    check_short_name = true
	  }
	}`
	unrestricted := `
	resource jenkins_node foo {
	  name          = "` + name + `"
	  num_executors = 2
	  description   = "updated in place"
	  remote_fs     = "/srv/agent"
	  labels        = "team-a linux"
	  mode          = "EXCLUSIVE"
	}`
	inPlace := resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
		plancheck.ExpectResourceAction("jenkins_node.foo", plancheck.ResourceActionUpdate),
	}}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsNodeDestroy,
		Steps: []resource.TestStep{
			{
				Config: restricted,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_node.foo", "web_socket", "true"),
					resource.TestCheckResourceAttr("jenkins_node.foo", "job_restriction.regex", "^team-a/.*"),
					resource.TestCheckResourceAttr("jenkins_node.foo", "job_restriction.check_short_name", "false"),
					testAccCheckJenkinsNodeConfig(name, func(cfg nodeConfig) error {
						if r := cfg.jobRestriction(); r == nil || *r != (jobRestriction{Regex: "^team-a/.*"}) {
							return fmt.Errorf("job restriction = %v", r)
						}
						if !cfg.webSocket() {
							return fmt.Errorf("webSocket = false")
						}
						return nil
					}),
				),
			},
			{
				PreConfig:        func() { testAccAddNodeEnvProperty(t, name) },
				Config:           changed,
				ConfigPlanChecks: inPlace,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_node.foo", "num_executors", "2"),
					resource.TestCheckResourceAttr("jenkins_node.foo", "description", "updated in place"),
					resource.TestCheckResourceAttr("jenkins_node.foo", "remote_fs", "/srv/agent"),
					resource.TestCheckResourceAttr("jenkins_node.foo", "labels", "team-a linux"),
					resource.TestCheckResourceAttr("jenkins_node.foo", "mode", "EXCLUSIVE"),
					resource.TestCheckResourceAttr("jenkins_node.foo", "web_socket", "false"),
					resource.TestCheckResourceAttr("jenkins_node.foo", "job_restriction.regex", "^(build|deploy)$"),
					resource.TestCheckResourceAttr("jenkins_node.foo", "job_restriction.check_short_name", "true"),
					testAccCheckJenkinsNodeConfig(name, func(cfg nodeConfig) error {
						if r := cfg.jobRestriction(); r == nil || *r != (jobRestriction{Regex: "^(build|deploy)$", CheckShortName: true}) {
							return fmt.Errorf("job restriction = %v", r)
						}
						if cfg.webSocket() || cfg.NumExecutors != 2 || cfg.RemoteFS != "/srv/agent" || cfg.mode() != "EXCLUSIVE" {
							return fmt.Errorf("config = %+v", cfg)
						}
						return testAccNodeHasEnvProperty(cfg)
					}),
				),
			},
			{
				// The same configuration again: a refresh must agree with it.
				Config: changed,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectEmptyPlan(),
				}},
			},
			{
				Config:           unrestricted,
				ConfigPlanChecks: inPlace,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckNoResourceAttr("jenkins_node.foo", "job_restriction.regex"),
					testAccCheckJenkinsNodeConfig(name, func(cfg nodeConfig) error {
						if r := cfg.jobRestriction(); r != nil {
							return fmt.Errorf("job restriction = %+v, want none", *r)
						}
						return testAccNodeHasEnvProperty(cfg)
					}),
				),
			},
			{
				Config:           restricted,
				ConfigPlanChecks: inPlace,
			},
			{
				ResourceName:      "jenkins_node.foo",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccJenkinsNode_restrictionWithoutRegex(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: `
				resource jenkins_node foo {
				  name      = "tf-acc-node-no-regex"
				  remote_fs = "/home/jenkins/agent"

				  job_restriction = {}
				}`,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`attribute "regex" is\s+required`),
			},
		},
	})
}

// A node with no executors has no computer in Jenkins, so its
// /computer/<name>/ endpoints answer 404 and Read would drop it from state.
func TestAccJenkinsNode_zeroExecutors(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: `
				resource jenkins_node foo {
				  name          = "tf-acc-node-zero-executors"
				  remote_fs     = "/home/jenkins/agent"
				  num_executors = 0
				}`,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`(?s)num_executors.*at least 1`),
			},
		},
	})
}

// testAccCheckJenkinsNodeConfig runs check on the node's config.xml as Jenkins
// saved it, so a step asserts the controller and not only the state.
func testAccCheckJenkinsNodeConfig(name string, check func(nodeConfig) error) resource.TestCheckFunc {
	return func(*terraform.State) error {
		var cfg nodeConfig
		if err := testAccClient.GetNodeConfig(context.Background(), name, &cfg); err != nil {
			return err
		}
		return check(cfg)
	}
}

// testAccAddNodeEnvProperty adds an environment variables property to the
// node behind Terraform's back.
func testAccAddNodeEnvProperty(t *testing.T, name string) {
	t.Helper()
	ctx := context.Background()
	var cfg nodeConfig
	if err := testAccClient.GetNodeConfig(ctx, name, &cfg); err != nil {
		t.Fatalf("GetNodeConfig: %v", err)
	}
	var env xmlRawProperty
	if err := xml.Unmarshal([]byte(envVarsPropertyXML), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	cfg.NodeProperties.Properties = append(cfg.NodeProperties.Properties, env)
	doc, err := cfg.render()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if err := testAccClient.UpdateNodeConfig(ctx, name, doc); err != nil {
		t.Fatalf("UpdateNodeConfig: %v", err)
	}
	var saved nodeConfig
	if err := testAccClient.GetNodeConfig(ctx, name, &saved); err != nil {
		t.Fatalf("GetNodeConfig: %v", err)
	}
	if err := testAccNodeHasEnvProperty(saved); err != nil {
		t.Fatalf("Jenkins did not keep the property: %v", err)
	}
}

func testAccNodeHasEnvProperty(cfg nodeConfig) error {
	for _, p := range cfg.NodeProperties.Properties {
		if p.XMLName.Local == "hudson.slaves.EnvironmentVariablesNodeProperty" && strings.Contains(p.Raw, "<string>FOO</string>") {
			return nil
		}
	}
	return fmt.Errorf("the environment variables property added out of band is gone")
}

func testAccCheckJenkinsNodeExists(resourceName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()

		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("%s not found", resourceName)
		}
		if rs.Primary.ID == "" {
			return fmt.Errorf("ID is not set")
		}

		if _, err := testAccClient.GetNode(ctx, rs.Primary.Attributes["name"]); err != nil {
			return fmt.Errorf("unable to retrieve node %s: %w", rs.Primary.Attributes["name"], err)
		}

		return nil
	}
}

func testAccCheckJenkinsNodeDestroy(s *terraform.State) error {
	ctx := context.Background()

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "jenkins_node" {
			continue
		}

		if _, err := testAccClient.GetNode(ctx, rs.Primary.Attributes["name"]); err == nil {
			return fmt.Errorf("node %s still exists", rs.Primary.Attributes["name"])
		}
	}

	return nil
}

// nodeConfigStore stands in for /computer/<name>/config.xml: GetNodeConfig
// decodes the stored document and UpdateNodeConfig replaces it.
type nodeConfigStore struct {
	doc   string
	posts int
	// dropRestriction mimics a controller without the job-restrictions
	// plugin, which drops the property it cannot load and still answers 200.
	dropRestriction bool
	postErr         error
	// getErr fails every read, readBackErr only the reads after a post.
	getErr      error
	readBackErr error
	deleted     bool
	deleteErr   error
}

var restrictionElement = regexp.MustCompile(`(?s)<com\.synopsys\.arc\.jenkinsci\.plugins\.jobrestrictions\.nodes\.JobRestrictionProperty.*</com\.synopsys\.arc\.jenkinsci\.plugins\.jobrestrictions\.nodes\.JobRestrictionProperty>`)

func (s *nodeConfigStore) resource() *nodeResource {
	return &nodeResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
		mockCreateNode: func(_ context.Context, name string, _ int, _, _, _ string, _ ...interface{}) (*jenkins.Node, error) {
			s.doc = nodeXML(`<launcher class="hudson.slaves.JNLPLauncher"><webSocket>false</webSocket></launcher>`, "")
			return nil, nil
		},
		mockDeleteNode: func(_ context.Context, _ string) (bool, error) {
			if s.deleteErr != nil {
				return false, s.deleteErr
			}
			s.deleted = true
			return true, nil
		},
		mockGetNodeConfig: func(_ context.Context, _ string, out interface{}) error {
			if s.getErr != nil {
				return s.getErr
			}
			if s.posts > 0 && s.readBackErr != nil {
				return s.readBackErr
			}
			return xml.Unmarshal(stripXMLDeclaration([]byte(s.doc)), out)
		},
		mockUpdateNodeConfig: func(_ context.Context, _ string, config []byte) error {
			if s.postErr != nil {
				return s.postErr
			}
			s.posts++
			s.doc = string(config)
			if s.dropRestriction {
				s.doc = restrictionElement.ReplaceAllString(s.doc, "")
			}
			return nil
		},
	}}}
}

func nodeTestModel() nodeResourceModel {
	return nodeResourceModel{
		ID: types.StringValue("agent1"), Name: types.StringValue("agent1"),
		NumExecutors: types.Int64Value(1), Description: types.StringValue(""),
		RemoteFS: types.StringValue("/x"), Labels: types.StringValue(""),
		Mode: types.StringValue("NORMAL"), WebSocket: types.BoolValue(false),
	}
}

func nodeTestPlan(t *testing.T, s rschema.Schema, m nodeResourceModel) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Schema: s}
	if d := plan.Set(context.Background(), &m); d.HasError() {
		t.Fatalf("plan.Set: %v", d)
	}
	return plan
}

func nodeTestState(t *testing.T, s rschema.Schema, m nodeResourceModel) tfsdk.State {
	t.Helper()
	st := tfsdk.State{Schema: s}
	if d := st.Set(context.Background(), &m); d.HasError() {
		t.Fatalf("state.Set: %v", d)
	}
	return st
}

func TestNodeResourceUpdate(t *testing.T) {
	ctx := context.Background()
	planned := nodeTestModel()
	planned.NumExecutors = types.Int64Value(3)
	planned.Labels = types.StringValue("platform")
	planned.Mode = types.StringValue("EXCLUSIVE")
	planned.WebSocket = types.BoolValue(true)
	planned.JobRestriction = &nodeJobRestrictionModel{Regex: types.StringValue("^platform/.*"), CheckShortName: types.BoolValue(false)}

	t.Run("in place", func(t *testing.T) {
		store := &nodeConfigStore{doc: nodeXML(`<launcher class="hudson.slaves.JNLPLauncher"/>`, envVarsPropertyXML)}
		r := store.resource()
		s := covMSchema(ctx, r)

		resp := fwresource.UpdateResponse{State: tfsdk.State{Schema: s}}
		r.Update(ctx, fwresource.UpdateRequest{Plan: nodeTestPlan(t, s, planned), State: nodeTestState(t, s, nodeTestModel())}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("Update: %v", resp.Diagnostics)
		}
		if store.posts != 1 {
			t.Errorf("%d config.xml posts, want 1", store.posts)
		}
		got := parseNodeConfig(t, store.doc)
		if got.NumExecutors != 3 || got.Label != "platform" || got.mode() != "EXCLUSIVE" || !got.webSocket() {
			t.Errorf("stored config = %+v", got)
		}
		if r := got.jobRestriction(); r == nil || r.Regex != "^platform/.*" {
			t.Errorf("stored restriction = %v", r)
		}
		if !strings.Contains(store.doc, "<string>FOO</string>") {
			t.Error("the unmanaged property was not written back")
		}
		var state nodeResourceModel
		if d := resp.State.Get(ctx, &state); d.HasError() {
			t.Fatalf("state.Get: %v", d)
		}
		if state.JobRestriction == nil || state.JobRestriction.Regex.ValueString() != "^platform/.*" || !state.WebSocket.ValueBool() {
			t.Errorf("state = %+v", state)
		}
	})

	t.Run("restriction dropped by Jenkins", func(t *testing.T) {
		store := &nodeConfigStore{doc: nodeXML(`<launcher class="hudson.slaves.JNLPLauncher"/>`, ""), dropRestriction: true}
		r := store.resource()
		s := covMSchema(ctx, r)

		resp := fwresource.UpdateResponse{State: tfsdk.State{Schema: s}}
		r.Update(ctx, fwresource.UpdateRequest{Plan: nodeTestPlan(t, s, planned), State: nodeTestState(t, s, nodeTestModel())}, &resp)
		if !resp.Diagnostics.HasError() || !strings.Contains(fmt.Sprint(resp.Diagnostics), "job-restrictions") {
			t.Fatalf("Update diagnostics = %v, want an error naming the job-restrictions plugin", resp.Diagnostics)
		}
	})

	t.Run("post error", func(t *testing.T) {
		store := &nodeConfigStore{doc: nodeXML(`<launcher class="hudson.slaves.JNLPLauncher"/>`, ""), postErr: fmt.Errorf("boom")}
		r := store.resource()
		s := covMSchema(ctx, r)

		resp := fwresource.UpdateResponse{State: tfsdk.State{Schema: s}}
		r.Update(ctx, fwresource.UpdateRequest{Plan: nodeTestPlan(t, s, planned), State: nodeTestState(t, s, nodeTestModel())}, &resp)
		if !resp.Diagnostics.HasError() {
			t.Fatal("Update should fail when the config.xml post fails")
		}
	})

	failures := []struct {
		name  string
		store *nodeConfigStore
		want  string
		posts int
	}{
		{"read error", &nodeConfigStore{getErr: fmt.Errorf("boom")}, "reading", 0},
		{
			"web_socket on a non-inbound launcher",
			&nodeConfigStore{doc: nodeXML(`<launcher class="hudson.plugins.sshslaves.SSHLauncher" plugin="ssh-slaves@3.0"><host>h</host></launcher>`, "")},
			"inbound agents only", 0,
		},
		{
			"read back error",
			&nodeConfigStore{doc: nodeXML(`<launcher class="hudson.slaves.JNLPLauncher"/>`, ""), readBackErr: fmt.Errorf("boom")},
			"reading back", 1,
		},
	}
	for _, tc := range failures {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.store.resource()
			s := covMSchema(ctx, r)

			resp := fwresource.UpdateResponse{State: tfsdk.State{Schema: s}}
			r.Update(ctx, fwresource.UpdateRequest{Plan: nodeTestPlan(t, s, planned), State: nodeTestState(t, s, nodeTestModel())}, &resp)
			if !resp.Diagnostics.HasError() || !strings.Contains(fmt.Sprint(resp.Diagnostics), tc.want) {
				t.Fatalf("Update diagnostics = %v, want an error containing %q", resp.Diagnostics, tc.want)
			}
			if tc.store.posts != tc.posts {
				t.Errorf("%d config.xml posts, want %d", tc.store.posts, tc.posts)
			}
		})
	}
}

func TestNodeResourceCreate(t *testing.T) {
	ctx := context.Background()
	restricted := nodeTestModel()
	restricted.JobRestriction = &nodeJobRestrictionModel{Regex: types.StringValue("^a/.*"), CheckShortName: types.BoolValue(false)}
	webSocket := nodeTestModel()
	webSocket.WebSocket = types.BoolValue(true)

	cases := []struct {
		name      string
		model     nodeResourceModel
		wantPosts int
	}{
		// doCreateItem already carries every other attribute.
		{"plain", nodeTestModel(), 0},
		{"job restriction", restricted, 1},
		{"web socket", webSocket, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &nodeConfigStore{}
			r := store.resource()
			s := covMSchema(ctx, r)

			resp := fwresource.CreateResponse{State: tfsdk.State{Schema: s}}
			r.Create(ctx, fwresource.CreateRequest{Plan: nodeTestPlan(t, s, tc.model)}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Create: %v", resp.Diagnostics)
			}
			if store.posts != tc.wantPosts {
				t.Errorf("%d config.xml posts, want %d", store.posts, tc.wantPosts)
			}
		})
	}

	// The node exists once doCreateItem returns. A failed property write
	// deletes it and leaves no state, so the next apply creates it again;
	// only when that delete fails too does the node stay in state, tainted,
	// rather than orphaned on the controller.
	writeFails := []struct {
		name        string
		deleteErr   error
		wantDeleted bool
		wantState   bool
	}{
		{"node deleted", nil, true, false},
		{"delete fails too", fmt.Errorf("gone fishing"), false, true},
	}
	for _, tc := range writeFails {
		t.Run("property write fails, "+tc.name, func(t *testing.T) {
			store := &nodeConfigStore{postErr: fmt.Errorf("boom"), deleteErr: tc.deleteErr}
			r := store.resource()
			s := covMSchema(ctx, r)

			resp := fwresource.CreateResponse{State: tfsdk.State{Schema: s, Raw: covMNull(ctx, s)}}
			r.Create(ctx, fwresource.CreateRequest{Plan: nodeTestPlan(t, s, restricted)}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("Create should fail when the config.xml post fails")
			}
			if store.deleted != tc.wantDeleted {
				t.Errorf("node deleted = %v, want %v", store.deleted, tc.wantDeleted)
			}
			if got := !resp.State.Raw.IsNull(); got != tc.wantState {
				t.Errorf("state saved = %v, want %v", got, tc.wantState)
			}
		})
	}
}

func TestNodeResourceRead(t *testing.T) {
	ctx := context.Background()
	store := &nodeConfigStore{doc: nodeConfigFixture}
	r := store.resource()
	s := covMSchema(ctx, r)

	resp := fwresource.ReadResponse{State: tfsdk.State{Schema: s}}
	r.Read(ctx, fwresource.ReadRequest{State: nodeTestState(t, s, nodeTestModel())}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	var got nodeResourceModel
	if d := resp.State.Get(ctx, &got); d.HasError() {
		t.Fatalf("state.Get: %v", d)
	}
	if !got.WebSocket.ValueBool() {
		t.Error("web_socket = false, want true")
	}
	if got.JobRestriction == nil || got.JobRestriction.Regex.ValueString() != "^platform/apps/.*" || got.JobRestriction.CheckShortName.ValueBool() {
		t.Errorf("job_restriction = %+v", got.JobRestriction)
	}
}
