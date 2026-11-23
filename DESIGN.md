---
name: Household — Warm Midnight
description: Warm household records, plum/copper navigation and navy/mint administration.
colors:
  warm-light-ground: "#faf5ee"
  warm-light-surface: "#fff9f3"
  warm-light-ink: "#271922"
  warm-light-muted: "#725a61"
  warm-light-accent: "#913f29"
  warm-light-on-accent: "#ffffff"
  warm-light-soft: "#f3dfd2"
  warm-light-line: "#ddc6b9"
  warm-light-control-border: "#967365"
  warm-light-danger: "#a42337"
  warm-light-warning: "#85500c"
  warm-light-backdrop: "#07132280"
  warm-dark-ground: "#19151d"
  warm-dark-surface: "#29212c"
  warm-dark-ink: "#fff2eb"
  warm-dark-muted: "#d8c3cf"
  warm-dark-accent: "#efb194"
  warm-dark-on-accent: "#2b1420"
  warm-dark-soft: "#46303c"
  warm-dark-line: "#705663"
  warm-dark-control-border: "#b99ba7"
  warm-dark-danger: "#ffacb8"
  warm-dark-warning: "#f5c17a"
  warm-dark-backdrop: "#03080dc2"
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
  nav-ground: "#241322"
  nav-ink: "#fff2eb"
  nav-selected: "#c77358"
  nav-selected-ink: "#271922"
  admin-ground: "#07131f"
  admin-surface: "#0d2233"
  admin-ink: "#f1f6fc"
  admin-accent: "#67e8c7"
typography:
  headline:
    fontFamily: Georgia,ui-serif,serif
    fontSize: clamp(36px,4.8vw,75px)
    fontWeight: 600
    lineHeight: 1.04
    letterSpacing: -.035em
  title:
    fontSize: 21px
    lineHeight: 1.3
    letterSpacing: -.45px
  dialog-title:
    fontSize: 22px
  body:
    fontFamily: ui-sans-serif,system-ui,-apple-system,"Segoe UI",sans-serif
    fontSize: 16px
    lineHeight: 1.5
  label:
    fontSize: 14px
    fontWeight: 600
  record-title:
    fontSize: 15px
    fontWeight: 600
  fine:
    fontSize: 14px
    lineHeight: 1.5
  mobile-nav-label:
    fontSize: 12px
    fontWeight: 500
    lineHeight: 1.2
  admin-headline:
    fontFamily: ui-sans-serif,system-ui,-apple-system,"Segoe UI",sans-serif
    fontSize: clamp(30px,3vw,40px)
    lineHeight: 1.2
    letterSpacing: -.02em
rounded:
  tab: 8px
  control: 10px
  notice: 12px
  panel: 14px
  auth: 16px
spacing:
  compact: 8px
  control-gap: 12px
  inset: 16px
  focus-clearance: 1rem
  mobile-panel: 20px
  panel: 24px
  column-gap: 28px
components:
  button-primary:
    backgroundColor: "{colors.warm-light-accent}"
    textColor: "{colors.warm-light-on-accent}"
    rounded: "{rounded.control}"
    padding: 10px 16px
  button-secondary:
    backgroundColor: "{colors.warm-light-surface}"
    textColor: "{colors.warm-light-ink}"
    rounded: "{rounded.control}"
    padding: 10px 16px
  button-secondary-hover:
    backgroundColor: "{colors.warm-light-soft}"
    textColor: "{colors.warm-light-ink}"
  input:
    backgroundColor: "{colors.warm-light-ground}"
    textColor: "{colors.warm-light-ink}"
    rounded: "{rounded.control}"
    padding: 11px 12px
  nav-current:
    backgroundColor: "{colors.nav-selected}"
    textColor: "{colors.nav-selected-ink}"
    rounded: "{rounded.control}"
    padding: 12px 14px
  badge-active:
    backgroundColor: "{colors.warm-light-soft}"
    textColor: "{colors.warm-light-accent}"
    rounded: 30px
    padding: 4px 9px
  panel:
    backgroundColor: "{colors.warm-light-surface}"
    textColor: "{colors.warm-light-ink}"
    rounded: "{rounded.panel}"
    padding: 24px
  record-row:
    textColor: "{colors.warm-light-ink}"
    padding: 18px 24px
  section-nav:
    textColor: "{colors.warm-light-accent}"
  disclosure:
    textColor: "{colors.warm-light-ink}"
---
# Design System: Household — Warm Midnight

## Approved replacement, 20 November 2026

The approved Warm Midnight package (17 references, 19 November) replaces Ocean. The rollout and test evidence are tracked in `docs/warm-midnight-rollout.md`. The tokens above describe the current implementation; runtime CSS remains authoritative.

Consumer surfaces use warm ivory in standard light and warm charcoal/plum in standard dark, with burgundy or light editorial serif page titles. Interface text remains system sans. Pink retains its light/dark palettes. Navigation uses independent plum/copper tokens, with dark plum text on the copper selection in Warm Midnight and on the rose selection in Pink. The persisted theme values remain `system`, `light`, `dark`, `pink-system`, `pink-light`, `pink-dark`; the visible standard family is Warm Midnight. System mode continues to follow the OS.

Administrative content deliberately uses dark Midnight Atlas, independent of consumer brightness. Its dialogs inherit that scoped palette; the right account drawer uses plum/copper. No reference dataset, photo or chart is production content. Existing navigation, review, permissions, accessibility, localization and privacy contracts remain binding.

## Overview

**Creative North Star: "Warm Midnight"**

Warm Midnight pairs warm ivory or charcoal household workspaces with editorial serif page titles and plum/copper navigation. Pink carries the same hierarchy into rose and deep plum. Navy/mint panels distinguish real technical data and administrative controls. Records, labels and exact quantities remain the focus.

This system records the replacement approved on 20 November 2026. The implementation authority is the shared CSS, templates and appearance controller. Earlier Ocean documents remain historical; their functional navigation, review and privacy contracts remain applicable. Product capabilities remain defined by `PRODUCT.md`.

**Key Characteristics:**

- Separated records with thin dividers and restrained corners.
- Softly tinted editing surfaces and visible form labels.
- Local serif page titles and editorial card headings, system sans interface text, outline icons and tabular quantities.
- Four explicit palettes plus automatic system following for either family.

## Colors

Warm Midnight uses ivory/plum neutrals and copper actions; Pink uses rose neutrals and magenta accents. The frontmatter holds the implemented palette values. Consumer component references show Warm Light. Navigation uses its own paired plum/copper roles (Pink selection in Pink modes), while data panels and administration use navy/mint roles. Sidecar previews use these same semantic properties; historical warm-family tonal ramps were removed rather than retained with an incorrect hue.

### Primary

`accent` identifies primary actions, links, current navigation, focus, checked controls, and progress. `on-accent` supplies the paired foreground; it is intentionally dark in dark themes. `soft` supports selection, hovered records, active navigation, and informational notices.

### Neutral

`ground` is the main canvas and input background. `surface` contains editing, authentication, and settings controls. `ink` serves headings and primary content; `muted` serves secondary content and placeholders. `line` separates records; `control-border` provides the stronger boundary for interactive controls. `backdrop` belongs only to modal overlays.

### State

`danger` marks destructive controls and errors; `warning` marks connection and warning states. These are semantic states, not additional brand accents. `--border` aliases `--line`; `--theme-color` aliases `--ground`.

**The Shared Palette Rule.** Household modules and public screens inherit the selected palette. Navigation, technical data panels and Admin C are explicit scoped exceptions with paired foreground/background tokens, including readable warning and error states. Administration uses native dark controls even when the household theme is light. The right tray remains plum/copper.

The six appearance choices are `system`, `light`, `dark`, `pink-system`, `pink-light`, and `pink-dark`. System follows the operating system within the selected Warm Midnight or Pink family, including live OS changes. Explicit light choices stay light even on a dark operating system. `theme.js` runs synchronously before the shared stylesheet, restores the device preference, and synchronizes browser theme metadata. CSS owns native `color-scheme`. Saving settings applies and stores the choice only after the preferences API succeeds; failed device storage is reported. Storage events synchronize other tabs. Appearance remains a device selection with a saved account preference; the selected System mode is never replaced by its currently effective palette. During a save, the form announces its pending state, marks itself busy, and blocks duplicate submission. Persistent inline success or error feedback supplies the save result without a duplicate toast announcement; edits made while the request is pending are identified as unsaved. Failed saves leave the entered choice available to retry.

## Typography

Use the local serif stack for consumer page titles and selected editorial card headings. Use the system sans stack for controls, records and administrative titles. No font download is required. Consumer headings scale from 36px to 75px; administrative headings from 30px to 40px. Tables, definition lists and record rows use tabular numbers for quantities and amounts.

The headline role serves page titles; section titles use the title role and dialogs use the dialog-title role. Records use a compact semibold title with secondary metadata. Labels remain visible above fields. Inputs retain body size for readable mobile entry; fine text is for supporting detail, never the sole instruction for an important action. Shared labels and supporting text use 14px, including mobile record metadata. Consumer page headings start at 32px; administrative titles start at 30px. Shared mobile record titles retain their compact sizing. These are existing responsive adjustments, not a separate type system. Component-specific compact captions may retain their own sizes; the shared secondary-text role is also exposed as `--text-secondary: .875rem`.

**The Visible Label Rule.** Placeholder text supplements a field label; it never replaces that label or the field's validation message.

## Layout

Desktop uses a fixed module sidebar (234px), a top tool bar (72px), and bounded main content (1460px maximum width) with 40px horizontal padding. At widths up to 1150px the sidebar becomes 210px and main padding becomes 28px. At widths up to 760px a single-row header groups the brand, search, notifications, appearance, language and the rightmost account button. The bottom module rail respects the device safe area. Content and toasts clear the rail, and the header leaves 80px of scroll clearance for keyboard and anchor navigation. Long text wraps in headings, notices and controls, and direct panel and dialog paragraphs have a 75ch maximum measure.

Keep every enabled module in the user's saved order in both the desktop sidebar and mobile rail. The mobile rail scrolls horizontally rather than hiding destinations behind More; when there are more than five destinations, a partial next tile signals that the rail scrolls. All destinations remain reachable by touch, pointer and keyboard. Reveal the current destination by scrolling only its rail, and keep its current-state tint and `aria-current="page"`. Settings remain separate behind the upper-right menu button. Long headings and primary actions wrap instead of colliding. Wide tables scroll within their container. Paired form fields and two-column content collapse to one column on narrow screens. Put the workspace title before its navigation. On screens up to 600px, three or more secondary destinations share a native disclosure with the current destination in its summary. Primary destinations, actions and the management entry stay directly reachable.

Record collections remain rows on the main canvas. Standard rows have a minimum height of 72px, metadata below their title, and a thin separator. Editing and grouped controls may use tinted panels; nested panels become transparent sections with a top divider. Module settings use rows with title, description, status, and action, including on mobile.

User administration shows the member list, with **Neuer Benutzer / New user** as its explicit creation action. Creation uses a labelled native modal, initially closed, with a focused username field, visible Cancel/close controls and Escape/backdrop dismissal. The dialog scrolls within narrow viewports, returns focus to its opener and clears unsaved credentials on dismissal. Recent-authentication confirmation opens above it while preserving the draft; cancelling that confirmation returns to the creation dialog. Pending submission prevents accidental dismissal. No role or authorization decision belongs in the presentation layer.

Shopping is a deliberate module-specific composition: at widths of at least 1050px, a 280–320px entry panel sits beside the record list with a 24px gap; narrower screens retain the natural stacked flow. Do not generalize that exact composition to every module. Fuel quick capture keeps its vehicle selector and apply action visible while placing secondary search, date, quantity, reload, and export controls inside the native “Zeitraum und Export” disclosure. This hierarchy applies to capture; other Fuel panes retain their working filter layout.

The spacing tokens describe reused values, not a mandatory arithmetic scale. Action groups wrap with a 12px gap. Preference buttons have explicit surrounding separation, and successive domain action forms have vertical separation. Keep these independent controls visually distinct. Settings has shared route navigation plus in-page links to device preferences and shopping notifications; its links have 44px minimum targets and stable focusable destinations. At widths up to 360px, panel padding reduces to 16px and module-setting action forms move onto a separate row.

At widths up to 760px, shared action controls wrap from a 12rem flex basis and stay within their container; a narrow column can place each action on its own row. Shared search inputs may shrink while their search button keeps its natural width and unbroken label. Module-specific control layouts retain their own scoped rules. The Documents review workspace switches its preview and editor to one column at 1100px; its metadata fields remain paired until the narrow-screen rules collapse them.

## Elevation & Depth

Use subtle paper gradients and restrained shadows on household feature cards. Ordinary list rows use fine dividers. Native dialogs use the stronger overlay shadow and a dimmed backdrop.

Use tinted surfaces for editing, thin dividers for records and the copper-to-plum navigation material in the shared shell.

Motion is conditional on `prefers-reduced-motion: no-preference`: shared button/link/input color transitions take 150ms; the final dialog entrance takes 160ms with `cubic-bezier(.16,1,.3,1)`, translating upward from 8px while restoring its shadow. Reduced-motion users receive neither effect. The earlier `appear` keyframe in the stylesheet is superseded by the final shared dialog animation.

## Shapes

Controls have gently rounded corners; notices, editing panels, and authentication surfaces use the progressively larger radii in the frontmatter. Mobile panels use 13px corners. Rows and module settings remain square and separated by a one-pixel line. Current tabs retain a two-pixel accent underline with rounded top corners. Status badges are compact pills; circular completion controls retain their existing functional silhouette.

Navigation icons come from the bundled outline SVG system, typically 22px with a 1.8px stroke. Do not introduce remote icon fonts or a new illustration system. The Household mark uses two staggered H letters and an ascending slash (Staffel). Show the mark alone on sign-in and in desktop and mobile navigation. Each brand link has the accessible name Household. The letters inherit the link color and the slash uses the theme accent. The standalone icon uses the Pink Dark palette.

## Components

### Buttons

Primary buttons use `accent` with `on-accent`; secondary buttons use `surface`, `ink`, and the stronger control border. Secondary hover uses `soft` and an accent border. Primary hover keeps its palette and underlines the label. Destructive controls use `danger`; subtle controls may have a transparent background. Shared buttons have a 44px minimum height, semibold labels, and a 1.35 line height. Mobile module-setting and list-toolbar action labels use 14px. Disabled buttons use half opacity and a default cursor. Keyboard-focusable controls use the shared three-pixel accent outline with a three-pixel offset; tab strips and mobile navigation draw it inward to prevent clipping. Pressed enabled buttons use a subtle brightness change; disabled hover does not imitate an enabled action. Busy buttons use a progress cursor. In Windows forced-colors mode, control borders use `ButtonText`, current navigation and focus use `Highlight`, and panel boundaries use `CanvasText`; preserve browser color adaptation.

### Inputs / Fields

Native input, select, and textarea controls use `ground`, `ink`, and a one-pixel `control-border`, with a 46px minimum height. Focus changes the border to the accent; the global visible-focus outline remains. Placeholders use `muted`, and text carets use the accent. Checkbox and progress accents follow the palette. Field errors appear in danger-colored text; failed requests and conflict states retain readable explanations and recovery actions.

Finance reference selectors keep one visible label for the selected value. Their search field, search/paging buttons and result notice sit outside that label within the same control group, so searching does not enlarge the selector's accessible name. Documents keeps full-text search and the deadline visible, with type, review state, issuer and category under “Weitere Filter”; the summary counts active secondary filters. Collapsing the disclosure preserves their submitted values. Saved-view controls have their own disclosure, and batch actions appear only when documents are selected.

Chores hides entire recurrence and fixed-person groups when they do not apply, including their labels and search controls. Hidden draft choices remain available when the mode is restored; submitted commands still normalize inactive choices. A daily completion anchor remains a draft choice while weekly rules use a calendar anchor. Actual unsaved series edits, including added or removed rows, trigger the browser's leave-page confirmation. Loading, reference search, reverting to the loaded values and preview rendering do not create false unsaved changes. Edits invalidate the checked preview, including edits made while a preview is loading; a confirmed save bypasses the leave-page prompt only when the current draft still matches the submitted one.

### Navigation

Desktop module links are compact icon-and-label rows with a 48px minimum height. Hover and current state use paired navigation-selection colors. Entity tabs and the mobile module rail scroll horizontally when necessary. The active destination is revealed inside its strip after initialization, current-state updates, and resizing; only the strip moves, preserving the page position. Mobile bottom destinations stack their icon above their 12px label with a 52px minimum height; selection uses copper or Pink with its paired dark foreground. All enabled module links remain in the rail. The separate settings tray uses native dialog focus handling and a single column of individual settings links at every width.

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

General, Modules, Notifications, Security, Users and Audit are independent links in the right-side settings tray, with `aria-current` and role-aware administrative destinations. There is no horizontal settings tab strip. A single button at the far upper right opens the native dialog; the desktop sidebar and mobile rail contain no duplicate menu opener. The tray is at most 344px wide, retains a 24px backdrop on narrow screens, and fills the dynamic viewport height. Account identity and close control remain above a bounded scrolling region containing only settings destinations; installation and the sole logout action remain below it. Module destinations stay in the desktop sidebar or mobile rail. Escape, close and backdrop dismissal restore opener focus. Only the tray content scrolls to reveal the current settings destination, and reduced motion disables its short side entrance.

Appearance uses a neutral outline sun and language uses DE/EN shorthand, both with transparent resting backgrounds, translated accessible names and 44px targets. On mobile, the account button opens settings from the right end of the single-row header. Focused content clears the header. Warm Midnight and Pink each offer Light, Dark and System; the selected System mode remains distinct from its currently effective palette.

General provides a personal ordered module list. Desktop pointer dragging, named up/down buttons and arrow keys on the drag handle provide equivalent ordering actions. Rows include paused modules with an explicit state label, so pausing never discards their position. Changes remain a draft until Save; a failed save retains that draft and restores the retry control's focus. A successful save updates both visible rails and focuses its confirmation; Reset immediately clears the stored override and retains focus on its action. Reordering affects only the signed-in account’s module rails and Today shortcuts. Today remains the home destination. A later page request or another signed-in device reads the persisted order. All active modules remain reachable; personal ordering neither activates nor deactivates shared modules.

Audit uses compact disclosure rows with current household-scoped usernames, action/entity summaries and expandable before/after values. Technical IDs are secondary. Missing, null, protected and legacy values stay distinguishable; existing financial visibility rules apply.

German and English are authored application copy. User titles, notes, values, imports and technical identifiers are never translated. Static application labels, accessible names, validation and state messages use explicit embedded catalog calls. Presentation dates/numbers follow the chosen language without changing civil dates, currency or exact monetary command values. Public offline shells have separate validated language cache keys and contain no account data or CSRF tokens.

## Workspace information hierarchy

Use two or three primary module destinations, a short contextual row for the active destination, and a separate Manage page for setup/reference data. Avoid presenting actions as permanent tabs. Preserve selected-record context and unsaved-change confirmation during navigation. Today is the home; the module order remains personal. Warm Midnight/Pink and system-aware light/dark modes use the existing shared tokens; all authored navigation copy supports DE/EN.

### Approved Warm Midnight screen references

The 17 approved household and administrative PNGs are mapped to application routes in [the visual review](docs/visual-fidelity.md). Standard light and system on a light device use the ivory/navy composition.

Use the transparent copper branch in the household header, the Staffel monogram in the sidebar and the vector sprig in the sidebar and account drawer. Use the market bag as decoration for shopping. Recipe imagery comes from the recipe's authorized attachment. The empty photo treatment uses the book icon. Paper gradients, fine outlines, restrained shadows and serif card headings distinguish household records from the navy administrative surfaces. Financial amounts and task completion use explicitly labeled scales.
