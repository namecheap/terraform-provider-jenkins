package jenkins

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// upgradeFromReleaseVersion is the last release built on the SDKv2 provider for
// jenkins_job / jenkins_folder. The upgrade tests below create resources with
// this published version (writing SDKv2-shaped state) and then assert that the
// migrated, framework-based provider produces an empty plan against that state —
// the compatibility release gate from issue #79.
const upgradeFromReleaseVersion = "1.1.2"

func upgradeExternalProviders() map[string]resource.ExternalProvider {
	return map[string]resource.ExternalProvider{
		"jenkins": {
			VersionConstraint: upgradeFromReleaseVersion,
			Source:            "namecheap/jenkins",
		},
	}
}

// TestAccJenkinsJob_upgradeFromRelease verifies that jenkins_job state written
// by the last SDKv2 release upgrades to the framework provider with no diff,
// both for a top-level job and a folder-scoped job.
func TestAccJenkinsJob_upgradeFromRelease(t *testing.T) {
	testDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(testDir, "test.xml"), testXML, 0644); err != nil {
		t.Fatal(err)
	}
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	config := fmt.Sprintf(`
resource jenkins_folder parent {
	name        = "tf-acc-test-%s"
	description = "upgrade test parent"
}

resource jenkins_job top {
	name     = "tf-acc-test-top-%s"
	template = templatefile("%s/test.xml", { description = "upgrade test top" })
}

resource jenkins_job nested {
	name     = "child"
	folder   = jenkins_folder.parent.id
	template = templatefile("%s/test.xml", { description = "upgrade test nested" })
}`, randString, randString, testDir, testDir)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		CheckDestroy: testAccCheckJenkinsJobDestroy,
		Steps: []resource.TestStep{
			{
				// Create the resources with the published SDKv2 release.
				ExternalProviders: upgradeExternalProviders(),
				Config:            config,
			},
			{
				// Re-plan with the migrated framework provider: no diff allowed.
				ProtoV6ProviderFactories: testAccProviders,
				Config:                   config,
				PlanOnly:                 true,
			},
		},
	})
}

// TestAccJenkinsFolder_upgradeFromRelease verifies that jenkins_folder state
// written by the last SDKv2 release upgrades to the framework provider with no
// diff, both for a top-level folder and a nested folder, including the
// project-based security block.
func TestAccJenkinsFolder_upgradeFromRelease(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	config := fmt.Sprintf(`
resource jenkins_folder parent {
	name         = "tf-acc-test-%s"
	display_name = "TF Acceptance Test %s"
	description  = "upgrade test parent"

	security {
		inheritance_strategy = "org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy"
		permissions = [
			"hudson.model.Item.Discover:anonymous",
		]
	}
}

resource jenkins_folder nested {
	name        = "child"
	folder      = jenkins_folder.parent.id
	description = "upgrade test nested"
}`, randString, randString)

	resource.Test(t, resource.TestCase{
		PreCheck:     func() { testAccPreCheck(t) },
		CheckDestroy: testAccCheckJenkinsFolderDestroy,
		Steps: []resource.TestStep{
			{
				ExternalProviders: upgradeExternalProviders(),
				Config:            config,
			},
			{
				ProtoV6ProviderFactories: testAccProviders,
				Config:                   config,
				PlanOnly:                 true,
			},
		},
	})
}
