resource "jenkins_pipeline_job" "example" {
  name        = "my-pipeline"
  description = "Managed by Terraform"

  script = <<-EOT
    pipeline {
      agent any
      stages {
        stage('build') {
          steps {
            echo 'Hello from Terraform-managed pipeline'
          }
        }
      }
    }
  EOT
}
