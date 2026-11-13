# Workspace navigation and Today checklist

Approved scope: A (progressive disclosure) + B (object-first workspaces) across all nine modules; C (Today) as the home screen. D (personal favourites and a command palette) is deferred. Ocean/Pink, automatic light/dark appearance and German/English remain available.

## Acceptance criteria

- Two or three primary destinations per module. Actions, record tools and administration have distinct homes.
- Existing routes, permissions, record context and unsaved-change safeguards remain usable.
- Today uses bounded, authorized live projections and truthful empty/partial-error states; no demo data or private offline cache.
- Each completed module receives focused regression checks before proceeding to the next module in its implementation sequence.
- Checkboxes below mean executed evidence, not a prediction. Real-device push acceptance remains deferred by the user.

## Implementation and verification

- [x] Inventory current module routes, controllers and navigation dependencies.
- [x] Shared primary/context navigation, management destinations and responsive styling. Seven focused Go contracts, full web/i18n unit packages, and initial module browser matrices pass. Remaining modules have their own gates below.
- [x] Today home: assigned due tasks, authorized notices, own cooking and today’s meals. Four Go units, JavaScript/localization checks, three isolated database integration mains and the strengthened notification-authority regression pass. Full browser: 102 checks/16 clean Axe scans; final compact 2×2 desktop layout: 66 checks/16 clean Axe scans.
- [x] Vehicles: Go/JavaScript checks pass; final functional browser 30 checks/2 clean Axe scans, including legacy-link relocation and saved-form navigation; theme/language/role matrix 223 checks/16 clean Axe scans.
- [x] Tasks: Today, Plan and History with series/area context. Focused Go/JavaScript checks pass; actual functional browser 29 checks/2 clean Axe scans; navigation matrix 226 checks/16 clean Axe scans. The history request now retains the explicitly selected area.
- [x] Meters: Overview, Readings and Costs with selected-register context. Focused Go/JavaScript checks pass; actual functional browser 35 checks/2 clean Axe, navigation matrix 229 checks/16 clean Axe. Unavailable registers require an explicit new selection.
- [x] Shopping: 63 JavaScript tests and shared navigation/parent-context units pass; navigation matrix 214 checks/16 Axe scans; checkout 32 checks/4 Axe and reuse 17 checks/4 Axe pass. Existing browser scripts now explicitly select DE through the real language setting.
- [x] Recipes: Recipe Book/Cooking and collapsed keyboard-accessible in-recipe contents. 35 JavaScript tests and shared navigation tests pass; navigation matrix 217 checks/16 clean Axe; actual editor workflow 107 checks/8 clean Axe, including keyboard anchors, long tags, conflicts, drafts and logout.
- [x] Pantry: Stock/Use First and contextual product/lot tools; 41 JavaScript tests and shared navigation tests pass. Navigation matrix 217 checks/16 clean Axe; consumption 36 checks/5 clean Axe; replenishment 32 checks/6 clean Axe. Product-edit action label restored after the first replenishment run exposed its ambiguity.
- [x] Finance: transactions/planning/reports and account-owned tools. 48 focused JavaScript/localization tests; focused browser 44 checks/2 clean Axe, full finance workflow 297 checks/16 clean Axe, navigation matrix 226 checks/16 clean Axe. Account context, unavailable accounts, unsaved edits and privacy after logout are covered.
- [x] Documents: inbox/library/deadlines and document-owned review. Focused navigation42 checks/2 clean Axe; full PDF workspace120/16; reminders121/16; role/theme/language/mobile matrix226/16. All509 browser checks and50 Axe scans passed; focused Node/localization15 passed.
- [x] Meal planning: direct working-week entry and contextual metadata, collapsed preparation tools. Go/JavaScript units pass; workflow44/4 clean Axe, extensions92/8, preparation reminders118/16 and role/theme/language/mobile matrix217/16 passed.
- [x] DE/EN, all four theme combinations, keyboard/mobile accessibility and cross-module regression. All nine module gates and Today have executed browser evidence; automatic theme behavior remains covered by the existing theme tests.
- [x] Update product, design, README and implementation evidence.
- [x] Rebuild and recreate Household local development app8100. Exact-image scan passed; migration runner completed with unchanged versions; app/database healthy;100 HTTP checks and93 byte-exact assets passed. Database container, volumes, secrets and environment preserved.
- [x] Remove exact-owned temporary test containers, network, uploads and private fixture files. Preserve rollback image, live override and evidence; D and physical-device push acceptance remain deferred.

## Evidence

Completed locally on13 November2026. No remote deployment or publication. Earlier interim entries below remain as execution history.

- `go test -p 1 ./internal/web ./internal/i18n -count=1`: passed after fixing an unused offline-shell stylesheet dependency.
- `node --test scripts/localization.test.cjs`: 11 passed after adding the new reviewed labels.
- Vehicle functional evidence: `outputs/ux-workspace/operational-fuel-1794389885379/result.json`.
- Isolated fixture: `outputs/ux-workspace/dd732ac9e3e7/fixture.json`; app 8101 and PostgreSQL 55434, all nine modules, three real roles. Live 8100 remains untouched until final tested refresh.

- Full untagged Go suite `go test -p 1 ./... -count=1`: passed during foundation/Recipes qualification.
- Today database evidence: `outputs/ux-workspace/today-integration-20261111T094535Z/`; Store day/household/bounds and Web assignment/privacy/home/navigation, plus the strengthened withdrawn-notice candidate regression.

- Tagged `golangci-lint run --build-tags=integration ./...`: 0 issues with the project-local cache. An initial run using the inaccessible global cache was stopped and is not counted as a pass.

- Gosec: 394 files / 80,793 lines, zero findings and zero Go analysis errors (`outputs/ux-workspace/gosec.json`).

### Final automated qualification (11 November 2026)

- Full ordinary Go suite passed after the Documents page-query and Mealplan record-destination changes: `outputs/ux-workspace/final-go-unit.log`.
- All 516 Node tests passed. The first run exposed missing registration of new suites in `verify.ps1` and the quality workflow; both entry points were corrected without removing assertions. Final report: `final-node-unit-rerun.log`.
- Tagged golangci-lint: zero issues; Gosec: 395 application files / 80,895 lines, zero findings or Go analysis errors. An accidentally broad Gosec invocation was stopped and replaced by the explicit `cmd/... internal/...` scope; it is not counted as a pass.
- Redacted Gitleaks scan of all 1,326 tracked/unignored authored files: no secrets found. Temporary scan copies removed.
- Workflow policy, indexed local-artifact policy, authored design-preview consistency, 119 migration checksums and the separate 180-improvement task-list consistency checks pass. No staging or publication occurred.

- Current online govulncheck identified reachable standard-library advisory GO-2026-6859 on Go1.27.3. This is an open acceptance failure until the patch toolchain and rescans pass; it is not suppressed. The first network-restricted attempt also remains recorded.

### Continuation — 13 November 2026
Go1.27.4 and its verified Docker digest are now configured for app/backup builds and local tooling; workflow alignment passes. Online govulncheck reports zero reachable vulnerabilities. Module integrity checking found modified local pgx/msgp cache directories; they were quarantined for checksum-verified restoration. Interrupted unit/build runs are not accepted evidence. Final Documents/Mealplan workflows and live refresh remain open; live8100 was not changed.

### Final continuation — 13 November 2026

- Restored cache: `go mod verify` passes; full Go suite, all516 Node tests, integration-tagged lint and online govulncheck pass with Go1.27.4. The vulnerability scan reports zero reachable vulnerabilities (one unused module advisory remains informational).
- Reboot had cleared the disposable test PostgreSQL schema. Reinitialized only that fixture, renewed real admin/editor/reader sessions, and bounded PostgreSQL at2 CPUs/1GiB after the strict2-second readiness check exceeded the earlier1-CPU allowance. No readiness assertions or deadlines were relaxed. Live data is unchanged.
- Documents focused navigation:42 checks/2 clean Axe scans; full actual PDF/workspace campaign:120 checks/16 clean Axe scans, no runtime errors. Reminder and final matrix gates pending.

- Weekly planning:44 actual workflow checks/4 clean Axe scans and92 extension workflow checks/8 clean Axe scans passed. The initial back-link test omitted the visible arrow from its exact accessible name; corrected the selector and reran the full workflow. Reminder fixture selection now uses the same exact current-container authority checks as Documents instead of the retired fixture ID. Final55 targeted test/driver contracts pass.
- Exact Docker candidate built and scanned successfully: `live-refresh-651d059755e7492aa5a8f51b1057b612/result.json`. Trivy reports zero fixable HIGH/CRITICAL findings. Activation remains pending the final reminder gate.

### Completed local delivery

Preparation reminders passed118 browser checks/16 clean Axe scans. All nine module gates and Today are complete. Final live evidence: `outputs/ux-workspace/live-refresh-3b87c8047c6d4a2ea65b6db782ec7a49/result.json` and `public-assets.json`. Exact candidate: `sha256:072264dff3ecd12b5a253c2f04c6e716fe449bc6dab4ad3731ce851b7cc45619`. App and database are healthy; observed memory18.86MiB/160.2MiB respectively. Task test containers/network/uploads and retired private fixture directories removed. No push, publication or remote deployment. Authenticated workflows were tested on the isolated fixture; live verification deliberately used public GET endpoints only. Real-device notification delivery remains deferred by user; D remains out of scope.
