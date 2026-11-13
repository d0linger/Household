# Workflow and guideline adoption

Reviewed on 2 November 2026 against immutable public source snapshots:

- [Treckrr `86c70c3`](https://github.com/d0linger/Treckrr/tree/86c70c3305ba60d0742a4b60dafc390c4da7a69b): workflow inventory, relevant workflow steps, contributor/agent/security policies, ignore rules and release-impact guidance.
- [Parkrr `d6368e8`](https://github.com/d0linger/Parkrr/tree/d6368e8d0d7bb29505a98fa1235c3809c4179cc3): workflow inventory, relevant workflow steps, contributor/security policies, ignore rules and the documented isolated browser workflow.

The snapshots were downloaded into ignored Household-local storage. No sibling checkout, container, database or global setting was accessed or changed. Remote repository policies are reference material, not authorization to publish or deploy Household.

## Decisions

| Reference pattern | Household decision and implementation |
| --- | --- |
| Both: PR dependency vulnerability review | Add immutable-pinned `actions/dependency-review-action` to existing GODep on pull requests, rejecting newly introduced high/critical vulnerabilities. Keep comments disabled and token read-only. Existing module verification, tidy check, license report and migration checks remain. The public repository supports this without a paid dependency-review license. |
| Both: coverage as a real blocking gate | Add a separate UnitTests job with the already documented 14% unit floor. Existing required Coverage now explicitly requires successful unit and integration jobs before accepting the 55% combined floor. Neither floor is reduced. |
| Both: scheduled vulnerability scans | Add Monday 06:17 UTC and manual Go/npm checks. This small workflow does not start databases, browsers or recovery stacks. GitHub schedules run on the default branch after the workflow is published; scheduling is not an execution guarantee. |
| Treckrr: bounded scheduled financial fuzzing | Exercise Household's actual exact-money and decimal parsers for 45 seconds each with two workers and a two-minute command timeout. Do not copy tractor invoice-allocation logic. Preserve short PR fuzzing. |
| Parkrr: pinned, checksummed image vulnerability scanner | Scan the exact verified runtime manifest from Household's OCI export with Trivy 0.74.0. Verify the official Linux archive SHA-256 before extracting/executing it. Fail on scanner errors and fixable HIGH/CRITICAL vulnerabilities; unfixed findings are explicitly outside this gate. Retain JSON results on a finding. No registry login or image publication. |
| Parkrr: reject local artifacts from the Git index | Add `scripts/check-local-artifacts.cjs --index` to local verification and WorkflowAndSecrets. Reject forced-in private/generated paths, unresolved merges and nonregular entries. Preserve authored design-preview source, icons, configuration examples and migrations. This is a path policy, not a replacement for content/secret review. |
| Both: immutable dependencies and least-privilege CI | Preserve SHA-pinned Actions, read-only tokens, nonpersistent checkout credentials and cancellation. Extend the tested guard to reject suppressed failures, empty jobs, mutable service/job images and conflicting Go setup versions. |
| Treckrr: operational-impact review tied to the actual diff | Extend the PR template with exact base/head commits, household-data/workflow impact, manual steps and rollback/restore compatibility. Reassess after a commit changes. A solo maintainer can complete the review; it grants no deployment permission. |
| Both: focused changes and documentation maintenance | Clarify focused PRs, dependency justification, synthetic bug/evidence data, immutable migrations and README updates in CONTRIBUTING. Existing user-approved work does not require a new issue or repeated approval. |
| Treckrr: repository-specific memory and resource boundaries | Clarify Household-only context/tooling and preservation of concurrent work in AGENTS. Sibling memory paths and historic permissions do not apply here. |
| Treckrr: embedded assets require rebuilding | Document that restart alone cannot incorporate changed Go-embedded assets. Use an isolated rebuilt candidate for validation; an explicit deployment instruction is still required for the running installation. |

## Retained Household protections

Keep the existing nine required GitHub check names and branch rules. The explicitly authorized release setup on 3 November adds PromotionSource and fuel-ocr-recovery for eleven main-promotion gates; see [the release contract](release-process.md). New unit checks feed the existing Coverage gate; PR dependency review feeds GODep; image scanning feeds container. The separate scheduled workflow is advisory to maintenance and is not a required PR context that could remain absent.

Retain16 independently isolated PostgreSQL integration shards with at most six running concurrently, complete test-inventory validation, race detection, migration checksums, full-history Gitleaks, browser/axe/privacy checks, actual recovery tests, OCI blob/SBOM/provenance inspection and the Go standard-library/embedded frontend architecture. Explicit failures remain failures; a local pass does not erase an earlier failed hosted run.

## Deliberately not copied

- Initially excluded automatic publishing was explicitly authorized on 3 November: [the production workflow](release-process.md) now defines checked dev-to-main promotion and exact-candidate GHCR latest/production tags. First publication remains gated on PR #11 and successful hosted checks. Multi-architecture images, deployed-image rescans and automatic deployment remain outside this contract.
- Billing parity runners, Austrian machinery invoicing, garage geometry and domain-specific fixtures: these are not Household contracts. Existing exact-value, finance, migration and module integration checks remain authoritative.
- Treckrr's standing automatic-commit policy, Parkrr's branch names, local ports, memory junctions, package paths, service names or access credentials: retain Household's own rules.
- Broad bans on all PNG/JPG files or all mockups: Household intentionally versions icons and authored design explorations. Generated screenshots and private evidence remain excluded.
- A copied SPA-specific CSP grep: Household already tests server-rendered CSP/nonces and browser behavior. A differently structured frontend needs a separately scoped static guard, not a false claim that the reference shell script covers it.
- A new blocking dead-code gate: reachability findings need a reviewed Household baseline, including CLI entry points and integration/testtools builds. Do not delete exported contracts or add a blanket baseline just to make a newly copied workflow green.
- A filename-only automatic release approval: files cannot prove data migration safety. The adapted operational-impact review requires actual diff review.

## Verification

Validation results and explicit execution limits are recorded in `docs/implementation.md`. Generated local logs remain under `outputs/workflow-adoption-review/`. Changes to CI configuration are not evidence that a hosted, scheduled, registry advisory or full application test campaign has executed.

When updating Trivy, verify the upstream release archive checksum and update version/hash together. When reviewing Dependabot updates, retain immutable Action/image pins and ensure browser tooling matches its installed browser version. Check jobs remain read-only; only the explicitly guarded production publishing job can write packages. Do not relax gates to accommodate an update.
