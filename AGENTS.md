# Household development instructions

Read `PRODUCT.md`, `DESIGN.md`, and `docs/implementation.md` before changing behavior. Always load the available `golang-how-to` skill for Go tasks and the relevant security/database/testing skills. The explicit user brief takes precedence over generic skill defaults about schema creation: implement the approved PostgreSQL schemas and narrowly scoped secure triggers.

Use standard-library HTTP, explicit parameterized PostgreSQL queries, bounded resources, and context propagation. No frontend CDN, external font, tracking script, or production demo data. Keep module schemas independent; cross-module integrations go through core contracts.

Update `README.md` whenever structure, architecture, configuration, deployment, or module availability changes. Keep implementation evidence honest in `docs/implementation.md`; do not mark operational checks passed unless they were actually executed. Run appropriate unit, integration, browser, and security checks for the affected behavior.

Never touch sibling Treckrr/Parkrr containers, databases, or files. Household local HTTP defaults to 127.0.0.1:8100 and isolated test PostgreSQL to 127.0.0.1:55434. The Git remote is `https://github.com/d0linger/Household.git`. Work on `dev`, review promotion to `main` through a pull request, and never force-push or deploy as part of synchronization. Do not publish or push without the user's explicit instruction; the initial synchronization was explicitly requested on 2026-11-02.
