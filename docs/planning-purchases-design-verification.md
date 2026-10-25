# Planning purchases — scoped design verification

Reviewed on 2026-10-25. This separate documentation comparison records the finished MP17 interface against the incumbent Household design system. It covers the current source, all ten saved captures and the supplied independent correction verdict. It does not certify backend, runtime or release acceptance.

## Authority and comparison boundary

The comparison used [PRODUCT.md](../PRODUCT.md), [DESIGN.md](../DESIGN.md), the [implementation ledger](implementation.md) and the [planning-purchases direction contract](../.impeccable/surfaces/internal-web-templates-planning-purchases-html.md). Source inspection covered the [embedded template](../internal/web/templates/planning-purchases.html), [local controller](../internal/web/static/planning-purchases.js), and [shared stylesheet with the appended purchase rules](../internal/web/static/app.css). The adjacent demand and Pantry styles provide incumbent examples of the same bounded review workspace.

The supplied Household artifact and reviewed prototypes remain the recorded visual authority. This is an ordinary extension of the existing green Operate interface. The comparison did not revisit all 50 prototypes, replace the visual world, run another context/detector pass or refresh the design system.

DESIGN.md remains byte-identical with SHA-256 `07190617EB9C9FB382823283517BAA89B8BA7B877CA7ADE713F9B449FCBA4522`. `.impeccable/design.json` was absent and remains absent. This pass writes only this document.

## Comparison with the incumbent system

| Area | Actual source and capture evidence |
|---|---|
| Palette and material | Shared light/dark ground, surface, ink, secondary ink, green accent, accent surface, border, warning and danger values match the DESIGN.md palette. Existing bordered panels and their restrained shared shadow retain the application material. Purchase rules introduce no new palette or shadow. |
| Typography | The existing local system sans-serif remains in use: 16px body and field text, the shared 28–40px main heading, 21px section headings, 17px subsection headings and 12px secondary read-time copy. Purchase checkbox explanations use 16px text. Quantities use tabular numerals; long names and paragraphs wrap. No font or image asset is added. |
| Layout and navigation | Desktop retains the sidebar and bounded main area; mobile retains the compact header and bottom module navigation with More. The purchase workspace is bounded at 960px with paragraphs limited to 75ch. Related planning, shopping and module-management links precede the task heading. Optional whole-meal selection and active scope appear before ingredient comparison and the separate save section. |
| Rows and controls | The surface reuses visible field labels, native dates/selects/checkboxes, shared buttons and focus outlines. Ingredient and source groups are divided by borders, rather than promoted to a new card system. At 600px and below, action groups and destination review stack; primary actions fill the available width. The quantity grid keeps labels beside right-aligned values on mobile. |
| Review and recovery | Source identity, stock coverage, desired purchase, existing attribution, difference, additional need and reviewed effect appear separately. Current-item disclosures expose the actual item, list, quantity, note and state. Explicit confirmation remains distinct from preview and destination review. Ending and detaching explain their different effects on active demand and independent shopping records. |
| State and focus | Loading, uncertain, stale comparison, blocked decision, disabled-module recovery and historical receipt states have explicit text. The controller hides failed comparison output, clears confirmation on invalidation, freezes competing mutations after an uncertain response, and preserves the original request/key for retry. Review headings, errors and receipts receive focus; successful destination review enables controls before focusing confirmation. |

The controller uses text nodes for source and item content. The synthetic HTML-like ingredient remains literal in the desktop captures. Exact quantity formatting preserves approximate/subprecision explanations and rational values when supplied; these purchase captures themselves show decimal quantities, so they do not visually establish every fraction case.

The shared control target and focus rules continue here; this comparison does not assert that every disclosure, textual link or shell element has been separately measured. The screenshots show task states at selected scroll positions. Fixed mobile navigation and content extending below a capture are part of those bounded views, rather than evidence that every control is simultaneously visible.

## Captures inspected

All ten image paths in the current [browser report](../outputs/planning-purchases-review/browser-report.json) were opened in this pass. Desktop captures are full-page at 1440px wide; mobile captures are 390×844. These are synthetic verification fixtures under `outputs/`, not shipped raster assets, and introduce no shipping-asset provenance obligation.

| Capture | Evidence compared |
|---|---|
| [Initial light desktop](../outputs/planning-purchases-review/purchases-initial-light-desktop.png) | Whole-meal selection, empty active scope, literal ingredient text, 1.2kg total demand against 1kg stock, and the resulting 0.2kg purchase. Review precedes explicit saving. |
| [Destination review, light mobile](../outputs/planning-purchases-review/purchases-confirm-light-mobile.png) | Labelled searchable destination, named reviewed target, effect counts and stock-booking explanation fit the narrow layout. |
| [Confirmation action, light mobile](../outputs/planning-purchases-review/purchases-confirm-action-light-mobile.png) | Full confirmation text and save action remain separate; unchecked confirmation leaves saving disabled. |
| [Uncertain response, dark mobile](../outputs/planning-purchases-review/purchases-uncertain-dark-mobile.png) | Lost-response explanation, disabled fresh refresh/week actions and enabled same-operation retry. |
| [Stale comparison error, dark mobile](../outputs/planning-purchases-review/purchases-stale-error-dark-mobile.png) | Clear stale status and failed-comparison alert with refresh available; report assertions establish that old preview quantities and confirmation are hidden. |
| [Current-item adoption, dark mobile](../outputs/planning-purchases-review/purchases-adopt-dark-mobile.png) | Attributable 0.2kg purchase is distinct from zero difference/additional need; existing-item disclosure is available. |
| [Adoption decision, dark mobile](../outputs/planning-purchases-review/purchases-adopt-decision-dark-mobile.png) | Actual manually edited item, 0.2kg quantity, retained note and version/state precede the explicit adoption decision. |
| [Detach review, light mobile](../outputs/planning-purchases-review/purchases-detach-light-mobile.png) | Meal, ingredient count, actual item, present list and 0.2kg quantity precede the explicit detach action and preservation explanation. |
| [Active scope, light desktop](../outputs/planning-purchases-review/purchases-active-light-desktop.png) | One active meal/two requirements remain visible; current attributed purchase and zero additional demand are separated from the labelled historical operation. |
| [Disabled-module recovery, dark mobile](../outputs/planning-purchases-review/purchases-disabled-recovery-dark-mobile.png) | Generic stored-source identity, continued whole-meal ending and disabled new comparison preserve recovery while private source/item labels are withheld. |

## Independent finish disposition and recorded browser evidence

The fresh independent finish review preserved the established visual world and requested four corrections. The same reviewer's correction-only verdict, supplied to this documentation pass, records **PP-F01 resolved, PP-F02 resolved, PP-F03 resolved and PP-F04 resolved**, with disposition **ship at that correction scope**. The initial wider review remains preserved; this verdict does not reopen or extend it.

| Finding | Corrected behavior supported by source and saved evidence |
|---|---|
| PP-F01 — actual note | Existing-item review displays the actual note, including an explicit empty-note message; the browser report checks both empty and manually edited notes before adoption and retention afterward. |
| PP-F02 — detach identity | Detach review names the actual item, current list and quantity before committing. The screenshot establishes the shown review; the report asserts these identities. Actual moved-list database correctness is outside this design comparison. |
| PP-F03 — stale preview | Failed comparison invalidation clears the preview reference, hides old quantities and hides saving. The browser report checks that stale verified quantities and confirmation are unavailable. |
| PP-F04 — confirmation focus | Successful destination review clears busy state and re-enables controls before focusing the confirmation checkbox. The browser report explicitly asserts focus on that enabled control and continued need for explicit confirmation. |

The current saved purchase report passes **42 checks**, with **ten axe scans without reported violations**, no purchase JavaScript errors and no external purchase requests. Its behavioral assertions include no preview registration, shared-stock subtraction, literal rendering, long mobile names, exact uncertain retry, durable replay, attributed purchase quantity, no-op proof, adoption, detach, whole-meal ending/reactivation, historical/current separation and disabled-module recovery. Automated scans of these states are bounded evidence, not complete WCAG or assistive-technology acceptance.

The completed [current 20-component browser campaign](../outputs/expansion-review/planning-purchases-browser-final.log) records **710 checks**, including the 42 purchase checks. The earlier [704-check campaign](../outputs/expansion-review/planning-purchases-browser-chain-final.log) belongs to the preceding purchase candidate; the earlier failed authentication campaign is not counted as a pass. This documenter read saved reports and logs and executed no tests, browser actions or database operations.

## Existing drift and acceptance limits

The incumbent DESIGN.md uses a concise token table and existing headings rather than the document reference's current YAML/canonical-section format. Shared fields use a 9px radius while the contract describes 10px controls. The shared strengthened control border is not distinguished in that token table, and its licensed-icon description differs from the project's existing SVG implementation, as already recorded in the [earlier design comparison](design-review.md). Existing small uppercase sidebar eyebrow text is visible in desktop captures; it is not adopted as a rule for future surfaces. These inherited differences remain reported without system changes or arbitrary repair.

At this documentation handoff, current full native and scoped race campaigns are running and **are not accepted as passed**. Their outcome belongs in the implementation ledger after actual completion. The scoped UI verdict and saved browser campaign do not approve full MP14/MP17 completion, current-image recovery, authenticated restored-service replay, production RTO/RPO, a live 8100 upgrade, hosted Actions, push or publication. No such operation was performed by this documentation pass.
