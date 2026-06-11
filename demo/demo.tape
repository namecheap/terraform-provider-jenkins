# Prerequisites:
#   brew install vhs
#   brew install charmbracelet/tap/gum
#   A local Jenkins running on http://localhost:8080 (admin/admin)
#   terraform-provider-jenkins built locally (make build)
#
# Generate the GIF:
#   vhs demo/demo.tape
#
# Output: demo/demo.gif

Output demo/demo.gif

Set Shell "bash"
Set FontSize 14
Set Width 1200
Set Height 650
Set Theme "Catppuccin Mocha"
Set Padding 20
Set TypingSpeed 40ms
Set PlaybackSpeed 1.0

# ── Setup ───────────────────────────────────────────────────────────────────
Hide
Type "cd /tmp && rm -rf jenkins-demo && mkdir jenkins-demo && cd jenkins-demo"
Enter
Sleep 300ms

Type `cat > main.tf << 'EOF'
terraform {
  required_providers {
    jenkins = {
      source  = "namecheap/jenkins"
      version = "~> 1.0"
    }
  }
}

provider "jenkins" {
  server_url = "http://localhost:8080"
  username   = "admin"
  password   = "admin"
}

resource "jenkins_folder" "team" {
  name        = "platform-team"
  description = "Platform team pipelines"
}

resource "jenkins_credential_secret_text" "token" {
  name        = "deploy-token"
  folder      = jenkins_folder.team.id
  description = "Deployment API token"
  secret      = "super-secret-token"
}

resource "jenkins_job" "deploy" {
  name   = "deploy-backend"
  folder = jenkins_folder.team.id
  template = file("pipeline.xml")
}
EOF`
Enter
Sleep 200ms

Type `cat > pipeline.xml << 'EOF'
<flow-definition>
  <description>Backend deployment pipeline</description>
  <definition class="org.jenkinsci.plugins.workflow.cps.CpsFlowDefinition">
    <script>pipeline { agent any; stages { stage("Deploy") { steps { echo "Deploying..." } } } }</script>
    <sandbox>true</sandbox>
  </definition>
</flow-definition>
EOF`
Enter
Sleep 200ms
Show

# ── terraform init ───────────────────────────────────────────────────────────
Type "terraform init"
Enter
Sleep 4s

# ── terraform plan ───────────────────────────────────────────────────────────
Type "terraform plan"
Enter
Sleep 5s

# ── terraform apply ──────────────────────────────────────────────────────────
Type "terraform apply -auto-approve"
Enter
Sleep 8s

# ── state show ───────────────────────────────────────────────────────────────
Type "terraform state list"
Enter
Sleep 1s

Type "terraform state show jenkins_folder.team"
Enter
Sleep 2s

# ── terraform destroy ────────────────────────────────────────────────────────
Type "terraform destroy -auto-approve"
Enter
Sleep 6s

Type "echo Done."
Enter
Sleep 2s
