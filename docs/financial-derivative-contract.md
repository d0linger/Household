# Financial derivatives — implementation contract

This specifies the next Core26 foundation and its later consumer integration. It is **not an available schema or selected-access capability**. Current source has Core25/Expenses4 and the household-only rollout CHECK. Direct financial readers and the shared editor have separate executed evidence in [financial-visibility-verification.md](financial-visibility-verification.md). No live deployment is authorized by this contract.

## Required invariant

A derivative requires every original financial subject and policy that contributed to it. Rebinding an attachment to a new record adds the result's requirements; it never removes the original source. A deleted category line denies its derivative even while its parent entry and policy remain live. Consequently a member records both the immutable Core financial subject ID and policy ID.

Missing classification, unresolved provenance, a retired subject/policy, mismatched household/generation or an unavailable required source denies access. There is no administrator bypass. Current view/edit rights and future account intersections apply to every member; a stored policy version is never a permanent grant. An authenticated read uses one bounded authorized snapshot. A mutation rechecks after acquiring the required locks.

## Proposed Core foundation

All relationships use Core FKs. Optional module schemas never reference one another. The foundation introduces structural metadata only; it must not claim producer completeness or enable selected access before the later integration gates.

| Table | Key fields and constraints | Purpose |
|---|---|---|
| `financial_requirement_sets` | UUID ID; household; classification `financial`, `private_import`, `nonfinancial`, `unresolved`; immutable exact required count; nullable known creator user; narrow reason code; classifier version; creation time | One immutable requirement set. Financial requires at least one member, nonfinancial exactly zero, private import a known creator, unresolved always denies. Missing rows never mean nonfinancial. |
| `financial_requirement_members` | Household, set ID, original subject ID, policy ID; unique set/subject; same-household Core FKs and exact subject-to-policy correspondence | Retains original source identity after source deletion. INSERT only; exact deferred count and immutability prevent late subset/extra-member changes. |
| `financial_artifact_scope_versions` | UUID scope ID; household; fixed artifact kind; typed object UUID/audit bigint/import row number; positive revision; previous scope ID; set ID; captured module/generation when applicable; creation time | Append-only lineage. Audit uses its real bigint ID; import rows use batch UUID plus row number. Other supported artifacts use UUID identities. Latest revision is determined by revision, never timestamp. |

Set membership has a global hard bound of1024anchors. Attachment/capture limits should be narrower, such as16. Existing import limits are500rows/1MiB; a complete source/result union must fit the declared bound or fail atomically, never truncate. The concrete migration must define every allowed kind and key CHECK branch; arbitrary text keys or inferred UUIDs are insufficient.

Use household/set identity indexes, reverse policy and subject indexes, exact typed identity/revision uniqueness and indexes for latest scope selection. A forward migration can add a composite unique subject key for the member FK without editing Core25. No payload FK cascade may erase scope history. Original audit rows remain unchanged.

Separate minimal append-only security events may be added in a subsequent explicit migration. Their fixed fields may identify actor, request, action, outcome and narrow reason, with an opaque policy revision where needed. They contain no financial values, titles, filenames, free-text details, hidden counts or subject lists. Administrative recovery uses such an event rather than exposing the private source audit.

## Guards, audit and maintenance

Runtime grants are individually specified and checked. Sets, members and scope versions are immutable under the App role. Deferred structural validation checks exact counts, same-household subject/policy correspondence, typed predecessor identity and monotonic revisions. Producer integration must additionally check the complete union against actual old/new source metadata; a caller-supplied policy list is never authoritative.

These security infrastructure tables are explicit exceptions to automatic row audit. Otherwise audit creation would produce scope metadata that creates another audit indefinitely. The original business audit remains intact. Latest `audit_ready()` must explicitly recognize the infrastructure exception and separately verify its narrow guards, privileges, function search paths, trigger modes and constraints; the existing77policy census must not be blindly increased.

Existing audit writers are SECURITY DEFINER. A trigger's `current_user='household_owner'` therefore **does not prove an operator initiated the call**. Maintenance handling must distinguish the actual login from a definer frame: direct owner login, or an actual explicit owner role selection by a login genuinely permitted to assume that role, can be trusted. An App login executing inside an owner-definer audit function cannot use the maintenance bypass. Preserve actual operator dump/restore identity and versions; test both direct owner and explicit-role maintenance paths along with the adversarial definer frame.

## Producers and lineage

1. New attachment creation resolves a complete typed source and captures its financial subject/policy when applicable. Nonfinancial classification requires actual source evidence.
2. Rebinding appends a scope revision with the union of all prior requirements and the new target. A missing or unresolved old scope cannot be healed by selecting a visible target.
3. Capture jobs and previews inherit the complete attachment lineage. Confirmation adds the result's financial identity in the same transaction as result creation, capture/attachment updates and audit.
4. Audit events capture the immutable union of the old/new artifact scopes actually affected at event creation, rather than consulting a later artifact head.
5. Import preview creation records the authenticated creator. `confirmed_by`, current administrator, uploader, filename and request timing are not substitutes for provenance.
6. A confirmed import row binds its actual result. Batch summaries, counts and complete replay require creator permission plus every required result policy; one visible row never exposes the other499.
7. Workers inherit the existing protected set. They never fabricate a user SessionID and do not hold policy/domain locks during OCR or file processing. Publication rechecks actual lease, source, scope head, generation and current actor/policy rights.
8. Purge and housekeeping remove rich payload while retaining minimal scope history and retired identity. Missing payload cannot reopen access or restart an old accepted operation.

Copied financial content also retains its origin. An expense created from OCR can reveal the source values through ordinary Expenses reads even when the file itself is protected. Bind the result's Core financial subject to a durable inherited set, independently of the expiring capture payload, and include it in every central financial read/edit predicate before selected mode is enabled. A typed `financial_subject` scope kind can be added explicitly in the foundation or a forward producer migration. Its UUID is the actual Core subject ID, not a guessed optional-domain key. Flatten complete inherited original requirements at creation; check the result's own primary policy separately so the representation has no self-reference or recursive authorization cycle. Repeated derivation retains all prior requirements within the declared hard bound.

Producer ordering is a required gate. The existing Expenses subject hook runs before row audit and creates its policy/subject before Go `save()` returns. Registering result-origin metadata only afterwards would leave the first immutable audit event incomplete. The producer migration must establish validated typed capture-result evidence before that audit runs, or enforce an exact deferred event binding within the same transaction. It must neither precreate conflicting Expenses4 identities nor classify an earlier event from a later transaction's artifact head. A scope-less or incomplete result/audit commit must fail atomically.

## Historical classification

Only a complete, typed, household-bound and generation-supported lifecycle chain is evidence. A safe attachment chain includes CREATE, every changed before/after source, and the current state or DELETE. Audit change values must retain correct string/UUID/NULL-presence and redaction flags; current audit value caps suffice for these identifiers but do not prove a complete chain exists.

Do not infer an origin from filename/hash, equal bytes, timestamp proximity, request ID alone, current target, `uploaded_by` or `confirmed_by`. Recover an import creator only from an unambiguous user CREATE audit of that exact batch/household. Domain audit lacks a generation column: resolve only through an unambiguous retained Core identity with proven lifecycle association. Reused identities, multiple generations, pre-foundation deleted sources, missing/redacted old values and incomplete chains become unresolved. Never invent replacement policies or silently assign responsibility.

Backfill must be bounded and keyset-based under a documented maintenance boundary, followed by exact completeness verification. Oversized individual histories become unresolved instead of being truncated. Historical classifications are stored separately; audit rows and original financial facts remain unchanged.

## Lock and revision contract

Order is household/current auth/modules → required policies in stable order SHARE NOWAIT → relevant domain parents/children NOWAIT → Core attachment/capture/import/job rows in a defined order. Recheck current complete requirements in a fresh RC statement after locks. Expected scope revision and current policy revisions are separate from content versions. Existing opposite-order capture paths must be reordered or use explicit bounded conflict failure.

Read predicates check the current complete set before LIMIT, ranking, counts or aggregates. Readiness errors, missing schema and unknown classification never trigger a shared fallback. The final selected-access cutover still requires draining old application/worker sessions as specified in [financial-visibility-contract.md](financial-visibility-contract.md).

## Acceptance sequence

1. Foundation-only fresh/repeat/populated migration, immutable exact-count/FK/typed-key guards and readiness tests, without enabling selected mode.
2. Owner/explicit-role restore succeeds; App-in-definer cannot bypass structural checks.
3. Safe complete historical chains classify; ambiguous/reused/deleted-before-foundation histories remain unresolved.
4. Every new producer atomically supplies a complete scope; missing/subset/extra/final-source mismatch rolls back all effects.
5. SourceA/resultB tests require both, including an excluded administrator and a global reader with an edit grant.
6. Deleting an original line while its parent remains live denies every dependent artifact and historical event.
7. Import ownership, bigint audit keys, composite row keys and500/501row/member limits reject incorrect or partial results.
8. Concurrent rebind/revocation/worker publication/purge and stale scope revisions fail without partial effects.
9. Audit pagination and summaries filter hidden-first records before51row sentinels/counts; no sensitive Core event bypass remains.
10. Real purge and encrypted recovery preserve exact denied historical/security identities; restored authenticated API checks remain a separate operational gate.
11. Current native/scoped race/browser/security/image checks precede any claim of full EX19 availability.
