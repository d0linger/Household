# Module interface critique and polish

Scope: the production Household interface, all nine modules, shared forms/dialogs and public/offline surfaces. Preserve Ocean Light/Dark and Pink Light/Dark. Alternative design-gallery concepts are unchanged.

This pass follows the completed shared-interface work in [interface-refinement.md](interface-refinement.md). It examines deeper module workflows rather than reopening that completed checklist.

## Checklist

- [x] Read product, design and implementation constraints; load Impeccable critique/polish guidance.
- [x] Start an isolated synthetic review application with bounded resources.
- [x] Inventory 54 production templates and registered module routes.
- [x] Complete independent visual assessment A and technical/browser assessment B.
- [x] Record the critique, prioritized corrections and page-by-page coverage.
- [x] Implement the selected corrections without changing the established design identity.
- [x] Run appropriate behavior, template and browser regression checks after implementation.
- [x] Inspect mobile, intermediate and desktop layouts, all four palettes and keyboard/error states.
- [x] Resolve material verification findings and complete independent finish review.
- [x] Update design documentation and record executed checks and limitations.
- [x] Remove task-owned review processes, database and temporary credentials.
- [x] Rebuild/recreate local development Docker on port 8100, retaining volumes and secrets.
- [x] Verify health and deployed assets, then finish the checklist.

## Follow-up: existing series and long tags

The two observations retained below are resolved in this separately authorized correction batch. Chores implementation and verification completed before Recipe layout changed. Evidence is recorded under `outputs/open-points-fix/`; earlier failed reports remain historical evidence.

- [x] Reproduce the existing-series failure from retained evidence and verify the effective-version invariant independently.
- [x] Start a fresh, bounded synthetic application/PostgreSQL fixture, isolated from the development stack.
- [x] Correct one-time series materialization without weakening the exact-version database guard or changing historical occurrences.
- [x] Verify Chores regression, integration and real existing-series browser saves.
- [x] Keep long Recipe tags readable while preserving the remove button at narrow widths.
- [x] Verify Recipe tag wrapping, removal and keyboard behavior across all four palettes.
- [x] Run final affected and shared checks; record results and update documentation.
- [x] Remove the task-owned fixture, processes and credentials.
- [x] Rebuild/recreate the local Docker stack, retain volumes and verify deployed health/assets.

### Correction and verification evidence

The Chores failure came from the one-time scheduler using the newest reviewed version for a due time before that version's effective instant. One-time calendar rules now use the same effective version windows as repeating rules, with bounded 90-day version reads and single-date projection for explicit historical tasks. Preview removes slots outside the new version/materialization window. Existing occurrence snapshots, checklists and completions remain immutable; the exact PostgreSQL guard remains unchanged. A newly exercised worker-generation path also exposed UUID/text ambiguity in the outbox insert, fixed with consistent UUID casts. No migration or dependency was added.

Recipe tag rows now wrap, long names remain visible, and remove buttons retain their width and one-line text. Six changed-surface captures were visually inspected: all four palettes at 320px, plus Ocean Light and Pink Dark at 1440px. No replacement design or palette changes were introduced.

| Executed check | Result |
|---|---|
| Chores integration, including HTTP | 23 top-level tests / 40 passing test entries, zero failures or skips. Includes old/current/completed/future one-time edits, supersession, exact trigger rejection, replay/conflicts, 400-version backlog, worker events, permissions and lifecycle behavior. |
| Focused Go units | 12 top-level tests / 34 passing entries; no failures or skips. |
| Existing-series browser regression | 102 checks, no runtime errors; real HTTP 200 update of the same series, new immutable version and unchanged historical occurrence, plus draft/race protections. |
| Broader Chores browser | 75 checks, 16 captures and 16 clean Axe scans. |
| Recipe browser | 105 checks, 12 captures and eight clean Axe scans; includes four themes × four widths, keyboard removal/refocus and real 80-character tag round trip. |
| Shared module browser | 214 checks, eight captures, one clean Axe scan, zero failures. |
| Complete ordinary source suites | Go: 359 top-level tests / 1,871 passing test entries, zero failures or skips. Node: all 352 tests in the 30 registered files passed, concurrency bounded to two. |
| Go static quality | Formatting, Vet, Golangci-lint, Gosec, Govulncheck and module verification passed on the final Go sources. Gosec found no issues; Govulncheck found no reachable/imported-package vulnerabilities and reported module-only GO-2026-5932 in unused `x/crypto/openpgp`. |
| Repository policy | 108 migration checksums, workflow policy, 180/180 task-list consistency and index artifact policy passed. |
| Fixture cleanup | 793 source files and the binary match the tested build. Owned app/database and temporary credentials removed; ports 8101/55434 released; protected live resources unchanged during cleanup. |
| Local Docker delivery | Image rebuilt; app/database healthy; migration exit 0. Named PostgreSQL/uploads volumes and all 11 existing secret files preserved. Four public HTTP endpoints return 200; 13 embedded assets match source bytes; all four mobile login palettes pass with no overflow or runtime errors. Task-owned browser processes exited; preview 8102 unchanged. |

Evidence: `chores-final-validation.json`, `chores-integration-final.log`, `chores-persist.json`, `chores-workspace/browser-report.json`, `recipe/browser-report.json`, `acceptance/browser-module-polish-report.json`, `quality/result.json`, `policy-checks.json`, `chores-review.md` and `recipe-review.md` under `outputs/open-points-fix/`. Static Go checks preceded the final tag-only CSS change; the actual browser build includes that CSS correction.

Initial failures remain preserved: the disposable database first needed a larger lock budget for schema-reset tests; the baseline reproduced the version mismatch and dropped backlog; a worker-generation test exposed the outbox cast issue. The first new browser harness attempted to read a discarded POST response body after navigation. It now checks identity through a fresh API read, retaining real HTTP status, history and reload assertions. These failed attempts are not counted as passes. Resource inspection recorded no OOM events or memory pressure. Tests use synthetic data; real household data and sibling applications are excluded.

Both fixes are available at **http://localhost:8100**. Final delivery evidence is under `outputs/open-points-fix/deployment/`, including `deployment.json`, `verification.json`, `secrets-preservation.json` and `final-resource-check.json`. No remote publication or Git push occurred. Existing PWA installations may offer **Jetzt laden** for the updated embedded assets.

## Earlier pass evidence boundary

Ignored evidence lives under `outputs/deep-polish-review/`. The baseline has nine enabled modules and synthetic records for Shopping, Recipes, Fuel and Meters. Finance, Chores, Pantry, Documents and Meal Planning initially show empty states. The 109 successful authenticated GETs in `fixture-route-inventory.json` establish route availability only; they do not certify rendering or every business workflow.

No remote publication or Git push is part of this task. Live household records are excluded from mutation tests.

## Critique before correction

Method: dual-agent (A: `/root/deep_critique_a`; B: `/root/deep_critique_b`). A completed before B findings entered synthesis. The established Ocean/Pink identity is appropriate; task density and responsive composition need correction, not a replacement design.

| Nielsen heuristic | Score / 4 | Evidence |
|---|---:|---|
| System status | 3 | Documents can retain a loading message after an empty response. |
| Real-world language | 3 | Familiar household terms; some technical detail remains prominent. |
| User control | 3 | Deliberate review and preserved drafts; Chores lacks a navigation guard. |
| Consistency | 2 | Mobile action labels collapse into vertical strips. |
| Error prevention | 3 | Strong confirmation patterns; Recipe omits the valid litre option. |
| Recognition | 2 | Long forms and offscreen report values require searching. |
| Efficiency | 2 | Secondary controls precede useful results. |
| Minimalism | 2 | Inactive recurrence controls and filter management add visual load. |
| Recovery | 3 | Existing conflict handling is explicit; report retry can improve. |
| Help | 3 | Useful explanations, sometimes overly prominent. |
| **Total** | **26 / 40** | Deeper workflow scope; not a like-for-like comparison with the preceding shell review. |

Strengths: coherent four-palette identity; exact amounts and explicit review; labelled native controls and honest unknown/offline states.

### Prioritized corrections

1. **P1 — Preserve recipe units.** The editor omitted `l`; an intercepted save of an existing litre ingredient sent an empty unit. Add the valid option and test an exact-value saved round trip.
2. **P1 — Restore readable responsive composition.** Six Recipe links become narrow vertical text strips at 390px. Documents review is 826px wide in a 768px viewport because its two-column minimum ignores the sidebar. Correct shared action wrapping, search sizing and the Documents breakpoint.
3. **P2 — Make long editors safe and navigable.** Add native Recipe section/save jumps. Guard unsaved Chores changes during navigation, retaining the verified-save path and invalidating stale previews after edits.
4. **P2 — Put daily work ahead of optional controls.** Disclose Documents filter/view management and entry-list Finance analysis. Hide zero selections and irrelevant Chores recurrence/assignment groups; fix stale empty Documents status without changing submitted semantics.
5. **P2 — Improve accessible controls and reports.** Separate Finance lookup tools from select labels and correct its invalid ARIA group. Add visible overflow-only table guidance, keyboard access for Meter tables, report retry and clear action spacing.

Recommended command: the requested `polish`, with focused `harden` behavior for unit preservation, draft protection and recovery. Questions skipped: the user already authorized critique plus application-wide polish, preserving the selected design and continuing without another priority approval.

The distracted mobile user is most affected by the unreadable Recipe actions and distant save control. First-time members encounter inactive recurrence choices and stale loading status. Keyboard/screen-reader users encounter polluted Finance select names and one invalid ARIA attribute. Optional metadata remains available; no business capability is removed to reduce density.

### Coverage and detector limits

A inventoried all 54 templates (`inventory.json`) and visually inspected eight fresh routes. B recorded 218 visits/states across 67 routes, three widths, all nine modules, four-theme samples and 29 main/supplemental Axe scans. One serious Finance ARIA violation was confirmed. The single template detector run emitted 56 findings: 54 unresolved-color advisories, one flat-type warning and one hidden-image warning. Rendered styles and the receipt-preview lifecycle demonstrate template-resolution/hidden-preview false positives. Native browser limitations required bounded headless Edge inspection; the nonce-only CSP was preserved and no visible detector overlay is claimed.

This covers production views and selected editors/dialogs, not every populated business state, physical camera/OCR, assistive technology or browser engine. The exact page-by-page inventory and initial failures remain in ignored evidence; route availability is not treated as functional certification.

## Executed verification

| Check | Recorded result |
|---|---|
| Ordinary Go suite | All packages passed, serial package execution with two Go CPUs. |
| Node suite | 352/352 passed. Initial sandbox run could not create a temporary junction used by a filesystem-safety test; the approved rerun passed unchanged. |
| Migration/workflow policy | 108 checksums verified; workflow policy passed. |
| Recipe browser regression | 44 checks and four clean Axe scans; includes an existing litre ingredient saved with unchanged exact quantity, section-link focus, conflict/draft handling. |
| Initial Chores persistence regression | 68 checks passed, including normalized previews, hidden-value retention, dynamic/stale-preview invalidation, real navigation cancellation and verified save without a false prompt. |
| Initial combined acceptance | 148 checks passed; one 768px search-label wrap failed and caused the first theme loop to stop. Initial evidence retained. This is not a complete passing matrix. |
| Final combined acceptance | 214 checks passed, eight captures inspected, Finance editor Axe clean, no runtime errors or attempted external/persistent mutation requests. Four palettes × four widths for Recipe actions and generic search; additional focused module cases are listed in the report. |
| Final Chores persistence regression | 81 checks passed with no page errors. Includes real accepted delayed creation, restoring the loaded baseline while the response is pending, dismissing navigation, and independently verifying saved server content versus retained local edits. |
| Final source suites | 352/352 Node and all ordinary Go packages passed again after the final application correction. |
| Independent finish review | Ship for the scoped polish; no remaining must-fix findings within its correction contract. |
| Design documentation | DESIGN.md and sidecar validated; all 48 palette values retained. |
| Review cleanup | 791 application source files match the review binary; owned app/database and temporary credentials removed, ports 8101/55434 released, protected resources unchanged. |
| Local development Docker | Rebuilt/recreated app, database and one-shot migration service; app/database healthy, migration exit 0. PostgreSQL/uploads named volumes and existing secrets retained. |
| Deployed verification | Four public endpoints return 200; 13 embedded assets match source bytes; all four mobile login palettes pass without overflow or runtime errors. |

The correction batch moved search sizing to every viewport. Independent source review identified a pending-save edge case: if a user restores the original value while a changed value is being saved, navigation must compare the current draft with the newly confirmed saved state. The baseline now advances only after the server confirms the save. The dedicated delayed-response regression passed against the final binary.

Evidence files include `node-tests.txt`, `node-tests-sandbox.txt`, `go-tests.txt`, `recipe/browser-report.json`, `chores-persist.json`, `acceptance/browser-module-polish-report.json`, `acceptance/browser-module-polish-initial-failure.json`, `acceptance/acceptance-notes.md`, `finish-verdict.md`, `build-source-verification.json` and `stack-cleanup.json`.

### Separate observations retained from the earlier pass

The existing-series and long-tag findings below were open at that earlier checkpoint. Both are now resolved and tested in the [follow-up correction batch](#follow-up-existing-series-and-long-tags) above; the original failed evidence is retained.

- A second save to an existing one-time synthetic Chores series returned HTTP 422. The isolated PostgreSQL log identified `chores.capture_occurrence()` rejecting a managed occurrence whose reviewed version was not the exact effective version. This is a separate existing-series workflow finding; its validity boundary and remediation were not resolved in this UI pass. No trigger was weakened and that update is not claimed as passed. The delayed-response protection was instead verified through an accepted new-series save, which exercises the same callback and exact original-baseline race. Failed interception/HTTP422 logs and earlier reports remain retained.
- The first delayed-save harness did not await interception cleanup when the API rejected the request, masking the HTTP422 with a route-already-handled error. Its cleanup now awaits the handler and preserves the actual API outcome. Process inspection confirmed no leftover temporary headless Edge processes.
- At 320px, an unusually long Recipe tag can still make its remove button wrap across three short lines. It remains usable; this minor visual observation is recorded outside the selected correction batch.
- The Meters workspace keyboard-table check used an explicitly reported synthetic GET response because the fixture's current period had no populated table. It proves rendering and keyboard behavior, not stored metering calculations. Recipe and Chores persistence checks used real synthetic API writes.

### Local delivery

The earlier reviewed UI was delivered at **http://localhost:8100**. Its deployment evidence is in `outputs/deep-polish-review/deployment/` (`build.log`, `recreate.log`, `deployment.json`, `verification.json`). Existing PWA installations may offer **Jetzt laden** for the new worker. No new dependencies, schema changes, remote deployment, image publication or Git push were introduced by that pass. The separate existing-series finding was still open at that checkpoint and is addressed by the follow-up above; the original polish checklist did not certify that workflow or every household behavior.

## Page-by-page source checklist

Every checkbox below means the template was source-reviewed. Rendered coverage is stated separately; it does not imply that every conditional or populated state was exercised. The baseline findings map to the corrections above.

| Reviewed | Area | Template | Rendered evidence | Baseline finding |
|---|---|---|---|---|
| [x] | Core | [page.html](../internal/web/templates/page.html) | prior+public | A1 |
| [x] | Core | [module.html](../internal/web/templates/module.html) | fresh+prior | A1 |
| [x] | Core | [related.html](../internal/web/templates/related.html) | fresh+prior | A5 |
| [x] | Core | [settings.html](../internal/web/templates/settings.html) | prior | — |
| [x] | Core | [audit.html](../internal/web/templates/audit.html) | not_observed | — |
| [x] | Finance | [finance-workspace.html](../internal/web/templates/finance-workspace.html) | fresh | A3 |
| [x] | Finance | [expense-editor.html](../internal/web/templates/expense-editor.html) | not_observed | — |
| [x] | Documents | [documents-workspace.html](../internal/web/templates/documents-workspace.html) | fresh | A3 |
| [x] | Chores | [chores-workspace.html](../internal/web/templates/chores-workspace.html) | fresh | A4 |
| [x] | Meters | [meters-workspace.html](../internal/web/templates/meters-workspace.html) | not_observed | A5 |
| [x] | Fuel | [fuel-module.html](../internal/web/templates/fuel-module.html) | prior | — |
| [x] | Fuel | [fuel-module-overview.html](../internal/web/templates/fuel-module-overview.html) | not_observed | — |
| [x] | Fuel | [fuel-module-capture.html](../internal/web/templates/fuel-module-capture.html) | prior | — |
| [x] | Fuel | [fuel-module-history.html](../internal/web/templates/fuel-module-history.html) | not_observed | — |
| [x] | Fuel | [fuel-module-reports.html](../internal/web/templates/fuel-module-reports.html) | not_observed | — |
| [x] | Fuel | [fuel-module-costs.html](../internal/web/templates/fuel-module-costs.html) | not_observed | — |
| [x] | Fuel | [fuel-module-maintenance.html](../internal/web/templates/fuel-module-maintenance.html) | not_observed | — |
| [x] | Fuel | [fuel-module-charging.html](../internal/web/templates/fuel-module-charging.html) | not_observed | — |
| [x] | Fuel | [fuel-module-trips.html](../internal/web/templates/fuel-module-trips.html) | not_observed | — |
| [x] | Fuel | [fuel-module-imports.html](../internal/web/templates/fuel-module-imports.html) | not_observed | — |
| [x] | Fuel | [fuel-module-documents.html](../internal/web/templates/fuel-module-documents.html) | not_observed | — |
| [x] | Fuel | [fuel-module-odometer.html](../internal/web/templates/fuel-module-odometer.html) | not_observed | — |
| [x] | Fuel | [fuel-module-settings.html](../internal/web/templates/fuel-module-settings.html) | not_observed | — |
| [x] | Fuel | [fuel-overview.html](../internal/web/templates/fuel-overview.html) | not_observed | — |
| [x] | Recipes | [recipe-workspace.html](../internal/web/templates/recipe-workspace.html) | fresh+prior | A1, A2 |
| [x] | Recipes | [recipe-find.html](../internal/web/templates/recipe-find.html) | not_observed | — |
| [x] | Recipes | [recipe-import-review.html](../internal/web/templates/recipe-import-review.html) | not_observed | — |
| [x] | Recipes | [recipe-insights.html](../internal/web/templates/recipe-insights.html) | not_observed | — |
| [x] | Recipes | [recipe-offline.html](../internal/web/templates/recipe-offline.html) | prior | — |
| [x] | Recipes | [cooking.html](../internal/web/templates/cooking.html) | not_observed | — |
| [x] | Recipes | [cooking-stock.html](../internal/web/templates/cooking-stock.html) | not_observed | — |
| [x] | Recipes | [ingredient-pantry.html](../internal/web/templates/ingredient-pantry.html) | not_observed | — |
| [x] | Pantry | [pantry.html](../internal/web/templates/pantry.html) | fresh | — |
| [x] | Pantry | [pantry-daily.html](../internal/web/templates/pantry-daily.html) | not_observed | — |
| [x] | Pantry | [pantry-waste.html](../internal/web/templates/pantry-waste.html) | prior | — |
| [x] | Pantry | [pantry-batches.html](../internal/web/templates/pantry-batches.html) | not_observed | — |
| [x] | Pantry | [pantry-leftovers.html](../internal/web/templates/pantry-leftovers.html) | not_observed | — |
| [x] | Pantry | [pantry-food-facts.html](../internal/web/templates/pantry-food-facts.html) | not_observed | — |
| [x] | Pantry | [pantry-scans.html](../internal/web/templates/pantry-scans.html) | not_observed | — |
| [x] | Pantry | [pantry-replenishment.html](../internal/web/templates/pantry-replenishment.html) | not_observed | — |
| [x] | Pantry | [pantry-suggestions.html](../internal/web/templates/pantry-suggestions.html) | not_observed | — |
| [x] | Mealplan | [planning.html](../internal/web/templates/planning.html) | not_observed | — |
| [x] | Mealplan | [planning-autoplan.html](../internal/web/templates/planning-autoplan.html) | not_observed | — |
| [x] | Mealplan | [planning-budget.html](../internal/web/templates/planning-budget.html) | not_observed | — |
| [x] | Mealplan | [planning-calendar.html](../internal/web/templates/planning-calendar.html) | not_observed | — |
| [x] | Mealplan | [planning-leftovers.html](../internal/web/templates/planning-leftovers.html) | not_observed | — |
| [x] | Mealplan | [planning-suggestions.html](../internal/web/templates/planning-suggestions.html) | not_observed | — |
| [x] | Mealplan | [planning-demand.html](../internal/web/templates/planning-demand.html) | not_observed | — |
| [x] | Mealplan | [planning-purchases.html](../internal/web/templates/planning-purchases.html) | not_observed | — |
| [x] | Mealplan | [reservations.html](../internal/web/templates/reservations.html) | not_observed | — |
| [x] | Shopping | [shopping.html](../internal/web/templates/shopping.html) | prior | — |
| [x] | Shopping | [shopping-checkout.html](../internal/web/templates/shopping-checkout.html) | not_observed | — |
| [x] | Shopping | [shopping_barcode.html](../internal/web/templates/shopping_barcode.html) | not_observed | — |
| [x] | Shopping | [shopping-offline.html](../internal/web/templates/shopping-offline.html) | prior | — |
