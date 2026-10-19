package jenkins

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"gopkg.in/yaml.v3"
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

// TestAccJenkinsConfigurationAsCode_secretNotInState asserts the Q3 invariant
// of docs/design/casc.md after out-of-band drift and after import (#249).
// JCasC resolves ${decodeBase64:...} on apply, so the resolved value stands in
// for a secret: the controller holds it, the configuration does not.
func TestAccJenkinsConfigurationAsCode_secretNotInState(t *testing.T) {
	resolved := "tf-acc-secret-" + acctest.RandStringFromCharSet(12, acctest.CharSetAlphaNum)
	encoded := base64.StdEncoding.EncodeToString([]byte(resolved))
	resourceName := "jenkins_configuration_as_code.test"
	config := fmt.Sprintf(`
resource jenkins_configuration_as_code test {
  section = "jenkins"
  yaml    = yamlencode({
    systemMessage = "$${decodeBase64:%s}"
    quietPeriod   = 5
  })
}`, encoded)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckCASCSectionContains("jenkins", resolved),
					testAccCheckStateOmits(resolved),
				),
			},
			{
				// Drift on a sibling key: the refreshed yaml shows it, keeps the
				// placeholder, and still differs from the configuration.
				PreConfig: func() {
					if err := testAccClient.ApplyCASC(context.Background(), "jenkins:\n  quietPeriod: 7\n"); err != nil {
						t.Fatalf("inducing drift: %v", err)
					}
				},
				RefreshState:       true,
				ExpectNonEmptyPlan: true,
				RefreshPlanChecks: resource.RefreshPlanChecks{PostRefresh: []plancheck.PlanCheck{
					expectPlanOmits(resolved),
				}},
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStateOmits(resolved),
					resource.TestMatchResourceAttr(resourceName, "yaml", regexp.MustCompile(`quietPeriod: 7`)),
					resource.TestMatchResourceAttr(resourceName, "yaml", regexp.MustCompile(`\$\{decodeBase64:`)),
					// Nothing else from the exported jenkins section, such as
					// securityRealm or authorizationStrategy, reaches state.
					testAccCheckCASCStateYAML(resourceName, fmt.Sprintf("systemMessage: \"${decodeBase64:%s}\"\nquietPeriod: 7\n", encoded)),
				),
			},
			{
				// Re-applying reconciles the drift.
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStateOmits(resolved),
					testAccCheckCASCSectionContains("jenkins", "quietPeriod: 5"),
				),
			},
			{
				ResourceName:            resourceName,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"yaml"},
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					for _, s := range states {
						for k, v := range s.Attributes {
							if strings.Contains(v, resolved) {
								return fmt.Errorf("imported attribute %s holds the resolved secret", k)
							}
						}
					}
					return nil
				},
			},
		},
	})
}

// TestAccJenkinsConfigurationAsCode_embeddedSecretNotInState covers a ${...}
// expression embedded in a longer string (#249). JCasC resolves it in place, so
// the controller value never equals the configured one and every refresh sees
// drift. The resolved value must stay out of state and plans on the first
// refresh, on a refresh of the stored view, and after a re-apply.
func TestAccJenkinsConfigurationAsCode_embeddedSecretNotInState(t *testing.T) {
	resolved := "tfacc-sec253-" + acctest.RandStringFromCharSet(12, acctest.CharSetAlphaNum)
	resourceName := "jenkins_configuration_as_code.test"
	config := fmt.Sprintf(`
resource jenkins_configuration_as_code test {
  section = "unclassified"
  yaml    = yamlencode({
    location = {
      adminAddress = "tfacc-sec253 <$${decodeBase64:%s}@example.invalid>"
    }
  })
}`, base64.StdEncoding.EncodeToString([]byte(resolved)))
	// Refresh stores only the declared key, with its value hidden.
	view := "location:\n  adminAddress: '" + cascDriftMarker + "'\n"
	omits := []plancheck.PlanCheck{expectPlanOmits(resolved)}
	refreshed := resource.TestStep{
		RefreshState:       true,
		ExpectNonEmptyPlan: true,
		RefreshPlanChecks:  resource.RefreshPlanChecks{PostRefresh: omits},
		Check: resource.ComposeTestCheckFunc(
			testAccCheckStateOmits(resolved),
			testAccCheckCASCStateYAML(resourceName, view),
		),
	}

	var adminAddress string
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			adminAddress = testAccCASCAdminAddress(t)
		},
		ProtoV6ProviderFactories: testAccProviders,
		// Delete leaves the applied value on the controller; put the original
		// address back.
		CheckDestroy: func(*terraform.State) error {
			return testAccClient.ApplyCASC(context.Background(), testAccCASCAdminAddressDoc(adminAddress))
		},
		Steps: []resource.TestStep{
			{
				Config: config,
				// The embedded expression never compares equal to its
				// resolved value, so the plan after apply is never empty.
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks:   resource.ConfigPlanChecks{PostApplyPostRefresh: omits},
				Check: resource.ComposeTestCheckFunc(
					testAccCheckCASCSectionContains("unclassified", resolved),
					testAccCheckStateOmits(resolved),
				),
			},
			// The first refresh stores the view; the second reads it back as
			// the declared document.
			refreshed,
			refreshed,
			{
				// Re-applying re-sends the expression, and the controller
				// still resolves it.
				Config:             config,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks:   resource.ConfigPlanChecks{PreApply: omits, PostApplyPostRefresh: omits},
				Check: resource.ComposeTestCheckFunc(
					testAccCheckCASCSectionContains("unclassified", resolved),
					testAccCheckStateOmits(resolved),
				),
			},
		},
	})
}

// TestAccJenkinsConfigurationAsCode_sequenceSecretNotInState covers a drifted
// sequence carrying a ${...} expression (#249). The controller keeps global
// environment variables sorted by key, so a list declared out of order comes
// back reordered on every refresh; matched by index, the resolved value would
// land under the other entry's declared plain value. The refreshed yaml must
// hide the list whole on the first refresh, on a refresh of the stored view,
// and after a re-apply, while the controller still resolves the expression.
func TestAccJenkinsConfigurationAsCode_sequenceSecretNotInState(t *testing.T) {
	prefix := "tfacc-sec253-" + acctest.RandStringFromCharSet(8, acctest.CharSetAlphaNum)
	resolved := "tfacc-sec253-" + acctest.RandStringFromCharSet(12, acctest.CharSetAlphaNum)
	resourceName := "jenkins_configuration_as_code.test"
	config := fmt.Sprintf(`
resource jenkins_configuration_as_code test {
  section = "jenkins"
  yaml    = yamlencode({
    globalNodeProperties = [{
      envVars = {
        env = [
          { key = "%[1]s-b", value = "tfacc-sec253-plain" },
          { key = "%[1]s-a", value = "$${decodeBase64:%[2]s}" },
        ]
      }
    }]
  })
}`, prefix, base64.StdEncoding.EncodeToString([]byte(resolved)))
	// Refresh stores only the declared key, with the whole list hidden.
	view := "globalNodeProperties: '" + cascDriftMarker + "'\n"
	omits := []plancheck.PlanCheck{expectPlanOmits(resolved)}
	refreshed := resource.TestStep{
		RefreshState:       true,
		ExpectNonEmptyPlan: true,
		RefreshPlanChecks:  resource.RefreshPlanChecks{PostRefresh: omits},
		Check: resource.ComposeTestCheckFunc(
			testAccCheckStateOmits(resolved),
			testAccCheckCASCStateYAML(resourceName, view),
		),
	}

	var original interface{}
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			original = testAccCASCJenkinsValue(t, "globalNodeProperties")
		},
		ProtoV6ProviderFactories: testAccProviders,
		// Delete leaves the applied list on the controller; put the original
		// global node properties back.
		CheckDestroy: func(*terraform.State) error {
			props := original
			if props == nil {
				props = []interface{}{}
			}
			doc, err := yaml.Marshal(map[string]interface{}{
				"jenkins": map[string]interface{}{"globalNodeProperties": props},
			})
			if err != nil {
				return err
			}
			return testAccClient.ApplyCASC(context.Background(), string(doc))
		},
		Steps: []resource.TestStep{
			{
				Config: config,
				// The controller reorders the list, so the plan after apply
				// is never empty.
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks:   resource.ConfigPlanChecks{PostApplyPostRefresh: omits},
				Check: resource.ComposeTestCheckFunc(
					testAccCheckCASCSectionContains("jenkins", resolved),
					testAccCheckStateOmits(resolved),
				),
			},
			// The first refresh stores the view; the second reads it back as
			// the declared document.
			refreshed,
			refreshed,
			{
				Config:             config,
				ExpectNonEmptyPlan: true,
				ConfigPlanChecks:   resource.ConfigPlanChecks{PreApply: omits, PostApplyPostRefresh: omits},
				Check: resource.ComposeTestCheckFunc(
					testAccCheckCASCSectionContains("jenkins", resolved),
					testAccCheckStateOmits(resolved),
				),
			},
		},
	})
}

// TestAccJenkinsConfigurationAsCode_grownSequenceNotInState covers a declared
// list the controller grew (#249). Another JCasC source adds a global
// environment variable whose value it resolved from a ${...}, and the
// controller keeps the variables sorted by key, so the new entry sorts before
// the declared one. Matched by index, it would be stored as the declared
// entry's drifted value. The refreshed yaml must hide the grown list whole, on
// the first refresh and on a refresh of the stored view.
func TestAccJenkinsConfigurationAsCode_grownSequenceNotInState(t *testing.T) {
	prefix := "tfacc-gap253-" + acctest.RandStringFromCharSet(8, acctest.CharSetAlphaNum)
	resolved := "tfacc-gap253-" + acctest.RandStringFromCharSet(12, acctest.CharSetAlphaNum)
	resourceName := "jenkins_configuration_as_code.test"
	config := fmt.Sprintf(`
resource jenkins_configuration_as_code test {
  section = "jenkins"
  yaml    = yamlencode({
    globalNodeProperties = [{
      envVars = {
        env = [{ key = "%s-b", value = "tfacc-gap253-plain" }]
      }
    }]
  })
}`, prefix)
	grown := fmt.Sprintf(`jenkins:
  globalNodeProperties:
    - envVars:
        env:
          - key: "%[1]s-b"
            value: tfacc-gap253-plain
          - key: "%[1]s-a"
            value: "${decodeBase64:%[2]s}"
`, prefix, base64.StdEncoding.EncodeToString([]byte(resolved)))
	// Refresh stores only the declared keys, with the grown list hidden.
	view := "globalNodeProperties:\n  - envVars:\n      env: '" + cascDriftMarker + "'\n"
	omits := []plancheck.PlanCheck{expectPlanOmits(resolved), expectPlanOmits(prefix + "-a")}
	refreshed := resource.TestStep{
		RefreshState:       true,
		ExpectNonEmptyPlan: true,
		RefreshPlanChecks:  resource.RefreshPlanChecks{PostRefresh: omits},
		Check: resource.ComposeTestCheckFunc(
			testAccCheckStateOmits(resolved),
			testAccCheckStateOmits(prefix+"-a"),
			testAccCheckCASCStateYAML(resourceName, view),
		),
	}

	var original interface{}
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			testAccPreCheck(t)
			original = testAccCASCJenkinsValue(t, "globalNodeProperties")
		},
		ProtoV6ProviderFactories: testAccProviders,
		// Delete leaves the applied list on the controller; put the original
		// global node properties back.
		CheckDestroy: func(*terraform.State) error {
			props := original
			if props == nil {
				props = []interface{}{}
			}
			doc, err := yaml.Marshal(map[string]interface{}{
				"jenkins": map[string]interface{}{"globalNodeProperties": props},
			})
			if err != nil {
				return err
			}
			return testAccClient.ApplyCASC(context.Background(), string(doc))
		},
		Steps: []resource.TestStep{
			{
				Config: config,
			},
			{
				PreConfig: func() {
					if err := testAccClient.ApplyCASC(context.Background(), grown); err != nil {
						t.Fatalf("adding an environment variable: %v", err)
					}
				},
				RefreshState:       true,
				ExpectNonEmptyPlan: true,
				RefreshPlanChecks:  resource.RefreshPlanChecks{PostRefresh: omits},
				Check: resource.ComposeTestCheckFunc(
					// The controller resolved the value and sorted the
					// added entry first.
					testAccCheckCASCSectionContains("jenkins", resolved),
					func(*terraform.State) error {
						exported, err := testAccClient.ExportCASC(context.Background())
						if err != nil {
							return fmt.Errorf("exporting JCasC config: %w", err)
						}
						a, b := strings.Index(exported, prefix+"-a"), strings.Index(exported, prefix+"-b")
						if a < 0 || b < 0 || a > b {
							return fmt.Errorf("the added variable is not exported before the declared one (at %d and %d)", a, b)
						}
						return nil
					},
					testAccCheckStateOmits(resolved),
					testAccCheckStateOmits(prefix+"-a"),
					testAccCheckCASCStateYAML(resourceName, view),
				),
			},
			// The second refresh reads the stored view back as the declared
			// document.
			refreshed,
			{
				// Re-applying replaces the list with the declared one.
				Config: config,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckStateOmits(resolved),
				),
			},
		},
	})
}

// testAccCASCJenkinsValue returns the controller's value for key in the
// exported jenkins section, or nil when it is not exported.
func testAccCASCJenkinsValue(t *testing.T, key string) interface{} {
	t.Helper()
	exported, err := testAccClient.ExportCASC(context.Background())
	if err != nil {
		t.Fatalf("exporting JCasC config: %v", err)
	}
	sub, found, err := exportedSection(exported, "jenkins")
	if err != nil || !found {
		t.Fatalf("jenkins section: found=%v err=%v", found, err)
	}
	section, _ := sub.(map[string]interface{})
	return section[key]
}

// testAccCASCAdminAddress returns the controller's
// unclassified.location.adminAddress.
func testAccCASCAdminAddress(t *testing.T) string {
	t.Helper()
	exported, err := testAccClient.ExportCASC(context.Background())
	if err != nil {
		t.Fatalf("exporting JCasC config: %v", err)
	}
	sub, found, err := exportedSection(exported, "unclassified")
	if err != nil || !found {
		t.Fatalf("unclassified section: found=%v err=%v", found, err)
	}
	section, _ := sub.(map[string]interface{})
	location, _ := section["location"].(map[string]interface{})
	addr, ok := location["adminAddress"].(string)
	if !ok {
		t.Fatalf("unclassified.location.adminAddress not exported:\n%s", exported)
	}
	return addr
}

// testAccCASCAdminAddressDoc renders a JCasC document setting
// unclassified.location.adminAddress to addr.
func testAccCASCAdminAddressDoc(addr string) string {
	out, _ := yaml.Marshal(map[string]interface{}{
		"unclassified": map[string]interface{}{
			"location": map[string]interface{}{"adminAddress": addr},
		},
	})
	return string(out)
}

// testAccCheckCASCStateYAML asserts the yaml attribute in state is
// semantically equal to want, so nothing beyond the expected keys is stored.
func testAccCheckCASCStateYAML(resourceName, want string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("%s not found in state", resourceName)
		}
		got, err := parseYAML(rs.Primary.Attributes["yaml"])
		if err != nil {
			return fmt.Errorf("parsing state yaml: %w", err)
		}
		wantDoc, err := parseYAML(want)
		if err != nil {
			return fmt.Errorf("parsing expected yaml: %w", err)
		}
		if !reflect.DeepEqual(got, wantDoc) {
			return fmt.Errorf("%s.yaml =\n%s\nwant:\n%s", resourceName, rs.Primary.Attributes["yaml"], want)
		}
		return nil
	}
}

// expectPlanOmits is a plan check that fails if value appears anywhere in the
// plan, including the refreshed prior state and the planned changes.
type expectPlanOmits string

func (v expectPlanOmits) CheckPlan(_ context.Context, req plancheck.CheckPlanRequest, resp *plancheck.CheckPlanResponse) {
	raw, err := json.Marshal(req.Plan)
	if err != nil {
		resp.Error = fmt.Errorf("encoding plan: %w", err)
		return
	}
	if strings.Contains(string(raw), string(v)) {
		resp.Error = fmt.Errorf("plan holds %q", string(v))
	}
}

// testAccCheckStateOmits fails if any attribute of any resource in state
// contains value.
func testAccCheckStateOmits(value string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		for name, rs := range s.RootModule().Resources {
			for k, v := range rs.Primary.Attributes {
				if strings.Contains(v, value) {
					return fmt.Errorf("%s.%s holds %q", name, k, value)
				}
			}
		}
		return nil
	}
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
