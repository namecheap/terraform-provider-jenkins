package jenkins

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccJenkinsCredentialSecretTextDataSource_basic(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource jenkins_credential_secret_text foo {
					name        = "tf-acc-test-%s"
					description = "Terraform acceptance tests %s"
					secret      = "supersecret"
				}

				data jenkins_credential_secret_text foo {
					name   = jenkins_credential_secret_text.foo.name
					domain = "`+defaultCredentialDomain+`"
				}`, randString, randString),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("data.jenkins_credential_secret_text.foo", "id", "/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("data.jenkins_credential_secret_text.foo", "name", "tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("data.jenkins_credential_secret_text.foo", "description", "Terraform acceptance tests "+randString),
					resource.TestCheckResourceAttr("data.jenkins_credential_secret_text.foo", "scope", "GLOBAL"),
				),
			},
		},
	})
}

func TestAccJenkinsCredentialSecretTextDataSource_nested(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource jenkins_folder foo {
					name = "tf-acc-test-%s"
				}

				resource jenkins_credential_secret_text sub {
					name        = "subfolder"
					folder      = jenkins_folder.foo.id
					description = "Terraform acceptance tests %s"
					secret      = "supersecret"
				}

				data jenkins_credential_secret_text sub {
					name   = jenkins_credential_secret_text.sub.name
					domain = "`+defaultCredentialDomain+`"
					folder = jenkins_credential_secret_text.sub.folder
				}`, randString, randString),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("data.jenkins_credential_secret_text.sub", "name", "subfolder"),
					resource.TestCheckResourceAttr("data.jenkins_credential_secret_text.sub", "folder", "/job/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("data.jenkins_credential_secret_text.sub", "description", "Terraform acceptance tests "+randString),
				),
			},
		},
	})
}
