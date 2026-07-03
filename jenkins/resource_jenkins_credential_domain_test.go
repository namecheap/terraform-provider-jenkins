package jenkins

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccJenkinsCredentialDomain_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsCredentialDomainDestroy,
		Steps: []resource.TestStep{
			{
				Config: `
				resource jenkins_credential_domain foo {
				  name        = "tf-acc-domain"
				  description = "managed by terraform"
				}`,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsCredentialDomainExists("jenkins_credential_domain.foo"),
					resource.TestCheckResourceAttr("jenkins_credential_domain.foo", "id", "/tf-acc-domain"),
					resource.TestCheckResourceAttr("jenkins_credential_domain.foo", "description", "managed by terraform"),
				),
			},
			{
				Config: `
				resource jenkins_credential_domain foo {
				  name        = "tf-acc-domain"
				  description = "updated"
				}`,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsCredentialDomainExists("jenkins_credential_domain.foo"),
					resource.TestCheckResourceAttr("jenkins_credential_domain.foo", "description", "updated"),
				),
			},
			{
				ResourceName:      "jenkins_credential_domain.foo",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func testAccCheckJenkinsCredentialDomainExists(resourceName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("%s not found", resourceName)
		}
		if rs.Primary.ID == "" {
			return fmt.Errorf("ID is not set")
		}

		var dom credentialDomainXML
		if err := testAccClient.GetCredentialDomain(context.Background(), rs.Primary.Attributes["folder"], rs.Primary.Attributes["name"], &dom); err != nil {
			return fmt.Errorf("unable to retrieve credential domain %s: %w", rs.Primary.Attributes["name"], err)
		}
		return nil
	}
}

func testAccCheckJenkinsCredentialDomainDestroy(s *terraform.State) error {
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "jenkins_credential_domain" {
			continue
		}

		var dom credentialDomainXML
		if err := testAccClient.GetCredentialDomain(context.Background(), rs.Primary.Attributes["folder"], rs.Primary.Attributes["name"], &dom); err == nil {
			return fmt.Errorf("credential domain %s still exists", rs.Primary.Attributes["name"])
		}
	}
	return nil
}
