package jenkins

import (
	"context"
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccJenkinsRole_global(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	name := "tf-acc-test-" + randString
	resourceName := "jenkins_role.foo"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource jenkins_role foo {
	type        = "global"
	name        = "%s"
	permissions = ["hudson.model.Hudson.Read"]
	assignments = ["alice"]
}`, name),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "id", "global/"+name),
					resource.TestCheckResourceAttr(resourceName, "type", "global"),
					resource.TestCheckResourceAttr(resourceName, "permissions.#", "1"),
					resource.TestCheckTypeSetElemAttr(resourceName, "permissions.*", "hudson.model.Hudson.Read"),
					resource.TestCheckResourceAttr(resourceName, "assignments.#", "1"),
					resource.TestCheckTypeSetElemAttr(resourceName, "assignments.*", "alice"),
				),
			},
			{
				// Add a permission and an assignment; both sets are authoritative.
				Config: fmt.Sprintf(`
resource jenkins_role foo {
	type        = "global"
	name        = "%s"
	permissions = ["hudson.model.Hudson.Read", "hudson.model.Item.Read"]
	assignments = ["alice", "bob"]
}`, name),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "permissions.#", "2"),
					resource.TestCheckResourceAttr(resourceName, "assignments.#", "2"),
					resource.TestCheckTypeSetElemAttr(resourceName, "assignments.*", "bob"),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccJenkinsRole_item(t *testing.T) {
	randString := acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	name := "tf-acc-test-" + randString
	resourceName := "jenkins_role.dev"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource jenkins_role dev {
	type        = "item"
	name        = "%s"
	pattern     = "team-a/.*"
	permissions = ["hudson.model.Item.Read", "hudson.model.Item.Build"]
	assignments = ["team-a-group"]
}`, name),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "id", "item/"+name),
					resource.TestCheckResourceAttr(resourceName, "pattern", "team-a/.*"),
					resource.TestCheckResourceAttr(resourceName, "permissions.#", "2"),
					resource.TestCheckResourceAttr(resourceName, "assignments.#", "1"),
				),
			},
			{
				ResourceName:      resourceName,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func testAccCheckJenkinsRoleDestroy(s *terraform.State) error {
	ctx := context.Background()

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "jenkins_role" {
			continue
		}

		apiType, ok := roleTypeAPI[rs.Primary.Attributes["type"]]
		if !ok {
			continue
		}

		var role roleStrategyRoleResponse
		if err := testAccClient.GetRole(ctx, apiType, rs.Primary.Attributes["name"], &role); err != nil {
			return err
		}
		if len(role.PermissionIDs) != 0 {
			return fmt.Errorf("Role %s still exists", rs.Primary.ID)
		}
	}

	return nil
}

func TestRoleValidate(t *testing.T) {
	r := &roleResource{resourceHelper: newResourceHelper()}

	tests := []struct {
		name        string
		roleType    string
		pattern     types.String
		wantErr     bool
		wantAPIType string
		wantPattern string
	}{
		{name: "global no pattern", roleType: "global", pattern: types.StringNull(), wantAPIType: "globalRoles"},
		{name: "global with pattern rejected", roleType: "global", pattern: types.StringValue("x/.*"), wantErr: true},
		{name: "item with pattern", roleType: "item", pattern: types.StringValue("team/.*"), wantAPIType: "projectRoles", wantPattern: "team/.*"},
		{name: "item without pattern ok (defaults)", roleType: "item", pattern: types.StringNull(), wantAPIType: "projectRoles", wantPattern: ""},
		{name: "agent with pattern", roleType: "agent", pattern: types.StringValue("linux-.*"), wantAPIType: "slaveRoles", wantPattern: "linux-.*"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var diags diag.Diagnostics
			data := &roleResourceModel{Type: types.StringValue(tt.roleType), Pattern: tt.pattern}
			apiType, pattern, ok := r.validate(data, &diags)
			if tt.wantErr {
				if ok || !diags.HasError() {
					t.Fatalf("expected validation error, got ok=%v diags=%v", ok, diags)
				}
				return
			}
			if !ok || diags.HasError() {
				t.Fatalf("unexpected validation error: %v", diags)
			}
			if apiType != tt.wantAPIType {
				t.Errorf("apiType = %q, want %q", apiType, tt.wantAPIType)
			}
			if pattern != tt.wantPattern {
				t.Errorf("pattern = %q, want %q", pattern, tt.wantPattern)
			}
		})
	}
}
