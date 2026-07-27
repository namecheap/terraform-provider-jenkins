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

### 4. Release automation credentials (GitHub App)

`.github/workflows/versioning.yml` authenticates release-please with a
dedicated GitHub App instead of the default `GITHUB_TOKEN`. This is required:
events authored by `GITHUB_TOKEN` do not trigger other workflows, so a
token-authored Release PR would never run CI and its tag would never trigger
`release.yml`.

One-time setup:

1. Install the release GitHub App on this repository (org admins manage it).
2. Add the variable `APP_CLIENT_ID` (the App's client ID) and the secret
   `APP_PRIVATE_KEY` (the App's private key) — either at repository level
   under **Settings → Secrets and variables → Actions**, or org-level with
   visibility extended to this repository.

---

## Cutting a release

Releases are **semi-automated and maintainer-gated** via
[release-please](https://github.com/googleapis/release-please). Changes merged
to `main` do not ship immediately — they accumulate in a long-lived "Release
PR", and a binary is published only when a maintainer merges that PR.

### How it works

1. **PR merged to `main`** — PR titles follow Conventional Commits (enforced
   by `pr-title.yml`) and PRs are squash-merged, so each commit on `main` is a
   conventional message.
2. **release-please opens/updates the Release PR** —
   `.github/workflows/versioning.yml` runs after every successful CI run on
   `main`. It computes the next SemVer bump from commits since the last tag
   (`fix:` → patch, `feat:` → minor, `feat!:`/`BREAKING CHANGE` → major),
   updates `CHANGELOG.md` and `.release-please-manifest.json`, and opens (or
   refreshes) a PR titled `chore(main): release X.Y.Z`. Non-releasing types
   (`chore:`, `ci:`, `docs:`, `test:`, ...) do not open a Release PR.
3. **Maintainer merges the Release PR** — review the computed version and
   CHANGELOG, then merge when the accumulated changes are worth shipping.
   Merging commits the version bump, creates the `vX.Y.Z` tag, and publishes
   the GitHub Release notes.
4. **GoReleaser publishes binaries** — `.github/workflows/release.yml` runs on
   the pushed tag: builds all platforms, signs `SHA256SUMS` with the GPG key,
   and attaches artifacts + `terraform-registry-manifest.json` to the release.
   The Terraform Registry webhook then indexes the new version (~2–5 min).

### Dependency bumps and releases

Dependabot commit prefixes are deliberately split (see
`.github/dependabot.yml`):

| Ecosystem | Prefix | Releases? | Why |
|---|---|---|---|
| `gomod` (root) | `fix(deps):` | patch | Changes the shipped provider binary |
| `github-actions` | `ci(deps):` | no | CI-only, never touches the binary |
| `gomod` (`/tools`), `terraform`/`docker` (`/integration`) | `chore(deps):` | no | Dev tooling and test fixtures only |

### Manual / emergency release

Prefer the normal flow. If release-please is unavailable or an out-of-band
hotfix is needed:

```bash
git checkout main
git pull origin main

# 1. Bump the version in .release-please-manifest.json and update
#    CHANGELOG.md, commit via a PR.
# 2. Tag and push (replace X.Y.Z):
git tag vX.Y.Z
git push origin vX.Y.Z
```

`release.yml` runs on the pushed tag as usual, and the next release-please run
reconciles its state with the updated manifest.

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

[Semantic Versioning](https://semver.org/), derived automatically by
release-please from Conventional Commit types since the previous tag. The
current version lives in `.release-please-manifest.json` (the source of truth
for release-please state).

| Change type                                        | Commit type              | Version bump |
|----------------------------------------------------|--------------------------|-------------|
| Breaking resource/attribute changes                | `feat!:` / `fix!:`       | Major (X)   |
| New resources or non-breaking fields               | `feat:`                  | Minor (Y)   |
| Bug fixes, shipped dependency bumps                | `fix:`                   | Patch (Z)   |
| Docs, tests, CI, tooling                           | `docs:`/`test:`/`ci:`/`chore:` | none  |