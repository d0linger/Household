# Development and production releases

`dev` is the development integration branch. `main` is the protected production source and the repository default branch. Changes reach production through a **same-repository `dev` → `main` pull request**. Keep both branches; do not force-push them or delete `dev` after a promotion.

```text
feature work → dev → checks → PR dev → main → production checks
                                                ↓
                                verified/scanned OCI candidate
                                                ↓
                       ghcr.io/d0linger/household:production
                       ghcr.io/d0linger/household:latest
```

## Daily procedure

1. Implement and validate changes on `dev` (or merge a feature PR into `dev`). Keep unrelated local edits out of a release change.
2. Let the development checks finish. Open a PR with base `main` and head `dev`; inspect the actual diff and document migration, configuration and rollback effects.
3. Resolve every required check and review discussion. The existing nine required contexts remain: `GOLint`, `GOSec`, `GOVul`, `GODep`, `WorkflowAndSecrets`, `Coverage`, `BrowserAccessibility`, `ShoppingExtensionsBrowser`, `container`. `PromotionSource` and `fuel-ocr-recovery` are also required for production promotion.
4. Merge using a merge commit. Squashing/rebasing the long-lived promotion is disabled so the next promotion retains common ancestry. Bring the resulting `main` merge commit back into `dev` before starting the next promotion; ordinary merge/fast-forward only.
5. The push to `main` runs `Production image`. Only after all five reusable check workflows pass does it publish the scanned image. A failed, skipped or cancelled prerequisite cannot publish.
6. Inspect the workflow's image digest and evidence. Deploying that image to an actual server remains a separate operator action. Pin production deployments to `ghcr.io/d0linger/household@sha256:…` for reproducibility.

## Image and permission contract

- Development pushes and all PRs run read-only checks; they have no registry-write job. There is no automatic development image or `latest` publication from `dev`.
- `release.yml` runs only on `main` pushes. It calls the existing quality, browser, shopping privacy, Fuel OCR/recovery and container workflows from that exact commit. Those workflows remain callable directly for development/PR checks; their `main` push work is performed once by the release orchestrator.
- Only the final publishing job receives `packages: write`, alongside read access to source and PR metadata. It uses the short-lived repository `GITHUB_TOKEN`, not a stored personal token. Repository default workflow permissions remain read-only. No `pull_request_target` or privileged `workflow_run` is introduced.
- The container job builds a Linux/amd64 runtime OCI archive with SBOM/provenance, verifies every blob/attestation relationship, and scans the verified runtime manifest for fixable HIGH/CRITICAL vulnerabilities. Scanner failure fails the job. The policy does not claim coverage of unfixed or lower-severity findings, signed attestations, or an ARM image.
- The publishing job downloads the artifact from its own run, re-verifies it and copies the same OCI graph using Skopeo `--all --preserve-digests`. It does not rebuild or minify different source after scanning. The existing candidate report must match the newly verified graph. The signed runner package repository supplies Skopeo; its version is recorded in the run log.
- Each successful run gets `build-<run-id>-<attempt>`, `sha-<full-main-commit>`, `production` and `latest`. Tags are convenient names, not immutable identifiers; pin by digest. The unique build tag is uploaded and verified before mutable aliases change. Partial registry failure is reported and can leave only some aliases updated; inspect digests and rerun the still-current main run.
- A bounded guard checks the actual push event, current `main` SHA and an actually merged same-repository `dev` → `main` PR whose merge commit exactly matches the candidate. It repeats the current-ref check before publication and each alias. The publishing job is serialized without cancelling in-progress registry writes. An old rerun cannot overwrite a newer completed publication.
- GHCR's initial package visibility may be private even for a public repository. Use authenticated pulls until the operator explicitly chooses package visibility. This workflow does not change visibility or deploy containers.

## Repository administration and bootstrap

Both branches reject deletion and non-fast-forward updates. `main` requires a PR, resolved review threads and strict required checks from GitHub Actions, without bypass actors. The solo-maintainer review model does not require a second account's approval; actual diff review is still required.

The first `main` branch is seeded from the pre-existing remote `dev` commit `430000692a6b97a373ad2bfb6dc791dbad3bb24f`. This establishes a comparison base, **not a claim that this historical commit passed production acceptance**. Its existing Browser/Shopping/Fuel failures remain recorded. That initial source has no publishing workflow. The first release therefore requires the release-setup PR and every required check to pass before merge; do not bypass those failures to obtain a `latest` image.

Dependabot version updates explicitly target `dev`. Existing dependency PRs keep that base. GitHub security updates always target the default branch; if one targets `main`, apply the fix on `dev` and promote through the same procedure rather than bypassing `PromotionSource`. Security alerts remain enabled. [GitHub Dependabot target-branch behavior](https://docs.github.com/en/code-security/reference/supply-chain-security/dependabot-options-reference#target-branch).

The initial release setup was isolated from unpublished application changes. On 13 November 2026, the user explicitly authorized promotion of the completed application work. The reconciled history and reviewed application changes were pushed to `dev` in `49b55f1`; [PR #11](https://github.com/d0linger/Household/pull/11) now includes that application scope and subsequent CI corrections. Its description records Core35–40 migration, backup and rollback requirements. Merge and publication remain gated on the final exact-candidate hosted checks; earlier setup-only evidence is historical.

## Validation and rollback

Run the ordinary Node tests, `node scripts/check-workflows.cjs`, Actionlint, secret scan and staged-artifact check. Release tests cover event identity, stale commits, forks/unmerged PRs, bounded API failure, missing gates, unexpected write permissions and changed OCI candidates. Mocked publication tests must not be described as an actual GHCR round trip.

To repair a release, revert/fix through `dev` and a new checked PR. To roll back a deployment, select a previously verified digest and assess DB/schema compatibility first. A Git revert or image change does not reverse SQL migrations. Keep volume/key backups and the project's existing recovery procedure.

## References adopted

- [Parkrr pinned release workflow](https://github.com/d0linger/Parkrr/blob/d6368e8d0d7bb29505a98fa1235c3809c4179cc3/.github/workflows/docker-publish.yml): separate read-only checks from the registry writer.
- [Treckrr pinned release workflow](https://github.com/d0linger/Treckrr/blob/86c70c3305ba60d0742a4b60dafc390c4da7a69b/.github/workflows/docker-publish.yml): same-revision gates, scanned candidate, promotion without rebuilding, serialized current-ref checks.
- [GitHub reusable workflows](https://docs.github.com/en/actions/how-tos/reuse-automations/reuse-workflows), [GHCR publishing](https://docs.github.com/en/actions/tutorials/publish-packages/publish-docker-images), [Skopeo digest preservation](https://github.com/containers/skopeo/blob/main/docs/skopeo-copy.1.md).

No sibling checkout, service, database or credential was used.
