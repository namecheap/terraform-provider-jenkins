package jenkins

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
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
		case "jenkins_credential_secret_text", "jenkins_credential_username", "jenkins_credential_certificate":
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

// testAccCheckCredentialPathGone fails if any of the given folder
// credentials, each a {domain, ID} pair, still exists in Jenkins.
func testAccCheckCredentialPathGone(folder string, creds ...[2]string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		for _, c := range creds {
			if _, err := testAccCredentialPathDescription(folder, c[0], c[1]); err == nil {
				return fmt.Errorf("credential %s/%s/%s still exists", folder, c[0], c[1])
			}
		}
		return nil
	}
}

// testAccCredentialDomainPathDescription reads a credentials domain's
// description straight from Jenkins, building and escaping the URL itself so
// it does not depend on the domain URL code under test.
func testAccCredentialDomainPathDescription(folder, name string) (string, error) {
	store := "/credentials/store/system"
	if folder != "" {
		store = "/job/" + formatFolderName(folder) + "/credentials/store/folder"
	}
	endpoint := strings.TrimRight(os.Getenv("JENKINS_URL"), "/") + store + "/domain/" + url.PathEscape(name) + "/config.xml"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(os.Getenv("JENKINS_USERNAME"), os.Getenv("JENKINS_PASSWORD"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: status %d", endpoint, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	var dom credentialDomainXML
	if err := xml.Unmarshal(stripXMLDeclaration(body), &dom); err != nil {
		return "", err
	}
	return dom.Description, nil
}

// testAccCheckCredentialDomainPathDescription fails unless the domain exists
// in Jenkins with the given description.
func testAccCheckCredentialDomainPathDescription(folder, name, want string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		got, err := testAccCredentialDomainPathDescription(folder, name)
		if err != nil {
			return fmt.Errorf("reading domain %s/%s: %w", folder, name, err)
		}
		if got != want {
			return fmt.Errorf("domain %s/%s has description %q, want %q", folder, name, got, want)
		}
		return nil
	}
}

// testAccPreValidatorProviders serves the provider under test with the
// plan-time name, domain and folder validators removed from every credential
// resource. It stands in for a release from before issue #250: a step run
// with it writes state that the current provider no longer accepts in config,
// as an upgraded workspace can hold. Everything else, including the runtime
// path guards, is the real code.
func testAccPreValidatorProviders() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"jenkins": providerserver.NewProtocol6WithError(testAccPreValidatorProvider{&JenkinsProvider{}}),
	}
}

type testAccPreValidatorProvider struct{ *JenkinsProvider }

func (p testAccPreValidatorProvider) Resources(ctx context.Context) []func() fwresource.Resource {
	var out []func() fwresource.Resource
	for _, newResource := range p.JenkinsProvider.Resources(ctx) {
		var meta fwresource.MetadataResponse
		newResource().Metadata(ctx, fwresource.MetadataRequest{ProviderTypeName: "jenkins"}, &meta)
		if !strings.HasPrefix(meta.TypeName, "jenkins_credential_") || meta.TypeName == "jenkins_credential_domain" {
			out = append(out, newResource)
			continue
		}
		out = append(out, func() fwresource.Resource { return testAccPreValidatorResource{newResource()} })
	}
	return out
}

// testAccPreValidatorResource is a credential resource without the plan-time
// checks on its name, domain and folder.
type testAccPreValidatorResource struct{ fwresource.Resource }

func (r testAccPreValidatorResource) Schema(ctx context.Context, req fwresource.SchemaRequest, resp *fwresource.SchemaResponse) {
	r.Resource.Schema(ctx, req, resp)
	for _, attr := range []string{"name", "domain", "folder"} {
		if a, ok := resp.Schema.Attributes[attr].(rschema.StringAttribute); ok {
			a.Validators = nil
			resp.Schema.Attributes[attr] = a
		}
	}
}

func (r testAccPreValidatorResource) Configure(ctx context.Context, req fwresource.ConfigureRequest, resp *fwresource.ConfigureResponse) {
	r.Resource.(fwresource.ResourceWithConfigure).Configure(ctx, req, resp)
}

func (r testAccPreValidatorResource) ConfigValidators(ctx context.Context) []fwresource.ConfigValidator {
	if v, ok := r.Resource.(fwresource.ResourceWithConfigValidators); ok {
		return v.ConfigValidators(ctx)
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
				// A folder with a ".." segment can still resolve to a real folder:
				// Jetty turns /job/<folder>/job/.. and /job/../job/<folder> into
				// /job/<folder>. Unchecked, both imports would read the credential
				// above into state whose folder is not the one it came from.
				ResourceName:  "jenkins_credential_secret_text.cred",
				ImportState:   true,
				ImportStateId: prefix + "/../_/" + prefix + "-cred",
				ExpectError:   regexp.MustCompile(`has\s+an\s+invalid\s+segment`),
			},
			{
				ResourceName:  "jenkins_credential_secret_text.cred",
				ImportState:   true,
				ImportStateId: "../" + prefix + "/_/" + prefix + "-cred",
				ExpectError:   regexp.MustCompile(`has\s+an\s+invalid\s+segment`),
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

// TestAccJenkinsCredentialPath_preValidatorState: state written before the
// plan-time checks existed can hold a domain or ID that climbs out of the
// folder's store. The current provider refuses such a value in config, but
// refresh and Delete still take it from state, so the runtime escaping alone
// must keep Read, Update and Delete on the folder credential, on the shared
// CRUD (secret text) and the hand-written certificate resource. Jenkins
// accepts "/" in domain names and IDs, so the test puts folder credentials at
// exactly the escaped paths: each op succeeds there, where unescaped it would
// reach the system credential the value climbs to (and Delete would remove it).
func TestAccJenkinsCredentialPath_preValidatorState(t *testing.T) {
	prefix := testAccCredentialPathPrefix()
	// climbingID climbs from .../domain/_/credential/ in the folder store to
	// the system store's victim.
	climbingID := "../../../../../../../../credentials/store/system/domain/_/credential/" + prefix + "-victim"
	base := fmt.Sprintf(`
resource jenkins_folder team {
  name = "%[1]s"
}

resource jenkins_credential_secret_text victim {
  name        = "%[1]s-victim"
  description = "victim"
  secret      = "original"
}

resource jenkins_credential_certificate victim_cert {
  name        = "%[1]s-victim-cert"
  description = "victim"
  keystore    = %[2]q
  password    = "changeit"
}
`, prefix, testCertificateKeystoreB64)
	tenants := func(description string) string {
		return base + fmt.Sprintf(`
resource jenkins_credential_secret_text tenant_domain {
  folder      = jenkins_folder.team.name
  domain      = %[2]q
  name        = "%[1]s-victim"
  description = %[4]q
  secret      = "tenant"
}

resource jenkins_credential_secret_text tenant_id {
  folder      = jenkins_folder.team.name
  name        = %[3]q
  description = %[4]q
  secret      = "tenant"
}

resource jenkins_credential_certificate tenant_cert {
  folder      = jenkins_folder.team.name
  domain      = %[2]q
  name        = "%[1]s-victim-cert"
  description = %[4]q
  keystore    = %[5]q
  password    = "changeit"
}
`, prefix, testAccClimbingDomain, climbingID, description, testCertificateKeystoreB64)
	}
	victimsUnchanged := resource.ComposeTestCheckFunc(
		testAccCheckCredentialPathDescription("", defaultCredentialDomain, prefix+"-victim", "victim"),
		testAccCheckCredentialPathDescription("", defaultCredentialDomain, prefix+"-victim-cert", "victim"),
	)
	tenantCreds := [][2]string{
		{testAccClimbingDomain, prefix + "-victim"},
		{defaultCredentialDomain, climbingID},
		{testAccClimbingDomain, prefix + "-victim-cert"},
	}
	tenantsHave := func(description string) resource.TestCheckFunc {
		var checks []resource.TestCheckFunc
		for _, c := range tenantCreds {
			checks = append(checks, testAccCheckCredentialPathDescription(prefix, c[0], c[1], description))
		}
		return resource.ComposeTestCheckFunc(checks...)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		CheckDestroy: resource.ComposeTestCheckFunc(testAccCheckCredentialPathDestroy, testAccCheckJenkinsFolderDestroy),
		Steps: []resource.TestStep{
			{
				ProtoV6ProviderFactories: testAccProviders,
				Config:                   base,
				Check:                    victimsUnchanged,
			},
			{
				// An older provider writes the climbing values to state. The
				// domain lives in the folder's store and goes with the folder.
				PreConfig: func() {
					if err := testAccClient.CreateCredentialDomain(context.Background(), prefix, testAccClimbingDomain, ""); err != nil {
						t.Errorf("creating domain %q in %s: %v", testAccClimbingDomain, prefix, err)
					}
				},
				ProtoV6ProviderFactories: testAccPreValidatorProviders(),
				Config:                   tenants("tenant"),
				Check:                    resource.ComposeTestCheckFunc(victimsUnchanged, tenantsHave("tenant")),
			},
			{
				// Update with the climbing values in plan and state.
				ProtoV6ProviderFactories: testAccPreValidatorProviders(),
				Config:                   tenants("tenant v2"),
				Check:                    resource.ComposeTestCheckFunc(victimsUnchanged, tenantsHave("tenant v2")),
			},
			{
				// The current provider refuses the same update at plan.
				ProtoV6ProviderFactories: testAccProviders,
				Config:                   tenants("tenant v3"),
				ExpectError:              regexp.MustCompile(`must\s+not\s+include\s+path\s+characters`),
			},
			{
				// The tenants leave the config: refresh and Delete take the
				// climbing values from state.
				PreConfig: func() {
					if err := resource.ComposeTestCheckFunc(victimsUnchanged, tenantsHave("tenant v2"))(nil); err != nil {
						t.Errorf("after the rejected update: %v", err)
					}
				},
				ProtoV6ProviderFactories: testAccProviders,
				Config:                   base,
				Check:                    resource.ComposeTestCheckFunc(victimsUnchanged, testAccCheckCredentialPathGone(prefix, tenantCreds...)),
			},
		},
	})
}

// TestAccJenkinsCredentialPath_specialCharacterDomainUpdate: Update and
// Delete of a domain whose name contains "#" and "?", and of a certificate in
// it, must act on that one domain. Unescaped, the "#" cuts each URL short to
// the domain named by the text before it, which the test creates as a decoy.
func TestAccJenkinsCredentialPath_specialCharacterDomainUpdate(t *testing.T) {
	prefix := testAccCredentialPathPrefix()
	domain := prefix + " C# builds?"
	decoy := prefix + " C"
	decoyConfig := fmt.Sprintf(`
resource jenkins_folder team {
  name = "%[1]s"
}

resource jenkins_credential_domain decoy {
  folder      = jenkins_folder.team.name
  name        = %[2]q
  description = "decoy"
}
`, prefix, decoy)
	config := func(description string) string {
		return decoyConfig + fmt.Sprintf(`
resource jenkins_credential_domain dom {
  folder      = jenkins_folder.team.name
  name        = %[2]q
  description = %[3]q
}

resource jenkins_credential_certificate cert {
  folder      = jenkins_folder.team.name
  domain      = jenkins_credential_domain.dom.name
  name        = "%[1]s-cert"
  description = %[3]q
  keystore    = %[4]q
  password    = "changeit"
}
`, prefix, domain, description, testCertificateKeystoreB64)
	}
	inDomain := func(description string) resource.TestCheckFunc {
		return resource.ComposeTestCheckFunc(
			testAccCheckCredentialDomainPathDescription(prefix, domain, description),
			testAccCheckCredentialDomainPathDescription(prefix, decoy, "decoy"),
			testAccCheckCredentialPathDescription(prefix, domain, prefix+"-cert", description),
		)
	}

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
				Config: config("one"),
				Check:  inDomain("one"),
			},
			{
				Config: config("two"),
				Check: resource.ComposeTestCheckFunc(
					inDomain("two"),
					resource.TestCheckResourceAttr("jenkins_credential_domain.dom", "description", "two"),
					resource.TestCheckResourceAttr("jenkins_credential_certificate.cert", "description", "two"),
				),
			},
			{
				ResourceName:            "jenkins_credential_certificate.cert",
				ImportState:             true,
				ImportStateId:           prefix + "/" + domain + "/" + prefix + "-cert",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"keystore", "password"},
			},
			{
				ResourceName:      "jenkins_credential_domain.dom",
				ImportState:       true,
				ImportStateId:     prefix + "/" + domain,
				ImportStateVerify: true,
			},
			{
				Config: decoyConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckCredentialDomainPathDescription(prefix, decoy, "decoy"),
					testAccCheckCredentialPathGone(prefix, [2]string{domain, prefix + "-cert"}),
					func(*terraform.State) error {
						if _, err := testAccCredentialDomainPathDescription(prefix, domain); err == nil {
							return fmt.Errorf("domain %s/%s still exists", prefix, domain)
						}
						return nil
					},
				),
			},
		},
	})
}

// TestAccJenkinsCredentialPath_domainImportFolder: a jenkins_credential_domain
// import ID skips plan validation, so its folder reaches the domain URLs as it
// is. "job" and "/job" format to nothing, which for a credential selects the
// system store: imported with them, a domain must not be read from the system
// store, or destroying it would delete the system domain of that name. A "?"
// or "#" in the folder cuts the URL short and must not import anything either.
func TestAccJenkinsCredentialPath_domainImportFolder(t *testing.T) {
	prefix := testAccCredentialPathPrefix()
	config := fmt.Sprintf(`
resource jenkins_folder team {
  name = "%[1]s"
}

resource jenkins_credential_domain system {
  name        = "%[1]s-dom"
  description = "system"
}

resource jenkins_credential_domain dom {
  folder      = jenkins_folder.team.name
  name        = "%[1]s-dom"
  description = "folder"
}
`, prefix)
	notImported := regexp.MustCompile(`Cannot\s+import\s+non-existent\s+remote\s+object|Invalid\s+Folder|Unable\s+to\s+Refresh\s+Resource`)
	domainsUnchanged := resource.ComposeTestCheckFunc(
		testAccCheckCredentialDomainPathDescription("", prefix+"-dom", "system"),
		testAccCheckCredentialDomainPathDescription(prefix, prefix+"-dom", "folder"),
	)

	steps := []resource.TestStep{
		{
			Config: config,
			Check:  domainsUnchanged,
		},
		{
			// Control: the folder domain imports by its plain ID.
			ResourceName:      "jenkins_credential_domain.dom",
			ImportState:       true,
			ImportStateId:     prefix + "/" + prefix + "-dom",
			ImportStateVerify: true,
		},
	}
	for _, folder := range []string{"job", "/job", "job/job", prefix + "?", prefix + "#"} {
		steps = append(steps, resource.TestStep{
			ResourceName:  "jenkins_credential_domain.dom",
			ImportState:   true,
			ImportStateId: folder + "/" + prefix + "-dom",
			ExpectError:   notImported,
		})
	}
	steps = append(steps, resource.TestStep{
		PreConfig: func() {
			if err := domainsUnchanged(nil); err != nil {
				t.Errorf("after the rejected imports: %v", err)
			}
		},
		Config:   config,
		PlanOnly: true,
	})

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             resource.ComposeTestCheckFunc(testAccCheckJenkinsCredentialDomainDestroy, testAccCheckJenkinsFolderDestroy),
		Steps:                    steps,
	})
}
