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
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
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

// An item or agent role without a configured pattern plans the ".*" the
// plugin stores, so an update leaves the pattern, and the role, alone.
func TestAccJenkinsRole_defaultPattern(t *testing.T) {
	name := "tf-acc-test-" + acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	resourceName := "jenkins_role.dev"
	config := func(roleType, users string) string {
		return fmt.Sprintf(`
resource jenkins_role dev {
	type        = "%s"
	name        = "%s"
	permissions = ["hudson.model.Item.Read"]
	users       = %s
}`, roleType, name, users)
	}
	pattern := func(want knownvalue.Check) plancheck.PlanCheck {
		return plancheck.ExpectKnownValue(resourceName, tfjsonpath.New("pattern"), want)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config:           config("global", `["alice"]`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{pattern(knownvalue.Null())}},
				Check:            resource.TestCheckNoResourceAttr(resourceName, "pattern"),
			},
			{
				// A type change replaces the role; the new item role gets ".*".
				Config: config("item", `["alice"]`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionReplace),
					pattern(knownvalue.StringExact(".*")),
				}},
				Check: resource.TestCheckResourceAttr(resourceName, "pattern", ".*"),
			},
			{
				Config: config("item", `["alice", "bob"]`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(resourceName, plancheck.ResourceActionUpdate),
					pattern(knownvalue.StringExact(".*")),
				}},
				Check: testAccCheckJenkinsRoleEntries("projectRoles", name,
					roleEntry{Type: roleEntryUser, Sid: "alice"}, roleEntry{Type: roleEntryUser, Sid: "bob"}),
			},
			{
				Config:           config("item", `["alice", "bob"]`),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
			},
		},
	})
}

func TestAccJenkinsRole_emptyPattern(t *testing.T) {
	name := "tf-acc-test-" + acctest.RandStringFromCharSet(10, acctest.CharSetAlphaNum)
	for _, roleType := range []string{"item", "global"} {
		t.Run(roleType, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				PreCheck:                 func() { testAccPreCheck(t) },
				ProtoV6ProviderFactories: testAccProviders,
				CheckDestroy:             testAccCheckJenkinsRoleDestroy,
				Steps: []resource.TestStep{
					{
						Config: fmt.Sprintf(`
resource jenkins_role dev {
	type        = "%s"
	name        = "%s"
	pattern     = ""
	permissions = ["hudson.model.Item.Read"]
}`, roleType, name),
						ExpectError: regexp.MustCompile(`string length must be at least 1`),
					},
				},
			})
		})
	}
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

// testAccJenkinsRoleCommaConfig renders one global role assigned to user. The
// comma tests use it for the comma-free roles a comma name could reach.
func testAccJenkinsRoleCommaConfig(label, name, permissions, user string) string {
	return fmt.Sprintf(`
resource jenkins_role %s {
	type        = "global"
	name        = %q
	permissions = %s
	users       = [%q]
}`, label, name, permissions, user)
}

// role-strategy's removeRoles splits role names on ",", so destroying a role
// named "<team>,<victim>" would delete the victim role and leave the comma
// role behind. The name is refused at plan, for a new role and for a rename,
// and the comma-free roles around it keep their permissions and entries.
func TestAccJenkinsRole_commaName(t *testing.T) {
	prefix := "tfacc-sec256-" + acctest.RandStringFromCharSet(8, acctest.CharSetAlphaNum)
	victim, team := prefix+"-victim", prefix+"-team"
	commaName := team + "," + victim
	victimConfig := testAccJenkinsRoleCommaConfig("victim", victim, `["hudson.model.Hudson.Read"]`, prefix+"-alice")
	teamConfig := func(name string) string {
		return victimConfig + testAccJenkinsRoleCommaConfig("team", name, `["hudson.model.Hudson.Read"]`, prefix+"-bob")
	}
	nameRejected := regexp.MustCompile(`Attribute\s+name\s+must\s+not\s+contain\s+a\s+comma`)
	victimIntact := testAccCheckJenkinsRoleEntries("globalRoles", victim, roleEntry{Type: roleEntryUser, Sid: prefix + "-alice"})

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config: victimConfig,
				Check:  victimIntact,
			},
			{
				// A new role: refused by plan, so nothing is ever applied.
				Config:      teamConfig(commaName),
				PlanOnly:    true,
				ExpectError: nameRejected,
			},
			{
				// Neither the comma role nor a role named after its first
				// part was created, and the victim is untouched.
				Config:           victimConfig,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check: resource.ComposeTestCheckFunc(
					victimIntact,
					testAccCheckJenkinsRolesAbsent("globalRoles", commaName, team),
				),
			},
			{
				Config: teamConfig(team),
				Check:  testAccCheckJenkinsRoleEntries("globalRoles", team, roleEntry{Type: roleEntryUser, Sid: prefix + "-bob"}),
			},
			{
				// A rename replaces the role: refused by plan as well, so the
				// comma-free role is neither destroyed nor recreated.
				Config:      teamConfig(commaName),
				PlanOnly:    true,
				ExpectError: nameRejected,
			},
			{
				Config:           teamConfig(team),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check: resource.ComposeTestCheckFunc(
					victimIntact,
					testAccCheckJenkinsRolePermissions("globalRoles", victim, "hudson.model.Hudson.Read"),
					testAccCheckJenkinsRoleEntries("globalRoles", team, roleEntry{Type: roleEntryUser, Sid: prefix + "-bob"}),
					testAccCheckJenkinsRolesAbsent("globalRoles", commaName),
				),
			},
		},
	})
}

// addRole receives the permission IDs joined with ",", so the element
// "hudson.model.Hudson.Read,hudson.model.Hudson.Administer" would grant
// Administer through a role planned with a single permission. It is refused at
// plan, on update and on create, and the role keeps exactly its permissions.
func TestAccJenkinsRole_commaPermission(t *testing.T) {
	prefix := "tfacc-sec256-" + acctest.RandStringFromCharSet(8, acctest.CharSetAlphaNum)
	reader, other := prefix+"-reader", prefix+"-other"
	const read = `["hudson.model.Hudson.Read"]`
	const joined = `["hudson.model.Hudson.Read,hudson.model.Hudson.Administer"]`
	readerConfig := func(permissions string) string {
		return testAccJenkinsRoleCommaConfig("reader", reader, permissions, prefix+"-alice")
	}
	permissionRejected := regexp.MustCompile(`Attribute\s+permissions\[.*\]\s+must\s+not\s+contain\s+a\s+comma`)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config: readerConfig(read),
				Check:  testAccCheckJenkinsRolePermissions("globalRoles", reader, "hudson.model.Hudson.Read"),
			},
			{
				// Update: the overwrite would grant Administer.
				Config:      readerConfig(joined),
				PlanOnly:    true,
				ExpectError: permissionRejected,
			},
			{
				// Create.
				Config:      readerConfig(read) + testAccJenkinsRoleCommaConfig("other", other, joined, prefix+"-bob"),
				PlanOnly:    true,
				ExpectError: permissionRejected,
			},
			{
				Config:           readerConfig(read),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsRolePermissions("globalRoles", reader, "hudson.model.Hudson.Read"),
					testAccCheckJenkinsRoleEntries("globalRoles", reader, roleEntry{Type: roleEntryUser, Sid: prefix + "-alice"}),
					testAccCheckJenkinsRolesAbsent("globalRoles", other),
				),
			},
		},
	})
}

// A name or permission computed by another resource is unknown at plan, so
// the plan-time check passes it. Terraform validates the configuration again
// at apply, once the value is known, and the role is refused before it is
// created.
func TestAccJenkinsRole_commaUnknownAtPlan(t *testing.T) {
	prefix := "tfacc-sec256-" + acctest.RandStringFromCharSet(8, acctest.CharSetAlphaNum)
	victim, team, reader := prefix+"-victim", prefix+"-team", prefix+"-reader"
	commaName := team + "," + victim
	victimConfig := testAccJenkinsRoleCommaConfig("victim", victim, `["hudson.model.Hudson.Read"]`, prefix+"-alice")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config: victimConfig,
			},
			{
				Config: victimConfig + fmt.Sprintf(`
resource terraform_data name {
	input = %q
}

resource jenkins_role team {
	type        = "global"
	name        = terraform_data.name.output
	permissions = ["hudson.model.Hudson.Read"]
}`, commaName),
				ExpectError: regexp.MustCompile(`Attribute\s+name\s+must\s+not\s+contain\s+a\s+comma`),
			},
			{
				Config: victimConfig + fmt.Sprintf(`
resource terraform_data permission {
	input = "hudson.model.Hudson.Read,hudson.model.Hudson.Administer"
}

resource jenkins_role reader {
	type        = "global"
	name        = %q
	permissions = [terraform_data.permission.output]
}`, reader),
				ExpectError: regexp.MustCompile(`Attribute\s+permissions\[.*\]\s+must\s+not\s+contain\s+a\s+comma`),
			},
			{
				Config: victimConfig,
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsRoleEntries("globalRoles", victim, roleEntry{Type: roleEntryUser, Sid: prefix + "-alice"}),
					testAccCheckJenkinsRolesAbsent("globalRoles", commaName, team, reader),
				),
			},
		},
	})
}

// ImportState refuses a role name holding a comma, since the imported role
// could not be destroyed without deleting other roles. terraform import and an
// import block are both refused, and the role the name points at is left as
// it was.
func TestAccJenkinsRole_commaImport(t *testing.T) {
	prefix := "tfacc-sec256-" + acctest.RandStringFromCharSet(8, acctest.CharSetAlphaNum)
	victim, team := prefix+"-victim", prefix+"-team"
	commaName := team + "," + victim
	victimConfig := fmt.Sprintf(`
resource jenkins_role victim {
	type        = "item"
	name        = %q
	pattern     = %q
	permissions = ["hudson.model.Item.Read"]
	groups      = [%q]
}`, victim, prefix+"/.*", prefix+"-group")
	// The import target's own attributes are never used: only the import
	// identifier carries the comma name.
	importConfig := fmt.Sprintf(`
resource jenkins_role team {
	type        = "item"
	name        = %q
	permissions = ["hudson.model.Item.Read"]
}`, team)
	importRejected := regexp.MustCompile(`(?s)Unable to Import Role.*contains\s+a\s+comma`)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProviders,
		CheckDestroy:             testAccCheckJenkinsRoleDestroy,
		Steps: []resource.TestStep{
			{
				Config: victimConfig,
			},
			{
				Config:        importConfig,
				ResourceName:  "jenkins_role.team",
				ImportState:   true,
				ImportStateId: "item/" + commaName,
				ExpectError:   importRejected,
			},
			{
				// An import block imports at plan.
				Config: victimConfig + importConfig + fmt.Sprintf(`
import {
	to = jenkins_role.team
	id = %q
}`, "item/"+commaName),
				PlanOnly:    true,
				ExpectError: importRejected,
			},
			{
				Config:           victimConfig,
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check: resource.ComposeTestCheckFunc(
					testAccCheckJenkinsRoleEntries("projectRoles", victim, roleEntry{Type: roleEntryGroup, Sid: prefix + "-group"}),
					testAccCheckJenkinsRolePermissions("projectRoles", victim, "hudson.model.Item.Read"),
					testAccCheckJenkinsRolesAbsent("projectRoles", commaName, team),
				),
			},
		},
	})
}

// testAccCheckJenkinsRolesAbsent asserts that none of the named roles exists
// on the controller. getRole takes a role name whole, so it is safe to call
// with a comma name.
func testAccCheckJenkinsRolesAbsent(apiType string, names ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		for _, name := range names {
			var role roleStrategyRoleResponse
			if err := testAccClient.GetRole(context.Background(), apiType, name, &role); err != nil {
				return err
			}
			if role.PermissionIDs != nil {
				return fmt.Errorf("role %s exists, want it absent", name)
			}
		}
		return nil
	}
}

// testAccCheckJenkinsRolePermissions asserts the role's permissions on the
// controller, which the state cannot show when an element was split.
func testAccCheckJenkinsRolePermissions(apiType, name string, want ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		var role roleStrategyRoleResponse
		if err := testAccClient.GetRole(context.Background(), apiType, name, &role); err != nil {
			return err
		}
		if len(role.PermissionIDs) != len(want) {
			return fmt.Errorf("role %s permissions = %v, want %v", name, role.PermissionIDs, want)
		}
		for _, p := range want {
			if !role.PermissionIDs[p] {
				return fmt.Errorf("role %s permissions = %v, want %v", name, role.PermissionIDs, want)
			}
		}
		return nil
	}
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
