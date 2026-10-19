# Manage the jenkins section. The system message alone is a safe first step for
# JCasC adoption; later jenkins settings go into this same resource, since one
# instance owns a whole top-level section.
#
# The role plugin exports permissions in an order of its own, so the
# permission lists are compared without regard to order.
resource "jenkins_configuration_as_code" "jenkins" {
  section = "jenkins"
  yaml = yamlencode({
    systemMessage = "Managed by Terraform: do not edit in the UI."
    authorizationStrategy = {
      roleBased = {
        roles = {
          global = [{
            name        = "readers"
            permissions = ["Overall/Read", "Job/Read"]
            entries     = [{ group = "authenticated" }]
          }]
        }
      }
    }
  })

  unordered_paths = ["authorizationStrategy.roleBased.roles.*.*.permissions"]
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
