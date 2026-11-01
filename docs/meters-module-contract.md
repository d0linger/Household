# Meters and energy workspace contract

Status: complete module sources prepared; runtime acceptance is pending. This document specifies behavior, not evidence of an executed check. The 20 ME criteria remain in progress and the overall accepted count remains 140/180.

## Model and compatibility

A meter is a named household installation. A register defines one measurement type, unit, direction, IANA timezone and input stream. A physical device has its own serial number and reviewed register periods. Import, export and generation remain independent registers; unrelated counter values are never subtracted. Once devices, plans, tariffs, bills, payments or observations depend on a register, its type/unit/direction/timezone/stream cannot change. Metadata, explicit thresholds, archive state and administrator retention settings remain editable with current versions.

The existing generic meter/device/reading routes remain available. Generic readings apply only to the manual cumulative default register. Other measurement types and register corrections use the workspace. Reviewed physical boundaries cannot be rewritten through generic device forms. Existing timestamps, exact values and devices survive migration; historical date-only device boundaries remain visibly unreviewed and no final reading is invented.

Five forward migrations follow the complete accepted Chores release: Meters2 → Core33 → Fuel10 → Pantry16 → Mealplan11. Earlier Core26–32 and optional-module barriers remain intact. All 97 released SQL files retain their exact bytes. The prepared release has 102 SQL pins, 20 Meters tables, 152 audit policies and 123 audited domain tables in the existing census. No foreign key points to optional Expenses: the reviewed bill records the Core-mediated entry identity and exact target generation.

## Authorization and requests

Standard-library HTTP and local embedded HTML/CSS/JavaScript serve seven panes at `/m/meters/workspace/{overview,capture,history,costs,plans,imports,settings}`. All private responses are `private, no-store`. The service worker must not persist private JSON, reading photos or credentials. Browser drafts and uncertain command bodies live only in the current page memory and are cleared on permission loss or page teardown.

Every read rechecks the actual session, membership, role, password state, enabled module and generation. A repeatable-read snapshot bounds each read to eight seconds. Writes use the existing Core household/module contract, then the household Meters advisory lock and resource locks. The 20-second command deadline is unchanged. Current rights are checked before returning a permanent command receipt. Linked bills additionally require current Financial authority and the original target generation even when a command key is replayed.

Commands are closed JSON objects with explicit `generation`, `version`, `action`, `id`, `data` and, only for financial transfer, `target_generations`. Unknown, duplicate, case-ambiguous and inappropriate null fields are rejected. The body is limited to 1 MiB. Mutations require CSRF and a UUID `Idempotency-Key`. Every register-bound action also submits its rendered `register_version`; a fresh operation rejects stale units/configuration. A previously authorized unchanged receipt remains replayable after later edits while current authority still permits it. Permanent receipts bind actor, household, key, operation, generation and exact request hash, with a 128 KiB response bound. They survive short Core receipt cleanup and disappear only in explicit Owner purge.

| GET `/api/v1/meters/…` | Bounds and meaning |
|---|---|
| `context`, `overview` | Current authority/module state, at most 250 meters/registers, latest matching observation, quality, interval and ambiguity/pending indicators. Oversized inventory is rejected. |
| `devices`, `plans`, `sources`, `changes` | Parameterized household/register management lists; at most 250 rows and 2 MiB. Sources expose status and mapping, never endpoint secrets. |
| `history` | Explicit register and at most 366 inclusive civil dates; 100 observations/page, stable time/id keyset cursor bound to household, session, generation and filters. Manual, CSV and telemetry provenance stay distinct. |
| `series` | Same date bound; at most 2,500 requested hour/day/month aggregates. Hour windows require narrowing if too large. Month mode requires complete calendar months. No raw-history scan or inherited 10,000-point report cap is used. |
| `tariffs`, `payments`, `bills` | Explicit register/period filters applied before the 250-row limit. Tariffs intersect the period; actual payments and invoices occur within it. Full current Financial visibility applies to linked bills before pagination. |

POST `/api/v1/meters/commands` supports meter/register/device save, explicit device replacement, reading/correction, dated tariff, actual payment, draft bill, bill confirmation, explicit bill handoff, reading plan, administrator source approval and CSV preview/apply. Decimal measurements are strings with at most 12 integer and six fractional digits; positive money is an integer-cent string. No float monetary arithmetic is used.

## Measurements, corrections and calculations

Cumulative counters produce an exact delta only between compatible physical observations. A documented rollover requires a declared counter modulus and explicit confirmation. An unexplained decrease remains unknown. A device boundary without an old final value remains a gap. The new device's own initial value anchors its later consumption. An explicitly reviewed telemetry-device final value belongs to device-change evidence and projected boundaries, never to fabricated raw adapter messages.

Interval quantities require explicit start/end instants and cannot overlap. Their values are quantities, not counters. Instantaneous `W`/`kW` is integrated by the trapezoidal rule only within the configured maximum sampling gap, producing estimated `kWh`. Wider gaps have unknown consumption. Manual readings and raw telemetry use separate registers/storage and retention contracts. Historical corrections append immutable revisions; they never replace prior evidence.

Calendar buckets use actual timezone boundaries, including 23/25-hour days, repeated hours, half-hour transitions and whole-day gaps. The shared civil-time helper preserves the accepted Chores policy: earlier fold, forward gap. CSV imports refuse ambiguous/nonexistent local timestamps unless an explicit offset resolves the instant. PostgreSQL precision is microseconds.

Only observed intervals contribute quantities/coverage. Known zero remains zero; missing data remains null. Splitting an interval across time/tariff boundaries is identified as estimated allocation. Readouts show covered/estimated/total seconds and no hidden extrapolation. Partial periods are compared explicitly using the same register type and unit.

Dated tariffs have immutable work price in cent/unit and a daily civil base price. Open predecessors close only through an explicit reviewed command. Overlap is rejected under the household lock. Base cost uses fractions of the actual civil day; price allocation uses exact rational arithmetic. Money rounds once at the presentation boundary. A missing tariff is not free. Actual paid advances remain separate from cost estimates. Only a confirmed original invoice/advance can be explicitly transferred as one posted, Core-authorized manual Finance snapshot; adapters cannot post money.

Daily warnings require the configured minimum 7–100 actual intervals and at least six days of measured history. Power-register base-load warnings require 7–100 distinct nights, each with at least 80% observed coverage, and use an explicit threshold with a lower-quartile night estimate. Warnings remain explanations, never automatic billing or fabricated faults.

Actual reading-plan completion retains plan version, due period and exact immutable reading revision. Corrections that supersede that fact stop satisfying the current view; a later qualifying revision appends its own fact. Estimated readings count only if the plan explicitly allows them. The bounded search considers 1,000 periods and displays its horizon rather than inventing a next date.

CSV accepts UTF-8, explicit delimiter/timezone/unit/device, at most 512 KiB and 1,000 rows. Preview validates the closed profile, exact decimals, timestamp uniqueness, device period, existing observations and interval collisions. It shows first 50 preview rows, full row count, known duplicates and proposed insert count. Final apply rechecks register/mapping versions, source revision, generation and all actual neighbor constraints in one transaction. Conflict rolls back every row. Exact physical duplicates are retained once, with insert/duplicate counts returned separately.

## Projection jobs and retention

The existing worker schedules `energy_workspace` jobs. Every batch checks current lease, generation and enabled state under the same bounded lock order and checks actual wall-clock lease validity again before commit. Work is finite: 512 points for intervals, 48 actual hour buckets for aggregation, two retention registers and 500 deletion candidates per register. Failed/expired work rolls back. Pending work continues with a persisted cursor.

Reading corrections and reviewed device boundaries invalidate their neighboring intervals. Tariff changes use current dated prices without rereading raw history. Dirty intersections suppress stale current costs/totals. Hour aggregates roll into day and month projections; reads use these aggregates beyond 10,000 raw observations.

Retention is off by default, administrator-controlled and limited to raw telemetry. Manual readings, revisions, device changes, bills and receipts are retained. The application has no direct raw DELETE grant. The narrowly scoped Owner function requires an actual current energy lease, configured age, no dirty projections and known completed hour aggregates. It preserves a predecessor anchor per device and stored historical aggregates, records the retained floor, rejects older new raw samples and prevents later recalculation from erasing preserved aggregate history. Owner maintenance purge uses the established generation-fenced, separately authorized lifecycle and all 20 schema-local tables.

## Optional adapters and operator configuration

No endpoint is contacted without explicit operator configuration and an enabled administrator-approved source. Up to 16 sources per household may be enabled. Exact entity/topic, physical device, measurement type and unit are mandatory. Archive, disabled module, generation/version change, disabled issuer or revoked administrator membership stops new claims and invalidates in-flight writes. Status is coded/redacted; no payload, token, password or arbitrary URL enters browser/audit/logs.

| Environment | Meaning |
|---|---|
| `HOUSEHOLD_METERS_HA_URL` | Fixed trusted `https://host[:port]` origin; no credentials, path, query or fragment. |
| `HOUSEHOLD_METERS_HA_TOKEN_FILE` | Operator-owned Home Assistant token file, mounted through `compose.meters-ha.yaml`. |
| `HOUSEHOLD_METERS_MQTT_URL` | Fixed authenticated `mqtts://host[:port]` broker, mounted with `compose.meters-mqtt.yaml`. |
| `HOUSEHOLD_METERS_MQTT_USER`, `HOUSEHOLD_METERS_MQTT_PASSWORD_FILE` | Explicit broker account and mounted password file. |
| `HOUSEHOLD_METERS_CA_FILE` | Optional mounted PEM CA bundle, maximum 128 KiB; `compose.meters-ca.yaml` provides the secret mount. System roots otherwise apply. |
| `HOUSEHOLD_METERS_ALLOW_PLAINTEXT` | Defaults false. Explicitly permits operator-trusted HTTP/MQTT, principally isolated counterpart testing. No TLS verification bypass exists. |

Compose overlay host variables are `HOUSEHOLD_METERS_HA_TOKEN_PATH`, `HOUSEHOLD_METERS_MQTT_PASSWORD_PATH` and optional `HOUSEHOLD_METERS_CA_PATH`. Combine only the overlays actually configured with the normal `compose.yaml`. Application and PostgreSQL remain the same isolated Docker services; no extra production telemetry container is required. Rotate a secret by replacing the operator-owned file and restarting the app. Disable a source in Settings or remove its operator configuration to stop ingestion; existing values remain.

HA uses fixed `/api/states/sensor.…`, bounded 64 KiB responses, a five-second request timeout, verified TLS, no HTTP proxy or redirects and the source's real `last_updated`. `total_increasing` is required for cumulative sensors; `measurement` for interval/power, with explicit interval start where applicable. Only the closed m³→m3 alias is normalized. Unknown/unavailable/nonnumeric states are rejected.

MQTT uses authenticated MQTT3.1.1, an exact non-wildcard topic, a persistent source/generation client session and requested QoS1. At most 100 packets, 64 KiB/frame and 16 KiB/measurement are read within eight seconds. Payloads use the closed `measured_at,value,measurement_kind,unit_code,quality,rollover_confirmed` fields, plus interval start only when required. Timestamps/values normalize before source-key hashing. Physical time duplicates cannot create a second fact. PUBACK follows committed insertion, an already committed identical fact, or a durable rejected-message digest; lease/rate/storage failure receives no acknowledgement. QoS0 remains visibly best effort. Persistent per-source budget allows at most 120 new samples/minute. Backoff is bounded to one hour, shutdown cancels network operations and joins both optional feed goroutines.

Protocol references: [Home Assistant REST API](https://developers.home-assistant.io/docs/api/rest/), [OASIS MQTT3.1.1 specification](https://docs.oasis-open.org/mqtt/mqtt/v3.1.1/os/mqtt-v3.1.1-os.html). These references do not imply that a production household endpoint has been configured or tested.
