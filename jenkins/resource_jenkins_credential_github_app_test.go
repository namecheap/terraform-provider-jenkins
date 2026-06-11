package jenkins

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"encoding/xml"
	"errors"
	"fmt"
	"testing"

	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestCredentialGitHubApp_metadata(t *testing.T) {
	r := newCredentialGitHubAppResource()
	resp := &fwresource.MetadataResponse{}
	r.Metadata(context.Background(), fwresource.MetadataRequest{ProviderTypeName: "jenkins"}, resp)
	if resp.TypeName != "jenkins_credential_github_app" {
		t.Errorf("TypeName = %q, want %q", resp.TypeName, "jenkins_credential_github_app")
	}
}

func TestCredentialGitHubApp_schema(t *testing.T) {
	r := newCredentialGitHubAppResource()
	resp := &fwresource.SchemaResponse{}
	r.Schema(context.Background(), fwresource.SchemaRequest{}, resp)

	for _, attr := range []string{"id", "name", "folder", "description", "domain", "scope", "app_id", "private_key"} {
		if _, ok := resp.Schema.Attributes[attr]; !ok {
			t.Errorf("Schema() missing attribute %q", attr)
		}
	}
}

func TestGitHubAppCredentials_xmlMarshal(t *testing.T) {
	cred := GitHubAppCredentials{
		ID:          "my-app",
		Scope:       "GLOBAL",
		Description: "test app",
		AppID:       "123456",
		PrivateKey:  "-----BEGIN RSA PRIVATE KEY-----\ntest\n-----END RSA PRIVATE KEY-----",
	}

	data, err := xml.Marshal(cred)
	if err != nil {
		t.Fatalf("xml.Marshal returned error: %v", err)
	}

	var got GitHubAppCredentials
	if err := xml.Unmarshal(data, &got); err != nil {
		t.Fatalf("xml.Unmarshal returned error: %v", err)
	}

	if got.ID != cred.ID {
		t.Errorf("ID = %q, want %q", got.ID, cred.ID)
	}
	if got.AppID != cred.AppID {
		t.Errorf("AppID = %q, want %q", got.AppID, cred.AppID)
	}
	if got.PrivateKey != cred.PrivateKey {
		t.Errorf("PrivateKey = %q, want %q", got.PrivateKey, cred.PrivateKey)
	}
	wantLocal := "org.jenkinsci.plugins.github__branch__source.GitHubAppCredentials"
	if got.XMLName.Local != wantLocal {
		t.Errorf("XMLName.Local = %q, want %q", got.XMLName.Local, wantLocal)
	}
}

// generateTestRSAKey returns a PKCS#1 PEM-encoded 2048-bit RSA private key for acceptance tests.
func generateTestRSAKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}
	block := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}
	return string(pem.EncodeToMemory(block))
}

func TestAccJenkinsCredentialGitHubApp_basic(t *testing.T) {
	privateKey := generateTestRSAKey(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsCredentialGitHubAppDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource jenkins_credential_github_app foo {
				  name        = "test-github-app"
				  app_id      = "12345"
				  private_key = %q
				}`, privateKey),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_credential_github_app.foo", "id", "/test-github-app"),
					resource.TestCheckResourceAttr("jenkins_credential_github_app.foo", "app_id", "12345"),
					testAccCheckJenkinsCredentialGitHubAppExists("jenkins_credential_github_app.foo"),
				),
			},
			{
				// Update by adding description
				Config: fmt.Sprintf(`
				resource jenkins_credential_github_app foo {
				  name        = "test-github-app"
				  description = "new-description"
				  app_id      = "12345"
				  private_key = %q
				}`, privateKey),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsCredentialGitHubAppExists("jenkins_credential_github_app.foo"),
					resource.TestCheckResourceAttr("jenkins_credential_github_app.foo", "description", "new-description"),
				),
			},
		},
	})
}

func TestAccJenkinsCredentialGitHubApp_ignoreChanges(t *testing.T) {
	privateKey := generateTestRSAKey(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsCredentialGitHubAppDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource jenkins_credential_github_app foo {
				  name        = "test-github-app-ignore"
				  app_id      = "99999"
				  private_key = %q
				}`, privateKey),
				Check: resource.TestCheckResourceAttr("jenkins_credential_github_app.foo", "id", "/test-github-app-ignore"),
			},
			{
				// Update description while ignoring private_key; key must not be overwritten.
				Config: fmt.Sprintf(`
				resource jenkins_credential_github_app foo {
				  name        = "test-github-app-ignore"
				  description = "updated"
				  app_id      = "99999"
				  private_key = %q

				  lifecycle {
				    ignore_changes = [private_key]
				  }
				}`, privateKey),
				Check: resource.TestCheckResourceAttr("jenkins_credential_github_app.foo", "description", "updated"),
			},
		},
	})
}

func TestAccJenkinsCredentialGitHubApp_folder(t *testing.T) {
	privateKey := generateTestRSAKey(t)
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy: resource.ComposeTestCheckFunc(
			testAccCheckJenkinsCredentialGitHubAppDestroy,
			testAccCheckJenkinsFolderDestroy,
		),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource jenkins_folder foo {
					name = "tf-acc-test-%s"
					description = "Terraform acceptance testing"
				}

				resource jenkins_credential_github_app foo {
				  name        = "test-github-app"
				  folder      = jenkins_folder.foo.id
				  app_id      = "12345"
				  private_key = %q
				}`, randString, privateKey),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_credential_github_app.foo", "id", "/job/tf-acc-test-"+randString+"/test-github-app"),
					testAccCheckJenkinsCredentialGitHubAppExists("jenkins_credential_github_app.foo"),
				),
			},
		},
	})
}

func testAccCheckJenkinsCredentialGitHubAppExists(resourceName string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		ctx := context.Background()

		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return errors.New(resourceName + " not found")
		}

		if rs.Primary.ID == "" {
			return fmt.Errorf("ID is not set")
		}

		cred := GitHubAppCredentials{}
		manager := testAccClient.Credentials()
		manager.Folder = formatFolderName(rs.Primary.Attributes["folder"])
		err := manager.GetSingle(ctx, rs.Primary.Attributes["domain"], rs.Primary.Attributes["name"], &cred)
		if err != nil {
			return fmt.Errorf("unable to retrieve credentials for %s - %s: %w", rs.Primary.Attributes["folder"], rs.Primary.Attributes["name"], err)
		}

		return nil
	}
}

func testAccCheckJenkinsCredentialGitHubAppDestroy(s *terraform.State) error {
	ctx := context.Background()

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "jenkins_credential_github_app" {
			continue
		} else if _, ok := rs.Primary.Meta["name"]; !ok {
			continue
		}

		cred := GitHubAppCredentials{}
		manager := testAccClient.Credentials()
		manager.Folder = formatFolderName(rs.Primary.Meta["folder"].(string))
		err := manager.GetSingle(ctx, rs.Primary.Meta["domain"].(string), rs.Primary.Meta["name"].(string), &cred)
		if err == nil {
			return fmt.Errorf("credentials still exists: %s - %s", rs.Primary.Attributes["folder"], rs.Primary.Attributes["name"])
		}
	}

	return nil
}
