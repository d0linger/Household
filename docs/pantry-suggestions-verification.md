# PA15 — standalone Pantry recipe suggestions

Status: locally verified. This increment follows locally saved MP11/MP12 checkpoint16affdd. It does not change schemas, migrations or booking semantics. Live8100 remains untouched.

## Behavior and boundaries

`/m/pantry/recipe-suggestions` opens the existing explanation/quantity/date comparison directly from Pantry. Its private `/api/v1/pantry/recipe-suggestions` endpoint uses the same15-second current-authorized read-only RR reader, immutable Recipe versions, explicit mappings, exact grouped demand and complete bounded free stock/date evaluation as MP11/MP12. It works with Mealplan disabled. Active claims are still subtracted while that module is enabled; disabling it atomically releases claims under the existing lifecycle contract.

The route chooses EntryModule; arbitrary request parameters cannot choose authority. EntryModule is bound to request HMAC and the snapshot scope. MealplanEnabled is part of that same snapshot comparison; the retained Generation still means Mealplan generation for existing Core claims. Default Store callers retain the Mealplan entry requirement. Current reader/session, disabled Recipes and disabled Pantry retain their respective privacy boundaries.

An empty comparison date resolves on the server using the current household timezone/day. The UI explicitly evaluates, invalidates changed filters and hides stale results after failure. Read-only source navigation is available to real readers. “Physischen Vorrat vergleichen” passes the exact immutable source version and canonical portions even after a later source change. Its destination names a fresh physical-stock comparison; this is a different stock scope from the free quantities used for suggestions.

Displayed date reasons retain at most50owned records after sorting; they no longer keep the complete sorted backing array alive. All bounded reasons still affect coverage/ranking and the full reason count stays visible. This is a source-level retention correction, not a measured capacity claim.

## Executed checks so far

| Check | Observed result |
|---|---|
| Focused native PostgreSQL | Eight suggestion contracts pass(Web48.334s), including two new Pantry entry/claim/reader/session/cursor contracts. |
| Initial browser |25checks/seven clean axe scans/seven opened valid captures. First attempt failed solely for a missing required received-date in synthetic lot setup; retained as failed evidence. |
| Final browser confirmation |23components/803checks pass, including25Pantry checks/seven clean axe scans/seven opened valid captures,0JS errors/external requests. The physical-versus-free explanation precedes the disclosure and the action explicitly names physical stock. |
| Standard verify |16.5%(1818/11049),78Node cases,61unchanged migration checksums, vet/module/workflow policy pass. |
| Tagged affected lint |0issues including integration sources. |
| Gosec |129production files/20047lines,0findings. Quiet-mode first invocation wrote no JSON; the non-quiet report is the accepted scanner evidence. |
| GOVul |0reachable/imported-package vulnerabilities; existing unused required-module advisory remains. |
| Dependency/license and Actionlint |Local checks pass; evidence saved. Hosted workflows not executed. |
| Source review |Independent limited source review reports no material finding; it executed no tests. |
| Independent UI and design documentation |Fresh reviewer opens all7captures and returns ship/no material findings at PA15 scope; separate documenter opens all7and validates23links while preserving DESIGN.md/absent sidecar. |
| Current images |Runtime0f22860d51fc4d352d31fb5139b7248917a4f4a90032246b1544d3f1546d94a4(40868449bytes), helperfba3e0f723b388724e8302b3c2aee1d42b97de1882d2cf423b0443b42cd96668(521515626bytes); both10001:10001. |
| Current-helper encrypted recovery |Synthetic backup7.18s/restore-and-verify23.73s passes, preserving exact prior domain/source/participant/proof values and two file hashes. No authenticated restored API replay or production RTO/RPO claim. |

Complete native regression passes297main tests with0failures(Web1292.030s),73.4% combined internal coverage(7976/10870;55% gate). Scoped Linux race passes44selected Web tests(Web677.101s), plus matching Store helpers(Store1.065s), with no race report. Both campaigns run serially against55434 with the review server stopped and the331-file source/dependency manifest unchanged. This is scoped race acceptance, not a whole-tree race result. Together with803browser checks, fresh UI ship, separate design documentation, local security/workflow checks and current-image encrypted synthetic recovery, PA15 meets local feature acceptance. Checklist58verified/2inprogress(EX02/EX19)/120open. No live upgrade, publication, hosted Actions, restored authenticated API replay, production load or RTO/RPO is claimed.

## Evidence

- [Focused PostgreSQL](../outputs/expansion-review/pantry-suggestions-focused-native.log)
- [Failed synthetic fixture attempt](../outputs/expansion-review/pantry-suggestions-browser-first.log)
- [Initial browser](../outputs/expansion-review/pantry-suggestions-browser-initial.log)
- [Browser report](../outputs/pantry-suggestions-review/browser-report.json)
- [Complete browser confirmation](../outputs/expansion-review/pantry-suggestions-browser-chain-final.log)
- [Standard verify](../outputs/expansion-review/pantry-suggestions-verify.log)
- [Tagged lint](../outputs/expansion-review/pantry-suggestions-lint.log)
- [Gosec JSON](../outputs/expansion-review/pantry-suggestions-gosec.json)
- [GOVul](../outputs/expansion-review/pantry-suggestions-govuln.log)
- [Dependencies/licenses](../outputs/expansion-review/pantry-suggestions-dependencies.md)
- [Actionlint](../outputs/expansion-review/pantry-suggestions-actionlint.log)
- [Design verification](pantry-suggestions-design-verification.md)
- [Current image identities](../outputs/expansion-review/pantry-suggestions-images.log)
- [Encrypted recovery](../outputs/expansion-review/pantry-suggestions-recovery-report.json)
- [Native source manifest](../outputs/expansion-review/pantry-suggestions-native-source-manifest.json)
- [Native campaign](../outputs/expansion-review/pantry-suggestions-native.jsonl)
- [Native coverage](../outputs/expansion-review/pantry-suggestions-native-coverage.json)
- [Scoped race selection](../outputs/expansion-review/pantry-suggestions-race-scope.json)
- [Scoped Linux race](../outputs/expansion-review/pantry-suggestions-linux-race.log)
