package jenkins

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccJenkinsUser_basic(t *testing.T) {
	randString := strings.ToLower(acctest.RandStringFromCharSet(10, acctest.CharSetAlpha))
	username := "tf-acc-test-" + randString
	resourceName := "jenkins_user.foo"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsUserDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource jenkins_user foo {
	username  = "%s"
	password  = "s3cr3t-passw0rd"
	full_name = "Test User"
	email     = "%s@example.com"
}`, username, username),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "id", username),
					resource.TestCheckResourceAttr(resourceName, "username", username),
					resource.TestCheckResourceAttr(resourceName, "full_name", "Test User"),
					resource.TestCheckResourceAttr(resourceName, "email", username+"@example.com"),
				),
			},
			{
				// Changing full_name recreates the user (all attributes force
				// replacement); confirm the new value is applied.
				Config: fmt.Sprintf(`
resource jenkins_user foo {
	username  = "%s"
	password  = "s3cr3t-passw0rd"
	full_name = "Renamed User"
	email     = "%s@example.com"
}`, username, username),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "full_name", "Renamed User"),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
				// Jenkins never returns the password, so it cannot be verified.
				ImportStateVerifyIgnore: []string{"password"},
			},
		},
	})
}

func testAccCheckJenkinsUserDestroy(s *terraform.State) error {
	ctx := context.Background()

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "jenkins_user" {
			continue
		}

		var u jenkinsUserResponse
		err := testAccClient.GetUser(ctx, rs.Primary.ID, &u)
		if err == nil && u.ID != "" {
			return fmt.Errorf("User %s still exists", rs.Primary.ID)
		}
	}

	return nil
}

func TestUserPopulate(t *testing.T) {
	ctx := context.Background()

	t.Run("reads full_name and email", func(t *testing.T) {
		r := &userResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetUser: func(_ context.Context, _ string, out interface{}) error {
				u := out.(*jenkinsUserResponse)
				u.ID = "alice"
				u.FullName = "Alice Example"
				u.Property = []jenkinsUserProperty{{}, {Address: "alice@example.com"}}
				return nil
			},
		}}}
		data := &userResourceModel{Username: types.StringValue("alice")}
		var diags diag.Diagnostics
		if ok := r.populate(ctx, data, &diags); !ok || diags.HasError() {
			t.Fatalf("populate failed: ok=%v diags=%v", ok, diags)
		}
		if data.FullName.ValueString() != "Alice Example" {
			t.Errorf("full_name = %q, want %q", data.FullName.ValueString(), "Alice Example")
		}
		if data.Email.ValueString() != "alice@example.com" {
			t.Errorf("email = %q, want %q", data.Email.ValueString(), "alice@example.com")
		}
	})

	t.Run("missing user returns not found", func(t *testing.T) {
		r := &userResource{resourceHelper: &resourceHelper{client: &mockJenkinsClient{
			mockGetUser: func(_ context.Context, _ string, _ interface{}) error {
				return fmt.Errorf("404 user not found")
			},
		}}}
		data := &userResourceModel{Username: types.StringValue("ghost")}
		var diags diag.Diagnostics
		if ok := r.populate(ctx, data, &diags); ok {
			t.Error("expected populate to report not found")
		}
		if diags.HasError() {
			t.Errorf("expected no error diagnostic for not-found, got %v", diags)
		}
	})
}
