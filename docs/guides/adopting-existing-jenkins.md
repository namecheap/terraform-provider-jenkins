---
page_title: "Adopting an existing Jenkins into Terraform"
subcategory: ""
description: |-
  Import existing Jenkins jobs, folders, credentials, and nodes into Terraform state, generate their configuration, and adopt a whole controller in bulk.
---

# Adopting an existing Jenkins into Terraform

Every resource in this provider supports [import blocks](https://developer.hashicorp.com/terraform/language/import), so you can bring an existing Jenkins controller under Terraform management without recreating anything. Combined with `terraform plan -generate-config-out` and the provider's list data sources, you can adopt a controller one object at a time or in bulk.

## Import a single object

Add an `import` block naming the target resource and the object's Jenkins ID, then let Terraform generate the configuration:

```terraform
import {
  to = jenkins_job.example
  id = "/job/my-job"
}
```

```console
$ terraform plan -generate-config-out=generated.tf
```

Terraform writes a starter `jenkins_job.example` block to `generated.tf`. Review it, move it into your configuration, and run `terraform apply` to complete the import.

## Import ID formats

| Resource | Import ID | Example |
|---|---|---|
| `jenkins_folder` | canonical folder path | `/job/team` |
| `jenkins_job` | canonical job path | `/job/team/job/build` |
| `jenkins_pipeline_job` | canonical job path | `/job/my-pipeline` |
| `jenkins_view` | view ID | `my-view` |
| `jenkins_node` | node name | `linux-agent-01` |
| `jenkins_credential_*` | `[<folder>/]<domain>/<name>` | `_/github-token` |
| `jenkins_credential_domain` | `[<folder>/]<name>` | `github` |

## Bulk adoption with list data sources

The `jenkins_jobs`, `jenkins_folders`, `jenkins_nodes`, and `jenkins_credentials` data sources enumerate what already exists, and `import` blocks accept `for_each` (Terraform >= 1.7). Together they adopt every object of a kind in one pass.

```terraform
# Discover every top-level job on the controller.
data "jenkins_jobs" "all" {}

# Import each of them, keyed by name.
import {
  for_each = data.jenkins_jobs.all.jobs
  to       = jenkins_job.adopted[each.value]
  id       = "/job/${each.value}"
}

resource "jenkins_job" "adopted" {
  for_each = data.jenkins_jobs.all.jobs
  name     = each.value
  template = "" # populated by `terraform plan -generate-config-out`
}
```

Run `terraform plan -generate-config-out=jobs.tf` to materialise the configuration for every discovered job, then apply. Repeat the pattern with `jenkins_folders`, `jenkins_nodes`, and `jenkins_credentials` for the rest of the controller.

## Secrets are never imported

Import reads only what Jenkins exposes. Credential secret material (passwords, keystores, tokens, private keys) is **not** returned by Jenkins and is therefore never written to state or to generated configuration. After importing a credential, supply its secret yourself — preferably through the [write-only arguments](index.md) (`<secret>_wo` / `<secret>_wo_version`) so it stays out of state entirely. The next `apply` sends the secret to Jenkins.

Even with write-only secrets, review generated configuration before committing it: non-secret attributes are materialised verbatim, and your state backend should be encrypted at rest.
