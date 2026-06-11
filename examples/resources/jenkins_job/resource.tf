resource "jenkins_folder" "team" {
  name = "platform-team"
}

resource "jenkins_job" "deploy" {
  name   = "deploy-backend"
  folder = jenkins_folder.team.id

  template = templatefile("${path.module}/pipeline.xml", {
    repo_url       = "https://github.com/example/backend.git"
    default_branch = "main"
  })
}
