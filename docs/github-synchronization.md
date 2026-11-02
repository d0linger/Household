# GitHub synchronization

The owner created the public repository [d0linger/Household](https://github.com/d0linger/Household) and explicitly requested synchronization on 2026-11-02. The remote was verified empty before linking `origin`. The existing `dev` history is preserved; no unrelated remote history is replaced. Future pushes still require an explicit instruction.

## Repository rules

- Develop on `dev`; review promotion to `main` through pull requests. The first synchronization does not declare a production release.
- Never force-push, mirror local refs indiscriminately, or deploy merely because a push was requested.
- Scan Git history and the exact staged source before initial publication; inspect the staged file list and required assets.
- Keep environment files, private keys, local databases/uploads/backups, caches, worktrees, recordings and generated reports outside Git. Track the example environment files, application icons, migration bytes and deliberately authored design-preview sources.
- Maintain SHA-pinned Actions, read-only workflow tokens, nonpersistent checkout credentials, explicit timeouts and cancellation of superseded workflow runs.
- Preserve Go lint, security, vulnerability, dependency, race/integration, coverage, migration, browser, accessibility and OCI checks. Container candidates are built without automatic publication or deployment.
- Run the design-preview source/HTTP tests and generated-asset freshness checks in both the developer verification script and CI. The local visual runner currently requires Edge and is a separate check.
- Keep Go, npm, GitHub Actions and Docker dependencies under weekly Monday Dependabot review with bounded pull-request counts.

## Reference adaptation

[Parkrr's ignore rules](https://github.com/d0linger/Parkrr/blob/main/.gitignore) and [Treckrr's ignore rules](https://github.com/d0linger/Treckrr/blob/main/.gitignore) informed the exclusions for private key material, Go workspace files, local data, build output, review artifacts and recordings. Their broad screenshot rules were not copied over application icons or authored prototype source. Existing Household checks are preserved where they are more comprehensive.

[Parkrr](https://github.com/d0linger/Parkrr/blob/main/.github/workflows/ci.yml) and [Treckrr](https://github.com/d0linger/Treckrr/blob/main/.github/workflows/ci.yml) informed the workflow review; Household retains its independent databases, ports, module migrations and resource ownership. No sibling installation or file was modified.

## Local evidence and limits

Preparation checks are recorded under ignored `outputs/github-sync-review/`: full-history Gitleaks, exact-index scanning, focused Go tests, workflow policy, actionlint and Node checks. The current source's Go verification covers `internal/auth`, `internal/store`, `internal/web`, `internal/worker` and `internal/webpush`; it is not a new complete database/race/browser acceptance campaign. The unchanged 107 migration checksums and completed 180-item task list were checked separately.

Two active GitHub rulesets were applied and read back: `Household branch history` prevents deletion and force-pushes of `dev` and `main`; `Household main review` requires a pull request, resolved review conversations and nine up-to-date GitHub Actions checks before updates to `main`. The required contexts are GOLint, GOSec, GOVul, GODep, WorkflowAndSecrets, Coverage, BrowserAccessibility, ShoppingExtensionsBrowser and container, bound to the verified GitHub Actions integration. No extra approver is required for this single-maintainer repository. `dev` remains directly synchronizable. These rules also cover a future `main`; no production branch was created during preparation.

Repository-level Actions permissions were set to read-only, Actions approving pull requests was disabled, and dependency vulnerability alerts were enabled. The settings were verified through GitHub's API and recorded locally in `repository-settings.json`. The existing public visibility was retained.

Hosted Actions begin only after a successful push. Local preparation does not imply that those hosted jobs passed. Remote commit identity and run status are verified separately after synchronization. The local Docker deployment is outside this operation.

## First hosted run corrections

The initial pushes exposed Linux-only workflow lint findings and missing PDF/OCR integration prerequisites. Both browser readiness loops now use an intentionally unused loop variable accepted by ShellCheck. The integration shards and full browser job explicitly install Poppler, Liberation fonts and Tesseract with English/German language data, log tool versions, and require both languages. Tests remain mandatory; no assertion, timeout or coverage gate was relaxed.

The first mobile browser sweep also rejected the meal-planning page. Its long module title and primary action were constrained to one flex row. Mobile headings now wrap the action onto another row when needed. A local reproduction at 320px measured 374px document width before the fix and 320px afterwards; 18 title/viewport combinations passed overflow, non-overlap and 44px action-target checks. The actual isolated application subsequently passed all 32 module entity pages at 390px and the meal-plan page at 320px. This focused CSS verification does not substitute for the hosted full browser suite.

Actual Linux actionlint1.7.12 with ShellCheck0.11.0 passed all five workflows in an isolated, resource-bounded container after these corrections. Installation and end-to-end acceptance remain the responsibility of the following hosted run. Detailed original failures and focused verification are retained in ignored outputs/github-sync-review/.

The dedicated shopping browser job failed twice on GitHub while reopening a saved list after going offline. A full local reproduction against the committed application plus the CSS correction passed 37 functional checks, three Axe scans and three screenshots, including that same offline reload, expiry, session refresh and logout. No speculative application change or weakened assertion was made for this unresolved hosted-only difference. Failure diagnostics are added to the browser runner for the next hosted run. The reproduction used its own bounded PostgreSQL container and server on 8101; production 8100 was untouched.
