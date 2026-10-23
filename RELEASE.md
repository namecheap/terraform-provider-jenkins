# Releasing

Releases are published to
[registry.terraform.io/namecheap/jenkins](https://registry.terraform.io/providers/namecheap/jenkins)
by [release-please](https://github.com/googleapis/release-please). Merging a
PR does not ship anything on its own; a maintainer decides when a release is
cut.

## How a release happens

1. PRs are squash-merged to `main`. PR titles follow Conventional Commits
   (enforced by CI), so every commit on `main` is a conventional message.
2. After each successful CI run on `main`, release-please opens or updates a
   Release PR titled `chore(main): release X.Y.Z`. The version and the
   `CHANGELOG.md` entry are computed from the commits since the last tag.
3. A maintainer merges the Release PR. That creates the `vX.Y.Z` tag and the
   GitHub Release.
4. GoReleaser runs on the tag, builds the binaries for all platforms, signs the
   checksums and attaches everything to the release. The Terraform Registry
   indexes the new version within a few minutes.

## Versioning

[Semantic Versioning](https://semver.org/), derived from the commit types
since the previous tag:

| Commit type | Bump |
|---|---|
| `feat!:`, `fix!:` or a `BREAKING CHANGE:` footer | major |
| `feat:` | minor |
| `fix:` (incl. `fix(deps):` for shipped Go dependencies and `fix(docs):` for registry docs) | patch |
| `docs:`, `test:`, `ci:`, `chore:`, `refactor:`, `perf:`, `build:`, `revert:` | none |

A `refactor:` or `perf:` change does not release even if it alters the binary.
Type anything users should receive as `fix:` or `feat:`.

## If a release doesn't appear

release-please re-scans every commit since the last tag on each run, so a
missed run delays a release rather than losing it.

- The Versioning workflow only runs after a successful CI run on `main`.
  Re-run the failed CI run, or start Versioning by hand from the Actions tab.
- If GoReleaser failed after the tag was created, the GitHub Release has no
  binaries and the Registry ignores it. Fix the cause and re-run the failed
  Release workflow for that tag.

## Manual release

Only when release-please is unavailable. First merge a PR that bumps
`.release-please-manifest.json` and updates `CHANGELOG.md`; if the tag is
pushed before that, the next release-please run recomputes the same version
and fails on the existing tag. Then:

```bash
git checkout main && git pull origin main
git tag vX.Y.Z
git push origin vX.Y.Z
```

The Release workflow runs as usual, but GoReleaser creates the GitHub Release
with an empty body. Paste the `CHANGELOG.md` entry in by hand.

## Verify

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
terraform init   # downloads X.Y.Z from registry.terraform.io
```
