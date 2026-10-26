# Participants and guests — verification ledger

MP06 is locally verified by the executed checks below. The application follows local checkpoint `ecc8391`; no live8100 upgrade, remote publication or hosted Actions run is included.

## Implemented contract

Core23/Mealplan7 add bounded meal-owned household memberships, anonymous guests and explicit manual/participant-derived meal portions. Recipe components independently retain their quantities or explicitly follow the meal. Existing rows remain manual/independent. Saves, versions, participants, component quantities, audit and idempotency evidence commit together. Newly selected members require active user/membership rows under ordered shared NOWAIT locks; saved or deliberately copied inactive members remain visible and counted.

The runtime role can select, insert and delete participants, with no update/truncate/trigger privileges. Readiness checks exact household foreign keys, unique columns/index state, trigger identity/type/deferral/search path, column types/nullability, individual grants and canonical CHECK expressions. Participant identifiers and person names are excluded from audit payloads. All59 prior migration files remain unchanged; the manifest now has61 hashes and the schema census has74 audit policies.

## Executed checks

| Check | Observed result and scope |
|---|---|
| Initial schema/readiness | Native web8.154s, passed. |
| Eight new native integration contracts | First campaign web54.662s; stricter three-CHECK tamper repeat web55.802s, both passed. Covers actual demand/source acceptance/reservation release, preserved independent quantities and identities, inactive-account copy and purge,100-member replacement/101 rejection, concurrent updates, deferred failure rollback, HTTP authorization/module privacy and populated Core22/Mealplan6 upgrade. |
| Additional activity-lock contract | Passed, native web10.515s. Separately contested membership and user rows return bounded conflicts with unchanged saved state, then actual administrative deactivation rejects a new selection. |
| Unit/JavaScript/workflow/migrations | Final verify passes15.2%(1601/10532 statements),75 Node cases and61 migration hashes. This includes15 focused planning cases for exact saved/draft impact, mode-only changes and new/removed components. The pure mealportions package has100% observed unit statement coverage; this does not imply complete application coverage. |
| Tagged lint | Final integration/testtools source passes with0issues. |
| Gosec |122 application files/18976 lines,0findings and no Go errors. Ignored preparation exports excluded by explicit cmd/internal scope. |
| GOVul/dependencies/workflows |0reachable/imported-package vulnerabilities; one unused required-module advisory remains outside the reachable application. Dependency/license review and Actionlint passed. |
| Runtime image | Final `household:participants-review`, SHA-256 `90cae003c28696b7f766a188b6aa82e9685adbf139cd49fb6a14a30adde96410`,40719083bytes, user10001:10001. Built locally, not deployed. Supersedes the pre-impact UI image. |
| Recovery helper | Final `household-backup:participants-review`, SHA-256 `233eb3c3623aee856472c4a5d3a35356fa1a831adfeab7d572d54e08d7416dba`,521366238bytes, user10001:10001. |
| Encrypted synthetic recovery | Final helper repeat passed: backup7.12s, restore-and-verify28.45s. Preserves derived3.000000 and manual2.500001, guests1/2, roster2/1 with retained inactive identity, independently keyed participant rows, linked/independent quantities, participant/audit readiness, prior domain/proof data and two file hashes. SQL-seeded persistence only; actual authenticated save/copy is tested separately, not inferred from this fixture. |

The source-pattern reviewer closed the singleton fixture, owner-only audit census and exact CHECK-expression findings, source-only, with no DB/test actions. Correct installed constraints were not weakened. Historical native/browser evidence for MP14/MP17 is preserved in its own ledger.

## Browser inspection and pending acceptance

The initial browser campaign exposed test-harness mistakes: inherited disabled state was asserted against the fieldset itself before its final state, a copy response was read after navigation, and the test omitted the application's explicit conflict-confirmation dialog. The tests now inspect effective disabled controls, use the actual copy navigation and explicitly confirm replacing the reviewed saved state. Those failures are not passing browser evidence.

Initial desktop/mobile light/dark captures were opened together. One correction batch removes repeated long names from participant removal buttons while preserving a specific accessible name, keeps the add action from stretching vertically, and gives the mobile roster readable spacing. Confirmation passed the full21-component chain with734checks, including24 participant checks/six clean axe scans/six opened valid captures. Fresh independent finish review returned `fix` for one missing ordinary pre-save impact comparison. That correction shows exact saved/draft quantities and modes for affected, added and removed components, updates from roster/guest/manual/component changes and retains a stable saved basis until explicit conflict resolution. The final complete chain passes740checks; MP06 contributes30checks/seven clean axe scans/seven opened valid captures with noJSerrors/externalrequests. The same reviewer scores the single finding resolved with disposition ship at that correction scope. The separate [incumbent-system comparison](planning-participants-design-verification.md) opens all seven captures, validates17links and preserves DESIGN.md's exact hash/absent sidecar while reporting inherited drift. No second context/detector pass or design-system replacement is included.

Complete native regression passes against isolated55434 after stopping8101:272 main tests, no failures, web1219.400s,72.7% combined internal coverage(7528/10353;55% gate). The final311-file source manifest is recorded. Scoped Linux race passes serially(web624.225s, Store1.038s, mealportions1.031s), with no race report. Separate MP11/MP12 helpers remain in an ignored preparation copy and are excluded from this candidate. Authenticated restored-service replay, real production load/RTO/RPO, hosted Actions, publication and a live8100 deployment remain unclaimed.

### Complete native first campaign — failed historical client fixture

The first complete native campaign failed `TestPlanningPurchasePopulatedPriorUpgrade` at web10.280s: the historical Core20/Mealplan4 fixture called the current weekly Store, which now reads Mealplan7 participants. The specific error is missing `mealplan.participants`; it is not a failure of the installed current schema or the new readiness function. After220main-test passes, the already-failed test child was deliberately stopped; web package911.580s. Partial coverage and the interrupted remainder are not acceptance.

The fixture now seeds the actual immutable Recipe4 snapshot and confirmed binding through parameterized actor-attributed `audit.Exec` against the real old App-role grants and guards. Its double upgrade, exact0.375001 stock, frozen-source/binding fingerprint, zero inferred requirements/operations and current explicit preview assertions remain unchanged. Focused corrected native passes(web13.856s). Source review confirms the correction and finds no additional corresponding historical misuse; that reviewer ran no tests. Only this integration-test source differs from the interrupted311-file manifest; production Go/SQL/embedded assets and final image/browser/recovery evidence remain unchanged. The complete repeat with failed Web seed1792974491750389200 passes all272main tests(web1219.400s); the corrected historical case passes11.550s within it. Its311-file manifest has no subsequent application-source differences. Scoped Linux race remains pending.

### Complete native and scoped race accepted

The passing serial Linux campaign selects all nine participant contracts, existing weekly mutation/copy/contention/schema contracts, reservations, demand/bindings, the corrected historical planning-purchase upgrade, audit contracts, migration repeat/repair and pure portion helpers. It uses the isolated database, read-only source mount, two CPUs and2GiB. Unmatched packages and unrelated tests do not receive whole-tree race acceptance. The exact selection is recorded in participants-race-scope.txt. The Windows race attempt without CGO is not acceptance; Linux provides the executed scoped race result.

Together with the740-check browser chain, actual source/claim/HTTP contracts, independent correction ship verdict and separate system comparison, current non-root images and encrypted synthetic recovery, this completes local MP06 acceptance. Checklist55verified/2inprogress(MP11/MP12)/123open.

## Evidence paths

- [Eight-contract final native result](../outputs/expansion-review/participants-eight-native-final.log)
- [Failed/interrupted complete native campaign](../outputs/expansion-review/participants-native-failed-fixture.jsonl), [corrected historical upgrade](../outputs/expansion-review/participants-prior-purchase-upgrade-corrected.log)
- [Complete same-seed native repeat](../outputs/expansion-review/participants-native-repeat.jsonl), [combined coverage](../outputs/expansion-review/participants-native-repeat-coverage.json), [race scope](../outputs/expansion-review/participants-race-scope.txt)
- [Passing scoped Linux race](../outputs/expansion-review/participants-linux-race.log)
- [Final unit/JavaScript verification](../outputs/expansion-review/participants-verify-accepted.log)
- [Final tagged lint](../outputs/expansion-review/participants-tagged-lint-final.log)
- [Gosec](../outputs/expansion-review/participants-gosec.json), [GOVul](../outputs/expansion-review/participants-govuln.log), [dependency/license inventory](../outputs/expansion-review/participants-dependencies.md)
- [Final image identities](../outputs/expansion-review/participants-images-final.log), [final recovery run](../outputs/expansion-review/participants-recovery-final.log), [restored values](../outputs/expansion-review/participants-recovery-final-report.json)
- [Initial browser confirmation](../outputs/expansion-review/participants-browser-chain.log), [final correction chain](../outputs/expansion-review/participants-browser-chain-final.log), [participant browser report](../outputs/planning-participants-review/browser-report.json)
