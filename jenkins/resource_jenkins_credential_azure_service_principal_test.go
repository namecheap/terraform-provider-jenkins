package jenkins

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccJenkinsCredentialAzureServicePrincipal_basic(t *testing.T) {
	var cred AzureServicePrincipalCredentials

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy: resource.ComposeTestCheckFunc(
			testAccCheckJenkinsCredentialAzureServicePrincipalDestroy,
			testAccCheckJenkinsFolderDestroy,
		),
		Steps: []resource.TestStep{
			{
				Config: `
				resource jenkins_credential_azure_service_principal foo {
					name = "bla"
					description = "blabla"
					subscription_id = "12345"
					client_id = "123"
					client_secret = "super-secret"
					tenant = "456"
				}`,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsCredentialAzureServicePrincipalExists("jenkins_credential_azure_service_principal.foo", &cred),
					resource.TestCheckResourceAttr("jenkins_credential_azure_service_principal.foo", "id", "/bla"),
					resource.TestCheckResourceAttr("jenkins_credential_azure_service_principal.foo", "description", "blabla"),
					resource.TestCheckResourceAttr("jenkins_credential_azure_service_principal.foo", "subscription_id", "12345"),
					resource.TestCheckResourceAttr("jenkins_credential_azure_service_principal.foo", "client_id", "123"),
					resource.TestCheckResourceAttr("jenkins_credential_azure_service_principal.foo", "client_secret", "super-secret"),
					resource.TestCheckResourceAttr("jenkins_credential_azure_service_principal.foo", "tenant", "456"),
				),
			},
		},
	})
}

func TestAccJenkinsCredentialAzureServicePrincipal_ignoreChanges(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsCredentialAzureServicePrincipalDestroy,
		Steps: []resource.TestStep{
			{
				Config: `
				resource jenkins_credential_azure_service_principal foo {
				  name = "test-azure-sp-ignore"
				  subscription_id = "sub-123"
				  client_id = "client-123"
				  client_secret = "initial-secret"
				  tenant = "tenant-456"
				}`,
				Check: resource.TestCheckResourceAttr("jenkins_credential_azure_service_principal.foo", "id", "/test-azure-sp-ignore"),
			},
			{
				// Update description while ignoring client_secret; secret must not be overwritten.
				Config: `
				resource jenkins_credential_azure_service_principal foo {
				  name = "test-azure-sp-ignore"
				  description = "updated"
				  subscription_id = "sub-123"
				  client_id = "client-123"
				  client_secret = "initial-secret"
				  tenant = "tenant-456"

				  lifecycle {
				    ignore_changes = [client_secret]
				  }
				}`,
				Check: resource.TestCheckResourceAttr("jenkins_credential_azure_service_principal.foo", "description", "updated"),
			},
		},
	})
}

func TestAccJenkinsCredentialAzureServicePrincipal_folder(t *testing.T) {
	var cred AzureServicePrincipalCredentials
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy: resource.ComposeTestCheckFunc(
			testAccCheckJenkinsCredentialAzureServicePrincipalDestroy,
			testAccCheckJenkinsFolderDestroy,
		),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource "jenkins_folder" "example" {
					name        = "azure-service-principal-test-folder-%s"
					description = "A sample folder"
				}

				resource jenkins_credential_azure_service_principal foo {
					name = "bla"
					folder = jenkins_folder.example.id
					description = "blabla"
					subscription_id = "123"
					client_id = "123"
					client_secret = "super-secret"
					tenant = "456"
				}`, randString),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsCredentialAzureServicePrincipalExists("jenkins_credential_azure_service_principal.foo", &cred),
					resource.TestCheckResourceAttr("jenkins_credential_azure_service_principal.foo", "id", "/job/azure-service-principal-test-folder-"+randString+"/bla"),
					resource.TestCheckResourceAttr("jenkins_credential_azure_service_principal.foo", "folder", "/job/azure-service-principal-test-folder-"+randString),
					resource.TestCheckResourceAttr("jenkins_credential_azure_service_principal.foo", "description", "blabla"),
				),
			},
			{
				Config: fmt.Sprintf(`
				resource "jenkins_folder" "example" {
					name        = "azure-service-principal-test-folder-%s"
					description = "A sample folder"
				}

				resource jenkins_credential_azure_service_principal foo {
					name = "bla"
					folder = jenkins_folder.example.id
					description = "blablablabla"
					subscription_id = "123"
					client_id = "123"
					client_secret = "super-secret"
					tenant = "456"
				}`, randString),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsCredentialAzureServicePrincipalExists("jenkins_credential_azure_service_principal.foo", &cred),
					resource.TestCheckResourceAttr("jenkins_credential_azure_service_principal.foo", "id", "/job/azure-service-principal-test-folder-"+randString+"/bla"),
					resource.TestCheckResourceAttr("jenkins_credential_azure_service_principal.foo", "folder", "/job/azure-service-principal-test-folder-"+randString),
					resource.TestCheckResourceAttr("jenkins_credential_azure_service_principal.foo", "description", "blablablabla"),
				),
			},
		},
	})
}

func TestAccJenkinsCredentialAzureServicePrincipal_folder_certificate(t *testing.T) {
	var cred AzureServicePrincipalCredentials
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy: resource.ComposeTestCheckFunc(
			testAccCheckJenkinsCredentialAzureServicePrincipalDestroy,
			testAccCheckJenkinsFolderDestroy,
		),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource "jenkins_folder" "example" {
					name        = "azure-service-principal-test-folder-%s"
					description = "A sample folder"
				}

				resource jenkins_credential_azure_service_principal foo {
					name = "bla"
					folder = jenkins_folder.example.id
					description = "blabla"
					subscription_id = "123"
					client_id = "123"
					certificate_id = "my-cred-id/123"
					tenant = "456"
				}`, randString),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsCredentialAzureServicePrincipalExists("jenkins_credential_azure_service_principal.foo", &cred),
					resource.TestCheckResourceAttr("jenkins_credential_azure_service_principal.foo", "id", "/job/azure-service-principal-test-folder-"+randString+"/bla"),
					resource.TestCheckResourceAttr("jenkins_credential_azure_service_principal.foo", "certificate_id", "my-cred-id/123"),
				),
			},
			{
				Config: fmt.Sprintf(`
				resource "jenkins_folder" "example" {
					name        = "azure-service-principal-test-folder-%s"
					description = "A sample folder"
				}

				resource jenkins_credential_azure_service_principal foo {
					name = "bla"
					folder = jenkins_folder.example.id
					description = "blablablabla"
					subscription_id = "123"
					client_id = "123"
					client_secret = "super-secret"
					tenant = "456"
				}`, randString),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsCredentialAzureServicePrincipalExists("jenkins_credential_azure_service_principal.foo", &cred),
					resource.TestCheckResourceAttr("jenkins_credential_azure_service_principal.foo", "id", "/job/azure-service-principal-test-folder-"+randString+"/bla"),
					resource.TestCheckResourceAttr("jenkins_credential_azure_service_principal.foo", "client_secret", "super-secret"),
				),
			},
		},
	})
}

// TestAccJenkinsCredentialAzureServicePrincipal_certificateDescriptionUpdate is a
// regression test for the Update() bug where a certificate-based credential had its
// certificate reference written into the client_id field, clobbering the real client
// id and wiping certificate_id on every update — including a description-only edit.
// The second step changes only the description while certificate_id stays populated,
// and asserts the Jenkins-stored client_id is unchanged (i.e. was not overwritten
// with the certificate reference).
func TestAccJenkinsCredentialAzureServicePrincipal_certificateDescriptionUpdate(t *testing.T) {
	var cred AzureServicePrincipalCredentials
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy: resource.ComposeTestCheckFunc(
			testAccCheckJenkinsCredentialAzureServicePrincipalDestroy,
			testAccCheckJenkinsFolderDestroy,
		),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource "jenkins_folder" "example" {
					name        = "azure-sp-cert-update-folder-%s"
					description = "A sample folder"
				}

				resource jenkins_credential_azure_service_principal foo {
					name = "bla"
					folder = jenkins_folder.example.id
					description = "initial"
					subscription_id = "123"
					client_id = "client-abc"
					certificate_id = "my-cred-id/123"
					tenant = "456"
				}`, randString),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsCredentialAzureServicePrincipalExists("jenkins_credential_azure_service_principal.foo", &cred),
					resource.TestCheckResourceAttr("jenkins_credential_azure_service_principal.foo", "client_id", "client-abc"),
					resource.TestCheckResourceAttr("jenkins_credential_azure_service_principal.foo", "certificate_id", "my-cred-id/123"),
				),
			},
			{
				// Change ONLY the description; client_id and certificate_id are identical.
				// This exercises the code path that previously corrupted the credential.
				// The Jenkins-side corruption is not observable through Read (the Azure
				// plugin blanks these fields on read), so the value-level regression is
				// pinned by TestBuildAzureServicePrincipalUpdate below; here we assert the
				// update applies cleanly and the config values persist in state.
				Config: fmt.Sprintf(`
				resource "jenkins_folder" "example" {
					name        = "azure-sp-cert-update-folder-%s"
					description = "A sample folder"
				}

				resource jenkins_credential_azure_service_principal foo {
					name = "bla"
					folder = jenkins_folder.example.id
					description = "updated"
					subscription_id = "123"
					client_id = "client-abc"
					certificate_id = "my-cred-id/123"
					tenant = "456"
				}`, randString),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsCredentialAzureServicePrincipalExists("jenkins_credential_azure_service_principal.foo", &cred),
					resource.TestCheckResourceAttr("jenkins_credential_azure_service_principal.foo", "description", "updated"),
					resource.TestCheckResourceAttr("jenkins_credential_azure_service_principal.foo", "client_id", "client-abc"),
					resource.TestCheckResourceAttr("jenkins_credential_azure_service_principal.foo", "certificate_id", "my-cred-id/123"),
				),
			},
		},
	})
}

// TestBuildAzureServicePrincipalUpdate is the value-level regression test for the
// Update() bug where the certificate reference was written to ClientId (wiping the
// real client id and clearing certificate_id). It exercises the pure mapping helper
// directly, so it needs neither a live Jenkins nor the Azure plugin.
func TestBuildAzureServicePrincipalUpdate(t *testing.T) {
	base := func() credentialAzureServicePrincipalResourceModel {
		return credentialAzureServicePrincipalResourceModel{
			Name:           types.StringValue("bla"),
			SubscriptionId: types.StringValue("sub-123"),
			ClientId:       types.StringValue("client-abc"),
			Tenant:         types.StringValue("tenant-456"),
		}
	}

	t.Run("certificate credential, description-only change", func(t *testing.T) {
		state := base()
		state.CertificateId = types.StringValue("my-cred-id/123")
		state.Description = types.StringValue("initial")

		data := base()
		data.CertificateId = types.StringValue("my-cred-id/123")
		data.Description = types.StringValue("updated")

		cred := buildAzureServicePrincipalUpdate(data, state)

		if cred.Data.ClientId != "client-abc" {
			t.Errorf("client_id: got %q, want %q (must not be overwritten with the certificate reference)", cred.Data.ClientId, "client-abc")
		}
		if cred.Data.CertificateId != "my-cred-id/123" {
			t.Errorf("certificate_id: got %q, want %q (must not be wiped on a description-only update)", cred.Data.CertificateId, "my-cred-id/123")
		}
		if cred.Data.ClientSecret != "" {
			t.Errorf("client_secret: got %q, want empty for a certificate-based credential", cred.Data.ClientSecret)
		}
	})

	t.Run("secret credential, secret unchanged (ignore_changes)", func(t *testing.T) {
		state := base()
		state.ClientSecret = types.StringValue("sekret")

		data := base()
		data.ClientSecret = types.StringValue("sekret")

		cred := buildAzureServicePrincipalUpdate(data, state)

		if cred.Data.ClientSecret != "" {
			t.Errorf("client_secret: got %q, want empty (unchanged secret must not be re-sent)", cred.Data.ClientSecret)
		}
		if cred.Data.CertificateId != "" {
			t.Errorf("certificate_id: got %q, want empty for a secret-based credential", cred.Data.CertificateId)
		}
		if cred.Data.ClientId != "client-abc" {
			t.Errorf("client_id: got %q, want %q", cred.Data.ClientId, "client-abc")
		}
	})

	t.Run("secret credential, secret changed", func(t *testing.T) {
		state := base()
		state.ClientSecret = types.StringValue("old")

		data := base()
		data.ClientSecret = types.StringValue("new")

		cred := buildAzureServicePrincipalUpdate(data, state)

		if cred.Data.ClientSecret != "new" {
			t.Errorf("client_secret: got %q, want %q (changed secret must be sent)", cred.Data.ClientSecret, "new")
		}
	})
}

func testAccCheckJenkinsCredentialAzureServicePrincipalExists(resourceName string, cred *AzureServicePrincipalCredentials) resource.TestCheckFunc {
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
			return fmt.Errorf("Unable to retrieve credentials for %s - %s: %w", rs.Primary.Attributes["folder"], rs.Primary.Attributes["name"], err)
		}

		return nil
	}
}

func testAccCheckJenkinsCredentialAzureServicePrincipalDestroy(s *terraform.State) error {
	ctx := context.Background()

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "jenkins_credential_azure_service_principal" {
			continue
		} else if _, ok := rs.Primary.Meta["name"]; !ok {
			continue
		}

		cred := AzureServicePrincipalCredentials{}
		manager := testAccClient.Credentials()
		manager.Folder = formatFolderName(rs.Primary.Meta["folder"].(string))
		err := manager.GetSingle(ctx, rs.Primary.Meta["domain"].(string), rs.Primary.Meta["name"].(string), &cred)
		if err == nil {
			return fmt.Errorf("Credentials still exists: %s - %s", rs.Primary.Attributes["folder"], rs.Primary.Attributes["name"])
		}
	}

	return nil
}
