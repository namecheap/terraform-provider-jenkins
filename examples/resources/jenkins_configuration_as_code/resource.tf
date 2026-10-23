# Manage settings in one resource per top-level section. This list disables
# informational URL/setup monitors; it does not change authorization or roles.
# JCasC replaces the entire list, so include any other monitors you want disabled.
resource "jenkins_configuration_as_code" "jenkins" {
  section = "jenkins"
  yaml = yamlencode({
    systemMessage = "Managed by Terraform: do not edit in the UI."
    disabledAdministrativeMonitors = [
      "hudson.diagnosis.ReverseProxySetupMonitor",
      "jenkins.diagnostics.RootUrlNotSetMonitor",
    ]
  })

  unordered_paths = ["disabledAdministrativeMonitors"]
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
