# Implementation ledger

The 22-phase / 220-step plan was approved on 2026-10-24. This ledger records verified delivery rather than treating the earlier interactive review checkboxes as implementation approval or completion.

## Selected defaults

- One household; German UI; Europe/Vienna; EUR.
- Go net/http, PostgreSQL, embedded assets, isolated Docker Compose stack.
- Five enabled initial modules; chores, meters, documents, meal planning available as optional extensions.
- Local assisted capture; no cloud AI or push subscription until explicitly configured.
- Daily encrypted backups, retained 7 daily / 4 weekly / 12 monthly where backup storage is configured.
- No production publication target or Git remote has been supplied.

## Live checklist — 2026-10-24

This is the central working checklist requested by the user. It is updated after meaningful changes and checks. **Open** means not completed; **In progress** includes implementation awaiting a fresh check; **Verified** requires an executed check; **Blocked** identifies an actual external prerequisite. Earlier passing results do not certify later edits.

Status clarification: **20 delivery workstreams have the evidence listed below; this does not mean all 220 individual plan steps are complete.** The working local candidate exists, but the additional release gates C22–C28 remain open, externally blocked or deferred. Implementation commit: **3972cc9** on **dev**, kept local. No remote push occurred.

| ID | Work item | Status | Evidence / next acceptance check |
|---|---|---|---|
| C01 | Approved scope and established design | Verified | Conversation; PRODUCT.md; DESIGN.md |
| C02 | Interactive phase overview and 50 design examples | Verified | outputs/design-review/; prototype QA from the design review |
| C03 | Go app, embedded assets, nine module schemas | Verified | Final native and Linux integration runs passed; core migration4, expenses migration2, other modules migration1; 19 migration checksums verified |
| C04 | Authentication, roles, sessions, CSRF, TOTP | Verified | Forced bootstrap workflow, reader restrictions, encrypted TOTP/one-use recovery, hashed sessions and revocation regressions passed; focused security follow-up complete |
| C05 | Financial and stock consistency | Verified | Final PostgreSQL and Linux race runs passed concurrent consumption, allocation-parent immutability, posting/reversal, meter/fuel history and transfer checks |
| C06 | Cross-module actions and offline generation binding | Verified | Final PostgreSQL regression rejects stale destination generation; destination checks precede idempotency replay; browser queue validates destinations |
| C07 | Attachment upload / deletion / capture lifecycle | Verified | Real multipart/thumbnail/private-download and deleted-source regressions passed; independent focused source follow-up confirms both lifecycle locks |
| C08 | Capture replay and recipe/shopping edge cases | Verified | PostgreSQL regressions passed: zero-timer replay, foreign ingredient rejection, archived item rejection; meal recipe values read under lock |
| C09 | Search, recurring tasks and transferred-lot reminders | Verified | PostgreSQL regressions assert search and reminder records for both new child records |
| C10 | Docker Compose isolation and local app | Verified | Final app/database/backup services run; app on 127.0.0.1:8100, database has no published port |
| C11 | Backup and isolated restore | Verified | Initial and final-schema live-installation restore succeeded; data-bearing result in C13; daily scheduler running |
| C12 | Backup retention and purge eligibility | Verified | Independent follow-up resolved all findings; scheduler pruned 859a... and marked it expired; stale-backup purge regression passed |
| C13 | Fresh encrypted backup / restore rehearsal | Verified | Live-installation snapshot restored at core migration4; separate data-bearing fixture restored exact 2.500000 kg value plus original/preview files (2 SHA-256 checks). outputs/recovery-review/report.json; scripts/test-recovery.ps1 |
| C14 | Unit, integration and race checks | Verified | Final scripts/verify.ps1, PostgreSQL integration contract, and Linux go test -race -tags=integration ./... passed, including report context/import/MFA additions |
| C15 | Lint, security, vulnerability and dependency checks | Verified | Final 0 lint issues; GOSec 39 files / 6482 lines / 0 findings; GOVul 0 reachable/imported-package vulnerabilities; runtime licenses and go mod verify passed |
| C16 | Final Docker app and optional OCR verification | Verified | Final runtime image sha256:a950f7f83d203624dce0a3977c43876dcb16de13f65dfb1ccbca3bea4324903d, 37,983,451 bytes; UID/GID10001, read-only root, healthy. Optional OCR image correctly read synthetic German receipt |
| C17 | Browser, offline, accessibility and responsive checks | Verified | Final 90-check pass, 11 screenshots, 3 clean axe scans, 0 unexpected JS/CSP/external requests; named drafts, reports, targets and field contrast checked |
| C18 | Independent visual finish review | Verified | Verdict pass: ship at scope of F01–F03, all resolved. Separate documentation pass complete; limitations and preserved drift in docs/design-review.md |
| C19 | Operator docs, complete master plan and release notes | Verified | README, architecture/schema/security/operations, 220-step master plan, release notes, contributing/security guidance and final evidence ledger present |
| C20 | Local development commit and secret exclusion check | Verified | Implementation commit 3972cc9 on dev, 121 project files; private/generated paths and actual secret values checked and excluded. No push; ledger update committed separately |
| C21 | Public deployment / publication | Blocked | No public origin, hosting target or publication instruction supplied; local Docker delivery continues |
| C22 | Representative performance/resource and recovery-target acceptance | Open | Functional/race checks passed; no recorded representative load/p95/resource benchmark or measured recovery result against agreed RTO/RPO targets. Plan steps 9, 200, 209 are not fully accepted |
| C23 | Release upgrade rehearsal with populated prior-version data | Open | Fresh/repeated migrations and existing-installation migration ran; the full populated prior-release upgrade and rollback/forward-recovery acceptance scenario is not documented as completed. Plan steps 40, 216–218 need a consolidated release rehearsal |
| C24 | Hosted CI and exported OCI/SBOM/provenance verification | Blocked | Workflows are configured and local source checks passed; no remote/GitHub Actions run or inspected CI-produced OCI/SBOM artifact exists. Plan step 206 remains only partially evidenced |
| C25 | Complete accessibility and keyboard-flow acceptance | Open | Three axe scans and named contrast/target checks passed; shopping focus continuity after reload and complete keyboard/assistive-technology coverage remain unverified |
| C26 | Off-host backup destination and host-loss recovery | Blocked | Encrypted local repository and isolated restoration passed; separate storage is not configured, so host-loss protection is not delivered |
| C27 | Deferred optional integrations | Deferred | Web Push, local/cloud AI adapters and passkeys are not implemented in this candidate. Tesseract OCR is implemented as an optional Docker image; manual capture is the default |
| C28 | Individual 220-step completion/evidence cross-reference | Open | The plan is exported, but the workstream checklist is not a completed per-step acceptance matrix. A 220/220 completion claim is unsupported |

## Current review findings

- [x] R01 Bind destination module generations to cross-module actions and queued drafts; invalidate related replay records on purge.
- [x] R02 Lock and validate the live source resource and ready attachment before capture confirmation; deleted-source regression passed.
- [x] R03 Serialize attachment uploads with source deletion; independent focused reviewer confirmed lock lifetime through commit.
- [x] R04 Persist capture confirmation values in the same canonical form used for replay comparison; zero-timer replay regression passed.
- [x] R05 Read linked meal-plan recipes under the lock used to protect ingredient copying; source reviewed, integration passed.
- [x] R06 Reject stale or foreign selected recipe ingredient IDs; regression passed.
- [x] R07 Reject checking items in archived shopping lists; regression passed.
- [x] R08 Verify recurring-task and transferred-lot search/reminder projections; both regression assertions passed.
- [x] R09 Group backup retention consistently; prevent old or pruned snapshots qualifying for purge; actual expiry reconciliation observed.

## Check history

### Visual finish findings

- [x] F01 Show German action/field labels, affected record and named relationships when reviewing offline drafts; preserve generation/version/idempotency checks.
- [x] F02 Show vehicle/series labels and meter interval dates using explicit report columns.
- [x] F03 Keep 44×44px mobile controls and module links; separate field-border contrast from quiet layout dividers (at least 3:1).

The first independent visual review returned **fix**. After one batch, its verdict pass returned **ship** and scored all three named findings resolved. Its existing hierarchy finding was accepted as a detector limitation; no redesign was requested. Full WCAG conformance and shopping keyboard continuity after reload are not certified.

| Check | Last observed result | Scope / limitation |
|---|---|---|
| Go unit tests | Passed after review fixes | Executed go test ./... on 2026-10-24 |
| PostgreSQL integration contract | Passed after review fixes | Executed scripts/test-integration.ps1 on 2026-10-24; includes destination generation, canonical capture, orphan source, archive, recipe selection, child projection and stale-backup regressions |
| Browser contract | 90 checks passed | outputs/runtime-review/browser-report.json; 11 full-page screenshots, 3 axe WCAG A/AA scans without violations; desktop/mobile, named explicit offline replay, report context, targets and field-border contrast verified |
| Browser assets and requests | No unexpected JS/CSP errors or external requests | Complete final browser run |
| Encrypted recovery rehearsal | Passed at core migration4, including data/files | Live snapshot verified_at confirmed; isolated fixture domain value and both original/preview SHA-256 hashes passed; outputs/recovery-review/report.json |
| GOLint / GOSec | Passed after review fixes | Pinned scanners: 0 lint issues; outputs/gosec-final.json has 0 findings in 39 source files / 6482 lines; completed 2026-10-24 |
| GOVul / GODep | Passed after review fixes | outputs/vulnerability-report.txt; outputs/dependency-report.md; go mod verify passed. One module-only unused OpenPGP advisory remains documented; no imported-package or reachable vulnerability |
| Linux race detector | Passed | Isolated Go 1.27.3 Alpine helper, real PostgreSQL: go test -race -tags=integration ./... -count=1 -timeout=3m |

### Latest work

- Exported the approved 22-phase / 220-step specification to docs/master-plan.md.
- Added core migration 4 for destination-generation-bound action receipts.
- Backup pruning now suspends purge eligibility until remaining snapshot IDs are reconciled; failed pruning remains fail-closed. Operational expiry reconciliation passed.
- Fresh backup, isolated restore, scheduler startup and snapshot-expiry reconciliation have now passed. Default Docker app is healthy at 127.0.0.1:8100; optional OCR image read a synthetic German receipt correctly.
- Expanded integration checks passed for reader restrictions, atomic import rollback/retry, CSV formula neutralization, encrypted TOTP and once-only recovery codes.
- Security/auth and domain/concurrency reviewers report no remaining material issue within their focused follow-up scope; these are source reviews, separate from executed tests.
- Data-bearing recovery rehearsal passed: synthetic rice 2.500000 kg, one original and one preview. Source/target were disposable databases on restore-db; fixture repository/work volumes and source database were cleaned up. No live household data was seeded.
- Independent visual verdict scored all three requested corrections resolved; documentation verification preserved the incumbent design system and reported existing drift separately.
- Final live snapshot 3b11eb621984fb749937344c3b19661c2fb3bb2893dc9769897f916830015ae6 restored successfully; outputs/docker-delivery.json records the image/runtime/verification identity. Disposable recovery, native review and test-database helpers stopped afterward; live app/database/daily backup remain running.

No public release or GitHub Actions run is claimed. Remaining checks are kept visible until executed.
