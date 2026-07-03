data "jenkins_jobs" "root" {}

data "jenkins_jobs" "in_folder" {
  folder = "my-folder"
}
