# Release branch setup checklist — 3 November 2026

- [x] Inspect remote branches, rules, PR bases and actual check results.
- [x] Review public Parkrr/Treckrr release workflows at pinned source revisions.
- [x] Isolate the workflow change from unpublished local application edits.
- [x] Implement same-revision production gates and exact-candidate GHCR promotion.
- [x] Keep dependency updates on dev and restrict production PRs to dev → main.
- [x] Complete 362 Node checks, including 44 release guard/policy/shell checks; workflow policy, Actionlint and source secret scan pass. Staged hygiene is checked immediately before commit.
- [ ] Publish the reviewed setup to dev without force-pushing.
- [x] Create main from the existing remote baseline, select it as default, and verify eleven required checks with no bypass actors and merge commits only.
- [ ] Open the dev → main setup PR and record its actual hosted status.
- [ ] Reconcile the new workflow changes with the local working tree safely.

First production publication is a separate acceptance result: it remains pending until the setup PR and all required hosted checks pass. No registry publication or remote deployment is claimed merely because this configuration exists.
