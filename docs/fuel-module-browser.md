# Standalone Fuel browser, OCR and recovery verification

Status: the installed Windows entry passes its complete real lifecycle (`standalone-a4dbdd417b964de0a510bff98e22790b`):20functional workflows,1031checks, six actual OCR cases, five clean Axe scans, mobile geometry, encrypted backup/restore, purge, exactly one generation increment and normal browser/resource cleanup. Earlier failed runs remain preserved, including the strict barcode CHECK roundtrip failure corrected in maintenance. The staged six-file originals remain under `.local/fuel-browser-repro-work/`. Linux execution and hosted CI remain unverified;31pure guard tests are separate evidence.

## Tracked files

- `scripts/run-fuel-module-browser.ps1`: one PowerShell 7.4+ entry point for Windows and Linux.
- `scripts/fuel-module-browser-controller.cjs`: current-session HTTP and real maintenance orchestration.
- `scripts/fuel-browser-contracts.cjs`: pure evidence guards and an explicitly invoked, bounded real Go JSON recorder.
- `scripts/fuel-browser-contracts.test.cjs`: negative and positive tests of those guards; importing it never runs Go, Docker, SQL or a browser.
- `.github/workflows/fuel-ocr-recovery.yml`: dedicated Linux job; source registration is not evidence of execution.

The controller loads the current tracked `scripts/browser-fuel-module-check.cjs`. There is no copied driver. A browser-source manifest covers all scripts plus both npm manifests before preparation and after execution, so a concurrent driver change invalidates the run.

## Isolation and prerequisites

Run only on a disposable development checkout after the native campaign has finished. Install PowerShell 7.4+, the Go toolchain specified in `go.mod`, Node 24, Docker Compose, npm dependencies and Playwright Chromium. Initialize test secrets through the existing development initialization process. Preserve any existing secrets; this runner never replaces them.

The only reset target is `household_test` in the exact `household-tests-db-1` Compose container, project `household-tests`, service `db`, network `household-tests_default`, published at `127.0.0.1:55434`. PostgreSQL must be version 18, owner and application roles must be restricted, and no client may be connected. Port 8101 must be free. The explicit `-ResetIsolatedDatabase` switch authorizes destruction of this disposable test database; `dropdb` has no force flag. The application on port 8100 and sibling applications are outside the target set.

Native acceptance requires the same-prefix `-summary.json`, `-source-manifest.json` and `.jsonl` files. The exact manifest hash, complete current `cmd/`, `internal/`, `go.mod`, `go.sum` membership and hashes, isolated database/port, unchanged source and actual JSON run/pass events must agree. Every currently defined Fuel module integration main must pass; at least 20 are required. Any failed or skipped test, including a subtest, repeated terminal result, pass before run or unfinished test rejects the evidence. This guard is deliberately stronger than trusting a summary count.

A fresh CI campaign can be recorded by explicitly invoking:

```powershell
$env:HOUSEHOLD_REQUIRE_INTEGRATION='1'
# Set HOUSEHOLD_TEST_OWNER_URL and HOUSEHOLD_TEST_APP_URL in this process from
# private secret files, targeting loopback:55434/household_test with sslmode=disable.
node scripts/fuel-browser-contracts.cjs record-native $PWD outputs/fuel-browser-native/module
```

This command runs real Go tests and must only be used in an authorized execution phase. Output is bounded; passwords, database URIs and token-like output are redacted. Stderr is decoded and redacted by complete line, including a final unterminated line, so a secret split between byte chunks is not published.

Both Docker images must be built from that same source with label `household.source-manifest-sha256=<manifest hash>`. The application uses Dockerfile target `ocr`; the toolbox uses `deploy/backup/Dockerfile`. Supply exact immutable `sha256:...` image IDs. The runner requires their label to match and configured user `10001:10001`; tags alone are insufficient.

```powershell
pwsh -File scripts/run-fuel-module-browser.ps1 `
  -RuntimeImageId $runtimeId -BackupImageId $backupId `
  -NativeSummary outputs/fuel-browser-native/module-summary.json `
  -NativeSourceManifest outputs/fuel-browser-native/module-source-manifest.json `
  -NativeManifestSha256 $manifestHash -ResetIsolatedDatabase
```

## Real workflow and proof

1. Acquire an exclusive orchestration lock, validate native evidence, current images and exact isolated source, then reset and migrate/seed only the empty synthetic household.
2. Create GUID-named, run-labelled application, toolbox and restore containers and three named volumes. Application and toolbox run non-root with dropped capabilities and read-only roots. Only the application binds loopback 8101. The restore database is `household_restore`, has no published port, and uses a disposable tmpfs.
3. Prepare actual local Tesseract/Poppler, `pg_dump`, `pg_restore`, and an initially empty encrypted restic repository before the time-critical handshake.
4. Run the current 20-workflow browser driver with normal authentication. Its private control state exports the existing primary session; the controller performs no extra login or session-age modification.
5. Verify six actual completed OCR jobs: receipt and odometer, each as PNG, JPEG and a literal two-page PDF. Job and attachment IDs, canonical downloaded bytes, SHA-256, size and MIME must match current owner projections. All six attachment IDs must differ. PDF first-page recognition and second-page exclusion establish this literal fixture boundary, not arbitrary OCR accuracy.
6. On the driver's bounded request, revalidate current HTTP administrator identity, household, generation and actual session age below eight minutes. Disable Fuel through its normal CSRF-protected HTTP interface.
7. Execute real `household backup` and `household restore-check <snapshot>` using the dedicated toolbox/restore target. Verify current stored backup eligibility, including backup start after module disable. No synthetic verified backup rows are accepted.
8. Queue purge through the normal HTTP confirmation, read its exact scoped UUID with a fixed SELECT, execute actual owner `household purge <uuid>`, and verify completed purge, exactly one generation increment, removed response payloads and retained minimal operation fences.
9. Re-enable Fuel through HTTP and fetch an unfiltered current authorized workspace. Only after every ordered stage passes, atomically acknowledge the current generation. The browser explicitly visits the current unfiltered page, preserves the old locator until confirmation, discards the old reference deliberately and never retries or rebases the old intent.
10. Require all 20 workflows, mobile/Axe checks, no external requests, source stability, controller completion and cleanup. Unknown, late, incomplete or missing evidence denies a pass.

The maintenance controller has a 110-second internal deadline and the driver a 120-second acknowledgement limit. Those bounds are failure limits, not a recovery-time guarantee. The recent-auth reserve can still fail honestly on a slow machine. The job's longer overall timeout includes compilation, image builds and normal workflows.

## Private data and cleanup

Windows uses a restricted ACL on the unique `.local/fuel-browser-auth-<run>` directory. Unix uses mode 0700. Mounted secret files are 0444 inside that host-private directory so the container's PostgreSQL/app UIDs can read the bind mounts; auth, controller configuration and env files remain 0600. They must never be artifacts. Browser cookies, private storage, bootstrap credentials, request/ack payloads, database URLs and restic secrets are excluded from uploads.

Cleanup removes only exact closed names whose run labels match. It never uses `docker system prune`, a production Compose file, or remove-orphans. Normal code-zero driver completion awaits `browser.close`; Unix additionally requires its driver process group to be gone. On failure the controller attempts bounded TERM/KILL of its own driver group, but Playwright can give Chromium a separate process group. Forced, nonzero or unknown driver termination therefore does not prove browser cleanup: it denies success and retains private recovery material locally. Complete forced-orphan elimination is not claimed. CI adds an independent run/attempt label for its always-cleanup, and source-database cleanup also requires its exact CI label. Interrupted-host cleanup and Linux process/mount behavior still require execution evidence.

Public evidence is limited to the Fuel browser report, listed screenshots, synthetic export/OCR fixture files, canonical-source evidence, sanitized recorder/controller logs, native summary/manifest/JSONL and standalone controller/report manifests. The controller report contains synthetic operation/actor/household IDs and the maintenance nonce; it contains no session cookie, CSRF value or request/ack file. Never upload `.local`, whole output trees or private handoff roots.

## Source review and remaining acceptance

- Two independent source reviews and their exact manifest are preserved. Native-event hierarchy, actual JSON-secret redaction and truthful forced-cleanup handling were corrected before installation.
- The four scripts, documentation and dedicated workflow are installed; the new pure suite is registered in all three explicit npm/verify/quality lists. Root execution results remain separate.
- Windows standalone success is recorded with current source-labelled images and actual maintenance stages; retain its success evidence and earlier failures.
- Keep this separate OCR/recovery driver outside the existing shared28-component browser chain; it requires its own real native and maintenance orchestration.
- Execute Windows and Linux real lifecycle acceptance, including failure/cleanup cases, before claiming portable or CI recovery coverage. A YAML/parser pass or unit guard fixture is not a real backup/restore result.
