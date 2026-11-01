# Zähler & Energie

Primary target: `internal/web/templates/meters-workspace.html`.
Related targets: `internal/web/static/meters-workspace.js`, `internal/web/static/meters-forms.js`, `internal/web/static/meters-finance.js`, `internal/web/static/meters.css`.
Mode: Operate. Extend the established Household interface and the approved module artifact.

## Direction contract

THESIS: Make a household measurement understandable before asking for another one. Reading quality, physical device and time interval stay beside the value. Rows, labelled forms and explicit decisions carry the workflow.

OWN-WORLD: Inherit DESIGN.md, the green accent, system sans, 44px controls and existing desktop sidebar/mobile bottom navigation. The reviewed current Chores desktop/light and mobile/dark captures establish the incumbent visual truth. Keep data in readable rows; use no metric-card wall or decorative chart.

STORY: Find a register, understand its last measurement and whether an actual interval is known, then record a new reading. Costs explain missing coverage and estimates. Configuration remains secondary to everyday capture.

FIRST VIEWPORT: Heading and Ablesen action, module subnavigation, selected register, then a practical list or the current capture step. Mobile forms use one column; desktop pairs related fields. An empty installation begins with one useful create-meter action and no illustrative production values.

FORM: Established operational workspace extension. Seven routes cover overview, capture, history, costs, plans, imports and settings. Editors appear inline; confirmed financial transfer includes a complete review. Charts have real time/quantity axes and an equivalent accessible table.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

## Required states and verification

Show loading, no register/device, unknown quantity, real zero, estimated quantity, missing tariff, pending recalculation, permission loss, conflict and uncertain network result. Preserve entered values in page memory; replay the exact command/key. Never put private data in persistent browser storage. Verify actual desktop/mobile captures and keyboard/Axe together after whole-module implementation; no Meters browser pass has occurred yet.
