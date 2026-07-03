# Configure the Jenkins Provider
provider "jenkins" {
  server_url = "http://localhost:8080" # Or JENKINS_URL env var
  username   = "admin"                 # Or JENKINS_USERNAME env var
  password   = "admin"                 # Or JENKINS_PASSWORD env var
  ca_cert    = ""                      # Or JENKINS_CA_CERT env var

  # Resilience settings (all optional):
  # retry_max       = 4     # Retries for idempotent requests on 429/5xx/connection errors; 0 disables. Or JENKINS_RETRY_MAX
  # retry_wait_min  = "1s"  # Minimum backoff between retries. Or JENKINS_RETRY_WAIT_MIN
  # retry_wait_max  = "30s" # Maximum backoff between retries. Or JENKINS_RETRY_WAIT_MAX
  # request_timeout = "30s" # Per-operation timeout including retries; unset means no timeout. Or JENKINS_REQUEST_TIMEOUT
}
