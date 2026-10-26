# Participants and portions — incumbent design comparison

2026-10-26. Separate Impeccable documenter pass for the MP06 extension, grounded in the finished source and seven saved captures. This is an ordinary extension of the German Operate interface described by [PRODUCT.md](../PRODUCT.md), [DESIGN.md](../DESIGN.md) and the [planning surface contract](../.impeccable/surfaces/internal-web-templates-planning-html.md). It introduces no visual world, font, icon system, dependency or raster asset.

## Comparison with the incumbent

The [template](../internal/web/templates/planning.html), [local script](../internal/web/static/planning.js) and [stylesheet](../internal/web/static/app.css) retain the forest/sage light and dark tokens, local sans-serif stack, bordered white/dark surfaces, green primary action and visible native field labels. The existing type hierarchy remains: body and inputs 16px, labels 13px, supporting copy 12px, section headings 17/21px and the responsive page heading 28–40px. Buttons retain 44px minimum height, fields 46px and the visible 3px accent focus outline.

The desktop sidebar and seven-day grid remain established navigation and layout. Mobile retains the compact header, bottom navigation and one-column form. Participant rows reuse plain names and removal actions; at 600px and below they stack with readable spacing. Names wrap, removal buttons retain specific accessible names, and the add action aligns to its content. These additions extend existing rows and form groups rather than creating a new component vocabulary.

The roster appears before recipe components. Saved inactive names remain visibly marked and counted; anonymous guests remain a count. The labelled meal mode makes derived versus manual portions explicit, while each component exposes its own independent versus meal-linked choice. The calculation and supporting text explain that manual quantities preserve the roster and that reservation/procurement needs fresh review.

The corrected ordinary pre-save comparison appears before the component list. It reports exact saved and draft amounts **and both modes**: the desktop capture shows 1 independent portion → 3 meal-linked portions; the mobile impact capture shows 3 → 2.500001 meal-linked portions. Source inspection confirms a stable saved-component copy, live recalculation from roster, guest, manual amount and mode edits, and entries for added/removed components. The saved baseline changes on explicit conflict resolution. This evidence addresses the finish review's missing-impact finding; it does not establish an issue-free whole surface.

## Captures and recorded checks

All seven files listed in the [participant browser report](../outputs/planning-participants-review/browser-report.json) were opened independently in this pass:

| Capture | View and observed evidence |
|---|---|
| [Derived light desktop](../outputs/planning-participants-review/participants-derived-light-desktop.png) | 1440px-wide full page; roster, calculation, linked/independent components and exact saved-to-draft impact. |
| [Uncertain dark mobile](../outputs/planning-participants-review/participants-uncertain-dark-mobile.png) | 390×844; preserved draft and same-identifier retry message/actions. |
| [Inactive dark mobile](../outputs/planning-participants-review/participants-inactive-dark-mobile.png) | 390×844; wrapped long name, inactive label and separate removal control. |
| [Manual light mobile](../outputs/planning-participants-review/participants-manual-light-mobile.png) | 390×844; explicit manual mode, preserved participant calculation and impact text. |
| [Impact light mobile](../outputs/planning-participants-review/participants-impact-light-mobile.png) | 390×844; exact 3 → 2.500001 comparison above the linked component amount. |
| [Conflict dark mobile](../outputs/planning-participants-review/participants-conflict-dark-mobile.png) | 390×844; named draft/saved comparison with quantities, guests and participant details. |
| [Saved light desktop](../outputs/planning-participants-review/participants-saved-light-desktop.png) | 1440px-wide full page; saved week with exact 2.500001 linked and 2 independent quantities. |

The report records 30 checks, seven axe scans with no reported violations, no JavaScript errors and no external requests. The [final browser-chain log](../outputs/expansion-review/participants-browser-chain-final.log) records the complete 21-part chain with 740 checks, including this participant campaign. These are existing executed records, not tests rerun by the documenter. The axe scans cover their configured WCAG 2/2.1 A/AA tags and do not alone establish complete WCAG 2.2 AA conformance.

The independent finish review first returned `fix` for the missing ordinary impact comparison. After correction, the same reviewer's correction-only verdict was **Resolved**, with all seven captures valid, no visible regression at that scope, and disposition **ship** for that one finding. This comparison does not expand that verdict into backend, database, whole-surface or full feature acceptance. Full native regression and scoped Linux race were pending/running separately at this pass; their results belong in the [verification ledger](planning-participants-verification.md). No browser, database or test operations were performed by this documenter.

## Preserved system and inherited drift

[DESIGN.md](../DESIGN.md) remains byte-for-byte unchanged at SHA-256 `07190617EB9C9FB382823283517BAA89B8BA7B877CA7ADE713F9B449FCBA4522`; `.impeccable/design.json` remains absent. No system files were regenerated.

The incumbent stylesheet already uses 9px input/select/textarea corners against DESIGN.md's general 10px control guidance, and carries the uppercase, letter-spaced sidebar eyebrow visible in the desktop captures. The existing design record also predates the current frontmatter/canonical-section format and has no sidecar. These inherited details were present before MP06; they are reported, not canonized as new rules or repaired without authorization. The participant extension requires no change to the incumbent design authority.
