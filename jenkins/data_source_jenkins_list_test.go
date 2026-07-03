package jenkins

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestAccJenkinsListDataSources_basic creates one object of each listable kind and
// asserts the corresponding list data source reports it. Membership (not exact set
// equality) is checked, so objects from other tests do not interfere.
func TestAccJenkinsListDataSources_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: `
				resource "jenkins_folder" "foo" {
				  name = "tf-acc-list-folder"
				}

				resource "jenkins_credential_secret_text" "bar" {
				  name   = "tf-acc-list-cred"
				  secret = "x"
				}

				resource "jenkins_pipeline_job" "baz" {
				  name   = "tf-acc-list-job"
				  script = "pipeline {\n  agent any\n  stages { stage('x') { steps { echo 'y' } } }\n}"
				}

				resource "jenkins_node" "qux" {
				  name      = "tf-acc-list-node"
				  remote_fs = "/tmp"
				}

				data "jenkins_folders" "all" {
				  depends_on = [jenkins_folder.foo]
				}

				data "jenkins_jobs" "all" {
				  depends_on = [jenkins_pipeline_job.baz]
				}

				data "jenkins_nodes" "all" {
				  depends_on = [jenkins_node.qux]
				}

				data "jenkins_credentials" "all" {
				  depends_on = [jenkins_credential_secret_text.bar]
				}
				`,
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckTypeSetElemAttr("data.jenkins_folders.all", "folders.*", "tf-acc-list-folder"),
					resource.TestCheckTypeSetElemAttr("data.jenkins_jobs.all", "jobs.*", "tf-acc-list-job"),
					resource.TestCheckTypeSetElemAttr("data.jenkins_nodes.all", "nodes.*", "tf-acc-list-node"),
					resource.TestCheckTypeSetElemAttr("data.jenkins_credentials.all", "credentials.*", "tf-acc-list-cred"),
				),
			},
		},
	})
}
