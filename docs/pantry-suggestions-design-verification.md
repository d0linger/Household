# PA15 — incumbent design verification

PA15 adds a standalone recipe comparison to the existing Pantry navigation under the approved 180-improvement brief and preferred implementation order. It reuses the accepted MP11/MP12 disclosure, result rows and comparison reader. The established green Household identity, independent modules and empty production-data policy remain the authority; no new visual world, raster asset, font or runtime dependency is introduced.

## Scope and evidence ownership

This separate documentation pass read [PRODUCT.md](../PRODUCT.md), [DESIGN.md](../DESIGN.md), the [surface brief](../.impeccable/surfaces/internal-web-templates-pantry-suggestions-html.md), the [feature verification ledger](pantry-suggestions-verification.md), [Pantry suggestions template](../internal/web/templates/pantry-suggestions.html), [Pantry controller](../internal/web/static/pantry-suggestions.js), [shared disclosure](../internal/web/templates/planning-suggestions.html), [shared comparison controller](../internal/web/static/planning-suggestions.js), [destination template](../internal/web/templates/ingredient-pantry.html), [destination controller](../internal/web/static/ingredient-pantry.js), [Pantry daily navigation](../internal/web/templates/pantry-daily.html) and [shared CSS](../internal/web/static/app.css). It opened all seven current captures below and compared their visual treatment with that source. This document is the only file written by this pass.

The [current browser report](../outputs/pantry-suggestions-review/browser-report.json) supplies inherited execution evidence: 25 component checks, seven axe scans with no violations, no JavaScript errors and no external requests. The [final browser chain](../outputs/expansion-review/pantry-suggestions-browser-chain-final.log) records 803 checks across 23 components. The documenter did not execute these checks, interact with the browser or access a database. Clean axe scans alone do not establish complete WCAG conformance.

The coordinator reports a fresh independent finish reviewer opened all seven required captures and returned a full PA15-target ship disposition with no material findings. A separate limited source review reported no material finding and ran no tests. These verdicts apply to their stated PA15 source/UI scopes; they do not establish whole-application acceptance.

## Preserved authority and inherited drift

`DESIGN.md` remains byte-for-byte unchanged, with SHA-256 `07190617EB9C9FB382823283517BAA89B8BA7B877CA7ADE713F9B449FCBA4522`. `PRODUCT.md` also remains unchanged. `.impeccable/design.json` is absent and was not created. Context and detector already ran in this continuing session and were not rerun here.

The existing design document has a compact legacy structure rather than the current documentation reference's canonical sections and machine-readable token layer. The extended component sidecar is absent. These are inherited documentation/tooling drift, reported without repair. The comparison records the actual incumbent implementation rather than rewriting its authority or inventing system-wide rules from this surface.

## Incumbent-system comparison

| Aspect | Observed comparison |
|---|---|
| Palette and ground | Shared CSS retains the documented light ground/surface/ink (`#F2F4F1`, `#FFFFFF`, `#14201C`) and dark equivalents (`#0E1513`, `#16201D`, `#E6EDE9`). Forest/sage accents (`#0E6B57` / `#79D5BD`), secondary ink and quiet dividers are reused. The standalone page places its disclosure and rows directly on the existing ground, without an added card shell. |
| Type and exact amounts | The local `ui-sans-serif, system-ui, -apple-system, Segoe UI, sans-serif` stack, 16px inputs, established heading scale and 12px secondary metadata remain. Result rows use tabular numbers. Current captures show chosen 2.5 portions yielding exact 0.625kg demand; the reader capture shows the separate one-portion basis. No visual score substitutes for demand/free/missing quantities. |
| Material and components | The existing native search, decimal and date controls, primary evaluation button, status text, error notice and native ingredient/date disclosures are reused. Suggestions remain numbered rows separated by rules. Shared buttons retain a 10px radius and at least 44px height; inputs retain the actual incumbent 9px radius and at least 46px height. These existing details are not normalized as part of PA15. |
| Desktop navigation and hierarchy | The full-page desktop capture retains the persistent sidebar, Pantry selection, top bar, bounded content area and existing horizontal Pantry navigation. A task heading and explanation lead into the already-open comparison disclosure, followed by its bounded result page and continuation action. No dashboard metrics or competing hierarchy is added. |
| Mobile and responsive behavior | The 390px captures retain the compact header and fixed bottom navigation, with Pantry selected until the recipe destination is opened. The destination correctly selects Cookbook. Search/portions/date stack with visible labels; evaluation and source actions use the available width. Long recipe and product names wrap, and all six mobile captures fit their horizontal viewport. Content continues vertically beyond the 844px capture height. |
| Result scope | Observation time, household timezone, selected date/portions, the independently evaluated alternatives explanation and the 20-recipe page boundary use the established secondary text treatment. Date reasons remain separately labelled. This extends the accepted comparison rather than introducing a different ranking presentation. |
| Focus and state language | Source retains the shared 3px accent `:focus-visible` ring, labelled controls, status region, focused result heading and focused error alert. The inherited report includes a keyboard handoff check. Static captures establish appearance; they are not an independent keyboard execution by this documenter. |

No material inconsistency with the incumbent visual system was observed within this source/image comparison. Existing global input/panel details that are more specific than the concise design prose remain incumbent behavior, not a PA15 redesign.

## Frozen source and stock-scope handoff

The first-page explanation explicitly says the feature also works without Mealplan, respects existing reservations and does not reserve, buy or consume anything. The shared comparison evaluates each recipe independently against free quantities. Blank date guidance names today in the household timezone; the inherited report verifies server resolution and canonical portions.

Before the disclosure, the page explains that “Physischen Vorrat vergleichen” opens the same immutable recipe source with the chosen portions, then compares physical stock afresh. The action uses that exact label. Source inspection confirms the Pantry controller passes `candidate.source_version_id` and the evaluated `page.servings` to the existing recipe comparison; the destination initializes both values from the URL. Its template names current product mappings and physical stock, and its product rows explicitly label “Physischer Vorrat.”

The dark handoff capture shows source revision 2, 2.500000 portions, 0.625kg demand and 1kg physical stock in the existing recipe comparison layout. The inherited browser checks additionally verify preservation of the historical source despite a later recipe change and navigation by an actual reader. The capture alone does not prove that later-change scenario. Free stock and physical stock can differ, so the destination does not inherit the suggestions coverage claim or imply a reservation.

## States reviewed

The empty capture offers a shorter search or recipe creation as recovery. The error capture removes prior results/actions and uses the existing danger notice with a reevaluation instruction. Disabled Recipes have an explicit named source state. The reader capture keeps the physical-comparison navigation action available without requiring write authority. Pantry-disabled entry closure is inherited browser evidence rather than an additional screenshot state.

Source shows explicit evaluation, locked controls while loading, invalidation on changed filters, request aborts and an epoch guard against late results. Failed refresh clears previous quantities/actions. These mechanisms and the signed continuation remain the shared comparison contract; this documenter did not rerun their runtime checks. No additional offline or cursor-error capture is claimed.

## Opened current captures

The desktop capture is full-page at 1440px width; each mobile capture is 390 × 844. Timestamped records are isolated local fixtures, not production demo data.

| Capture | Visual evidence |
|---|---|
| [Light desktop](../outputs/pantry-suggestions-review/pantry-suggestions-light-desktop.png) | Existing Pantry shell, explanation before the disclosure, 20 divided recipe alternatives and continuation. |
| [Dark mobile](../outputs/pantry-suggestions-review/pantry-suggestions-dark-mobile.png) | Readable selected-basis metadata, wrapped identities, exact free quantities and full-width physical-comparison action. |
| [Empty, light mobile](../outputs/pantry-suggestions-review/pantry-suggestions-empty-light-mobile.png) | Visible form labels, household-today guidance and actionable empty-search text. |
| [Error, dark mobile](../outputs/pantry-suggestions-review/pantry-suggestions-error-dark-mobile.png) | Existing danger treatment and reevaluation instruction without stale result rows/actions. |
| [Disabled, light mobile](../outputs/pantry-suggestions-review/pantry-suggestions-disabled-light-mobile.png) | Explicit Recipes-disabled state within the unchanged form hierarchy. |
| [Handoff, dark mobile](../outputs/pantry-suggestions-review/pantry-suggestions-handoff-dark-mobile.png) | Existing recipe destination names the selected revision/portions and physical-stock scope. |
| [Reader, light mobile](../outputs/pantry-suggestions-review/pantry-suggestions-reader-light-mobile.png) | Readable one-portion results and read-only comparison navigation. |

## Operational context and limits

At this handoff the current complete native regression was running serially against isolated port 55434 with a 331-file source manifest; scoped race acceptance remained pending. Neither is marked passed here. The [recorded image identities](../outputs/expansion-review/pantry-suggestions-images.log) are runtime `0f22860d51fc4d352d31fb5139b7248917a4f4a90032246b1544d3f1546d94a4` (40,868,449 bytes) and helper `fba3e0f723b388724e8302b3c2aee1d42b97de1882d2cf423b0443b42cd96668` (521,515,626 bytes), both user `10001:10001`. These are local builds, not a live deployment.

The inherited [encrypted synthetic recovery report](../outputs/expansion-review/pantry-suggestions-recovery-report.json) passes with 7.18s backup and 23.73s restore-and-verify. It does not establish authenticated restored API replay, load acceptance or production RTO/RPO. The feature/implementation ledgers retain responsibility for subsequent acceptance status. This document confirms consistency with the incumbent system at the stated PA15 source/image scope and does not authorize publication.
