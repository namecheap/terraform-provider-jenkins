package jenkins

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccJenkinsJobDataSource_basic(t *testing.T) {
	testDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(testDir, "test.xml"), testXML, 0644)
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource jenkins_job foo {
	name = "tf-acc-test-%s"
	template = templatefile("%s/test.xml", {
		description = "Acceptance testing Jenkins provider"
	})
}

data jenkins_job foo {
	name = jenkins_job.foo.name
}`, randString, testDir),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_job.foo", "id", "/job/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("data.jenkins_job.foo", "id", "/job/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("data.jenkins_job.foo", "name", "tf-acc-test-"+randString),
					// The data source returns Jenkins' rendered config.xml. Since the
					// jenkins_job resource now preserves the user's template verbatim
					// (semantic equality), assert the data source contains the expected
					// content rather than requiring byte-for-byte equality with the resource.
					resource.TestMatchResourceAttr("data.jenkins_job.foo", "template", regexp.MustCompile("Acceptance testing Jenkins provider")),
				),
			},
		},
	})
}

func TestAccJenkinsJobDataSource_nested(t *testing.T) {
	testDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(testDir, "test.xml"), testXML, 0644)
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource jenkins_folder foo {
	name = "tf-acc-test-%s"
}

resource jenkins_job sub {
	name = "subfolder"
	folder = jenkins_folder.foo.id
	template = templatefile("%s/test.xml", {
		description = "Acceptance testing Jenkins provider"
	})
}

data jenkins_job sub {
	name = jenkins_job.sub.name
	folder = jenkins_job.sub.folder
}`, randString, testDir),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_folder.foo", "id", "/job/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_job.sub", "id", "/job/tf-acc-test-"+randString+"/job/subfolder"),
					resource.TestCheckResourceAttr("data.jenkins_job.sub", "name", "subfolder"),
					resource.TestCheckResourceAttr("data.jenkins_job.sub", "folder", "/job/tf-acc-test-"+randString),
					resource.TestMatchResourceAttr("data.jenkins_job.sub", "template", regexp.MustCompile("Acceptance testing Jenkins provider")),
				),
			},
		},
	})
}
