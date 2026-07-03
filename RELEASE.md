# Publishing to the Terraform Registry

This document is the one-time runbook for setting up and performing releases to
[registry.terraform.io/namecheap/jenkins](https://registry.terraform.io/providers/namecheap/jenkins).

---

## Prerequisites (one-time setup)

### 1. Reuse the existing GPG signing key from terraform-provider-spaceship

The `namecheap` organisation already has a GPG signing key registered on the
Terraform Registry (key ID `D48A102B93ABB761`, "Namecheap Cloud Platform (for
terraform registry) <squid@namecheap.com>"). **Do not generate a new key** —
the same key and secrets are reused here.

**Key owner:** the SQUID / Namecheap Cloud Platform team (`squid@namecheap.com`).
Contact them for rotation or access requests.

**Canonical storage:** the private key and passphrase live in **HashiCorp
Vault** at the KV v2 path below (the authoritative source of truth — GitHub
repository secrets are only mirrors of these values):

| Vault path                                          | Field         | Maps to GitHub secret |
|-----------------------------------------------------|---------------|-----------------------|
| `kv/squid/terraform-registry/gpg-signing-key`       | `private_key` | `GPG_PRIVATE_KEY`     |
| `kv/squid/terraform-registry/gpg-signing-key`       | `passphrase`  | `PASSPHRASE`          |

Read them with the Vault CLI (requires SQUID-team Vault access):

```bash
vault kv get kv/squid/terraform-registry/gpg-signing-key
```

### 2. Add GitHub repository secrets

Go to **Settings → Secrets and variables → Actions** in this repository and add:

| Secret name       | Value                                                                              |
|-------------------|------------------------------------------------------------------------------------|
| `GPG_PRIVATE_KEY` | Same value as `GPG_PRIVATE_KEY` in `namecheap/terraform-provider-spaceship`        |
| `PASSPHRASE`      | Same value as `PASSPHRASE` in `namecheap/terraform-provider-spaceship`             |

> **Note:** GitHub secrets are write-only and cannot be read back through the
> UI or API. Retrieve the canonical values from HashiCorp Vault
> (`kv/squid/terraform-registry/gpg-signing-key`, see step 1) rather than from
> the Spaceship repository's secrets — Vault is the source of truth.

### 3. Connect to Terraform Registry

The `namecheap` organisation GPG key (`D48A102B93ABB761`) is already registered
at [registry.terraform.io](https://registry.terraform.io) — no need to add
another key. You only need to publish the provider:

1. Open [registry.terraform.io](https://registry.terraform.io) and sign in with
   the **namecheap** GitHub organisation account (requires Owner or Admin
   permission on the org).
2. Click **Publish → Provider** and select `terraform-provider-jenkins`.
3. The Registry installs a webhook automatically. Verify it appears under
   **Settings → Webhooks** in this repository.

The provider will show "No versions published yet" until the first release tag
is pushed.

---

## Cutting a release

Once the one-time setup above is done, every release is a single command:

```bash
git checkout main
git pull origin main

# Replace X.Y.Z with the new semantic version
git tag vX.Y.Z
git push origin vX.Y.Z
```

This triggers `.github/workflows/release.yml`:

1. goreleaser builds binaries for all platforms (Linux / macOS / Windows × amd64 / arm64 / 386 / arm)
2. goreleaser signs `SHA256SUMS` with the repo's GPG key
3. goreleaser creates a GitHub Release with all artifacts + `terraform-registry-manifest.json`
4. The Terraform Registry webhook detects the tag and indexes the new version (~2–5 min)

### Verify the release

After the workflow completes, check:

```hcl
terraform {
  required_providers {
    jenkins = {
      source  = "namecheap/jenkins"
      version = "X.Y.Z"
    }
  }
}
```

```bash
terraform init   # should download from registry.terraform.io
```

---

## Versioning

Follow [Semantic Versioning](https://semver.org/):

| Change type                          | Version bump |
|--------------------------------------|-------------|
| Breaking resource/attribute changes  | Major (X)   |
| New resources or non-breaking fields | Minor (Y)   |
| Bug fixes, docs, internal changes    | Patch (Z)   |