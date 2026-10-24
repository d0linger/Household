# Design review — 2026-10-24

The established Household artifact and approved prototype gallery supplied the visual authority. This was a code-led extension of the sage/forest Operate interface, with system fonts, project SVG icons, record rows, visible form labels, desktop sidebar and mobile four-plus-More navigation. There was no raster comp or replacement visual-world decision.

A fresh independent finish reviewer inspected the desktop/mobile captures and source. Its initial disposition was **fix**, covering readable offline review, report context, and control boundaries/targets. One correction batch addressed all three. The verdict pass scored **F01 resolved, F02 resolved, F03 resolved**, with disposition **ship at the scope of those fixes**. This is not a claim of complete WCAG, security or deployment certification.

| Finding | Delivered behavior / evidence |
|---|---|
| F01 | German action and field labels, affected record, named relationships and before/after values replace endpoints/JSON/UUIDs. Missing relationships block sending. Generation, version and idempotency checks remain. |
| F02 | Explicit report columns retain vehicle/odometer-series names, meter/device identity and localized interval boundaries. Wide tables have labelled keyboard-focusable scroll regions. |
| F03 | Separate control-border token #71877b measures 3.85:1 on the light surface and 4.33:1 on the dark surface. Named detail/CSV/install/module-open targets meet 44px minimums. |

The final browser report records **90 checks**, **11 screenshots**, **three axe scans without violations**, **zero external frontend requests** and **zero unexpected browser errors**. The scans covered login, desktop shopping and dark mobile settings. The executed checks also measured the named control rectangles and field-border contrast and exercised explicit offline replay with a changed named relationship. Evidence is in outputs/runtime-review/browser-report.json and the 11 adjacent PNG files; reviewed copies are under .impeccable/review/.

All captures were opened and validated. Fixed navigation within tall full-page screenshots is expected. Mobile report images show the left side of horizontally scrollable tables; source and executed column assertions support the remaining interval columns. Test records are synthetic; the live household remains empty.

A separate fresh read-only documenter compared the finished interface with the incumbent system and preserved DESIGN.md (SHA-256 07190617eb9c9fb382823283517baa89b8ba7b877ca7ade713f9b449fcba4522). No sidecar exists. Existing documentation drift remains visible: DESIGN.md does not distinguish the strengthened control border, describes icons as a licensed set rather than project-drawn SVG, uses a general 10px control radius while text fields use 9px, and retains its compact format without canonical YAML frontmatter. These discrepancies did not authorize replacing the design system.

Shopping keyboard-focus continuity after a full-page reload remains unverified. The report verifies the specified controls, not every textual link/disclosure on every route. No second detector was run; its original unresolved CSS-template/hierarchy warning was judged against the actual render, whose hierarchy was adequate.
