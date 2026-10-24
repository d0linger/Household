# Shopping merge design verification — 2026-10-24

This documentation check covers **SH07: controlled consolidation of shopping items**, using the consolidation section of [shopping-reuse.md](shopping-reuse.md), [DESIGN.md](../DESIGN.md), the shopping template/script and supplied execution evidence. It extends the established shopping surface; SH06 has its [separate verification](shopping-reuse-verification.md).

The interface retains the incumbent palette, local system typography, labelled form controls, rows, disclosures, buttons and focus treatments. The merge disclosure follows the working items. Its labels distinguish “Position behalten” from “Position dazugeben”; the explanation identifies the first position as the survivor. The comparison uses the existing conflict-review treatment, wraps long text and stacks the two choices on mobile. No new visual system was introduced.

| Workflow | Checked evidence |
|---|---|
| Explicit review | The user selects two different open positions from the current page. An authorized, non-binding server preview supplies the exact result and named differences for article, quantity, unit, original/replacement variant, availability, assignment, group and note. The comparison names the surviving article and explains that the other position will be removed. |
| Blocking conditions | The confirmation control is offered only for a compatible preview. Known attachment/resource-link and closed-list blockers prevent confirmation; different variants show their named brands and links to details. The focused test output records attachment, resource-link, template, archive, checked-item and other compatibility guards. These conditions are rechecked by the mutation; a preview does not reserve the records. |
| Version and focus continuity | The browser run rejects a version change made after preview without partially merging either item. A fresh preview then succeeds, preserves the selected survivor and exact quantity, removes only the reviewed second item and returns keyboard focus to “Auswahl vergleichen”. Invalid self-selection returns focus to the first choice; a ready comparison receives focus. |

The current [desktop](../outputs/merge-review/merge-comparison-desktop.png) and [dark mobile](../outputs/merge-review/merge-comparison-mobile-dark.png) captures were opened and inspected. Both show the established shell and the comparison in its full-page context. The captured preview combines two 500g packs with 1kg into **4 Stück**. Later in the test, the second quantity changes to 2kg: the stale preview fails, and a fresh preview confirms **6 Stück**. These are distinct recorded stages, not conflicting totals. Fixtures are synthetic on the isolated review server.

The inspected [browser report](../outputs/merge-review/browser-report.json), written at 22:30:00 local time, records **13 passing checks, two screenshots, two axe scans without violations, zero JavaScript errors and zero external requests**. It uses 1440×1000 desktop and 390×844 mobile viewports and checks the mobile comparison for page overflow. Axe covers WCAG 2 A/AA and 2.1 AA tags; complete WCAG 2.2 and assistive-technology acceptance are not established here.

Earlier browser work included a failed focus assertion and captures taken from an unsuitable scroll position. Those results are not treated as successful final evidence. The current capture helper explicitly calls `scrollTo(0, 0)` and waits one animation frame before each screenshot; the two images inspected here are the replacement captures. The current report separately records the passing selection, comparison and completion focus checks.

| Existing execution evidence | Recorded result |
|---|---|
| [merge-focused.log](../outputs/expansion-review/merge-focused.log) | Exact preview/replay and guard tests passed; web package 13.565s. Guards include versions, variants, notes, assignment, list state, numeric overflow/rounding, attachments, links and lock contention. |
| [reuse-merge-integration.log](../outputs/expansion-review/reuse-merge-integration.log) | Complete native integration run passed; combined internal coverage 63.3% (2982/4708 statements). |
| [merge-race.log](../outputs/expansion-review/merge-race.log) | Focused Linux race run passed; web package 27.889s. |
| [merge-verify.log](../outputs/expansion-review/merge-verify.log) | Unit coverage 14.7% (716/4878 statements), 28 Node tests passed and 25 migration checksums verified. |
| [merge-lint.log](../outputs/expansion-review/merge-lint.log) | Zero issues. |
| [merge-gosec.json](../outputs/expansion-review/merge-gosec.json) | 49 files, 8295 lines, zero findings and no Go errors. |

This documentation pass inspected the existing outputs; it did not execute tests, browser checks or scanners. DESIGN.md remains unchanged with SHA-256 `07190617eb9c9fb382823283517baa89b8ba7b877ca7ade713f9b449fcba4522`. No sidecar exists or was created, and no design detector was run. Existing drift remains unchanged: undocumented strengthened control border, icon wording versus project SVGs, 9px field corners versus general 10px guidance, and absent canonical token frontmatter.

Documentation verification is complete at this evidence scope. The independent finish review returned **ship scoped to SH07**, with no further material findings, including review of both current captures and the focus checks. This document does not establish completion of all 180 improvements or production deployment.
