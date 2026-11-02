# Documents whole-module implementation and acceptance

Status: implementation, tests, source qualification and exact index review complete; local checkpoint and joint acceptance remain pending.
Accepted predecessor: Meters code e25b88b and acceptance 4674388; 160/180 criteria verified.
This block covers all DO01–DO20. Finish its entire implementation before running application tests; resolve all module failures before final acceptance. No live deployment or publication is authorized.

## Complete scope before behavior changes

| Criteria | Complete behavior |
|---|---|
| DO01–02 | Inbox grouped by next action; focused preview/metadata review, keyboard/mobile support and unsaved edit preservation. |
| DO03–06 | Personal combined saved filters, explicit confirmed deadlines and task handoff, reviewed atomic batch changes, validated document types and fields. |
| DO07–09 | Immutable original bytes and version history; explicit current file; ordered scan pages and non-destructive derived merge/split; typed, source-attributed deadline proposals. |
| DO10 | Current authorized Core links to vehicles, devices, expenses and tasks; unavailable targets disclose no snapshot details. |
| DO11–12 | Bounded local PDF text first, optional local OCR, explainable rule preview and deliberate application preserving manual fields. |
| DO13–15 | Optional operator-configured watch folder, TLS IMAP attachments and Paperless pull; durable source identities, explicit ownership, retry and no remote deletion. |
| DO16–18 | Durable fenced jobs with progress/cancel/retry, authorized PostgreSQL full text, protected plain-text snippets, exact/probable duplicate review without deletion. |
| DO19–20 | Consistent live session/role/household/generation checks for metadata, original files, previews, search, jobs, links and portable archives; backup/purge/retention evidence. |

## Boundaries and visual structure

Reuse the established Household shell, colors, 44px touch targets, native controls and local assets. Desktop has inbox/library rows beside a bounded preview and labeled review form. Mobile stacks the same document header, preview, metadata and sticky save/cancel actions; changing a pane must not discard a dirty draft without confirmation. Workspace tabs: Eingang, Bibliothek, Fristen, Regeln, Verarbeitung, Eingänge. Empty/error/offline/conflict states have concrete next actions. Search and OCR content are never HTML. No private content in localStorage, service-worker caches or remote services by default.

Independent Documents tables own metadata, fields, versions, pages, deadlines, views, rules, processing and import provenance. Core owns attachments, jobs, reminders and cross-module links. Original files use random storage keys under os.Root; no user-controlled path reaches a shell. Existing image sanitization for other modules remains intact. Source images are preserved as originals for Documents and safe derivatives are separate. Worker actions are bounded by context, file/page/output caps, current generation and lease. Optional integrations are disabled until operator configuration and administrator approval; credentials never enter page JSON, logs, exports or audit payloads.

## Phase 1 — contracts and complete design

- [x] 001. Inspect released Documents CRUD and Core file contracts.
- [x] 002. Inspect live-session, role and generation fences.
- [x] 003. Inspect leased worker and resource limits.
- [x] 004. Record all twenty acceptance criteria and complete UI flow.
- [x] 005. Define original/version/page identities and immutable boundaries.
- [x] 006. Define bounded typed fields and manual provenance.
- [x] 007. Define confirmed deadlines, reminders and task handoff.
- [x] 008. Define current authorized link targets and unavailable state.
- [x] 009. Define durable job cancellation/retry state machine.
- [x] 010. Define source ownership, dedupe and integration credentials.
- [x] 011. Freeze all 102 released migration bytes.
- [x] 012. Mark all twenty criteria in progress, none verified early.

## Phase 2 — independent schema and secure Core composition

- [x] 013. Add explicit predecessor barrier for Documents1.
- [x] 014. Add Documents2 forward migration and predecessor snapshot.
- [x] 015. Add review profile and typed metadata constraints.
- [x] 016. Add immutable original file versions and current selection.
- [x] 017. Add page sets and source-preserving scan derivations.
- [x] 018. Add confirmed typed deadlines and source provenance.
- [x] 019. Add personal saved views with bounded filters.
- [x] 020. Add explainable classification rules.
- [x] 021. Add durable processing states and cancellation fences.
- [x] 022. Add operator-source approval and import identities.
- [x] 023. Add command receipts and replay identity constraints.
- [x] 024. Add household/document composite foreign keys.
- [x] 025. Add full-text vectors and scoped search indexes.
- [x] 026. Add version immutability and current-file integrity guards.
- [x] 027. Add narrowly scoped application grants.
- [x] 028. Add safe audit policies without extracted/private content.
- [x] 029. Compose exact existing Finance/audit/readiness contracts.
- [x] 030. Add forward Fuel/Pantry/Mealplan compatibility deltas.
- [x] 031. Capture exact Documents catalog/owner/ACL readiness.
- [x] 032. Extend migration order and application readiness.

## Phase 3 — complete Store behavior

- [x] 033. Add bounded live-authenticated read scopes.
- [x] 034. Add typed closed command contracts and validation.
- [x] 035. Add permanent command receipts with current authorization first.
- [x] 036. Add optimistic document version checks.
- [x] 037. Add inbox action groups and stable paginated library.
- [x] 038. Add document detail with current file and version history.
- [x] 039. Add typed metadata review and manual field provenance.
- [x] 040. Add personal combined saved views.
- [x] 041. Add batch preview bounded to fifty documents.
- [x] 042. Add atomic version-checked batch application.
- [x] 043. Add confirmed deadline creation/update/cancellation.
- [x] 044. Add deadline agenda and household-local dates.
- [x] 045. Schedule Core reminders without duplicate legacy reminders.
- [x] 046. Add explicit Core task create/edit handoff.
- [x] 047. Add current authorized Core link target selection.
- [x] 048. Add link save/remove with target generation fences.
- [x] 049. Add redacted unavailable target rendering.
- [x] 050. Add immutable original upload registration.
- [x] 051. Add explicit current version selection.
- [x] 052. Add ordered page-set preview and source validation.
- [x] 053. Add derived merge/split request planning.
- [x] 054. Add PDF/text/OCR processing requests.
- [x] 055. Add processing status/progress read APIs.
- [x] 056. Add explicit cancel and retry commands.
- [x] 057. Add rule creation and bounded condition validation.
- [x] 058. Add explainable rule preview and manual-field exclusions.
- [x] 059. Add reviewed rule application.
- [x] 060. Add PostgreSQL full-text and literal safe snippets.
- [x] 061. Add exact hash duplicate groups.
- [x] 062. Add probable duplicate explanations without deletion.
- [x] 063. Add archive metadata manifest and original inventory.
- [x] 064. Fence generic mutation/file/capture bypasses.

## Phase 4 — worker and optional inputs

- [x] 065. Add current-generation durable worker scheduling.
- [x] 066. Add current-lease claim and completion checks.
- [x] 067. Add bounded local PDF text extraction first.
- [x] 068. Add optional PDF rendering and image OCR fallback.
- [x] 069. Add bounded source-preserving PDF page operations.
- [x] 070. Add safe subprocess cancellation and capped output.
- [x] 071. Preserve manual metadata when extraction completes.
- [x] 072. Add processing errors without source/credential leakage.
- [x] 073. Add watch-folder operator configuration and approval.
- [x] 074. Add atomic ready-file ingestion through os.Root.
- [x] 075. Add durable folder source/hash deduplication.
- [x] 076. Add strict TLS IMAP configuration and bounded protocol reads.
- [x] 077. Add MIME attachment extraction and UID validity identities.
- [x] 078. Keep mail flags, messages and remote state unchanged.
- [x] 079. Add fixed-origin Paperless pull and bounded pagination.
- [x] 080. Add explicit local-copy ownership and remote identity dedupe.
- [x] 081. Add retry/backoff, approval revocation and generation checks.
- [x] 082. Add integration health without secrets or remote response bodies.

## Phase 5 — complete HTTP/mobile interface and prepared tests

- [x] 083. Add private no-store workspace routes and strict JSON APIs.
- [x] 084. Add safe upload/download/preview routes and disposition.
- [x] 085. Add bounded portable ZIP export with manifest and original hashes.
- [x] 086. Add inbox/library navigation and filters.
- [x] 087. Add focused preview and labeled review form.
- [x] 088. Preserve drafts during refresh and mobile pane changes.
- [x] 089. Add saved views and batch review/confirmation.
- [x] 090. Add deadline agenda/types and task/link controls.
- [x] 091. Add version history and ordered scan editor.
- [x] 092. Add rule preview/apply interface.
- [x] 093. Add processing progress/cancel/retry interface.
- [x] 094. Add integration approval/status interface.
- [x] 095. Add accessible errors, empty states and keyboard/focus behavior.
- [x] 096. Prepare pure, native, HTTP, adapter and browser test sources.
- [x] 097. Update README/configuration/deployment/recovery instructions.
- [x] 098. Extend purge census, historical fixtures and SQL pins honestly.
- [x] 099. Freeze complete implementation source before first tests.
- [x] 100. Record prepared-source gate without runtime acceptance.

## Phase 6 — whole-module tests and systematic fixes

- [x] 101. Run formatting, ordinary unit tests, vet and lint.
- [x] 102. Run security/dependency/workflow/license checks.
- [x] 103. Run populated predecessor migration and readiness drift tests.
- [x] 104. Run complete Documents native command/rights/rollback tests.
- [x] 105. Run actual file/PDF/OCR/page operation tests.
- [x] 106. Run actual isolated watch-folder/IMAP/Paperless counterparts.
- [x] 107. Run cancellation/lease/replay/concurrency tests.
- [x] 108. Run Node/frontend tests.
- [x] 109. Build non-root runtime/OCR/backup images.
- [x] 110. Run standalone desktop/mobile browser and axe checks.
- [x] 111. Display and inspect actual screenshots; fix observed defects.
- [x] 112. Run complete registered Linux race identities with bounded workers.
- [x] 113. Run full cross-module browser regression.
- [x] 114. Run actual nonempty encrypted file backup/restore qualification.
- [x] 115. Run restored-copy Owner purge and independent-data preservation.
- [x] 116. Verify OCI/SBOM/provenance against actual runtime.
- [x] 117. Resolve failures and qualify complete evidence against final sources.
- [x] 118. Scan exact staged blobs and all immutable predecessor SQL bytes.
- [ ] 119. Create local reviewed checkpoint and remove owned unused resources.
- [ ] 120. Promote DO01–DO20 only after evidence; update checklist to 180/180.

## Evidence policy

Resource snapshots include host free memory, Household container CPU/memory/OOM and worker caps. Start with at most two expensive DB/race lanes and increase only with headroom; retain caches needed by active builds. Remove only identified owned stopped processes, superseded unreferenced images and stale scoped temp trees. No broad prune, unrelated container operation or dependency deletion without an actual unused-reference check. Failed attempts remain failed. Future evidence belongs in documents-module-validation.md; none is claimed by this plan.
