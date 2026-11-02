# Contributing

Read AGENTS.md, PRODUCT.md, DESIGN.md and docs/implementation.md before changing behavior. Use Go 1.27.3 or newer, PostgreSQL 18 and Docker Compose. Runtime assets are embedded and local; Node dependencies are development-only browser tooling.

Keep SQL parameterized, queries cancellable, module contracts explicit and immutable histories append-only. Add forward migrations rather than editing released files. Update deploy/migration-checksums.json when adding migrations, then run `node scripts/check-migrations.cjs`. CI rejects modifications/deletions of migrations present on the pull request's base.

Run `./scripts/verify.ps1 -Integration` on Windows, or the corresponding Go checks against a fresh isolated PostgreSQL database on Linux. For interface changes, run the browser suite and inspect mobile/desktop captures. For maintenance changes, run the disposable recovery rehearsal. Never use a live household as a test fixture.

Run `npm ci --ignore-scripts` once for development-only tools. `scripts/test-race.ps1` provides the pinned Linux race environment from Windows. PostgreSQL fixtures recreate their compiled schemas and must run sequentially with browser work; packages run with `-p 1` while intentional in-test concurrency and `-race` remain enabled. Use `HOUSEHOLD_REQUIRE_INTEGRATION=1` in every required database test job. Read [the test guide](docs/testing.md) for reports, fixtures, workload budgets and the patterns adapted from Treckrr/Parkrr.

Update README.md for architecture, structure, deployment or configuration changes, and record actual check evidence in docs/implementation.md. Do not turn design-review checkmarks into release claims. Keep secrets, databases, upload files, caches and generated evidence out of Git.

The repository is [d0linger/Household](https://github.com/d0linger/Household). Work on `dev`; synchronize only when explicitly requested by the owner. Review changes through a pull request before promoting them to `main`. A push is not permission to deploy. For the initial empty repository, `dev` is the first synchronized branch; do not invent a production release or overwrite remote history. Never touch sibling Treckrr/Parkrr resources.

Before the first push or a publication containing new files, inspect `git status`, verify the staged file list, run the affected checks, and scan both Git history and the exact staged source with Gitleaks. Local credentials, private keys, uploads, database snapshots, test recordings and generated reports must remain ignored. Keep source icons, configuration examples, migrations and deliberate `design-preview/` prototypes versioned; do not copy broad image exclusions that would hide required assets. CI Actions remain SHA-pinned, use read-only default permissions, and build container candidates without publishing them.
