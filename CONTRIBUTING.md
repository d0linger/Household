# Contributing

Keep each change focused on one problem and explain why it is needed. For a substantial new feature, agree its scope with the maintainer through an issue or an existing user-approved brief before implementation. Existing approval does not need to be requested again.

Read AGENTS.md, PRODUCT.md, DESIGN.md and docs/implementation.md before changing behavior. Use Go 1.27.3 or newer, PostgreSQL 18 and Docker Compose. Runtime assets are embedded and local; Node dependencies are development-only browser tooling.

Keep SQL parameterized, queries cancellable, module contracts explicit and immutable histories append-only. Add forward migrations rather than editing released files. Update deploy/migration-checksums.json when adding migrations, then run `node scripts/check-migrations.cjs`. CI rejects modifications/deletions of migrations present on the pull request's base.

Run `./scripts/verify.ps1 -Integration` on Windows, or the corresponding Go checks against a fresh isolated PostgreSQL database on Linux. For interface changes, run the browser suite and inspect mobile/desktop captures. For maintenance changes, run the disposable recovery rehearsal. Never use a live household as a test fixture.

Run `npm ci --ignore-scripts` once for development-only tools. `scripts/test-race.ps1` provides the pinned Linux race environment from Windows. PostgreSQL fixtures recreate their compiled schemas and must run sequentially with browser work; packages run with `-p 1` while intentional in-test concurrency and `-race` remain enabled. Use `HOUSEHOLD_REQUIRE_INTEGRATION=1` in every required database test job. Read [the test guide](docs/testing.md) for reports, fixtures, workload budgets and the patterns adapted from Treckrr/Parkrr.

Update README.md for architecture, structure, deployment or configuration changes, and record actual check evidence in docs/implementation.md. Do not turn design-review checkmarks into release claims. Keep secrets, databases, upload files, caches and generated evidence out of Git.

The repository is [d0linger/Household](https://github.com/d0linger/Household). Work on `dev`; synchronize only when explicitly requested by the owner. Promote through a same-repository `dev` → `main` PR with all required checks and resolved discussions. Use a merge commit and retain `dev`; reconcile the resulting main merge into development before the next promotion. The owner authorized checked production image publication on 2026-11-03. This does not authorize deployment or bypassing checks. Follow [the release procedure](docs/release-process.md). Never touch sibling Treckrr/Parkrr resources or rewrite published history.

Before a publication containing new files, inspect `git status`, verify the staged file list, run the affected checks, and scan both Git history and the exact staged source with Gitleaks. Local credentials, private keys, uploads, database snapshots, test recordings and generated reports must remain ignored. Keep source icons, configuration examples, migrations and deliberate `design-preview/` prototypes versioned. CI Actions remain SHA-pinned with read-only default permissions. Only the guarded production publishing job receives registry-write permission, after all same-revision prerequisites succeed; it promotes the scanned OCI candidate without rebuilding. Keep ordinary check jobs read-only.

Run `node scripts/check-local-artifacts.cjs --index` after staging: `.gitignore` does not protect against an already tracked or force-added private file. This path check complements secret scanning; it does not inspect file contents or prove that permitted files contain no personal data.

Changes to embedded templates, JavaScript, CSS or icons require a new Go binary/image. Restarting an existing container does not incorporate edited source. Validate the new build on an isolated test stack; updating a running installation requires explicit deployment authorization.

Before promotion to `main`, fill in the PR's operational-impact section against the exact base/head commits: household-data/workflow effects, manual steps, and binary/schema rollback compatibility. Prefer a forward corrective migration; never assume an old binary can run on a new schema. Reassess after either commit changes. These are review notes, not automatic deployment permission.

Keep required checks failing on scanner errors, missing test prerequisites and failed assertions. Do not use `continue-on-error`, skip tests, lower coverage floors or add broad ignore lists to make CI green. Review dependency changes for vulnerabilities, licenses and runtime necessity; scheduled scans also cover advisories published after a commit. See [the reference adoption record](docs/workflow-reference-adoption.md) for the adapted policies and deliberate differences.

Use synthetic examples in bug reports and UI evidence. Report security concerns privately using [SECURITY.md](SECURITY.md). Contributions are covered by this repository's [MIT license](LICENSE).
