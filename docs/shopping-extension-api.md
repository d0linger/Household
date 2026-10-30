# Shopping extensions – version1 implementation contract

Source specification for SH15/SH17,29October2026. See the [complete module plan](shopping-module-plan.md). This document fixes shared backend/frontend/worker contracts before implementation. Source existence is not runtime acceptance; tests start only after the entire module is integrated.

## Context and authority

`GET /api/v1/shopping/extensions/context` returns `{context,limits,push}`. Context contains `user_id,household_id,generation,session_scope,auth_scope,role,can_write,offline_allowed,server_time`. Scopes are opaque **non-bearer** identifiers/digests, never cookie/session tokens or CSRF credentials. Generation is a positive safe integer on the browser wire; unsupported numeric range fails closed. Every private route still authenticates the actual HTTP cookie. Server-side internal session UUIDs are not copied into device snapshots.

Limits are `offline_lists:10,offline_items:1000,offline_bytes:2097152,offline_days:7,notification_watches:20,push_devices:5`. Push contains `state:disabled|not_configured|ready,application_server_key:null|string,vapid_key_id:null|string`. Browser capability/permission/connection are client facts, not guessed server values.

All mutable/private POST/PUT/DELETE routes use existing HMAC-bound CSRF checks and closed bodies. Snapshot POST is an explicit authenticated read and uses the same CSRF mechanism. API errors never echo endpoints, keys or whole private records. Current user/household/role/session/module generation are revalidated at effect boundaries. Reader permission is sufficient for personal preferences, push management and list reading, and never grants domain editing.

## Personal preferences

`GET /api/v1/shopping/lists/{id}/notification-preferences?generation=N` returns `{context,list,revision,preference}`. List contains only `id,name,archived,is_template`. Revision is the exact own-user profile revision as a decimal string. Missing profile returns revision`0` and `preference:null` without writing anything. Present preference contains `enabled:true,push_enabled,muted_until,enabled_since,push_enabled_since`; absent preference is disabled. Effective eligibility additionally reflects archive/mute/current module state.

`PUT` at the same path accepts exactly `{generation,revision,enabled,push_enabled,mute_for_seconds}`. Mute values are0,3600,86400 or604800, computed from server time. Disabled input must not enable push or mute. Current active non-template lists may be enabled; an existing own watch may be disabled without inventing a new record. Response uses the same view and the actual committed new revision. Stale revision/generation is409; missing/foreign list404; quota422. Unknown outcome requires GET before another deliberate change; do not label an unconfirmed request saved.

`GET /api/v1/shopping/notification-preferences?generation=N` returns own `revision` and a bounded `lists` overview. It never returns another user's preferences or the complete settings namespace.

Storage is `core.module_states.settings.shopping_notify_v1`. Its typed shape is schema1, current generation and `users[userUUID] = {revision,watches}`. Each watch is keyed by list UUID and contains activation time, push boolean/time and optional mute deadline. A user revision remains after their last unsubscribe; old revision0 cannot recreate a removed watch. At most20active watches/user,256overall,128KiB encoded namespace. Parse only this namespace after SQL byte preflight. Preserve other JSON keys and module state/version/changed metadata. Unknown schema, invalid structures and overflow fail closed.

Preference locking is read-committed **household SHARE → actual session/membership → module UPDATE NOWAIT**. Do not use editor-only BeginMutation or upgrade its module SHARE lock. Own-user mutation is independent of domain afterWrite and does not emit a list-content event.

## Transactional change events

Keep one existing `record_changed` event per supported Shopping afterWrite. For content changes to lists/items use event_version2, retaining `entity,id` and adding `shopping:{schema:1,kind,list_ids}`. Kinds are `item_changed,item_deleted,list_changed,list_deleted`; list_ids contains one or two distinct canonical UUIDs. Moves carry old and new list IDs from the same transaction's locked before/after images. Other modules and unchanged legacy events stay version1. No names, notes, quantities or endpoint data enter this payload.

Generic Delete, list-cascade deletion and direct Planning-Purchase removal need explicit same-transaction emission. Copy, merge, import, barcode, repurchase, Recipe/Pantry/Planning contributions and checkout must use the same producer. Activity leases and content no-ops do not create content digests; a pre-existing generic bookkeeping event may remain version1. Existing idempotent replays return before emitting again. Event IDs/dedupe use server-owned identities; event creation time is DB `clock_timestamp()` so lock waits are not misordered against consent.

The new consumer `shopping-list-notifications-v1` accepts only this closed event contract. It atomically records processing/fanout progress and stages per-user/list/generation60-second digest jobs using DB processing time. Bound fanout, preserve continuation when recipients exceed one batch, and never acknowledge unseen recipients. Digest keys/receipts prevent duplicate DB effects across workers, restart and lost leases.

Current account/membership, watch revision, list visibility, module generation and activation/mute times are checked again at digest execution. No historical changes before opt-in or new push consent. Muted intervals and obsolete pre-activation work do not reappear after resume. In-app recipients do not need a live browser session; push needs its actual registering session/auth state too. Removed-list handling is an explicitly generic final notice with a safe fallback; archived lists stop normal content hints.

Core.notifications uses new kind `shopping_list_changed_v1` and a strict versioned dedupe key binding household, recipient, generation, list and bucket. It has no generation column: the new typed inbox projection must reject malformed/old-generation keys and rebuild/validate targets under current access before display. Other notification kinds remain unchanged. Push click target is always `/notifications`.

Use existing Core job states `pending,running,done,failed,paused` and lease fields. Obsolete work ends with a bounded closed reason; do not introduce cancelled. Existing worker dispatch must recognize both new digest and push kinds. Retention touches only new namespaces/kinds/consumer receipts. Shopping purge already clears its jobs, outbox and notifications; additionally remove only the reserved settings namespace before generation advancement. Shared personal push connections are Core-owned; session/device withdrawal and their own expiry govern their lifecycle.

## Optional push API and envelope

`GET /api/v1/core/push/context` returns current own non-bearer scopes, `state,application_server_key,vapid_key_id,device_limit`. No secret key or endpoint is returned.

`GET /api/v1/core/push/subscriptions` returns `{subscriptions:[{id,device_id,state,vapid_key_id,registered_at}]}` for the current owner only, bounded to supported own devices. State describes server registration/disable/renewal requirements, not delivery.

`POST /api/v1/core/push/subscriptions` accepts exactly `{device_id,vapid_key_id,subscription:{endpoint,keys:{p256dh,auth}}}`. Validate UUID, current VAPID binding, bounded HTTPS endpoint, canonical receiver key/auth sizes and P-256 point. Return the own committed `{id,device_id,state:registered,vapid_key_id,registered_at}`. Do not steal a foreign globally unique endpoint with ON CONFLICT. Repeated registration may update the same own device only with current authorization.

`DELETE /api/v1/core/push/subscriptions/{id}` disables only the authenticated owner's connection and returns `{state:disabled}`. It remains a personal Core privacy control when Shopping is paused. The browser separately requests native unsubscribe on deliberate device withdrawal/logout/account switch. A session invalidated on the server is no longer eligible for future push attempts even if a stale encrypted row remains until housekeeping.

The existing encrypted_subscription contains a closed schema1 envelope with row/subscription ID, household/user/device IDs, internal actual session identity, non-bearer auth scope, endpoint/receiver keys, registration/consent/expiry and VAPID key ID. Use the existing AES-GCM sealer/key-id contract; validate decrypted fields against the stored row and current user/session. Endpoint SHA256 is unique; plaintext endpoints/keys are never selected into public responses or logs.

Configuration: `HOUSEHOLD_WEB_PUSH_ENABLED=false` by default; optional `HOUSEHOLD_WEB_PUSH_VAPID_PRIVATE_KEY_FILE` and `HOUSEHOLD_WEB_PUSH_VAPID_CONTACT`. Derive public key from private. Contact is a valid mailto/HTTPS VAPID subject, not a callback endpoint. Missing optional setup yields not_configured; malformed supplied keys fail before bootstrap. `compose.webpush.yaml` is optional and supplies only its required secret; base Compose needs no absent push secret. A standalone keygen command makes no DB connection and does not overwrite files silently.

Use MIT webpush-go1.4.0 with JWT/v5.3.1 and existing newer x/crypto, supplying an owned bounded HTTP client and request context. Support reviewed Google/Mozilla/Apple/Microsoft production gateway hosts with exact domain boundaries; keep endpoint paths/queries opaque. Enforce HTTPS443, no userinfo/fragment/IP literal, no proxy/redirect, public DNS/IPs and original TLS host, bounded request/response sizes and total timeout. No custom URL/environment bypass. Persisted rate/backoff and bounded leases survive restarts; no HTTP call while holding DB locks.

Push jobs contain only closed IDs/authority references, never a raw endpoint. Before each attempt resolve/decrypt/revalidate.404/410 disables the exact obsolete endpoint;429 uses bounded Retry-After; retryable transport/5xx outcomes use bounded persisted backoff. Response uncertainty may repeat an encrypted generic notice with the same stable tag.2xx is gateway accepted, never proof of actual device display. Logs contain closed categories/status, no bearer endpoint or keys.

Closed push payload: `{schema:1,kind:shopping_notice,id:UUID,expires_at:integer,audience_scope:nonBearerDigest}`. It carries no list/item/actor/count/price/URL. Service worker requires the expected schema, lifetime and local device scope, shows fixed generic text and local icon, and handles click only through fixed same-origin `/notifications`. No push-driven fetch of snapshots, mutation, arbitrary icon/link or automatic subscription renewal. Browser permission, synthetic SW behavior, controlled HTTP/encryption proof and actual vendor delivery remain separate evidence.

## Offline snapshot API

`POST /api/v1/shopping/offline-snapshots` accepts exactly `{generation,list_ids:[UUID...]}` with1–10unique currently readable lists. Require current own session and explicit offline storage consent. Read all selected lists and all their items, independent of current UI filters, in a bounded read-only repeatable-read snapshot; then revalidate current authority/consent/generation before response. No100-row stitching.

Response: `{schema:1,context,captured_at,expires_at,complete:true,lists,totals:{lists,items,utf8_bytes},fingerprint}`. captured_at/expires_at are server timestamps; expiry is exactly the bounded7-day lifetime. Lists contain `id,version,name,archived,is_template,items`. Item fields are `id,version,name,quantity,unit,group,note,checked,availability,assignee_label,product_label,pack_label,replacement_label`. Versions and non-null quantities are exact strings; quantity/unit may explicitly be null. Display labels may be null. No list description, images/image IDs, calculated money/estimates, attachments, raw foreign user IDs, CSRF, cookies or mutation bodies. The UI discloses storage of item notes and household display labels; arbitrary user text is not claimed to be price/secret-free.

More than1000total items or2MiB encoded complete data rejects the whole batch. A1001row sentinel detects overflow without an unbounded materialization. Preserve unknown-vs-zero and exact decimals. The fingerprint detects representation corruption; it is not a server authorization token or write permission.

## Browser storage and public shell

Separate IndexedDB `household-shopping-readlists`, schema1, stores `meta` and `snapshots`. Shared device opt-in plus explicit save is required. Stored own non-bearer scopes, generation, epoch and revision fence visibility/writes. One readwrite transaction verifies current owner/lease, expiry, request epoch/revision and aggregate10/1000/2MiB quotas before replacing selected complete snapshots. No localStorage/private-Cache fallback on failure. Other valid snapshots survive a rejected refresh; no automatic eviction.

Same owner/new session needs current reauthorization and full refetch before explicit activation. Old generation cannot be rebound. Opening/lease renewal does not extend snapshot expiry. Expired data is unreadable and pruned on next access; logout/opt-out/account switch immediately locks UI, invalidates pending writes, signals tabs and physically clears meta/rows. Raw store/owner observations precede opt-in-aware helper inspection in later tests. Failed deletion is not success.

`GET /offline/shopping` is a neutral public shell with no user/CSRF/list embedding. Only public hashed assets and this shell enter SW caches. It opens explicitly authorized local copies, always labels saved-as-of/expiry/server-unconfirmed state, and renders static statuses rather than interactive mutation controls. Local filtering/paging does not change complete stored counts. Explicit online opening/refetch/deletion are separate actions. No offline edits, automatic upload, background sync or cache of private HTML/API responses. Existing mutation queue and Pantry draft namespace remain independent.

## Implementation ownership / source-only phase

- Store lane: contexts, prefs, complete producer coverage, snapshots, durable digest staging/completion and narrow purge contract; publish concrete exported Go signatures to Worker/HTTP lanes before callers are integrated.
- Push/worker lane: bounded library adapter, encrypted own-device service, registered job dispatch, retry/lease/network behavior and their test sources; coordinate typed authority calls with Store lane.
- UI lane: Shopping personal controls, readonly device store/shell/viewer, notification/push states and pure/browser test sources; consume this wire contract.
- Root: configuration, HTTP routes, service wiring, common page/privacy/SW integration, optional Compose/keygen/dependency registration, README/checklists and final integration.

Agents prepare owned files only and report exact pre/post hashes. No new unit/integration/browser/security/syntax campaigns run until all lanes are implemented, independently source-reviewed and integrated; formatting and read-only source review are allowed. All75SQL/Ready contracts remain unchanged.
