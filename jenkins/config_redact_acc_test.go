package jenkins

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
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
	credentialKept := testAccCheckFolderCredentialCiphertext("jenkins_folder.foo", credID, "secret", &ciphertext)

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
	credentialKept := testAccCheckFolderCredentialCiphertext("jenkins_job.foo", credID, "secret", &ciphertext)

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

// TestAccJenkinsFolder_secretFileKeepsSecretsOut covers folder credentials
// stored as SecretBytes (jenkins_credential_secret_file), whose braced base64
// ciphertext has no {AQAA prefix, so testAccCheckTemplatesHaveNoSecrets cannot
// recognize it by its shape. The ciphertext is read back from Jenkins and
// looked for in every template instead, for a folder managed by jenkins_folder
// and one managed by a jenkins_job template without the store.
func TestAccJenkinsFolder_secretFileKeepsSecretsOut(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	folderName := "tfacc-sec257-sbfolder-" + randString
	jobName := "tfacc-sec257-sbjob-" + randString
	folderCredID := "tfacc-sec257-folderfile-" + randString
	jobCredID := "tfacc-sec257-jobfile-" + randString
	secret := "tfacc-sec257-plaintext-" + randString
	config := func(description string) string {
		return fmt.Sprintf(`
resource jenkins_folder foo {
  name        = %q
  description = %q
}

resource jenkins_credential_secret_file foo {
  name        = %q
  folder      = jenkins_folder.foo.id
  filename    = "secret.txt"
  secretbytes = base64encode(%q)
}

resource jenkins_job bar {
  name     = %q
  template = <<EOT
%s
EOT
}

resource jenkins_credential_secret_file bar {
  name        = %q
  folder      = jenkins_job.bar.id
  filename    = "secret.txt"
  secretbytes = base64encode(%q)
}

data jenkins_folder foo {
  name       = jenkins_folder.foo.name
  depends_on = [jenkins_credential_secret_file.foo]
}

data jenkins_job foo {
  name       = jenkins_folder.foo.name
  depends_on = [jenkins_credential_secret_file.foo]
}`, folderName, description, folderCredID, secret, jobName, fmt.Sprintf(testAccFolderJobXML, description), jobCredID, secret)
	}

	var folderCiphertext, jobCiphertext string
	folderFileKept := testAccCheckFolderCredentialCiphertext("jenkins_folder.foo", folderCredID, "secretBytes", &folderCiphertext)
	jobFileKept := testAccCheckFolderCredentialCiphertext("jenkins_job.bar", jobCredID, "secretBytes", &jobCiphertext)
	// The premise: SecretBytes ciphertext does not look like a Secret's.
	noSecretPrefix := func(*terraform.State) error {
		for _, c := range []string{folderCiphertext, jobCiphertext} {
			if strings.HasPrefix(c, "{AQAA") {
				return fmt.Errorf("SecretBytes ciphertext %s has the {AQAA prefix; this test no longer covers prefix-less ciphertext", c)
			}
		}
		return nil
	}
	// Checked before testAccCheckTemplatesHaveNoSecrets, so a leak is reported
	// as the ciphertext itself.
	noFileCiphertext := resource.ComposeTestCheckFunc(
		testAccCheckTemplatesLackCiphertext(&folderCiphertext, "jenkins_folder.foo", "data.jenkins_folder.foo", "data.jenkins_job.foo"),
		testAccCheckTemplatesLackCiphertext(&jobCiphertext, "jenkins_job.bar"),
		testAccCheckTemplatesHaveNoSecrets(secret, "jenkins_folder.foo", "data.jenkins_folder.foo", "data.jenkins_job.foo", "jenkins_job.bar"),
	)
	filesKept := resource.ComposeTestCheckFunc(folderFileKept, jobFileKept)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy: resource.ComposeTestCheckFunc(
			testAccCheckJenkinsCredentialSecretFileDestroy,
			testAccCheckJenkinsFolderDestroy,
			testAccCheckJenkinsJobDestroy,
		),
		Steps: []resource.TestStep{
			{
				Config: config("created"),
				Check: resource.ComposeTestCheckFunc(
					filesKept,
					noSecretPrefix,
					testAccCheckTemplatesLackCiphertext(&folderCiphertext, "data.jenkins_folder.foo", "data.jenkins_job.foo"),
					testAccCheckTemplatesHaveNoSecrets(secret, "data.jenkins_folder.foo", "data.jenkins_job.foo"),
				),
			},
			{
				RefreshState: true,
				Check:        resource.ComposeTestCheckFunc(noFileCiphertext, filesKept),
			},
			{
				// Both updates must post the store Jenkins has, SecretBytes and all.
				Config: config("updated"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_folder.foo", "description", "updated"),
					resource.TestMatchResourceAttr("jenkins_job.bar", "template", regexp.MustCompile("<description>updated</description>")),
					noFileCiphertext,
					filesKept,
				),
			},
			{
				Config: config("updated"),
				Check:  resource.ComposeTestCheckFunc(noFileCiphertext, filesKept),
			},
			{
				ResourceName: "jenkins_folder.foo",
				ImportState:  true,
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if err := testAccCheckImportedTemplateHasNoSecrets(secret)(states); err != nil {
						return err
					}
					return testAccTemplateLacksCiphertext("imported "+states[0].ID, states[0].Attributes["template"], folderCiphertext)
				},
			},
			{
				ResourceName: "jenkins_job.bar",
				ImportState:  true,
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if err := testAccCheckImportedTemplateHasNoSecrets(secret)(states); err != nil {
						return err
					}
					return testAccTemplateLacksCiphertext("imported "+states[0].ID, states[0].Attributes["template"], jobCiphertext)
				},
			},
		},
	})
}

// testAccManagedFolderCredentialsXML is a folder's <properties> holding a
// credentials store with no secrets, only a credentials domain named by %s, so
// a jenkins_job template can manage the store without carrying ciphertext:
// Jenkins encrypts a plain-text secret when it saves the folder, so a template
// giving one would never again match the refreshed configuration.
const testAccManagedFolderCredentialsXML = `<properties>
    <com.cloudbees.hudson.plugins.folder.properties.FolderCredentialsProvider_-FolderCredentialsProperty>
      <domainCredentialsMap class="hudson.util.CopyOnWriteMap$Hash">
        <entry>
          <com.cloudbees.plugins.credentials.domains.Domain>
            <specifications/>
          </com.cloudbees.plugins.credentials.domains.Domain>
          <java.util.concurrent.CopyOnWriteArrayList/>
        </entry>
        <entry>
          <com.cloudbees.plugins.credentials.domains.Domain>
            <name>%s</name>
            <description>managed by the template</description>
            <specifications>
              <com.cloudbees.plugins.credentials.domains.HostnameSpecification>
                <includes>example.com</includes>
                <excludes></excludes>
              </com.cloudbees.plugins.credentials.domains.HostnameSpecification>
            </specifications>
          </com.cloudbees.plugins.credentials.domains.Domain>
          <java.util.concurrent.CopyOnWriteArrayList/>
        </entry>
      </domainCredentialsMap>
    </com.cloudbees.hudson.plugins.folder.properties.FolderCredentialsProvider_-FolderCredentialsProperty>
  </properties>`

// TestAccJenkinsJob_folderTemplateManagesStore covers a jenkins_job template
// that contains the folder credentials store and so manages it: the store
// stays in state like any other element, and removing it from the template is
// a change the plan shows and the apply carries out. Update must post that
// template as is, not add the store Jenkins still has, as it does for a
// template that never contained one.
func TestAccJenkinsJob_folderTemplateManagesStore(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	name := "tfacc-sec257-managed-" + randString
	domain := "tfacc-sec257-domain-" + randString
	plain := fmt.Sprintf(testAccFolderJobXML, "managed store")
	managed := strings.Replace(plain, "<properties/>", fmt.Sprintf(testAccManagedFolderCredentialsXML, domain), 1)
	config := func(template string) string {
		return fmt.Sprintf(`
resource jenkins_job foo {
  name     = %q
  template = <<EOT
%s
EOT
}`, name, template)
	}
	storeInState := resource.TestMatchResourceAttr("jenkins_job.foo", "template", regexp.MustCompile(regexp.QuoteMeta("<"+folderCredentialsPropertyElementName)))
	storeNotInState := resource.TestCheckResourceAttrWith("jenkins_job.foo", "template", func(v string) error {
		if strings.Contains(v, folderCredentialsPropertyElementName) {
			return fmt.Errorf("template in state still holds the credentials store the template dropped:\n%s", v)
		}
		return nil
	})

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsJobDestroy,
		Steps: []resource.TestStep{
			{
				Config: config(managed),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckFolderCredentialsStore("jenkins_job.foo", domain, true),
					storeInState,
				),
			},
			{
				Config:           config(managed),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check: resource.ComposeTestCheckFunc(
					testAccCheckFolderCredentialsStore("jenkins_job.foo", domain, true),
					storeInState,
				),
			},
			{
				// The plan must show the removal, and the apply delete the store.
				Config: config(plain),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction("jenkins_job.foo", plancheck.ResourceActionUpdate),
				}},
				Check: resource.ComposeTestCheckFunc(
					testAccCheckFolderCredentialsStore("jenkins_job.foo", domain, false),
					storeNotInState,
				),
			},
			{
				Config:           config(plain),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check:            testAccCheckFolderCredentialsStore("jenkins_job.foo", domain, false),
			},
		},
	})
}

// folderTemplateUnredactedReleaseVersion is the last release before #255: its
// jenkins_job refresh copies the folder credentials store, ciphertext and all,
// into state, and it records nothing in private state.
const folderTemplateUnredactedReleaseVersion = "1.4.0"

// TestAccJenkinsJob_folderTemplateUpgradeKeepsCredentials covers state written
// by that release for a folder managed as a jenkins_job, after a
// jenkins_credential_* resource added a credential to it. Without a private
// state record the store counts as unmanaged: the first refresh by this
// provider drops it from state, and the next template update keeps the
// credential, with the same ciphertext.
func TestAccJenkinsJob_folderTemplateUpgradeKeepsCredentials(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	name := "tfacc-sec257-upgrade-" + randString
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
	release := map[string]resource.ExternalProvider{
		"jenkins": {VersionConstraint: folderTemplateUnredactedReleaseVersion, Source: "namecheap/jenkins"},
	}

	var ciphertext string
	noSecrets := testAccCheckTemplatesHaveNoSecrets(secret, "jenkins_job.foo")
	credentialKept := testAccCheckFolderCredentialCiphertext("jenkins_job.foo", credID, "secret", &ciphertext)

	resource.Test(t, resource.TestCase{
		PreCheck: func() { testAccPreCheck(t) },
		CheckDestroy: resource.ComposeTestCheckFunc(
			testAccCheckJenkinsCredentialSecretTextDestroy,
			testAccCheckJenkinsJobDestroy,
		),
		Steps: []resource.TestStep{
			{
				// The release's refresh puts the store in state, so its plan
				// would delete the credential.
				ExternalProviders:  release,
				Config:             config("created"),
				Check:              credentialKept,
				ExpectNonEmptyPlan: true,
			},
			{
				// Persist that refresh: the prior state this provider starts from.
				ExternalProviders:  release,
				RefreshState:       true,
				ExpectNonEmptyPlan: true,
				Check: resource.ComposeTestCheckFunc(
					resource.TestMatchResourceAttr("jenkins_job.foo", "template", regexp.MustCompile(regexp.QuoteMeta(folderCredentialsPropertyElementName)+`[\s\S]*\{AQAA`)),
					credentialKept,
				),
			},
			{
				// The first refresh by this provider, through a plan of the
				// unchanged configuration (a RefreshState step would keep the
				// release as the provider). It drops the store from state, and
				// leaves nothing to apply.
				ProtoV6ProviderFactories: testAccProviders,
				Config:                   config("created"),
				ConfigPlanChecks:         resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check:                    resource.ComposeTestCheckFunc(noSecrets, credentialKept),
			},
			{
				ProtoV6ProviderFactories: testAccProviders,
				Config:                   config("updated"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestMatchResourceAttr("jenkins_job.foo", "template", regexp.MustCompile("<description>updated</description>")),
					noSecrets,
					credentialKept,
				),
			},
			{
				ProtoV6ProviderFactories: testAccProviders,
				Config:                   config("updated"),
				Check:                    resource.ComposeTestCheckFunc(noSecrets, credentialKept),
			},
		},
	})
}

// TestAccJenkinsFolder_nestedTemplateKeepsSecretsOut is
// TestAccJenkinsFolder_templateKeepsSecretsOut for a folder inside a folder,
// read by the data sources through their folder argument.
func TestAccJenkinsFolder_nestedTemplateKeepsSecretsOut(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	parent := "tfacc-sec257-parent-" + randString
	child := "tfacc-sec257-child-" + randString
	credID := "tfacc-sec257-token-" + randString
	secret := "tfacc-sec257-plaintext-" + randString
	config := func(description string) string {
		return fmt.Sprintf(`
resource jenkins_folder parent {
  name = %q
}

resource jenkins_folder child {
  name        = %q
  folder      = jenkins_folder.parent.id
  description = %q
}

resource jenkins_credential_secret_text token {
  name   = %q
  folder = jenkins_folder.child.id
  secret = %q
}

data jenkins_folder child {
  name       = jenkins_folder.child.name
  folder     = jenkins_folder.parent.id
  depends_on = [jenkins_credential_secret_text.token]
}

data jenkins_job child {
  name       = jenkins_folder.child.name
  folder     = jenkins_folder.parent.id
  depends_on = [jenkins_credential_secret_text.token]
}`, parent, child, description, credID, secret)
	}

	var ciphertext string
	noSecrets := testAccCheckTemplatesHaveNoSecrets(secret, "jenkins_folder.parent", "jenkins_folder.child", "data.jenkins_folder.child", "data.jenkins_job.child")
	credentialKept := testAccCheckFolderCredentialCiphertext("jenkins_folder.child", credID, "secret", &ciphertext)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy: resource.ComposeTestCheckFunc(
			testAccCheckJenkinsCredentialSecretTextDestroy,
			testAccCheckJenkinsFolderDestroy,
		),
		Steps: []resource.TestStep{
			{
				Config: config("created"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_folder.child", "id", "/job/"+parent+"/job/"+child),
					credentialKept,
					testAccCheckTemplatesHaveNoSecrets(secret, "data.jenkins_folder.child", "data.jenkins_job.child"),
				),
			},
			{
				RefreshState: true,
				Check:        resource.ComposeTestCheckFunc(noSecrets, credentialKept),
			},
			{
				Config: config("updated"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_folder.child", "description", "updated"),
					noSecrets,
					credentialKept,
				),
			},
			{
				Config: config("updated"),
				Check:  resource.ComposeTestCheckFunc(noSecrets, credentialKept),
			},
			{
				ResourceName:     "jenkins_folder.child",
				ImportState:      true,
				ImportStateCheck: testAccCheckImportedTemplateHasNoSecrets(secret),
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

// testAccCheckTemplatesLackCiphertext fails when the template attribute of any
// of addrs holds *ciphertext, as recorded by
// testAccCheckFolderCredentialCiphertext. Unlike testAccCheckTemplatesHaveNoSecrets
// it does not depend on the ciphertext's shape.
func testAccCheckTemplatesLackCiphertext(ciphertext *string, addrs ...string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		for _, addr := range addrs {
			rs, ok := s.RootModule().Resources[addr]
			if !ok {
				return fmt.Errorf("resource not found: %s", addr)
			}
			if err := testAccTemplateLacksCiphertext(addr, rs.Primary.Attributes["template"], *ciphertext); err != nil {
				return err
			}
		}
		return nil
	}
}

func testAccTemplateLacksCiphertext(addr, template, ciphertext string) error {
	switch {
	case ciphertext == "":
		return fmt.Errorf("no ciphertext recorded to look for in the %s template", addr)
	case strings.Contains(template, ciphertext):
		return fmt.Errorf("%s template in state contains folder credential ciphertext %s:\n%s", addr, ciphertext, template)
	}
	return nil
}

// testAccCheckFolderCredentialsStore fails unless the live config.xml of the
// folder at addr holds a folder credentials store with the credentials domain
// named domain exactly when want is true.
func testAccCheckFolderCredentialsStore(addr, domain string, want bool) resource.TestCheckFunc {
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
		has := strings.Contains(config, "<"+folderCredentialsPropertyElementName) && strings.Contains(config, "<name>"+domain+"</name>")
		switch {
		case want && !has:
			return fmt.Errorf("folder %s has no credentials store with domain %s:\n%s", rs.Primary.ID, domain, config)
		case !want && strings.Contains(config, folderCredentialsPropertyElementName):
			return fmt.Errorf("folder %s still has a credentials store:\n%s", rs.Primary.ID, config)
		}
		return nil
	}
}

// testAccCheckFolderCredentialCiphertext reads the encrypted value of
// credential id, the text of its element (secret for a secret text,
// secretBytes for a secret file), from the live config.xml of the folder at
// addr. The first call records it in *ciphertext; later ones fail unless the
// folder still holds exactly that ciphertext. An update that posted the
// template without the store, or with {redacted} in place of the secret, fails
// it.
func testAccCheckFolderCredentialCiphertext(addr, id, element string, ciphertext *string) resource.TestCheckFunc {
	reSecret := regexp.MustCompile(`<id>` + regexp.QuoteMeta(id) + `</id>[\s\S]*?<` + element + `>(\{[A-Za-z0-9+/]+={0,2}\})</` + element + `>`)
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
