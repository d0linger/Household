# Warm Midnight rollout

Approved 20 November 2026. Work proceeds one phase at a time; a phase is complete only after its applicable checks pass. Reference package: Household-Warm-Midnight-Designpaket (19 November, 17 images). No reference image supplies production data.

## Decisions

- Keep the persisted six theme values and system-following behavior. Rename the visible standard family to Warm Midnight; retain Pink.
- Standard light uses warm ivory, burgundy headings and plum/copper navigation. Standard dark uses warm dark surfaces. Pink retains light/dark variants.
- Settings content deliberately uses dark Midnight Atlas, including in light mode. Its dialogs inherit the administrative palette. The right navigation tray uses plum/copper independently.
- Preserve all routes, authorization, input names, identifiers, review and conflict flows. No schema changes or new runtime dependencies.

## Checklist

- [x] Phase 0: inspect repository, specifications and all 17 reference images; document risks and file map in chat.
- [x] Phase 1: shared shell, semantic themes, typography; theme, template, localization and browser checks.
- [x] Phase 2: Today; bounded real sections and responsive/state checks.
- [x] Phase 3: Shopping; capture, list, review and offline regression checks.
- [x] Phase 4: Recipes, cooking and pantry; version, timer and stock checks.
- [x] Phase 5: Finance and vehicles; privacy, exact values and workflow checks.
- [x] Phase 6: Planning, chores, meters, documents; module browser checks.
- [x] Phase 7: General and Modules; preferences, ordering and purge protections.
- [x] Phase 8: Notifications and Security; consent, devices, reauthentication and factor checks.
- [x] Phase 9: Users and Audit; modal creation, role and protected detail checks.
- [x] Phase 10: right tray and mobile navigation; keyboard, dismissal and rail checks.
- [x] Phase 11: complete visual/accessibility/regression acceptance.
- [x] Rebuild local development Docker stack; verify health, readiness and embedded assets.

## Evidence

Phase 0: clean dev checkout ce0b435. Source and reference analysis only; no runtime test pass claimed.

Phase 1: 22 theme and 11 localization Node tests passed; `go test ./internal/web ./internal/i18n` passed. Baseline browser: 115 checks, 11 captures, no page errors or external requests. Theme matrix: 204 checks, 60 Axe scans, eight captures, zero failures (`outputs/ocean-design-review/browser-theme-report.json`). Initial matrix failed because its selected Shopping fixture had fewer than three items; fixture completed without weakening assertions, original report retained. Read-only review found an oversized public login heading and obsolete PWA colors; both fixed. Focused login fold/reflow/Axe and exact manifest/PNG asset checks passed. Hallmark static app.css check: zero FAIL/REFLEX, two inherited palette warnings dismissed with browser evidence, two finish notes answered. The render sweep and full application acceptance remain Phase 11. Physical OS installation was not tested.

Phase 2: Today Node tests (4) and Go Today handler tests passed. Full browser suite passed 102 checks, 16 Axe scans and 16 captures across three actual roles, DE/EN and four palettes at 320/1440px, including empty/disabled/error/lifecycle states. Desktop/mobile captures inspected. Evidence: outputs/warm-midnight/today/report.json. Fixture setup initially failed ownership checks, then was restored into its own labeled, bounded container after a verified dump of the original test database. The known pg_dump barcode-expression representation was normalized to its original constraint in this test copy; application readiness passes. No development data was changed.

Phase 3: 63 Node checks passed. Shopping extensions: 38 checks/3 Axe scans; checkout: 32/4; merge: 13/2. Theme matrix: 204 checks/60 Axe scans/8 captures passed. Corrected the extensions suite's inherited-language assumption using the existing real locale helper; no assertion weakened. Visual inspection found the progress paragraph's inherited measure; removed that constraint. Rebuilt and confirmed capture-before-list ordering, aligned progress widths and no overflow at 1440/390/320px (outputs/warm-midnight/shopping). A first confirmation launch raced server startup and was rerun after readiness.

Phase 4: Recipe action moved to the header without changing its URL or authorization. Ingredient/preparation columns, cooking data panel and Pantry row density completed. 75 Node tests and go test ./internal/web ./internal/i18n passed. Browser recipe-workspace 107/8 Axe, cooking 149/20, cooking-stock 34/5, pantry-daily 36/5, pantry-batches 37/5; all passed. Real mobile conflict/long-tag and desktop cooking captures inspected. No reference photo or fabricated nutrition data was added; existing attachment access stays unchanged. Physical audio and Push delivery remain untested.

Phase 5: 53 Node tests passed. Finance browser: 296 checks/16 Axe scans; Fuel overview: 15/2; Finance navigation: 44/2. The navigation suite initially lacked its required private test password; supplied the synthetic fixture credential and passed the real logout/reauthentication path. Visual review identified detached Finance labels/values; corrected the definition-list grid, rebuilt and verified paired geometry/no overflow at 1440/390/320px. Actual desktop/mobile reports inspected. Full Fuel OCR/backup-purge recovery was not rerun for this presentation-only change; no such pass is claimed.

Phase 6: 44 Node tests passed. Planning browser 43 checks/4 Axe, Chores 74/16, Meters 117/14, Documents 119/16 all passed. Inspected mobile agenda and document deadlines. The first Documents run correctly failed because the native Windows review process lacked PDF tools; replaced it with an owned, bounded Docker review server using the existing OCR runtime. Actual PDF extraction, preview, original hash, search and document workflows then passed. The private startup probe timed out too aggressively; independent HTTP readiness and the browser suite confirmed readiness. No application assertion was relaxed.

Phase 7: shared Admin C tokens and General/Modules presentation completed. Theme/localization 33 tests and Go web/i18n passed; full go test ./... also passed. Theme browser 204 checks/60 Axe; navigation/personal order 400/16; targeted admin palette/reflow 24/6 across four modes and 1440/390/320px. Module activation/pause, rejected saves, retry, cross-context ordering and role-aware navigation passed. Native purge disclosure and all server gates retained. Desktop module layout inspected. Evidence: outputs/warm-midnight/admin and outputs/navigation-personalization/browser.

Phase 8: notification permission/category/device groups and security panels completed. 75 security/notification/worker Node tests passed. Notification browser 512 checks/18 Axe/18 captures passed. Complete settings/security browser 209 checks/34 Axe/25 captures passed, including actual TOTP, recovery, sessions, roles and CDP virtual WebAuthn. The initial security run stopped after a language navigation before deferred dialog handlers were ready; the test now waits for page load, retaining every assertion. Failed evidence is preserved separately. Actual disabled-device mobile presentation inspected. Physical authenticator hardware and OS Push receipt are not tested. Evidence: outputs/warm-midnight/notifications and outputs/warm-midnight/security.

Phase 9: desktop member/form alignment and compact Audit rows completed. Audit browser 16 checks/2 Axe/3 captures passed; settings/users/security 209/34/25 passed again after the layout change. Initial Audit run inherited English while asserting German; it now uses the existing real locale helper before testing, retaining its assertions. Actual actor labels, before/after masking, role effects, modal creation, focus and reauthentication passed. Evidence: outputs/security-review/browser-audit-report.json and outputs/warm-midnight/users.

Phase 10: right tray now uses independent plum/copper (Pink selection in Pink modes), paired hover/current text, visible focus and existing native placement. Navigation suite passed 400 checks/16 Axe, covering roles, install projection, logout, right anchoring, focus/escape/backdrop, mobile rail and personal ordering. Evidence: outputs/warm-midnight/tray/report.json.

Phase 11: final npm test passed 524 tests with no skips; go test ./... passed, followed by another successful web/i18n run after the final template/catalog changes. Final presentation matrix passed 457 checks/36 Axe/40 captures across 18 destinations, DE/EN, all four explicit palettes, five viewport widths, forced colors, reduced motion, right-tray focus and 200% reflow equivalent. The older module-polish suite passed 216 checks after preparing its required German locale and an actual empty document search through the real filter form; no product assertion was removed. The full Documents and Chores persistence suites remain the earlier phase evidence; the final polish suite deliberately uses non-persisting previews.

Independent Hallmark review inspected all 17 references and actual module/admin/tray captures. Its one finish finding—mobile Finance bars initially outside the horizontal viewport—was resolved with an overflow instruction, keyboard guidance and an accessible description. Focused 1440/390/320 checks confirmed instruction visibility and real arrow-key scrolling; 30 Finance/localization tests and final 524-test suite passed afterward. Exact values and authorization are unchanged. Shared spacing aliases now use the existing tokens with identical geometry. Navy data panels explicitly carry contrasting danger/warning tokens.

Hallmark static scan: zero FAIL/REFLEX/WARN across six changed CSS files. Its built-in render adapter could not run because puppeteer-core is absent; no pass is claimed for that adapter. Actual Playwright/Edge renders, Axe and the independent finish review provide the visual evidence. Git diff whitespace, indexed-artifact guard and a redacted Gitleaks scan of changed/new authored files passed. DESIGN.md, its Impeccable sidecar, PRODUCT.md and README.md reflect the implemented system. Detailed acceptance: [warm-midnight-qa.md](warm-midnight-qa.md).

Limits: physical Windows/Android/iOS Push delivery, hardware authenticators, manual screen-reader use and browser-menu/OS zoom were not tested. The 200% check verifies equivalent layout reflow at 720×450 CSS pixels and DPR 2. Full Go integration/race and Fuel OCR/backup-recovery campaigns were not rerun for this UI change; actual document PDF processing and the listed real PostgreSQL-backed browser workflows did run. No remote publication or deployment was requested or performed.

Local Docker completion: the regular multi-stage OCR image was rebuilt and promoted only to local `household:local`. Compose recreated App and Backup on 127.0.0.1:8100. App healthy, readiness HTTP 200, Backup running; the existing database container, named volumes and environment file were unchanged. Served app.css/settings-shell.css/icon.svg hashes matched current source and manifest colors matched the new design. Image: sha256:56331acc23fce2dfb7ab17e574826f390cab607f410d6d9b939ce70b83df515e. Evidence: outputs/warm-midnight/local-docker.json.

Cleanup: restored the pre-existing test database from its verified archive, verified barcode structural readiness, removed both owned review containers and task image tags, and removed the private cross-build directory. Retained the verified private backup, ignored test evidence and one local rollback image (`household:warm-rollback-946c8ea95202`). No sibling containers, dependencies or shared build caches were pruned. Evidence: outputs/warm-midnight/cleanup.json. No commit, push, PR or remote release was performed.
