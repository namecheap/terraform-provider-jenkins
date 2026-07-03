package jenkins

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
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
