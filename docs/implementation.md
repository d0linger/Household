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

### Approved 180-improvement expansion

The user has approved implementation of all 180 module-comparison proposals. The authoritative per-item checklist, acceptance criteria, dependency order and fresh evidence are maintained in [improvements-180.md](improvements-180.md). Implementation is in progress; historical verification below does not certify this expansion. The first batch covers searchable references, shopping entry/mode and conflict handling. The next batch adds the store/product/availability/assignment/price contract in shopping-catalog.md (core 6, shopping 2, 49 audited tables). On 2026-10-24 the focused PostgreSQL tests passed, including populated upgrade, private image lifecycle, concurrent activity claims, exact estimates, selected reference labels and long escaped cursors. Fresh unit/vet/Node/workflow/22-checksum checks passed (14.4% unit-only statement coverage). The compatible pinned local lint/security tools passed; the first attempt with older executables on PATH failed to parse the new Go export format and is not counted as a successful scan. Full native integration passed at 62.6% combined internal coverage. Focused Linux race tests for Shopping/Reference passed (web package 112.216 s). Browser components passed 103 general, 38 dialog/error, 15 audit, 33 expansion and 27 catalog checks, with twelve clean axe scans across those components. Catalog test-preparation/selector failures and the long-name mobile finding were corrected and the affected component rerun; no single final all-component chain is claimed. Independent F01–F04 and K01 verdicts are resolved at their stated scopes. At that checkpoint eleven expansion items were verified. SH06 and SH07 are now also verified (thirteen total); 167 items remain open. SH06 adds independent template copying and explicit favourite drafts (core7/shopping3). Its boundary/replay/contention tests passed; a corrected populated-upgrade assertion passed in the focused Linux race run (web159.266s). Browser17checks/four clean axe scans passed, including absolute filter navigation from detail pages; independent R01 review is resolved. Unit/vet/Node/workflow/24-checksum verification passed at14.2% unit coverage; pinned lint and Gosec passed. SH07 introduces core 8 and passed final browser verification: 13 checks/two clean axe scans plus independent scoped ship verdict. Its corrected focus return and recaptured desktop/mobile comparison passed. The existing shopping expansion also passed again at 33 checks/two axe scans. Focused Linux merge race passed (web 27.889 s). The complete native integration suite at this state passed at 63.3% combined internal coverage (2982/4708); focused merge tests cover concurrent replay, projections/audit and fourteen rejection cases. Fresh verify passed at 14.7% unit coverage (716/4878), with 28 Node tests and 25 migration checksums; pinned lint and Gosec (49 files/8295 lines) passed without findings. The running8100 Docker candidate has not been upgraded by this work.

### Current AppSec / audit / UX pass

The following pass supersedes earlier validation for files it changes. Fresh native, Linux race, browser-component, scanner, Docker and recovery checks passed. The historical Q-series records the previous candidate (993604f); current measured results are consolidated in [audit-review.md](audit-review.md).

| ID | Work item | Status | Evidence / next acceptance check |
|---|---|---|---|
| A01 | Privacy and security review | Verified | Five stale-capture disclosure cases reproduced and now rejected; focused and complete native regression passed |
| A02 | Generic atomic audit trail | Verified | Core 5, 45 policies, exact field diffs, masked secrets, runtime append-only permissions; complete native suite passed including populated core 4 upgrade |
| A03 | Actor propagation and security events | Verified | User/request/worker/operator context, session/authentication/terminal worker events, concurrent pooled actor isolation passed in native integration |
| A04 | Administrator audit viewer | Verified | Native authorization/pagination/legacy/masking checks and 15 browser checks with two clean axe scans passed |
| A05 | Precision, reference and report correctness | Verified | Exact related amount, selected parent beyond 100, report at-limit/overflow (100/10000) native regressions passed |
| A06 | Dialog / validation / relationship loading UX | Verified | 38 checks and three clean dialog axe scans; 422/409 input retention and focus; failed required/multiple references block unsafe writes, retry preserves existing tags |
| A07 | Fresh complete QA and Docker recovery | Verified locally | Unit/integration/race passed (15.2% unit / 61.3% combined); scanners, 149 checks across three browser components, eight axe scans, updated Docker image, OCI evidence and encrypted recovery passed; full npm chain not rerun after test-only animation fix |
| A08 | Before/after visuals, documentation and local commit | Verified | Actual comparison passed six view/width checks and keyboard interaction; reports complete, temporary test services stopped; staged Gitleaks clean and 11 actual local secret files absent from diff. Implementation committed locally as 361cb5f on dev; no push |

This is the central working checklist requested by the user. It is updated after meaningful changes and checks. **Open** means not completed; **In progress** includes implementation awaiting a fresh check; **Verified** requires an executed check; **Blocked** identifies an actual external prerequisite. Earlier passing results do not certify later edits.

Status clarification: **20 delivery workstreams have the evidence listed below; this does not mean all 220 individual plan steps are complete.** The working local candidate exists, but the additional release gates C22–C28 remain open, externally blocked or deferred. Initial implementation commit: **3972cc9** on **dev**; the QA follow-up is recorded separately in local history. No remote push occurred.

| ID | Work item | Status | Evidence / next acceptance check |
|---|---|---|---|
| C01 | Approved scope and established design | Verified | Conversation; PRODUCT.md; DESIGN.md |
| C02 | Interactive phase overview and 50 design examples | Verified | outputs/design-review/; prototype QA from the design review |
| C03 | Go app, embedded assets, nine module schemas | Verified | Current native and Linux integration runs passed; core migration 5, expenses migration 2, other modules migration 1; 20 migration checksums verified |
| C04 | Authentication, roles, sessions, CSRF, TOTP | Verified | Forced bootstrap workflow, reader restrictions, encrypted TOTP/one-use recovery, hashed sessions and revocation regressions passed; focused security follow-up complete |
| C05 | Financial and stock consistency | Verified | Final PostgreSQL and Linux race runs passed concurrent consumption, allocation-parent immutability, posting/reversal, meter/fuel history and transfer checks |
| C06 | Cross-module actions and offline generation binding | Verified | Final PostgreSQL regression rejects stale destination generation; destination checks precede idempotency replay; browser queue validates destinations |
| C07 | Attachment upload / deletion / capture lifecycle | Verified | Real multipart/thumbnail/private-download and deleted-source regressions passed; independent focused source follow-up confirms both lifecycle locks |
| C08 | Capture replay and recipe/shopping edge cases | Verified | PostgreSQL regressions passed: zero-timer replay, foreign ingredient rejection, archived item rejection; meal recipe values read under lock |
| C09 | Search, recurring tasks and transferred-lot reminders | Verified | PostgreSQL regressions assert search and reminder records for both new child records |
| C10 | Docker Compose isolation and local app | Verified | Final app/database/backup services run; app on 127.0.0.1:8100, database has no published port |
| C11 | Backup and isolated restore | Verified | Earlier live-installation restore succeeded at core 4; current core 5 data-bearing fixture result in C13; daily scheduler running |
| C12 | Backup retention and purge eligibility | Verified | Independent follow-up resolved all findings; scheduler pruned 859a... and marked it expired; stale-backup purge regression passed |
| C13 | Fresh encrypted backup / restore rehearsal | Verified | New core 5 fixture restored exact 2.500000 kg plus original/preview files (2 SHA-256 checks); restore/verification 4.17 s. Live pre/post-upgrade snapshots created; earlier live core 4 restore remains historical evidence. outputs/recovery-review/report.json |
| C14 | Unit, integration and race checks | Verified | Current verify.ps1, native PostgreSQL and Linux race suites passed; combined internal coverage 61.3%, floor 55%; unit-only 15.2%, floor 14% |
| C15 | Lint, security, vulnerability and dependency checks | Verified | Zero tagged lint issues; GOSec 43 files / 7468 lines / zero findings; GOVul zero reachable/imported-package vulnerabilities with one unused module-only advisory; license inventory, npm audit and module verification passed |
| C16 | Docker app and OCR evidence | Verified | Current runtime sha256:18d139d998f98649693fe718dea312696aae1a8032d1c1380ddbb25b20bd6378, 38,107,152 bytes; non-root/read-only/healthy; ready at core 5 with 45 audit policies. Earlier optional Tesseract image read synthetic German text; current worker failure/review paths have regressions |
| C17 | Browser, offline, accessibility and responsive checks | Verified | Current components: 96 general + 38 dialog/error + 15 audit checks; 14 application screenshots; eight clean axe scans. No reported page errors; general/audit suites assert no external frontend requests. Full chain caveat in audit-review.md |
| C18 | Independent visual finish review | Verified | Verdict pass: ship at scope of F01–F03, all resolved. Separate documentation pass complete; limitations and preserved drift in docs/design-review.md |
| C19 | Operator docs, complete master plan and release notes | Verified | README, architecture/schema/security/operations, 220-step master plan, release notes, contributing/security guidance and final evidence ledger present |
| C20 | Local development commit and secret exclusion check | Verified | Implementation commit 3972cc9 on dev, 121 project files; private/generated paths and actual secret values checked and excluded. No push; ledger update committed separately |
| C21 | Public deployment / publication | Blocked | No public origin, hosting target or publication instruction supplied; local Docker delivery continues |
| C22 | Representative performance/resource and recovery-target acceptance | Open | Fresh local 1,000-row probe: read p95 81.1 ms / write p95 117.8 ms; small synthetic restore/verification 4.17 s. Production resource/workload and agreed RTO/RPO acceptance remain open; steps 9, 200, 209 not fully accepted |
| C23 | Release upgrade rehearsal with populated prior-version data | Open | Actual populated core 4 schemas upgraded to core 5 twice with preserved domain/legacy data; current live migration succeeded. Complete prior-release rollback/forward-recovery acceptance remains open; steps 40, 216–218 need a consolidated release rehearsal |
| C24 | Hosted CI and exported OCI/SBOM/provenance verification | Blocked | Current OCI export verified: 11 blobs, 32-package SPDX, SLSA v1. Four filesystem layers match runtime; Compose metadata makes config digests differ. Hosted Actions still needs a remote/push; step 206 partly evidenced |
| C25 | Complete accessibility and keyboard-flow acceptance | Open | Eight clean axe scans, field contrast/targets, shopping keyboard continuity, dialog names, error focus and audit expansion passed; complete keyboard/assistive-technology coverage remains unverified |
| C26 | Off-host backup destination and host-loss recovery | Blocked | Encrypted local repository and isolated restoration passed; separate storage is not configured, so host-loss protection is not delivered |
| C27 | Deferred optional integrations | Deferred | Web Push, local/cloud AI adapters and passkeys are not implemented in this candidate. Tesseract OCR is implemented as an optional Docker image; manual capture is the default |
| C28 | Individual 220-step completion/evidence cross-reference | Open | The plan is exported, but the workstream checklist is not a completed per-step acceptance matrix. A 220/220 completion claim is unsupported |

## QA revision requested 2026-10-24

This pass reviews the existing candidate and strengthens reproducible checks. A passing local run does not imply a hosted Actions run or completion of the 220-step plan.

| ID | Check / improvement | Status | Evidence / next check |
|---|---|---|---|
| Q01 | Baseline and Treckrr/Parkrr test/workflow comparison | Verified | Repositories inspected read-only; baseline unit-only statement coverage 7.5%. Combined database coverage is measured separately |
| Q02 | Configuration, exact values and input validation | Verified | Regression fixes for malformed origins and invalid input types; focused package coverage config 98.9%, module 88.9%, value 94.1%; both exact-value fuzz targets passed |
| Q03 | Integration fixture isolation and authentication boundaries | Verified | Independent guarded fixtures; required database flag; session, CSRF, durable concurrent rate limits and eight SQL privilege-denial checks passed in native and Linux runs |
| Q04 | Worker cancellation, lease exhaustion and recovery validation | Verified | OCR terminal/paused jobs return to manual review, output limits resist io.Copy bypass, strict maintenance connection/restore guards; bounded orphan cursor traverses 10,451 entries; shuffled worker tests passed ten times |
| Q05 | Browser fixtures, keyboard continuity and offline/logout assertions | Verified | Independent API fixtures; keyboard focus bug reproduced and fixed. 96 checks / 11 screenshots / zero browser errors or external requests. Local 1,000-row load probe passed |
| Q06 | CI evidence, workflow validation and OCI attestations | Verified locally | Exact OCI candidate and attached evidence passed; 13 quality-tool tests, actionlint, workflow pins and required DB guards passed; hosted run is C24 |
| Q07 | Fresh Linux race/fuzz/security/browser/recovery checks | Verified | Native and Linux combined coverage 57.2%; scanners and browser checks passed; fresh encrypted restore verified data/files; updated Docker app healthy |
| Q08 | Documentation and local development commit | Verified | README, operating/test guides, release notes and detailed QA report updated; staged Gitleaks and actual-local-secret comparison passed; QA changes recorded in local dev history; no push |

The complete results, reproduced failures, coverage gaps and workload/recovery measurements are in [qa-review.md](qa-review.md).

## Original implementation review findings

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

The first independent visual review returned **fix**. After one batch, its verdict pass returned **ship** and scored all three named findings resolved. Its existing hierarchy finding was accepted as a detector limitation; no redesign was requested. The later QA pass verifies shopping keyboard continuity; full WCAG conformance is not certified.

| Check | Last observed result | Scope / limitation |
|---|---|---|
| Go unit tests | Passed after review fixes | Executed go test ./... on 2026-10-24 |
| PostgreSQL integration contract | Passed after review fixes | Executed scripts/test-integration.ps1 on 2026-10-24; includes destination generation, canonical capture, orphan source, archive, recipe selection, child projection and stale-backup regressions |
| Browser contract | 96 checks passed | outputs/runtime-review/browser-report.json; 11 full-page screenshots, three axe WCAG A/AA scans without violations; desktop/mobile, explicit offline replay, keyboard change/undo, report context, targets and field-border contrast verified |
| Browser assets and requests | No unexpected JS/CSP errors or external requests | Complete final browser run |
| Encrypted recovery rehearsal | Passed at core migration4, including data/files | Live snapshot verified_at confirmed; isolated fixture domain value and both original/preview SHA-256 hashes passed; outputs/recovery-review/report.json |
| GOLint / GOSec | Passed after QA fixes | Pinned scanners: zero lint issues including testtools; outputs/qa/gosec.json has zero findings in 40 source files / 6814 lines; completed 2026-10-24 |
| GOVul / GODep | Passed after review fixes | outputs/vulnerability-report.txt; outputs/dependency-report.md; go mod verify passed. One module-only unused OpenPGP advisory remains documented; no imported-package or reachable vulnerability |
| Linux race detector | Passed after QA fixes | Isolated pinned Go 1.27.3 Alpine helper, real PostgreSQL, shuffled tests and combined coverage; two CPUs / 2 GiB; test-race.ps1 |

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
