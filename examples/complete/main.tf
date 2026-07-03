# End-to-end example: a folder containing credentials, a pipeline job, an agent
# node, and a view — managed entirely as code.

terraform {
  required_providers {
    jenkins = {
      source  = "namecheap/jenkins"
      version = "~> 1.0"
    }
  }
}

provider "jenkins" {
  server_url = var.jenkins_url
  username   = var.jenkins_username
  password   = var.jenkins_password

  # Retry transient controller failures on idempotent requests.
  retry_max = 4
}

variable "jenkins_url" {
  type = string
}

variable "jenkins_username" {
  type = string
}

variable "jenkins_password" {
  type      = string
  sensitive = true
}

variable "deploy_token" {
  type      = string
  sensitive = true
}

# A folder to namespace this team's objects.
resource "jenkins_folder" "team" {
  name        = "platform"
  description = "Platform team — managed by Terraform"
}

# A dedicated credential domain within the folder's store.
resource "jenkins_credential_domain" "github" {
  name        = "github"
  folder      = jenkins_folder.team.id
  description = "Credentials for github.com"
}

# A secret stored write-only, so it never lands in Terraform state.
resource "jenkins_credential_secret_text" "deploy_token" {
  name              = "deploy-token"
  folder            = jenkins_folder.team.id
  domain            = jenkins_credential_domain.github.name
  secret_wo         = var.deploy_token
  secret_wo_version = "1"
}

# A pipeline job defined inline, no raw XML required.
resource "jenkins_pipeline_job" "build" {
  name   = "build"
  folder = jenkins_folder.team.id

  script = <<-EOT
    pipeline {
      agent { label 'linux' }
      stages {
        stage('build') {
          steps {
            echo 'Building on a Terraform-managed agent'
          }
        }
      }
    }
  EOT
}

# A static (inbound/JNLP) agent.
resource "jenkins_node" "agent" {
  name          = "linux-agent-01"
  num_executors = 2
  remote_fs     = "/home/jenkins/agent"
  labels        = "linux"
  description   = "Managed by Terraform"
}

# A view surfacing the team's jobs.
resource "jenkins_view" "team" {
  name = "platform"
}
