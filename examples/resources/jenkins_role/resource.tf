# A reader / developer / admin trio.

# Global read-only access for everyone who is signed in.
resource "jenkins_role" "reader" {
  type = "global"
  name = "reader"
  permissions = [
    "hudson.model.Hudson.Read",
    "hudson.model.Item.Read",
  ]
  assignments = ["authenticated"]
}

# A developer role scoped to a team's folder, allowing builds and configuration
# of the jobs within it.
resource "jenkins_role" "developer" {
  type    = "item"
  name    = "team-a-developer"
  pattern = "team-a/.*"
  permissions = [
    "hudson.model.Item.Read",
    "hudson.model.Item.Build",
    "hudson.model.Item.Configure",
    "hudson.model.Item.Cancel",
  ]
  assignments = ["alice", "bob", "team-a-group"]
}

# A global administrator role.
resource "jenkins_role" "admin" {
  type        = "global"
  name        = "admin"
  permissions = ["hudson.model.Hudson.Administer"]
  assignments = ["admin-group"]
}
