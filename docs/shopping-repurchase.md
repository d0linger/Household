# Local repurchase suggestions — SH14

Open an active working Shopping list and expand **Nachkauf anhand meines Einkaufsverlaufs prüfen**. **Kaufabstände prüfen** reads completed purchases without changing the list. Choose a quantity yourself, then use **Geprüft zur Liste hinzufügen**. This adds an unchecked Shopping item; it does not confirm a purchase, change stock or create an expense.

## Evidence and limits

- Only committed Shopping checkout decisions `store` and `no_stock` count. Skipped items and simply checked list items do not count.
- The latest64 recorded checkout completions form the history window. Purchase dates come from their saved `received_on`, not their recording timestamps.
- A suggestion requires at least three distinct purchase dates for the same Shopping product and frozen pack quantity/unit. Purchases on the same day count once. At most the last eight dates determine the interval.
- The typical interval is the median of positive civil-day gaps, rounded up for an even number of gaps. Today uses the household timezone. This calculation predicts neither available stock nor required quantity.
- A changed current pack does not inherit evidence from its old pack. Missing or insufficient evidence stays explicitly unknown.
- The display shows at most30 sorted suggestions. The full analysis is bounded to500 product variants and16MiB of stored history before JSON projection. Malformed item/decision pairing or an exceeded bound rejects the analysis without partial suggestions.

The projection contains purchase dates and typed Shopping product/pack identities. Receipt metadata, prices, notes, attachments and Pantry product identities are not returned by this endpoint. No external service or new dependency is used.

## Mutation and concurrency

The server rechecks the current session, household membership, Shopping state and generation. Adoption checks the list and product versions, the history fingerprint and current list contents. Archived lists, templates, previously completed lists and an already-listed effective product cannot accept another suggestion.

Adoption uses the normal App database role in one transaction, with bounded contexts, list/product locks, parameterized queries, audit/projection writes and an idempotency record. A failed transaction leaves its key reusable. A retry checks current authorization and the actual resulting item before returning its prior result.

## Schema and operation

Shopping7 adds a generated stored snapshot-byte count and indexes for household history order and effective product lookup. The size expression and index shapes participate in readiness checks. The populated forward-upgrade test verifies that existing checkout facts are preserved.

Use the normal migration service and backup procedure when upgrading. The generated column and indexes require work proportional to existing checkout history; schedule the migration accordingly. Disabling Shopping preserves its data. This development increment does not upgrade the running8100 deployment.

## Executed verification

The frozen375-source candidate passes nine new PostgreSQL contracts116.526s and22 existing Shopping/checkout/application compatibility contracts222.976s. The final presentation adjustment changes only exact decimal formatting; all Go and SQL bytes from those campaigns remain unchanged.

The final isolated browser test passes its seven recorded check groups, exact quantity-format assertion and Axe scan with zero violations. Mobile, desktop and dark screenshots are in `outputs/repurchase-review/`. Project verification passes formatting, vet, non-database Go tests,86 Node tests, module verification, workflow policy,68 migration checksums and checklist consistency. Gosec reports144 files,22596 lines and zero findings. The local review image builds with user10001:10001.

The full25-component suite also passes against the actual non-root review image:846 existing browser checks plus seven recorded repurchase groups, including the shared-session handoff. The image health/readiness command passes. All12 focused Linux race cases pass(Web264.909s), with375 source hashes unchanged and no race report. Current-source govulncheck passes. Implementation is saved locally in commit7c54250 and SH14 is checked as verified; no new full-application native/race or encrypted-recovery campaign is claimed.
