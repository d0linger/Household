# Vehicle overview — FU01

This increment is integrated in development source on the verified SH13/PA14 checkpointd30c219. Its initial Root404/71-migration evidence remains historical. The complete current Fuel module now passes local acceptance on456source files/74migrations, including the existing28-component browser regression and87selected Linux race contracts. See the [final module evidence](fuel-module-validation.md#lokale-gesamtabnahme-fu0120). Live8100 is unchanged.

## Behavior and correctness

The proposed Fuel overview shows each vehicle's last fueling, current counter reading, an explained consumption interval and optional manually recorded service, inspection and insurance dates. Missing readings or dates stay unknown. It does not create automatic maintenance events.

Readings belong to their own counter series. Replacement counters, overlapping applicability, future records and tied latest records have explicit states. Same-day receipts from different counters cannot be ordered by their incomparable readings; this ambiguity remains visible even when an old counter has multiple higher records. The currently applicable replacement counter can independently have a known reading.

Consumption uses exact rational arithmetic between full-tank boundaries, includes intervening partial fills and excludes the opening fill. Missing records, fuel changes, nonpositive distance and incomplete boundaries are explained. Historical valid intervals stay labeled historical when newer records cannot form a current interval. Liters and kilograms are never silently combined.

Manual dates use the household civil day. The read is authorized in one bounded repeatable-read snapshot with signed20-vehicle pages and explicit source budgets. Money, prices, station, notes and financial links are omitted from the response.

The preparation explicitly requires the current clean Core26/manual-only contract. Future financial-derived Fuel existence and physical facts require inherited record-level authorization; a newer Core version alone cannot grant access. Composition with those later producers remains a separate contract gate.

## Checklist and actual evidence

- [x] Independent review closes393native files and17owned paths; all69priorSQL files are unchanged, with only forward Fuel2 proposed.
- [x] Six pure Go main tests, three Node groups, tagged compilation and lint pass.
- [x] Three actual PostgreSQL smoke contracts pass20.230s: physical facts/explained history, same-day replacement ambiguity, and genuine populated Fuel1 upgrade.
- [x] Eight remaining PostgreSQL contracts pass55.469s, including authority, limits, signed paging, future/initial/overlapping facts, dates, private projections and readiness drift. All393source hashes remain unchanged.
- [x] Actual Gosec JSON reports149files/23664lines/0findings.
- [x] Four existing migration, Fuel-report-limit, complete application and financial-link compatibility contracts pass55.422s with393hashes unchanged.
- [x] Explicit composition with accepted Root394 preserves all70existing SQL files and passes nine actual combined Fuel/Pantry/Barcode contracts57.103s. All three exact overlap merges and404source hashes are independently reviewed.
- [ ] Actual current-source browser, mobile/dark/keyboard/accessibility and scoped race acceptance.
- [x] Root availability and README recorded;102Node cases and28browser components/final10shared-session components are wired into local verification and CI.
- [x] Actual current Root verification passes102Node cases, non-database Go/vet/formatting, module verification,71migration checksums and workflow/task-list checks; observed unit-only coverage is17.0%. Current Gosec JSON reports153files/24620lines/0findings. Actionlint passes.
- [ ] Final FU01 checkbox after current-source acceptance.

Native source manifest: `7704246d764cfb5616de647abf78d5b0b61ef569a430974002473524f1e1a79a`. These executed preparation checks do not establish current deployment or whole-current native/race/recovery acceptance.
