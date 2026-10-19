package jenkins

import (
	"context"
	"encoding/xml"
	"fmt"
	"regexp"
	"strings"
	"testing"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

// The acceptance tests below check against a real Jenkins that an in-place
// update of a credential keeps the secrets Jenkins stores for it. Jenkins'
// config.xml update replaces the whole credential, so a secret the provider
// does not re-send is erased. Jenkins never returns a secret, so the tests read
// what it holds in two ways that do not reveal the value:
//
//   - config.xml has an element for each secret Jenkins holds. An SSH
//     passphrase sent empty or left out is stored as none, so its element shows
//     whether it survived an update. Other secrets keep their element even when
//     empty.
//   - A certificate credential's display name is the subject of the
//     certificate in its keystore, which Jenkins can read only with both the
//     keystore and its password intact.
//
// Where an update must fail instead, the tests check that it posted nothing:
// the description stored in Jenkins is unchanged.

var (
	errUpdateWouldErase = regexp.MustCompile("Update Would Erase Credential Secret")
	errSecretNotKnown   = regexp.MustCompile("Credential Secret Not Known to Terraform")
)

// testCertificateSubject is the subject of the certificate in
// testCertificateKeystoreB64.
const testCertificateSubject = "CN=terraform-provider-jenkins-test"

// testAccSecretUpdateName returns a unique credential name for these tests.
func testAccSecretUpdateName(kind string) string {
	return "tfacc-sec258-" + kind + "-" + acctest.RandStringFromCharSet(8, acctest.CharSetAlphaNum)
}

// testAccCreateCredentialInJenkins creates cred, named name, in the global
// domain of the system store directly through the API, as an operator setting
// it up within Jenkins would, and deletes it when the test ends unless
// Terraform already has.
func testAccCreateCredentialInJenkins(t *testing.T, name string, cred interface{}) {
	t.Helper()
	ctx := context.Background()
	if err := testAccClient.Credentials().Add(ctx, defaultCredentialDomain, cred); err != nil {
		t.Fatalf("creating credential %s in Jenkins: %v", name, err)
	}
	t.Cleanup(func() {
		_ = testAccClient.Credentials().Delete(ctx, defaultCredentialDomain, name)
	})
}

// storedCredentialElements is a credential's config.xml as Jenkins returns it.
type storedCredentialElements struct {
	Description string `xml:"description"`
	Children    []struct {
		XMLName xml.Name
	} `xml:",any"`
}

func (c storedCredentialElements) has(element string) bool {
	for _, child := range c.Children {
		if child.XMLName.Local == element {
			return true
		}
	}
	return false
}

// testAccCheckStoredCredential checks the credential resourceName manages as
// Jenkins stores it: its description, the secret elements it must have (held)
// and those it must not (notHeld).
func testAccCheckStoredCredential(resourceName, wantDescription string, held, notHeld []string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("%s not found", resourceName)
		}
		manager := testAccClient.Credentials()
		manager.Folder = formatFolderName(rs.Primary.Attributes["folder"])
		var stored storedCredentialElements
		if err := manager.GetSingle(context.Background(), rs.Primary.Attributes["domain"], rs.Primary.Attributes["name"], &stored); err != nil {
			return fmt.Errorf("reading %s from Jenkins: %w", resourceName, err)
		}
		if stored.Description != wantDescription {
			return fmt.Errorf("description stored in Jenkins = %q, want %q", stored.Description, wantDescription)
		}
		for _, el := range held {
			if !stored.has(el) {
				return fmt.Errorf("Jenkins holds no <%s> for %s: the secret was erased", el, resourceName)
			}
		}
		for _, el := range notHeld {
			if stored.has(el) {
				return fmt.Errorf("Jenkins still holds <%s> for %s, want it cleared", el, resourceName)
			}
		}
		return nil
	}
}

// testAccCheckCertificateDisplayName checks the display name Jenkins gives the
// certificate credential resourceName manages, in the system store.
func testAccCheckCertificateDisplayName(resourceName, want string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("%s not found", resourceName)
		}
		var got struct {
			DisplayName string `json:"displayName"`
		}
		endpoint := "/credentials/store/system/domain/" + rs.Primary.Attributes["domain"] + "/credential/" + rs.Primary.Attributes["name"] + "/api/json"
		if _, err := testAccClient.Requester.GetJSON(context.Background(), endpoint, &got, nil); err != nil {
			return fmt.Errorf("reading %s from Jenkins: %w", resourceName, err)
		}
		if got.DisplayName != want {
			return fmt.Errorf("display name in Jenkins = %q, want %q: the keystore or its password was erased", got.DisplayName, want)
		}
		return nil
	}
}

// testAccCheckStateLacks checks that no attribute of any resource in state
// holds value.
func testAccCheckStateLacks(value string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		for name, rs := range s.RootModule().Resources {
			for attr, v := range rs.Primary.Attributes {
				if strings.Contains(v, value) {
					return fmt.Errorf("state of %s has %q in %s", name, value, attr)
				}
			}
		}
		return nil
	}
}

func TestAccJenkinsCredentialSSH_updateKeepsPassphrase(t *testing.T) {
	name := testAccSecretUpdateName("ssh-keep")
	config := func(description, passphrase string) string {
		return fmt.Sprintf(`
		resource jenkins_credential_ssh foo {
		  name        = %q
		  description = %q
		  username    = "tfacc-user"
		  privatekey  = "Some fake private key"
		  %s
		}`, name, description, passphrase)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsCredentialSSHDestroy,
		Steps: []resource.TestStep{
			{
				Config: config("old", `passphrase = "SuperSecret"`),
				Check:  testAccCheckStoredCredential("jenkins_credential_ssh.foo", "old", []string{"passphrase"}, nil),
			},
			{
				// A description-only update re-sends the passphrase.
				Config: config("new", `passphrase = "SuperSecret"`),
				Check:  testAccCheckStoredCredential("jenkins_credential_ssh.foo", "new", []string{"passphrase"}, nil),
			},
			{
				// Removing the passphrase Terraform set clears it.
				Config: config("new", ""),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStoredCredential("jenkins_credential_ssh.foo", "new", nil, []string{"passphrase"}),
					resource.TestCheckNoResourceAttr("jenkins_credential_ssh.foo", "passphrase"),
				),
			},
		},
	})
}

func TestAccJenkinsCredentialCertificate_updateKeepsKeystore(t *testing.T) {
	name := testAccSecretUpdateName("cert-keep")
	config := func(description string) string {
		return fmt.Sprintf(`
		resource jenkins_credential_certificate foo {
		  name        = %q
		  description = %q
		  keystore    = %q
		  password    = "changeit"
		}`, name, description, testCertificateKeystoreB64)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsCredentialCertificateDestroy,
		Steps: []resource.TestStep{
			{
				Config: config("old"),
				Check:  testAccCheckCertificateDisplayName("jenkins_credential_certificate.foo", testCertificateSubject+" (old)"),
			},
			{
				// A description-only update re-sends the keystore and password.
				Config: config("new"),
				Check:  testAccCheckCertificateDisplayName("jenkins_credential_certificate.foo", testCertificateSubject+" (new)"),
			},
		},
	})
}

func TestAccJenkinsCredentialCertificate_updateKeepsWriteOnlyKeystore(t *testing.T) {
	name := testAccSecretUpdateName("cert-wo-keep")
	config := func(description string) string {
		return fmt.Sprintf(`
		resource jenkins_credential_certificate foo {
		  name                = %q
		  description         = %q
		  keystore_wo         = %q
		  keystore_wo_version = "1"
		  password            = "changeit"
		}`, name, description, testCertificateKeystoreB64)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		TerraformVersionChecks:   []tfversion.TerraformVersionCheck{tfversion.SkipBelow(tfversion.Version1_11_0)},
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsCredentialCertificateDestroy,
		Steps: []resource.TestStep{
			{
				Config: config("old"),
				Check:  testAccCheckCertificateDisplayName("jenkins_credential_certificate.foo", testCertificateSubject+" (old)"),
			},
			{
				// A description-only update re-sends the write-only keystore
				// although its version is unchanged, and keeps it out of state.
				Config: config("new"),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckCertificateDisplayName("jenkins_credential_certificate.foo", testCertificateSubject+" (new)"),
					resource.TestCheckNoResourceAttr("jenkins_credential_certificate.foo", "keystore_wo"),
					resource.TestCheckNoResourceAttr("jenkins_credential_certificate.foo", "keystore"),
				),
			},
		},
	})
}

// testAccImportedCredentialSteps returns the steps of a test that imports a
// credential created within Jenkins by create, using the configuration
// imported, then applies update, which changes the description to "new" and
// must fail with wantErr, and finally re-applies imported and runs checks: the
// failed update must have left Jenkins as it was. The configurations set
// folder = "": import stores the system store as "", and a null folder would
// plan a replacement instead of an update.
func testAccImportedCredentialSteps(resourceName, importID string, create func(), imported, update string, wantErr *regexp.Regexp, checks ...resource.TestCheckFunc) []resource.TestStep {
	return []resource.TestStep{
		{
			PreConfig:          create,
			Config:             imported,
			ResourceName:       resourceName,
			ImportState:        true,
			ImportStateId:      importID,
			ImportStatePersist: true,
		},
		{
			Config: update,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate)},
			},
			ExpectError: wantErr,
		},
		{
			// Nothing to change: the checks see what the failed update left.
			Config: imported,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
			},
			Check: resource.ComposeTestCheckFunc(checks...),
		},
	}
}

func TestAccJenkinsCredentialUsername_updateKeepsPasswordSetInJenkins(t *testing.T) {
	name := testAccSecretUpdateName("user-imp")
	const resourceName = "jenkins_credential_username.foo"
	config := func(description string) string {
		return fmt.Sprintf(`
		resource jenkins_credential_username foo {
		  name        = %q
		  folder      = ""
		  description = %q
		  username    = "tfacc-user"
		}`, name, description)
	}
	create := func() {
		testAccCreateCredentialInJenkins(t, name, &jenkins.UsernameCredentials{
			ID: name, Scope: "GLOBAL", Description: "old", Username: "tfacc-user", Password: "set-in-jenkins",
		})
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsCredentialUsernameDestroy,
		Steps: testAccImportedCredentialSteps(resourceName, "_/"+name, create, config("old"), config("new"), errUpdateWouldErase,
			testAccCheckStoredCredential(resourceName, "old", []string{"password"}, nil),
			resource.TestCheckNoResourceAttr(resourceName, "password"),
		),
	})
}

func TestAccJenkinsCredentialVaultAppRole_updateKeepsSecretIDSetInJenkins(t *testing.T) {
	name := testAccSecretUpdateName("vault-imp")
	const resourceName = "jenkins_credential_vault_approle.foo"
	config := func(description string) string {
		return fmt.Sprintf(`
		resource jenkins_credential_vault_approle foo {
		  name        = %q
		  folder      = ""
		  description = %q
		  role_id     = "tfacc-role"
		}`, name, description)
	}
	create := func() {
		testAccCreateCredentialInJenkins(t, name, &VaultAppRoleCredentials{
			ID: name, Scope: "GLOBAL", Description: "old", Path: "approle", RoleID: "tfacc-role", SecretID: "set-in-jenkins",
		})
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsCredentialVaultAppRoleDestroy,
		Steps: testAccImportedCredentialSteps(resourceName, "_/"+name, create, config("old"), config("new"), errUpdateWouldErase,
			testAccCheckStoredCredential(resourceName, "old", []string{"secretId"}, nil),
			resource.TestCheckNoResourceAttr(resourceName, "secret_id"),
		),
	})
}

func TestAccJenkinsCredentialSSH_updateKeepsPassphraseSetInJenkins(t *testing.T) {
	name := testAccSecretUpdateName("ssh-imp")
	const resourceName = "jenkins_credential_ssh.foo"
	// The update sends the configured private key; the imported configuration
	// ignores it, as Terraform has no value for it after the import.
	config := func(description, lifecycle string) string {
		return fmt.Sprintf(`
		resource jenkins_credential_ssh foo {
		  name        = %q
		  folder      = ""
		  description = %q
		  username    = "tfacc-user"
		  privatekey  = "Some fake private key"
		  %s
		}`, name, description, lifecycle)
	}
	create := func() {
		testAccCreateCredentialInJenkins(t, name, &jenkins.SSHCredentials{
			ID: name, Scope: "GLOBAL", Description: "old", Username: "tfacc-user", Passphrase: "set-in-jenkins",
			PrivateKeySource: &jenkins.PrivateKey{Class: jenkins.KeySourceDirectEntryType, Value: "Some fake private key"},
		})
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsCredentialSSHDestroy,
		Steps: testAccImportedCredentialSteps(resourceName, "_/"+name, create,
			config("old", "lifecycle {\n ignore_changes = [privatekey]\n }"), config("new", ""), errUpdateWouldErase,
			testAccCheckStoredCredential(resourceName, "old", []string{"passphrase"}, nil),
			resource.TestCheckNoResourceAttr(resourceName, "passphrase"),
		),
	})
}

func TestAccJenkinsCredentialSecretText_updateKeepsIgnoredSecret(t *testing.T) {
	name := testAccSecretUpdateName("text-imp")
	const resourceName = "jenkins_credential_secret_text.foo"
	config := func(description string) string {
		return fmt.Sprintf(`
		resource jenkins_credential_secret_text foo {
		  name        = %q
		  folder      = ""
		  description = %q
		  secret      = "tfacc-placeholder"

		  lifecycle {
		    ignore_changes = [secret]
		  }
		}`, name, description)
	}
	create := func() {
		testAccCreateCredentialInJenkins(t, name, &jenkins.StringCredentials{
			ID: name, Scope: "GLOBAL", Description: "old", Secret: "set-in-jenkins",
		})
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsCredentialSecretTextDestroy,
		Steps: testAccImportedCredentialSteps(resourceName, "_/"+name, create, config("old"), config("new"), errSecretNotKnown,
			testAccCheckStoredCredential(resourceName, "old", []string{"secret"}, nil),
			resource.TestCheckNoResourceAttr(resourceName, "secret"),
			testAccCheckStateLacks("tfacc-placeholder"),
		),
	})
}

func TestAccJenkinsCredentialCertificate_updateKeepsIgnoredPassword(t *testing.T) {
	name := testAccSecretUpdateName("cert-imp")
	const resourceName = "jenkins_credential_certificate.foo"
	// The update sends the configured keystore and ignores the password; the
	// imported configuration ignores both, as Terraform has no value for
	// either after the import.
	config := func(description, ignored string) string {
		return fmt.Sprintf(`
		resource jenkins_credential_certificate foo {
		  name        = %q
		  folder      = ""
		  description = %q
		  keystore    = %q
		  password    = "tfacc-placeholder"

		  lifecycle {
		    ignore_changes = [%s]
		  }
		}`, name, description, testCertificateKeystoreB64, ignored)
	}
	create := func() {
		testAccCreateCredentialInJenkins(t, name, &CertificateCredentials{
			ID: name, Scope: "GLOBAL", Description: "old", Password: "changeit",
			KeyStoreSource: certificateKeyStoreSource{Class: uploadedKeyStoreSourceClass, UploadedKeystore: testCertificateKeystoreB64},
		})
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsCredentialCertificateDestroy,
		Steps: testAccImportedCredentialSteps(resourceName, "_/"+name, create,
			config("old", "keystore, password"), config("new", "password"), errSecretNotKnown,
			testAccCheckCertificateDisplayName(resourceName, testCertificateSubject+" (old)"),
			resource.TestCheckNoResourceAttr(resourceName, "password"),
			testAccCheckStateLacks("tfacc-placeholder"),
		),
	})
}
