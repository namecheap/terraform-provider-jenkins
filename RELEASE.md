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

### 2. Signing secrets

`GPG_PRIVATE_KEY` and `PASSPHRASE` are organisation secrets shared with this
repository. GitHub secrets are write-only, so the organisation secret is the
only copy; contact the key owner above for rotation.

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

1. Install the release GitHub App on this repository (org admins manage it),
   granting it **Repository contents: Read & write** and **Pull requests:
   Read & write** — these App-installation scopes (not the workflow's
   `permissions:` block, which only governs the unused `GITHUB_TOKEN`) gate
   release-please's branch push, Release PR, tag, and publish operations.
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

### If a release doesn't appear (troubleshooting)

`versioning.yml` only runs after a successful CI run on `main`, and
release-please state is **cumulative** — every run re-scans all commits since
the last tag, so a missed trigger delays a release rather than losing it.
Known cases:

- **CI failed or was flaky on a merge commit** (including the Release PR's
  own merge commit): re-run the failed CI run — `workflow_run` fires again
  when a re-run completes — or trigger versioning manually (Actions →
  Versioning → Run workflow). A merged Release PR whose tag was never created
  is reconciled the same way on the next successful run.
- **The commit touched only root-level markdown / LICENSE / the manifest**:
  CI still runs — the `changes` gate in `test.yml` skips the test jobs, but
  the always-run `CI OK` job completes the run successfully, so versioning
  fires as usual (see [CI.md](CI.md)). If versioning didn't run, look for an
  actual CI failure rather than a missing trigger.
- **GoReleaser failed after the tag was created** (bad GPG key, upload
  error): the GitHub Release exists without binaries. The Terraform Registry
  will not ingest an artifact-less version, so nothing broken is served —
  fix the cause and re-run the failed `release.yml` run for the tag.

### Manual / emergency release

Prefer the normal flow. If release-please is unavailable or an out-of-band
hotfix is needed:

```bash
# 1. Bump the version in .release-please-manifest.json and update
#    CHANGELOG.md via a PR, and MERGE IT BEFORE TAGGING — if the tag is
#    pushed first, the next release-please run recomputes the same version
#    from the stale manifest and fails on the already-existing tag.
# 2. Re-sync so the tag includes the bump commit:
git checkout main
git pull origin main
# 3. Tag and push (replace X.Y.Z):
git tag vX.Y.Z
git push origin vX.Y.Z
```

Only a repository admin can push the tag (see [Release tags](#release-tags)).
`release.yml` runs on the pushed tag as usual, and the next release-please run
reconciles its state with the updated manifest.

One caveat: in the manual path GoReleaser is the release *creator*, and its
changelog is disabled (release-please owns release notes in the normal flow),
so the GitHub Release is published with an **empty body**. Edit the Release
afterwards and paste the CHANGELOG entry in by hand.

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

## Release tags

A `release-tags` tag ruleset restricts creating, updating and deleting `v*`
tags. Only the release App (`namecheap-i-app`) and repository admins can bypass
it.

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
| Registry-published docs corrections (`docs/`, `templates/`) | `fix(docs):`    | Patch (Z)   |
| Repo-internal docs, tests, CI, tooling             | `docs:`/`test:`/`ci:`/`chore:` | none  |
| Refactor / perf / revert / build (⚠ may change the binary but do **not** release — type binary-affecting changes as `fix:`/`feat:` instead) | `refactor:`/`perf:`/`revert:`/`build:` | none |