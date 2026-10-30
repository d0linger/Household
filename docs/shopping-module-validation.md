# Shopping SH15 / SH17 validation

Status: SH15/SH17 passed scoped local module acceptance on the corrected 527-source layout manifest `42c434abc668879ace5c018a505be7e436d47944dc640308f5f58a742266d610`. The [180-item acceptance list](improvements-180.md) is 87 verified / 0 in progress / 93 open, Shopping 20/20. The sections below preserve the original Phase 2 source gate, failed runs, corrections, executed tests and their limits in chronological order.

## Scope and baseline

- SH15: personal list watches, durable grouped in-app notices and optional explicitly configured Web Push.
- SH17: complete explicitly selected device read copies, a neutral public offline shell and persistent privacy fences.
- Previous accepted checkpoint: Pantry `c94072aa49533319947f32d91c1894f222675f9f`. Its tests do not prove this new source.
- No new SQL migration or Ready contract is planned. All 75 accepted migration files must be checked again during Phase 3.
- Live HTTP port 8100, sibling projects, publication and Git remotes remain outside these changes. Local runtime checks use the isolated Household test environment.

## Historical Phase 2 source integration gate

| Gate | Evidence at the original source gate |
|---|---|
| Complete module scope before implementation | Published in the module plan and shared API contract. |
| Backend store lane | Independently reviewed, 16 files transferred from manifest `e7f1e3fa…`. |
| Worker / encrypted push lane | Independently reviewed, 13 files transferred from manifest `90c42b65…`. |
| Frontend / device privacy lane | Independently reviewed, 11 files transferred from manifest `1ccd9e09…`. |
| Root integration | Configuration, keygen, API routes, common privacy hooks, neutral shell/SW and CI source written. |
| Whole-module source freeze | 527 sources, SHA256 `411fefccf41f3aa31134836f2e6b3a5e3b17e5293865da11b4ff3d879dbe9324`; all 40 transferred targets hash/byte identical. |

Formatting and source inspection are not compilation, unit tests, native database checks, browser execution or security acceptance.

## Phase 3 campaigns

Initial campaigns run against the unchanged 527-source freeze `411fefcc…`:

| Executed campaign | Actual result |
|---|---|
| Full Go unit suite | 245 main tests passed, 1 failed, 0 skipped; 44.675 seconds. The failed settings test reuses a decoded struct between JSON cases. A fresh per-case value is the proposed correction; no source correction has been applied yet. Evidence: `outputs/shopping-module-validation/unit-af9f887135db48389e244bcc992f3184/`. |
| Full Node suite | 243/243 passed, 0 failed/skipped on the native filesystem repeat. The initial sandbox run had 12 filesystem/junction permission failures; its original log is retained. |
| JavaScript syntax | 106/106 passed. |
| Workflow / migration pins / checklist | Passed, including all 75 SQL pins and the unchanged 85/2/93 acceptance counts. |

Node/static evidence: `outputs/shopping-module-validation/phase3-node-d6873ea581ea4e67a0c21e7c86115012/final-summary.json`, SHA256 `205207feeca2007006c0bbaca73929841192d9921d5b546a9a82bc24b67b979d`. All 617 monitored hashes and source membership remained unchanged during that campaign.

The initial native campaign then completed 20/22 passes, 2 failures and no skips in 201.921 seconds on the unchanged `411fefcc…` source. Both failures occurred in test setup: a UUID parameter was also inferred as text, and the snapshot fixture attempted to omit a currently required unit. The original failed JSONL and summary remain under `outputs/expansion-review/shopping-module-native-sh15-sh17-initial-20261029-1930`.

Three independently reviewed fixture corrections preserve the existing assertions. The nullable-unit case now first creates a valid application record, then explicitly models a historical nullable database row through an audited owner update; it additionally verifies that no unit is fabricated. The key generator now creates both files with mode 0600. G704 has one local documented exception at the guarded HTTP sink, backed by a new direct gate test. No global scanner rule or network gate was relaxed.

On corrected 527-source freeze `cb5b9ec2f792b81e44115049373490228d8c12cfaad21db70f6b7ef11a7e747a`:

| Repeated campaign | Actual result |
|---|---|
| Whole Go unit suite | 247/247 mains, 1,464 main/subcase passes, zero skips; 17.216 seconds. Evidence: `outputs/shopping-module-validation/unit-97aaf117be224e9f8739bc8861403ea9/`. |
| All 22 new native contracts | 22/22 mains, 34 main/subcase passes, zero skips; 198.906 seconds. Exact source-derived required-main and terminal-event proof: `outputs/expansion-review/shopping-module-native-sh15-sh17-corrected-20261029-1945-exact-event-proof.json`. |
| Gosec | 200 production files, 39,437 lines, 17 local exclusions, zero findings/parser errors. |
| Gofmt / tagged Vet | Passed. |
| Tagged linter | Five findings: two boolean simplifications, test response cleanup and deprecated test crypto calls. Four narrowly scoped source corrections are now under focused verification; final acceptance remains pending. |

The corrected security report is `outputs/shopping-module-validation/security-corrected-5c51ac91bab54ff2b4ffc52db43639e3/summary.json`. All source hashes remained unchanged during those completed campaigns. The four subsequent lint corrections require a new source pin and suitable repeats; the earlier passes are not relabeled as final-source passes.

Module verification and the actual 13-module production license inventory passed on `411fefcc…`. Linux-target govulncheck reported zero reachable vulnerabilities, with module-only advisory `GO-2026-5932` concerning an unimported OpenPGP package. This was source analysis hosted on Windows, not Linux binary execution. No module versions or imports changed in the subsequent narrow corrections.

### Current source and regression reconciliation — 30 October 2026

The final narrow lint corrections passed the full tagged linter (zero issues), Gosec (200 files, 39,437 lines, 17 documented local exclusions, zero findings) and Linux-target source govulncheck (zero reachable or imported-package findings). Evidence: `outputs/shopping-module-validation/security-final-41bed30cde65412996fb1131283399a9/summary.json`. The unchanged module-only OpenPGP advisory remains explicitly recorded.

The 122-main affected native campaign actually finished **121 passed, one failed, zero skipped**. Its sole failure was an existing service-worker test whose old public-shell allowlist omitted the new neutral Shopping shell. The test was strengthened to require that shell and to verify anonymous and authenticated responses contain no private owner, household, CSRF or fixture data and set no cookie. No production code or SQL changed. Original failed evidence remains under `outputs/expansion-review/shopping-module-native-affected-122-final-20261029-2010`.

Current 527-source manifest: `outputs/shopping-module-validation/post-cache-freeze-76f697aa1b6c448f84a1803117e1d461.json`, SHA256 `58c7681bdbffcbaa0aefb86a00a555e5b6fb22b7ec67d2ce7a49ce1c88c6fe35`. Its sole delta from the lint-accepted source is that tagged test file. On this current source:

| Executed campaign | Actual result |
|---|---|
| Whole Go unit suite | 247 main tests / 1,464 main and subcase passes, zero failed or skipped; 18.722 seconds. `unit-e2738eac3d4340279e96ab3efb337f73/summary.json` under the module-validation output tree. |
| Node and static checks | 243/243 Node tests, 106 JavaScript syntax checks, workflow policy, all 75 migration pins and checklist synchronization passed. `phase3-node-post-cache-8a3a718eb3474fc495b211e2f672fe52/final-summary.json`. |
| Fresh targeted PostgreSQL repeat | All 22 new Shopping contracts plus both tests in the changed cache-test file passed: 24 mains, zero failed/skipped; 221.063 seconds. `outputs/expansion-review/shopping-module-native-final-24-cache-20261029-2050-summary.json`. |
| Composed affected regression | All 122 required mains are covered by 24 fresh passes plus 98 earlier passes with byte-identical corresponding sources. `composed-122-current-58c-proof.json`, SHA256 `94f3daa2c14eb85a6224c3bd419945819b0838e09c275ace82d4a0238eebe89b`. This is composed evidence, not a single green 122-test run. |

Each campaign checks source hashes and membership before and after execution. The composed proof checks exact run/terminal events and rejects skipped, unfinished or changed required cases. It includes migration repeat/audit repair, populated predecessors, existing Shopping/Planning/Pantry writers and the maintenance restore contract; a populated operational recovery rehearsal remains separate and open.

Current non-root runtime and backup images were built and inspected with the exact source label. Their IDs are `sha256:6b06e0412a7aa3df4a3e3f16d34a2243610dd97786972bde43206eb90b4d4c53` and `sha256:92b352eacb73188220f051eb5db35b1f680aee805dc6d6073f085981a5dd378a`. Build evidence: `outputs/shopping-module-validation/images-final-8cad938e4eae4db8a30a25e5b68494b4/summary.json`. A separate real non-root, network-free keygen probe verified mode 0600 for both files, overwrite refusal and cleanup; its earlier image has identical production sources, and is identified separately in `keygen-2bcd3a0cc4154912965ae14416d7e594/summary.json`.

The full tagged linter was also repeated on current source58c: exit0, zero issues, 101.157 seconds, source membership/hashes unchanged. Evidence: `outputs/shopping-module-validation/tagged-lint-current-07950780b01a4e2590db68ace7e4b128/summary.json`. The current OCR target required by the existing full browser chain is built as `sha256:7095832d8acc27d3068caff4fd0674298cf7aa226817e1d4be4bbc340fd25a6d`, UID/GID10001, same source label and unchanged Dockerfile/source. Its 23.553-second build under `ocr-image-e779ac7644594da99d918ac8c016457b/summary.json` is not browser or OCR execution evidence.

The first actual standalone browser run `b235119230d04a689c737fb92ff5d5e7` failed after three successful checks at the durable digest assertion. Its log shows the asynchronous wait returned immediately, before a worker cycle; this is not proof that a digest failed to arrive after its allowed interval. All original reports and logs remain under `outputs/shopping-module-browser-review/run-b235119230d04a689c737fb92ff5d5e7/`. The owned app and uploads volume were removed. Because normal browser exit was not established, ACL-private state was retained and browser-descendant cleanup was not claimed. A narrow driver-only correction and actual repeat are required. SH15/SH17 remain in progress, with 85/2/93 overall counts.

The reviewed driver-only correction replaces all three asynchronous Playwright wait predicates with bounded Node-side polling that awaits the result. The digest still has its original90-second/2-second budget and now requires a real row linking to the actual watched list. Expiry and opt-out observe raw IndexedDB without calling a helper that could heal the state. Two new meaningful regression tests cover async false/retry, eventual true, unresolved predicates and timer cleanup. The full Node suite then passed245/245, no failures, cancellations or skips; two syntax checks passed. Evidence: `outputs/shopping-module-validation/browser-driver-poll-fix-cddf4386e7d94a2b8f92c9e864bdfe82/`. Application527-source identity was checked afterward, not separately monitored during that short Node campaign.

Actual second browser run `354d117a67724ffb8d0877765af97883` confirms the durable watched-list digest, absence of article content and server-time mute deadline. It then fails after six passed checks at unsubscribe: the driver observes the previous success message before the new PUT has occurred. Original report/logs and retained private failure state remain. A further driver-only correction now awaits each exact preference PUT200 and the completed editable form before inspecting the saved state. Syntax and all32Shopping Node tests pass (`browser-watch-response-fix-f864c3334cd844f9b660e7f82a4e05a1/`); application/SQL/image source identity is unchanged. A third actual standalone run is in progress, with no pass inferred.

### Standalone browser accepted

Third run `e605805ad1f04bfabc2864f2d41d0067` actually passed **37 checks, three screenshots and three Axe analyses with zero violations**, zero page errors and zero external requests. Exact orchestration report: `outputs/shopping-module-browser-review/run-e605805ad1f04bfabc2864f2d41d0067/orchestration-report.json`, SHA256 `b7a38c92cafa7e09c08c469220450e4dc4412da959c15a6e679585e5baa2a738`. Its archived actual driver report has SHA256 `c7e237740b05fdc2b8112fee358e1611faf3bcaedf5dd42c20324e1b728d107b`. Sources remained unchanged, driver exited0 normally, the exact owned app/volume were removed and run-private state was deleted.

The run proves a real other-actor watched-list digest, mute/unsubscribe, a complete102-item Reader snapshot with100-row local paging and literal last-page-note filtering, zero offline writes, neutral-shell offline restart, new actual session rejection followed by explicit complete refetch, eight-day expiry pruning, opt-out physical clearing and a second stored copy followed by actual POST logout303/API401/raw owner+rows empty. Mobile/light and desktop/dark offline screenshots and mobile settings were captured; mobile screens were visually inspected. Device push is explicitly disabled in this campaign. Other push states and safe worker click behavior have unit/protocol evidence, not physical vendor-delivery evidence.

Base, optional Push and OCR+Push Compose configurations pass actual `config --quiet` validation without service changes (`deployment-config-0720d7b8558a4bfdb983e285d3d4b347/`). Read-only npm audit reports zero vulnerabilities of every severity and unchanged package-lock (`npm-audit-2422d6f43f044611b608893635ff34c9/`). Operational recovery, Linux race, complete28-component regression and final staged-secret acceptance are still open.

### Real encrypted recovery accepted

Recovery run `71165f560e7d4ab987f807856a187650` actually passes all11operator stages. Report: `outputs/shopping-module-recovery-review/run-71165f560e7d4ab987f807856a187650/recovery-report.json`, SHA256 `508fea3944b2518099ece0637b545fd86b15521941dba07ece74432a1b3e6386`. It consumes the successful standalone database without resetting it: two real lists,102items, actual digest/outbox/delivery records and one empty-watch user revision fence. The other eight Shopping business tables and global Push devices are explicitly empty fixtures, not claimed populated recovery cases.

The rehearsal performs actual owner bootstrap recovery, HTTP login/mandatory password change, recent-admin disable, stable typed row inventories, encrypted backup and security-key bundle comparison, real isolated restore-check, exact restored inventory/readiness, real HTTP purge request, unchanged App-role denial, successful owner purge, HTTP re-enable and old-generation409. Source/restored/final readiness has95audit policies and clean module versions, with all75SQL pins unchanged. Restored lists/items/Core digest work and consent tombstone match the stopped source; all ten business tables are empty after the authorized purge and generation advances1→2. No synthetic backup eligibility or purge intent is inserted.

All three exact owned containers, four volumes, private credentials and temporary repository were removed. Source PostgreSQL and live8100 were not stopped or replaced. The temporary repository proves this rehearsal, not a permanent backup schedule/RPO. Encrypted registered Push-device restore and attachment contents were not exercised because those boundaries are explicitly empty here. Native encrypted-device contracts remain separate evidence. Linux race, complete28-browser regression and final staged-secret/commit acceptance remain open.

### Staged-source security and packaging

An exact720-file index copy was checked against every Git blob ID; all75SQL migration files retain identical raw bytes. Only the75Shopping-related changes are staged; the deferred financial document remains unstaged. Its older tracked version is included in the full index scan, while its separate worktree delta is explicitly excluded from this module. CRLF→LF packaging normalization is recorded separately, including two changed templates with identical normalized text. No Go/JavaScript/SQL content change is hidden by this comparison; the raw527-source runtime manifest remains58c.

The first scanner invocation had a malformed PowerShell option expression, no structured report and an unverified scan scope; its log/reported hits are retained and excluded from acceptance. The corrected argument-array invocation scanned the exact6.74MB index copy with the complete default rules/decoding settings and passed with **zero findings** in2.131seconds. Accepted proof: `outputs/shopping-module-validation/staged-secret-repeat-9759808c6c2944deb0217031c6963ced/accepted-index-proof.json`. Exact scanner binary SHA256 is recorded. Final documentation/checklist staging will require a fresh exact-index scan before commit.

### Linux race: actual tests passed, report reconciliation pending

Actual Linux/amd64 Go1.27.3/CGO1 execution finished exit0 with **44/44 selected mains passed, zero failures or skips**, including all22new Shopping native contracts. Source527 before/after remains58c. Original campaign: `outputs/expansion-review/shopping-module-race-final-20261030/`; complete actual test events and environment are present. Root independently checks the exact44source-selected pass identities.

The campaign wrapper reports `failed_cleanup`: its sanitizer rejects a valid zero-byte stderr file before completing all exports. The exact race container and both cache volumes were removed; raw private logs/source copy remain retained. The original failed wrapper report is preserved. A narrowly scoped postprocessing-only reconciliation must sanitize empty files, validate discovery/terminal/shuffle/source evidence and remove only the exact retained owned state before race acceptance is checked. No database/Go rerun is inferred from postprocessing. The full28-browser regression is running separately after the database was released.

### Whole-suite regression correction and current layout source

Full28 run `a2329b2d9aae4e1bb018dfd53e521aeb` failed in the older runtime cache assertion, which omitted `/offline/shopping`. Both existing runtime/Pantry browser assertions now require all three neutral shells while preserving exclusion of every private path. Syntax and fullNode245/245 pass. Run `0561d5dc5fbf4ff1897313f1887ef1eb` then passes the first three components but exposes a genuine mobile regression: the two new personal controls displace the first check action below fixed navigation at390×844. The valid browser assertion is preserved.

The reviewed correction moves the unchanged unconditional personal-control block below items/paging. It preserves Reader access, all controls/selectors,44px targets and all Go/JS/CSS/SQL. Only `internal/web/templates/shopping.html` changes from source58c. New527-source manifest: `outputs/shopping-module-validation/post-layout-freeze-977952b290ab47149432e260a8dc5cc5.json`, SHA256 `42c434abc668879ace5c018a505be7e436d47944dc640308f5f58a742266d610`. The mechanical layout scan returns no findings, with an explicit limitation that it cannot resolve the template's dynamic stylesheet link; rendered browser checks remain required. Mobile acceptance step120 is reopened until the corrected runtime is verified.

Current42c executed repeats: fullGo247mains/1,464case passes/zero skips in13.070s (`unit-313ae0b8d77a4945a394a41288374a20/`), fullNode245/245 plus workflow/75SQL/checklist/source fences (`layout-node-static-c396c57d36694a38a483dfedf3d51580/`), and fresh24native mains/zero failures or skips in225.797s (`outputs/expansion-review/shopping-module-native-layout-24-20261030-01-summary.json`). Exact composed122proof SHA256 `08105e2d7f71d49a76bd3e8d13b89411e881a64b192cc1b72544913ac0c0613e` is under `composed-122-current-layout-proof.json`. It explicitly lists the cache-test and HTML deltas from originala43; other Go/JS/CSS/SQL/test bytes remain identical. It never claims current-layout browser acceptance from native composition.

The new non-root source-labelled images are runtime`48de87ac…`, OCR`29d730de…`, backup`ab85f1cc…`; exact IDs and build durations are in `layout-images-e3741622f3734ccf91b3e2900c916049/summary.json`. A fresh standalone browser repeat is running. New local recovery/regression runner variants share the exact current-layout evidence validator while preserving historical helper bytes and every operator/privacy/cleanup guard.

Race reconciliation actually passed: `outputs/expansion-review/shopping-race-reconciled-762e198cf3fd4443890b6612d4d9bf57/reconciliation.json`. It revalidates all44actual pass identities, discovery, five shuffle seeds, LinuxGo1.27.3/CGO1 environment, source copy and original exit0/removals. All nine raw log files, including empty stderr, are sanitized. Exact retained private/source directories are removed. The original failed export report remains unchanged. This is postprocessing of the real source58c race; current42c differs only by the explicit HTML line relocation, with every Go/race test input unchanged. No current42c race rerun is claimed.

### Corrected layout: executed browser, recovery and complete regression

Current42c standalone run `456957b696e043d9902947ed77adf6b2` passed 37 checks, three PNG captures and three Axe analyses with zero violations, JavaScript errors or external frontend requests. It exercised the real Reader watch/digest/mute/unsubscribe flow, complete 102-item Reader snapshot, offline restart without writes, changed-session refetch, expiry, opt-out and real logout with raw IndexedDB clearance. The source remained unchanged, the browser exited normally, and the exact app container, upload volume and private state were removed. Report: `outputs/shopping-module-browser-review/run-456957b696e043d9902947ed77adf6b2/orchestration-report.json`, SHA256 `6e74089e6d3b82e83d0c8c5dde98db3d33af14f405b509deb1603fceb5317f6f`.

Current42c encrypted recovery run `130ec546d9954b38b3cd9b339827e0db` passed all 11 actual operator stages on the populated standalone database, including security-key roundtrip, encrypted backup and isolated restore, 95 audit policies, HTTP purge request, App-role refusal, Owner purge, re-enable and old-generation rejection. All three exact owned containers, four volumes, private state and temporary repository were removed; the source database and live 8100 remained untouched. The temporary repository is a rehearsal, not a permanent backup schedule or RPO. Report: `outputs/shopping-module-recovery-review/run-130ec546d9954b38b3cd9b339827e0db/recovery-report.json`, SHA256 `88552ecbe71820c0c53b79b5364dd8cb129a1f6d6380e1514eae93f6aaad04c8`.

Current42c full existing browser run `b825e690c46f42fa8f05359d409a6f06` passed all 28 distinct components, 912 checks, 135 Axe scans and 147 verified PNG copies. Its unchanged first-open-action assertion passed at 390×844 after the layout correction; the mobile screenshot was visually inspected. The run used the source-labelled OCR runtime, a freshly reset isolated `household_test`, the current 24-main native evidence and explicitly composed 122-main proof. All 28 reports are fresh, the browser exited zero, source/evidence were unchanged, and its exact app container, upload volume, private handoff and browser descendants were cleaned. Optional external FoodFacts and Web Push remained off. Report: `outputs/shopping-module-regression-review/run-b825e690c46f42fa8f05359d409a6f06/regression-report.json`, SHA256 `da5fa3a0641f65717c2536a87f1c10f2b5f4ba3c8029bee0bb5b1b4753b9e8cc`.

The native 122-main statement remains a composition of 24 actual current42c passes and 98 actual earlier passes whose relevant source bytes are unchanged, not one green 122-main run. The Linux race was actually run on source58c; only the Shopping HTML block moved afterwards. Physical vendor Push delivery, hosted CI, production deployment, and permanent backup timing remain untested and are not claimed.

Local acceptance evidence map (all listed campaigns passed within the source and environment limits above):

| Campaign | Required evidence |
|---|---|
| Go unit, Node and source checks | Actual nonzero passes; no invented or omitted test entry points. |
| Native PostgreSQL | Producers, Reader ownership, current sessions, revisions, fanout continuation, leases, digest dedupe, snapshots, privacy and purge. |
| Controlled push protocol | Actual encrypted requests, JWT claims, bounded HTTP/status/retry behavior and current worker authority; no physical delivery claim. |
| Dependency / license / security | Module verification, dependency license inventory, vulnerability scanner, Gosec and an actual staged-blob secret scan. |
| Immutable database history | Exact current bytes for all 75 SQL pins, fresh/repeated migration and predecessor compatibility. |
| Browser / accessibility | Real online and offline workflows, fresh-session/reload fences, raw IndexedDB observations, static read status, mobile/keyboard and Axe. |
| Race detection | A source-pinned Linux campaign covering affected concurrency and worker behavior. |
| Recovery / full regression | Actual populated encrypted backup/restore/purge/generation and the existing complete browser chain. |

An independent read-only review rehashed all 527 current42c application sources and all 28 final component reports, finding zero mismatches, Axe violations or browser errors. The HTML relocation remains outside Reader/write-only branches and preserves selectors. This closes the scoped local SH15/SH17 behavior, UI, privacy, database and recovery review. Final staged-index secret scanning and the local Git checkpoint are packaging gates; external vendor delivery, hosted CI, deployment to 8100 and a permanent backup schedule remain outside this local acceptance.

The updated index contains exactly 77 Shopping changes and leaves the separate financial verification draft unstaged. An exact720-blob index copy matched every Git object ID; all75SQL migration raw bytes match. Full default Gitleaks scanning with decoding and redacted output found zero secrets and left the index unchanged (`outputs/shopping-module-validation/staged-scan-0591b7c784164fc7aa20c8f275f3cf39/`). The final documentation-only delta will be included in one more exact-index scan before the local commit; its output records the committed scope.

## Evidence boundaries

Browser permission, server registration, controlled protocol execution, gateway acceptance and actual physical vendor delivery are separate facts. Optional push will not send to other people during development. Synthetic push events cannot prove vendor or OS delivery. A temporary recovery repository cannot prove a permanent backup schedule or RPO. Hosted CI and production deployment remain unclaimed unless actually executed.

The controlled HTTP setter is compiled only with `testtools`; ordinary production builds must omit it. CI keeps the existing integration-only coverage shards and adds a separate controlled push campaign. The latter refuses an empty inventory, skipped required tests, unfinished cases and changed source.
