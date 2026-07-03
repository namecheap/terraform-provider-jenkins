# Install (or adopt) a plugin, pinning its version.
resource "jenkins_plugin" "git" {
  name    = "git"
  version = "5.2.0"
}

# Track the latest available version (drift-prone) and uninstall on destroy.
resource "jenkins_plugin" "vault" {
  name                 = "hashicorp-vault-plugin"
  uninstall_on_destroy = true
}
