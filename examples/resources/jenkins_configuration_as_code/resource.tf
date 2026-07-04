# Manage the controller's system message (a safe first step for JCasC adoption).
resource "jenkins_configuration_as_code" "system_message" {
  section = "jenkins"
  yaml = yamlencode({
    systemMessage = "Managed by Terraform — do not edit in the UI."
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
