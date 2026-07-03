package jenkins

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// TestAccJenkinsPlugin_basic manages a plugin that the acceptance Jenkins image
// already ships (git), so Create takes the idempotent "already installed" path
// and the test needs no update-center download. Destroy uses the default
// uninstall_on_destroy = false, so the plugin must remain installed afterward.
func TestAccJenkinsPlugin_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsPluginStillInstalled("git"),
		Steps: []resource.TestStep{
			{
				Config: `
				resource jenkins_plugin git {
				  name = "git"
				}`,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsPluginExists("jenkins_plugin.git"),
					resource.TestCheckResourceAttr("jenkins_plugin.git", "id", "git"),
					resource.TestCheckResourceAttr("jenkins_plugin.git", "active", "true"),
					resource.TestCheckResourceAttr("jenkins_plugin.git", "pending_restart", "false"),
					resource.TestCheckResourceAttr("jenkins_plugin.git", "uninstall_on_destroy", "false"),
					resource.TestCheckResourceAttrSet("jenkins_plugin.git", "version"),
				),
			},
			{
				ResourceName:            "jenkins_plugin.git",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"uninstall_on_destroy"},
			},
		},
	})
}

func testAccCheckJenkinsPluginExists(resourceName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("%s not found", resourceName)
		}
		if rs.Primary.ID == "" {
			return fmt.Errorf("ID is not set")
		}

		name := rs.Primary.Attributes["name"]
		p, err := testAccClient.HasPlugin(context.Background(), name)
		if err != nil {
			return fmt.Errorf("unable to retrieve plugin %s: %w", name, err)
		}
		if p == nil {
			return fmt.Errorf("plugin %s is not installed", name)
		}
		return nil
	}
}

func testAccCheckJenkinsPluginStillInstalled(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		p, err := testAccClient.HasPlugin(context.Background(), name)
		if err != nil {
			return fmt.Errorf("unable to retrieve plugin %s: %w", name, err)
		}
		if p == nil {
			return fmt.Errorf("plugin %s should remain installed after destroy with uninstall_on_destroy = false", name)
		}
		return nil
	}
}
