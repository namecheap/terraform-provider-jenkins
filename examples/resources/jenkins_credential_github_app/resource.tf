resource "jenkins_credential_github_app" "example" {
  name        = "my-github-app"
  app_id      = "123456"
  private_key = file("/path/to/github-app.pem")
}
