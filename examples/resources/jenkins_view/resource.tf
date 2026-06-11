resource "jenkins_folder" "team" {
  name = "platform-team"
}

resource "jenkins_view" "overview" {
  name = "platform-overview"
  assigned_projects = [
    jenkins_folder.team.name,
  ]
}
