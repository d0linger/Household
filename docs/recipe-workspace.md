# Integrated recipe workspace

This contract covers RE01, RE08 and RE16. Implementation is present; acceptance remains in progress in the 180-item checklist. The incumbent Household design and independent recipes schema remain authoritative. Surface mode: Operate.

## Implemented architecture

- The recipe overview opens `/m/recipes/new` or `/m/recipes/recipes/{id}/workspace`. Metadata, ordered ingredients, preparation and tag assignments share one editor and one commit. Existing individual CRUD, CSV and capture routes remain supported. Their changes also enter the recipe history.
- `GET /api/v1/recipes/{id}/workspace` returns one consistent SQL snapshot with a fixed set of indexed subqueries. Exact quantities are converted to strings in PostgreSQL. There is no per-ingredient query and no hidden 100-row truncation. The legacy related view also uses this bundle. Limits are 500 ingredients, 500 steps and 100 tags; sentinel rows cause explicit refusal rather than truncated editing.
- `POST /api/v1/recipes/workspace` creates a recipe; `POST /api/v1/recipes/{id}/workspace` edits one. Typed bodies carry the module generation, full-source SHA-256 fingerprint, stable client row keys, owned optional child IDs, fields, ordering and tag IDs. Limits include the existing 1 MiB JSON body and a 30-second request context. Field errors identify the exact field and client row. Optional text nulls become empty text; quantity/unit nulls remain unknown. Zero timers become absent.
- The server normalizes the complete payload before writing. A transaction revalidates the session and household/module barriers, checks request replay, locks the recipe and all children, verifies the fingerprint, then writes every record, tag, search/outbox projection and audit event. Removed children receive the existing orphan/file-retention cleanup. A late foreign child/tag fails the entire transaction. Existing request receipts make retrying an uncertain response idempotent.
- Child and tag triggers advance `content_version` on every affected recipe. They take parent locks with NOWAIT, preventing a child-first legacy writer from deadlocking with a parent-first workspace save. The workspace also uses NOWAIT for child/tag locks. Competing writers return a review conflict. Variant creation protects the original with `FOR KEY SHARE NOWAIT`, while its selected version is immutable.
- `recipes.versions` holds immutable complete JSON snapshots, per-recipe revision numbers, actor, timestamp and transaction identity. Deferred parent triggers capture the final transaction once, including generic child changes, import/capture writes, reparenting and tag rename/deletion. Deduplication combines the cluster-local transaction ID with its transaction start timestamp; an old XID imported into a restored cluster does not suppress new history. Runtime can only SELECT versions; narrow owner functions have a fixed `pg_catalog` search path and no callable runtime bypass.
- History metadata uses stable revision cursors with 20 rows plus a sentinel. A selected version is loaded separately. Restoring it fills a reviewable draft and creates a new version only on save. A variant receives new recipe/child IDs and a same-module reference to its source version. Later source edits do not modify the variant. Attachments remain owned by the original; no files are copied implicitly.
- Deleting a recipe removes its ordinary children but retains its immutable historical versions until recipe-module purge. History endpoints require the current recipe to exist. Versions deliberately have no recipe-parent FK, so existing variant provenance survives source deletion. A disabled module exposes neither workspace nor history. Owner-only module purge removes the schema contents and increments generation through the existing contract.

## Draft and conflict behavior

The normal draft stays in page memory, survives validation/network errors and warns before leaving. Loading a device draft, restoring a historical version or preparing a variant requires an explicit replacement decision when the current form has unsaved changes. Cancel and Escape preserve metadata and ingredients; confirming replaces only the in-memory form. Device persistence is explicit: at most 10 drafts, 512 KiB per draft and 2 MiB total, retained for 24 hours and bound to household, authenticated user and module generation. The app removes expired/foreign-account drafts on navigation and all recipe drafts on logout. The workspace removes stale-generation drafts and deletes its saved draft after a successful commit. Device drafts are private local storage, not encrypted vaults or automatically replayed offline mutations.

Conflicts preserve the user's form. A fresh comparison names changed metadata, ingredient/step values, additions, removals, order and tags. The user chooses either current content or explicit continuation of their entire draft against that reviewed source. A removed child restored from the draft receives a new identity. A subsequent concurrent change still invalidates the fingerprint; no blind last-write-wins operation exists.

## Upgrade and recovery contract

Core11 adds the version audit policy (51 total); Recipes2 adds content versions, the immutable history and narrow triggers. The migration takes the outer household write barrier before schema/table locks, then baselines each existing recipe once with a null historical actor. It invents no earlier revisions. Historical snapshot size is capped at 8 MiB. **Preflight existing recipes for the 500/500/100 collection limits before upgrading**: an oversized existing recipe makes the migration roll back; split it using the previous version, then retry. No content is silently discarded.

Backups include version rows, transaction timestamps and provenance. The recovery rehearsal now checks an exact historical ingredient after restoration, edits both recipe and ingredient in one transaction and expects one additional complete version while preserving the earlier snapshot. This small disposable database test does not imply production RTO/RPO acceptance.

## Authoring and read contract

- Present recipe metadata, ordered ingredient sections and preparation steps in one workspace. Keep exact quantity strings and explicit unknown quantities. Reordering must also work with labelled keyboard controls.
- Load bounded ingredient, step and tag collections as a bundle with a constant number of queries. An oversized recipe must be clearly identified rather than silently truncated by the generic related-record view.
- Validate the full edit before writing; return fields and row identities for correction. One save commits the whole recipe, child records, tag assignments, audit and search projections. A later invalid child rolls back every earlier change.
- Preserve a private draft while reviewing or correcting errors. Define bounded, generation-bound draft retention and ownership before implementing persistence. Do not silently opt private recipe contents into device offline storage.
- Compare source recipe and child versions under the existing household/session barriers. Concurrent generic ingredient/step edits, source deletion and a second editor must produce a reviewable conflict, not overwrite an unseen change. Review lock order against legacy child-to-parent operations.

## Recipe history and independent variants

- Persist immutable recipe versions separate from the security audit. Each version contains exact source metadata, ordered ingredients, instructions and tag identities/labels at that point.
- Cover every supported write path, including legacy generic child operations, imports and capture, or explicitly route those paths through the same version contract. A bulk save produces one coherent version rather than a sequence of partially edited recipes.
- A variant is a new recipe identity copied from a specifically reviewed source version. Record provenance inside the recipes module; later edits to the original do not mutate its variants. A historic version can become a new current version only through explicit review.
- Keep attachment ownership, module purge, bounded history navigation and backup/restore semantics explicit. Never introduce a foreign key to another optional module.

## Required verification

Exercise exact decimals, optional quantities, zero timers, ingredient/step ordering, duplicate or foreign child IDs, deleted children, changed tags, interrupted saves, concurrent editors, reader restrictions, generation changes, variant independence, immutable history, partial rollback, populated upgrade and restored versions. Browser coverage must use the real authoring flow, named field/conflict errors, preserved drafts, keyboard reordering, mobile/dark views and accessibility scans.
