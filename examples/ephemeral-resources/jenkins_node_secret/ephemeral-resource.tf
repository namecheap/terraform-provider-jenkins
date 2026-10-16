resource "jenkins_node" "agent" {
  name      = "linux-agent-01"
  remote_fs = "/home/jenkins/agent"
}

# Referencing the node's id defers the lookup until the node exists.
ephemeral "jenkins_node_secret" "agent" {
  name = jenkins_node.agent.id
}

resource "aws_secretsmanager_secret" "agent" {
  name = "jenkins/agent/linux-agent-01"
}

# A write-only argument (AWS provider 5.90 or later) hands the secret over
# without storing it in the state. Terraform sends it again only when the
# version changes: bump it when the node is renamed or the controller is
# rebuilt from an empty JENKINS_HOME, otherwise the entry keeps the old secret.
resource "aws_secretsmanager_secret_version" "agent" {
  secret_id                = aws_secretsmanager_secret.agent.id
  secret_string_wo         = ephemeral.jenkins_node_secret.agent.secret
  secret_string_wo_version = 1
}
