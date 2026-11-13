---
name: Household — Ocean / Ruhe
description: Calm household records, blue or rose actions, and clearly contained editing.
colors:
  ocean-light-ground: "#f4f8fe"
  ocean-light-surface: "#e5effb"
  ocean-light-ink: "#142638"
  ocean-light-muted: "#435b70"
  ocean-light-accent: "#075ca8"
  ocean-light-on-accent: "#ffffff"
  ocean-light-soft: "#cce5ff"
  ocean-light-line: "#bacbdc"
  ocean-light-control-border: "#607991"
  ocean-light-danger: "#a42337"
  ocean-light-warning: "#85500c"
  ocean-light-backdrop: "#07132280"
  ocean-dark-ground: "#0a111a"
  ocean-dark-surface: "#14212d"
  ocean-dark-ink: "#edf4fc"
  ocean-dark-muted: "#b3c5d7"
  ocean-dark-accent: "#87c2ff"
  ocean-dark-on-accent: "#09213b"
  ocean-dark-soft: "#1d3854"
  ocean-dark-line: "#415264"
  ocean-dark-control-border: "#859db4"
  ocean-dark-danger: "#ffacb8"
  ocean-dark-warning: "#f5c17a"
  ocean-dark-backdrop: "#03080dc2"
  pink-light-ground: "#fff6fb"
  pink-light-surface: "#f8e7f1"
  pink-light-ink: "#391a30"
  pink-light-muted: "#705069"
  pink-light-accent: "#a01967"
  pink-light-on-accent: "#ffffff"
  pink-light-soft: "#f5cee5"
  pink-light-line: "#d8b9cc"
  pink-light-control-border: "#98738b"
  pink-light-danger: "#aa2038"
  pink-light-warning: "#80500e"
  pink-light-backdrop: "#27102280"
  pink-dark-ground: "#190f1b"
  pink-dark-surface: "#2b192d"
  pink-dark-ink: "#fff0fa"
  pink-dark-muted: "#d5b5cf"
  pink-dark-accent: "#ffa1d5"
  pink-dark-on-accent: "#3a102b"
  pink-dark-soft: "#4a2542"
  pink-dark-line: "#684960"
  pink-dark-control-border: "#b58aa9"
  pink-dark-danger: "#ffafba"
  pink-dark-warning: "#f4c180"
  pink-dark-backdrop: "#100713c2"
typography:
  headline:
    fontFamily: "ui-sans-serif,system-ui,-apple-system,\"Segoe UI\",sans-serif"
    fontSize: "clamp(28px,3vw,34px)"
    fontWeight: 700
    lineHeight: 1.16
    letterSpacing: "-.025em"
  title:
    fontSize: "21px"
    lineHeight: 1.3
    letterSpacing: "-.45px"
  dialog-title:
    fontSize: "22px"
  body:
    fontFamily: "ui-sans-serif,system-ui,-apple-system,\"Segoe UI\",sans-serif"
    fontSize: "16px"
    lineHeight: 1.5
  label:
    fontSize: "14px"
    fontWeight: 600
  record-title:
    fontSize: "15px"
    fontWeight: 600
  fine:
    fontSize: "14px"
    lineHeight: 1.5
  mobile-nav-label:
    fontSize: "12px"
    fontWeight: 500
    lineHeight: 1.2
rounded:
  tab: "8px"
  control: "10px"
  notice: "12px"
  panel: "14px"
  auth: "16px"
spacing:
  compact: "8px"
  control-gap: "12px"
  inset: "16px"
  focus-clearance: "1rem"
  mobile-panel: "20px"
  panel: "24px"
  column-gap: "28px"
components:
  button-primary:
    backgroundColor: "{colors.ocean-light-accent}"
    textColor: "{colors.ocean-light-on-accent}"
    rounded: "{rounded.control}"
    padding: "10px 16px"
  button-secondary:
    backgroundColor: "{colors.ocean-light-surface}"
    textColor: "{colors.ocean-light-ink}"
    rounded: "{rounded.control}"
    padding: "10px 16px"
  button-secondary-hover:
    backgroundColor: "{colors.ocean-light-soft}"
    textColor: "{colors.ocean-light-ink}"
  input:
    backgroundColor: "{colors.ocean-light-ground}"
    textColor: "{colors.ocean-light-ink}"
    rounded: "{rounded.control}"
    padding: "11px 12px"
  nav-current:
    backgroundColor: "{colors.ocean-light-soft}"
    textColor: "{colors.ocean-light-accent}"
    rounded: "{rounded.control}"
    padding: "12px 14px"
  badge-active:
    backgroundColor: "{colors.ocean-light-soft}"
    textColor: "{colors.ocean-light-accent}"
    rounded: "30px"
    padding: "4px 9px"
  panel:
    backgroundColor: "{colors.ocean-light-surface}"
    textColor: "{colors.ocean-light-ink}"
    rounded: "{rounded.panel}"
    padding: "24px"
  record-row:
    textColor: "{colors.ocean-light-ink}"
    padding: "18px 24px"
  section-nav:
    textColor: "{colors.ocean-light-accent}"
  disclosure:
    textColor: "{colors.ocean-light-ink}"
---
# Design System: Household — Ocean / Ruhe

## Overview

**Creative North Star: "Ocean / Ruhe"**

Ocean / Ruhe gives everyday household records a calm, readable workspace. Ice-blue daylight and nearly black navy at night share the same hierarchy; the Pink variants carry it into pale rose and deep plum. Color identifies actions and state while records, labels, and quantities remain the focus.

This system records the implemented replacement selected on 2 November 2026. It supersedes the earlier green identity. The approved direction is in `docs/ocean-design-contract.md`; the implementation authority is the final cascade in `internal/web/static/app.css`, the shared templates, and the local appearance controller. The subsequent refinements in `docs/interface-refinement.md` and `docs/module-interface-polish.md` improve access, readability, and editing within this identity. Product capabilities remain defined by `PRODUCT.md`.

**Key Characteristics:**

- Separated records with thin dividers and restrained corners.
- Softly tinted editing surfaces and visible form labels.
- Local sans-serif, outline navigation icons, and tabular quantities.
- Four explicit palettes plus automatic system following for either family.

## Colors

Ocean uses cool blue neutrals and a clear blue action color; Pink uses rose neutrals and a plum or rose action color. The frontmatter holds the exact implemented values for each mode. Its component references show Ocean Light; live components resolve the same semantic roles from the selected palette. The sidecar binds component previews to those CSS properties. Its synthesized tonal ramps are preview metadata, not additional application tokens; translucent backdrops retain their authoritative alpha values in the frontmatter.

### Primary

`accent` identifies primary actions, links, current navigation, focus, checked controls, and progress. `on-accent` supplies the paired foreground; it is intentionally dark in dark themes. `soft` supports selection, hovered records, active navigation, and informational notices.

### Neutral

`ground` is the main canvas and input background. `surface` contains editing, authentication, and settings controls. `ink` serves headings and primary content; `muted` serves secondary content and placeholders. `line` separates records; `control-border` provides the stronger boundary for interactive controls. `backdrop` belongs only to modal overlays.

### State

`danger` marks destructive controls and errors; `warning` marks connection and warning states. These are semantic states, not additional brand accents. `--border` aliases `--line`; `--theme-color` aliases `--ground`.

**The Shared Palette Rule.** All modules and public screens inherit the same selected semantic palette; do not create module-specific light/dark color systems.

The six appearance choices are `system`, `light`, `dark`, `pink-system`, `pink-light`, and `pink-dark`. System follows the operating system within the selected Ocean or Pink family, including live OS changes. Explicit light choices stay light even on a dark operating system. `theme.js` runs synchronously before the shared stylesheet, restores the device preference, and synchronizes browser theme metadata. CSS owns native `color-scheme`. Saving settings applies and stores the choice only after the preferences API succeeds; failed device storage is reported. Storage events synchronize other tabs. Appearance remains a device selection with a saved account preference; the selected System mode is never replaced by its currently effective palette. During a save, the form announces its pending state, marks itself busy, and blocks duplicate submission. Persistent inline success or error feedback supplies the save result without a duplicate toast announcement; edits made while the request is pending are identified as unsaved. Failed saves leave the entered choice available to retry.

## Typography

Use the locally available sans-serif stack in the frontmatter throughout the interface; no font download is required. The hierarchy comes from size, weight, spacing, and contrast, without decorative display type. Tables, definition lists, and record rows use tabular numbers for quantities and amounts.

The headline role serves page titles; section titles use the title role and dialogs use the dialog-title role. Records use a compact semibold title with secondary metadata. Labels remain visible above fields. Inputs retain body size for readable mobile entry; fine text is for supporting detail, never the sole instruction for an important action. Shared labels and supporting text use 14px, including mobile record metadata. Mobile page headings use 30px and record titles 14px. These are existing responsive adjustments, not a separate type system. Component-specific compact captions may retain their own sizes; the shared secondary-text role is also exposed as `--text-secondary: .875rem`.

**The Visible Label Rule.** Placeholder text supplements a field label; it never replaces that label or the field's validation message.

## Layout

Desktop uses a fixed module sidebar (234px), a top tool bar (72px), and bounded main content (1460px maximum width) with 40px horizontal padding. At widths up to 1150px the sidebar becomes 210px and main padding becomes 28px. At widths up to 760px the sidebar gives way to a sticky, wrapping header with a 110px minimum height and fixed bottom module navigation; main content uses 18px horizontal padding and reserves space below for navigation. The single settings-menu button occupies the upper-right position on the first header row; search, notifications, appearance and language occupy the second. Logout belongs only to the settings tray. Bottom navigation includes the device safe-area inset. Mobile content reserves `calc(96px + env(safe-area-inset-bottom))` below; toasts sit above it using the same safe-area adjustment. Root scroll padding keeps keyboard targets clear of fixed controls, with 132px top clearance on mobile. Long text wraps within headings, notices, rows, and controls; direct panel/dialog paragraphs have a 75ch maximum measure.

Keep every enabled module in the user's saved order in both the desktop sidebar and mobile rail. The mobile rail scrolls horizontally rather than hiding destinations behind More; further items remain reachable by touch, pointer and keyboard. Reveal the current destination by scrolling only its rail, and keep its current-state tint and `aria-current="page"`. Settings remain separate behind the upper-right menu button. Long headings and primary actions wrap instead of colliding. Wide tables scroll within their container. Paired form fields and two-column content collapse to one column on narrow screens.

Record collections remain rows on the main canvas. Standard rows have a minimum height of 72px, metadata below their title, and a thin separator. Editing and grouped controls may use tinted panels; nested panels become transparent sections with a top divider. Module settings use rows with title, description, status, and action, including on mobile.

User administration shows the member list, with **Neuer Benutzer / New user** as its explicit creation action. Creation uses a labelled native modal, initially closed, with a focused username field, visible Cancel/close controls and Escape/backdrop dismissal. The dialog scrolls within narrow viewports, returns focus to its opener and clears unsaved credentials on dismissal. Recent-authentication confirmation opens above it while preserving the draft; cancelling that confirmation returns to the creation dialog. Pending submission prevents accidental dismissal. No role or authorization decision belongs in the presentation layer.

Shopping is a deliberate module-specific composition: at widths of at least 1050px, a 280–330px entry panel sits beside the record list with a 28px gap; narrower screens retain the natural stacked flow. Do not generalize that exact composition to every module. Fuel quick capture keeps its vehicle selector and apply action visible while placing secondary search, date, quantity, reload, and export controls inside the native “Zeitraum und Export” disclosure. This hierarchy applies to capture; other Fuel panes retain their working filter layout.

The spacing tokens describe reused values, not a mandatory arithmetic scale. Action groups wrap with a 12px gap. Preference buttons have explicit surrounding separation, and successive domain action forms have vertical separation. Keep these independent controls visually distinct. Settings has shared route navigation plus in-page links to device preferences and shopping notifications; its links have 44px minimum targets and stable focusable destinations. At widths up to 360px, panel padding reduces to 16px and module-setting action forms move onto a separate row.

At widths up to 760px, shared action controls wrap from a 12rem flex basis and stay within their container; a narrow column can place each action on its own row. Shared search inputs may shrink while their search button keeps its natural width and unbroken label. Module-specific control layouts retain their own scoped rules. The Documents review workspace switches its preview and editor to one column at 1100px; its metadata fields remain paired until the narrow-screen rules collapse them.

## Elevation & Depth

Everyday panels are flat (`--shadow: none`). Ground, surface tint, and thin dividers communicate grouping. Only native modal dialogs use the palette-specific diffuse shadow and dimmed backdrop recorded in the sidecar. Do not give ordinary records dialog-like elevation.

**The Quiet Surface Rule.** Use a tinted surface to contain editing and a divider to separate records; reserve overlay depth for actual overlays.

Motion is conditional on `prefers-reduced-motion: no-preference`: shared button/link/input color transitions take 150ms; the final dialog entrance takes 160ms with `cubic-bezier(.16,1,.3,1)`, translating upward from 8px while restoring its shadow. Reduced-motion users receive neither effect. The earlier `appear` keyframe in the stylesheet is superseded by the final Ocean dialog animation.

## Shapes

Controls have gently rounded corners; notices, editing panels, and authentication surfaces use the progressively larger radii in the frontmatter. Mobile panels use 13px corners. Rows and module settings remain square and separated by a one-pixel line. Current tabs retain a two-pixel accent underline with rounded top corners. Status badges are compact pills; circular completion controls retain their existing functional silhouette.

Navigation icons come from the bundled outline SVG system, typically 22px with a 1.8px stroke. Do not introduce remote icon fonts or a new illustration system. The local house mark remains the identity asset.

## Components

### Buttons

Primary buttons use `accent` with `on-accent`; secondary buttons use `surface`, `ink`, and the stronger control border. Secondary hover uses `soft` and an accent border. Primary hover keeps its palette and underlines the label. Destructive controls use `danger`; subtle controls may have a transparent background. Shared buttons have a 44px minimum height, semibold labels, and a 1.35 line height. Mobile module-setting and list-toolbar action labels use 14px. Disabled buttons use half opacity and a default cursor. Keyboard-focusable controls use the shared three-pixel accent outline with a three-pixel offset; tab strips and mobile navigation draw it inward to prevent clipping. Pressed enabled buttons use a subtle brightness change; disabled hover does not imitate an enabled action. Busy buttons use a progress cursor. In Windows forced-colors mode, control borders use `ButtonText`, current navigation and focus use `Highlight`, and panel boundaries use `CanvasText`; preserve browser color adaptation.

### Inputs / Fields

Native input, select, and textarea controls use `ground`, `ink`, and a one-pixel `control-border`, with a 46px minimum height. Focus changes the border to the accent; the global visible-focus outline remains. Placeholders use `muted`, and text carets use the accent. Checkbox and progress accents follow the palette. Field errors appear in danger-colored text; failed requests and conflict states retain readable explanations and recovery actions.

Finance reference selectors keep one visible label for the selected value. Their search field, search/paging buttons and result notice sit outside that label within the same control group, so searching does not enlarge the selector's accessible name. Documents keeps full-text search and the deadline visible, with type, review state, issuer and category under “Weitere Filter”; the summary counts active secondary filters. Collapsing the disclosure preserves their submitted values. Saved-view controls have their own disclosure, and batch actions appear only when documents are selected.

Chores hides entire recurrence and fixed-person groups when they do not apply, including their labels and search controls. Hidden draft choices remain available when the mode is restored; submitted commands still normalize inactive choices. A daily completion anchor remains a draft choice while weekly rules use a calendar anchor. Actual unsaved series edits, including added or removed rows, trigger the browser's leave-page confirmation. Loading, reference search, reverting to the loaded values and preview rendering do not create false unsaved changes. Edits invalidate the checked preview, including edits made while a preview is loading; a confirmed save bypasses the leave-page prompt only when the current draft still matches the submitted one.

### Navigation

Desktop module links are compact icon-and-label rows with a 48px minimum height. Hover and current state use `soft` with accent text. Entity tabs and the mobile module rail scroll horizontally when necessary. The active destination is revealed inside its strip after initialization, current-state updates, and resizing; only the strip moves, preserving the page position. Mobile bottom destinations stack their icon above their 12px label with a 52px minimum height; the selected destination is softly tinted. All enabled module links remain in the rail. The separate settings tray uses native dialog focus handling and a single column of individual settings links at every width.

The Recipe editor uses wrapping native section links to Grunddaten, Zutaten, Zubereitung, Tags and, for writers, Speichern. Each link has a 44px minimum target and points to a stable focusable heading. These links move within the existing form without creating a separate editing mode.

### Records and module settings

Rows use a strong title, secondary metadata, tabular values, and trailing contextual actions. Hover and focus within a record add `soft`; completed records use muted, struck-through titles. Module settings use the same row language, with compact active/paused badges and an explicit activate/pause button. Status is communicated in text as well as color.

Recipe tag names remain fully visible, including unbroken names up to the accepted 80-character limit. Tag rows wrap when necessary; the remove button retains its natural width and one-line label instead of squeezing between letters. Removing a tag by keyboard returns focus to the tag selector. These scoped rules retain the shared four-palette controls and touch targets.

Finance entry totals use a native “Gesamte berechtigte Auswahl auswerten” disclosure; warnings remain visible outside it. Compact Finance rows label each fact and separate their values from the action group. Shared report tables remain keyboard-focusable horizontal regions. Their scrolling hint appears only when measured content overflows and updates on resize. Loading and failure states remain visible; failed or timed-out reports provide “Erneut laden” without replacing exact values with invented totals.

### Panels, notices, and dialogs

Panels contain forms or coherent control groups; they do not wrap each record in a separate card. Informational notices use a soft fill and accent border. Error notices use surface fill and danger text/border. Native dialogs have a bounded width (up to 700px with 16px viewport gutters), constrained height, a visible close control, and clearly grouped actions. They preserve the application's established focus and dismissal behavior. Dialog scrolling is contained with interior scroll padding, and the close control cannot shrink. Native disclosure summaries share a 44px minimum target and retain their native open/closed affordance; keyboard focus adds gently rounded corners. Toasts announce results through a polite live region; errors must remain actionable outside transient feedback when the workflow requires it.

## Do's and Don'ts

### Do:

- **Do** use semantic CSS properties so every module follows all four palettes.
- **Do** keep labels, headings, native controls, focus, and selection legible in every theme.
- **Do** keep independent actions visibly separated and consequential actions explicit.
- **Do** preserve keyboard focus, native dialog behavior, and the reduced-motion preference.
- **Do** retain real empty, loading, error, conflict, and offline states.

### Don't:

- **Don't** restore the former green palette or hard-code light text on a light surface.
- **Don't** turn record collections or module settings into grids of floating cards.
- **Don't** add external fonts, frontend CDN dependencies, tracking scripts, or decorative gradients.
- **Don't** add decorative eyebrows or substitute text glyphs for the bundled outline icon system.
- **Don't** infer a passed operational or accessibility check from the existence of these design documents.

## Settings and account navigation

General, Modules, Security, Users and Audit are independent links in the right-side settings tray, with `aria-current` and role-aware administrative destinations. There is no horizontal settings tab strip. A single button at the far upper right opens the native dialog; the desktop sidebar and mobile rail contain no duplicate menu opener. The tray is at most 344px wide, retains a 24px backdrop on narrow screens, and fills the dynamic viewport height. Account identity and close control remain above a bounded scrolling region containing only settings destinations; installation and the sole logout action remain below it. Module destinations stay in the desktop sidebar or mobile rail. Escape, close and backdrop dismissal restore opener focus. Only the tray content scrolls to reveal the current settings destination, and reduced motion disables its short side entrance.

Appearance uses a neutral outline sun and language uses DE/EN shorthand, both with transparent resting backgrounds, translated accessible names and 44px targets. Mobile retains the established two-row header and focus clearance, with the settings-menu button in its upper-right corner. Ocean and Pink each offer Light, Dark and System; the selected System mode remains distinct from its currently effective palette.

General provides a personal ordered module list. Desktop pointer dragging, named up/down buttons and arrow keys on the drag handle provide equivalent ordering actions. Rows include paused modules with an explicit state label, so pausing never discards their position. Changes remain a draft until Save; a failed save retains that draft and restores the retry control's focus. A successful save updates both visible rails and focuses its confirmation; Reset immediately clears the stored override and retains focus on its action. Reordering affects only the signed-in account’s module rails and Today shortcuts. Today remains the home destination. A later page request or another signed-in device reads the persisted order. All active modules remain reachable; personal ordering neither activates nor deactivates shared modules.

Audit uses compact disclosure rows with current household-scoped usernames, action/entity summaries and expandable before/after values. Technical IDs are secondary. Missing, null, protected and legacy values stay distinguishable; existing financial visibility rules apply.

German and English are authored application copy. User titles, notes, values, imports and technical identifiers are never translated. Static application labels, accessible names, validation and state messages use explicit embedded catalog calls. Presentation dates/numbers follow the chosen language without changing civil dates, currency or exact monetary command values. Public offline shells have separate validated language cache keys and contain no account data or CSRF tokens.

## Workspace information hierarchy

Use two or three primary module destinations, a short contextual row for the active destination, and a separate Manage page for setup/reference data. Avoid presenting actions as permanent tabs. Preserve selected-record context and unsaved-change confirmation during navigation. Today is the home; the module order remains personal. Ocean/Pink and system-aware light/dark modes use the existing shared tokens; all authored navigation copy supports DE/EN.
