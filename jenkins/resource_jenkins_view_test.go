package jenkins

import (
	"context"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccJenkinsView_basic(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsViewDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource jenkins_view foo {
				  name = "tf-acc-test-%s"
				}`, randString),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_view.foo", "id", "tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_view.foo", "name", "tf-acc-test-"+randString),
				),
			},
			{
				ResourceName:      "jenkins_view.foo",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccJenkinsView_folderUnsupported(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource jenkins_view foo {
				  name   = "tf-acc-test-%s"
				  folder = "some-folder"
				}`, randString),
				ExpectError: regexp.MustCompile(`Folder-Scoped Views Not Supported`),
			},
		},
	})
}

func TestAccJenkinsView_withAssignedProjects(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsViewDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource jenkins_folder project {
				  name = "tf-acc-test-%s"
				}

				resource jenkins_view foo {
				  name              = "tf-acc-view-%s"
				  assigned_projects = [jenkins_folder.project.name]
				}`, randString, randString),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_view.foo", "id", "tf-acc-view-"+randString),
					resource.TestCheckResourceAttr("jenkins_view.foo", "assigned_projects.#", "1"),
					resource.TestCheckResourceAttr("jenkins_view.foo", "assigned_projects.0", "tf-acc-test-"+randString),
				),
			},
		},
	})
}

func testAccCheckJenkinsViewDestroy(s *terraform.State) error {
	ctx := context.Background()

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "jenkins_view" {
			continue
		}

		_, err := testAccClient.GetView(ctx, rs.Primary.ID)
		if err == nil {
			return fmt.Errorf("View %s still exists", rs.Primary.ID)
		}
	}

	return nil
}
