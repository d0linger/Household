# Pantry completion API – source contract

Status: implementation contract for PA10/PA13/PA19. Whole-module source implementation and independent source review are complete; Phase3 execution checks are now open. This is not yet runtime acceptance. See [the module plan](pantry-module-plan.md). Existing Pantry lookup, intake/inventory drafts and durable batch receipts remain authoritative.

## Context and documents

| Method/path | Contract |
|---|---|
|GET `/api/v1/pantry/extensions/context`|Current authenticated session/household/role, enabled Pantry generation, opaque identity-scope fingerprint, server time/civil day and local draft limits; readers receive `can_write:false`; never public/cacheable.|
|GET `/m/pantry/leftovers`|Writer's cooked-leftover capture/review workspace.|
|GET `/m/pantry/leftovers/{lot}`|Household reader-safe prepared-lot detail shell; data is separately authorized.|
|GET `/api/v1/pantry/lots/{lot}/prepared`|Own preparation/initial-portion metadata and current journal-derived balance; source details require the current Core source contract.|
|GET `/m/pantry/food-facts`|Writer's local-first optional product-proposal workspace.|
|GET `/m/pantry/scans`|Writer's device draft/review workspace.|
|GET `/offline/scans`|Identical public data-free shell; contains no household identity, credential or private API data.|

Context returns `{context:{household_id,user_id,session_id,role,generation,auth_scope,can_write,server_time,today},limits:{scan_lines:100,scan_bytes:262144,scan_days:7,leftover_containers:20}}`. The session UUID and scope fingerprint are identifiers, not bearer credentials. Scan drafts persist no cookies/CSRF values; reconnect obtains fresh bootstrap/session data before explicit mutation.

Only the two scan documents receive local camera/media permission. The default document camera policy and `connect-src 'self'` remain restricted. The service worker may cache local assets, `/offline` and `/offline/scans`; private HTML/API remains uncached.

## Explicit review and mutation

Preview requires a valid UUID `Idempotency-Key`. A successful preview returns the normalized input, exact fingerprint, effects, a five-minute `review` envelope and Unix `expires`. The envelope binds action, key, generation, user, household, membership, session, authentication revision and role. It is not persisted in device storage. Execute accepts only `{review:string}` with the same key and current CSRF header; the Store rechecks current authority, versions, source and complete effects before committing.

All quantities are decimal strings. Version/generation fields are bounded integers. JSON is size bounded, rejects unknown fields and requires exactly one document. A preview has no stock effect.

### Cooked leftovers

| Method/path | Request/response |
|---|---|
|GET `/api/v1/pantry/leftovers/context?source_kind=manual`|Current writer/source context; linked source uses `recipe_version` or `cooking` plus `source_id`.|
|POST `/api/v1/pantry/leftovers/preview`|Typed `PantryLeftoversInput`; returns `normalized`, `fingerprint`, `source`, `before`, `after`, `effects`, `review`, `expires`.|
|POST `/api/v1/pantry/leftovers/execute`|Signed review; one atomic receipt and all accepted lot/journal/provenance effects.|
|GET `/api/v1/pantry/leftovers/operations/{key}?generation=N`|Current-authority `complete/refused/unknown`; unknown is not proof of nonexecution.|
|POST `/api/v1/pantry/leftovers/operations/{key}/abandon`|`{generation:N}`; serializes with execution and returns the actual receipt or durable refusal.|

Input fields: `generation,recipes_generation,source_kind,source_id,source_version_id,product_id,product_version,name,prepared_on,prepared_portions,portions_unknown,containers,fingerprint`. Each of1–20containers has stable `id,label,quantity,location_id,location_version,received_on,best_before_on,use_by_on`. Unit is exactly `portion`; no ingredient-consumption decisions or inferred food safety dates are accepted. A known prepared total caps the combined rest quantity. Actual completed-cooking source claims are permanent across transport keys and Pantry purge. Recipe-version-only and manual preparations retain their explicitly separate identity.

### Open Food Facts

All routes below start `/api/v1/pantry/extensions/food-facts`. Mutation/cache/status routes require current writer authority; the provenance reader uses current household read authority. Default disabled/unconfigured operation remains visible without contacting the provider.

| Suffix/method | Contract |
|---|---|
|POST `/lookup`|`{generation,code,allow_external}`; local alias first, then bounded fresh cache, and only explicit consent can use the fixed upstream.|
|POST `/preview`|Typed `PantryFoodFactsInput`; source/product/pack and selected fields are revalidated and signed.|
|POST `/execute`|Signed review; atomic reviewed local product/pack/barcode/provenance plus durable receipt, with no stock.|
|GET `/operations/{key}?generation=N`|Current actor's durable outcome.|
|POST `/operations/{key}/abandon`|`{generation:N}`, permanent refusal or actual accepted receipt.|
|GET `/provenance/{product}?generation=N`|Reader-authorized `{sources:[],more:bool}` with the latest20current source records and an explicit further-data flag.|

Lookup distinguishes `local,found,not_found,stale,error,limited,disabled,not_configured,unsupported,unqueried`. `unqueried` means no explicit external request has occurred; it is not an upstream no-match. Source includes provider/code/plain product text, source URL, ODbL/DbCL identifiers, retrieved/expiry times and bounded response/content fingerprints.

Adoption input: `generation,code,provider,cache_fingerprint,selected_fields,product_id,product_version,product_name,unit,integer_quantities,create_pack,pack_label,pack_quantity,pack_unit`. A user may select an existing active product without renaming it, or explicitly create a new one. Pack amounts are explicit and compatible; source prose is never converted silently. Existing local barcode aliases are not overwritten.

The complete retrieved text proposal, its licence and the adopted local values remain in the provenance receipt. `selected_fields` records which source fields the user explicitly selected; it does not remove other original proposal fields from that historical evidence. Brand and pack text remain provenance metadata. Current local product values are separately reviewed.

## Offline draft recovery

Device storage contains typed proposals and locator metadata, not a generic URL/method/mutation body. Local quota/owner/epoch/revision checks are atomic. No automatic upload occurs on reconnect.

The seven-day lifetime limits eligibility to send a proposal; an inactive browser cannot promise physical deletion at that instant. Before every draft PUT, current server authority is revalidated and current device metadata plus the exact local draft are checked in one IndexedDB transaction. Owner, household, session scope, epoch/revision, draft revision, expiry and unchanged uncertain-request identity must match. An expired or pruned draft cannot be resent from an old in-memory request. Status GET may still resolve the same already-started server-draft identity after local expiry; it does not recreate or rebind the proposal.

After current lookup/role/version review, an explicit transfer uses the existing `PUT /api/v1/pantry/batches/drafts/{clientUUID}` with `PantryBatchDraftInput` and a stable request key. This saves an unposted server draft only. The existing explicit batch post creates physical stock with its permanent receipt.

If draft creation has an uncertain response, GET the same UUID. Do not mint a new UUID/key. After reload, default to status only. An explicit fresh review may send the same UUID/key with Version0: an existing accepted request hash or draft/version conflict forces retrieval/comparison; it cannot create a second draft identity. A changed or nonmatching existing draft is shown for explicit review. Physical posting is never retried with a new identity.

Business execute uncertainty follows the permanent status/refusal protocol, rather than interpreting404/unknown as an unprocessed write. Historical generation locators are never silently rebound.

## Schema and lifecycle

The forward-only Pantry12 migration adds six tables to the unchanged 74-SQL baseline; Core26 remains the compatible predecessor. Prepared/capture readiness is additive: exact prior signatures, column/function ACLs and existing financial gates remain checked. A NULL table ACL is represented by its exact owner default ACL so an equivalent PostgreSQL dump/restore preserves readiness; distinct explicit privileges remain failures. Git preserves raw migration bytes for checksum portability.

Owner-only purge clears facts and response payloads while retaining the minimal `pantry.leftover_operations` and `pantry.foodfacts_operations` fences. Old identities never acquire a new generation; a cooking source cannot be booked again through a new key. [Executed validation and remaining acceptance](pantry-module-validation.md) distinguish actual provider, offline, native and encrypted recovery evidence. Live8100 deployment, hosted CI, physical camera recognition and production recovery targets are separate boundaries.
