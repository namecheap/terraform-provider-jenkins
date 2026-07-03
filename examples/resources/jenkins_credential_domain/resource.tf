resource "jenkins_credential_domain" "example" {
  name        = "github"
  description = "Credentials for github.com"
}

# Place a credential into the domain via its `domain` argument:
resource "jenkins_credential_username" "example" {
  name                = "github-bot"
  domain              = jenkins_credential_domain.example.name
  username            = "bot"
  password_wo         = var.bot_token
  password_wo_version = "1"
}
