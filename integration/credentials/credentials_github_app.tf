resource "tls_private_key" "github_app" {
  algorithm = "RSA"
  rsa_bits  = 2048
}

resource "jenkins_credential_github_app" "global" {
  name        = "global-github-app"
  app_id      = "12345"
  description = "GitHub App for integration testing"
  private_key = tls_private_key.github_app.private_key_pem
}
