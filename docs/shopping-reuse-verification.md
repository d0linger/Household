# Shopping reuse design verification — 2026-10-24

This documentation check is limited to **SH06: favourites and reusable list templates**. It uses only the reuse section of [shopping-reuse.md](shopping-reuse.md), [DESIGN.md](../DESIGN.md), `shopping.html`, `shopping.js`, `module.html` and the supplied evidence. The SH07 consolidation contract and its unconnected store helpers are outside this verification.

The extension retains the incumbent Household palette, local system typography, labelled 16px form inputs, panels, rows, disclosures, buttons and focus treatments. Favourites use the existing searchable reference control. A template uses the existing accent notice to identify its preparation role and link to the copy form; catalogue/list maintenance remains in the existing tab/editor system. No replacement visual direction was introduced.

| Workflow | Checked source and rendered evidence |
|---|---|
| Favourite draft | “Aus Favoriten wählen” names the product, brand and pack. “In Entwurf übernehmen” fills a one-pack draft and explains that only “Hinzufügen” saves an item. Source restores focus to the name field and rejects a delayed response when the draft has changed. The browser report checks both delayed-response retention and no item being written by favourite selection. |
| Template preparation | The template notice distinguishes preparation from shopping. Live activity claiming is absent and article checks are disabled. The browser flow enters through the Vorlagen filter and verifies these restrictions. |
| Independent copy | The copy form has a visible name field and explains the 500-item limit and reset of purchase decisions/assignments. The browser flow creates a separately named working list, checks copied amount and named pack, then confirms that checking the copy leaves its template unchanged with a different item identity. |
| Filter navigation | Vorlagen and Favoriten now link to their filtered overview using paths from the application root. Two added browser checks confirm that these links leave a list detail and product detail respectively, rather than retaining the detail route with only a changed query. |

All four current captures were opened and inspected: [template desktop](../outputs/reuse-review/reuse-template-desktop.png), [template mobile](../outputs/reuse-review/reuse-template-mobile.png), [working-list desktop](../outputs/reuse-review/reuse-desktop.png) and [working-list mobile dark](../outputs/reuse-review/reuse-mobile-dark.png). They show the reviewed favourite draft and the resulting independent working list. Template editing stays expanded; shopping mode collapses its entry controls. Mobile layouts retain the compact header and five active module destinations. Fixed navigation appears within tall full-page images because it remains attached to the viewport. The fixture records are synthetic on the isolated review server.

The inspected [browser report](../outputs/reuse-review/browser-report.json), written at 22:21:21 local time, records **17 passing checks, four screenshots, four axe scans without violations, zero JavaScript errors and zero external requests**. The two added checks cover detail-to-filter navigation. The script uses 1440×1000 desktop and 390×844 mobile viewports; its final no-overflow assertion now checks the mobile favourites overview after navigation, not the working list shown in the last capture. Axe uses WCAG 2 A/AA and 2.1 AA tags; this does not establish complete WCAG 2.2 or assistive-technology acceptance.

An intermediate overflow assertion failed before page construction had completed. The test now waits for `networkidle` and the page's actual animation `finished` promises before measuring layout; the rerun passed without a CSS change. That earlier failure is not counted as a successful check.

| Existing execution evidence | Recorded result and limit |
|---|---|
| [reuse-focused.log](../outputs/expansion-review/reuse-focused.log) | Template/favourite copy and copy-limit/contention tests passed; web package 15.310s. |
| [reuse-shopping-reference.log](../outputs/expansion-review/reuse-shopping-reference.log) | Failed at the populated-upgrade audit-policy assertion because the application role could not read `audit_policies`. This run is not counted as passing. Its other named tests passed. |
| [reuse-race.log](../outputs/expansion-review/reuse-race.log) | Subsequent Linux race run passed; web package 159.266s. The implementation handoff identifies this as the run containing the corrected upgrade assertion after fixing its test role. |
| [reuse-verify.log](../outputs/expansion-review/reuse-verify.log) | Unit coverage 14.2% (677/4757 statements), 28 Node tests passed, workflow checks passed and 24 migration checksums verified. |
| [reuse-lint.log](../outputs/expansion-review/reuse-lint.log) | Zero issues. |
| [reuse-gosec.json](../outputs/expansion-review/reuse-gosec.json) | 48 files, 8092 lines, zero findings and no Go errors. |

This documentation pass inspected those outputs; it did not execute the test suites, browser checks or scanners. The failed intermediate run and later passing run remain separate evidence. No fresh complete native integration-suite claim is made here.

DESIGN.md is preserved with SHA-256 `07190617eb9c9fb382823283517baa89b8ba7b877ca7ade713f9b449fcba4522`. No `.impeccable/design.json` exists or was created, and no design detector was run in this pass. Existing documentation drift remains unchanged: the strengthened control border is undocumented, icon wording differs from the project SVGs, text fields use 9px corners against general 10px guidance, and canonical token frontmatter is absent. No drift repair was performed.

Documentation verification is complete at this evidence scope. The independent finish reviewer marked **R01 resolved**, with disposition **ship scoped to the corrected filter links**. This document does not accept SH07, all 180 improvements or production deployment.
