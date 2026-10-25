# Planning demand — scoped design comparison and development evidence

Reviewed on 2026-10-25. This is the separate documentation comparison for the code-led MP14 demand surface after the final exact-fraction display correction. It records the established Operate identity, the evidence inspected, and the limits of that evidence. It does not complete MP14 or approve backend/runtime deployment.

## Authority and files compared

The comparison used [PRODUCT.md](../PRODUCT.md), [DESIGN.md](../DESIGN.md), the [planning-demand surface brief](../.impeccable/surfaces/internal-web-templates-planning-demand-html.md), and the [development contract](planning-demand.md). Source inspection covered [the embedded template](../internal/web/templates/planning-demand.html), [the demand controller](../internal/web/static/planning-demand.js), [shared and demand styles](../internal/web/static/app.css), and [weekly planning links](../internal/web/static/planning.js). All ten captures listed below were opened, and the saved [browser report](../outputs/planning-demand-review/browser-report.json) was read.

The supplied module-system artifact and reviewed prototypes remain the visual authority identified by DESIGN.md. This pass compares the new surface with that recorded contract and its current shared implementation; it does not re-review all 50 prototypes or establish a replacement visual world. The Impeccable document reference was used for source-to-design comparison within the explicitly limited documentation scope. No second context or detector run was performed.

DESIGN.md retains SHA-256 `07190617eb9c9fb382823283517baa89b8ba7b877ca7ade713f9b449fcba4522`. `.impeccable/design.json` was absent before this pass and remains absent. Neither file was generated, refreshed, or repaired.

## Comparison with the established interface

| Contract | Source and capture evidence |
|---|---|
| Quiet green Operate interface | The shared light/dark ground, surface, ink, secondary ink, accent, accent surface, border, warning, and danger values match the DESIGN.md table. Local system sans-serif, labelled native controls, bordered rows and existing panels continue the application identity. No new image asset, font, or visual system is introduced. |
| Source → selection → explicit review | The page begins with the return link, plan identity, week and whole-meal selection. Its preview action precedes results. Week links omit templates; eligible recipe-meal actions carry that meal into the same demand route. Product totals precede ingredient details. |
| Exact and incomplete quantities remain legible | Unmapped ingredients remain open. Confirmed repeated products show grouped demand and a single stock comparison. The corrected amount formatter exposes exact `1/3` and `1/3000000` alongside approximate or subprecision display, including grouped demand, shortage and ingredient demand. The unbookable explanation remains visible. |
| Deliberate product mapping | The inline review names meal, component, ingredient, portions, current product and reviewed replacement. Saving and clearing have explicit confirmation. The receipt identifies the ingredient, reviewed choice and applied binding version. Source text containing HTML-like input appears literally in captures. |
| Useful stock and recovery detail | The expanded stock row distinguishes physical, eligible, own, other, free, selection-usable and undercovered amounts. The uncertain state visibly disables replacement actions and retains the nearby exact-operation retry. The controller retains the serialized request and idempotency key; the report checks that retry behavior. |
| Mobile and accessibility continuity | The bounded 960px surface, 75-character paragraph limit, wrapping names, tabular product amounts, 44px controls and stock disclosure, focus targets and existing shell are retained. At 600px and below, action groups become a single column. Captures show the existing desktop sidebar and mobile header/bottom navigation in light and dark appearances. |

Shopping coverage is explicitly disclosed as unlinked before the product results. The wording explains that displayed shortage is not an additional shopping quantity and that preview performs no stock booking or shopping creation. This preserves the MP17 dependency rather than presenting partial MP14 work as complete.

## Captures inspected

Desktop evidence is full-page; mobile target-state captures are 390×844. Mobile captures intentionally show the relevant scrolled review state rather than the entire page. The PNGs are verification artifacts under `outputs/`, not shipped raster assets.

| Capture | Compared evidence |
|---|---|
| [demand-unmapped-light-desktop](../outputs/planning-demand-review/demand-unmapped-light-desktop.png) | Selection before results; incomplete status; unlinked Shopping notice; missing mappings; literal HTML-like ingredient text. |
| [demand-mapping-light-mobile](../outputs/planning-demand-review/demand-mapping-light-mobile.png) | Source identity, portions, current mapping and labelled local product chooser; long content wraps. |
| [demand-mapping-confirmation-light-mobile](../outputs/planning-demand-review/demand-mapping-confirmation-light-mobile.png) | Reviewed old/new product and explicit metadata-only effect; save, clear and close controls. |
| [demand-uncertain-dark-mobile](../outputs/planning-demand-review/demand-uncertain-dark-mobile.png) | Uncertain outcome notice, disabled competing actions and visible same-operation retry. |
| [demand-grouped-light-desktop](../outputs/planning-demand-review/demand-grouped-light-desktop.png) | Two ingredient sources aggregate to 1.2kg demand and 0.2kg shortage against 1kg stock; ingredient details and saved receipt remain distinct. |
| [demand-stock-dark-mobile](../outputs/planning-demand-review/demand-stock-dark-mobile.png) | Expanded own/other claims: 0.6kg own, 0.3kg other, 0.1kg free and 0.7kg usable for selection; 0.5kg shortage. |
| [demand-cleared-receipt-light-mobile](../outputs/planning-demand-review/demand-cleared-receipt-light-mobile.png) | Clear receipt explicitly reports the reviewed unmapping and binding version 2. |
| [demand-fractions-light-desktop](../outputs/planning-demand-review/demand-fractions-light-desktop.png) | Grouped demand, shortage and ingredient rows expose exact `1/3` and `1/3000000`; both shortages are described as not directly bookable. |
| [demand-fractions-dark-mobile](../outputs/planning-demand-review/demand-fractions-dark-mobile.png) | Exact grouped demand and shortage fractions wrap within the narrow dark surface. |
| [demand-subprecision-source-light-mobile](../outputs/planning-demand-review/demand-subprecision-source-light-mobile.png) | Stored source of 0.000001kg for three base portions yields visible exact ingredient demand `1/3000000`; receipt and long labels fit. |

## Finish disposition and browser scope

The initial fresh finish review returned `fix` for PD-F01: exact fractions were missing from the displayed quantities. After the scoped formatter correction, the same reviewer inspected all ten valid captures plus source/report evidence and returned **PD-F01 resolved / ship**, with no regression observed at that correction scope. This disposition is a scoped UI review, not complete MP14 or backend/runtime acceptance.

The saved browser report passes **43 checks**, with **10 axe scans reporting no violations**, no demand JavaScript errors and no external demand requests. Its checks cover selection invalidation, literal source rendering, review focus, mapping without premature writes, exact uncertain retry, replay receipt, grouped subtraction, own/other claims, version conflict, explicit unmapping and exact nonterminating/subprecision quantities. The parent implementation pass validated all ten captures. This documentation pass inspected those existing artifacts and executed no tests or additional browser campaign. Automated axe results at these states do not constitute whole-application accessibility certification.

## Existing drift preserved

DESIGN.md is an established concise contract with a token table and existing headings; it does not use the current document reference's YAML frontmatter/canonical-section format, and it has no sidecar. The shared stylesheet also uses a 9px form-field radius while the contract describes a 10px control radius. These are existing format/implementation differences, recorded here without changing the design system. The demand surface reuses the incumbent controls and shared style values.

## Development and acceptance limits

MP14 remains **in progress: 52 verified / 1 in progress / 127 open**. Attributable pending Shopping coverage and complete acceptance depend on MP17.

Earlier corrected development campaigns passed five focused native contracts at `web 30.518s` and seven at `web 44.989s`; the corrected historical-upgrade case passed separately at `web 13.297s`, and the schema/source-guard plus reader/real-purge campaign passed at `web 14.300s`. These are bounded development results. At this documentation handoff, nine focused native contracts are executing and are **not recorded as passed**. Final native whole-chain acceptance, scoped Linux race and final encrypted recovery remain unclaimed. The current report does not extend earlier reservation or replenishment acceptance to this demand feature.

No live `127.0.0.1:8100` deployment, push or publication is claimed. This pass changes only this comparison document.

Subsequent parent execution passed all nine focused native contracts together (`web65.881s`), scoped Linux race (`web143.260s`) and the complete19-component browser chain (668checks, including this43-check component). The rebuilt encrypted synthetic recovery preserves the new mapping's version, source/ingredient fingerprint, current target/generations/unit/readiness, earlier values and two file hashes (backup9.91s, restore/verify28.91s). This does not check authenticated restored API replay or production RTO/RPO. Full native regression is executing and is not yet counted as passed. These are parent-executed operational updates after the separate documentation comparison; they do not widen the scoped UI verdict or close MP14/MP17.

The first full native attempt subsequently failed (`web810.018s`) when the isolated PostgreSQL connection closed and55434 became unreachable. The test container was no longer present and its pinned image required a fresh pull; no retained event established why. This was not a package timeout and is not a passed regression. Recreating the disposable test service and repeating the full campaign are required before full-native acceptance.

The second full campaign also failed (`web902.480s`): the dated Pantry request returned500, then coverage/tool execution reported missing global Go temporary/SDK files. Its500 is not attributed to a proven cause. A fresh pinned project-local SDK/cache/temp environment passes the same targeted Pantry case (`web19.870s`); the complete frozen-candidate repeat remains pending. The exact exported runtime and matching backup helper have been rebuilt after the earlier tag disappeared. Their new encrypted synthetic recovery passes at7.90s backup/33.26s restore and verification, with the same binding/prior-data/two-file scope. The parent reopened all ten final full-chain captures and confirmed valid rendered states; it did not perform another design/polish campaign.

The isolated repeat reaches the actual15-minute cumulative package alarm (`web904.133s`), with no preceding assertion failure and its current test only1s old. A same-seed full repeat uses the new20-minute native suite budget. Focused/native/backend acceptance remains exactly as previously recorded until that repeat completes; the timeout adjustment is not a passed full regression.

The frozen MP14 same-seed repeat subsequently passes (`web993.516s`,70.9% combined internal coverage,6555/9239 statements,55% floor). It excludes later MP17 reader/schema/helper changes. This closes that foundation's full-native gap, not MP14's remaining attributable-Shopping dependency or MP17 acceptance.
