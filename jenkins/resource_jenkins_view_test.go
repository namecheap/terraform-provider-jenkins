package jenkins

import (
	"context"
	"fmt"
	"regexp"
	"testing"
	"time"

	"github.com/bndr/gojenkins"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccJenkinsView_basic(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsViewDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource jenkins_view foo {
				  name = "tf-acc-test-%s"
				}`, randString),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_view.foo", "id", "tf-acc-test-"+randString),
					resource.TestCheckResourceAttr("jenkins_view.foo", "name", "tf-acc-test-"+randString),
				),
			},
			{
				ResourceName:      "jenkins_view.foo",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccJenkinsView_folderUnsupported(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource jenkins_view foo {
				  name   = "tf-acc-test-%s"
				  folder = "some-folder"
				}`, randString),
				ExpectError: regexp.MustCompile(`Folder-Scoped Views Not Supported`),
			},
		},
	})
}

func TestAccJenkinsView_withAssignedProjects(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsViewDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
				resource jenkins_folder project {
				  name = "tf-acc-test-%s"
				}

				resource jenkins_view foo {
				  name              = "tf-acc-view-%s"
				  assigned_projects = [jenkins_folder.project.name]
				}`, randString, randString),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("jenkins_view.foo", "id", "tf-acc-view-"+randString),
					resource.TestCheckResourceAttr("jenkins_view.foo", "assigned_projects.#", "1"),
					resource.TestCheckResourceAttr("jenkins_view.foo", "assigned_projects.0", "tf-acc-test-"+randString),
				),
			},
		},
	})
}

func TestWaitForView(t *testing.T) {
	ctx := context.Background()

	// Shrink the retry timings so the exhaustion case does not take seconds.
	origTimeout, origInterval := createViewRetryTimeout, createViewRetryInterval
	createViewRetryTimeout, createViewRetryInterval = 50*time.Millisecond, time.Millisecond
	defer func() { createViewRetryTimeout, createViewRetryInterval = origTimeout, origInterval }()

	t.Run("returns as soon as the view is readable", func(t *testing.T) {
		calls := 0
		r := &ViewResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetView: func(_ context.Context, _ string) (*gojenkins.View, error) {
				calls++
				if calls < 3 {
					return nil, fmt.Errorf("404 view not indexed yet")
				}
				return &gojenkins.View{}, nil
			},
		}}}
		view, err := r.waitForView(ctx, "foo")
		if err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
		if view == nil {
			t.Fatal("expected a view, got nil")
		}
		if calls != 3 {
			t.Errorf("expected 3 GetView calls, got %d", calls)
		}
	})

	t.Run("returns the last error when the window elapses", func(t *testing.T) {
		r := &ViewResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetView: func(_ context.Context, _ string) (*gojenkins.View, error) {
				return nil, fmt.Errorf("still 404")
			},
		}}}
		if _, err := r.waitForView(ctx, "foo"); err == nil {
			t.Fatal("expected an error after the retry window elapsed")
		}
	})

	t.Run("honors context cancellation", func(t *testing.T) {
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		r := &ViewResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetView: func(_ context.Context, _ string) (*gojenkins.View, error) {
				return nil, fmt.Errorf("404")
			},
		}}}
		if _, err := r.waitForView(cctx, "foo"); err == nil {
			t.Fatal("expected a cancellation error")
		}
	})
}

func testAccCheckJenkinsViewDestroy(s *terraform.State) error {
	ctx := context.Background()

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "jenkins_view" {
			continue
		}

		_, err := testAccClient.GetView(ctx, rs.Primary.ID)
		if err == nil {
			return fmt.Errorf("View %s still exists", rs.Primary.ID)
		}
	}

	return nil
}
