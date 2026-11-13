# Settings navigation tray — 3 November 2026

Requested follow-up: reach each settings area directly from the side navigation tray, matching the supplied Parkrr reference. Remove the horizontal settings tabs and reduce the visual weight of the language/appearance controls while preserving Ocean/Pink, role boundaries and accessible targets.

- [x] Move General, Modules, Security, Users and Audit into individual drawer destinations.
- [x] Keep administrator-only links role-aware and mark the current destination.
- [x] Add shared header/sidebar entry points; mobile More opens the same side tray.
- [x] Show account identity, grouped module/settings links and installation/logout actions.
- [x] Replace prominent appearance/language controls with a quiet icon and DE/EN shorthand.
- [x] Preserve 44px targets, accessible names, Escape/focus restoration, backdrop dismissal, bounded scrolling and reduced motion.
- [x] Verify compiled templates, localization catalogs and applicable regression tests.
- [x] Verify real browser navigation, permissions, keyboard, themes, responsive layout and installation/logout behavior.
- [x] Update design/product/operator documentation and record actual evidence.
- [x] Remove owned review resources and rebuild/recreate the local Docker application.
- [x] Verify deployed assets and health while preserving volumes and secrets.

Evidence is recorded under ignored `outputs/settings-navigation-tray/`. No schema or authentication-policy changes were made. The full ordinary Go suite (375 test mains), all 366 registered Node tests, the two focused PostgreSQL language/settings contracts and all 6,204 authored template/JavaScript localization keys passed. The existing general browser component passed 113 assertions. The final tray browser component passed 293 assertions and eight clean Axe scans across German/English, four palettes, desktop/tablet/mobile and short landscape viewports. It verifies all five direct settings routes, all nine enabled module entries, sidebar/header access, current location, quiet 44px controls, actual language POSTs and session revocation through tray logout. Installation uses an explicitly synthetic platform event to verify its prompt wiring; it does not claim a real OS install.

The test initially assumed Chromium's reverse-Tab boundary immediately returns to the last modal control. Chromium can first visit browser chrome; the corrected check permits that native browser step, rejects focus on background application controls and then requires the tray logout action. Escape verification waits for the native asynchronous close event before checking restored focus and `aria-expanded`. Both initial failures are retained. Visual review of the first complete desktop/mobile batch requested left-aligned session actions; the final CSS correction was confirmed by the repeated full Go/Node and tray checks. The earlier PostgreSQL/general-browser passes remain valid for the unchanged server behavior; they were not rerun for that alignment-only change.

The final security regression passed 137 assertions and 24 clean Axe scans, including reader/admin boundaries, stale-authentication user creation, virtual-authenticator passkeys, TOTP/session actions, localization and all six appearance selections. Physical security keys/biometrics were not tested. Its report remains separately preserved under `security/`, without replacing the earlier settings increment's evidence.

The task-owned native application, PostgreSQL container, temporary binaries, synthetic uploads and credentials were removed; ports 8101/55434 were released. Source hashes matched the final tested binary, and the existing local services were unchanged during cleanup. Docker delivery then completed on 3 November at 08:05 Europe/Vienna: the image was rebuilt and the Household app/database/migration services recreated. Both long-running services are healthy, migration exited zero, unchanged Core36 and all 163 business audit policies are ready, and named volumes plus secret mounts/contents were preserved. Public verification passed 24 HTTP checks, exact comparison of 12 embedded assets and 16 language/appearance/system combinations. Proof: `deployment/deployment.json`, `deployment/public-verification.json`, `stack-cleanup.json` and `build-source-verification.json`. No hosted CI, full database-suite rerun, image publication or Git push is claimed.
