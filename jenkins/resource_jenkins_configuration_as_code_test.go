package jenkins

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccJenkinsConfigurationAsCode_basic(t *testing.T) {
	msg := "tf-acc " + acctest.RandStringFromCharSet(8, acctest.CharSetAlphaNum)
	updated := "tf-acc " + acctest.RandStringFromCharSet(8, acctest.CharSetAlphaNum)
	resourceName := "jenkins_configuration_as_code.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource jenkins_configuration_as_code test {
  section = "jenkins"
  yaml    = yamlencode({ systemMessage = "%s" })
}`, msg),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "id", "jenkins"),
					resource.TestCheckResourceAttr(resourceName, "section", "jenkins"),
					resource.TestCheckResourceAttrSet(resourceName, "yaml"),
					testAccCheckCASCSectionContains("jenkins", msg),
				),
			},
			{
				// Changing the YAML re-applies (in-place update).
				Config: fmt.Sprintf(`
resource jenkins_configuration_as_code test {
  section = "jenkins"
  yaml    = yamlencode({ systemMessage = "%s" })
}`, updated),
				Check: resource.ComposeTestCheckFunc(
					testAccCheckCASCSectionContains("jenkins", updated),
				),
			},
		},
	})
}

// testAccCheckCASCSectionContains exports the live JCasC configuration and
// asserts the given section's YAML contains want, proving the apply reached the
// controller.
func testAccCheckCASCSectionContains(section, want string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		exported, err := testAccClient.ExportCASC(context.Background())
		if err != nil {
			return fmt.Errorf("exporting JCasC config: %w", err)
		}
		sub, found, err := extractSectionYAML(exported, section)
		if err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("section %q not present in exported configuration", section)
		}
		if !strings.Contains(sub, want) {
			return fmt.Errorf("section %q does not contain %q; got:\n%s", section, want, sub)
		}
		return nil
	}
}
