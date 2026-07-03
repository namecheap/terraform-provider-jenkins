resource "jenkins_node" "example" {
  name          = "linux-agent-01"
  num_executors = 2
  remote_fs     = "/home/jenkins/agent"
  labels        = "linux docker"
  description   = "Managed by Terraform"
}
