# Recipe alternatives — incumbent design verification

MP11/MP12 extend the existing green weekly editor under the approved 180-improvement brief. This is the separate incumbent-system comparison for the suggestions disclosure, its results and its existing source-review handoff. It introduces no new visual world, font, raster asset or dependency.

## Scope and evidence ownership

This documentation pass read [PRODUCT.md](../PRODUCT.md), [DESIGN.md](../DESIGN.md), the [surface brief](../.impeccable/surfaces/internal-web-templates-planning-suggestions-html.md), the [feature contract](planning-suggestions.md), the [suggestions template](../internal/web/templates/planning-suggestions.html), the [weekly template](../internal/web/templates/planning.html), [suggestions JavaScript](../internal/web/static/planning-suggestions.js), [planning JavaScript](../internal/web/static/planning.js) and [shared CSS](../internal/web/static/app.css). It opened all ten current images listed below and compared them with that source. The document is the only file written by this pass.

Browser execution, accessibility scans, database checks and finish-review scoring are inherited evidence, not tests performed by this documenter. The [current browser report](../outputs/planning-suggestions-review/browser-report.json) records 38 suggestions checks, ten axe scans with no violations, no JavaScript errors and no external requests. The [final complete-chain log](../outputs/expansion-review/suggestions-browser-chain-final-focus.log) records 778 checks across 22 components. A clean axe result does not by itself establish complete WCAG conformance.

The coordinator reports the same independent finish reviewer accepted the SUG-F01 correction as resolved, with a ship disposition at correction-only scope and no regression observed in that scope. This comparison does not enlarge that verdict into another full finish review. Complete native regression and meaningful scoped Linux race acceptance were still pending at this handoff. Local captures, image builds and synthetic recovery evidence do not establish deployment or authenticated restored-service replay; operational status belongs in the [verification ledger](planning-suggestions-verification.md) and [implementation ledger](implementation.md).

## Preserved authority and inherited drift

`DESIGN.md` remains byte-for-byte unchanged. Its SHA-256 is `07190617EB9C9FB382823283517BAA89B8BA7B877CA7ADE713F9B449FCBA4522`. `.impeccable/design.json` is absent and was not created. Context and detector were already run in the continuing session and were not rerun here.

The existing design document uses a compact legacy structure rather than the current documentation reference's canonical section/token format, and there is no sidecar for its extended component material. These are inherited documentation/tooling drift, reported without repair. The established document, code and rendered weekly workspace remain the visual authority for this extension. No new qualitative direction or system-wide rule is inferred from the suggestions page.

## Comparison with the incumbent system

| Aspect | Incumbent evidence and result |
|---|---|
| Palette and ground | Shared CSS retains the documented light ground/surface/ink (`#F2F4F1`, `#FFFFFF`, `#14201C`) and dark equivalents (`#0E1513`, `#16201D`, `#E6EDE9`). The existing forest/sage accent pair (`#0E6B57` / `#79D5BD`), tonal notice surfaces and quiet dividers carry into suggestions. No feature-specific palette is introduced. |
| Type and amounts | The local `ui-sans-serif, system-ui, -apple-system, Segoe UI, sans-serif` stack, 16px inputs and existing small secondary metadata are reused. Recipe headings establish each row; tabular numbers support demand/free/shortage comparisons. The reader capture visibly retains the subprecision threshold and exact fraction instead of displaying unknown or tiny demand as an ordinary zero. |
| Material and shape | The weekly editor remains the existing bordered surface with its restrained shared shadow. Suggestions use a disclosure bounded by horizontal rules, followed by numbered rows and dividers rather than another stack of cards. Existing notices, rounded controls and the native confirmation dialog provide the same material language. Shared buttons have a 10px radius; inputs retain the incumbent 9px radius rather than introducing a new variant. |
| Desktop hierarchy | The persistent sidebar, top bar, bounded main area and seven-day grid remain intact. Suggestions appear after the meal/component editor. The covered full-page capture shows the complete relationship; the saved desktop capture returns to the established week with the confirmed source visible in its meal. |
| Mobile hierarchy | At 390px, labels remain above native fields, suggestion controls stack, primary/actions span the available row, and long recipe/product evidence wraps. The existing compact header and fixed bottom navigation remain. All eight mobile captures fit their horizontal viewport; their scrolled content is intentionally longer than one screen. |
| Density and disclosure | Search, portions, date and explicit evaluation precede results. Observation time, comparison basis and the bounded 20-recipe page explanation remain adjacent to results. Ingredient/mapping and lot/date evidence use native disclosures, preserving the weekly editor's task-oriented row structure. |
| Controls and motion | Shared buttons retain a minimum 44px height, inputs at least 46px, and suggestion summaries at least 44px. The source continues to use the shared 3px accent `:focus-visible` ring. Existing transitions/dialog animation remain conditional on reduced-motion preference; this document did not execute a motion check. |

No new mismatch with the incumbent system was observed in this bounded source/image comparison. Existing global sizing differences between the concise design prose and the actual input/panel styles are recorded as incumbent details, not repaired as part of this extension.

## States and focus

The named disclosure explains that alternatives are evaluated independently against the same free stock. Results distinguish covered, known shortage and incomplete mappings/amounts. Use-by exclusion, best-before and opening guidance are separately labelled; the shortage capture visibly says the review state is not a consumption recommendation. Unknown total demand remains explicitly partial and offers a concrete source-repair destination.

Empty search gives a recovery action. A failed evaluation hides old results/actions and displays an alert with an explicit reevaluation instruction. Disabled pantry leaves availability open; disabled recipes have a separate source state in the inherited browser report. Reader results retain explanatory evidence without a source-write action. The captures and source keep these states within the same palette, typography and form hierarchy.

Source inspection shows input changes invalidate the comparison, abort its request and clear old results; an epoch guard prevents late responses from restoring them. Busy controls are locked and successful results focus their heading. Errors focus their alert. These execution claims are supported by the inherited browser checks; the documenter did not trigger them again.

Changed immutable source opens the existing named confirmation before preview. Preview shows the current revision, evaluated portions and the reminder to recheck stock before reservation/purchase. Closing it returns focus to its still-connected recipe action. Preview and staging remain separate from explicit meal save.

SUG-F01 concerned staging hiding the activated preview control without assigning focus. The corrected source captures the new component identity, renders it and focuses its heading after comparison invalidation. The current staged capture visibly shows the accent focus ring on that heading. The inherited report specifically records a real keyboard staging check, and the coordinator identifies this capture as taken after Enter activation. The screenshot alone is evidence of the visible ring, not independent proof of the preceding keyboard interaction.

## Opened current captures

Desktop captures are full-page at 1440px width; mobile captures are 390 × 844. Names and synthetic timestamped records belong to the isolated local review, not production demo data.

| Capture | Comparison observation |
|---|---|
| [Covered, light desktop](../outputs/planning-suggestions-review/suggestions-covered-light-desktop.png) | Full weekly/editor hierarchy, independently evaluated alternatives, exact demand and divided evidence rows. |
| [Shortage, dark mobile](../outputs/planning-suggestions-review/suggestions-shortage-dark-mobile.png) | Wrapped long identity, readable demand/free/missing amounts and separate excluded use-by review. |
| [Empty, light mobile](../outputs/planning-suggestions-review/suggestions-empty-light-mobile.png) | Visible filter/portion/date labels, full-width evaluation and actionable empty state. |
| [Error, dark mobile](../outputs/planning-suggestions-review/suggestions-error-dark-mobile.png) | No stale quantities/actions; existing danger treatment and retry instruction. |
| [Disabled, light mobile](../outputs/planning-suggestions-review/suggestions-disabled-light-mobile.png) | Explicit disabled-pantry explanation and availability left open. |
| [Stale source, dark mobile](../outputs/planning-suggestions-review/suggestions-stale-dark-mobile.png) | Existing modal material, named old/current revisions and legible retain/confirm choices. |
| [Handoff, light mobile](../outputs/planning-suggestions-review/suggestions-handoff-light-mobile.png) | Existing tonal source preview with current source, ingredient evidence and portion/time fields. |
| [Staged, dark mobile](../outputs/planning-suggestions-review/suggestions-staged-dark-mobile.png) | New component heading has visible keyboard focus; existing component controls remain usable. |
| [Saved, light desktop](../outputs/planning-suggestions-review/suggestions-saved-light-desktop.png) | Established week grid and meal retain the explicitly confirmed source revision after save. |
| [Reader, dark mobile](../outputs/planning-suggestions-review/suggestions-reader-dark-mobile.png) | Readable exact/subprecision evidence and disclosures without source-write controls. |

This pass approves consistency with the established visual system at the stated source/image scope. It does not close pending native/race gates, claim another browser run, or authorize publication.
