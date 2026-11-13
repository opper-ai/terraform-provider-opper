# Releasing the provider

The examples below use **v0.2.0**. Preparing or merging release changes does not
publish a version or change project policies. Tagging creates a draft for review;
publishing that draft remains a separate operator action.

## Before a release

1. Merge the release preparation PR after the normal CI and review gates.
2. Obtain approval to tag and publish the reviewed version. For a first release,
   separately approve public visibility after reviewing the repository and history
   for private material. The repository uses Apache 2.0.
3. Verify the approved release signing setup: `GPG_PRIVATE_KEY` and `PASSPHRASE`
   remain GitHub Actions secrets, and the matching public key is registered for
   the `opper-ai` namespace in HCP Terraform. Reuse the existing approved key;
   key rotation is separate from a routine release. If setting up a new publisher,
   prepare an organization-owned, passphrase-protected RSA key and protected backup
   with operator approval. Never commit private key material or its passphrase.
4. Confirm the final acceptance record and that temporary test credentials and
   fixtures have been restored or intentionally retained. Project-access policy
   administration is available to every organization through the dedicated
   management scopes. Publishing this provider does not change any policy or the
   default Organization access of new projects.

The Registry requires a public repository, versioned release archives, a protocol
manifest, checksums, and a GPG signature. See HashiCorp's
[publishing requirements](https://developer.hashicorp.com/terraform/registry/providers/publishing).

## Publisher account and namespace

The Registry sign-in page now directs provider publishers to **HCP Terraform**.
Use an Opper-controlled HCP Terraform organization to manage the `opper-ai`
public namespace. Account creation, accepting terms, granting the GitHub App
access, claiming a namespace and registering a signing key are separate from
merging this repository's release tooling.

1. Sign in at [HCP Terraform](https://app.terraform.io/). If a new account is
   needed, its owner completes credentials, email verification and terms.
2. Select the intended Opper organization, or create one, and verify its identity
   before changing namespace ownership. Public namespaces currently require the
   standard HCP Terraform service; HCP Europe does not support them.
3. Under **Registry > Public namespaces**, select the existing `opper-ai` namespace
   or use **New Namespace**. Installing the Terraform Cloud GitHub App may be
   required. Limit a new installation to `terraform-provider-opper` where possible;
   review and approve its actual permissions before submitting.
4. An organization owner creates or claims `opper-ai` from the linked GitHub
   organization. If it is already claimed elsewhere, resolve ownership rather
   than publishing under another namespace or transferring it without approval.
5. Add the approved release **public** GPG key to that namespace. The private key
   and passphrase go only to the repository's GitHub Actions secrets and the
   protected operator backup. Owners or teams with **Manage public providers**
   permission can manage provider artifacts.

HashiCorp documents [namespace creation](https://developer.hashicorp.com/terraform/cloud-docs/users-teams-organizations/organizations/public-namespace/create_or_claim),
[namespace management](https://developer.hashicorp.com/terraform/cloud-docs/users-teams-organizations/organizations/public-namespace/manage)
and [publishing artifacts](https://developer.hashicorp.com/terraform/cloud-docs/users-teams-organizations/organizations/public-namespace/artifacts).

## Local package verification

Install GoReleaser **v2.20.2**, Go **1.27.4** (pinned in `go.mod`) or later,
Terraform **1.11+**, and Python 3. Run:

```sh
make check
make release-check
```

`release-check` validates the configuration and exercises a real, non-snapshot
GoReleaser release against a loopback GitHub fixture, using a tiny Go program and
dummy credentials. It verifies that the supplied notes reach the draft unchanged,
the release remains a draft, and the expected unsigned assets are uploaded only
to that local fixture. Snapshot mode skips release-note loading, so the snapshot
check alone cannot verify this behavior. Do not disable `changelog`: that also
discards the reviewed file passed with `--release-notes`.

It then builds eight unsigned snapshot ZIPs
in `dist/`, verifies their contents, checksums and protocol 6 manifest, and installs
the host package with `terraform init` through an isolated filesystem mirror.
It loads the actual provider schema and checks all 11 resources and 9 data sources.
It does not use development overrides, connect to Opper, or upload to GitHub or
the Registry. This checks packaging and installation, not Registry signing or
resource lifecycles.

CI runs both checks on pull requests and main, using Terraform 1.15.3 for the
lifecycle suite and 1.11.4 for the packaged installation check. The release workflow runs the same
checks before importing a key or creating a draft. Binaries are built without CGO
for macOS and Windows on AMD64/ARM64, and Linux on AMD64/ARM64/ARMv6/s390x.

## Creating and publishing a version

1. Add reviewed release notes at `docs/releases/vX.Y.Z.md` and update `CHANGELOG.md`.
   Before tagging, finalize the version's changelog heading and remove preparation-only
   wording from the README, provider overview, coverage plan and release notes.
   Use a new version for every change to published assets; never replace a release.
2. After release approval, tag the reviewed commit on main and push that tag:

   ```sh
   git tag v0.2.0 <reviewed-main-commit>
   git push origin v0.2.0
   ```

3. The tag workflow tests the revision, checks it belongs to main, builds the
   packages, signs the checksums and creates a **draft** GitHub release. It uses
   the matching release notes. A failed run is not a published release; inspect
   the run and draft before retrying.
4. Download the draft assets. Verify the checksum signature against the approved
   public key (`gpg --verify *_SHA256SUMS.sig *_SHA256SUMS`), then verify each asset
   checksum. Review the notes and version, then publish the draft explicitly.
5. For the first release, sign in to HCP Terraform, select the verified Opper
   organization and **Registry > Public namespaces > opper-ai**, then choose
   **Publish > Provider**. Select `terraform-provider-opper` and an appropriate
   category. The operator must review and accept the publishing terms at this
   step before submitting. Register the signing key before ingestion. Later
   published GitHub releases notify HCP Terraform and the public Registry through
   the release integration.
6. In a clean directory with no development override or filesystem mirror, use
   the configuration below and run `terraform init` and
   `terraform providers schema -json`. Confirm Terraform reports the expected
   version and signing key. This does not need an Opper credential or API call.

   ```hcl
   terraform {
     required_version = ">= 1.11"
     required_providers {
       opper = {
         source  = "opper-ai/opper"
         version = "0.2.0"
       }
     }
   }
   ```

7. Confirm the Registry documentation renders and the expected platform packages
   are listed. Record the release URL, version, signing fingerprint, installation
   result and any remaining limitations before reporting the release complete.

Resource lifecycle acceptance is recorded in [live-acceptance.md](live-acceptance.md).
Publication does not add support for invitations, managed membership or billing,
and does not change existing project policies or plan/scope requirements. v0.2.0
adds opt-in atomic project creation and runtime-key allowlists; their documented
lifecycle and authorization requirements still apply.
