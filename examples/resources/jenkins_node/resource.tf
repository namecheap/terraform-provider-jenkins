resource "jenkins_node" "example" {
  name          = "linux-agent-01"
  num_executors = 2
  remote_fs     = "/home/jenkins/agent"
  labels        = "linux docker"
  description   = "Managed by Terraform"
}

# Only builds jobs whose label expression names team-a, so a job with no
# label never lands on it. A label reserves nothing by itself, since any job
# can name it: the job restriction refuses every job outside the team-a
# folder (needs the job-restrictions plugin). The agent connects over
# WebSocket, through the controller's HTTP(S) port.
resource "jenkins_node" "team_a" {
  name       = "team-a-agent-01"
  remote_fs  = "/home/jenkins/agent"
  labels     = "team-a"
  mode       = "EXCLUSIVE"
  web_socket = true

  job_restriction = {
    regex = "^team-a/.*"
  }
}
