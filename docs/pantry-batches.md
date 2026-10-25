# Pantry intake and inventory batches — PA04 / PA05

Next approved work contract, prepared while RE12 completes regression. This document is planning evidence only: PA04 and PA05 remain open until their implementation and acceptance run. Preserve PRODUCT.md and DESIGN.md. The completed RE12 slice is tracked separately in cooking-stock.md.

## User outcomes

**Intake:** Prepare several explicit product/quantity/location/date lines, preserve progress in a private server draft, review exact base quantities and confirm the complete intake atomically. Each confirmed line creates its own new labelled lot and positive receive movement. Identical names do not silently merge lots. A cancelled review creates no stock.

**Inventory:** Select a location and explicitly count selected lots, including lots currently at zero. Enter the absolute physical amount for each counted lot. Show recorded amount, counted amount and exact difference before confirmation. A positive or negative difference requires a bounded reason; unchanged counts still receive a receipt without a forbidden zero journal movement. Unselected lots remain unchanged. A partial batch must never claim that the entire location has been counted.

Do not infer lot allocation from a product total. Unrecorded physical stock uses an explicitly created intake lot; it does not silently create an inventory balancing lot. The first scope is a current counting workflow, not historical inventory reconstruction. Server review/confirmation timestamps are distinct from journal time; no physical movement is backdated.

## Shared limits and exact values

- At most100 stable line UUIDs per draft/final batch,50 paginated choices,1MiB request, bounded JSON snapshots and20-second transaction deadlines.
- Initial private-draft limit10 active drafts per household member,30-day expiry; show expiry and provide explicit discard. A draft is never a reservation or physical stock authority.
- Product quantities remain decimal strings and rational conversions. Intake amount is strictly positive; physical count is nonnegative, including explicit zero. Missing count is unknown and cannot become zero.
- Counts and balances retain the supported aggregate range; a single nonzero difference must be exactly representable by the journal's numeric(18,6). An unsupported difference rejects the whole batch with an explanation. Never truncate, round or silently split it.
- Integer products require integer base units for actual quantities and each difference. Pack multiplication and compatible mass/volume conversion are exact; density is never guessed.
- Intake permits several locations. Inventory fixes one selected location in its draft, with every selected lot checked against it.
- Every final line is an explicit reviewed intake/count. Draft rows with missing fields can be saved as incomplete but cannot be committed. Omitted inventory lots have no effect.

## Planned independent schema

Pantry owns three tables; no optional-module foreign keys and no new permanent Core proof are needed for this entirely Pantry-local workflow. Purge removes local history and increments the generation, which rejects old requests. Existing stock_operations retains its consume/undo contract.

| Table | Required fields and invariants |
|---|---|
| pantry.stock_batch_drafts | UUID, household, owner, positive generation, kind intake/inventory, nullable fixed inventory location identity, bounded draft JSON, version, active/posted/discarded state, creation/update/expiry timestamps. Owner and generation are immutable. Private dedicated APIs only; never register drafts in generic CRUD/search/export. |
| pantry.stock_batch_receipts | UUID, household, draft UUID, generation, actor, request key/hash, kind, line/movement counts, immutable bounded historical snapshot, server confirmation time. Unique household/draft and household/actor/request-key. Runtime SELECT/INSERT only. |
| pantry.stock_batch_lines | Receipt/line identity, household, lot identity, exact before/count/delta values as appropriate, optional journal movement UUID for zero inventory difference, frozen product/unit/location labels and provenance in the receipt. Same-household local FKs, unique receipt/line and unique inventory receipt/lot. Runtime SELECT/INSERT only. |

Expected new migrations are core17/pantry5 after accepted core16/pantry4. Three redacted audit policies would raise64 to67 and the domain-table count43 to46. These are planned counts, not installed schema evidence.

Narrow secure invoker guards must protect identity/version/state transitions and enforce line/lot/actor/correlation/reason/exact journal-amount agreement. A deferred completeness constraint must be attached to the receipt root as well as its lines, including zero-movement inventory receipts. Readiness checks exact trigger events/functions, protected search paths, each privilege separately and deferred properties. Private draft JSON, labels, count amounts and reasons stay out of safe audit columns.

## Transaction and replay contract

1. Normalize and bound every input. Canonicalize line and lot identities without modifying the caller's slices. Hash the normalized complete request, including draft/generation/version and reviewed stands.
2. Use BeginMutation for Pantry, including current write permission, session, household barrier and generation. Hold the private draft lock and serialize the actor/request key consistently.
3. Read the immutable completion before revalidating new stock or draft versions. Identical authorized retry returns the original receipt. Changed input conflicts. A new request key cannot post the same draft again.
4. A new completion requires an active, unexpired owned draft with the expected version and matching kind/location. Confirm exactly its current reviewed lines; do not post an older unsaved draft revision.
5. Intake locks selected products/locations and optional packs for SHARE NOWAIT, in deterministic order. Check current versions, active status, pack identity/unit and date/opening provenance rules before writes.
6. Inventory sorts unique lot UUIDs and reuses lockPantryLot. Re-read balance and movement count in a fresh READ COMMITTED statement after the lot/product/location locks. Check lot/product/location versions, exact reviewed balance, movement count and fixed location. An unchanged balance after intervening movement is still a conflict.
7. Validate every line and exact difference before any domain write. A conflict retains physical counts but invalidates their confirmation; the user explicitly reviews or recounts after loading current stands. Never silently replace the comparison basis and post.
8. Insert the immutable receipt root, new intake lots when applicable, one journal movement for every nonzero effect, and receipt lines. Reuse afterWrite for each new lot and movement, with redacted semantic audit. Never call several independently committing Write/MutatePantryStock operations or write projections directly.
9. Transition the draft to posted in the same transaction and commit once. Deferred guards prove the complete bundle exists. Failure of a late movement rolls back every lot, journal/projection effect, receipt/line and draft transition.
10. After uncertain response, keep exact body/key and freeze edits until replay resolves. Generic short-lived receipt pruning cannot make an already-posted draft postable again.

## Reads, privacy and workflow

- Draft listing/detail/save/discard always scopes household, owner and current generation. Other members receive not-found for private drafts, including readers. Shared completed receipts are readable to authorized household members while Pantry is enabled.
- Explicit server-draft save persists incomplete rows with version guards. Confirm/save indicators distinguish local edits, saved draft and posted stock. Reload restores saved progress; unsaved changes receive the existing navigation protection. No automatic local sensitive storage.
- Catalogue/reference results provide choices, not authoritative versions. Load the selected product/location/pack stand separately and bind it to final review.
- Inventory needs a location-scoped lot read that includes zero balances. Its signed cursor binds household, actor, purpose, generation, location, filter and expiry. Use a stable direct sort anchor; deletion of the previous row must not invalidate pagination or leak a new scope.
- A new lot created while paging is never implicitly marked counted. Each batch confirms only selected rows. Whole-location completion needs a future stable scope contract and is not part of this first workflow.
- Show archived status explicitly. New intake requires active products/locations. Inventory may explicitly count existing archived products/locations: incumbent generic adjustments and the journal guard permit this, and physical correction must not silently omit their stock. Label archival status in choice/review/receipt; the correction does not reactivate the product or location. Location choices must therefore offer an explicit archived-inclusive view as well as ordinary active choices.
- Offer batch receipt links from the wizard and affected lot history. Intake/count adjustments retain their own correction contract; do not offer the consume-only reversal button for an adjust/receive movement.

## Chronological implementation checklist

1. Preserve the reviewed archived-inventory policy from incumbent rules and cover it with tests.
2. Define precise typed input/output and JSON/text/count limits.
3. Add pure exact count/intake/difference normalization and boundary tests.
4. Design the three local tables, ownership/state invariants and redacted audit policies.
5. Implement narrow guards, deferred root completeness and readiness metadata.
6. Exercise direct-SQL incomplete bundles and privilege/readiness failures.
7. Add bounded private draft reads, explicit versioned save and discard.
8. Add location-scoped zero-inclusive lot pagination and signed scope checks.
9. Add authoritative product/location/pack review reads.
10. Implement actor/request/draft durable replay before new-state validation.
11. Implement all-or-nothing intake with new explicit lots and exact receive amounts.
12. Implement selected-lot absolute inventory counts and nonzero adjustments.
13. Cover two-tab draft conflicts, parallel finalization and same-balance intervening movement.
14. Inject a real failure into the second effect and prove complete rollback.
15. Test reader/owner/household/CSRF/module/generation boundaries and real Pantry purge.
16. Test populated prior-schema upgrade, immutable grants and audit redaction.
17. Record the Operate surface direction and read craft-floor immediately before UI work.
18. Extend the Pantry workspace with discoverable intake and count actions.
19. Build private saved-draft list, bounded row editor and explicit progress states.
20. Build aggregate review, count conflict comparison and exact uncertain-response retry.
21. Build immutable completed receipt views and correctly named correction guidance.
22. Test reload, keyboard editing/removal, incomplete rows, zero count, pagination and long labels in browser.
23. Inspect desktop/light and mobile/dark states in a bounded batch; correct material findings once.
24. Obtain the required fresh finish review and separate incumbent-design documentation comparison.
25. Extend synthetic encrypted restore to preserve saved draft, posted receipt, exact effects and duplicate-draft protection.
26. Run appropriate native integration, scoped Linux race, lint/security/workflow and complete browser regression.
27. Update README and honest implementation evidence; mark PA04/PA05 verified only after acceptance.

## Planned mobile and desktop flow

These wireframes specify the next workflow, not an existing interface. Inherit the Pantry workspace's established layout and controls; no visual identity replacement.

```text
Vorrat
  [Bestand] [Zuerst verbrauchen] [Einlagern] [Inventur]

Meine gespeicherten Entwürfe
  Einlagerung · 6 Zeilen · 4 vollständig · zuletzt gespeichert ...
  Inventur Speisekammer · 8 Chargen · 5 gezählt · gespeichert ...
  [Fortsetzen] [Ausdrücklich verwerfen]
  [Neue Einlagerung] [Neue Inventur]
```

The draft list is account-private and bounded. A receipt is household-shared, with actor and confirmation time. Never mix saved draft progress with completed stock changes.

```text
Einlagerung · Zeile 2 von 6
  Produkt [ausdrücklich suchen/auswählen]
  Menge   [          ] Einheit/Packung [          ]
  Geprüfte Basismenge: ... · ganzzahlig/exakt/noch offen
  Lagerort [          ]  Erhalten am [          ]
  Chargenbezeichnung [                         ]
  [Optionale MHD-/Verbrauchs-/Öffnungsangaben]
  [Zeile prüfen] [Weitere Zeile] [Zeile entfernen]
  [Privaten Entwurf speichern] [Gesamtaufnahme prüfen]
  Lokal geändert / Gespeichert um ... / Speichern fehlgeschlagen
```

Do not preselect a product or interpret an empty quantity as zero. Removing a line restores keyboard focus to the nearest surviving row or add-line control. Product/unit changes retain input until the user explicitly resolves incompatible choices. Saving an incomplete draft preserves errors and never books stock.

```text
Inventur · Speisekammer
  Hinweis: Teilzählung nur der ausgewählten Chargen
  [Aktive / einschließlich archivierter Einträge]
  Produkt / Charge / Datum / Buchbestand / Auswahl
  ...50 Einträge... [Weitere Chargen]

Ausgewählte Charge
  Buchbestand: ... · geprüft um ...
  Physisch gezählt [            ] Basiseinheit
  Differenz: ... / unverändert / noch unbekannt
  Begründung [                                 ]
  [Zählung prüfen] [Nächste Charge]
  [Privaten Entwurf speichern] [Teilzählung insgesamt prüfen]
```

A zero balance is a visible eligible lot, and a deliberately entered zero count differs from an empty field. On concurrency conflict, show the old reviewed stand, current stand, retained physical count and explicit recount/review action. Do not relabel the stale count as freshly counted when refreshing stock.

```text
Gesamtprüfung
  Jede neue Charge / bestätigte Zählung
  Produkt · Lagerort · Charge · vorher → nachher · Grund
  Unvollständige Zeilen: Abschluss gesperrt, Entwurf speicherbar
  [Alle geprüften Zeilen verbindlich buchen] [Weiterbearbeiten]

Ungewisse Antwort
  Geprüfte Angaben bleiben eingefroren
  [Dieselbe Anfrage erneut prüfen]

Gespeicherter Beleg
  Art · Actor · Bestätigung · exakte Zeilen und Effekte
  Unveränderte Zählungen: bestätigt, keine Bewegung
  [Betroffene Charge öffnen] [Neuen unabhängigen Entwurf beginnen]
```

Desktop may show the selected line/count list beside its editor, but mobile retains the same draft, explicit decisions and aggregate confirmation. Loading, empty/no-matching-lot, expired draft, forbidden/foreign owner, stale version, offline and unknown-result states need named recovery actions. Add at most the shared primitives required by these flows; preserve existing Pantry layout outside this scope.

## Evidence required

The pattern scan is read-only design input. No PA04/PA05 migration, API, browser flow, operational check or verified checklist item is claimed by this document. Required acceptance includes mixed products/locations, exact packs, zero/positive/negative counts, skipped lots, duplicate identities, same-draft new-key replay, second-effect rollback, concurrent quick/manual/cooking consumption, two-tab private drafts, zero-inclusive pagination, actual purge, populated upgrade, encrypted restore and keyboard/accessibility review.
