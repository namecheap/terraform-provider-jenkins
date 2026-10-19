package jenkins

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Acceptance tests for issue #250 against a real Jenkins: a credential domain,
// ID or folder must not move a request out of the folder's credentials store.
// Jetty resolves the dot-segments that Go sends unresolved, so before the fix
// each of these reached the system store. Every object is named
// tfacc-sec252-<random>, so the tests can share a controller.

// testAccClimbingDomain climbs from a top-level folder's store domain to the
// system store's global domain: the six ".." undo domain, folder, store,
// credentials, <folder> and job.
const testAccClimbingDomain = "../../../../../../credentials/store/system/domain/_"

func testAccCredentialPathPrefix() string {
	return "tfacc-sec252-" + acctest.RandStringFromCharSet(8, acctest.CharSetAlphaNum)
}

// testAccCredentialPathDescription reads a credential's description straight
// from Jenkins, escaping the domain and ID itself so it does not depend on the
// provider code under test.
func testAccCredentialPathDescription(folder, domain, name string) (string, error) {
	var cred struct {
		Description string `xml:"description"`
	}
	cm := testAccClient.Credentials()
	cm.Folder = formatFolderName(folder)
	err := cm.GetSingle(context.Background(), url.PathEscape(domain), url.PathEscape(name), &cred)
	return cred.Description, err
}

// testAccCheckCredentialPathDescription fails unless the credential exists in
// Jenkins with the given description.
func testAccCheckCredentialPathDescription(folder, domain, name, want string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got, err := testAccCredentialPathDescription(folder, domain, name)
		if err != nil {
			return fmt.Errorf("reading credential %s/%s/%s: %w", folder, domain, name, err)
		}
		if got != want {
			return fmt.Errorf("credential %s/%s/%s has description %q, want %q", folder, domain, name, got, want)
		}
		return nil
	}
}

// testAccCheckCredentialPathDestroy checks that no credential resource from
// the test is left in Jenkins, looking each one up by its escaped path.
func testAccCheckCredentialPathDestroy(s *terraform.State) error {
	for _, rs := range s.RootModule().Resources {
		switch rs.Type {
		case "jenkins_credential_secret_text", "jenkins_credential_username":
		default:
			continue
		}
		a := rs.Primary.Attributes
		if _, err := testAccCredentialPathDescription(a["folder"], a["domain"], a["name"]); err == nil {
			return fmt.Errorf("credential %s/%s/%s still exists", a["folder"], a["domain"], a["name"])
		}
	}
	return nil
}

// TestAccJenkinsCredentialPath_crossStoreDomain is the issue #250
// reproduction: a folder-scoped credential whose domain climbs to the system
// store (or, without a dot-segment, to another credential in its own store)
// and ends in "?" overwrote that credential. It must fail at plan, and the
// targets must be left as they were.
func TestAccJenkinsCredentialPath_crossStoreDomain(t *testing.T) {
	prefix := testAccCredentialPathPrefix()
	base := fmt.Sprintf(`
resource jenkins_folder team {
  name = "%[1]s"
}

resource jenkins_credential_secret_text victim {
  name        = "%[1]s-victim"
  description = "victim"
  secret      = "original"
}

resource jenkins_credential_secret_text sibling {
  folder      = jenkins_folder.team.name
  name        = "%[1]s-sibling"
  description = "sibling"
  secret      = "original"
}
`, prefix)
	tenant := func(domain string) string {
		return base + fmt.Sprintf(`
resource jenkins_credential_secret_text tenant {
  folder      = jenkins_folder.team.name
  domain      = %q
  name        = "%s-tenant"
  description = "tenant credential"
  secret      = "attacker-chosen"
}
`, domain, prefix)
	}
	targetsUnchanged := resource.ComposeTestCheckFunc(
		testAccCheckCredentialPathDescription("", defaultCredentialDomain, prefix+"-victim", "victim"),
		testAccCheckCredentialPathDescription(prefix, defaultCredentialDomain, prefix+"-sibling", "sibling"),
	)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             resource.ComposeTestCheckFunc(testAccCheckCredentialPathDestroy, testAccCheckJenkinsFolderDestroy),
		Steps: []resource.TestStep{
			{
				Config: base,
				Check:  targetsUnchanged,
			},
			{
				Config:      tenant("../../../../../../credentials/store/system/domain/_/credential/" + prefix + "-victim/config.xml?"),
				ExpectError: regexp.MustCompile(`must\s+not\s+include\s+path\s+characters`),
			},
			{
				Config:      tenant("_/credential/" + prefix + "-sibling/config.xml?"),
				ExpectError: regexp.MustCompile(`must\s+not\s+include\s+path\s+characters`),
			},
			{
				// Both targets still hold their own values in Jenkins, and the
				// plan for them is empty.
				PreConfig: func() {
					if err := targetsUnchanged(nil); err != nil {
						t.Errorf("after the rejected tenants: %v", err)
					}
				},
				Config:   base,
				PlanOnly: true,
			},
		},
	})
}

// TestAccJenkinsCredentialPath_dataSourceClimbing: data sources have no
// plan-time check on domain or ID, so the runtime escaping alone stops a
// folder-scoped read from returning the system store's credentials.
func TestAccJenkinsCredentialPath_dataSourceClimbing(t *testing.T) {
	prefix := testAccCredentialPathPrefix()
	base := fmt.Sprintf(`
resource jenkins_folder team {
  name = "%[1]s"
}

resource jenkins_credential_username victim {
  name     = "%[1]s-victim"
  username = "%[1]s-leaked"
  password = "original"
}
`, prefix)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             resource.ComposeTestCheckFunc(testAccCheckCredentialPathDestroy, testAccCheckJenkinsFolderDestroy),
		Steps: []resource.TestStep{
			{
				// Control: the victim is readable from its own store.
				Config: base + `
data jenkins_credential_username direct {
  name = jenkins_credential_username.victim.name
}

data jenkins_credentials system {
  depends_on = [jenkins_credential_username.victim]
}
`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("data.jenkins_credential_username.direct", "username", prefix+"-leaked"),
					resource.TestCheckTypeSetElemAttr("data.jenkins_credentials.system", "credentials.*", prefix+"-victim"),
				),
			},
			{
				Config: base + fmt.Sprintf(`
data jenkins_credential_username leak {
  folder = jenkins_folder.team.name
  domain = %q
  name   = jenkins_credential_username.victim.name
}
`, testAccClimbingDomain),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+Read\s+Data\s+Source`),
			},
			{
				// The ID climbs from .../domain/_/credential/ instead.
				Config: base + `
data jenkins_credential_username leak {
  folder = jenkins_folder.team.name
  name   = "../../../../../../../../credentials/store/system/domain/_/credential/${jenkins_credential_username.victim.name}"
}
`,
				ExpectError: regexp.MustCompile(`Unable\s+to\s+Read\s+Data\s+Source`),
			},
			{
				Config: base + fmt.Sprintf(`
data jenkins_credentials leak {
  folder = jenkins_folder.team.name
  domain = %q
}
`, testAccClimbingDomain),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+List\s+Credentials`),
			},
		},
	})
}

// TestAccJenkinsCredentialPath_folderMustNameFolder: a folder such as "job" or
// "/" formats to nothing, which gojenkins takes as the system store. Each
// attribute that selects a credentials store must reject it at plan.
func TestAccJenkinsCredentialPath_folderMustNameFolder(t *testing.T) {
	prefix := testAccCredentialPathPrefix()
	invalidFolder := regexp.MustCompile(`Invalid\s+Folder\s+Path`)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             resource.ComposeTestCheckFunc(testAccCheckCredentialPathDestroy, testAccCheckJenkinsCredentialDomainDestroy),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource jenkins_credential_secret_text misplaced {
  folder = "job"
  name   = "%s-misplaced"
  secret = "s"
}
`, prefix),
				PlanOnly:    true,
				ExpectError: invalidFolder,
			},
			{
				Config: fmt.Sprintf(`
resource jenkins_credential_secret_text misplaced {
  folder = "%s/.."
  name   = "%s-misplaced"
  secret = "s"
}
`, prefix, prefix),
				PlanOnly:    true,
				ExpectError: invalidFolder,
			},
			{
				Config: fmt.Sprintf(`
resource jenkins_credential_domain misplaced {
  folder = "/job/"
  name   = "%s-misplaced"
}
`, prefix),
				PlanOnly:    true,
				ExpectError: invalidFolder,
			},
			{
				Config: fmt.Sprintf(`
data jenkins_credential_secret_text misplaced {
  folder = "/"
  name   = "%s-misplaced"
}
`, prefix),
				PlanOnly:    true,
				ExpectError: invalidFolder,
			},
			{
				Config: `
data jenkins_credentials misplaced {
  folder = "job"
}
`,
				PlanOnly:    true,
				ExpectError: invalidFolder,
			},
		},
	})
}

// TestAccJenkinsCredentialPath_importIDs: import IDs skip plan validation.
// Before the fix "job/_/<id>" imported a system credential into a resource
// whose destroy would delete it, and a ".." domain or ID read another URL.
func TestAccJenkinsCredentialPath_importIDs(t *testing.T) {
	prefix := testAccCredentialPathPrefix()
	config := fmt.Sprintf(`
resource jenkins_folder team {
  name = "%[1]s"
}

resource jenkins_credential_secret_text victim {
  name   = "%[1]s-victim"
  secret = "original"
}

resource jenkins_credential_secret_text cred {
  folder = jenkins_folder.team.name
  name   = "%[1]s-cred"
  secret = "original"
}

resource jenkins_credential_domain dom {
  folder = jenkins_folder.team.name
  name   = "%[1]s-dom"
}
`, prefix)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy: resource.ComposeTestCheckFunc(
			testAccCheckCredentialPathDestroy,
			testAccCheckJenkinsCredentialDomainDestroy,
			testAccCheckJenkinsFolderDestroy,
		),
		Steps: []resource.TestStep{
			{Config: config},
			{
				// Control: the folder credential imports by its plain ID.
				ResourceName:            "jenkins_credential_secret_text.cred",
				ImportState:             true,
				ImportStateId:           prefix + "/_/" + prefix + "-cred",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"secret"},
			},
			{
				ResourceName:  "jenkins_credential_secret_text.cred",
				ImportState:   true,
				ImportStateId: "job/_/" + prefix + "-victim",
				ExpectError:   regexp.MustCompile(`does\s+not\s+name\s+a\s+folder`),
			},
			{
				ResourceName:  "jenkins_credential_secret_text.cred",
				ImportState:   true,
				ImportStateId: prefix + "/../" + prefix + "-cred",
				ExpectError:   regexp.MustCompile(`invalid\s+credential\s+domain`),
			},
			{
				ResourceName:  "jenkins_credential_secret_text.cred",
				ImportState:   true,
				ImportStateId: prefix + "/_/..",
				ExpectError:   regexp.MustCompile(`invalid\s+credential\s+name`),
			},
			{
				ResourceName:  "jenkins_credential_domain.dom",
				ImportState:   true,
				ImportStateId: prefix + "/..",
				ExpectError:   regexp.MustCompile(`is\s+not\s+a\s+valid\s+URL\s+path\s+segment`),
			},
		},
	})
}

// TestAccJenkinsCredentialPath_specialCharacterDomain: a domain name may
// contain "?" and "#". Escaped, they reach Jenkins as part of the one domain
// segment, so the credential lands in that domain, reads back without drift
// and imports; unescaped, the "#" or "?" cut the URL short.
func TestAccJenkinsCredentialPath_specialCharacterDomain(t *testing.T) {
	prefix := testAccCredentialPathPrefix()
	domain := prefix + " C# builds?"
	config := fmt.Sprintf(`
resource jenkins_folder team {
  name = "%[1]s"
}

resource jenkins_credential_domain dom {
  folder = jenkins_folder.team.name
  name   = %[2]q
}

resource jenkins_credential_secret_text cred {
  folder      = jenkins_folder.team.name
  domain      = jenkins_credential_domain.dom.name
  name        = "%[1]s-cred"
  description = "in the special domain"
  secret      = "original"
}
`, prefix, domain)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy: resource.ComposeTestCheckFunc(
			testAccCheckCredentialPathDestroy,
			testAccCheckJenkinsCredentialDomainDestroy,
			testAccCheckJenkinsFolderDestroy,
		),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsCredentialDomainExists("jenkins_credential_domain.dom"),
					testAccCheckCredentialPathDescription(prefix, domain, prefix+"-cred", "in the special domain"),
					resource.TestCheckResourceAttr("jenkins_credential_secret_text.cred", "domain", domain),
				),
			},
			{
				// Re-apply: the refresh must find the credential in the same domain.
				Config: config,
				Check:  testAccCheckCredentialPathDescription(prefix, domain, prefix+"-cred", "in the special domain"),
			},
			{
				ResourceName:            "jenkins_credential_secret_text.cred",
				ImportState:             true,
				ImportStateId:           prefix + "/" + domain + "/" + prefix + "-cred",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"secret"},
			},
		},
	})
}
