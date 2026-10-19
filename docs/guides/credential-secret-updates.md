---
page_title: "Credential updates and secrets"
subcategory: ""
description: |-
  Every jenkins_credential_* update re-sends the secrets Terraform knows, and fails instead of erasing one it does not. Some configurations that updated on v1.3.0 now fail at apply.
---

# Credential updates and secrets

Jenkins' credential API cannot update a credential while keeping a secret it stores: an update replaces the whole credential, and a secret left out of it is stored as empty. Up to v1.3.0 the credential resources sent only the secrets whose value had changed, so any other change (a new description, scope or username) erased the stored secret. That is fixed in the release after v1.3.0, which changes how updates handle secrets.

## What changed

* Every in-place update re-sends each secret Terraform knows: the plain value from the plan, or `<secret>_wo` from configuration. `<secret>_wo_version` still forces an update, but `<secret>_wo` is now sent on every update, not only when the version changes.
* A plain secret under `lifecycle { ignore_changes = [...] }` is re-sent from state.
* Removing an optional secret that Terraform set (`password`, `password_wo`, `secret_id`, `secret_id_wo`, `passphrase`) from the configuration clears it in Jenkins, as before.
* Unchanged: an omitted `jenkins_credential_certificate` `password` still means a keystore without a password, and is sent empty with the keystore.

## Updates that now fail

Each failure stops the apply before anything is sent to Jenkins. `terraform plan` does not predict it, so in a multi-resource apply other resources may already have changed.

### Credential Secret Not Known to Terraform

The configuration sets a secret, but `ignore_changes` keeps its value from state and state holds none, typically after an [import](adopting-existing-jenkins.md) (Jenkins never returns secrets). The update used to erase the secret. Either remove the attribute from `ignore_changes` for one apply, so that the configured value is sent, or manage the secret with `<secret>_wo` and `<secret>_wo_version`.

### Update Would Erase Credential Secret

An optional secret was never set in Terraform (neither configuration nor state has a value), and Jenkins reports a stored value for it:

| Resource | Attribute | When Jenkins reports a value |
|----------|-----------|------------------------------|
| `jenkins_credential_username` | `password` | always: a credential created without a password holds an empty one |
| `jenkins_credential_vault_approle` | `secret_id` | always: a credential created without a secret_id holds an empty one |
| `jenkins_credential_ssh` | `passphrase` | only when a passphrase was set, e.g. within Jenkins or before an import |

So **every** `jenkins_credential_username` that sets neither `password` nor `password_wo`, and every `jenkins_credential_vault_approle` that sets neither `secret_id` nor `secret_id_wo`, now fails on any in-place change. On v1.3.0 the same change erased any password or secret_id set within Jenkins.

Jenkins does not reveal the stored value, so there is no way to update such a credential and keep a secret set outside Terraform. Choose one:

* Manage the secret in Terraform, preferably with `<secret>_wo` and `<secret>_wo_version` from a secret store.
* Set it to `""` if the credential has no secret, or if losing the one set within Jenkins is acceptable.
* Avoid in-place changes to the credential, or replace the resource.
