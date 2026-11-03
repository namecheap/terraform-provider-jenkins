# Start with the system message. Add later jenkins settings to this resource;
# one instance owns a whole top-level section.
resource "jenkins_configuration_as_code" "jenkins" {
  section = "jenkins"
  yaml = yamlencode({
    systemMessage = "Managed by Terraform: do not edit in the UI."
  })
}

# A second instance can own a different top-level section. Secrets use JCasC
# ${VAR} interpolation, so the resolved value never enters Terraform state.
resource "jenkins_configuration_as_code" "unclassified" {
  section = "unclassified"
  yaml    = <<-YAML
    globalLibraries:
      libraries:
        - name: shared-pipeline
          defaultVersion: main
          retriever:
            modernSCM:
              scm:
                git:
                  remote: "https://git.example.com/jenkins/shared-pipeline.git"
                  credentialsId: "shared-pipeline-token"
  YAML
}

# Optional OIDC example: replace the jenkins resource above; do not add a second
# resource for the same section. WARNING: this replaces the entire security
# realm. Configure and test your IdP, client ID, client secret and administrator
# access first, and arrange filesystem recovery access before applying.
# Supply var.oidc_client_secret as a sensitive or ephemeral input. State retains
# only ${client_secret}; bump secrets_wo_version when rotating it. Every section
# apply sends the current write-only values.
#
# resource "jenkins_configuration_as_code" "jenkins" {
#   section = "jenkins"
#   yaml = yamlencode({
#     securityRealm = {
#       oic = {
#         clientId     = "jenkins"
#         clientSecret = "$${client_secret}"
#         serverConfiguration = {
#           wellKnown = {
#             wellKnownOpenIDConfigurationUrl = "https://sso.example.com/.well-known/openid-configuration"
#           }
#         }
#       }
#     }
#   })
#
#   secrets_wo         = { client_secret = var.oidc_client_secret }
#   secrets_wo_version = "1"
# }
