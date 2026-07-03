package jenkins

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccJenkinsPipelineJob_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsPipelineJobDestroy,
		Steps: []resource.TestStep{
			{
				Config: `
				resource jenkins_pipeline_job foo {
				  name   = "tf-acc-pipeline"
				  script = "pipeline {\n  agent any\n  stages {\n    stage('build') { steps { echo 'hello' } }\n  }\n}"
				}`,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsPipelineJobExists("jenkins_pipeline_job.foo"),
					resource.TestCheckResourceAttr("jenkins_pipeline_job.foo", "id", "/job/tf-acc-pipeline"),
					resource.TestCheckResourceAttr("jenkins_pipeline_job.foo", "sandbox", "true"),
					resource.TestCheckResourceAttr("jenkins_pipeline_job.foo", "disabled", "false"),
				),
			},
			{
				Config: `
				resource jenkins_pipeline_job foo {
				  name        = "tf-acc-pipeline"
				  description = "updated"
				  script      = "pipeline {\n  agent none\n  stages {\n    stage('noop') { steps { echo 'bye' } }\n  }\n}"
				  disabled    = true
				}`,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsPipelineJobExists("jenkins_pipeline_job.foo"),
					resource.TestCheckResourceAttr("jenkins_pipeline_job.foo", "description", "updated"),
					resource.TestCheckResourceAttr("jenkins_pipeline_job.foo", "disabled", "true"),
				),
			},
			{
				ResourceName:      "jenkins_pipeline_job.foo",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func testAccCheckJenkinsPipelineJobExists(resourceName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("%s not found", resourceName)
		}
		if rs.Primary.ID == "" {
			return fmt.Errorf("ID is not set")
		}

		name, folders := parseCanonicalJobID(rs.Primary.ID)
		if _, err := testAccClient.GetJob(context.Background(), name, folders...); err != nil {
			return fmt.Errorf("unable to retrieve pipeline job %s: %w", name, err)
		}
		return nil
	}
}

func testAccCheckJenkinsPipelineJobDestroy(s *terraform.State) error {
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "jenkins_pipeline_job" {
			continue
		}

		name, folders := parseCanonicalJobID(rs.Primary.ID)
		if _, err := testAccClient.GetJob(context.Background(), name, folders...); err == nil {
			return fmt.Errorf("pipeline job %s still exists", name)
		}
	}
	return nil
}
