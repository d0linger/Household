# Household

<!-- impeccable:product-schema 1 -->

## Platform

web

## Stack

User-specified Go, PostgreSQL, Docker Compose, and mobile-first PWA. Standard-library HTTP and server-rendered templates with local JavaScript. No external runtime scripts, styles, fonts, analytics, or mandatory paid services.

## Users

Members of one household per installation. Administrators configure users and modules; editors maintain household records; readers inspect permitted records.

## Product Purpose

Keep shopping, vehicle fuel, recipes, pantry stock, and expenses together while allowing each module to be disabled independently without losing its data.

## Operating Context

German application UI, English operator documentation, Europe/Vienna default timezone, EUR default currency. Mobile capture and quick daily actions are primary; desktop remains fully supported.

## Capabilities and Constraints

The user approved the full implementation plan on 2026-10-24 by requesting all work in the recommended order. Initial modules are shopping, fuel, recipes, pantry, expenses. Selected recommended extensions are chores, meters, documents, and meal planning, delivered disabled by default. Shared capabilities are attachments, reminders, assisted capture with explicit review, and cross-module search. Optional external integrations remain off until configured. Application data starts empty; fixtures are confined to tests or explicit demo seeding.

## Evidence on Hand

Original reference: `C:/Users/admin/Downloads/Household Modulsystem.html`. Reviewed prototypes and screenshots: `outputs/design-review/`. Those prototypes use illustrative data and are not application functionality or implementation evidence.

## Product Principles

- Deactivation preserves records; deletion is a separate deliberate maintenance action.
- Every mutation is authorized on the server and linked to the authenticated actor.
- Exact amounts and auditable corrections protect household records.
- Integrations preview changes and work without mandatory external services.
- Recovery must be tested, not inferred from the existence of a backup file.

## Accessibility & Inclusion

Target WCAG 2.2 AA, keyboard access, visible focus, reduced motion, responsive layouts, and useful validation errors. Dark/light/system appearance follows the approved prototypes.
