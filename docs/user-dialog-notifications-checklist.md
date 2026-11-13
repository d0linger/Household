# User creation dialog and device notification plan — 3 November 2026

Scope: implement the explicitly requested user-creation popup, plan cross-device notifications and their security requirements, then refresh the authorized local Docker deployment. Notification expansion is a plan awaiting the user's decision; this increment does not activate provider delivery or rotate production keys.

- [x] Replace the permanently displayed form with an explicitly opened native dialog.
- [x] Preserve role/CSRF/recent-authentication controls and drafts during nested confirmation.
- [x] Add cancel/Escape/backdrop behavior, focus handling and credential clearing on dismissal.
- [x] Preserve Ocean/Pink and German/English; update dependent browser workflows to open the dialog.
- [x] Verify actual creation, stale-session retry, closed defaults, cancellation, narrow layouts and accessibility: 199 settings/security browser assertions and 32 clean Axe scans.
- [x] Review current notification/security contracts and official platform/reference sources.
- [x] Deliver the [notification architecture, security matrix, 56-step rollout and acceptance plan](notifications-device-plan.md).
- [x] Update README/design/testing/implementation evidence.
- [x] Clean only task-owned resources, rebuild and scan the exact local image, then verify the refreshed stack; data volumes and secrets preserved.

Evidence: ignored `outputs/user-dialog-notifications/`. Checks are completed only after their recorded execution. No new database migration or dependency is planned for the dialog change.
