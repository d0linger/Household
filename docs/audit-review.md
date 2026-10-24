# Security, audit and usability review

Date: 2026-10-24. Baseline: local `dev` commit `993604f`. This pass implements the approved review and audit-trail request. It updates only Household; no sibling application, remote service or public target was modified. The three specialized reviews covered mutation inventory, audit architecture and privacy/security. The local Docker installation is updated; publication is outside this delivery.

Implementation commit: `361cb5f` on local `dev` (51 files). The staged Gitleaks scan passed; direct comparison against all 11 local secret files found no secret values in the patch. Private/generated paths remained excluded. This final evidence update is recorded separately in local history. No push occurred and no remote is configured.

## Findings and delivered changes

| Finding / gap | Change and evidence |
|---|---|
| Capture review exposed retained OCR after source removal | Five disclosure cases were reproduced. Rendering now requires a ready attachment, matching household/module/generation and a live source. All eight privacy boundary cases passed. Details and attack preconditions: [SEC-01](security-review-2026-10-24.md#confirmed-finding-capture-content-remained-readable-after-source-deletion). |
| Existing semantic logs lacked universal field history | Core migration 5 audits 45 authoritative tables, including every one of the 26 domain tables. Atomic CREATE/UPDATE/DELETE events include precise field values, presence, actor, request and composite entity keys. Bulk/cascade and owner TRUNCATE paths are covered. |
| History needed privacy and attribution boundaries | Unknown/private fields are masked before persistence. Credentials, hashes, ciphertext, OCR/import payloads and private text are excluded from values. Transaction-local actor context, runtime append-only permissions, generated request IDs and worker/operator identity passed regressions. Ordinary domain names/titles remain intentionally retained. |
| Administrators could not inspect exact changes | `/settings/audit` and `/api/v1/core/audit` provide scoped filters, bounded cursor pagination and accessible before/after comparisons. Reader/editor denial, escaped values, masking and private-cache headers passed. |
| Legacy recovery records would disappear from the viewer | A narrow projection exposes only the known old recovery-event shape, preserving original rows and marking actor identity unknown. Unrelated unscoped history remains excluded. Populated core-4 upgrade and repeated migration passed. |
| Related decimal values could pass through float64 | A shared `UseNumber` decoder preserves exact amounts and quantities. A real related amount previously rendered as zero now retains its value; regression passed. |
| Editing a reference outside the first 100 options lost its selection | The server loads the selected authorized parent separately. Regression passed. Searching/paginating every available parent is still a future enhancement. |
| Report caps could silently produce incomplete results | Pantry checks 100 products; fuel/meters check 10,000 source rows. Overflow returns a clear 422 response. At-limit and overflow tests passed; the browser displays the explanation as text. |
| Dialog and error states impaired keyboard/assistive use | Dialog headings and delete warnings are programmatically associated. Validation focuses a preserved-input error summary. Required and multiple relationship loading failures block unsafe submission and offer retry. Tests verify an existing recipe tag survives loading failure; optional scalar references remain usable. |

The [audit contract](audit-trail.md) inventories every registered table and deliberate internal-table exclusion, explains the helper/trigger implementation, and provides concrete event and Go examples. The [security review](security-review-2026-10-24.md) records OWASP coverage, attack preconditions, privacy policy and limitations. These are bounded findings, not a vulnerability-free or WCAG certification.

## Executed checks after production changes

| Check | Result | Local evidence |
|---|---|---|
| Formatting, vet, shuffled unit tests, module verification | Passed; unit coverage 15.2% (673/4426 statements, including command packages), floor 14% | `outputs/qa/audit-verify-final.log` |
| Native PostgreSQL integration | Passed; web package 200.926 s; combined internal coverage 61.3% (2609/4256), floor 55% | `outputs/qa/audit-integration.log` |
| Linux race + PostgreSQL | Passed, no reported races; web package 544.515 s; combined coverage 61.3% | `outputs/qa/audit-race.log` |
| Tagged golangci-lint | Zero issues, including integration/testtools | Current local scanner run |
| GOSec | 43 files / 7468 lines, zero findings; reviewed localized suppressions | `outputs/qa/gosec-current.json` |
| GOVul | Zero reachable or imported-package vulnerabilities; one advisory in a required module with no affected imported package | `outputs/qa/vulnerability-current.txt` |
| Dependencies/workflow tooling | npm audit, module/license inventory, actionlint, workflow policy passed; 13 quality-tool tests and 20 migration checksums passed | `outputs/qa/dependency-current.md`, final verification log |
| General browser contract | 96 checks, 11 screenshots, three clean axe scans, no unexpected JS/CSP errors or external frontend requests | `outputs/runtime-review/browser-report.json` |
| Accessibility/error hardening | 38 checks, three clean dialog axe scans, zero page errors | `outputs/qa/a11y-hardening.json` |
| Audit browser contract | 15 checks, three screenshots, two clean axe scans, no page errors/external requests | `outputs/security-review/browser-audit-report.json` |
| Encrypted recovery | Exact `Synthetic rice\|2.500000\|kg` restored; original and preview passed two SHA-256 comparisons | `outputs/recovery-review/report.json` |
| OCI/SBOM/provenance | 11 blobs verified; 32-package SPDX and matching SLSA v1 subject; UID/GID 10001:10001 | `outputs/qa/audit-oci-report.json` |

The 149 browser checks are the sum of three executed components. The first chained run stopped at the new dialog contrast check during its opening animation. Diagnostics showed transient opacity and blended colors; the test now awaits actual subtree animation completion before axe. The corrected component passed separately, as did the audit component. No contrast rule was disabled, and the complete npm chain was not rerun after this test-only correction. The workflow executes all three in order.

The actual prior core-4 migration set was populated, upgraded to core 5 twice and checked for preserved records/history, working new triggers and revoked direct audit inserts. Tests also cover concurrent actor isolation, rollback/idempotent replay, no-op updates, composite keys, cascading deletion, TRUNCATE evidence, terminal worker failures and authenticated request identity. This is specific upgrade evidence, not a complete prior-release rollback/recovery acceptance scenario.

## Runtime, load and recovery observations

The local application at `http://localhost:8100` is healthy with core schema 5, 45 audit policies and successful audit readiness. The app runs as `10001:10001`, with a read-only root filesystem and all Linux capabilities dropped. PostgreSQL has no published live port. The daily backup helper is running and completed a new snapshot after the upgrade; it has no Docker healthcheck. The temporary review server, test database and restore database were stopped after verification.

- Running image configuration: `sha256:18d139d998f98649693fe718dea312696aae1a8032d1c1380ddbb25b20bd6378`, 38,107,152 bytes.
- Verified OCI platform manifest: `sha256:1bbd041e02961f768e3408500bd760197a748675d2ca59c0d10afcb2e7599da1`.
- Export configuration: `sha256:ebf37c7f5bc215ca95215d77ad931d49508792649b9276fae0bff60c428aee92`. Its four root filesystem layer digests match the running image. Configuration digests differ because the Compose image includes Compose labels; the export does not. They are not claimed to be byte-identical images or publisher-signed artifacts.
- The isolated 1,000-row workload used 300 reads at concurrency 10 and 50 writes at concurrency 2: read p95 81.1 ms, write p95 117.8 ms. This is a small local regression workload, not production capacity acceptance.
- The isolated recovery fixture took 7.49 s for backup and 4.17 s for restore/verification; total rehearsal 15.57 s. Snapshot: `b793ed8e47442e83a348c42765c69e92e8e10b3f1b2d7ad68a50fe98aedf4d6c`.
- A live pre-upgrade backup and a new post-upgrade scheduled backup were created. The data-bearing restore assertion above belongs to the isolated fixture; it is not presented as a host-loss restore of the live installation.

## Actual interface comparison

These are real browser captures using isolated synthetic fixtures, not generated design mockups. The earlier settings screenshot was preserved before the new run overwrote browser output.

- [Before: mobile settings, baseline 993604f](../outputs/security-review/before-settings-mobile-dark.png)
- [After: mobile settings and Protokoll access](../outputs/security-review/after-settings-mobile-dark.png)
- [After: mobile expanded field comparison](../outputs/security-review/after-audit-mobile-dark.png)
- [After: desktop administrator history](../outputs/security-review/after-audit-desktop.png)

The in-conversation comparison switches among the real mobile captures; the source images remain unchanged. Six view/width checks at 320 and 736 pixels, decoded embedded images and keyboard selection passed. Optional external libraries in the preview wrapper were blocked; the comparison itself uses only embedded PNGs. Screenshots and machine-readable logs are local ignored outputs, not repository files or production demo data.

## Remaining acceptance and improvements

- A runtime credential cannot directly change audit rows, but a database owner can. Permitted names/dates/amounts and UUIDs can contain personal data. There is no cryptographic tamper evidence or new automatic audit retention policy; purge retains audit history and existing backups.
- Confirmed capture/import payload minimization, safe categorization of malformed connection errors, searchable reference pagination and independent audit export/retention are follow-up improvements, not all completed by this pass.
- Hosted Actions needs a remote and explicit push. Public HTTPS/reverse-proxy acceptance, off-host backups and host-loss recovery need operator configuration. None is inferred from local passes.
- Representative resource/performance budgets, agreed RTO/RPO, complete keyboard/assistive-technology acceptance and a per-step 220-item acceptance matrix remain open. The current checklist is [implementation.md](implementation.md).
