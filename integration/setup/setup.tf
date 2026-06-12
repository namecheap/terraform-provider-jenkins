resource "random_pet" "name" {
  prefix = "jenkins"
}

resource "docker_image" "jenkins" {
  name = "jenkins:terraformtest"

  build {
    context = "."
    tag     = ["jenkins:terraformtest"]
  }
}

resource "docker_container" "jenkins" {
  name         = random_pet.name.id
  image        = docker_image.jenkins.image_id
  wait         = true
  wait_timeout = 120

  env = [
    "JAVA_OPTS=-Djenkins.install.runSetupWizard=false",
  ]

  ports {
    internal = 8080
    ip       = "127.0.0.1"
  }

  # wait = true only proceeds once this healthcheck reports healthy.
  # Querying /view/all/api/json (not just /api/json) ensures the view subsystem
  # is fully initialised — the lighter /api/json endpoint can return 200 while
  # Jenkins is still loading plugins, causing createView to return HTML.
  healthcheck {
    test         = ["CMD-SHELL", "curl -sf http://localhost:8080/view/all/api/json"]
    interval     = "4s"
    timeout      = "3s"
    start_period = "15s"
    retries      = 30
  }
}

output "name" {
  description = "The name of the docker container spun up"
  value       = docker_container.jenkins.name
}

output "port" {
  description = "The port that the Jenkins setup has been published on"
  value       = docker_container.jenkins.ports[0].external
}
