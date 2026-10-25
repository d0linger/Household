---
version: 1
slug: "internal-web-templates-planning-demand-html"
primary_target: "internal/web/templates/planning-demand.html"
related_targets: ["internal/web/static/planning-demand.js","internal/web/static/planning.js"]
---

# Reviewed meal demand

Mode: Operate. Precisely scoped extension of the approved weekly planning and stock review workflow. The user authorized implementation in the preferred order; purpose, existing identity and source→selection→review sequence are already settled. Preserve PRODUCT.md and DESIGN.md.

## Direction contract

THESIS: select whole meals, confirm each frozen ingredient's product identity, then compare grouped demand with actual eligible stock and own/other claims.

OWN-WORLD: existing quiet green actions, labelled native controls, bordered rows, local sans-serif and tabular exact amounts; no new visual system or raster asset.

STORY: select meals from a week, inspect completeness and product totals, resolve ingredient mappings deliberately, refresh current stock. Unknown is never zero. Pending Shopping coverage remains explicitly unlinked until MP17 is implemented.

FIRST VIEWPORT: back link, plan title, week and meal selection, explicit preview action. Product totals precede ingredient details; each product exposes physical/eligible/own/other/free/usable/undercoverage amounts without hiding shortage.

FORM: code-led narrow extension. Inline single-ingredient mapping review names meal/component/ingredient, old/new target, portions and effects. Uncertain results retain exact request/key and a nearby retry; readers inspect without mutation controls.

FINISH: scoped browser accessibility and desktop/mobile light/dark captures, fresh finish reviewer and separate documentation comparison. No second context/detector invocation for this continuing task.

## Boundaries

1–42 whole open/planned recipe meals, at most500 ingredients and16MiB frozen source copies. Product grouping happens before stock subtraction. No automatic stock booking, reservation, Shopping item or rounding. Missing/incompatible/inexact/out-of-range quantities remain visible. Changes to selection invalidate the visible preview. Stored plan mapping identity survives clear and Recipe purge; copies start unassigned.
