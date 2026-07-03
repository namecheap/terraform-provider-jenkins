package jenkins

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

// testCertificateKeystoreB64 is a base64-encoded PKCS#12 keystore (password
// "changeit") generated solely for these tests. It carries a throwaway
// self-signed certificate and key; it authenticates nothing.
const testCertificateKeystoreB64 = "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"

func TestAccJenkinsCredentialCertificate_basic(t *testing.T) {
	var cred CertificateCredentials

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsCredentialCertificateDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource jenkins_credential_certificate foo {
				  name     = "test-certificate"
				  keystore = %q
				  password = "changeit"
				}`, testCertificateKeystoreB64),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_credential_certificate.foo", "id", "/test-certificate"),
					testAccCheckJenkinsCredentialCertificateExists("jenkins_credential_certificate.foo", &cred),
				),
			},
			{
				// Update description; keystore is re-sent and must remain valid.
				Config: fmt.Sprintf(`
				resource jenkins_credential_certificate foo {
				  name        = "test-certificate"
				  description = "new-description"
				  keystore    = %q
				  password    = "changeit"
				}`, testCertificateKeystoreB64),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsCredentialCertificateExists("jenkins_credential_certificate.foo", &cred),
					resource.TestCheckResourceAttr("jenkins_credential_certificate.foo", "description", "new-description"),
				),
			},
		},
	})
}

func TestAccJenkinsCredentialCertificate_writeOnly(t *testing.T) {
	var cred CertificateCredentials

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)},
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsCredentialCertificateDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource jenkins_credential_certificate foo {
				  name               = "test-certificate-wo"
				  keystore_wo        = %q
				  keystore_wo_version = "1"
				  password           = "changeit"
				}`, testCertificateKeystoreB64),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsCredentialCertificateExists("jenkins_credential_certificate.foo", &cred),
					resource.TestCheckResourceAttr("jenkins_credential_certificate.foo", "keystore_wo_version", "1"),
					resource.TestCheckNoResourceAttr("jenkins_credential_certificate.foo", "keystore_wo"),
					resource.TestCheckNoResourceAttr("jenkins_credential_certificate.foo", "keystore"),
				),
			},
		},
	})
}

func testAccCheckJenkinsCredentialCertificateExists(resourceName string, cred *CertificateCredentials) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()

		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return errors.New(resourceName + " not found")
		}
		if rs.Primary.ID == "" {
			return fmt.Errorf("ID is not set")
		}

		manager := testAccClient.Credentials()
		manager.Folder = formatFolderName(rs.Primary.Attributes["folder"])
		err := manager.GetSingle(ctx, rs.Primary.Attributes["domain"], rs.Primary.Attributes["name"], cred)
		if err != nil {
			return fmt.Errorf("unable to retrieve certificate credential %s - %s: %w", rs.Primary.Attributes["folder"], rs.Primary.Attributes["name"], err)
		}

		return nil
	}
}

func testAccCheckJenkinsCredentialCertificateDestroy(s *terraform.State) error {
	ctx := context.Background()

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "jenkins_credential_certificate" {
			continue
		} else if _, ok := rs.Primary.Meta["name"]; !ok {
			continue
		}

		cred := CertificateCredentials{}
		manager := testAccClient.Credentials()
		manager.Folder = formatFolderName(rs.Primary.Meta["folder"].(string))
		err := manager.GetSingle(ctx, rs.Primary.Meta["domain"].(string), rs.Primary.Meta["name"].(string), &cred)
		if err == nil {
			return fmt.Errorf("certificate credential still exists: %s - %s", rs.Primary.Attributes["folder"], rs.Primary.Attributes["name"])
		}
	}

	return nil
}
