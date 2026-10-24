# Shopping catalog and store context

This implements SH03, SH04, SH08, SH09 and SH10 from the approved expansion. It extends the existing shopping board and generic record editors; the incumbent visual system remains authoritative. Implementation and verification status is tracked separately in improvements-180.md.

## Domain contract

- Stores own named aisle groups with an explicit integer order. Lists choose a store. Unmapped item groups follow mapped groups in alphabetic order; item order remains editable within a group.
- Local catalog products distinguish name, brand and a positive pack quantity/unit. Product references display all three, so equal names cannot conceal different packs. Product images use the existing private upload, sanitized image and asynchronous preview contract.
- An item retains its original product when a substitute is selected. Availability is available, unavailable or substituted; only substituted positions have a replacement product. A substitution must be a different explicit catalog product.
- Item quantity is the quantity in its selected unit. A quantity in pieces denotes catalog packs when a catalog product is selected; otherwise it denotes individual pieces. Price observations explicitly state their quantity and unit. No implicit conversion between mass and volume is permitted.
- Item assignment uses a household membership. New assignments require an active member and enabled user. A historical assignment remains readable after deactivation and can be cleared.
- A shopping-list activity claim lasts 30 minutes, belongs to the authenticated member and expires without a background write. Another member cannot silently replace a live claim. The holder can renew or release it; an administrator can release it. Archival clears activity.
- Price observations belong to a product and a store and carry an observation date. The latest compatible observation not in the future is used; ties resolve by creation time and ID. Estimates round each item to cents using exact arithmetic, include checked and open available items, and separately count unavailable and unknown-price positions. A source-date range and an estimate label prevent stale observations being presented as current checkout prices.

## Integrity and privacy

All relationships use household-scoped foreign keys. Catalog/store deletion is restricted while referenced. New tables receive explicit grants and audit policies; unknown private fields stay redacted. Activity actions reuse the existing session, generation, version, idempotency and transaction boundary. Reads have no side effects. Product previews are selected in the board query and downloaded through the existing authenticated, generation-checked endpoint; no original-file fallback or remote image URL is introduced.

## Direction contract

The working list remains the primary surface. Store context, estimated amount and an optional active shopper appear as concise text. Each article shows only its relevant variant, assignment and availability; catalog maintenance stays in the existing tab/editor system. The mobile shopping mode retains its compact disclosures and first visible check action above bottom navigation. Images load lazily at a fixed small size to avoid layout shifts.

## Acceptance evidence required

Exercise old-data upgrades, household foreign-key boundaries, inactive assignments, bounded/concurrent claims, unavailable/substitution consistency, duplicate product names, exact price conversion/rounding, missing and future prices, more than one board page, audit coverage, source deletion and private previews. Browser checks must exercise store ordering, variant display, edited references, availability and activity with keyboard and mobile layouts. Results are recorded only after execution.
