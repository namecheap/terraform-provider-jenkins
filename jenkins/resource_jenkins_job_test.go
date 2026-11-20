package jenkins

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

var (
	//go:embed "resource_jenkins_job_test.xml"
	testXML []byte
)

func TestAccJenkinsJob_basic(t *testing.T) {
	testDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(testDir, "test.xml"), testXML, 0644)
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsJobDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource jenkins_job foo {
	name = "tf-acc-test-%s"
	template = templatefile("%s/test.xml", {
		description = "Acceptance testing Jenkins provider"
	})
}`, randString, testDir),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_job.foo", "id", "/job/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_job.foo", "name", "tf-acc-test-"+randString),
				),
			},
			{
				ResourceName:      "jenkins_job.foo",
				ImportState:       true,
				ImportStateVerify: true,
				// The template stored on read is Jenkins' re-serialized config,
				// which is semantically (not textually) equal to the submitted
				// one; ImportStateVerify compares raw strings, so ignore it.
				ImportStateVerifyIgnore: []string{"template"},
			},
		},
	})
}

func TestAccJenkinsJob_nested(t *testing.T) {
	testDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(testDir, "test.xml"), testXML, 0644)
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsFolderDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource jenkins_folder foo {
	name = "tf-acc-test-%s"
	description = "Terraform acceptance tests %s"
}

resource jenkins_job sub {
	name = "subfolder"
	folder = jenkins_folder.foo.id
	template = templatefile("%s/test.xml", {
		description = "Acceptance testing Jenkins provider"
	})
}`, randString, randString, testDir),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_folder.foo", "id", "/job/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.foo", "name", "tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_job.sub", "id", "/job/tf-acc-test-"+randString+"/job/subfolder"),
					resource.TestCheckResourceAttr("jenkins_job.sub", "name", "subfolder"),
					resource.TestCheckResourceAttr("jenkins_job.sub", "folder", "/job/tf-acc-test-"+randString),
				),
			},
		},
	})
}

func TestAccJenkinsJob_disabled(t *testing.T) {
	testDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(testDir, "test.xml"), testXML, 0644)
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	resourceName := "jenkins_job.foo"

	config := func(disabled bool) string {
		return fmt.Sprintf(`
resource jenkins_job foo {
	name = "tf-acc-test-%s"
	disabled = %t
	template = templatefile("%s/test.xml", {
		description = "Acceptance testing Jenkins provider"
	})
}`, randString, disabled, testDir)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsJobDestroy,
		Steps: []resource.TestStep{
			{
				// Created disabled: the job must not be buildable.
				Config: config(true),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "disabled", "true"),
					testAccCheckJenkinsJobEnabled(resourceName, false),
				),
			},
			{
				// Toggled to enabled: the provider must flip the job state.
				Config: config(false),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "disabled", "false"),
					testAccCheckJenkinsJobEnabled(resourceName, true),
				),
			},
		},
	})
}

// TestAccJenkinsJob_ignoredElements stands a pipeline job's description in
// for an element a plugin writes on its own (the integration image has no
// gitlab-branch-source): changed out of band, it plans no change, and an
// update keeps Jenkins' copy.
func TestAccJenkinsJob_ignoredElements(t *testing.T) {
	testDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(testDir, "test.xml"), testXML, 0644)
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	name := "tf-acc-test-" + randString

	config := func(disabled bool) string {
		return fmt.Sprintf(`
resource jenkins_job foo {
	name = "%s"
	disabled = %t
	ignored_elements = ["/flow-definition/description"]
	template = templatefile("%s/test.xml", {
		description = "managed"
	})
}`, name, disabled, testDir)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsJobDestroy,
		Steps: []resource.TestStep{
			{
				Config: config(false),
			},
			{
				PreConfig: func() {
					ctx := context.Background()
					job, err := testAccClient.GetJob(ctx, name)
					if err != nil {
						t.Fatalf("GetJob: %v", err)
					}
					live, err := job.GetConfig(ctx)
					if err != nil {
						t.Fatalf("GetConfig: %v", err)
					}
					changed := strings.Replace(live, "<description>managed</description>", "<description>out-of-band</description>", 1)
					if changed == live {
						t.Fatalf("config.xml has no managed description:\n%s", live)
					}
					if err := job.UpdateConfig(ctx, changed); err != nil {
						t.Fatalf("UpdateConfig: %v", err)
					}
				},
				Config:   config(false),
				PlanOnly: true,
			},
			{
				Config: config(true),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsJobConfigContains("jenkins_job.foo", "<description>out-of-band</description>"),
					// State keeps the template's copy, not the one Jenkins has.
					resource.TestMatchResourceAttr("jenkins_job.foo", "template", regexp.MustCompile("<description>managed</description>")),
				),
			},
		},
	})
}

// An update that changes how many same-named parents there are cannot pair
// them with Jenkins', so it must not post one without an ignored element
// Jenkins has in them.
func TestAccJenkinsJob_ignoredElementsUnpairedParent(t *testing.T) {
	name := "tf-acc-test-" + acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	param := func(name, description string) string {
		return "<hudson.model.StringParameterDefinition><name>" + name + "</name>" + description +
			"<trim>false</trim></hudson.model.StringParameterDefinition>"
	}
	config := func(params string) string {
		return fmt.Sprintf(`
resource jenkins_job foo {
	name = "%s"
	ignored_elements = ["/project/properties/hudson.model.ParametersDefinitionProperty/parameterDefinitions/*/description"]
	template = <<-EOT
		<project>
		  <actions/>
		  <keepDependencies>false</keepDependencies>
		  <properties>
		    <hudson.model.ParametersDefinitionProperty>
		      <parameterDefinitions>%s</parameterDefinitions>
		    </hudson.model.ParametersDefinitionProperty>
		  </properties>
		  <scm class="hudson.scm.NullSCM"/>
		  <canRoam>false</canRoam>
		  <disabled>false</disabled>
		  <blockBuildWhenDownstreamBuilding>false</blockBuildWhenDownstreamBuilding>
		  <blockBuildWhenUpstreamBuilding>false</blockBuildWhenUpstreamBuilding>
		  <triggers/>
		  <concurrentBuild>false</concurrentBuild>
		  <builders/>
		  <publishers/>
		  <buildWrappers/>
		</project>
	EOT
}`, name, params)
	}
	one := param("first", "<description>set on create</description>")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsJobDestroy,
		Steps: []resource.TestStep{
			{
				Config: config(one),
				Check:  testAccCheckJenkinsJobConfigContains("jenkins_job.foo", "<description>set on create</description>"),
			},
			{
				Config:      config(param("first", "") + param("second", "")),
				ExpectError: regexp.MustCompile(`the template has no <description> in a\s+/project/properties/hudson\.model\.ParametersDefinitionProperty/parameterDefinitions/hudson\.model\.StringParameterDefinition\s+element`),
			},
			{
				Config: config(param("first", "<description/>") + param("second", "<description/>")),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsJobConfigContains("jenkins_job.foo", "<name>second</name>"),
				),
			},
			{
				Config:   config(param("first", "<description/>") + param("second", "<description/>")),
				PlanOnly: true,
			},
		},
	})
}

// testAccCheckJenkinsJobConfigContains asserts the live config.xml of a job
// resource contains want.
func testAccCheckJenkinsJobConfigContains(resourceName, want string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("resource not found: %s", resourceName)
		}

		ctx := context.Background()
		name, folders := parseCanonicalJobID(rs.Primary.ID)
		job, err := testAccClient.GetJob(ctx, name, folders...)
		if err != nil {
			return err
		}
		config, err := job.GetConfig(ctx)
		if err != nil {
			return err
		}
		if !strings.Contains(config, want) {
			return fmt.Errorf("job %s config.xml does not contain %q:\n%s", rs.Primary.ID, want, config)
		}
		return nil
	}
}

// testAccCheckJenkinsJobEnabled asserts the live enabled state of a job resource.
func testAccCheckJenkinsJobEnabled(resourceName string, want bool) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("resource not found: %s", resourceName)
		}

		ctx := context.Background()
		name, folders := parseCanonicalJobID(rs.Primary.ID)
		job, err := testAccClient.GetJob(ctx, name, folders...)
		if err != nil {
			return err
		}
		enabled, err := job.IsEnabled(ctx)
		if err != nil {
			return err
		}
		if enabled != want {
			return fmt.Errorf("job %s enabled = %t, want %t", rs.Primary.ID, enabled, want)
		}
		return nil
	}
}

func testAccCheckJenkinsJobDestroy(s *terraform.State) error {
	ctx := context.Background()

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "jenkins_job" {
			continue
		}

		_, err := testAccClient.GetJob(ctx, rs.Primary.ID)
		if err == nil {
			return fmt.Errorf("Job %s still exists", rs.Primary.ID)
		}
	}

	return nil
}
