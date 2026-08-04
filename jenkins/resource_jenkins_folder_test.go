package jenkins

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccJenkinsFolder_basic(t *testing.T) {
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
				}`, randString, randString),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_folder.foo", "id", "/job/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.foo", "name", "tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.foo", "display_name", ""),
				),
			},
			{
				ResourceName:            "jenkins_folder.foo",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"template"},
			},
		},
	})
}

func TestAccJenkinsFolder_withDisplayName(t *testing.T) {
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
				  display_name = "TF Acceptance Test %s"
				  description = "Terraform acceptance tests %s"
				}`, randString, randString, randString),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_folder.foo", "id", "/job/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.foo", "name", "tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.foo", "display_name", "TF Acceptance Test "+randString),
				),
			},
		},
	})
}

func TestAccJenkinsFolder_nested(t *testing.T) {
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

				resource jenkins_folder sub {
					name = "subfolder"
                    display_name = "TF Acceptance Test %s"
					folder = jenkins_folder.foo.id
					description = "Terraform acceptance tests ${jenkins_folder.foo.name}"
				}`, randString, randString, randString),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_folder.foo", "id", "/job/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.foo", "name", "tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.sub", "id", "/job/tf-acc-test-"+randString+"/job/subfolder"),
					resource.TestCheckResourceAttr("jenkins_folder.sub", "name", "subfolder"),
					resource.TestCheckResourceAttr("jenkins_folder.sub", "folder", "/job/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.sub", "display_name", "TF Acceptance Test "+randString),
				),
			},
		},
	})
}

func TestAccJenkinsFolder_withSecurity(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsFolderDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource jenkins_folder foo {
				  name        = "tf-acc-test-%s"
				  description = "Terraform acceptance tests %s"
				  security {
				    inheritance_strategy = "org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy"
				    permissions = [
				      "hudson.model.Item.Discover:anonymous",
				    ]
				  }
				}`, randString, randString),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_folder.foo", "id", "/job/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.foo", "security.#", "1"),
					resource.TestCheckTypeSetElemNestedAttrs("jenkins_folder.foo", "security.*", map[string]string{
						"inheritance_strategy": "org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy",
						"permissions.#":        "1",
					}),
					testAccCheckJenkinsFolderHasPermission("jenkins_folder.foo", "hudson.model.Item.Discover:anonymous"),
				),
			},
		},
	})
}

// testAccCheckJenkinsFolderHasPermission asserts that one of the resource's
// "security.*.permissions.*" attributes (a set nested inside a set, so the
// exact flatmap keys are hash-based rather than index-based) equals want.
func testAccCheckJenkinsFolderHasPermission(resourceName, want string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("resource not found: %s", resourceName)
		}

		for k, v := range rs.Primary.Attributes {
			if strings.Contains(k, "permissions.") && v == want {
				return nil
			}
		}
		return fmt.Errorf("no %q attribute matching %q found on %s; attributes: %v", "security.*.permissions.*", want, resourceName, rs.Primary.Attributes)
	}
}

func testAccCheckJenkinsFolderDestroy(s *terraform.State) error {
	ctx := context.Background()

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "jenkins_folder" {
			continue
		}

		_, err := testAccClient.GetJob(ctx, rs.Primary.ID)
		if err == nil {
			return fmt.Errorf("Folder %s still exists", rs.Primary.ID)
		}
	}

	return nil
}
