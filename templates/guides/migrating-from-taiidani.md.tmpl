---
page_title: "Migrating from taiidani/jenkins"
subcategory: ""
description: |-
  Move existing Terraform state and configuration from the taiidani/jenkins provider to namecheap/jenkins.
---

# Migrating from taiidani/jenkins

`namecheap/jenkins` is a fork of [`taiidani/jenkins`](https://registry.terraform.io/providers/taiidani/jenkins) that keeps the original resource and data-source schemas while adding new resources (typed pipeline jobs, nodes, certificate credentials, credential domains), list data sources, provider-level retries and timeouts, and write-only credential secrets. Because the core schemas are unchanged, migrating is a provider swap rather than a rewrite.

## Migration steps

1. Point `required_providers` at the new source:

   ```terraform
   terraform {
     required_providers {
       jenkins = {
         source  = "namecheap/jenkins"
         version = "~> 1.0"
       }
     }
   }
   ```

2. Download the new provider:

   ```console
   $ terraform init -upgrade
   ```

3. Rewrite the provider reference recorded in state so your existing resources bind to the new provider:

   ```console
   $ terraform state replace-provider registry.terraform.io/taiidani/jenkins registry.terraform.io/namecheap/jenkins
   ```

4. Confirm there is nothing to change:

   ```console
   $ terraform plan
   ```

   The core resources (`jenkins_job`, `jenkins_folder`, `jenkins_view`, and every `jenkins_credential_*`) share `taiidani/jenkins`'s schema, so the plan should report no changes. Review any diff before applying — it usually reflects drift that predates the migration rather than the provider swap.

## After migrating

New capabilities you can adopt incrementally, none of which change existing behaviour until you opt in:

- **Keep secrets out of state** with the write-only credential arguments (`<secret>_wo` / `<secret>_wo_version`). See the provider [index](index.md).
- **Provider-level resilience** via `retry_max`, `retry_wait_min`, `retry_wait_max`, and `request_timeout`.
- **New resources**: `jenkins_pipeline_job`, `jenkins_node`, `jenkins_credential_certificate`, `jenkins_credential_domain`.
- **List data sources** (`jenkins_jobs`, `jenkins_folders`, `jenkins_nodes`, `jenkins_credentials`) for discovery and bulk adoption — see [Adopting an existing Jenkins into Terraform](adopting-existing-jenkins.md).

## End-to-end example

A complete, runnable stack — folder, credentials (write-only), a pipeline job, an agent node, and a view — lives in [`examples/complete`](https://github.com/namecheap/terraform-provider-jenkins/tree/main/examples/complete) in the provider repository.
