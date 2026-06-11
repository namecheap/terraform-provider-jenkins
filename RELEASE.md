# Publishing to the Terraform Registry

This document is the one-time runbook for setting up and performing releases to
[registry.terraform.io/namecheap/jenkins-v2](https://registry.terraform.io/providers/namecheap/jenkins-v2).

---

## Prerequisites (one-time setup)

### 1. Generate a GPG signing key

The Terraform Registry requires all releases to be GPG-signed.
Run this **once**, on a trusted machine, and store the output securely.

```bash
gpg --batch --gen-key <<GPGEOF
Key-Type: RSA
Key-Length: 4096
Subkey-Type: RSA
Subkey-Length: 4096
Name-Real: Namecheap Terraform Provider
Name-Email: sre@namecheap.com
Expire-Date: 0
Passphrase: <choose-a-strong-passphrase>
GPGEOF

FINGERPRINT=$(gpg --list-keys --with-colons sre@namecheap.com | awk -F: '/^fpr/{print $10; exit}')
echo "Fingerprint: $FINGERPRINT"

# Export public key — paste into Terraform Registry (step 3)
gpg --export --armor "$FINGERPRINT" > terraform-signing-public.gpg

# Export private key — add to GitHub secrets as GPG_PRIVATE_KEY (step 2)
gpg --export-secret-keys --armor "$FINGERPRINT" > terraform-signing-private.gpg
```

Store both files and the passphrase in the team secrets vault before proceeding.
**Do not commit either file.**

### 2. Add GitHub repository secrets

Go to **Settings → Secrets and variables → Actions** in this repository and add:

| Secret name       | Value                                                        |
|-------------------|--------------------------------------------------------------|
| `GPG_PRIVATE_KEY` | Full contents of `terraform-signing-private.gpg`            |
| `PASSPHRASE`      | The passphrase used when generating the key                  |

### 3. Connect to Terraform Registry

1. Open [registry.terraform.io](https://registry.terraform.io) and sign in with the **namecheap** GitHub organisation account (requires Owner or Admin permission on the org).
2. Navigate to the organisation dropdown → **GPG Keys** → **Add a Key**.
3. Paste the full contents of `terraform-signing-public.gpg` and save.
4. Click **Publish → Provider** and select `terraform-provider-jenkins-v2`.
5. The Registry installs a webhook automatically. Verify it appears under **Settings → Webhooks**.

The provider will show "No versions published yet" until the first release tag is pushed.

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
      source  = "namecheap/jenkins-v2"
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
