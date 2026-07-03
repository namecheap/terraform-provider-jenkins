resource "jenkins_multibranch_pipeline" "example" {
  name        = "my-service"
  description = "Managed by Terraform"
  remote      = "https://github.com/org/my-service.git"
  script_path = "Jenkinsfile"
}
