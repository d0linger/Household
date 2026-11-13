# Open PR and dependency review — 2026-11-03

All **10 open Household pull requests** were inspected, including their complete file patches, exact base/head commits and the 16 check runs attached to each head. Every reviewed head has failing checks. Their dependency changes have been applied to the current local working tree, with related pins kept consistent. This is **not** approval to merge a failing PR or a claim that GitHub has tested the new local tree.

## Scope and evidence

- Initial GitHub REST snapshot: `2026-11-03T07:11:42Z`; review inventory confirmation: `2026-11-03T07:23:31Z`; coordinating final inventory confirmation: `2026-11-03T07:51:10Z`, with the same ten heads.
- `GET /repos/d0linger/Household/pulls?state=open&per_page=100&page=1` returned 10 entries, below the page limit. There was no further result page. The final check found the same ten numbers and head SHAs.
- Each PR contained at most five changed files; the complete patches were present. All check-run `head_sha` values matched the corresponding PR head.
- Read-only REST/CLI access was used. No PR was merged, closed, commented on or rerun. No commit or push was made by this review.
- The desktop PR diagnostics connection was unavailable; the GitHub API and narrowly scoped read-only access to retained Actions logs/artifacts supplied the evidence instead.
- Raw API snapshots, nine selected failing job logs and three small diagnostic archives remain ignored under `outputs/pr-dependency-review-2026-11-03/`. They are local evidence, not repository inputs.
- Existing local application/security changes are newer than the reviewed remote bases. Local test success must not be substituted for success on those remote commits.

## Every open PR

`F/P` below means failed/passed check runs, out of 16. The legacy combined commit-status API returned `pending` without useful status contexts; the actual check-run conclusions below are the relevant results.

| PR | Exact reviewed head | Change | F/P | Local decision |
|---|---|---|---|---|
| [#1](https://github.com/d0linger/Household/pull/1) | `c121cfb0f23b6507fb889a7ceeffa0b8e6fad1b3` | Backup Go builder digest | 6/10 | Applied together with #8, preserving the Go version |
| [#2](https://github.com/d0linger/Household/pull/2) | `86175f73490a071780672af94ab11ef3f58b3908` | setup-go 6.5.0 → 7.0.0 | 3/13 | Applied to all current workflows |
| [#3](https://github.com/d0linger/Household/pull/3) | `4eac7f65a630eaa3ae3b6815506b02f8caffd03c` | Backup PostgreSQL 18 Alpine digest | 5/11 | Applied consistently to backup, Compose, CI and source test helper |
| [#4](https://github.com/d0linger/Household/pull/4) | `33dee68f18588c902e2ee7f61def299a52e8c7ea` | setup-buildx 3.12.0 → 4.4.1 | 6/10 | Applied with build-push update |
| [#5](https://github.com/d0linger/Household/pull/5) | `5969b4720cc0dff75632ff368035ea588d3fb03a` | setup-node 6.5.0 → 7.1.0 | 3/13 | Applied to all current workflows |
| [#6](https://github.com/d0linger/Household/pull/6) | `30f41781ecdfa62600085ff302a9ff7d7c71599e` | Playwright 1.62.1 → 1.64.1 | 4/12 | Applied; regenerated lockfile removes the obsolete optional fsevents dependency |
| [#7](https://github.com/d0linger/Household/pull/7) | `4c7ea71cf52c8c7c6470e04ca8cfb19dadf1055d` | Alpine 3.24 runtime digest | 5/11 | Applied to runtime Dockerfile |
| [#8](https://github.com/d0linger/Household/pull/8) | `17bf0416b66b024644722aca8b0286ec78aa1460` | Application Go builder digest | 6/10 | Applied together with #1 |
| [#9](https://github.com/d0linger/Household/pull/9) | `236eaefda7f7948335b9e8d426c04b025e69c01c` | build-push 6.19.2 → 7.4.0 | 6/10 | Applied; push remains disabled |
| [#10](https://github.com/d0linger/Household/pull/10) | `a838dd931b029bfb2b9d73951f71d67e03e088c4` | upload-artifact 5.0.0 → 7.0.2 | 5/11 | Applied; current download-artifact 8.0.2 retained |

PRs #2 and #5 used base `430000692a6b97a373ad2bfb6dc791dbad3bb24f`. The other eight used `b07ed5909dab9dc78aa54c4512664615f1d40743`.

| PRs | Failing checks at the reviewed heads |
|---|---|
| #1, #4, #8, #9 | Coverage; Tests (5); WorkflowAndSecrets; fuel-ocr-recovery; both browser jobs |
| #3, #7, #10 | Coverage; Tests (5); WorkflowAndSecrets; both browser jobs |
| #6 | Coverage; Tests (5); WorkflowAndSecrets; one browser job |
| #2, #5 | BrowserAccessibility; ShoppingExtensionsBrowser; fuel-ocr-recovery |

GOLint, GOSec, GOVul, GODep and the container job succeeded on each reviewed remote head. Those results describe the recorded runs, not a fresh vulnerability assessment of today's local source.

## Actionable CI findings

These diagnoses come from the identified logs/artifacts. Similar check names on other PRs were not assumed to prove identical causes.

| Evidence | Observed failure | Required follow-up |
|---|---|---|
| [#10 WorkflowAndSecrets](https://github.com/d0linger/Household/actions/runs/41448626690/job/125287928777) | Actionlint/ShellCheck SC2034: unused `attempt` in readiness loops in browser.yml and shopping-extensions.yml | Current local files already use `_`; preserve the readiness checks and validate the resulting workflows |
| [#10 Tests (5)](https://github.com/d0linger/Household/actions/runs/41448626690/job/125287929115), retained `go-test-shard-5` artifact | `TestDocumentsProcessActualPDFTextPreviewAndOrderedSplitMerge` fails immediately at process_integration_test.go:46 with unavailable/rejected/resource-limited local document tool | Current workflow installs and checks Poppler, OCR languages and fonts; execute the real document integration test. Do not skip the case |
| [#10 Coverage](https://github.com/d0linger/Household/actions/runs/41448626690/job/125392278783) | Missing `outputs/shards/go-test-shard-5/result.json`; coverage evidence cannot be created | Repair the failed prerequisite. Do not lower coverage thresholds or fabricate a successful shard receipt |
| [#10 general browser](https://github.com/d0linger/Household/actions/runs/41448626689/job/125287929222) | `No mobile overflow: /m/mealplan/plans` | Exercise mobile meal planning against the current shared layout |
| [#2 BrowserAccessibility](https://github.com/d0linger/Household/actions/runs/41463250571/job/125337400653) | `Catalog first check remains above fixed mobile navigation` | Exercise shopping catalog visibility with the current bottom navigation |
| [#10 shopping browser](https://github.com/d0linger/Household/actions/runs/41448626778/job/125287929873), retained browser-report.json | A 30-second click timeout waiting for the synthetic complete-list button under `#so-stored` | Check the current browser flow and rendered stored-list control; no application stack trace was supplied by this artifact |
| [#9 Fuel recovery](https://github.com/d0linger/Household/actions/runs/41448619981/job/125287906701), retained native module JSONL | Whole native campaign reaches the 15-minute Go timeout; 18 main tests had passed. The then-active `TestFuelModuleNativeChargeTripArchiveAndOdometerHandover` had run for 27 seconds | Bounded campaign allocation corrected locally as detailed below; actual whole-campaign repetition remains required. The artifact alone does not establish a deadlock in the last test |

The current local source already contains several fixes absent from these remote PR bases. Their existence is not a hosted rerun. Promotion remains dependent on a new, authorized push and successful checks for its exact resulting commit.

## Additional dependency updates

Module metadata was queried with `go list -mod=readonly -m -u -json all`, while changes were restricted to modules explicitly present in Household's go.mod. Unused database/cloud drivers pulled into the migration tool's module graph were not blindly upgraded.

| Dependency | Previous local pin | New pin | Reason / compatibility consideration |
|---|---|---|---|
| github.com/jackc/pgx/v5 | 5.11.0 | 5.12.0 | Security fixes and pool cancellation improvements; changes to custom BatchResults implementations require a Tx method, but Household has no such implementation |
| github.com/fxamacker/cbor/v2 | 2.9.4 | 2.10.0 | Bug fixes and allocation improvements; Go ≥1.24 fits Household 1.27.3; exercise WebAuthn decoding and registration |
| github.com/boombuler/barcode | 2019 pseudo-version | 1.1.0 | Update the OTP QR-code dependency within its existing major version |
| github.com/jackc/pgerrcode | 2022 pseudo-version | v0.0.0-20250907135507-afb5586c32a6 | Current published error-code constants |
| github.com/jackc/puddle/v2 | 2.2.2 | 2.2.3 | Fix background construction completion after Close/Reset and stale resource handling |
| github.com/philhofer/fwd | 1.2.0 | 1.2.1 | Patch update in the existing encoding dependency chain |
| github.com/tinylib/msgp | 1.6.4 | 1.6.5 | Patch update in the existing encoding dependency chain |
| Playwright | 1.62.1 | 1.64.1 | Current browser test runner; matching browser binaries must be installed before browser acceptance |
| actions/checkout | 6.x pinned commit | 7.0.1 | Current immutable release; safeguards unsafe fork checkouts on privileged triggers, which Household does not use |
| Trivy | 0.74.0 | 0.76.1 | Official security maintenance release, pinned archive checksum; JSON reporting does not use the removed getHostByName template function |

pgx 5.12.0 fixes two high-severity upstream advisories published on 2026-10-31: [simple-protocol batch injection](https://github.com/jackc/pgx/security/advisories/GHSA-j38q-h3ph-jjpq) and [authentication/channel-binding enforcement](https://github.com/jackc/pgx/security/advisories/GHSA-95fj-3fxw-g62g). No Household `SendBatch` or explicit simple-protocol call was found during this review; this is a dependency remediation, not evidence that either attack was demonstrated against Household. See the [official 5.12.0 changelog](https://github.com/jackc/pgx/blob/v5.12.0/CHANGELOG.md).

[CBOR 2.10.0 release notes](https://github.com/fxamacker/cbor/releases/tag/v2.10.0) describe stricter handling of forbidden tags and invalid times, plus pointer-receiver and decoder-position changes. Household must retain its actual passkey/CBOR tests rather than infer compatibility from a successful download. The [puddle changelog](https://github.com/jackc/puddle/blob/v2.2.3/CHANGELOG.md) documents the resource lifecycle fix.

The [Trivy 0.76.1 release](https://github.com/aquasecurity/trivy/releases/tag/v0.76.1) updates its Go-built binaries and CA mitigation. Its official Linux x86-64 archive checksum is `c7cbe3137fb8a48a17574bede3dd6beb16742d1e9772929940fc90082c39d9c0`, obtained from the [release checksum file](https://github.com/aquasecurity/trivy/releases/download/v0.76.1/trivy_0.76.1_checksums.txt). The workflow still scans the verified OCI runtime manifest, fails on fixable high/critical findings and retains failed-scan evidence.

### Exact action pins

| Action | Version | Immutable commit |
|---|---|---|
| [checkout](https://github.com/actions/checkout/releases/tag/v7.0.1) | 7.0.1 | `3d3c42e5aac5ba805825da76410c181273ba90b1` |
| [setup-go](https://github.com/actions/setup-go/releases/tag/v7.0.0) | 7.0.0 | `b7ad1dad31e06c5925ef5d2fc7ad053ef454303e` |
| [setup-node](https://github.com/actions/setup-node/releases/tag/v7.1.0) | 7.1.0 | `949feb2413d6458794dcd2491c4babbbce0c15c1` |
| [upload-artifact](https://github.com/actions/upload-artifact/releases/tag/v7.0.2) | 7.0.2 | `cf430e030ddbb5b0abf93d22962f4752f3646cd9` |
| [setup-buildx](https://github.com/docker/setup-buildx-action/releases/tag/v4.4.1) | 4.4.1 | `f87e5991a6d7451dcb8d9637bfbc97413f497069` |
| [build-push](https://github.com/docker/build-push-action/releases/tag/v7.4.0) | 7.4.0 | `c3c9e263c25d99ce0380d002d59b67737d91b0dc` |

Current download-artifact 8.0.2 and dependency-review-action 5.0.0 were verified against their official latest releases and retained. Existing `ubuntu-latest` GitHub-hosted jobs provide the modern Node action runtime; no self-hosted runner compatibility is claimed. Permissions remain read-only, checkout credentials are not persisted, and build publication remains disabled.

The Go toolchain remains **1.27.3**. Published module queries did not offer newer pins for the other explicitly required Go modules. npm reported no further outdated dependencies after installation. Official release metadata confirmed the existing quality-tool versions: golangci-lint 2.15.0, Gosec 2.30.0, govulncheck 1.9.0, actionlint 1.7.12 and Gitleaks 8.30.1. This statement is scoped to the registries, dependencies and date checked here, not every executable on the host.

### Immutable container inputs

The following manifest-list digests were resolved from official registry tags using `docker buildx imagetools inspect`; each resolved list contained the required Linux platform entries. Tags and major versions remain unchanged.

| Image | New manifest-list digest | Updated references |
|---|---|---|
| golang:1.27.3-alpine3.24 | `sha256:d200d2dd76b03a99b52651fd1325aff7dcd35422c35c01bac56571b0507f3deb` | Root and backup Dockerfiles |
| alpine:3.24 | `sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a` | Runtime Dockerfile |
| postgres:18-alpine | `sha256:10a5c69a83035ab5239f5620d708520e8b28f5aa1f2b20143e6024532edadfb5` | Compose, backup/test Compose, backup Dockerfile, CI services, Fuel source helper |

No application data migration is introduced by these dependency edits. Local recreation must still preserve volumes and secrets, use the verified backup required by concurrent application migrations, and keep the PostgreSQL 18 server/client images aligned. Historical evidence and ignored local scripts were not silently rewritten. Downgrading images is not a database rollback plan; restore testing and migration compatibility remain separate release checks.

## Review checklist and executed validation

- [x] Enumerate every open PR and confirm the final inventory/head list is unchanged.
- [x] Inspect every complete file patch and exact head check-run set.
- [x] Inspect nine representative failing job logs and the failed document, Fuel and shopping artifacts.
- [x] Verify official action releases, image digests and dependency metadata.
- [x] Apply all ten proposed dependency changes coherently to the current local tree.
- [x] Apply the additional pgx security and bounded compatible dependency updates.
- [x] Regenerate Go/npm locks; `go mod tidy` and `go mod verify` succeed.
- [x] `npm audit --json`: zero known npm vulnerabilities; `npm outdated --json`: empty.
- [x] Workflow pin/permissions/timeouts/toolchain policy succeeds after the final scanner pin update.
- [x] `node --test scripts/quality-tools.test.cjs`: 22/22 pass after the final update. Initial sandbox execution could not create a Windows junction; the unchanged test passed outside that sandbox. No assertion was disabled.
- [x] Scoped `git diff --check` succeeds.
- [ ] A new GitHub run for the combined local changes: requires a separately authorized push; none was performed.
- [ ] Merge readiness of the ten remote PR heads: blocked by their recorded failures; no green result is claimed.

Application, database, passkey/OTP, browser, security scanner, license and local Docker acceptance for the combined working tree is recorded separately in [implementation.md](implementation.md). This review's successful dependency/workflow checks do not replace those tests or an actual execution of the new Trivy binary against the rebuilt OCI image.

## Fuel campaign deadline follow-up

The local recorder still used the same failing remote limits: 15 minutes for the entire Go package and 16 minutes for its parent recorder, inside a 75-minute workflow job. Its unchanged closed selection discovers 21 current main tests:

```text
go test -tags=integration -p 1 -json ./internal/web -run ^TestFuelModule -count=1 -timeout=20m
```

Only the final timeout argument changed from the earlier command. Required-integration environment guards, serial package execution, all 21 required source-discovered mains, source hashes, JSONL reconciliation, no-skip requirements and every application assertion remain intact.

The remote #9 artifact records 18 completed mains in **872.65 seconds** total (48.48 seconds mean; 146.05 seconds maximum). Its setup before the native step took about 79 seconds; compilation before package execution took about 44 seconds. Extrapolating the observed mean to 21 mains gives approximately **1,018 seconds / 17 minutes**. This estimate explains why a 15-minute aggregate deadline is inadequate; it does not certify a completed campaign or establish a future duration.

The new allocation is **20 minutes for Go**, **22 minutes for the recorder** and **23 minutes for the native workflow step**. The enclosing job stays at **75 minutes**. It therefore retains 52 minutes outside the native step, including the existing 25-minute browser-controller allowance and 27 minutes for installation, image builds and cleanup. This changes no per-request/database limit or browser deadline and does not split or omit test cases.

The 18 recorded passes were:

1. TestFuelModuleCaptureLeaseProposalAndExplicitPhotoConfirmation
2. TestFuelModuleCaptureRechecksAuthorityAndBoundedFailure
3. TestFuelModuleCaptureExpiresTerminalJobsBeforeExtraction
4. TestFuelModuleCoreCostsCorrectExpenseAtomicallyAndKeepDurableDedup
5. TestFuelModuleCoreMaintenanceTaskDedupCyclesDSTAndDocumentedKilometres
6. TestFuelModuleCoreDocumentBindingsRelinkAndRespectCurrentTargetAuthority
7. TestFuelModuleNativeVehicleAndSignedReviewContract
8. TestFuelModuleNativeDisablePurgeEnableRetainsOnlyProtocolFences
9. TestFuelModuleReadLargeHistoryEOFCachesExactTotalsAndKeysetPages
10. TestFuelModuleReadHistoricalBackwardsPrefixRemainsUnknownBeforeAndAfterCache
11. TestFuelModuleReadMutationInvalidatesCursorAndFutureReceiptDoesNotEnterBalance
12. TestFuelModuleReadMaintenanceUsesFourStreamsCurrentSeriesAndFutureCutoff
13. TestFuelModuleReadExportPreservesFullNotesWindowAndCurrentActor
14. TestFuelModuleNativePopulatedFuel2UpgradeRetainsFactsAndOldIntentFences
15. TestFuelModuleNativeDurableReplayChangedBodyAndDeletedFact
16. TestFuelModuleNativeReplayRequiresCurrentWriterAndGeneration
17. TestFuelModuleNativeProtocolRejectsRRUnfinishedAndPrivateProofWrites
18. TestFuelModuleNativeUnknownStatusAndExplicitTerminalAbandon

The interrupted main was `TestFuelModuleNativeChargeTripArchiveAndOdometerHandover`. `TestFuelModuleNativeCSVStageConfirmDedupAndProcessedFence` and `TestFuelModuleNativeCSVHundredRowsRollbackAtFinalDuplicate` had not started. None of these three is recorded as passed by this review.

Executed after the allocation change: workflow policy; the complete Fuel evidence-guard suite plus quality-tool suite (**54/54 passing, zero skipped**); scoped whitespace validation. The new regression guard checks the nested deadline headroom and preserves the existing 75-minute total. A full current native Fuel/OCR/recovery run has **not** been inferred from these pure checks. If the bounded repeat still exceeds its budget, retain the failure and measure fixture cost before changing execution structure.

## Actual rebuilt runtime vulnerability scan

On `2026-11-03T08:05:25Z`, the final actual **Trivy 0.76.1** scan passed with exit code 0 and **zero fixable HIGH/CRITICAL findings** for the final rebuilt Household runtime. It inspected 50 Alpine 3.24.3 OS packages and the Go binary at `usr/local/bin/household`. The gate intentionally excludes lower severities and unfixed vulnerabilities; this is not a claim that every possible vulnerability class is absent.

The exported image's identity was cryptographically resolved through its OCI metadata, rather than equating Docker Desktop's index digest with Trivy's runtime config identifier:

| Identity | Verified value |
|---|---|
| Requested Docker/OCI index | `sha256:5642b43df87cb7e0bb0be23336af452aaa2e567591d9d96f80581e4568862342` |
| Unique Linux amd64 runtime manifest | `sha256:17c9873bd347a31a62a1a6eb60a938eb935b77e221c92669125acf01f27bc5ac` |
| Runtime config, also returned by Trivy | `sha256:e21d5ce3b2ca0ff1a3d24d6d904e2caab015bdab134f1e7d70c2654961718fab` |
| Export archive SHA-256 | `1405e6262eddbb2054aeccb4c440186d1c1bd5f81a6d8b2f089ee4d8949f86b6` |
| Accepted Trivy report SHA-256 | `ac5313283e107d26061b5f4f1f278bc05a69b252323d4091818b69c6af5eedab` |
| Accepted summary SHA-256 | `aa1173d0795c3bd3c7d4117640765d3b7fdca4762bddd848497df7ef35bea124` |

Exact final accepted local evidence: `outputs/personal-navigation-dependencies/container-scan/5/summary.json`, `image-binding.json`, `trivy.json` and `scanner.log`. OCI metadata hashes/sizes, the unique platform/runtime and its non-root `10001:10001` user were checked. The scan container was non-root with a read-only root, no capabilities or Docker socket, 2 CPUs, 768 MiB RAM, 64 PIDs, a five-minute scanner deadline and six-minute outer deadline.

Three preceding operational attempts remain preserved as failures. The first two hit provisional 128/256 MiB temporary-filesystem caps because downloading the 131.49 MiB database archive requires two temporary copies. Disk-backed task scratch corrected this without increasing container RAM. The third was stopped by the provisional 1 GiB cache monitor after the valid database expanded to 1,690,230,784 bytes. With approximately 42 GiB host disk free, the cache budget was set to a bounded 2 GiB; attempt four reused the completed database and passed in about eight seconds. Vulnerability selection, severity, failure exit code and the image were unchanged across attempts.

After attempt four, a redundant cached delivery build regenerated provenance and changed only the image index from `5264b527…` to `5642b43d…`; the deployment guard correctly refused to reuse acceptance for the old index. Attempt five scanned the new exact exported index, verified the same runtime manifest/config and passed in about six seconds. Attempt-four evidence is preserved separately. The redundant rebuild was removed from delivery before proceeding; no earlier scan is silently relabelled as acceptance for a different image index.

All task-owned scanner containers, exported image archives and scratch directories were removed, confirmed by a final resource inventory. The verified scanner executable and dedicated approximately 1.574 GiB vulnerability database cache remain for reuse. The database metadata records `UpdatedAt=2026-11-03T01:30:30Z` and `NextUpdate=2026-11-04T01:30:30Z`. No sibling resource or live application container was changed by the scan.

## Subsequent local acceptance

The final local application passed 377 ordinary Go test mains, 367 Node tests, all 21 selected integration-build mains (17 with real PostgreSQL), and 725 browser assertions with 49 clean Axe scans. The real WebAuthn ceremonies and account/security tests exercise the updated CBOR/pgx dependencies. Go static/security checks, module integrity, dependency-license checks, workflow policy and actual Actionlint 1.7.12 pass. ShellCheck was not available on this Windows host; the actionlint result does not imply its execution.

The recorded remote failures received these actual local follow-ups:

| Remote case | Local result on the updated working tree |
|---|---|
| PDF/OCR/text/preview/split/merge | The unchanged real document integration test passes in the current optional OCR runtime; it is not skipped or simulated |
| Mealplan mobile overflow | Passes in the 114-assertion general browser component |
| Catalog first check above fixed navigation | Passes in the 30-assertion catalog component, including three clean Axe scans |
| Shopping stored-list opening | Passes in the 38-assertion extensions component, including actual IndexedDB ownership/expiry/logout behavior and three clean Axe scans |
| Workflow parsing/policy | Current workflow files pass Actionlint and the repository workflow guard |
| Fuel aggregate deadline | Nested budget regression passes; a complete current Fuel campaign remains unexecuted here |

The local suites also found and fixed a new Core37 audit-array compatibility defect; its original failed result is retained. No hosted coverage/shard receipt or successful check status is fabricated from these local checks. The [navigation checklist](personal-navigation-dependencies.md) and [implementation record](implementation.md#personal-module-navigation-and-dependency-review--3-november-2026) track image scanning, resource cleanup and actual local delivery separately. Current evidence is under `outputs/personal-navigation-dependencies/`, including the final same-head inventory JSON.
