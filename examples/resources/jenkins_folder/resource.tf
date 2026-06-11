resource "jenkins_folder" "team" {
  name        = "platform-team"
  description = "Folder for the platform team pipelines"
}

resource "jenkins_folder" "squad" {
  name        = "backend-squad"
  folder      = jenkins_folder.team.id
  description = "Nested subfolder for the backend squad"
}
