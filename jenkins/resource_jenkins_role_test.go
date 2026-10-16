package jenkins

import (
	"context"
	"fmt"
	"regexp"
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
	config := func(permissions, users, groups string) string {
		return fmt.Sprintf(`
resource jenkins_role foo {
	type        = "global"
	name        = "%s"
	permissions = %s
	users       = %s
	groups      = %s
}`, name, permissions, users, groups)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config: config(`["hudson.model.Hudson.Read"]`, `["alice"]`, `["authenticated"]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "id", "global/"+name),
					resource.TestCheckResourceAttr(resourceName, "type", "global"),
					resource.TestCheckResourceAttr(resourceName, "permissions.#", "1"),
					resource.TestCheckTypeSetElemAttr(resourceName, "permissions.*", "hudson.model.Hudson.Read"),
					resource.TestCheckResourceAttr(resourceName, "users.#", "1"),
					resource.TestCheckTypeSetElemAttr(resourceName, "users.*", "alice"),
					resource.TestCheckResourceAttr(resourceName, "groups.#", "1"),
					resource.TestCheckTypeSetElemAttr(resourceName, "groups.*", "authenticated"),
					resource.TestCheckResourceAttr(resourceName, "assignments.#", "0"),
					testAccCheckJenkinsRoleEntries("globalRoles", name,
						roleEntry{Type: roleEntryUser, Sid: "alice"},
						roleEntry{Type: roleEntryGroup, Sid: "authenticated"},
					),
				),
			},
			{
				// Add a permission and a user, drop the group; every set is
				// authoritative. role-strategy 927 keeps the entries across the
				// addRole overwrite, so the provider has to unassign the group.
				Config: config(`["hudson.model.Hudson.Read", "hudson.model.Item.Read"]`, `["alice", "bob"]`, `[]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "permissions.#", "2"),
					resource.TestCheckResourceAttr(resourceName, "users.#", "2"),
					resource.TestCheckResourceAttr(resourceName, "groups.#", "0"),
					testAccCheckJenkinsRoleEntries("globalRoles", name,
						roleEntry{Type: roleEntryUser, Sid: "alice"},
						roleEntry{Type: roleEntryUser, Sid: "bob"},
					),
				),
			},
			{
				// Remove a user while the permissions stay: the role is not
				// overwritten, so only the user is unassigned.
				Config: config(`["hudson.model.Hudson.Read", "hudson.model.Item.Read"]`, `["bob"]`, `[]`),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "users.#", "1"),
					resource.TestCheckTypeSetElemAttr(resourceName, "users.*", "bob"),
					testAccCheckJenkinsRoleEntries("globalRoles", name, roleEntry{Type: roleEntryUser, Sid: "bob"}),
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
				// The deprecated attribute creates an ambiguous entry.
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
					resource.TestCheckResourceAttr(resourceName, "groups.#", "0"),
					testAccCheckJenkinsRoleEntries("projectRoles", name, roleEntry{Type: roleEntryEither, Sid: "team-a-group"}),
				),
			},
			{
				// Migrating the SID to groups replaces the ambiguous entry with a
				// Group entry.
				Config: fmt.Sprintf(`
resource jenkins_role dev {
	type        = "item"
	name        = "%s"
	pattern     = "team-a/.*"
	permissions = ["hudson.model.Item.Read", "hudson.model.Item.Build"]
	groups      = ["team-a-group"]
}`, name),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(resourceName, "assignments.#", "0"),
					resource.TestCheckResourceAttr(resourceName, "groups.#", "1"),
					resource.TestCheckTypeSetElemAttr(resourceName, "groups.*", "team-a-group"),
					testAccCheckJenkinsRoleEntries("projectRoles", name, roleEntry{Type: roleEntryGroup, Sid: "team-a-group"}),
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

func TestAccJenkinsRole_existing(t *testing.T) {
	name := "tf-acc-test-" + acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	ctx := context.Background()

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		Steps: []resource.TestStep{
			{
				// A role created outside Terraform is not taken over: Create
				// fails and the role keeps its entries.
				PreConfig: func() {
					t.Cleanup(func() { _ = testAccClient.RemoveRole(ctx, "globalRoles", name) })
					if err := testAccClient.AddRole(ctx, "globalRoles", name, []string{"hudson.model.Hudson.Read"}, "", false); err != nil {
						t.Fatal(err)
					}
					if err := testAccClient.AssignRole(ctx, "globalRoles", name, roleEntry{Type: roleEntryUser, Sid: "alice"}); err != nil {
						t.Fatal(err)
					}
				},
				Config: fmt.Sprintf(`
resource jenkins_role foo {
	type        = "global"
	name        = "%s"
	permissions = ["hudson.model.Hudson.Read"]
	users       = ["bob"]
}`, name),
				ExpectError: regexp.MustCompile(`already exists`),
			},
			{
				// An empty config: the step only runs the check, against the
				// role left by the failed create.
				Config: ` `,
				Check:  testAccCheckJenkinsRoleEntries("globalRoles", name, roleEntry{Type: roleEntryUser, Sid: "alice"}),
			},
		},
	})
}

// testAccCheckJenkinsRoleEntries asserts the role's entries on the controller,
// types included, which the state alone cannot show for the deprecated
// assignments attribute.
func testAccCheckJenkinsRoleEntries(apiType, name string, want ...roleEntry) resource.TestCheckFunc {
	return func(*terraform.State) error {
		var role roleStrategyRoleResponse
		if err := testAccClient.GetRole(context.Background(), apiType, name, &role); err != nil {
			return err
		}
		got := map[roleEntry]bool{}
		for _, e := range role.Sids {
			got[e] = true
		}
		if len(got) != len(want) {
			return fmt.Errorf("role %s entries = %v, want %v", name, role.Sids, want)
		}
		for _, e := range want {
			if !got[e] {
				return fmt.Errorf("role %s entries = %v, want %v", name, role.Sids, want)
			}
		}
		return nil
	}
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
