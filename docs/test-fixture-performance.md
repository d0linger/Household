# Isolated test fixture performance

Status: the smallest test-only change passes full shuffled native regression and focused Linux race. PA04/PA05 acceptance remains in implementation.md. The full native RE12 web package took642.856s; the first expanded batch run exceeded the previous12-minute ceiling. Raising a timeout is not a runtime improvement.

## Preserve the safety contract

- Retain the exact isolated database-name/server-role checks, exclusive test advisory lock, separate owner/runtime pools, fresh schemas, real bootstrap/login and cleanup.
- Keep historical populated-upgrade tests on actual older migrations; do not replace their data with current-schema fixtures.
- Keep grants, trigger completeness, readiness, cross-connection commits, concurrency and real rollback tests. An enclosing transaction cannot isolate HTTP/worker writers on other connections.
- Never use sibling applications or the live Household database as a source, target or cache.

## Smallest next change

1. Measure reset, first migration, repeated migration, bootstrap/login and cleanup separately. Record actual durations; do not infer a savings percentage from source counts.
2. Add one dedicated fresh-install/repeat regression: migrate twice, verify runtime readiness and privileges, deliberately damage one audit trigger, migrate again and prove repair. Preserve existing populated upgrade/repeat cases.
3. Run migration once in ordinary application fixtures. The former common fixture migrated twice, and every repeat also invoked audit-trigger installation for the current policy set.
4. Compare the same bounded fixture test before/after, then run the shuffled full suite. Report the changed evidence scope rather than treating old timing as a new result.
5. If needed, separately evaluate a bare database fixture for historical-upgrade tests that currently build and then immediately discard the latest application. Preserve the same connection guards and lock ownership. This additional refactor is not implemented here.

## Executed local observations

The same `TestPantryBatchCountRejectsContradictoryUnit` ran three times on each side with verbose phase logging. Before:6.69/5.29/5.08s, median5.29s, package19.534s. After:4.61/4.68/4.58s, median4.61s, package16.124s. The removed repeat phase had a median532ms. The other median phases remained similar: first migration2318→2308ms, real application/authentication811→802ms and cleanup1252→1257ms. The first baseline reset included prior browser data, so the two aggregate package times are not a clean performance benchmark. Three local observations establish a useful signal, not a general latency guarantee or a full-suite speedup.

The separate repeat/repair contract checks an empty fresh fixture, exact stock and movement count surviving a repeat, readiness rejecting a deliberately missing audit trigger, runtime-role repair denial, owner migration repair, actual authenticated audit creation afterward and direct audit forgery denial. It ran with the URL guard and two populated historical-upgrade cases in shuffled order: all passed, web26.481s. The source review found no material issue at its stated scope; the reviewer did not execute tests. Existing historical migration/repeat tests are retained.

The complete shuffled native regression passes: web700.776s, combined internal coverage69.4% (5759/8299). The preceding batch checkpoint took772.207s with the former fixture, but these are individual suite runs with different randomized order, not a controlled benchmark. Verify passes14.8% (1254/8478),69 Node tests and43 migration checksums; tagged lint reports zero issues. No production source behavior, schema or UI is changed by the fixture checkpoint, so its acceptance does not claim a new browser/recovery/security campaign. PA11 preparation is a separate uncommitted workstream.

Focused Linux race passes (web51.543s), selecting the explicit repeat/repair contract, URL guard and the two named historical-upgrade cases. Other packages correctly report no tests to run; this is not a whole-tree race acceptance. The separately prepared PA11 code is not part of the six-file fixture checkpoint or its feature acceptance.

Evidence: ignored `outputs/expansion-review/fixture-timing-before.log`, `fixture-timing-after.log`, `fixture-contracts.log`, `fixture-full-integration.log`, `fixture-race.log`, `fixture-verify.log` and `fixture-lint.log`. Feature status stays49 verified/131 open because this foundation work does not complete a new feature.

## Defer larger caching until measured need

Plain TRUNCATE is insufficient: tests intentionally alter grants and triggers. A future dump/template must preserve table data needed by migrations, units, policies, ownership, ACLs, constraints, functions, triggers and migration state. Historical upgrade cases must bypass it. Existing test roles are NOCREATEDB, so database templates require an explicit provisioning change rather than an assumed privilege.

Any future cache must prove isolation after deliberate schema damage, empty runtime data, unchanged readiness/grant checks and repeated shuffled runs. Cache identity must include migration/source contents and server compatibility, not only migration version numbers. This remains a separate decision after the smallest measured change.
