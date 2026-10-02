package jenkins

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	jenkins "github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccJenkinsFolder_basic(t *testing.T) {
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
				}`, randString, randString),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_folder.foo", "id", "/job/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.foo", "name", "tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.foo", "display_name", ""),
				),
			},
			{
				ResourceName:            "jenkins_folder.foo",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"template"},
			},
		},
	})
}

func TestAccJenkinsFolder_withDisplayName(t *testing.T) {
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
				  display_name = "TF Acceptance Test %s"
				  description = "Terraform acceptance tests %s"
				}`, randString, randString, randString),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_folder.foo", "id", "/job/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.foo", "name", "tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.foo", "display_name", "TF Acceptance Test "+randString),
				),
			},
		},
	})
}

func TestAccJenkinsFolder_nested(t *testing.T) {
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

				resource jenkins_folder sub {
					name = "subfolder"
                    display_name = "TF Acceptance Test %s"
					folder = jenkins_folder.foo.id
					description = "Terraform acceptance tests ${jenkins_folder.foo.name}"
				}`, randString, randString, randString),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_folder.foo", "id", "/job/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.foo", "name", "tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.sub", "id", "/job/tf-acc-test-"+randString+"/job/subfolder"),
					resource.TestCheckResourceAttr("jenkins_folder.sub", "name", "subfolder"),
					resource.TestCheckResourceAttr("jenkins_folder.sub", "folder", "/job/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.sub", "display_name", "TF Acceptance Test "+randString),
				),
			},
		},
	})
}

func TestAccJenkinsFolder_withSecurity(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsFolderDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource jenkins_folder foo {
				  name        = "tf-acc-test-%s"
				  description = "Terraform acceptance tests %s"
				  security {
				    inheritance_strategy = "org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy"
				    permissions = [
				      "hudson.model.Item.Discover:anonymous",
				    ]
				  }
				}`, randString, randString),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_folder.foo", "id", "/job/tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_folder.foo", "security.#", "1"),
					resource.TestCheckTypeSetElemNestedAttrs("jenkins_folder.foo", "security.*", map[string]string{
						"inheritance_strategy": "org.jenkinsci.plugins.matrixauth.inheritance.InheritParentStrategy",
						"permissions.#":        "1",
					}),
					testAccCheckJenkinsFolderHasPermission("jenkins_folder.foo", "hudson.model.Item.Discover:anonymous"),
				),
			},
		},
	})
}

// testAccCheckJenkinsFolderHasPermission asserts that one of the resource's
// "security.*.permissions.*" attributes (a set nested inside a set, so the
// exact flatmap keys are hash-based rather than index-based) equals want.
func testAccCheckJenkinsFolderHasPermission(resourceName, want string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("resource not found: %s", resourceName)
		}

		for k, v := range rs.Primary.Attributes {
			if strings.Contains(k, "permissions.") && v == want {
				return nil
			}
		}
		return fmt.Errorf("no %q attribute matching %q found on %s; attributes: %v", "security.*.permissions.*", want, resourceName, rs.Primary.Attributes)
	}
}

func testAccCheckJenkinsFolderDestroy(s *terraform.State) error {
	ctx := context.Background()

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "jenkins_folder" {
			continue
		}

		_, err := testAccClient.GetJob(ctx, rs.Primary.ID)
		if err == nil {
			return fmt.Errorf("Folder %s still exists", rs.Primary.ID)
		}
	}

	return nil
}

// TestAccJenkinsFolder_withEmptySecurityPermissions covers `permissions = []`
// end to end. Jenkins persists an AuthorizationMatrixProperty with zero
// <permission> children, which encoding/xml decodes into a nil slice —
// reflected naively that becomes a *null* set and Terraform rejects the apply
// with "planned set element ... does not correlate with any element in actual".
// The second step re-applies the same config so a plan/refresh cycle has to
// agree with the server too.
func TestAccJenkinsFolder_withEmptySecurityPermissions(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	config := fmt.Sprintf(`
				resource jenkins_folder foo {
				  name        = "tf-acc-test-%s"
				  description = "Terraform acceptance tests %s"
				  security {
				    inheritance_strategy = "org.jenkinsci.plugins.matrixauth.inheritance.NonInheritingStrategy"
				    permissions          = []
				  }
				}`, randString, randString)

	check := resource.ComposeTestCheckFunc(
		resource.TestCheckResourceAttr("jenkins_folder.foo", "id", "/job/tf-acc-test-"+randString),
		resource.TestCheckResourceAttr("jenkins_folder.foo", "security.#", "1"),
		resource.TestCheckTypeSetElemNestedAttrs("jenkins_folder.foo", "security.*", map[string]string{
			"inheritance_strategy": "org.jenkinsci.plugins.matrixauth.inheritance.NonInheritingStrategy",
			"permissions.#":        "0",
		}),
	)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsFolderDestroy,
		Steps: []resource.TestStep{
			{Config: config, Check: check},
			{Config: config, Check: check},
		},
	})
}

// TestAccJenkinsFolder_folderViewsReadable guards #233. A <folderViews>
// without its class attribute cannot be deserialized (AbstractFolderViewHolder
// is abstract), and Jenkins lists the folder under Manage Old Data. The problem
// only surfaces when Jenkins loads the item from disk, so each step reloads the
// configuration before checking. Step 1 creates the folder, step 2 updates it,
// which covers both render paths, and step 3 re-applies step 2 so a
// plan/refresh cycle has to agree with the server too.
func TestAccJenkinsFolder_folderViewsReadable(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	name := "tf-acc-test-" + randString
	config := func(description string) string {
		return fmt.Sprintf(`
				resource jenkins_folder foo {
				  name        = %q
				  description = %q
				}`, name, description)
	}
	check := resource.ComposeTestCheckFunc(
		resource.TestCheckResourceAttrWith("jenkins_folder.foo", "template", func(v string) error {
			if !strings.Contains(v, `<folderViews class="`) {
				return fmt.Errorf("config.xml has no <folderViews class=...>:\n%s", v)
			}
			return nil
		}),
		testAccCheckJenkinsFolderReadable(name),
	)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsFolderDestroy,
		Steps: []resource.TestStep{
			{Config: config("created"), Check: check},
			{Config: config("updated"), Check: check},
			{Config: config("updated"), Check: check},
		},
	})
}

// testAccCheckJenkinsFolderReadable reloads Jenkins from disk and fails if the
// folder is listed as unreadable data under Manage Old Data.
func testAccCheckJenkinsFolderReadable(name string) resource.TestCheckFunc {
	return func(_ *terraform.State) error {
		ctx := context.Background()
		r, ok := testAccClient.Requester.(*jenkins.Requester)
		if !ok {
			return fmt.Errorf("unexpected requester type %T", testAccClient.Requester)
		}
		if _, err := testAccClient.PostRequest(ctx, "/reload", nil, nil, map[string]string{}); err != nil {
			return fmt.Errorf("reload Jenkins: %w", err)
		}

		// The reload runs in the background and the page is served again once it
		// is done. Fetched with the adapter's own client rather than through
		// Requester.Do, which appends a trailing slash that Jenkins answers with
		// a 404. Each attempt has its own timeout so a stalled request cannot
		// outlive the deadline.
		endpoint := strings.TrimRight(r.Base, "/") + "/administrativeMonitor/OldData/manage"
		var page string
		var lastStatus int
		var lastErr error
		deadline := time.Now().Add(60 * time.Second)
		for {
			page, lastStatus, lastErr = testAccGetPage(ctx, r, endpoint)
			if lastErr == nil && lastStatus == http.StatusOK && strings.Contains(page, "Manage Old Data") {
				break
			}
			if time.Now().After(deadline) {
				return fmt.Errorf("Manage Old Data did not come back after reload (status=%d, err=%v)", lastStatus, lastErr)
			}
			time.Sleep(2 * time.Second)
		}

		// Matched on the folder name and the error that follows it in the same
		// row, without depending on the cell markup. If the page stops using
		// table rows, the rest of the page is checked instead, which can only
		// make the check stricter.
		i := strings.Index(page, name)
		if i < 0 {
			return nil
		}
		row := page[i:]
		if j := strings.Index(row, "</tr>"); j >= 0 {
			row = row[:j]
		}
		if strings.Contains(row, "AbstractFolderViewHolder") {
			return fmt.Errorf("folder %s is listed as unreadable data (AbstractFolderViewHolder)", name)
		}
		return nil
	}
}

// testAccGetPage GETs endpoint with the requester's authenticated client and a
// per-request timeout, and returns the body and status code.
func testAccGetPage(ctx context.Context, r *jenkins.Requester, endpoint string) (string, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", 0, err
	}
	if r.BasicAuth != nil {
		req.SetBasicAuth(r.BasicAuth.Username, r.BasicAuth.Password)
	}
	resp, err := r.Client.Do(req)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	return string(body), resp.StatusCode, err
}
