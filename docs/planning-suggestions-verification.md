# Recipe alternatives — verification ledger

MP11/MP12 are locally verified after participant checkpoint886f09f. This ledger records executed checks and their scope. Live8100 remains unchanged; no publication, push or hosted workflow is included.

## Implemented contract

One current-authorized,15-second read-only RR snapshot evaluates a searched creation-order page of20immutable recipe sources plus sentinel, at most500ingredients/16MiB and5000relevant lots/active allocation dependencies. Explicit catalogue/Core/product mappings determine identity. Repeated ingredient quantities are grouped before one stock subtraction per product. Each recipe independently sees the same current net free stock; active current-generation claims are foreign. Unknown quantities/dimensions cannot imply complete coverage. Passed use-by quantities remain physical but are excluded from coverage. Opening guidance, use-by and best-before reasons remain separate, with no expiry-driven recommendation boost for review states.

Every bounded lot contributes to evaluation. Presentation shows at most50date reasons per candidate with its explicit full count, review evidence first. Signed cursors bind session/actor/query/portions and validate generations, module activity, expiry, timezone, current household day and resolved planned date inside that same read transaction. Every page observes fresh stock; ordering is scoped to its evaluated page.

The local weekly disclosure provides search, date/portions, exact shortages/reasons, direct source repair, empty/error/disabled states and reader access. Parent quantity/date changes invalidate pending/results; late replies cannot restore actions. Selecting a changed source requires a named decision and then the existing explicit source preview. Staging still writes nothing; only meal save commits it. No new schema or migration is introduced;61hashes remain unchanged.

## Executed evidence

| Check | Result and scope |
|---|---|
| Source-pattern review | Existing exact scaling/mappings/free-lot/claim boundaries reviewed. Nil selected-array SQL trap avoided. Past-day and cursor-context concerns closed source-only; reviewer ran no tests. |
| Prepared pure/compile checks | Final prepared Store1.084s/Web2.239s passed; integration compile-only2.501s. Fifteen pure Store contracts, two Web cursor/request contracts; no database claim from compile-only. |
| Six native PostgreSQL contracts | Passed, Web37.047s, isolated55434. Real reservations/duplicate ingredients/independent alternatives/read-only domain counts;60-lot coverage/use-by review; actual reader/session/module privacy; stable page anchor before ranking;500-ingredient/cancellation recovery; authenticated signed HTTP scope with actual timezone and module changes. |
| Complete native regression |Passed295main tests, no failures; Web1234.590s, combined internal coverage73.3%(7957/10851;55% gate). All six new contracts also pass within this complete run. The327-file source/dependency manifest matches afterwards. |
| Scoped Linux race |Passed, Web603.489s/Store1.070s. All42selected Web contracts and matching pure/request/cursor helpers run with a read-only source mount, two CPUs/2GiB, serial isolated database access. No race report. Unmatched packages/tests do not get whole-tree race acceptance. |
| Verify | Final passed16.4%(1814/11030statements),78Node cases, Go vet/mod verify, workflow policy and61migration hashes. Unit coverage is not full application coverage. |
| Tagged lint | Current integration/testtools source passes0issues. |
| Gosec |128application files/19999lines,0findings. Explicit cmd/internal scope excludes ignored exports. |
| GOVul/dependencies/Actionlint |0reachable/imported-package vulnerabilities; existing unused required-module advisory remains. Dependency/license inventory and Actionlint pass. |
| Initial browser |29checks/eight clean axe scans/eight opened valid captures. Failed fixture/details observations and missing concrete catalogue reason preceded this result. |
| Initial corrected complete chain |All22components pass776checks; suggestions36checks/nine clean axe scans/nine opened valid captures, no JS errors/external requests. Includes actual reader, late response invalidation, changed source and explicit save boundary. |
| Independent finish review |Initial fix for SUG-F01: staging hid the activated preview control without assigning focus. Final same-viewpoint confirmation passes778checks; suggestions38checks/ten clean axe scans/ten opened valid captures, including real Enter-key activation and focus after comparison invalidation. The same reviewer scores SUG-F01 resolved/disposition ship at correction scope, with no regression observed there. |
| Runtime image |Locally built `household:suggestions-review`, SHA-25639a9b896b41f0e438d1bca21ea497ccc21605712e289a4418ecaf553cbe6c021,40858389bytes, user10001:10001. No deployment. |
| Backup helper |Locally built `household-backup:suggestions-review`, SHA-2562817da44b94e2f1cdb03e3bfffcfb336dce618e1acd8f90b20ce9505115e675f,521505568bytes, user10001:10001. |
| Current encrypted synthetic recovery |Passed in separate restore-db: backup8.00s, restore-and-verify28.19s. Preserves exact existing stock/projections, source versions/mappings, roster/guest/portion identities, durable operation/proof/detail counterexamples and two file hashes. No new suggestion persistence exists; this is SQL-seeded persistence, not authenticated suggestions from a restored service. |
| Separate design documentation |The documenter opened all ten captures and validated23local links in [the incumbent-system comparison](planning-suggestions-design-verification.md). DESIGN.md hash preserved and absent sidecar/inherited drift reported without repair. |

The first attempted full chain passed the first component, then failed login because the operator incorrectly forced the bootstrap password for subsequent components after password change. That failed campaign remains recorded. Its corrected complete repeat uses the suite's intended per-component defaults/private session handoff. The production login budget is unchanged.

## Local acceptance and operational limits

Complete native and scoped Linux race passed serially against55434 with the review server stopped and an unchanged327-file source/dependency manifest. Together with778browser checks,38suggestion checks/ten clean axe scans/ten opened captures, SUG-F01 resolved/ship at correction scope, separate design comparison, current images/recovery and the recorded source/security/workflow evidence, MP11/MP12 meet local feature acceptance. Checklist57verified/2inprogress(EX02/EX19)/121open. Separate ignored financial preparation is excluded from this candidate. No authenticated API replay from the restored service, production capacity/RTO/RPO, hosted Actions, live8100 upgrade or remote publication is claimed.

## Evidence

- [Six native contracts](../outputs/expansion-review/suggestions-six-native-first.log), [final verify](../outputs/expansion-review/suggestions-verify-final.log), [tagged lint](../outputs/expansion-review/suggestions-tagged-lint-final.log)
- [Gosec](../outputs/expansion-review/suggestions-gosec.json), [GOVul](../outputs/expansion-review/suggestions-govuln.log), [dependency/license inventory](../outputs/expansion-review/suggestions-dependencies.md), [Actionlint](../outputs/expansion-review/suggestions-actionlint.log)
- [Initial browser result](../outputs/expansion-review/suggestions-browser-initial-corrected-labels.log), [first complete corrected chain](../outputs/expansion-review/suggestions-browser-chain-final.log), [pre-focus-fix browser report](../outputs/expansion-review/suggestions-browser-before-focus-fix.json)
- [Final focus-corrected complete chain](../outputs/expansion-review/suggestions-browser-chain-final-focus.log), [final component report](../outputs/planning-suggestions-review/browser-report.json), [native source manifest](../outputs/expansion-review/suggestions-native-source-manifest.json)
- [Complete native result](../outputs/expansion-review/suggestions-native.jsonl), [combined coverage](../outputs/expansion-review/suggestions-native-coverage.json), [exact scoped race selection](../outputs/expansion-review/suggestions-race-scope.json)
- [Passing scoped Linux race](../outputs/expansion-review/suggestions-linux-race.log)
- [Failed full-chain password configuration](../outputs/expansion-review/suggestions-browser-chain-failed-password-parameter.log), [failed fixture](../outputs/expansion-review/suggestions-browser-failed-fixture.json), [failed catalogue-label case](../outputs/expansion-review/suggestions-browser-failed-catalog-reason.json)
- [Image identities](../outputs/expansion-review/suggestions-images.log), [current recovery run](../outputs/expansion-review/suggestions-recovery.log), [restored values](../outputs/expansion-review/suggestions-recovery-report.json)
