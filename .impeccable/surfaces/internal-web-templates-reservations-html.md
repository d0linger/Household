---
version: 1
slug: "internal-web-templates-reservations-html"
primary_target: "internal/web/templates/reservations.html"
related_targets: ["internal/web/static/reservations.js","internal/web/static/planning.js"]
---

# Explicit meal stock reservations

Mode: Operate. Extend Household's planning and Pantry review workflow. Members inspect stock; editors deliberately allocate up to100 named lots and release exact lease identities. Preserve the approved shell and existing workflows. Ingredient-demand inference is a separate later feature.

## Direction contract

THESIS: make a meal's soft stock claims understandable before saving; physical stock, free stock and undercoverage remain distinct.

OWN-WORLD: inherit the existing green actions, quiet bordered workspace, local sans-serif, tabular amounts and labelled native controls. No new visual system or shipping raster asset.

STORY: identify the meal and expiry, choose a named lot, compare physical/own/other/free amounts, add an exact quantity, review every line and explicitly save or release.

FIRST VIEWPORT: existing navigation and back link, meal heading, current lease state and a short explanation. The allocation editor follows in the same bounded column; review and exact retry stay beside their messages on mobile.

FORM: precisely scoped code-led extension, with the existing source→selection→review→receipt sequence. No concept seed applies to this narrow approved task.

FINISH: unreviewed and undocumented is unfinished; this build ends with the finish review, the verdict, DESIGN.md, and every shipping raster carrying its provenance

## Boundaries

No automatic Recipe or Shopping mutation. Never infer a guarantee of food usability. State every lease's household timezone and exact expiry. Expired leases stop counting immediately. Retain draft selections on a known conflict and request a fresh review; uncertain reserve/release retains the exact body and destination. Readers receive no mutation controls. Controls and quantity labels remain visible at mobile width. Review includes every named product/location/lot, quantity, expiry and replacement effect. Release confirmation names the immutable lease and cannot affect its replacement.
