# Jenkins Terraform Provider

[![CI](https://github.com/namecheap/terraform-provider-jenkins/actions/workflows/test.yml/badge.svg)](https://github.com/namecheap/terraform-provider-jenkins/actions/workflows/test.yml)
[![codecov](https://codecov.io/gh/namecheap/terraform-provider-jenkins/branch/main/graph/badge.svg)](https://app.codecov.io/gh/namecheap/terraform-provider-jenkins)
[![Go Report Card](https://goreportcard.com/badge/github.com/namecheap/terraform-provider-jenkins)](https://goreportcard.com/report/github.com/namecheap/terraform-provider-jenkins)
[![GitHub release](https://img.shields.io/github/v/release/namecheap/terraform-provider-jenkins?label=release)](https://github.com/namecheap/terraform-provider-jenkins/releases/latest)
[![Terraform Registry](https://img.shields.io/badge/Terraform%20Registry-namecheap%2Fjenkins-623CE4?logo=terraform)](https://registry.terraform.io/providers/namecheap/jenkins)
[![License](https://img.shields.io/github/license/namecheap/terraform-provider-jenkins)](LICENSE)

Manage Jenkins jobs, folders, views, and credentials declaratively with Terraform.

> Community provider — not supported by HashiCorp.

---

## Requirements

- **Terraform >= 1.11** or **OpenTofu >= 1.11.** The credential resources expose
  write-only secret arguments (`<secret>_wo`), which rely on Terraform's
  write-only attribute feature introduced in 1.11. Older versions are not
  supported.

---

## Quick Start

```hcl
terraform {
  required_version = ">= 1.11"
  required_providers {
    jenkins = {
      source  = "namecheap/jenkins"
      version = "~> 1.2"
    }
  }
}

provider "jenkins" {
  server_url = "https://jenkins.example.com"
  username   = var.jenkins_username
  password   = var.jenkins_api_token  # API token recommended over password
}

resource "jenkins_folder" "team" {
  name        = "platform-team"
  description = "Platform team pipelines"
}

resource "jenkins_credential_username" "github" {
  name     = "github-bot"
  folder   = jenkins_folder.team.id
  username = "github-bot"
  password = var.github_token
}

resource "jenkins_job" "deploy" {
  name   = "deploy-backend"
  folder = jenkins_folder.team.id
  template = templatefile("${path.module}/pipeline.xml", {
    credentials_id = jenkins_credential_username.github.id
  })
}
```

### Terraform and OpenTofu

The provider works with both **Terraform** (≥ 1.0) and **[OpenTofu](https://opentofu.org/)** (≥ 1.6). The configuration above is identical for either tool.

OpenTofu resolves the same `namecheap/jenkins` source from its own registry, which mirrors GitHub releases automatically:

```hcl
terraform {
  required_providers {
    jenkins = {
      # OpenTofu pulls this from registry.opentofu.org;
      # Terraform pulls the same address from registry.terraform.io.
      source  = "namecheap/jenkins"
      version = "~> 1.0"
    }
  }
}
```

Then `tofu init` (or `terraform init`) downloads and verifies the provider. GPG-signature verification uses the release signing key already registered with the Terraform Registry.

## Resources

| Resource | Description | Required Plugin |
|---|---|---|
| [`jenkins_folder`](https://registry.terraform.io/providers/namecheap/jenkins/latest/docs/resources/folder) | Folder namespace | [Cloudbees Folders](https://plugins.jenkins.io/cloudbees-folder) |
| [`jenkins_job`](https://registry.terraform.io/providers/namecheap/jenkins/latest/docs/resources/job) | Job / pipeline | — |
| [`jenkins_view`](https://registry.terraform.io/providers/namecheap/jenkins/latest/docs/resources/view) | View | — |
| [`jenkins_credential_aws`](https://registry.terraform.io/providers/namecheap/jenkins/latest/docs/resources/credential_aws) | AWS credentials | [AWS Credentials](https://plugins.jenkins.io/aws-credentials) |
| [`jenkins_credential_azure_service_principal`](https://registry.terraform.io/providers/namecheap/jenkins/latest/docs/resources/credential_azure_service_principal) | Azure Service Principal | [Azure Credentials](https://plugins.jenkins.io/azure-credentials) |
| [`jenkins_credential_github_app`](https://registry.terraform.io/providers/namecheap/jenkins/latest/docs/resources/credential_github_app) | GitHub App | [GitHub Branch Source](https://plugins.jenkins.io/github-branch-source) |
| [`jenkins_credential_secret_file`](https://registry.terraform.io/providers/namecheap/jenkins/latest/docs/resources/credential_secret_file) | Secret file | — |
| [`jenkins_credential_secret_text`](https://registry.terraform.io/providers/namecheap/jenkins/latest/docs/resources/credential_secret_text) | Secret text | — |
| [`jenkins_credential_ssh`](https://registry.terraform.io/providers/namecheap/jenkins/latest/docs/resources/credential_ssh) | SSH key | — |
| [`jenkins_credential_username`](https://registry.terraform.io/providers/namecheap/jenkins/latest/docs/resources/credential_username) | Username / password | — |
| [`jenkins_credential_vault_approle`](https://registry.terraform.io/providers/namecheap/jenkins/latest/docs/resources/credential_vault_approle) | HashiCorp Vault AppRole | [HashiCorp Vault](https://plugins.jenkins.io/hashicorp-vault-plugin) |

## Data Sources

| Data Source | Description |
|---|---|
| [`jenkins_folder`](https://registry.terraform.io/providers/namecheap/jenkins/latest/docs/data-sources/folder) | Read an existing folder |
| [`jenkins_job`](https://registry.terraform.io/providers/namecheap/jenkins/latest/docs/data-sources/job) | Read an existing job |
| [`jenkins_view`](https://registry.terraform.io/providers/namecheap/jenkins/latest/docs/data-sources/view) | Read an existing view |
| [`jenkins_plugin`](https://registry.terraform.io/providers/namecheap/jenkins/latest/docs/data-sources/plugin) | Query installed plugin version |
| [`jenkins_credential_aws`](https://registry.terraform.io/providers/namecheap/jenkins/latest/docs/data-sources/credential_aws) | Read an AWS credential |
| [`jenkins_credential_azure_service_principal`](https://registry.terraform.io/providers/namecheap/jenkins/latest/docs/data-sources/credential_azure_service_principal) | Read an Azure SP credential |
| [`jenkins_credential_secret_file`](https://registry.terraform.io/providers/namecheap/jenkins/latest/docs/data-sources/credential_secret_file) | Read a secret-file credential |
| [`jenkins_credential_secret_text`](https://registry.terraform.io/providers/namecheap/jenkins/latest/docs/data-sources/credential_secret_text) | Read a secret-text credential |
| [`jenkins_credential_ssh`](https://registry.terraform.io/providers/namecheap/jenkins/latest/docs/data-sources/credential_ssh) | Read an SSH credential |
| [`jenkins_credential_username`](https://registry.terraform.io/providers/namecheap/jenkins/latest/docs/data-sources/credential_username) | Read a username/password credential |
| [`jenkins_credential_vault_approle`](https://registry.terraform.io/providers/namecheap/jenkins/latest/docs/data-sources/credential_vault_approle) | Read a Vault AppRole credential |

## Provider Configuration

```hcl
provider "jenkins" {
  server_url = "https://jenkins.example.com"  # or $JENKINS_URL
  username   = "admin"                         # or $JENKINS_USERNAME
  password   = "api-token"                     # or $JENKINS_PASSWORD
  ca_cert    = "/path/to/ca.pem"              # optional: custom CA
  insecure   = false                           # optional: skip TLS verify
}
```

| Argument | Environment variable | Required | Description |
|---|---|---|---|
| `server_url` | `JENKINS_URL` | **Yes** | Jenkins base URL. Must be set in the provider block or via `JENKINS_URL`. |
| `username` | `JENKINS_USERNAME` | No | Jenkins username |
| `password` | `JENKINS_PASSWORD` | No | Jenkins API token or password |
| `ca_cert` | `JENKINS_CA_CERT` | No | Path to a custom CA certificate |
| `insecure` | — | No | Skip TLS certificate verification (non-production only) |

`server_url` (or the `JENKINS_URL` environment variable) is required; the provider returns a configuration error if it is not set. All other arguments are optional.

## Developing the Provider

**Requirements:**

- [Go](https://go.dev/dl/) — see the `go` directive in `go.mod` for the authoritative minimum
- [Terraform](https://www.terraform.io/downloads.html) ≥ 1.6 (required by the `terraform test` integration suite)
- [Docker Engine](https://docs.docker.com/engine/install/) ≥ 20.10 (acceptance and integration tests)
- [golangci-lint](https://golangci-lint.run/) (for `make lint`) — pinned in CI (`.github/workflows/test.yml`)

Exact, reproducible tool versions for local development (Go, Terraform, golangci-lint) are pinned in [`mise.toml`](mise.toml); run [`mise install`](https://mise.jdx.dev/) to match them.

**Build & local install:**

```sh
make build
# Prints the ~/.terraformrc dev_overrides snippet — add it to use the local build.
```

**Run tests:**

```sh
make test       # unit tests (no Docker needed)
make testacc    # acceptance tests — starts Jenkins via Docker Compose
make lint       # golangci-lint + govulncheck
make generate   # regenerate docs/  from provider schema
```

**Integration tests** use `terraform test` against a Docker-managed Jenkins:

```sh
ssh-keygen -t ed25519 -N "" -f integration/credentials/id_ed25519
cd integration && terraform init && terraform test
```

> `terraform init` may report an error resolving the provider from the registry (the dev build is used via `dev_overrides`, not the registry). This is expected and safe to ignore — CI runs `terraform init || true` for the same reason; `terraform test` uses the local build.

**Update docs** after any schema change:

```sh
make generate   # re-renders docs/ from provider schema + examples/
git diff docs/  # verify changes look correct before committing
```

## Attribution

Provider design inspired by [dihedron/terraform-provider-jenkins](https://github.com/dihedron/terraform-provider-jenkins).
