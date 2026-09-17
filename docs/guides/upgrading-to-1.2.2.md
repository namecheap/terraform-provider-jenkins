---
page_title: "Upgrading to v1.2.2"
subcategory: ""
description: |-
  jenkins_folder security.permissions changed from a list to a set in v1.2.2. Replace index-based references to it.
---

# Upgrading to v1.2.2

`jenkins_folder`'s `security.permissions` attribute changed from a **list** to a **set** in v1.2.2 ([#188](https://github.com/namecheap/terraform-provider-jenkins/pull/188)). The change fixed spurious `Provider produced inconsistent result after apply` errors — Jenkins does not preserve the order of a permission matrix, so a list could never round-trip reliably — but it is an observable schema change that shipped in a patch release. A `version = "~> 1.2"` constraint picks it up on `terraform init -upgrade`.

## What still works

* **Your configuration.** `permissions = ["hudson.model.Item.Build:alice", ...]` is a tuple literal and converts to either type. No HCL change is needed.
* **Your state.** The folder schema is still version 0, so the framework decodes an existing JSON array into the set type on first read. No state migration, no `terraform state` surgery.

## What breaks

Anything that treats the attribute as ordered or indexable. A set has no indices, so Terraform rejects the reference at plan time:

```terraform
output "first_permission" {
  value = one(jenkins_folder.team.security).permissions[0]
}
```

```
Error: Invalid index

  on main.tf line 2, in output "first_permission":
   value = one(jenkins_folder.team.security).permissions[0]

This value does not have any indices.
```

The same applies to `tolist(jenkins_folder.team.security)[0].permissions[0]`, to `jenkins_folder.team.security[*].permissions[0]` (the trailing index applies per element), and to any order-dependent `for` or `element()` expression over the attribute.

Quieter variants, which fail no plan and raise no error:

* Policy checks — Sentinel, OPA, `terraform show -json` scripts — keyed on the flatmap path `security.0.permissions.0`. Set element keys are hash-based, so those paths stop matching.
* Comparisons that assumed a stable order between runs.

## How to fix it

Iterate the set instead of indexing it:

```terraform
output "permissions" {
  value = [for p in one(jenkins_folder.team.security).permissions : p]
}
```

If a single deterministic element really is needed, sort first so the result does not depend on set ordering:

```terraform
output "first_permission" {
  value = sort(tolist(one(jenkins_folder.team.security).permissions))[0]
}
```

For policy checks, match on set membership (`contains(...)`) rather than on an element path.

## Also worth knowing

Duplicate entries are now collapsed rather than preserved. A configuration that listed the same permission twice applied it twice before and applies it once now; the Jenkins-side result is identical either way.
