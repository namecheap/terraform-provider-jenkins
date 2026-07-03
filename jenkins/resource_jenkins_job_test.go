package jenkins

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
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
