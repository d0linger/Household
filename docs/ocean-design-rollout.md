# Ocean design rollout

## Approved scope

The user selected **Ozean**, a color variant of **Ruhe**, from [Designideen erstellen](https://chatgpt.com/c/6ae888a6-89c0-83ed-890a-6bcb6b4835d6), and requested application-wide adoption including Pink Light and Pink Dark. This approval supersedes the former visual identity for this redesign; another design-selection round is unnecessary.

The actual Ozean preview is now visually inspected in signed-in Edge, including its Dark/Light states and native appearance dialog. The temporary desktop-control access problem was resolved by bringing the preview into view. The reference uses a flat navy/ice-blue canvas, blue actions, separated rows and a softly tinted editing panel. Implementation colors are derived from this visual reference; source CSS was not extracted. The Light preview's near-white heading defect is deliberately corrected. See `ocean-design-contract.md`.

## Implementation checklist

- [x] Identify the chosen chat, design family and color variant.
- [x] Inventory shared templates, authentication/public/offline surfaces and all nine modules.
- [x] Inspect the actual Ozean/Ruhe preview or exported source.
- [x] Record its layout, spacing, typography and Ocean light/dark palette.
- [x] Add and test the forward database migration and explicit API support for `pink-light` and `pink-dark`.
- [x] Add and test safe early theme initialization, device persistence, system appearance and cross-tab updates.
- [x] Integrate four palettes and native-control color schemes; preserve system preference as an additional selection.
- [x] Apply the selected design to the shared shell, navigation, dialogs and status states.
- [x] Apply shared Shopping styling; verify module entry, populated list, creation dialog and offline reader shell.
- [x] Apply shared Fuel styling; verify module entry and typed capture workspace.
- [x] Apply Recipes styling; verify entry, search, import review, cooking and offline recipe shell.
- [x] Apply Pantry styling; verify entry, priority, waste and scan pages; remove fixed-color fallbacks.
- [x] Apply Finance styling and verify module entry and shared form presentation.
- [x] Apply Chores styling and verify module entry and selected-control contrast.
- [x] Apply Meters styling and verify typed overview/capture, module entry and selected-control contrast.
- [x] Apply Documents styling and verify module entry and shared form presentation.
- [x] Apply Meal Planning styling and verify module entry and planning/purchase comparisons.
- [x] Verify settings/security, search, notifications, login, missing-record and public offline routes; account/audit templates inherit the common shell.
- [x] Verify PWA chrome, install assets and actual service-worker caching of neutral offline shells.
- [x] Execute four-palette mobile/desktop layout, keyboard, contrast and functional regression checks.
- [x] Complete independent visual review, resolve its three fixes and update DESIGN.md/README.
- [x] Verify the final review binary matches source and remove owned review processes, database and temporary credentials.

These boxes describe this appearance rollout, not a fresh execution of every module's business workflow. Checkout settlement, camera hardware, OCR, every populated history and all domain integration suites were not rerun. Their shared tokens/templates changed; existing module acceptance remains recorded separately in `implementation.md`.

## Integration constraints

Use the existing Go/templates/vanilla JavaScript/CSS stack and embedded local assets. Preserve routes, form payloads, IDs relied on by scripts, authorization, exact financial values, offline privacy and module contracts. Never modify released migrations. Pink Light must remain light even when the operating system uses dark appearance.

The database's original theme constraint and API admitted only system/light/dark. Core migration 35 and explicit API validation now also admit pink-light/pink-dark; CSS alone would not provide persistent Pink modes. It also advances Fuel's strictly verified Core-version compatibility predicates from 34 to 35. All production/public/offline pages share `page.html`, making a common initialization and token layer the primary integration point. Module-specific fixed colors and selected-control foregrounds were adapted to shared tokens.

## Executed acceptance — 2 November 2026

| Check | Actual result | Evidence under ignored outputs/ |
|---|---|---|
| Node suite | 338/338, including 12 theme cases | ocean-design-review/node-tests-elevated.txt |
| Ordinary Go suite | All packages passed; serial, two Go CPUs | ocean-design-review/go-all-unit-tests-elevated.txt |
| PostgreSQL theme/readiness integration | 14 subtests passed, including real 34→35 upgrade, full readiness, unchanged preference/timestamp, repeat migration, invalid inputs and fail-closed tampering/version checks | ocean-theme-review/preferences-readiness-integration.txt |
| Tagged web/database vet; migrations | Passed; 108 checksums verified | ocean-theme-review/preferences-readiness-vet.txt |
| Main browser theme matrix | 204 checks, 60 Axe scans, 68 computed contrast pairs; zero failures/runtime/CSP errors/external requests | ocean-design-review/browser-theme-report.json |
| Deeper typed routes | 80 route/theme/viewport checks, four Meters Axe scans; zero failures/runtime errors/external requests | ocean-design-review/browser-theme-supplement.json |
| Existing functional browser regression | 111 checks, 11 captures; keyboard actions, offline review/replay, logout cleanup; zero runtime errors/external requests | ocean-design-review/core-browser-report.json |
| Final reviewer fixes | Eight action-gap checks (desktop 12px, mobile 10px), two waste style probes, two clean Axe scans; ten refreshed full-page captures | ocean-design-review/final-capture-report.json |
| Independent finish review | Three requested fixes resolved; disposition ship for those fixes | ocean-design-review/finish-review.md |
| Design documentation | 48 palette values matched CSS; YAML references and JSON validated | DESIGN.md and .impeccable/design.json |

The matrix exercises real settings persistence/reload, explicit Pink Light under a dark OS, system following, cross-tab updates, blocked storage, native dialogs, page overflow, and real cached offline shells. Four desktop Shopping and four mobile settings captures were visually inspected in every palette, with additional Meters, Fuel and waste views. The waste fixture has no transactions: its divider was verified using a transient DOM style probe removed before screenshot; no populated report is claimed. All fixtures were synthetic and isolated from live data.

### Findings and corrections

The initial ten-subtest preference test missed complete application readiness. An isolated startup then exposed Fuel's strict Core-34 requirement. Unreleased Core35 now verifies its exact predecessor definitions/owners/ACLs before advancing the compatibility predicates; the final 14-subtest integration and actual app startup pass. Released migrations remain unchanged.

The first sandboxed Node run could not create a disposable security-test junction; the unrestricted complete rerun passed without skipping it. A sandboxed full Go run stalled in a loopback-network test; its owned process pair was stopped and the bounded unrestricted full suite passed. The first browser matrix had an about:blank storage-access error in the test initializer; an origin guard fixed the harness and the complete matrix reran cleanly. The first final-capture attempt expected a populated waste summary; the corrected explicit style-probe check passed against the genuine empty report. Failed attempts remain in the evidence directory.

The independent review requested action separation, removal of the waste accent stripe and replacement of obsolete design documentation. All three are resolved. Its final verdict covers those scored fixes, not every application screen. The source reference was visually inspected but no direct reference capture or external quality-bar card was supplied to that reviewer; pixel-identical fidelity is not claimed.

### Delivery boundary

Changes preserve unrelated backend/workflow edits. The review app embeds the final assets in a newly built binary. All 790 `cmd`/`internal` files match its build manifest, with no missing, added or changed files, and its executable SHA matches the recorded build. Evidence: `outputs/ocean-design-review/build-source-verification.json`.

Cleanup passed: the owned review app process and exact labeled PostgreSQL container were removed; ports 8101/55434 are released. Temporary fixture binaries, environment, session handoff and secrets were removed; reports, logs, manifests and screenshots remain. Live Household container IDs/images/start times, port 8100 and the pre-existing preview on 8102 are unchanged. Regular Edge sessions and shared caches/images were untouched. Evidence: `outputs/ocean-design-review/stack-cleanup.json`.

No push, publication or live Docker deployment occurred. Rebuild the application/container to install the new interface; merely restarting the existing image leaves the old embedded assets in place.


## Local development deployment — completed 2 November 2026

- [x] Record the user's standing instruction to recreate local development Docker after application changes in AGENTS.md.
- [x] Take an encrypted pre-upgrade snapshot and verify it in the isolated restore database.
- [x] Rebuild the current working tree and recreate Household app/database/migration containers, retaining named data/uploads volumes and secrets.
- [x] Verify healthy containers, successful migrations, HTTP readiness and exact served asset bytes.
- [x] Verify all four themes on the local deployment with a read-only mobile login browser check.
- [x] Remove the disposable restore container and retain the encrypted snapshot and deployment evidence.

The new design now runs at **http://localhost:8100**. This follow-up supersedes the earlier delivery boundary above, which described the isolated acceptance phase. No publication or remote deployment was performed. Evidence: `outputs/ocean-dev-refresh/`; details in `implementation.md`.
