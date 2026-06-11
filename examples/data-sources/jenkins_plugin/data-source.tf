data "jenkins_plugin" "git" {
  name = "git"
}

output "git_plugin_version" {
  value = data.jenkins_plugin.git.version
}
