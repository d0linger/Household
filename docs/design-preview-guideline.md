# Household design directions

This guideline governs Household's standalone design laboratory. The user requested **100 different design concepts**, not 100 component variations inside three styles. Every concept must work across the same household dialogs. The comparison is a design decision aid; it does not replace the application or change household records.

[PRODUCT.md](../PRODUCT.md) remains the authority for product behavior. [DESIGN.md](../DESIGN.md) remains the production visual authority until the owner deliberately approves and implements a direction.

## 1. Purpose and boundaries

**Audience:** household members evaluating daily mobile workflows, and the owner selecting a coherent visual direction for Household.

**Mode:** Operate. Visitors browse design concepts, shortlist candidates, compare equivalent dialogs, try the controls and export a decision. The workbench stays predictable so differences between concepts are easy to judge.

**Design thesis:** change the visual system, keep the household task constant.

The prototype uses local HTML, CSS variables, JavaScript and declarative definitions. It follows the existing Go/vanilla-browser environment without React, Tailwind, external fonts, CDNs or a production frontend build. The standalone preview has no database connection, authentication session or device provider.

Names, amounts, dates, readings, documents and devices are explicitly illustrative fixtures. A form submission validates local example values and reports a **preview result**, not a saved expense, completed task, uploaded document or device command. Downloading or selecting a design never deploys it.

## 2. The 100-design contract

The catalog contains exactly **100 stable design IDs, DS-001 through DS-100**. Each ID represents a visual system applicable to every comparison dialog. There is no longer a quota of 20 designs per category, and no restriction to Retro-Brutalist, Cyberpunk and Modern.

Broad discovery tags such as editorial, nature, precision, culture, material or playful are filters, not parent templates that determine every result.

| Property | Design responsibility |
|---|---|
| Name, thesis and signature | Explain the idea and its recognizable structural decision. |
| Native hue and chroma | Give the design its own color world in Light and Dark. |
| Typography | Choose a suitable local stack, heading voice and weight. |
| Layout | Set the relationship between task, fields, context, summary and actions. |
| Surface treatment | Define rules, frames, layers, material or emphasis. |
| Radius and control shape | Establish consistent surface and input geometry. |
| Density and alignment | Define reading rhythm, grouping and breathing room. |
| Navigation | Express orientation through top navigation, side context, an index or steps. |

The canonical definitions live in [designDirections.ts](../design-preview/designDirections.ts). Shared dialogs live in [dialog-fixtures.js](../design-preview/dialog-fixtures.js). Generated browser artifacts must match their sources.

### Distinction beyond recoloring

A new name, hue, radius or shadow alone is not a different design. Each direction needs an intentional combination of structure, type, density, navigation, control grammar and material, supported by a concise rationale.

1. Can two concepts still be distinguished when both are shown in Pink Light?
2. Does the first useful field or action occupy a meaningfully different hierarchy?
3. Is the grouping of fields and context coherent with the stated idea?
4. Does the treatment change reading rhythm rather than merely add decoration?
5. Can the system carry shopping, finance, documents and meters without losing its identity?
6. Does the distinction survive keyboard operation and a narrow screen?

A unique configuration fingerprint is a structural check, not proof of good design. Reviewers must inspect rendered concepts. Shared renderers and reusable controls are desirable; the project does not claim 100 separately handwritten application implementations.

## 3. Color and Pink parity

| Mode | Palette source | What remains characteristic |
|---|---|---|
| Native Light | The direction's own light palette | Type, hierarchy, navigation, geometry and material |
| Native Dark | The direction's own dark palette | The same identity with readable dark surfaces |
| Pink Light | A common warm pink/plum semantic palette | The direction's structure and typography |
| Pink Dark | A common deep plum/rose semantic palette | The direction's structure and typography |

Pink Light and Pink Dark are available for **all 100 designs and every dialog**. Pink is not an exclusive design, limited category or separate subset. It changes the palette while retaining the concept's identity.

Native palettes must include distinct, purposeful color worlds rather than making every design Household green. Palette variety never excuses weak contrast or color-only meaning.

All colors and fonts flow through root/theme/design tokens. Semantic roles include canvas, surface, raised surface, primary and secondary text, control edge, separator, accent, accent foreground, focus, error, selection and shadow. Control edges and decorative rules have different contrast obligations.

Light and Dark are designed separately, never produced through CSS inversion. Pink foregrounds remain plum or rose-tinted as appropriate. Every action fill has a paired readable foreground. Glow, gradients and translucent surfaces cannot compensate for unreadable text.

Theme switching updates the gallery and open comparison. The same mode applies to every candidate, making both native-color and Pink comparisons fair. A decision export records the theme and direction.

## 4. Shared household dialogs

Every direction receives the same dialog definition, initial fixture values, field labels, requirements and validation bounds. Design changes presentation, not the facts being compared.

| Dialog | Shared task | Important distinction |
|---|---|---|
| Shopping | Add an example shopping item with quantity and unit | No application shopping entry is created. |
| Chores | Prepare a task, responsibility and due date | Responsibility and completion are separate concepts. |
| Inventory | Edit stock, minimum quantity and expiry | Stock, expiry and reorder intent remain distinguishable. |
| Finance | Enter an expense and sharing information | Units and aligned values remain visible. No booking occurs. |
| Recipes | Edit servings, duration and instructions | Longer text must not hide the form actions. |
| Meal planning | Assign a meal, date and serving count | Planning is distinct from cooking or stock consumption. |
| Fuel | Enter odometer reading, fuel quantity and cost | Units and full-tank meaning remain visible. |
| Meters | Enter a reading with its date and unit | A reading is not a live telemetry claim. |
| Documents | Describe a document and relevant date/reminder | No original file is uploaded, processed or stored. |
| Smart Home | Try room/device/setpoint controls | Explicit simulation, with no device integration. |

These dialogs stress-test short inputs, long text, numeric values, dates, selectors, switches, summaries and validation feedback. Do not give a favored design shorter copy or easier data. A layout can regroup fields or reposition its summary, but must preserve labels, accessible associations and required information.

## 5. Direct comparison workflow

1. **Browse:** show 100 concept names with their design idea and recognizable previews.
2. **Narrow:** search by name/ID and use broad design-group/favorite filters without erasing the shortlist.
3. **Shortlist:** add up to four directions. Favorites and comparison membership are separate states.
4. **Compare:** open a native dialog. Every selected direction renders the same active household dialog.
5. **Change the task:** choose one of the ten shared dialogs; all candidates change together.
6. **Change the palette:** compare native Light/Dark, then Pink Light/Dark. A common Pink palette exposes structural differences.
7. **Try controls:** edit example values and exercise validation. Synchronized editing is available and on by default; switch it off for independent trial edits.
8. **Choose:** mark one preferred direction for the application. This is a preference, not approval to modify production.
9. **Export:** download the decision or a standalone interactive component for the chosen direction and household dialog.

Synchronization concerns matching field values, not focus, caret position or validation feedback. Updating a sibling preview must not move focus or replace the input being edited. It needs no network request.

Changing household dialogs starts from the corresponding shared fixture. Make any reset of transient edits clear. Form reset is separate from clearing favorites, the shortlist or the final direction.

On desktop, compare candidates side by side only when each has a readable width. On narrow screens, retain complete dialogs and candidate names by stacking or explicit accessible navigation. Do not shrink four forms into unreadable columns.

### Native dialog behavior

- Give the comparison an accessible name and a visible close button.
- Move focus to useful content on opening and keep keyboard navigation within the modal.
- Support Escape and explicit close; restore focus to the opening control.
- Keep task/theme controls reachable while content scrolls.
- Avoid inaccessible nested modals.
- Associate validation messages with their fields instead of repeatedly announcing the whole comparison.

## 6. Saving and component export

Portable decision JSON records schema/version, export time, preview-only boundary, active theme, active household dialog, comparison shortlist, favorites and a final direction when chosen. Unknown IDs and unsupported themes are rejected when restoring preferences.

The shortlist is not four final application designs. A final direction is a separate choice, and saving an undecided comparison is useful.

A component export contains the chosen direction, current household dialog, shared example values, local CSS and preview interactions. The independent HTML file includes native Light, native Dark, Pink Light and Pink Dark controls and opens without the preview server or external assets.

The output is a reviewable HTML prototype, not a React component, Go route, production integration or transfer of household data. It identifies local-only feedback and simulated devices. Copying it into the application does not provide permissions, persistence or domain logic.

New preferences use a versioned contract distinct from the previous component gallery. Old v1 preferences and downloads are not silently mapped from HH component IDs to DS direction IDs. They can remain stored; the new laboratory does not reinterpret them as new choices.

If storage is denied or corrupt, browsing and comparison remain usable for the session. Communicate the limitation and preserve JSON export as the portable save path. No server-side or cross-device persistence is implied.

## 7. Typography, geometry and responsive rules

Typography uses declared local stacks. Humanist, editorial, mono, geometric, rounded, condensed, classical or grotesk character can support a direction. Name actual fallback behavior honestly; no font is secretly fetched.

Input text is at least 16 CSS pixels on mobile. Labels remain visible and in sentence case. Money, quantities and measurements use tabular numerals. Long German names wrap without losing meaning.

Use a four-pixel spacing scale, consistent inner/outer geometry and fixed control-border widths across states. Shapes can differ by design; hit targets, labels and focus must not become weaker.

Use shrinkable grid tracks, zero minimum widths for flexible children, bounded page width and document-level horizontal clipping. Fix inaccessible component overflow rather than concealing it. Deliberately scrollable regions need a clear purpose and keyboard access.

On phones, do not bury the first useful preview under a full screen of chrome. Put secondary filters behind a native disclosure. Keep theme choice, search and the shortlist discoverable. On larger screens, grow gutters before indefinitely stretching text or fields.

Motion explains state changes. Use specific transform/opacity transitions, never a transition of every property, layout-property animation or an endlessly animated gallery. Reduced motion removes spatial movement while preserving feedback.

## 8. Forms, states and accessibility

The task compares usable interfaces, not decorative cards. Form semantics stay consistent across designs.

| Situation | Required behavior |
|---|---|
| Default fixture | Show the same valid example values for every design. |
| Field edit | Preserve active input, selection and focus. |
| Missing or out-of-range value | Expose validation clearly and associate the message with its field. |
| Preview submit | Report a local validation/preview result, never a production save. |
| Reset | Restore the shared fixture and clear local feedback consistently. |
| No search matches | Explain and provide a clear-filter action. |
| Empty shortlist | Explain how to add designs and retain access to the gallery. |
| Four designs selected | Explain the limit; allow removal/replacement rather than silently dropping a choice. |
| Persistence unavailable | Keep the session usable and offer portable export. |
| Code export failure | Preserve the decision and allow retry. |
| JavaScript unavailable | Show a readable explanation and access to the guideline. |

Use native elements where possible. A switch retains a clear binary meaning. Every field has a persistent label, appropriate type/unit and accessible name. Required fields state the requirement in text rather than color alone.

Body text requires at least 4.5:1 contrast. Qualifying large text, meaningful control boundaries and focus indicators require at least 3:1. Check computed pairs in every native palette and both Pink modes, including validation and disabled explanations. Color cannot be the only signal.

Every interactive element has instant visible focus and a usable touch target. Labels, field order and actions remain available under zoom and keyboard operation. Decorative graphics are hidden from assistive technology; meaningful values are text-accessible.

Do not insert artificial loading or fake success just to demonstrate states. Future production loading, conflict, offline and provider-error flows require actual implementation and tests during adoption.

## 9. Implementation discipline

Separate direction definitions, shared fixtures, rendering, interaction and theme generation. Hand-authored design data is applied through reusable structures.

Ten layout grammars and a broader set of surface/control/navigation treatments support the catalog. Reuse is not an excuse for 100 renamed color swatches. Test fingerprints excluding name/ID/color, then judge the result in actual dialogs.

Store only bounded, validated preferences. Treat storage and URL parameters as untrusted. Escape fixture strings at the markup boundary; use text nodes for dynamic messages. Never generate executable code from form input.

The loopback server exposes an explicit public file allowlist. No production API, credential, authenticated session, remote font, tracker or device provider belongs in the prototype or its exports. Production CSP stays unchanged.

Avoid global listeners or animation timers per concept. Mount only necessary comparison forms and prefer ordinary browser controls over new dependencies. Bound verification processes and close temporary browser contexts; only the requested preview server remains.

## 10. Applying all Impeccable design commands

This maps all 23 commands to the task. It does not claim every CLI helper or contradictory redesign operation was executed. Actual checks belong in the validation record.

| Command | Application |
|---|---|
| craft | The ordinary build covers this deprecated alias; no duplicate build process. |
| shape | Define 100 full directions, common dialogs and the browse/compare/choose task. |
| init | Reuse PRODUCT.md instead of inventing claims or module behavior. |
| document | Document exploration here; do not replace production DESIGN.md with incompatible choices. |
| extract | Centralize tokens, semantics and fixtures while retaining structural differences. |
| critique | Judge recognizability, usefulness and coherence across household tasks. |
| audit | Check contrast, forms, modal behavior, reflow, privacy and generated output. |
| polish | Finish browsing, shortlist, comparison, choice and export after functional fixes. |
| bolder | Commit expressive concepts to their hierarchy and material rather than a common neutral shell. |
| quieter | Remove distracting decoration while retaining the concept's identity. |
| distill | Keep the workbench and forms focused without reducing the required catalog. |
| harden | Handle bad preferences, invalid fields, long labels, shortlist bounds and export failures. |
| onboard | Explain the short flow and local-only boundary without blocking first use. |
| animate | Use state feedback with reduced-motion parity, not theatrical entrance sequences. |
| colorize | Create native palettes and preserve Pink Light/Pink Dark for every direction and dialog. |
| typeset | Make type voice part of identity while retaining readable labels, values and long text. |
| layout | Vary hierarchy intentionally and compare identical content at readable widths. |
| delight | Make synchronization, shortlist changes and exports predictable and immediate. |
| overdrive | Put ambition into coherent systems and direct comparisons, not unrelated effects or integrations. |
| clarify | Separate favorite, comparison membership, final direction, local edit and export. |
| adapt | Preserve primary tasks on mobile, desktop, keyboard, touch and zoomed layouts. |
| optimize | Measure the gallery/comparison workload and bound dependencies, listeners and test processes. |
| live | Deliver live comparison without claiming an unrelated HMR/polling helper was started. |

Shortcut pinning, hooks and doctor/repair are tooling operations, not extra styles or permission to change unrelated configuration. Native iOS/Android branches do not apply.

## 11. Applying Hallmark

**Build:** the user wants many different design worlds. Do not restrict the catalog to the original three references or let an arbitrary theme draw override this scope. Each direction needs a concise visual argument and a recognizable arrangement in a real form.

**Redesign:** replace the earlier laboratory's three-family restriction while preserving household truth and the local-only boundary. This does not authorize redesigning the running app.

**Audit:** apply tokens, focus, contrast, responsive behavior, honest examples and motion constraints. Run available static/render checks, classify findings and disclose unavailable checks. Detector success does not establish that 100 designs differ meaningfully.

Use app-screen scope rather than a marketing hero, stock photography, invented customer metrics or fake operating-system windows. Form content is the material. Expressive geometry, type and bounded effects must preserve meaning and access.

The original Retro-Brutalist, Cyberpunk and Modern references remain historical inspiration. They no longer define the catalog boundaries or count.

## 12. Acceptance and adoption

These are requirements, not automatic completion claims:

- Verify exactly 100 DS IDs and names, with structural fingerprints excluding ID/name/color.
- Render every direction in every shared dialog with equal fields, values, labels and validation rules.
- Check native Light/Dark and Pink Light/Dark for every direction; retain structural distinction in Pink.
- Exercise editing, validation, local submit feedback, reset and synchronized/independent comparison.
- Check zero-to-four shortlist states, removal/replacement, final choice, favorites, filters, reload and malformed storage.
- Verify modal naming, focus containment, Escape, close and focus restoration.
- Parse JSON and open exported HTML independently; exercise its themes and controls without network dependence.
- Inspect complete dialogs on desktop/mobile, not only thumbnails; test long text, zoom, errors and contrast.
- Record errors, unexpected requests and resource observations without inventing performance scores.
- Preserve failed attempts, batch fixes and use bounded confirming review; record which skill tools ran.
- Deliver the access URL, guideline, catalog location, export instructions and exact evidence.

After selection, validate the preferred direction on a complete real workflow before global adoption. Preserve roles, module independence, exact money, document originals and established loading/conflict/offline behavior. Only an approved adoption step promotes selected tokens and conventions into production DESIGN.md.
