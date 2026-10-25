# Isolated test fixture performance — next foundation step

Status: design from source inspection; no optimization is implemented by this document. PA04/PA05 acceptance remains in implementation.md. The full native RE12 web package took642.856s; the first expanded batch run exceeded the previous12-minute ceiling. Raising a timeout is not a runtime improvement.

## Preserve the safety contract

- Retain the exact isolated database-name/server-role checks, exclusive test advisory lock, separate owner/runtime pools, fresh schemas, real bootstrap/login and cleanup.
- Keep historical populated-upgrade tests on actual older migrations; do not replace their data with current-schema fixtures.
- Keep grants, trigger completeness, readiness, cross-connection commits, concurrency and real rollback tests. An enclosing transaction cannot isolate HTTP/worker writers on other connections.
- Never use sibling applications or the live Household database as a source, target or cache.

## Smallest next change

1. Measure reset, first migration, repeated migration, bootstrap/login and cleanup separately. Record actual durations; do not infer a savings percentage from source counts.
2. Add one dedicated fresh-install/repeat regression: migrate twice, verify runtime readiness and privileges, deliberately damage one audit trigger, migrate again and prove repair. Preserve existing populated upgrade/repeat cases.
3. Run migration once in ordinary application fixtures. The common fixture currently migrates twice, and every repeat also invokes audit-trigger installation for the current policy set.
4. Compare the same bounded fixture test before/after, then run the shuffled full suite. Report the changed evidence scope rather than treating old timing as a new result.
5. If needed, separate a bare database fixture for historical-upgrade tests that currently build and then immediately discard the latest application. Preserve the same connection guards and lock ownership.

## Defer larger caching until measured need

Plain TRUNCATE is insufficient: tests intentionally alter grants and triggers. A future dump/template must preserve table data needed by migrations, units, policies, ownership, ACLs, constraints, functions, triggers and migration state. Historical upgrade cases must bypass it. Existing test roles are NOCREATEDB, so database templates require an explicit provisioning change rather than an assumed privilege.

Any future cache must prove isolation after deliberate schema damage, empty runtime data, unchanged readiness/grant checks and repeated shuffled runs. Cache identity must include migration/source contents and server compatibility, not only migration version numbers. This remains a separate decision after the smallest measured change.
