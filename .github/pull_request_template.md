## Problem and resulting behavior

Describe the concrete problem and what this change does. Include a before/after example when useful.

## Operational impact

- Exact base/head commits reviewed:
- Effects on existing household data, permissions and workflows:
- Manual configuration, migration or deployment steps (or explicitly none):
- Rollback compatibility and any restore requirements:

Review the actual diff, including removed settings and migrations. Passing CI or a file-category report alone does not establish release safety. The solo operator can perform this review; merging or building does not authorize deployment.

## Validation

List the commands actually run and their results. State any checks not run and why. For UI changes, include desktop/mobile evidence using synthetic data only.

## Review checklist

- [ ] Scope follows `PRODUCT.md`, `DESIGN.md` and `AGENTS.md`.
- [ ] Relevant unit, integration, browser and security checks passed, or missing evidence is explained above.
- [ ] `docs/implementation.md` records accurate evidence; `README.md` reflects structural, architectural, configuration or deployment changes.
- [ ] Schema changes use new migrations with updated checksums, audit coverage and an upgrade/rollback assessment, where applicable.
- [ ] No secrets, session data, household records, private screenshots or generated test outputs are included.

Mark a checklist item as not applicable in the validation notes when it does not apply.
