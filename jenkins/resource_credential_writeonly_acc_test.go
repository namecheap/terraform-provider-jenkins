package jenkins

import (
	"regexp"
	"testing"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

// errInvalidAttributeCombination matches the diagnostic summary emitted by the
// ExactlyOneOf / Conflicting / RequiredTogether config validators.
var errInvalidAttributeCombination = regexp.MustCompile("Invalid Attribute Combination")

// The write-only acceptance tests below assert that a credential can be created
// and rotated through the `<secret>_wo` / `<secret>_wo_version` pair, and that the
// secret material never lands in Terraform state. They gate on Terraform >= 1.11,
// which is where write-only arguments were introduced, and skip cleanly below it.
//
// Coverage is by structural variant rather than one-per-resource: secret_text
// (a required single secret, ExactlyOneOf), username (an optional secret,
// Conflicting), and ssh (a required secret nested under PrivateKeySource that is
// always re-sent). The other credential resources reuse these same patterns and
// helpers, and their write-only wiring is verified by TestCredentialWriteOnlySchema.

func TestAccJenkinsCredentialSecretText_writeOnly(t *testing.T) {
	var cred jenkins.StringCredentials

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)},
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsCredentialSecretTextDestroy,
		Steps: []resource.TestStep{
			{
				Config: `
				resource jenkins_credential_secret_text foo {
				  name              = "test-secret-text-wo"
				  secret_wo         = "very-secret"
				  secret_wo_version = "1"
				}`,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsCredentialSecretTextExists("jenkins_credential_secret_text.foo", &cred),
					resource.TestCheckResourceAttr("jenkins_credential_secret_text.foo", "secret_wo_version", "1"),
					// The write-only value and the plain attribute must never be persisted.
					resource.TestCheckNoResourceAttr("jenkins_credential_secret_text.foo", "secret_wo"),
					resource.TestCheckNoResourceAttr("jenkins_credential_secret_text.foo", "secret"),
				),
			},
			{
				// Rotating the secret requires bumping the version trigger.
				Config: `
				resource jenkins_credential_secret_text foo {
				  name              = "test-secret-text-wo"
				  secret_wo         = "rotated-secret"
				  secret_wo_version = "2"
				}`,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsCredentialSecretTextExists("jenkins_credential_secret_text.foo", &cred),
					resource.TestCheckResourceAttr("jenkins_credential_secret_text.foo", "secret_wo_version", "2"),
					resource.TestCheckNoResourceAttr("jenkins_credential_secret_text.foo", "secret_wo"),
				),
			},
		},
	})
}

func TestAccJenkinsCredentialSecretText_writeOnlyConflict(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)},
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				// Setting both the plain and write-only secret must fail validation.
				Config: `
				resource jenkins_credential_secret_text foo {
				  name              = "test-secret-text-wo-conflict"
				  secret            = "plain"
				  secret_wo         = "written"
				  secret_wo_version = "1"
				}`,
				ExpectError: errInvalidAttributeCombination,
			},
			{
				// A write-only secret without its version trigger must fail validation.
				Config: `
				resource jenkins_credential_secret_text foo {
				  name      = "test-secret-text-wo-noversion"
				  secret_wo = "written"
				}`,
				ExpectError: errInvalidAttributeCombination,
			},
		},
	})
}

func TestAccJenkinsCredentialUsername_writeOnly(t *testing.T) {
	var cred jenkins.UsernameCredentials

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)},
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsCredentialUsernameDestroy,
		Steps: []resource.TestStep{
			{
				Config: `
				resource jenkins_credential_username foo {
				  name                = "test-username-wo"
				  username            = "foo"
				  password_wo         = "very-secret"
				  password_wo_version = "1"
				}`,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsCredentialUsernameExists("jenkins_credential_username.foo", &cred),
					resource.TestCheckResourceAttr("jenkins_credential_username.foo", "password_wo_version", "1"),
					resource.TestCheckNoResourceAttr("jenkins_credential_username.foo", "password_wo"),
					resource.TestCheckNoResourceAttr("jenkins_credential_username.foo", "password"),
				),
			},
			{
				Config: `
				resource jenkins_credential_username foo {
				  name                = "test-username-wo"
				  username            = "foo"
				  password_wo         = "rotated-secret"
				  password_wo_version = "2"
				}`,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsCredentialUsernameExists("jenkins_credential_username.foo", &cred),
					resource.TestCheckResourceAttr("jenkins_credential_username.foo", "password_wo_version", "2"),
				),
			},
		},
	})
}

func TestAccJenkinsCredentialSSH_writeOnly(t *testing.T) {
	var cred jenkins.SSHCredentials

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)},
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsCredentialSSHDestroy,
		Steps: []resource.TestStep{
			{
				Config: `
				resource jenkins_credential_ssh foo {
				  name                  = "test-ssh-wo"
				  username              = "foo"
				  privatekey_wo         = "Some fake private key"
				  privatekey_wo_version = "1"
				}`,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsCredentialSSHExists("jenkins_credential_ssh.foo", &cred),
					resource.TestCheckResourceAttr("jenkins_credential_ssh.foo", "privatekey_wo_version", "1"),
					resource.TestCheckNoResourceAttr("jenkins_credential_ssh.foo", "privatekey_wo"),
					resource.TestCheckNoResourceAttr("jenkins_credential_ssh.foo", "privatekey"),
				),
			},
			{
				Config: `
				resource jenkins_credential_ssh foo {
				  name                  = "test-ssh-wo"
				  username              = "foo"
				  privatekey_wo         = "Some other fake private key"
				  privatekey_wo_version = "2"
				}`,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsCredentialSSHExists("jenkins_credential_ssh.foo", &cred),
					resource.TestCheckResourceAttr("jenkins_credential_ssh.foo", "privatekey_wo_version", "2"),
				),
			},
		},
	})
}
