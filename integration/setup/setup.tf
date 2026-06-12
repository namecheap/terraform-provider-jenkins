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
  # The && sleep 5 gives Jenkins a 5-second stabilisation window after its view
  # API becomes available. Without this buffer, terraform test starts resource
  # creation immediately after healthy, and the first createView POST can race
  # against Jenkins finishing its plugin initialisation.
  healthcheck {
    test         = ["CMD-SHELL", "curl -sf http://localhost:8080/view/all/api/json && sleep 5"]
    interval     = "10s"
    timeout      = "8s"
    start_period = "15s"
    retries      = 15
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
