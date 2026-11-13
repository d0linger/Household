# Household interface refinement — 2 November 2026

## Brief and scope

Refine the established Ocean/Ruhe interface for household members completing daily tasks on mobile and desktop. Preserve Ocean Light/Dark, Pink Light/Dark, local assets, German product copy, authorization, exact quantities and explicit confirmation flows. The production app and all nine modules share this pass; the alternative-design gallery remains unchanged.

Shape decisions follow the already approved product/direction: put entry ahead of reporting controls, make current location visible, provide direct access to device preferences, and use consistent readable guidance and focus treatment. No new visual world or business capability is proposed. Questions skipped: the user authorized the combined critique and implementation and the audience, stack and direction are settled.

## Independent critique

Assessment A and B run in separate agents. A completed before B's detector findings were read. A scores the interface **28/40** across Nielsen's ten heuristics: status 3, real-world match 3, control 3, consistency 3, prevention 3, recognition 3, efficiency 2, minimalism 3, recovery 3, help 2. The identity and domain language are appropriate; the main weakness is reaching daily actions through secondary controls.

Priority findings:

1. Fuel capture puts reporting filters/export ahead of capture. Keep the vehicle authority selector visible and disclose secondary filters on this pane.
2. Mobile More does not identify the active destination when it contains the selected module. Preserve exact current semantics in the dialog and visible location context.
3. Horizontal section navigation can leave the active destination outside the viewport. Reveal it inside its own scroller, without moving the page.
4. Device preferences follow all nine module rows. Add direct section navigation and a stable target.

The detector's template-only color, typography and image findings need verification against rendered styles and dynamic image behavior; unresolved generated-template paths are not product defects by themselves.

## Checklist

- [x] Review current Ocean screenshots, source and the approved design system.
- [x] Complete independent design assessment A and technical assessment B's static scan.
- [x] Define the refinement structure and keep all four themes.
- [x] Implement shared typography, focus, wrapping, safe-area and high-contrast refinements.
- [x] Implement Fuel capture hierarchy and settings section navigation.
- [x] Implement current mobile/section navigation and robust preference save feedback.
- [x] Measure baseline and final browser performance; optimize evidenced bottlenecks.
- [x] Run focused behavior tests and the full existing Node suite.
- [x] Run appropriate Go/template checks.
- [x] Inspect a batched desktop/mobile/theme round with keyboard and failure-state checks.
- [x] Fix findings in one batch and run a bounded confirmation if required.
- [x] Complete independent finish review and update design documentation.
- [x] Rebuild/recreate local Docker, verify changed assets and health, preserve household data.
- [x] Remove owned test resources and record actual evidence.

## Verification record

Implemented and checked on isolated synthetic data. Live development data is reserved for read-only deployment smoke checks.

| Check | Actual result |
|---|---|
| Full Node suite | 352/352 passed after final fixes, including 21 theme/navigation/save cases and 21 push/service-worker cases |
| Full ordinary Go suite | All packages passed with two Go CPUs and serial package execution; includes rendered offline dependency and worker version tests |
| Migration/workflow policy | 108 migration checksums unchanged; workflow policy passed |
| Broad theme matrix | 203 checks passed and 60 Axe scans were clean; one asynchronous OS-theme assertion failed, retained in the report |
| Focused theme confirmation | All eight checks passed after the test awaited the queued matchMedia event, covering real save/reload, OS/cross-tab changes, denied storage and offline shells |
| Refinement interaction matrix | 157 checks and four Axe scans passed: 320px reflow, long headings, preferences failure/retry, navigation/current state, disclosure, keyboard focus, 768/1440px layouts and forced-colors boundaries |
| Existing functional browser regression | 111 checks, 11 captures and three Axe scans passed, including keyboard actions, forms, offline review and logout cleanup |
| Public before/after matrix | 48 combinations each: three routes × four themes × four widths; final six Axe scans clean, no document overflow or runtime page errors |
| Real service-worker proof | 80 resources allowlisted, 22 precached; unseen Fuel script absent initially, cached after successful request, served offline; private bootstrap response not cached; recipe navigation falls back offline |
| Independent finish review | Two scored fixes resolved: narrow More layout and duplicate preference announcements; disposition ship at that scope |
| Design documentation | 48 palette values preserved, YAML/JSON validated; current typography/layout/feedback rules documented |
| Cleanup | All 791 source files match review build; owned app/database and temporary credentials removed; ports 8101/55434 released |

Evidence is under ignored `outputs/refinement-review/`: `node-tests.txt`, `go-tests.txt`, `themes/browser-theme-report.json`, `preference-confirmation/browser-theme-report.json`, `focused-browser.json`, `core/browser-report.json`, `public-final/browser-evidence-b.json`, `sw-browser-proof.json`, `finish-verdict.md`, `build-source-verification.json` and `stack-cleanup.json`.

### Performance and interaction comparison

- Service-worker installation: **80 → 22 resources**, 72.5% fewer initial cache entries. Remaining exact hashed public assets cache on demand. Cache lookup/write failures fall back to the network; failed, redirected, opaque, query-variant and private responses are not stored.
- Fresh desktop login observation: **92 → 34 total requests**, of which worker-related requests were **81 → 23**. The eight shared privacy/offline-support scripts remain intentionally present; no device-cleanup contract was removed to reduce a byte count.
- Loopback timing is not a speedup claim: the sampled desktop login FCP/LCP was 64ms before and 104ms after, with CLS0 in both samples. Separate local runs are noisy and not field measurements. No production INP, low-end physical-device, Safari or Firefox certification is claimed.
- Settings baseline sent two mocked requests for two rapid submits. The finished handler permits one in flight, exposes pending status, preserves edits, bounds waiting and reports one persistent result. Failures remain retryable beside the form.
- Native second-factor disclosure target: 19.5px before, 44px after. The initial small target was an ergonomic finding; Axe did not identify an AA failure.

### Corrections and scope limits

The original matrix's OS-theme failure was a test timing race: computed CSS changed before the queued media-change handler updated browser chrome. The harness now awaits both and focused confirmation passes. Its initial failed report remains intact; it is not represented as a clean 204-check run.

Finish review caught German labels splitting in the 320px More dialog and duplicate live announcements from inline preference feedback plus toast. A single correction batch made More one column through 420px and removed preference toasts. The reviewer scored both fixes resolved using refreshed captures and final functional evidence. Loading-state captures were replaced with captures waiting for actual readiness. A public matrix run overlapped the isolated app rebuild and encountered connection refusals; that interrupted report is retained, and the stable final 48-cell run passes.

The audit agent's authenticated launch was initially rejected by automatic approval review for exceeding its assigned read-only source scope. The parent verified the generated synthetic container/process/session ownership and obtained approval for the isolated tests; real household data and sessions were excluded. Executable detector overlay injection was blocked by nonce-only CSP. The policy was preserved; no visible overlay is claimed. The single CLI detector scan produced 54 color advisories plus one typography and one hidden-image warning; rendered evidence and dynamic image/source inspection identified those as template-resolution/hidden-preview false positives.

This is a shared-interface refinement and the stated behavior regression coverage, not a fresh validation of every populated business workflow, physical camera/OCR, every permission combination or all server-side security integrations. Alternate designs in the gallery are unchanged. Critique snapshot: `.impeccable/critique/2026-11-02T15-23-49Z__localhost.md` (28/40, first run, closed after its selected priority findings were resolved).


## Local development delivery

Rebuilt and recreated Household's local app, database container and one-shot migration service. App/database are healthy and migrations exited 0. Named PostgreSQL/uploads volumes and secrets were retained. HTTP live/readiness/login/manifest all return 200; deployed CSS, theme/app scripts and icons match source byte-for-byte. Read-only mobile smoke checks pass for all four palettes without overflow or runtime errors. Evidence: `outputs/refinement-review/deployment/`. Open **http://localhost:8100**; an existing PWA may offer **Jetzt laden** for the updated worker. No remote deployment, push or image publication occurred.
