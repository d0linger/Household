# Dependency and image updates

Household pins production dependencies and images so a release can be rebuilt and reviewed. Check the versions that are **declared and actually used**; the Go module graph also lists modules mentioned only by dependencies' `go.mod` files. Those graph entries do not become Household runtime dependencies merely by appearing in `go list -m all`.

## Audit on 16 November 2026

| Surface | Result |
| --- | --- |
| `go.mod` | 24 declared modules; `go list -m -u -json all` reports no available update for any of them. |
| Go package imports | 23 external modules are used by the inspected application/internal package tree; none has an available update. |
| npm lockfile | `npm outdated --json` reports no update. Node packages are development-only. |
| Base images | `golang:1.27.4-alpine3.24`, `alpine:3.24` and `postgres:18-alpine` resolve to the pinned manifest digests in the Dockerfiles and Compose definitions. |
| Go CI tools | Latest module queries match the pinned golangci-lint 2.15.0, gosec 2.30.0 and govulncheck 1.9.0. |
| Trivy | CI pins 0.76.2 and the SHA-256 of its official Linux archive. The PR #18 push and pull-request container checks both passed with this pin. |
| GitHub Actions | All eight distinct external actions match their upstream latest release tags, and every pinned SHA matches the corresponding tag. Dependabot checks updates against `dev` each Monday. |

The full Go module graph contains 229 entries, 148 of which show a newer release. These are not declared in Household's `go.mod` and none is imported by the checked package tree. Upgrading them as a blanket `go get -u all` would add unneeded dependencies or change version selection without a consumer. If a future feature imports one, review its version and license at that point. This is why “all dependencies” means declared/used dependencies and intentionally pinned build tools, not every transitive graph entry.

## Repeat the audit

```powershell
. ./.local/project-go.ps1
go list -m -u -json all
npm outdated --json
npm audit --audit-level=high
go mod verify
```

Check Docker manifest digests with `docker buildx imagetools inspect`, and compare every CI service image to its fixture guard before changing a pin. For scanner archives, pin the release version and published SHA-256 together. Run the affected Go/Node/Compose checks, the actual container scanner and browser suite, then promote through a checked `dev` → `main` PR. Record genuine results in [the implementation ledger](implementation.md); a release page or a dependency PR is not execution evidence.

Dependency updates follow [the release process](release-process.md). Keep old PostgreSQL/uploads volumes and security keys through an image upgrade; a new image digest does not migrate data by itself.
