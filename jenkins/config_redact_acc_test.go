package jenkins

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Acceptance tests for #255 against a real Jenkins: a folder credential added
// with jenkins_credential_secret_text puts its {AQAA...} ciphertext in the
// folder's config.xml, and no template attribute may carry it, or the folder
// credentials store, into state. Updates must still post the store back, so
// the credential survives with the very same ciphertext.

// errRedactedTemplate matches redactedSecretValidator's diagnostic summary.
var errRedactedTemplate = regexp.MustCompile("Template contains a redacted secret")

// testAccFolderJobXML is a folder's config.xml as Jenkins writes it once a
// credential has been added, without the credentials store, so a jenkins_job
// managing the folder has nothing to reconcile after a refresh.
const testAccFolderJobXML = `<com.cloudbees.hudson.plugins.folder.Folder>
  <actions/>
  <description>%s</description>
  <properties/>
  <folderViews class="com.cloudbees.hudson.plugins.folder.views.DefaultFolderViewHolder">
    <views>
      <hudson.model.AllView>
        <owner class="com.cloudbees.hudson.plugins.folder.Folder" reference="../../../.."/>
        <name>All</name>
        <filterExecutors>false</filterExecutors>
        <filterQueue>false</filterQueue>
        <properties class="hudson.model.View$PropertyList"/>
      </hudson.model.AllView>
    </views>
    <tabBar class="hudson.views.DefaultViewsTabBar"/>
  </folderViews>
  <healthMetrics/>
  <icon class="com.cloudbees.hudson.plugins.folder.icons.StockFolderIcon"/>
</com.cloudbees.hudson.plugins.folder.Folder>`

// testAccPasswordParameterJobXML is a freestyle job with a password parameter,
// whose default Jenkins stores encrypted.
const testAccPasswordParameterJobXML = `<project>
  <description>password parameter</description>
  <properties>
    <hudson.model.ParametersDefinitionProperty>
      <parameterDefinitions>
        <hudson.model.PasswordParameterDefinition>
          <name>TOKEN</name>
          <defaultValue>%s</defaultValue>
        </hudson.model.PasswordParameterDefinition>
      </parameterDefinitions>
    </hudson.model.ParametersDefinitionProperty>
  </properties>
  <builders/>
  <disabled>false</disabled>
</project>`

func TestAccJenkinsFolder_templateKeepsSecretsOut(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	name := "tfacc-sec257-folder-" + randString
	credID := "tfacc-sec257-token-" + randString
	secret := "tfacc-sec257-plaintext-" + randString
	config := func(description string) string {
		return fmt.Sprintf(`
				resource jenkins_folder foo {
				  name        = %q
				  description = %q
				}

				resource jenkins_credential_secret_text token {
				  name   = %q
				  folder = jenkins_folder.foo.id
				  secret = %q
				}

				data jenkins_folder foo {
				  name       = jenkins_folder.foo.name
				  depends_on = [jenkins_credential_secret_text.token]
				}

				data jenkins_job foo {
				  name       = jenkins_folder.foo.name
				  depends_on = [jenkins_credential_secret_text.token]
				}`, name, description, credID, secret)
	}

	var ciphertext string
	noSecrets := testAccCheckTemplatesHaveNoSecrets(secret, "jenkins_folder.foo", "data.jenkins_folder.foo", "data.jenkins_job.foo")
	credentialKept := testAccCheckFolderCredentialCiphertext("jenkins_folder.foo", credID, &ciphertext)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy: resource.ComposeTestCheckFunc(
			testAccCheckJenkinsCredentialSecretTextDestroy,
			testAccCheckJenkinsFolderDestroy,
		),
		Steps: []resource.TestStep{
			{
				// The data sources are read once the credential exists.
				Config: config("created"),
				Check: resource.ComposeTestCheckFunc(
					credentialKept,
					testAccCheckTemplatesHaveNoSecrets(secret, "data.jenkins_folder.foo", "data.jenkins_job.foo"),
				),
			},
			{
				// The folder's own template, refreshed from a config.xml that now
				// holds the credential.
				RefreshState: true,
				Check:        resource.ComposeTestCheckFunc(noSecrets, credentialKept),
			},
			{
				// Update must post the store Jenkins has, not the redacted template.
				Config: config("updated"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_folder.foo", "description", "updated"),
					noSecrets,
					credentialKept,
				),
			},
			{
				Config: config("updated"),
				Check:  resource.ComposeTestCheckFunc(noSecrets, credentialKept),
			},
			{
				ResourceName:     "jenkins_folder.foo",
				ImportState:      true,
				ImportStateCheck: testAccCheckImportedTemplateHasNoSecrets(secret),
			},
		},
	})
}

// TestAccJenkinsJob_folderTemplateKeepsSecretsOut covers a folder managed as a
// jenkins_job whose template does not contain the credentials store: the store
// a jenkins_credential_* resource adds is not managed by the template, so it is
// left out of state and kept by template updates.
func TestAccJenkinsJob_folderTemplateKeepsSecretsOut(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	name := "tfacc-sec257-job-" + randString
	credID := "tfacc-sec257-token-" + randString
	secret := "tfacc-sec257-plaintext-" + randString
	config := func(description string) string {
		return fmt.Sprintf(`
resource jenkins_job foo {
  name     = %q
  template = <<EOT
%s
EOT
}

resource jenkins_credential_secret_text token {
  name   = %q
  folder = jenkins_job.foo.id
  secret = %q
}`, name, fmt.Sprintf(testAccFolderJobXML, description), credID, secret)
	}

	var ciphertext string
	noSecrets := testAccCheckTemplatesHaveNoSecrets(secret, "jenkins_job.foo")
	credentialKept := testAccCheckFolderCredentialCiphertext("jenkins_job.foo", credID, &ciphertext)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy: resource.ComposeTestCheckFunc(
			testAccCheckJenkinsCredentialSecretTextDestroy,
			testAccCheckJenkinsJobDestroy,
		),
		Steps: []resource.TestStep{
			{
				Config: config("created"),
				Check:  credentialKept,
			},
			{
				RefreshState: true,
				Check:        resource.ComposeTestCheckFunc(noSecrets, credentialKept),
			},
			{
				// The template never contained the store, so editing it must not
				// delete the credential.
				Config: config("updated"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestMatchResourceAttr("jenkins_job.foo", "template", regexp.MustCompile("<description>updated</description>")),
					noSecrets,
					credentialKept,
				),
			},
			{
				RefreshState: true,
				Check:        resource.ComposeTestCheckFunc(noSecrets, credentialKept),
			},
			{
				ResourceName:     "jenkins_job.foo",
				ImportState:      true,
				ImportStateCheck: testAccCheckImportedTemplateHasNoSecrets(secret),
			},
		},
	})
}

// TestAccJenkinsJob_rejectsRedactedTemplate covers copying a jenkins_job data
// source's template, whose encrypted values read {redacted}, into a jenkins_job:
// Jenkins would store the marker itself as the secret, so the copy must be
// rejected before it is created, whether the data source is read at plan or,
// when it depends on a resource still to be created, only at apply.
func TestAccJenkinsJob_rejectsRedactedTemplate(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	source := "tfacc-sec257-src-" + randString
	copied := "tfacc-sec257-copy-" + randString
	literal := "tfacc-sec257-literal-" + randString
	secret := "tfacc-sec257-plaintext-" + randString

	sourceConfig := fmt.Sprintf(`
resource jenkins_job src {
  name     = %q
  template = <<EOT
%s
EOT

  lifecycle {
    # Jenkins stores the parameter default encrypted.
    ignore_changes = [template]
  }
}

data jenkins_job src {
  name = jenkins_job.src.name
}`, source, fmt.Sprintf(testAccPasswordParameterJobXML, secret))
	copyConfig := sourceConfig + fmt.Sprintf(`

resource jenkins_job copy {
  name     = %q
  template = data.jenkins_job.src.template
}`, copied)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsJobDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource jenkins_job literal {
  name     = %q
  template = <<EOT
%s
EOT
}`, literal, fmt.Sprintf(testAccPasswordParameterJobXML, "{redacted}")),
				ExpectError: errRedactedTemplate,
			},
			{
				// The data source waits for src, so the copied template is
				// unknown at plan and only validated at apply.
				Config:      copyConfig,
				ExpectError: errRedactedTemplate,
			},
			{
				Config: sourceConfig,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrWith("data.jenkins_job.src", "template", func(v string) error {
						if !strings.Contains(v, "<defaultValue>{redacted}</defaultValue>") {
							return fmt.Errorf("data source template does not redact the parameter default:\n%s", v)
						}
						return nil
					}),
					testAccCheckTemplatesHaveNoSecrets(secret, "data.jenkins_job.src"),
					testAccCheckJenkinsJobAbsent(copied),
					testAccCheckJenkinsJobAbsent(literal),
				),
			},
			{
				// Read at plan this time.
				Config:      copyConfig,
				ExpectError: errRedactedTemplate,
			},
			{
				Config: sourceConfig,
				Check:  testAccCheckJenkinsJobAbsent(copied),
			},
		},
	})
}

// testAccCheckTemplatesHaveNoSecrets fails when the template attribute of any
// of addrs (resources or data sources) carries the folder credentials store,
// Jenkins ciphertext, or plaintext.
func testAccCheckTemplatesHaveNoSecrets(plaintext string, addrs ...string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		for _, addr := range addrs {
			rs, ok := s.RootModule().Resources[addr]
			if !ok {
				return fmt.Errorf("resource not found: %s", addr)
			}
			if err := testAccTemplateHasNoSecrets(addr, rs.Primary.Attributes["template"], plaintext); err != nil {
				return err
			}
		}
		return nil
	}
}

// testAccCheckImportedTemplateHasNoSecrets is testAccCheckTemplatesHaveNoSecrets
// for the state an import produces.
func testAccCheckImportedTemplateHasNoSecrets(plaintext string) resource.ImportStateCheckFunc {
	return func(states []*terraform.InstanceState) error {
		if len(states) != 1 {
			return fmt.Errorf("import produced %d instances, want 1", len(states))
		}
		return testAccTemplateHasNoSecrets("imported "+states[0].ID, states[0].Attributes["template"], plaintext)
	}
}

func testAccTemplateHasNoSecrets(addr, template, plaintext string) error {
	if template == "" {
		return fmt.Errorf("%s has no template in state", addr)
	}
	for _, leak := range []string{"{AQAA", folderCredentialsPropertyElementName, plaintext} {
		if strings.Contains(template, leak) {
			return fmt.Errorf("%s template in state contains %q:\n%s", addr, leak, template)
		}
	}
	return nil
}

// testAccCheckFolderCredentialCiphertext reads the encrypted secret of
// credential id from the live config.xml of the folder at addr. The first call
// records it in *ciphertext; later ones fail unless the folder still holds
// exactly that ciphertext. An update that posted the template without the
// store, or with {redacted} in place of the secret, fails it.
func testAccCheckFolderCredentialCiphertext(addr, id string, ciphertext *string) resource.TestCheckFunc {
	reSecret := regexp.MustCompile(`<id>` + regexp.QuoteMeta(id) + `</id>[\s\S]*?<secret>(\{AQAA[^<]*\})</secret>`)
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[addr]
		if !ok {
			return fmt.Errorf("resource not found: %s", addr)
		}

		ctx := context.Background()
		name, folders := parseCanonicalJobID(rs.Primary.ID)
		job, err := testAccClient.GetJob(ctx, name, folders...)
		if err != nil {
			return err
		}
		config, err := job.GetConfig(ctx)
		if err != nil {
			return err
		}
		m := reSecret.FindStringSubmatch(config)
		switch {
		case m == nil:
			return fmt.Errorf("folder %s no longer holds an encrypted secret for credential %s", rs.Primary.ID, id)
		case *ciphertext == "":
			*ciphertext = m[1]
		case m[1] != *ciphertext:
			return fmt.Errorf("secret of credential %s in folder %s was replaced", id, rs.Primary.ID)
		}
		return nil
	}
}

// testAccCheckJenkinsJobAbsent fails unless Jenkins has no top-level item name.
func testAccCheckJenkinsJobAbsent(name string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		_, err := testAccClient.GetJob(context.Background(), name)
		switch {
		case err == nil:
			return fmt.Errorf("job %s exists", name)
		case !isNotFound(err):
			return err
		}
		return nil
	}
}
