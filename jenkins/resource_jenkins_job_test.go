package jenkins

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

var (
	//go:embed "resource_jenkins_job_test.xml"
	testXML []byte
)

func TestAccJenkinsJob_basic(t *testing.T) {
	testDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(testDir, "test.xml"), testXML, 0644)
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsJobDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource jenkins_job foo {
	name = "tf-acc-test-%s"
	template = templatefile("%s/test.xml", {
		description = "Acceptance testing Jenkins provider"
	})
}`, randString, testDir),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_job.foo", "id", "/job/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_job.foo", "name", "tf-acc-test-"+randString),
				),
			},
		},
	})
}

func TestAccJenkinsJob_nested(t *testing.T) {
	testDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(testDir, "test.xml"), testXML, 0644)
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsFolderDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource jenkins_folder foo {
	name = "tf-acc-test-%s"
	description = "Terraform acceptance tests %s"
}

resource jenkins_job sub {
	name = "subfolder"
	folder = jenkins_folder.foo.id
	template = templatefile("%s/test.xml", {
		description = "Acceptance testing Jenkins provider"
	})
}`, randString, randString, testDir),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_folder.foo", "id", "/job/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.foo", "name", "tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_job.sub", "id", "/job/tf-acc-test-"+randString+"/job/subfolder"),
					resource.TestCheckResourceAttr("jenkins_job.sub", "name", "subfolder"),
					resource.TestCheckResourceAttr("jenkins_job.sub", "folder", "/job/tf-acc-test-"+randString),
				),
			},
		},
	})
}

func testAccCheckJenkinsJobDestroy(s *terraform.State) error {
	ctx := context.Background()

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "jenkins_job" {
			continue
		}

		_, err := testAccClient.GetJob(ctx, rs.Primary.ID)
		if err == nil {
			return fmt.Errorf("Job %s still exists", rs.Primary.ID)
		}
	}

	return nil
}

// TestJobTemplateSemanticEquality verifies the framework template plan modifier
// suppresses a diff for semantically-equal (but textually different) XML — the
// behaviour previously provided by the SDKv2 templateDiff DiffSuppressFunc — and
// preserves a diff for a genuine change.
func TestJobTemplateSemanticEquality(t *testing.T) {
	cases := []struct {
		name         string
		state        string
		plan         string
		wantSuppress bool
	}{
		{
			name:         "reformatted equal",
			state:        `<?xml version='1.1' encoding='UTF-8'?><project plugin="x@1.0"> <description>hi</description></project>`,
			plan:         `<project plugin="x@2.0"><description>hi</description></project>`,
			wantSuppress: true,
		},
		{
			name:         "genuine change",
			state:        `<project><description>hi</description></project>`,
			plan:         `<project><description>changed</description></project>`,
			wantSuppress: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := planmodifier.StringRequest{
				StateValue: types.StringValue(tc.state),
				PlanValue:  types.StringValue(tc.plan),
			}
			resp := &planmodifier.StringResponse{PlanValue: req.PlanValue}
			templateSemanticEqualityModifier{}.PlanModifyString(context.Background(), req, resp)

			suppressed := resp.PlanValue.Equal(req.StateValue)
			if suppressed != tc.wantSuppress {
				t.Errorf("suppressed = %v, want %v (plan=%q)", suppressed, tc.wantSuppress, resp.PlanValue.ValueString())
			}
		})
	}
}
