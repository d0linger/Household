# Shared expense editor — incumbent design verification

EX02 extends the existing Expenses module with one shared editor for a household entry and its category lines. This is the separate comparison with the incumbent interface, covering the exact remainder, draft/post distinction, saved facts and recovery states. No new visual world, font, raster asset or frontend dependency is introduced. Selected financial visibility belongs to EX19 and remains unavailable; the editor explicitly states that the booking is visible to the household.

## Scope and evidence ownership

This pass read [PRODUCT.md](../PRODUCT.md), [DESIGN.md](../DESIGN.md), the [surface brief](../.impeccable/surfaces/internal-web-templates-expense-editor-html.md), the [editor template](../internal/web/templates/expense-editor.html), [editor CSS](../internal/web/static/expense-editor.css), [editor JavaScript](../internal/web/static/expense-editor.js), [exact-value helper](../internal/web/static/expense-values.js), [shared CSS](../internal/web/static/app.css), relevant [shared JavaScript](../internal/web/static/app.js), the [browser script](../scripts/browser-expense-editor-check.cjs) and the evidence linked below. All ten current images were opened as images and compared with the source. This document is the only file written by the documentation pass.

Browser, accessibility, database and finish-review results are inherited evidence, not executions by this documenter. The [current editor report](../outputs/expense-editor-review/browser-report.json) records 43 checks, ten axe scans with no violations, ten captures, no JavaScript errors and no external requests. The [final complete-chain log](../outputs/expansion-review/finance-editor-browser-f1-final.log) records 846 checks across 24 components. Clean axe scans do not establish complete WCAG conformance.

The coordinator reports that the fresh independent finish reviewer accepted EX02-F1 as resolved, with a ship disposition at correction-only scope. The correction makes legacy entry updates open their exact existing entry and category drafts open their own maintenance surface. This comparison does not enlarge that verdict into another full finish review. Final complete native regression, meaningful scoped Linux race and current runtime/helper encrypted recovery acceptance remain pending at this handoff. Operational acceptance belongs in the [feature verification ledger](expense-editor-verification.md) and [implementation ledger](implementation.md); no deployment, authenticated restored-service replay or publication is certified here.

## Preserved authority and inherited drift

`DESIGN.md` remains byte-for-byte unchanged, with SHA-256 `07190617EB9C9FB382823283517BAA89B8BA7B877CA7ADE713F9B449FCBA4522`. `.impeccable/design.json` is absent and was not created.

The incumbent design document has a compact legacy structure rather than the current documentation reference's canonical sections and token frontmatter. The missing sidecar and that format difference are inherited documentation/tooling drift, left unrepaired for this ordinary extension. The shared shell also retains its small uppercase sidebar eyebrow; it is inherited craft drift, not a new rule for future surfaces. Actual inputs retain the existing 9px radius while the concise design prose calls for 10px controls. These details are reported without changing the system or legitimizing a defect as a reusable token.

## Comparison with the incumbent system

| Aspect | Source and capture comparison |
|---|---|
| Palette | Shared light ground/surface/ink (`#F2F4F1`, `#FFFFFF`, `#14201C`) and dark equivalents (`#0E1513`, `#16201D`, `#E6EDE9`) match the incumbent document. Forest/sage accents (`#0E6B57` / `#79D5BD`), tonal notices, quiet borders and the existing danger colors carry the editor states. There is no expense-specific palette. |
| Typography | The local `ui-sans-serif, system-ui, -apple-system, Segoe UI, sans-serif` stack is preserved. Source hierarchy uses the shared responsive heading (28–40px; 30px on mobile), 21px section headings, 17px row headings, 16px body/inputs, 13px labels and 12px helper text. Totals use 19px semibold tabular numbers. This is an observation of the build, not a new global type scale. |
| Material and shape | The existing bordered panel, restrained shared shadow and rounded controls remain. Category lines use horizontal dividers rather than nested cards. Shared buttons retain 10px corners and native fields retain the incumbent 9px corners; the shared panel uses 16px corners, reduced to 13px on mobile. |
| Desktop hierarchy | The fixed sidebar, top bar and bounded main area are preserved. The editor is bounded to 1050px. The back link, title, saved status and household-sharing sentence precede the form. Description spans the form; paired date/kind and amount/payee fields use the shared two-column grid. Category lines precede the in-flow total/allocation/remainder row and explicit actions. |
| Mobile hierarchy | At 390px, header and fixed bottom module navigation remain. Native labels and fields stack, long identities wrap, row actions wrap, and totals become label/value rows. Posting review and recovery notices stay in the same flow. The inherited browser check reports no horizontal overflow. Focused captures intentionally show the relevant scrolled region, not the entire editor at once. |
| Controls and focus | Shared buttons have at least 44px height, fields at least 46px, and the existing 3px accent focus outline. The empty desktop capture visibly retains focus on the category action. The browser report records keyboard order, category-dialog Escape focus return and posting-cancel focus return. Source focuses alerts, review, accepted status and new/reordered row amounts; this pass did not repeat those interactions. |
| Motion | The source keeps shared color transitions and the native dialog's short entrance animation conditional on reduced-motion preference. No new editor motion is introduced. This documenter did not execute a motion check. |

No new mismatch with the incumbent system was observed at this bounded source/image scope. Surface-specific layout and financial workflow are kept in this verification and the surface brief; they are not promoted into new system-wide visual rules.

## State and recovery comparison

The empty editor has a household date without an invented amount, an explicit unknown-payer option and a category-free draft explanation. Payer/category selection reuses the searchable, paged native reference control, retaining selected historical identities. Archived or inactive selections remain labelled and must be explicitly replaced before posting.

The saved draft visibly shows `10,00 €` total, `9,00 €` allocated and `1,00 €` remainder. The exact-value helper uses integer cents through BigInt and rejects values requiring rounding. Signed category amounts are visible in review and saved facts: the inspected posting combines `11,00 €` with `-1,00 €`. Draft save remains distinct from posting review; review freezes the form and asks for a separate final confirmation. Posted and reader surfaces replace editing with labelled saved facts and an immutability explanation. Synthetic markup-shaped description text remains readable text; the report separately records the executable-markup check.

The validation capture has the established danger outline, an actionable field link and retained raw inputs. The conflict capture places fresh server facts above the retained unsaved form and labels the explicit action that discards those inputs. The uncertain-result capture explains that the operation may already be saved, keeps inputs locked and exposes retry beside the notice. Source retains the same in-memory body/key for retry; the inherited report records identical replay data and exactly one resulting entry. No automatic save, local financial form persistence or financial generic-queue submission is introduced.

The current legacy-queue capture shows both an existing-entry draft and a category draft, each retained for manual comparison with explicit discard. Recovery links open separate tabs. Source maps the existing entry ID to `/m/expenses/editor?entry=<ID>` and category identity to its maintenance route. The report records clicking the real entry recovery link, verifying the opened ID, the category destination, absence of automatic transmission and explicit discard. The screenshot alone does not prove those destinations; the source and executed report support that correction.

## Opened current captures

The desktop browser viewport is 1440 × 1000; mobile captures are 390 × 844. Empty, draft and posted desktop captures are full-page. Conflict and legacy-queue desktop captures intentionally retain the 1440 × 1000 focused viewport, avoiding fixed-navigation stitching after scrolling. Timestamped names and financial examples are isolated review fixtures, not production demo data.

| Capture | Observed comparison |
|---|---|
| [Empty, light desktop](../outputs/expense-editor-review/expense-empty-light-desktop.png) | Complete shell and bounded labelled form; unknown payer, no invented amount, empty allocation and disabled posting review. |
| [Draft, light desktop](../outputs/expense-editor-review/expense-draft-light-desktop.png) | Saved status, one divided category row, exact `1,00 €` remainder and separate draft/review actions. |
| [Draft, dark mobile](../outputs/expense-editor-review/expense-draft-dark-mobile.png) | Wrapped category identity, stacked fields/actions and aligned exact totals in the inherited dark palette. |
| [Posting review, light mobile](../outputs/expense-editor-review/expense-post-review-light-mobile.png) | Frozen draft actions, zero remainder, signed lines and distinct continue-editing/final-post decisions. |
| [Posted, light desktop](../outputs/expense-editor-review/expense-posted-light-desktop.png) | Immutable saved facts, explicit household sharing and readable positive/negative allocations. |
| [Validation, dark mobile](../outputs/expense-editor-review/expense-validation-dark-mobile.png) | Named error, field jump link and danger-bordered description alongside retained date and amount. |
| [Conflict, light desktop](../outputs/expense-editor-review/expense-conflict-light-desktop.png) | Fresh current facts and explicit discard/reload action above the retained own description. |
| [Uncertain result, dark mobile](../outputs/expense-editor-review/expense-uncertain-dark-mobile.png) | Prominent retry notice, understandable connection copy and unchanged locked inputs. |
| [Legacy queue, light desktop](../outputs/expense-editor-review/expense-legacy-queue-light-desktop.png) | Manual entry/category comparison, separate recovery destinations and deliberate discard controls. |
| [Reader, light mobile](../outputs/expense-editor-review/expense-reader-light-mobile.png) | Saved labelled facts, wrapped literal description and no editable form. |

This comparison accepts consistency with the established visual system at the stated source/image scope. It leaves pending native, race and recovery gates open and grants no publication authority.
