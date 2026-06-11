package jenkins

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccJenkinsCredentialAzureServicePrincipalDataSource_basic(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource jenkins_credential_azure_service_principal foo {
					name            = "tf-acc-test-%s"
					description     = "Terraform acceptance tests %s"
					subscription_id = "00000000-0000-0000-0000-000000000001"
					client_id       = "00000000-0000-0000-0000-000000000002"
					client_secret   = "supersecret"
					tenant          = "00000000-0000-0000-0000-000000000003"
				}

				data jenkins_credential_azure_service_principal foo {
					name   = jenkins_credential_azure_service_principal.foo.name
					domain = "`+defaultCredentialDomain+`"
				}`, randString, randString),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("data.jenkins_credential_azure_service_principal.foo", "id", "/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("data.jenkins_credential_azure_service_principal.foo", "name", "tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("data.jenkins_credential_azure_service_principal.foo", "description", "Terraform acceptance tests "+randString),
					resource.TestCheckResourceAttr("data.jenkins_credential_azure_service_principal.foo", "scope", "GLOBAL"),
				),
			},
		},
	})
}
