package jenkins

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccJenkinsMultibranchPipeline_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsMultibranchPipelineDestroy,
		Steps: []resource.TestStep{
			{
				Config: `
				resource jenkins_multibranch_pipeline foo {
				  name   = "tf-acc-multibranch"
				  remote = "https://example.com/org/repo.git"
				}`,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsMultibranchPipelineExists("jenkins_multibranch_pipeline.foo"),
					resource.TestCheckResourceAttr("jenkins_multibranch_pipeline.foo", "id", "/job/tf-acc-multibranch"),
					resource.TestCheckResourceAttr("jenkins_multibranch_pipeline.foo", "remote", "https://example.com/org/repo.git"),
					resource.TestCheckResourceAttr("jenkins_multibranch_pipeline.foo", "script_path", "Jenkinsfile"),
				),
			},
			{
				Config: `
				resource jenkins_multibranch_pipeline foo {
				  name        = "tf-acc-multibranch"
				  description = "updated"
				  remote      = "https://example.com/org/other.git"
				  script_path = "ci/Jenkinsfile"
				}`,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsMultibranchPipelineExists("jenkins_multibranch_pipeline.foo"),
					resource.TestCheckResourceAttr("jenkins_multibranch_pipeline.foo", "description", "updated"),
					resource.TestCheckResourceAttr("jenkins_multibranch_pipeline.foo", "remote", "https://example.com/org/other.git"),
					resource.TestCheckResourceAttr("jenkins_multibranch_pipeline.foo", "script_path", "ci/Jenkinsfile"),
				),
			},
			{
				ResourceName:      "jenkins_multibranch_pipeline.foo",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func testAccCheckJenkinsMultibranchPipelineExists(resourceName string) resource.TestCheckFunc {
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
			return fmt.Errorf("unable to retrieve multibranch pipeline %s: %w", name, err)
		}
		return nil
	}
}

func testAccCheckJenkinsMultibranchPipelineDestroy(s *terraform.State) error {
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "jenkins_multibranch_pipeline" {
			continue
		}

		name, folders := parseCanonicalJobID(rs.Primary.ID)
		if _, err := testAccClient.GetJob(context.Background(), name, folders...); err == nil {
			return fmt.Errorf("multibranch pipeline %s still exists", name)
		}
	}
	return nil
}
