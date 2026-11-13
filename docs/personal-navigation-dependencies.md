# Personal module order and dependency review — 3 November 2026

Approved scope: account-specific module order for desktop/mobile navigation; a single settings-menu trigger at the far upper right; logout only in its tray; remove module destinations from that tray. Validate every currently open Household PR and update suitable dependencies. Preserve data, role boundaries, Ocean/Pink, German/English and the existing dirty working tree.

- [x] Persist and validate a personal module order without changing shared module activation.
- [x] Provide accessible reorder, save, reset, failure and retry controls under General.
- [x] Use personal order in the desktop sidebar and horizontally scrollable mobile rail.
- [x] Keep all active modules available on mobile without duplicating them in the tray.
- [x] Keep a single upper-right settings-menu opener and tray-only logout.
- [x] Verify account isolation, unknown/duplicate IDs, sparse preference updates and disabled-module positions.
- [x] Verify migration/upgrade compatibility, audit readiness and backup/restore before local upgrade.
- [x] Inventory all open PRs with exact head SHAs and inspect changes/check results.
- [x] Apply appropriate dependency and immutable action/container pin updates.
- [x] Run relevant Go, Node, database, browser, security and workflow checks.
- [x] Update product/design/operator documentation and actual execution evidence.
- [x] Remove only owned test resources and refresh the local Docker stack.
- [x] Verify deployed health, current assets and preserved volumes/secrets.

The first real PostgreSQL campaign exposed the generic audit writer rejecting the new module-order array; its original failed evidence is retained. Core37 now admits only a bounded, validated module-order array and preserves the rejection of other structured audit values. The repeated integration build passed all 21 selected test mains (17 use real PostgreSQL), with no failures or skips. This includes exact audit values, account isolation, concurrent partial updates, malformed array rejection, populated/idempotent upgrade, tampered-predecessor refusal and the selected authentication/security regressions.

Accepted checks: 377 ordinary Go test mains, 367 Node tests, all six Go formatting/lint/security/module checks, 110 migration checksums, workflow policy, Actionlint and authored localization coverage pass. The real PDF/OCR preview/split/merge integration test passes in the optional OCR runtime. Browser acceptance passes 725 assertions and 49 clean Axe scans (general, navigation, security, Shopping catalog and Shopping offline/extensions), with no JavaScript errors or external frontend requests. Desktop/mobile visual review and the repeated navigation capture check pass. See [implementation evidence](implementation.md#personal-module-navigation-and-dependency-review--3-november-2026) for precise scope and limitations.

The encrypted pre-upgrade backup was refreshed immediately before deployment and actually restored/checked in an isolated database. The owned test process/database, binaries, uploads, credentials, scanner containers, image exports and scratch files have been removed. The verified Trivy database cache remains reusable. Trivy 0.76.1 found zero fixable HIGH/CRITICAL vulnerabilities in the final image; this does not claim all severities or unfixed findings are absent.

Local delivery completed on **3 November 2026 at 09:07:55 Europe/Vienna** at `http://localhost:8100`. App/database are healthy, migration exited zero, Core37 and all 163 business audit policies are ready, and named volumes plus secret mounts/contents are preserved. Deployed verification passed 25 public HTTP checks, 13 exact source-to-embedded-asset comparisons and 16 language/appearance/system combinations. The deployment used exactly the scanned image index `sha256:5642b43df87cb7e0bb0be23336af452aaa2e567591d9d96f80581e4568862342`.

All ten open PRs were inspected and their heads reconfirmed at 07:51:10 UTC; none is claimed merge-ready. Local acceptance does not replace a fresh hosted CI run, and the complete current Fuel campaign remains outside this focused increment. The exact review and locally adopted updates are in [the PR/dependency report](pr-dependency-review-2026-11-03.md).

No PR merge, remote publication or Git push is implied. Evidence is recorded under ignored `outputs/personal-navigation-dependencies/`; unchecked items are not yet verified.
