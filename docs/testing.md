# Test and review guide

Tests are executable behavior checks. Coverage is a diagnostic and regression signal, not proof that every release-plan item is complete. The live results belong in [implementation.md](implementation.md).

The measured baseline sets a 55% floor for combined internal-package coverage and a separate 14% floor for unit-only coverage including command packages. The latest AppSec/audit pass reached 61.3% and 15.2% respectively; the earlier QA baseline was 57.2% and 14.9%. Keep these floors synchronized in quality.yml, test-integration.ps1, test-race.ps1 and verify.ps1; raise them as useful regression coverage grows rather than adding assertions just to inflate a number. See [the current review](audit-review.md) for executed evidence and [the earlier review](qa-review.md) for historical package-level gaps.

## Reproducible local checks

1. Initialize Household secrets as described in README, then install development tools with `npm ci --ignore-scripts`.
2. Run `./scripts/verify.ps1`: formatting, vet, shuffled unit tests, unit coverage, module verification, migration checksums, workflow policy and the quality-tool unit tests.
3. Run `./scripts/test-integration.ps1`: fresh real PostgreSQL fixtures with authentication, database permissions, domain transactions and worker failure cases. It refuses other database names and validates the connected server role before reset.
4. Run `./scripts/test-race.ps1`: the same tagged suite under the pinned Linux Go builder, two CPUs and 2 GiB RAM, with race detection and combined internal-package coverage. Packages are serialized because they share one disposable database. Repeated runs begin with fresh fixtures.
5. After database tests finish, start `./scripts/run-review-server.ps1` in a separate terminal. It starts only the test database and prepares the synthetic `bootstrap` account using `go run -tags=testtools ./cmd/test-fixture`. This helper is excluded from the production binary.
6. Run `npm run test:browser`. The browser creates its own module/report data through authenticated APIs; it does not depend on tests leaving records behind. It runs the original contract followed by `browser-a11y-check.cjs` and `browser-audit-check.cjs`. The latter suites reuse the synthetic account after its password change. Checks cover forced password change, CRUD, shopping keyboard/undo focus, mobile layout, axe, offline review, public-only caching, private draft deletion, logout, named open dialogs, validation focus, failed relationship loading/retry, administrator audit filters, field masking, keyboard diff expansion and report-limit feedback.
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
