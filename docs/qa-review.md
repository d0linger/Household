# Application review — 2026-10-24

This review covers the implemented local Household candidate. It strengthens executable checks and fixes defects found by those checks. It does not certify completion of all 220 plan steps, a public deployment or a hosted GitHub Actions run. The current checklist is [implementation.md](implementation.md); repeatable commands are in [testing.md](testing.md).

## Fixes and regression coverage

| Finding | Correction and evidence |
|---|---|
| Configured origins could disagree with browser Origin serialization | Normalize case, root slash, default ports and IPv6; reject invalid/ambiguous hosts, ports and URL suffixes. Config tests cover the accepted and rejected forms |
| Whitespace-only security keys met the length check | Validate trimmed key length and retain independent-key checks |
| Domain text fields accepted JSON objects, booleans or arbitrary numbers as stringified text | Explicit type checks, complete JSON/UTF-8 validation and NUL rejection; preserve exact decimal/money parsing |
| Embedding bytes.Buffer exposed ReadFrom and let io.Copy bypass output caps | Composition keeps all writes behind the limit; maintenance and OCR regressions exercise io.Copy |
| Maintenance command connections could diverge from the application connection | Validate explicit PostgreSQL URI components, allow only supported TLS/timeout options, remove inherited PG connection overrides and reject duplicate/ambiguous parameters |
| Restore validation and relative upload paths were insufficiently constrained | Reserve household_restore for the disposable target, validate before locking, record absolute backup paths and reject traversal/ambiguous restore paths |
| OCR captures could remain extracting after terminal lease exhaustion or disabled OCR | Return eligible captures to manual review, clear leases, retain household/module-generation/attachment fences and leave retryable jobs pending |
| Orphan scanning restarted at 10,000 entries and repeatedly reread prefixes | Persistent cursor reads at most 200 entries per cycle; regression visits 10,451 entries exactly once before EOF and checks close/restart/cancellation |
| Shopping checkbox reload lost keyboard focus | Restore focus to the same item after change and undo, with user/household/page/expiry binding; browser test failed before the fix and passed afterward |
| Browser tests depended on records left by integration tests | Separate guarded bootstrap helper and authenticated API fixtures; test pending offline data deletion and denied private API access after logout |
| CI could omit database checks or inspect an unrelated container image | Require configured test URLs, use isolated fixtures, retain test/coverage evidence and inspect the actual exported OCI candidate |
| Quality helpers could accept malformed evidence | Negative tests cover YAML trigger variants, unsafe coverage counts, corrupt blobs, descriptor sizes and absent/mismatched attestation content |

Integration fixtures validate both parsed connection identities and actual connected database/role before resetting only the compiled schemas of household_test. An advisory lock rejects concurrent fixture ownership. Real PostgreSQL regressions cover session lifetime/revocation, forced password change, CSRF/origin rejection without side effects, concurrent durable rate limits, last-admin protection, eight denied runtime-role SQL operations, OCR failure cases and canceled/contended backup locks.

## Executed checks

| Check | Observed result | Evidence |
|---|---|---|
| Native Go unit/vet/format checks | Passed, shuffled fresh run | outputs/qa/verify-run.log |
| Unit-only statement coverage | 14.9% (611/4,113), baseline 7.5%; this includes command packages | outputs/qa/unit-coverage.json |
| Focused foundation coverage | Config 98.9%, module contracts 88.9%, exact values 94.1% | Unit coverage report |
| Linux race and combined integration suite | Passed; 57.2% internal-package statement coverage (2,256/3,943); web integration package completed in 279.6 seconds | outputs/qa/linux-race-run.log; outputs/qa/linux-race-coverage.json |
| Native Windows integration wrapper | Passed; same 57.2% combined coverage and 55% gate; web integration package completed in 88.1 seconds | outputs/qa/native-integration-run.log |
| Exact-value fuzzing | Decimal 473,582 executions; money 346,267 executions; both 10-second probes passed | Focused audit output; CI repeats each target for 15 seconds |
| Worker stability | Ten shuffled passes; vet passed | Focused worker audit |
| Browser | 96 checks, 11 screenshots, three clean axe scans; no unexpected browser errors or external requests | outputs/runtime-review/browser-report.json |
| Quality-tool tests | 13 passed, including missing attestation subjects from untagged exports | node --test scripts/quality-tools.test.cjs |
| GOLint / GOSec | Zero lint issues including tagged integration tests; zero Gosec findings in 40 files / 6,814 lines | outputs/qa/gosec.json |
| Vulnerabilities / dependencies | Zero reachable/imported-package Go vulnerabilities; modules verified; dependency/license inventory generated; npm audit zero findings | outputs/qa/vulnerabilities.txt; outputs/qa/dependency-report.md |
| CI syntax and policy | actionlint and immutable action/trigger/Go-version checks passed | Local tool runs |
| Secret exclusion | Redacted Gitleaks scan of staged changes and direct comparison with actual local secret values passed; private/generated paths excluded | outputs/qa/staged-secrets.json |
| Actual OCI export | Verified 11 blobs; Linux amd64; UID/GID 10001:10001; populated 32-package SPDX SBOM and SLSA v1 provenance with matching subjects | outputs/qa/oci-report.json |
| Encrypted restore rehearsal | Exact 2.500000 kg value plus original and preview SHA-256 verification passed | outputs/recovery-review/report.json |
| Updated local Docker runtime | Healthy; nine HTTP/embedded-asset/security smoke checks passed; non-root, read-only filesystem, all capabilities dropped | outputs/qa/docker-smoke.json; outputs/qa/docker-delivery.json |

The combined internal-package coverage is auth 63.8%, config 98.9%, database 63.8%, maintenance 31.5%, module contracts 97.0%, module registration 83.3%, store 61.8%, exact values 94.1%, web 49.1% and worker 63.2%. The recorded regression floors are 55% combined internal coverage and 14% unit-only coverage. These are separate denominators and are not directly comparable. Lower coverage in web and maintenance remains visible; process-level backup/browser checks exercise behavior outside the Go coverage profile.

The OCI runtime manifest is sha256:f156d407d1ca1c47f1595b511d1d080ca4b965bc42a6b5ea2e9471ca21b0d466. The first local export failed verification because the build command omitted an image tag and BuildKit emitted empty statement subjects. Repeating with household:qa, matching CI's use of a tag, produced valid bindings. The verifier was not relaxed. Gosec's quiet mode suppressed the empty report; the workflow now requires a report and runs without quiet mode.

The vulnerability scanner still reports one advisory in an unused module; it finds no affected imported package or reachable function. These are scanner observations at the review date, not a guarantee of absence of vulnerabilities.

The local Compose runtime image is sha256:2403bf288dbcfe88ecc4511efcac018d06fb186fd9a86c16d94c165b1681da82, 37,996,891 bytes. App and backup helper were rebuilt and restarted; PostgreSQL retains its isolated network and persistent volume. The native review server and disposable integration/recovery services were stopped after their checks.

## Workload and recovery observations

The native Windows review server with isolated Docker PostgreSQL handled 1,000 synthetic shopping rows. The measured workload combined 300 reads at concurrency 10 and 50 writes at concurrency 2. Read p95 was **77.3 ms**, write p95 **105 ms**, and every one of the 1,351 HTTP responses, including fixture creation, was 200. The measured phase lasted 1.77 seconds; fixture creation plus measurement lasted 9.92 seconds. Regression budgets are 1,000/1,500 ms for read/write p95. See outputs/qa/load-report.json.

The fresh Docker backup helper created an encrypted synthetic snapshot in **8.10 seconds** and restored/verified it in **4.77 seconds**; setup, backup and verification together took **16.53 seconds**. Source/target were disposable databases on the separate restore service. The fixture source and unique repository/work volumes were removed afterward; no live data was used as test input.

These measurements are local regression observations. They do not establish production resource capacity, representative large-file performance, off-host recovery or agreed RTO/RPO targets. Screenshots of shopping and offline review were also inspected; full keyboard and assistive-technology conformance remains a separate acceptance task.

## Remaining release work

- Execute hosted GitHub workflows when a remote and push authorization exist.
- Configure a public HTTPS origin/hosting target and separately stored encrypted backups before public operation.
- Agree production workload/resource and RTO/RPO targets; conduct a representative acceptance test and populated prior-release upgrade/recovery rehearsal.
- Complete broader accessibility/assistive-technology testing and the individual 220-step evidence cross-reference.
- Optional Web Push, AI adapters and passkeys remain deferred.

Treckrr/Parkrr were used as read-only references for testing/workflow structure. Their containers, databases and files were not changed.
