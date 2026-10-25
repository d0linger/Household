# Test and review guide

Tests are executable behavior checks. Coverage is a diagnostic and regression signal, not proof that every release-plan item is complete. The live results belong in [implementation.md](implementation.md).

The measured baseline sets a 55% floor for combined internal-package coverage and a separate 14% floor for unit-only coverage including command packages. The latest AppSec/audit pass reached 61.3% and 15.2% respectively; the earlier QA baseline was 57.2% and 14.9%. Keep these floors synchronized in quality.yml, test-integration.ps1, test-race.ps1 and verify.ps1; raise them as useful regression coverage grows rather than adding assertions just to inflate a number. See [the current review](audit-review.md) for executed evidence and [the earlier review](qa-review.md) for historical package-level gaps.

## Reproducible local checks

1. Initialize Household secrets as described in README, then install development tools with `npm ci --ignore-scripts`.
2. Run `./scripts/verify.ps1`: formatting, vet, shuffled unit tests, unit coverage, module verification, migration checksums, workflow policy and the quality-tool unit tests.
3. Run `./scripts/test-integration.ps1`: fresh real PostgreSQL fixtures with authentication, database permissions, domain transactions and worker failure cases. It refuses other database names and validates the connected server role before reset.
4. Run `./scripts/test-race.ps1`: the same tagged suite under the pinned Linux Go builder, two CPUs and 2 GiB RAM, with race detection and combined internal-package coverage. Packages are serialized because they share one disposable database. Repeated runs begin with fresh fixtures.
   For a focused regression, `./scripts/test-race.ps1 -Run 'TestShopping|TestReference'` selects matching tests. This mode does not replace or overwrite the full coverage report and cannot certify the full-suite coverage floor.
5. After database tests finish, start `./scripts/run-review-server.ps1` in a separate terminal. It starts only the test database and prepares the synthetic `bootstrap` account using `go run -tags=testtools ./cmd/test-fixture`. This helper is excluded from the production binary.
6. Run `npm run test:browser`. The browser creates its own module/report data through authenticated APIs; it does not depend on tests leaving records behind. It runs the original contract followed by `browser-a11y-check.cjs`, `browser-audit-check.cjs`, `browser-expansion-check.cjs`, `browser-shopping-catalog-check.cjs`, `browser-shopping-reuse-check.cjs` and `browser-shopping-merge-check.cjs`. Later suites reuse the synthetic account after its password change. Checks cover forced password change, CRUD, shopping keyboard/undo focus, mobile layout, axe, offline review, public-only caching, private draft deletion, logout, named open dialogs, validation focus, failed relationship loading/retry, administrator audit filters, field masking, keyboard diff expansion, large reference pages, reviewed conflicts, exact store estimates, substitutes, private previews, activity status, favourite draft preservation and independent template copying, exact merge previews, rejected stale comparisons and focus after confirmation. The catalog's long-name layout check expands only the response label after a real claim; it is a presentation fixture, not a second authentication test.
7. Optionally set `HOUSEHOLD_LOAD_CHECK=1` for a local workload probe during that browser run. It creates 1,000 rows, then combines 300 reads (10 concurrent) and 50 writes (2 concurrent). The regression budgets are read p95 ≤1,000 ms and write p95 ≤1,500 ms. These are local test budgets, not an agreed production SLO; host hardware, container limits and representative attachment sizes still matter.
8. Stop the review server before running database tests again. Rebuild the backup helper and run `./scripts/test-recovery.ps1` after maintenance changes; it uses a separate disposable recovery service and verifies exact data plus original/preview hashes.

Native Windows race detection needs a compatible C compiler. The Docker script avoids depending on one. No script runs sibling Treckrr/Parkrr resources. Synthetic account passwords are confined to tests and must never be used in a live installation.

## CI evidence and guards

- `quality.yml`: GOLint including tagged tests; GOSec; reachable vulnerability checks; module/license review; immutable migration history; strict required database tests with randomized order, race detection, JSON test logs and combined coverage; short fuzzing; actionlint; full-history redacted Gitleaks.
- `browser.yml`: independent fixture preparation and real browser checks on pull requests, dev/main pushes and manual dispatch. The server readiness check fails explicitly if startup never succeeds.
- `container.yml`: OCI export with SBOM/provenance, followed by inspection of that exact export. All referenced blobs and sizes are checked, attestations must target the candidate, and runtime UID/GID must be 10001:10001. This is artifact integrity/structure verification, not signature-based publisher verification.
- `scripts/quality-tools.test.cjs`: negative cases for overflowed coverage counts, alternate YAML syntaxes, corrupt layers, incorrect/repeated descriptor sizes, wrong attestation subjects, absent/malformed SBOM/provenance and root runtime configuration.

Reports live under ignored `outputs/qa/`, general browser screenshots under `outputs/runtime-review/`, audit screenshots under `outputs/security-review/`, and recovery evidence under `outputs/recovery-review/`. The current browser components passed 96 general, 38 accessibility-hardening and 15 audit checks, including eight axe scans. The first chained run stopped on a transient dialog-animation contrast result; the repaired accessibility component and audit component were then executed successfully against the same isolated server. The complete chain was not rerun. Dialog scans wait for real opening animations to finish; they do not disable contrast checks. Hosted Actions runs require a Git remote and an actual push; local results must never be presented as hosted results.

To reproduce the container export locally, use a tag just as CI does. An untagged BuildKit export can contain empty attestation subjects, which the verifier intentionally rejects:

```powershell
docker buildx build --target runtime --platform linux/amd64 --tag household:qa --provenance=mode=max --sbom=true --output type=oci,dest=outputs/qa/household.oci.tar .
New-Item -ItemType Directory -Force outputs/qa/household-oci
tar -xf outputs/qa/household.oci.tar -C outputs/qa/household-oci
node scripts/check-oci.cjs outputs/qa/household-oci outputs/qa/oci-report.json
```

The recovery report records backup and restore/verification durations for its small synthetic fixture. These observations are not agreed production RTO/RPO targets. Gosec runs without `-quiet` so a zero-finding scan still produces its required report artifact.

## Reference patterns

The local Treckrr and Parkrr repositories were inspected read-only. Adapted patterns are PostgreSQL-backed tests that cannot silently skip in CI, serialized packages with deliberate concurrency inside tests, observable coverage reports, explicit fixture ownership, immutable action pins, secret scans, CSP/offline browser checks and inspecting the delivered image rather than an unrelated base image. Domain-specific billing/planner code was not copied into Household.

Primary specifications used for the artifact/workflow checks: [Docker attestation storage](https://docs.docker.com/build/metadata/attestations/attestation-storage/), [Docker build attestations](https://docs.docker.com/build/metadata/attestations/), and [GitHub secure workflow use](https://docs.github.com/en/actions/reference/security/secure-use).

The pantry browser component is now the eighth component of `npm run test:browser`. Its own output lives in ignored `outputs/pantry-review`. Pantry projection integration tests cover exact aggregates beyond the single-input range, populated upgrade, independent product/lot pages, timezone boundaries, later corrections, privilege denial, rollback, concurrent consumption and writer/rebuild lock ordering. The recovery fixture includes populated stock and rejects a restored double count; its executed result must be recorded separately.

## Reviewed checkout regression

The ninth browser component, scripts/browser-shopping-checkout-check.cjs, creates its own shopping and pantry records plus a real uploaded receipt on the isolated 8101 server. It exercises precise quantities, storage/skip decisions, explicit receipt differences, stale source review and an emptied list from editor and confirmation. Its captures and axe reports are stored under outputs/checkout-review. scripts/shopping-checkout.test.cjs checks exact browser arithmetic and unknown-price semantics; both verify.ps1 and the quality workflow execute it.

The recovery fixture also preserves a minimal immutable shopping completion and its exact quantity string. It tests persisted completion metadata, not a full authenticated checkout replay after restoring production data.
# Recipe workspace expansion

`recipe_workspace_test.go` checks exact values, nullable semantics, field-addressed errors, stable fingerprints and collection limits. `recipe_workspace_integration_test.go` checks complete atomic versions, rollback after a later foreign child, generic mutations and tag changes, concurrent editors, immutable privileges, history pagination, 500/501 limits, populated migration with a blocked legacy writer, concurrent source deletion and imported XID collisions. Browser coverage in `browser-recipe-workspace-check.cjs` exercises the real editor, explicit device drafts, named conflict comparison, keyboard reordering, restoration and independent variants. The npm browser chain includes this tenth component; passing counts are recorded only after execution.

The synthetic recovery rehearsal verifies the restored first recipe snapshot and exactly one appended full version after a new transaction modifies metadata and ingredient quantity. It uses the disposable restore service and does not reset the native integration database or the live application.

Repeated browser chains reuse synthetic domain data. The guarded `test-fixture` command requires the development environment, `household_test`, `household_owner` and the bootstrap account. It resets that account and clears the isolated database's rate-limit buckets before a new chain so earlier test logins do not exhaust its authentication budget. Production rate limits are unchanged. The recipe browser component also verifies cancellation and explicit replacement of unsaved metadata and ingredients for local-draft, historical-version and variant transitions.

The isolated PostgreSQL test container uses a 1 GiB tmpfs, a 128 MiB WAL target, 32 MiB minimum WAL and one-minute checkpoints. Long sequences of schema-recreation tests previously exhausted the 512 MiB tmpfs, causing PostgreSQL recovery failure; that run is a failed infrastructure run, not a passing product regression. The test database is disposable. Recreate it explicitly between long test campaigns only after every test/review process using port 55434 has stopped. These settings do not alter the live database or backup repository.

## Cooking regression

`cooking_values_test.go` and `cooking_mutation_test.go` cover rational scaling, very small and large results, unknown amounts, UTC timer transitions, exact paused milliseconds and completion/progress validation. `scripts/cooking.test.cjs` checks browser countdown boundaries and DST-independent deadlines. Verify and the quality workflow execute these tests.

`cooking_integration_test.go` covers private ownership, real HTTP authentication/CSRF and JSON bounds, rollback/audit redaction, source deletion, timer transitions, concurrent session limits and writes, durable completion replay, history cursors and populated core11/recipes2 upgrades. Its held-lock test observes the application role's query and compares the deadline against PostgreSQL time sampled just before releasing the lock.

`browser-cooking-check.cjs` is the eleventh browser-chain component. It exercises actual API persistence, uncertain start recovery, preserved drafts, keyboard focus, multiple timers across reload, elapsed acknowledgement, conflict review, denied Wake Lock, completion/abandonment and mobile dark layouts. Evidence goes to ignored `outputs/cooking-review`; executed results are recorded separately. The recovery fixture includes an absolute timer deadline, a paused millisecond remainder, immutable completion and its unique session key. It verifies restored database constraints, not a full authenticated replay against restored production data.

## Planning regression

`mealplan_values_test.go` checks seven-day calendar mapping across DST/leap/year boundaries, exact rational quantities and known-zero versus unknown effort. `scripts/planning.test.cjs` checks browser scaling, approximate/tiny labels and date helpers. Both verification entry points include it.

`mealplan_workspace_integration_test.go` covers household/reader/HTTP/CSRF boundaries, immutable sources, independent portions, atomic move/swap/copy, durable copy replay after receipt deletion, current-generation source checks, real recipe purge, snapshot shopping transfer, final count rejection, component contention and concurrent expected versions. Its populated core12/mealplan1 upgrade preserves legacy notes/quantities and oversized windows without fabricating snapshots. A held row beyond the first 43 legacy meals proves the lock preselection rejects overflow before touching the unbounded remainder.

`browser-planning-check.cjs` is the twelfth browser-chain component. It exercises recipe review, multiple components, move/swap/drop review, draft protection, same-key recovery after actual saved responses are lost, rejected copy dialogs and explicit copied templates. Its focused regressions include same-date conflicts in notes/slot/portions/effort and focus at both component-list edges. Captures and axe output go to ignored `outputs/planning-review`; results are recorded only after execution. The recovery fixture checks independent component portions, frozen ingredients after live recipe edits and restored durable copy uniqueness. It does not claim authenticated application replay after restoration.

`verify.ps1 -Browser` invokes the complete npm browser chain against an already running isolated review server, matching `npm run test:browser` and the browser workflow. Integration/race tests and the review server must run sequentially because they share the deliberately disposable test database.

The native integration package timeout is twelve minutes. The planning regression completed in 461.034 seconds under the earlier eight-minute ceiling, leaving only nineteen seconds of machine/load tolerance. Individual fixture contexts remain bounded at eight minutes; the hosted race workflow retains its separate fifteen-minute test timeout.
