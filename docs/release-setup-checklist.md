# Release branch setup checklist — 3 November 2026

- [x] Inspect remote branches, rules, PR bases and actual check results.
- [x] Review public Parkrr/Treckrr release workflows at pinned source revisions.
- [x] Isolate the workflow change from unpublished local application edits.
- [x] Implement same-revision production gates and exact-candidate GHCR promotion.
- [x] Keep dependency updates on dev and restrict production PRs to dev → main.
- [x] Complete 362 Node checks, including 44 release guard/policy/shell checks; workflow policy, Actionlint and source secret scan pass. Staged hygiene is checked immediately before commit.
- [x] Publish setup commit `d07f6e8cf43b2bf5876a19b349cefc1f0f6f82fe` to dev without force-pushing.
- [x] Create main from the existing remote baseline, select it as default, and verify eleven required checks with no bypass actors and merge commits only.
- [x] Open [dev → main PR #11](https://github.com/d0linger/Household/pull/11) and inspect actual hosted results: PromotionSource/container pass; the dev BrowserAccessibility run fails on the existing catalog/mobile-navigation assertion. Remaining long-running checks were still running/queued at the recorded inspection.
- [x] Reconcile workflow history into local dev (`0cf1e3c`), preserve all unrelated existing file bytes and unpublished application edits, and pass 411 Node tests plus workflow policy/Actionlint on the combined local source. The local merge was not pushed.

First production publication is a separate acceptance result: it remains pending until the setup PR and all required hosted checks pass. No registry publication or remote deployment is claimed merely because this configuration exists.

- [ ] Resolve the existing application CI failure on the exact PR source, complete every hosted check and review the promotion diff before merging.
- [ ] Observe an actual successful main production run and verify the published GHCR digest/tags. No physical deployment is part of this setup.

## Application promotion — 13 November 2026

- [x] Review the completed application scope and push it to `dev` under the user's explicit promotion instruction (`49b55f1` plus focused CI corrections).
- [x] Update existing PR #11 with the application scope, Core35–40 upgrade/backup requirements and rollback limits.
- [x] Resolve the Gosec package-loading failure without omitting packages or rules; exact `e2536df` push and PR GOSec jobs pass.
- [x] Build and scan the next local candidate; defer activation until the final application source passes acceptance.
- [ ] Complete the remaining browser correction and all final-head hosted checks.
- [ ] Merge the exact accepted `dev` head into `main` and reconcile the merge back into `dev`.
- [ ] Verify the resulting production image publication and final local development refresh.

Earlier setup-only entries above describe their recorded historical state. Physical-device notification acceptance remains deferred; publication does not authorize remote deployment.
