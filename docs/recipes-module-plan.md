# Rezepte: gemeinsamer Modulblock RE05/06/09/13/14/15/17/18/19/20

Status: Phase 3 geöffnet, nachdem alle zehn Erweiterungen als gemeinsamer Quellstand integriert und unabhängig gelesen wurden. Sämtliche Ausführungsprüfungen ab Schritt 98 bleiben bis zu echten Ergebnissen offen. Ausgangspunkt ist der lokal abgenommene Shopping-Checkpoint `3c5d710776aea97dbb99020c026cab1f425879fa` mit 75 unveränderten SQL-Dateien und 87/180 verifizierten Punkten. Der separate, bereits vorhandene Finanz-Dokumententwurf bleibt außerhalb dieses Moduls. Der pausierte RE05-Entwurf unter `.local/recipe-filter-work/` basiert auf 404 Quellen/71 SQL-Dateien und ist weder integrierbar noch abgenommen; nur einzeln geprüfte Ideen werden neu umgesetzt. Das Vorhandensein neuer Go-, SQL-, JS- und Testquellen ist noch kein bestandener Modulcheck.

## 1. Zusammenfassung sämtlicher Anpassungen

| Punkt | Vollständiges Ziel | Sichtbarer Ablauf | Daten-/Integrationsgrenze |
|---|---|---|---|
| RE05 | Dauer, Tags, Katalogzutaten und letzte tatsächliche Zubereitung kombiniert filtern; persönliche Ansichten speichern | „Rezepte finden“, aktive Filterchips, 50 Ergebnisse/Seite, benannte Ansichten | Kochdatum aus abgeschlossenen Kochdatensätzen über unveränderliche Versionen; persönliche Ansichten enthalten nur Kriterien und sind ausdrücklich gerätelokal. |
| RE06 | Strukturierte Rezeptdatei und Schema.org-Recipe vor Übernahme prüfen | Importquelle → lesbare Feldvorschau → Fehler/Verluste → ausdrückliche Bestätigung | Kein fremder HTML-Code wird ausgeführt; alle Kinder werden gemeinsam mit dem Rezept gespeichert. |
| RE09 | Zutatenersatz mit Mengenverhältnis, Einheit und Quelle vorschlagen | Rezeptinformationen zeigen Ziel und Menge; Nutzer wählt eine Änderung selbst | Katalogregeln bleiben begrenzt; keine stille Änderung am Original, Vorrat oder Einkaufszettel. |
| RE13 | Portionskosten aus datierten, konkreten Shopping-Preisbeobachtungen schätzen | Preisdatum/Quelle, bekannter Teilbetrag und unbekannte Zutaten getrennt | Nur ausdrückliche Rezeptkatalog↔Shopping-Produkt-Zuordnung über Core; Modulstatus und Generationen werden erneut geprüft. |
| RE14 | Nährwerte und Allergene strukturiert mit Basis, Quelle und Vollständigkeit erfassen | Detailkarten „bekannt“, „teilweise“ und „unbekannt“ | Fehlender Eintrag bedeutet unbekannt und niemals „allergenfrei“; keine Ableitung allein aus Namen. |
| RE15 | Household-JSON v1 austauschen und ausgewählte Tandoor-/Mealie-/Paprika-Profile importieren | Profilwahl, Feldverlustbericht, kontrollierte Vorschau, Household-Download | Fremde IDs, externe Bilder, Rechte und versteckte Felder werden nicht ungeprüft übernommen. |
| RE17 | Ausgewählte Rezepte einschließlich optionaler kleiner lokaler Bilder bewusst offline lesen | Zustimmung → Speichern → neutrale Offline-Bibliothek → Entfernen | Eigenes IndexedDB, reine Lesekopie, Eigentümer/Sitzung/Rolle/Generation/Epoche/Ablauf, physische Löschung bei Logout/Opt-out. |
| RE18 | TL/EL exakt als Volumen behandeln; Masse↔Volumen nur mit belegter Zutatendichte | Umrechnung mit Ausgangs-/Zielwert, Quelle und „nicht exakt möglich“ | Keine geratene Dichte, kein stilles Runden über sechs Dezimalstellen, kein automatisches Überschreiben. |
| RE19 | URL-Import mit starker SSRF-/HTML-/Zeit-/Größengrenze | Bewusster „URL prüfen“-Start, nur inert dargestellte Vorschau | HTTPS:443, öffentlicher DNS und gepinnte Verbindung, kein Proxy/Redirect/Cookie/Subresource, begrenzte Parallelität. |
| RE20 | URL-/Inhaltsduplikate vor jeder Übernahme vergleichen | Nebeneinander: vorhandener Stand / Vorschlag; separat anlegen, geprüft aktualisieren oder abbrechen | Keine automatische Zusammenführung; Update prüft aktuelle Version, Generation und Wiederholschlüssel. |

Der bestehende integrierte Rezepteditor mit Zutaten/Schritten/Tags, Entwürfen, Konfliktvergleich und Versionen bleibt die Bearbeitungszentrale. Die zehn Erweiterungen erhalten vier schrittweise Oberflächen: **Finden**, **Import prüfen**, **Rezeptwissen**, **Offline lesen**. Mobil bleiben Kopf- und Modulnavigation, 44-px-Ziele, sichtbare Feldnamen und die erste Aktion oberhalb der festen Leiste; Desktop nutzt den vorhandenen begrenzten Inhaltsbereich. Jeder Bereich zeigt Laden, Leerstand, Berechtigungsverlust, Konflikt und konkrete Korrektur an.

## 2. Architektur- und Datenvertrag

**Keine alte Migration ändern.** Der Migrationsläufer installiert Core bis zur geprüften Version 26, dann sämtliche bisher abgenommenen Modulversionen einschließlich Shopping8, Fuel5 und Pantry12 (Rezepte zunächst bis Version 4, Fuel bis Version 5), danach Core27, Recipes5 und Fuel6. Fuel6 passt die Fahrzeugübersicht und Befehlsautorisierung an den geprüften Core27-Nachfolger an; alle bisherigen Schema-Fingerprints bleiben erhalten, nur der Hash der angepassten Autorisierungsfunktion wird ersetzt. Die älteren Migrationen verlangen Core26 als exakten Audit-Vorgänger; diese Reihenfolge gilt für frische Installationen und bestückte Upgrades. Pantry12 ersetzt dabei `core.audit_ready()` mit der exakten Zahl 95. Neue auditierte Tabellen würden auf frischen Installationen die Ready-Prüfung brechen. Für diesen Modulblock werden deshalb **keine neuen auditierten Tabellen** eingeführt: eng begrenzte und serverseitig typisierte JSONB-Werte liegen in bereits auditierten Rezept-/Katalogzeilen; persönliche Filteransichten sind explizit gerätelokal. Eine spätere Normalisierung neuer Tabellen benötigt zuerst einen gesonderten, tatsächlich geprüften Post-Modul-Audit-Migrationsvertrag. Alle neuen JSONB-Felder erhalten Größen-, Form-, Kardinalitäts- und exakte Werteprüfungen; unbekannte Auditfelder bleiben maskiert.

- `core/000027`: die Volumeneinheiten `tsp` = 5 ml und `tbsp` = 15 ml ergänzen; `core.ingredient_product_bindings` um eine **separate optionale Shopping-Produktbindung** mit Shopping-Generation und enger gleicher-Haushalt-/aktueller-Modul-Prüfung erweitern. Die vorhandene Pantry-Bindung bleibt unabhängig. `core.import_batches` erhält einen Batch-Zweck (`flat_csv` als kompatibler Vorgabewert, `recipe_review` für diese Vorschauen), Besitzer, Ablauf und Prüfdigest; `core.import_rows.normalized_values` speichert begrenzte, bereits normalisierte Vorschauen. Der generische CSV-Confirm muss `recipe_review` ablehnen, der Rezept-Confirm muss `flat_csv` ablehnen. Auditpolitik-Anzahl bleibt 95.
- `recipes/000005`: `recipes.recipes` erhält optionale kanonische `source_url`, den **unveränderten ursprünglichen Importfingerabdruck**, begrenzte Importprovenienz und typisierte Nährwert-/Allergenprofile; `recipes.versions` erhält einen SHA-256-Fingerabdruck des jeweils vollständigen aktuellen Snapshots, der nach allen Eltern-/Kind-/Tag-Schreibwegen gemeinsam mit der Version entsteht. `recipes.ingredients` kann die konkret bestätigte Ersatz-/Dichteumrechnung als kleine unveränderliche Herkunft in der nächsten Version festhalten. `recipes.ingredient_catalog` erhält begrenzte Ersatzregeln und optionale positive g/ml-Dichte mit Quelle/Datum. Geeignete Haushalt-/URL-/Hash- und Filterindizes ergänzen die bestehenden Tabellen. Weder URLs noch Hashes werden einzigartig gemacht, damit eine ausdrücklich getrennte Kopie möglich bleibt. Die vorhandene komplette Versionsaufnahme mittels `to_jsonb` enthält neue Rezeptfelder automatisch; 8-MiB-Snapshotgrenze und historische Lesbarkeit bleiben Prüfpflicht.
- Preisbindung ist ein Core-Vertrag zwischen optionalen Modulen, **ohne FK von Rezepten nach Shopping**. Die Read-Seite liefert nur aktuelle, ausdrücklich zugeordnete Shopping-Preisbeobachtungen und verweigert alte Generationen, fehlende Produkte und deaktivierte Module. `shopping.products` hat derzeit kein Archivfeld; ein solches wird nicht vorausgesetzt. **Recipes-Purge löscht die ganze Quellbindung; Pantry-Purge leert nur Pantry-Ziel/Generation und erhält Shopping; Shopping-Purge leert nur Shopping-Ziel/Generation und erhält Pantry.** Bereits bestehende Purge-SQL darf die nun unabhängigen Ziele nicht gemeinsam löschen. Produktlöschung und Generationwechsel machen betroffene Zuordnungen unbrauchbar und werden ausdrücklich bereinigt. Abgelaufene Rezeptvorschauen werden separat entfernt.
- Geld wird als Cent-Ganzzahl, Menge/Dichte als exakte Dezimalzeichenkette mit höchstens sechs Stellen verarbeitet. Kosten zeigen bekannte Summe und unbekannte Positionen getrennt. Eine Stückpreis-/Packumrechnung benötigt passende Dimension und bekannte Packmenge; alte Preise erhalten sichtbar Alter/Datum. Keine stillen Netto-/Brutto-, Währungs- oder Massendichteannahmen.
- Nährwerte sind ein begrenztes Profil je Rezept mit Basis `per_recipe` oder `per_serving`, expliziten Einheiten, Herkunft, Datum, damaliger Portionszahl und Fingerabdruck der beurteilten Zutaten. Nach einer Änderung an Zutaten oder Portionen bleibt der alte Beleg sichtbar, wird aber **stale/erneut prüfen** und darf nicht als aktuelle Portionsangabe ausgegeben werden. Pro Nährstoff ist fehlend/teilweise/vollständig unterscheidbar. Allergenangaben sind pro Stoff `contains`, `may_contain` oder ausdrücklich durch eine benannte Quelle/datiert `reviewed_absent`; ohne Eintrag oder nach relevantem Rezeptwechsel ist der aktuelle Zustand `unknown`. Auch `reviewed_absent` erscheint nie als pauschales grünes „allergenfrei“. Profile ändern die immutable Rezeptgeschichte nur bei bewusstem Speichern.
- Die Importvorschau ist ein generationgebundener, benutzergebundener, ablaufender `recipe_review`-Core-Batch mit höchstens 20 Rezepten, 2 MiB Eingabe und 4 MiB entpackten Daten. Zusätzlich gelten höchstens 64 ZIP-Einträge über alle Ebenen, 20:1 Entpackverhältnis je Eintrag, 32 JSON-Ebenen und 20 JSON-LD-Scripts pro HTML-Datei; Symlinks und Traversal sind gesperrt. Für Tandoor ist höchstens **eine** innere Rezept-ZIP-Ebene nach Versionsprüfung erlaubt, sonst keine Verschachtelung. **Jede ausgewählte Zeile** wird nach genauem Vorschau- und Duplikatvergleich als ein vollständiger Rezeptbaum atomar bestätigt; eine Mehrrezeptdatei kann teils bestätigt/teils offen sein, und die Oberfläche zeigt diesen Fortschritt wahrheitsgemäß. Ein Batch über 20 Rezepte ist nicht als eine gemeinsame Transaktion behauptet. Wiederholung je Zeile verwendet dauerhafte Idempotenzbelege. Roh-HTML und Remote-Bilder werden nicht persistiert. Verlustberichte nennen nicht unterstützte Felder.
- Unterstützte dokumentierte Teilprofile: Household-JSON v1 für Import **und** Export; Schema.org Recipe als JSON-LD-Datei oder HTML-Script; Mealie-Einzelrezept-JSON; Tandoor-Standard-ZIP mit begrenzten JSON-Rezepten; Paprika-Einzel-GZIP und begrenztes `.paprikarecipes`-ZIP. Gemeinsamer Kern: Titel, Beschreibung, Portionen, Zeiten, geordnete Zutaten/Schritte, Tags, Quell-URL. Der Household-Export ist eine **explizite Feld-Positivliste** aus dem gebündelten Rezept, nie die rohe `content_snapshot`-Antwort: Haushalt-/Nutzer-/Kind-IDs, Version-/Auditdaten, Dateischlüssel und vertrauliche Provenienz bleiben draußen; beim Import entstehen neue IDs und alle Felder durchlaufen die normale Workspace-Validierung. Zusätzliche Quellfelder werden angezeigt oder als Verlust gemeldet, nie frei als aktives HTML gerendert. Die genaue Version und akzeptierte Beispielstruktur werden vor Parsercode festgeschrieben und durch Format-Fixtures belegt.
- Bereits gültige ältere Datenbankrezepte können größere Mengen, Zeiten oder Texte enthalten als das enge portable Dateiprofil. Ein Editieren dieses Bestands bleibt möglich: Der Inhaltsfingerabdruck wird aus der Positivliste berechnet, auch wenn ein strenger Household-Export wegen seiner 2-MiB-/Werte-Grenzen mit einer verständlichen Korrekturmeldung abgewiesen wird. Neue Importe und Exporte bleiben strikt validiert; ein Legacy-Digest ist keine Freigabe für unsichere Importdaten.
- URL-Abruf ist optional pro Nutzeraktion. Nur HTTPS-Port 443 und gültiger öffentlicher DNS-Name; alle A/AAAA-Antworten müssen öffentlich sein, höchstens 16, die ausgewählte IP wird bis zur TLS-Verbindung gepinnt und der ursprüngliche Host bleibt TLS/SNI/Host. Proxy, Redirect, Cookies, HTTP-Alternativprotokolle und automatische Unterressourcen sind gesperrt. Eigene Frist, 2 parallele Abrufe, Content-Type-Positivliste und entpackte Antwortgrenze; Fehler geben keine internen Ziele oder Rohdaten preis. Die rohe Abruf-URL samt Query ist nur flüchtig im Abruf; gespeicherte/exportierte/angezeigte `source_url` entfernt Fragment, Userinfo und **alle Query-Parameter**. Roh-URLs werden weder auditiert noch geloggt. Sichtbare externe Links werden erst nach HTTPS-Prüfung und Nutzeraktion mit `rel="noopener noreferrer"` geöffnet; die Ansicht fordert keine fremden Bilder/Skripte nach. Importvorschau ist ausschließlich Datenquelle, keine Anweisung.
- Offline-Rezepte nutzen eine eigene IndexedDB mit höchstens 20 Rezepten, 5 MiB Gesamtspeicher, höchstens 2 lokal freigegebenen JPEG/PNG-Bildern je Rezept, je höchstens 200 KiB und 1024×1024 Pixeln, sowie 30 Tagen festem Ablauf. Die Zustimmung nennt ausdrücklich Notizen und Bilder. Eine Kopie enthält Titel, Beschreibung, Quelle ohne Query, Portionen/Zeiten, geordnete Zutaten/Schritte/Tags, Notizen und die beim Speichern beurteilten Nährwert-/Allergenangaben samt Herkunft. Sie zeigt Aufnahmezeit, Rezeptversion, Ablauf und einen deutlichen „seit Speicherung ungeprüft“-Hinweis. Dynamische Preise, Vorratsstand, aktuelle Ersatzregeln und Dichte werden **nicht** als aktuell offline dargestellt. Bilder kommen nur aus bereits berechtigten lokalen Attachments mit Alt-/Beschriftung und werden typ-/größen-/anzahlgeprüft kopiert; private HTTP-Antworten bleiben außerhalb CacheStorage. Ablauf, Opt-out, Logout und Kontowechsel entfernen auch Bild-Blobs physisch in derselben Geräteablage. Die öffentliche `/offline/recipes`-Shell enthält keine Haushaltsdaten. Gerätebereinigung wird mit den vorhandenen Logout-/Opt-out-/Kontowechsel-Hooks verbunden; unbekannter Remote-Widerruf wird beim nächsten Online-Abgleich erkannt, nicht fälschlich sofort versprochen.

### API und Bildschirme

| Oberfläche | Vorgesehene private Endpunkte | Zustand und Entscheidung |
|---|---|---|
| Finden `/m/recipes/find` | `GET /api/v1/recipes/discover`, `GET/PUT/DELETE /api/v1/recipes/device-views` nur falls serverseitige Synchronisation später ausdrücklich gewählt wird | In diesem Modul speichert das Gerät nur Filterkriterien; keine Server-View-Endpunkte. Signierter, scopegebundener Cursor für 50er-Seiten. |
| Import `/m/recipes/import-review` | `POST /api/v1/recipe-imports/previews`, `GET /api/v1/recipe-imports/previews/{id}`, `POST /api/v1/recipe-imports/previews/{id}/rows/{row}/confirm`, `GET /api/v1/recipes/{id}/portable` | Datei/URL/Profil, Vorschau, Verlustbericht, zeilenweiser Fortschritt, Dubletten, klare Commit-Entscheidung und Wiederholbeleg. |
| Wissen `/m/recipes/recipes/{id}/insights` | `GET /api/v1/recipes/{id}/insights`, getrennte versiongeprüfte `PUT`-Routen für Profil/Katalogregeln/Dichte/Preisbindung | Reader sieht Erklärungen; Editor/Admin dürfen nach Feldprüfung ändern. |
| Offline `/offline/recipes` | `GET /api/v1/recipes/{id}/read-snapshot` | Öffentliche neutrale Shell, private vollständige Quelle nur online; explizites Speichern und lokal nur lesen. |

Filterdetails: Jeder einzelne Wert `prep_minutes=0` oder `cook_minutes=0` ist im bestehenden Schema nicht von „nicht eingetragen“ unterscheidbar. Die kombinierte Dauer gilt deshalb nur dann als bekannt, wenn **beide Werte positiv** sind; sonst zeigt die Suche „Dauer nicht eindeutig erfasst“, und ein begrenzter Dauerfilter schließt sie nur bei ausdrücklich gewähltem „unbekannt einschließen“ ein. Ein „zuletzt gekocht“-Filter berücksichtigt nur abgeschlossene Kochdatensätze bis heute; zukünftige Daten werden sichtbar als unplausibel markiert und nicht still als aktuell bewertet. Gerätelokale Ansichten enthalten höchstens fünf Tags/fünf Katalogzutaten und nur Kriterien; fehlen/archivieren spätere Referenzen, wird die Ansicht sichtbar gesperrt und muss neu ausgewählt werden, statt die Suche zu verbreitern. Sie verschwindet mit Logout/Kontowechsel und wird nicht geräteübergreifend synchronisiert. Die Datenbank filtert direkt mit indexgestützten `EXISTS`-/Version-Kochbelegen, Schlüsselset-Paginierung 50+1, einer engen Abfragefrist und ohne vorzeitiges 1000-/10000-Zeilen-Trunkieren; bei Fristüberschreitung erscheint ein echter Eingrenzungsfehler.

Ersatz-/Dichte-Details: JSONB-Regelziele werden beim Schreiben und beim späteren Anzeigen/Bestätigen unter gleicher-Haushalt-, aktueller-Generation- und archiviert/gelöscht-Prüfung erneut aufgelöst; Quelle und Ziel erhalten geordnete Locks. Umbenannte Ziele zeigen den aktuellen Namen, veraltete Versionen/gelöschte oder archivierte Ziele verlangen neue Auswahl. Eine tatsächlich übernommene Menge bewahrt Regel-/Dichteversion, Quelle, Zeitpunkt und ursprünglichen Wert als begrenzte Herkunft am neuen Rezeptzutatensatz und damit in der unveränderlichen Rezeptversion. Reine Anzeigen verwenden ausdrücklich die **heute aktuellen** Regeln/Dichten.

### Gepinnte Teilprofile und prüfbare Beispiele

| Profil | Kleine akzeptierte Struktur | Bewusst nicht übernommene Inhalte |
|---|---|---|
| Household JSON v1 | `{"format":"household-recipe","version":1,"recipes":[{"title":"Suppe","servings":"2","ingredients":[{"name":"Wasser","quantity":"1","unit":"l"}],"steps":[{"instruction":"Kochen"}]}]}` | Alle unbekannten Felder werden abgewiesen; IDs, Rechte, Dateischlüssel und Auditereignisse sind kein Teil des Formats. |
| Schema.org Recipe JSON-LD/HTML | `{"@type":"Recipe","name":"Suppe","recipeIngredient":["1 l Wasser"],"recipeInstructions":[{"text":"Kochen"}]}` als JSON oder Text eines `application/ld+json`-Scripts | Freitextmengen bleiben mit Prüfhinweis; fremde Scripts, HTML-Darstellung, Bilder und Unterressourcen werden nicht geladen. |
| Mealie Einzel-JSON | `{"name":"Suppe","recipeServings":2,"recipeIngredient":[{"display":"Wasser","quantity":1,"unit":{"name":"l"}}],"recipeInstructions":[{"text":"Kochen"}]}` | IDs, Bilder, Werkzeuge, fremde Organisationsfelder. |
| Tandoor Standard-ZIP | `recipe.json` mit `{"name":"Suppe","servings":2,"ingredients":[{"name":"Wasser","amount":1,"unit":"l"}],"steps":[{"instruction":"Kochen"}]}`; eine innere ZIP-Ebene zulässig | Medien, fremde IDs und alle anderen Organisationsfelder. |
| Paprika GZIP/ZIP | GZIP mit `{"name":"Suppe","servings":"2","ingredients":"1 l Wasser","directions":"Kochen"}`; ZIP enthält höchstens 20 solche GZIP-Dateien | Bilder, interne IDs, unbekannte Profilfelder; Freitextmengen bleiben ausdrücklich zu prüfen. |

Die Minimalbeispiele sind Vertrags-Fixtures für die unterstützte Teilmenge, keine Behauptung über jede Exportversion der Fremdprodukte. `internal/recipeimport/recipeimport_test.go` legt sie und Ablehnungsfälle als Testquelle ab; `fetch_test.go` prüft den kontrollierten TLS-/DNS-Transport. Beide Dateien wurden bei diesem Phase-2-Eintrag noch nicht ausgeführt.

## 3. Chronologische, atomare Modul-Checkliste

### Phase 1 – Gesamtmodul zuerst zusammenfassen und festlegen

1. [x] Den geprüften Shopping-Checkpoint und die 87/93-Ausgangsliste als Modulgrenze festhalten.
2. [x] Alle zehn offenen RE-Kennungen und ihre Abnahmekriterien inventarisieren.
3. [x] Bestehenden Rezepteditor, Versionen, Kochhistorie, Katalog und Pantry-Vertrag lesen.
4. [x] Veraltete RE05-Isolierung als ungetestete Referenz markieren und Quellkopie ausschließen.
5. [x] Die vier neuen Oberflächen und mobilen/desktop Bedienabläufe festlegen.
6. [x] Gemeinsamen Import-/Duplikat-/Sicherheitsvertrag für RE06/15/19/20 festlegen.
7. [x] Exakte Preis-, Nährwert-, Ersatz- und Umrechnungsgrenzen für RE09/13/14/18 festlegen.
8. [x] Besitzer-/Sitzungs-/Generation-/Ablaufvertrag für RE05-Geräteansichten und RE17-Lesekopien festlegen.
9. [x] Core27/Recipes5 ohne neue auditierte Tabellen und ohne Änderung alter SQL-Dateien planen.
10. [x] Vollständige Reihenfolge und gemeinsame Testphase vor erster Verhaltensänderung veröffentlichen.

### Phase 2 – sämtliche zehn Punkte implementieren, noch keine Tests ausführen

11. [x] Exakte aktuelle Core-/Recipes-/Shopping-Schema- und Ready-Vorversionen festhalten.
12. [x] Importprofil-Beispielstrukturen und erlaubte Feldmengen mit offiziellen Quellen/Fixtures pinnen.
13. [x] Core27-Vorwärtsmigration für TL/EL und unabhängige Shopping-Preisbindung schreiben.
14. [x] Core27-Vorwärtsmigration für zweckgetrennte persönliche Rezept-Importbatches ergänzen.
15. [x] Core-Bindungsgrenzen, Generationen, Rechte und Purge-Semantik eng absichern.
16. [x] Recipes5-Vorwärtsmigration für Quelle/Fingerprint/Provenienz schreiben.
17. [x] Recipes5-Vorwärtsmigration für typisierte Nährwert-/Allergenwerte schreiben.
18. [x] Recipes5-Vorwärtsmigration für Ersatzregeln/Dichte und begrenzte Indizes schreiben.
19. [x] Datenbank-Checks und eng benötigte Trigger/Funktionen auf bestehende Audit-Readiness abstimmen.
20. [x] Modul-/Core-Readiness und Owner-Purge an neue Spalten/Batches binden.
21. [x] Serverseitige Filterform und maximale Kriterienzahl typisieren.
22. [x] Dauerfilter mit bestehender Null-Minuten-als-unbekannt-Grenze definieren.
23. [x] Tag- und Katalogzutatfilter mit Haushalt-/Archivgrenzen kombinieren.
24. [x] „Zuletzt gekocht“ nur aus abgeschlossenen, nicht zukünftigen Kochdatensätzen über Versionen ermitteln.
25. [x] Stabile 50er-Paginierung mit signiertem, benutzer-/filter-/generationgebundenem Cursor, Indexstrategie und Abfragefrist implementieren.
26. [x] Suchergebnisse auf aktuelle berechtigte Rezepte begrenzen und Quoten prüfen.
27. [x] Persönliche reine Kriterienansichten in eigener Geräteablage mit höchstens 20 Ansichten speichern.
28. [x] Geräteansichten an Besitzer/Sitzung/Generation/Epoche binden, fehlende Referenzen sperren und bei Logout physisch löschen.
29. [x] Import-Dateityp, 2-MiB-Eingabe, 4-MiB-Ausgabe, 64-ZIP-Einträge, maximal eine Tandoor-ZIP-Innenebene, 20:1-Verhältnis, 32-JSON-Ebenen und 20-Scripts vor Verarbeitung prüfen.
30. [x] Household-JSON-v1-Format mit Version, exakten Zahlen und Feldverlustregeln definieren.
31. [x] Household-JSON aus einer ausdrücklichen privaten-Metadaten-freien Positivliste des aktuellen Rezepts exportieren.
32. [x] Household-JSON in denselben geprüften Importentwurf zurücklesen.
33. [x] Schema.org-Recipe-JSON-LD aus Datei ohne fremde Ausführung extrahieren.
34. [x] Schema.org-Recipe aus begrenztem HTML nur aus passenden Script-Daten extrahieren.
35. [x] Mealie-Einzel-JSON-Profil mit erlaubtem Feldsubset implementieren.
36. [x] Tandoor-Standard-ZIP-Profil mit strikten Pfad-/Datei-/Größenlimits implementieren.
37. [x] Paprika-Einzel-GZIP und begrenztes ZIP/GZIP-Profil implementieren.
38. [x] Alle Profile in ein gemeinsames typisiertes Vorschauformat abbilden.
39. [x] Unsichere HTML-, externe Bild- und unbekannte Quellfelder als Verlust/Ungewissheit zeigen.
40. [x] Titel/Portion/Zeiten/Einheiten/Zeilen/Tags vor DB-Vorschau normalisieren.
41. [x] Importbatch an Zweck, Mitglied, Generation, Ablauf, Digest und normalisierte Zeilen binden.
42. [x] Vorschau nur dem aktuellen Besitzer mit aktueller Rolle und Generation zugänglich machen.
43. [x] URL-Parser auf HTTPS:443, öffentliche Namen und leere Zugangsdaten begrenzen.
44. [x] Alle DNS-Antworten öffentlich prüfen und gewählte Ziel-IP beim Dial festhalten.
45. [x] TLS-Hostprüfung/SNI am ursprünglichen Namen bei gepinnter IP erhalten.
46. [x] Proxy, Redirects, Cookies, Keepalive und alternative Protokolle abschalten.
47. [x] Frist, Parallelität, MIME und entpackte Antwortbytes vor JSON-LD-Verarbeitung begrenzen.
48. [x] Abruffehler redigieren und keine internen Adressen oder HTML in Logs ausgeben.
49. [x] Query-freie URL, unveränderten Importquellenhash und aktuellen vollständigen Versionsfingerabdruck getrennt bilden.
50. [x] Exakte und ähnliche vorhandene Kandidaten begrenzt und haushaltsgebunden finden.
51. [x] Bestands- und Vorschauwerte feldweise nebeneinander für Übernahme liefern.
52. [x] Separat anlegen, geprüft aktualisieren und abbrechen als getrennte Entscheidungen modellieren.
53. [x] Update mit aktueller Rezeptversion/Fingerprint und unverändertem Importdigest sperren.
54. [x] Jede bestätigte Rezeptzeile als ganzen Rezeptbaum atomar über bestehenden Workspace-/Versionsvertrag übernehmen und Teilfortschritt zeigen.
55. [x] Wiederholung pro Rezeptzeile mit dauerhaftem Operation-Key idempotent beantworten.
56. [x] Abgelaufene/verbrauchte Importbatches begrenzt bereinigen.
57. [x] Ersatzregeln mit aktuellem eigenem Katalogziel, Verhältnis, Quelle, Version und Grenzen typisieren.
58. [x] Ersatzvorschlag mit erneut geprüfter Zielautorität für aktuelle Rezeptmenge exakt berechnen.
59. [x] Ersatz nur nach ausdrücklicher Auswahl samt begrenzter Herkunft in einem Entwurf anwenden.
60. [x] Rezeptkatalog↔Shopping-Produktbindung versioniert und generationgeprüft setzen/lösen.
61. [x] Aktuelle Preisbeobachtungen mit Store/Datum/Packmenge begrenzt laden.
62. [x] Kompatible Einheiten exakt auf Zutatenmengen und Portionen umrechnen.
63. [x] Fehlende, inkompatible, alte und nur teilweise bepreiste Positionen getrennt anzeigen.
64. [x] Strukturierte Nährstoffcodes, Einheit, Basis, Quell-/Zutatenfingerabdruck und Vollständigkeit validieren.
65. [x] Allergenstatus pro Stoff mit benannter/datiert negativer Quelle und „ungeprüft“ getrennt speichern/lesen.
66. [x] Nur bewusst gespeicherte Rezeptprofile in Version und Audit aufnehmen.
67. [x] TL/EL in Rezeptauswahl und allen Mengenlesern als Volumen zulassen.
68. [x] Exakte Volumenfaktoren und sechsstellige Darstellung ohne stilles Runden implementieren.
69. [x] Katalogdichte mit positiver g/ml-Menge, Quelle, Datum und aktueller Revision versioniert bearbeiten.
70. [x] Masse↔Volumen nur bei aktueller ausdrücklich gewählter Katalogzutat/Dichte rechnen.
71. [x] Die private Offline-Rezeptprojektion samt Version, Notizen, Zutaten/Schritten und belegten Profilen begrenzen; dynamische Kosten ausschließen.
72. [x] Höchstens zwei berechtigte JPEG/PNG-Bilder je200KiB/1024px mit Alt-/Beschriftung bereitstellen.
73. [x] Öffentlich neutrale `/offline/recipes`-Shell ohne private Daten hinzufügen.
74. [x] Eigene Rezept-IndexedDB mit 20-Rezept-/5-MiB-/30-Tage-Budgets implementieren.
75. [x] Besitzer-/Sitzungs-/Rollen-/Generations-/Epochen-/Revision-Fences atomar implementieren.
76. [x] Späte Antworten nach Logout, Löschung oder Opt-out zuverlässig verwerfen.
77. [x] Neue Sitzung/Generation nur nach ausdrücklichem vollständigem Neuabruf zulassen.
78. [x] Offline-Reader ohne Mutation/Autoupload und mit klarer Ungewissheitsanzeige bauen.
79. [x] Rezeptgeräteansichten und Lesekopien in globale Logout-/Opt-out-/Kontowechsel-Cleanup-Hooks aufnehmen.
80. [x] Neutrale Shell in die enge Service-Worker-Positivliste und Offline-Navigation aufnehmen.
81. [x] Finden-Seite mit Filterchips, Trefferzustand und Tastaturbedienung bauen.
82. [x] Persönliche Ansichten mit sichtbarer „nur dieses Gerät“-Kennzeichnung bauen.
83. [x] Import-Assistent mit Quellenwahl, Parserstatus und Verlustbericht bauen.
84. [x] Dublettenvergleich und explizite Commit-/Konfliktentscheidung mobil und Desktop bauen.
85. [x] Rezeptwissen für Ersatz/Kosten/Nährwerte/Allergene/Umrechnung getrennt vom langen Editor bauen.
86. [x] Editor-Links zu Wissen, Finden, Import und Offline lesbar ergänzen.
87. [x] Reader-Ansichten ohne versteckte Schreibwege oder irreführende Aktionen gestalten.
88. [x] Lade-, Leer-, Fehler-, Konflikt-, Quota- und Ablaufzustände in allen vier Oberflächen gestalten.
89. [x] CSS-Token, 44-px-Ziele, Fokus, Hell/Dunkel, Langtext und feste Mobilnavigation prüfen und ergänzen.
90. [x] API-, Profil-, Datenbank-, Backup-/Purge- und Sicherheitsverträge dokumentieren.
91. [x] README bei neuen Routen/Moduloberflächen/Importkonfiguration aktualisieren.
92. [x] Pure Tests für Parser, Profile, exakte Mengen, Filtercursor und Gerätedaten vorbereiten.
93. [x] Native DB-/API-/Rollen-/Import-/Konflikt-/Purge-Testquellen vorbereiten.
94. [x] Browserabläufe für Suche, Import, Dubletten, Wissen und Offline vorbereiten.
95. [x] CI/Node-Registrierung und kontrollierte URL-Transporttests vorbereiten.
96. [x] Sämtliche Backend-/Frontend-/Migration-/Privacy-Quellen unabhängig prüfen.
97. [x] Vollständigen integrierten Modulstand festhalten und erst dann Phase 3 öffnen.

### Phase 3 – erst nach vollständiger Phase 2 gemeinsam testen und Fehler beheben

98. [x] Vollständige Go-Unit- und registrierte Node-Suiten ausführen (aktueller Stand: `outputs/recipes-module-validation/go-unit-c260-current.txt`; Node: 262/262, `npm-mandatory-password-elevated.txt`).
99. [x] Formatierung, Vet, Linter, Gosec, Govuln, Modul-/Lizenz-/Secret-Prüfungen ausführen (aktueller Vet/Linter/Gosec grün; keine erreichbare gemeldete Schwachstelle; 93 geänderte Quellen/1.456.840 Bytes mit Gitleaks ohne Befund; finale Indexprüfung bleibt Schritt 116).
100. [x] Alle alten 75 SQL-Bytes und frische/wiederholte Vorwärtsmigration nachweisen (81/81 Manifest, sechs native Rezeptverträge mit frischem und bestücktem Upgrade bestanden).
101. [x] Bestücktes Upgrade und Ready-/95-Audit-/Purge-Verträge prüfen (sechs Rezeptverträge; 21 korrigierte Web-Haupttests/35 Haupt- und Unterfälle; unabhängiger Pantry-/Shopping-Purge und Recipes-Quelllöschung tatsächlich bestanden).
102. [x] Kombinierte Filter, Cursor, Reader und gespeicherte Geräteansichten prüfen (native Recipe-Verträge auf unverändertem Go/SQL; aktueller Geräte-/Filter-Browserlauf und vollständige 29-Komponenten-Kette bestanden).
103. [x] Alle Profile mit echten gültigen/ungültigen Dateien und Verlustberichten prüfen (vollständige aktuelle Go-Unit-Suite einschließlich `internal/recipeimport`; strukturierte Formate, Archiv-/Tiefen-/Größen-/Feldgrenzen und Digest-Roundtrip).
104. [x] URL-Transport gegen kontrollierte öffentliche/privat-DNS-/Redirect-/Proxy-/Größen-/Fristfälle prüfen (`fetch-final.txt` und vollständiger Go-Unit-Lauf).
105. [x] Vorschau-Zweck/Besitzer, Ablauf, Version, Teilfortschritt, Duplikatentscheidung und Idempotenz prüfen (native Import-/Receipt-Verträge auf unverändertem Go/SQL; aktuelle Browserprüfung der vollständigen Vergleichsbäume und ausdrücklichen Entscheidung bestanden).
106. [x] Ersatz, Teilkosten, fehlende Preise, Allergie-Unbekannt und Dichte-Umrechnung prüfen (native Insights-/Guards-Verträge, aktuelle exakte Go-/Node-Rechnung und Recipe-Wissen-/Offline-Browserprüfungen bestanden).
107. [x] Versions-/Audit-/Legacy-/Pantry-/Shopping-Modulgrenzen regressiv prüfen (vollständige 616-Haupttest-Komposition ohne Fehler/Skips: 205 frühere +311 vollständig bestandene Folgetests +100 frisch ausgeführte Abschlussfälle; 2.257 Haupt- und Unterfälle, 72,43 % interne Statement-Abdeckung).
108. [x] Eigenen Offline-Neustart, Bildquoten, Reader, Kontowechsel, Logout und rohe IndexedDB prüfen (aktueller Stand c260: eigenständiger Lauf `758d3adefe5d4db2ae3b4a7398898e72`, 72 Prüfungen einschließlich echter PNG-Kopie, Quota, Ablauf, Sitzungswechsel/Neuabruf, Reader-Kontowechsel, Opt-out und POST-Abmeldung).
109. [x] Mobile/Keyboard/Fokus/Hell-Dunkel/390×844/Axe auf allen vier Oberflächen prüfen (acht aktuelle Screenshots und acht Axe-Prüfungen ohne Befund; alle acht Ansichten auf Quelle14ef manuell nachgeprüft, danach ausschließlich gemeinsamer Passwortwechsel-Start geändert, keine Layoutquelle).
110. [x] Vollständige bestehende Browserkette auf aktuellem Image ausführen (aktueller Stand c260, Lauf `f6c64ca4392e4920876b995f78288ed7`: 29 Komponenten, 976 Prüfungen, 143 Axe-Prüfungen ohne Befund, 155 verifizierte Screenshots, Quellen unverändert und eigene Ressourcen bereinigt).
111. [x] Fokussierte Linux-Race-Prüfung mit Backend-/Gerätekonkurrenz ausführen (Stand b33, Lauf `5f50f24668b140dab060e68e5292b3b5`: 42 ausgewählte Haupttests/87 Haupt- und Unterfälle, keine Fehler/Skips, Linux/amd64/CGO1/Go1.27.3, Quellen unverändert, eigene Ressourcen bereinigt; unabhängige Dateiprüfung bestanden).
112. [x] Bestückte verschlüsselte Sicherung/Restore/Purge/Generation ausführen (aktueller Stand c260: vollständiger frischer Lauf `7933f6d5d042470ea2ab374a1f368f20`; Original-PNG und erzeugte Vorschau mit echten Dateihashes, exakte Zeilen-/Metadatenprojektion, Schlüssel, App-Verweigerung, Owner-Purge, Generation 1→2 und Altzugriff 409; eigene Ressourcen entfernt).
113. [x] Gefundene Fehler modulweit beheben und betroffene Kampagnen wiederholen (Import-Routing, Core/Fuel-Kompatibilität, echte Legacy-Fixtures, Offline-Consent/Ablauf, Passwortwechsel-Start und Testharness-Übergaben korrigiert; alle betroffenen Prüfungen und die vollständige native Komposition bestanden).
114. [x] Unabhängige DB-/Backend-/UI-/Sicherheits-/Dokumentationsabnahme abgleichen (Quellen, 29 Browserberichte/155 PNG-Hashes, 42 Race-Identitäten, 16 frische Recovery-Stufen, exakte 616-Identitäts-/Coverage-Komposition und Nachweisgrenzen unabhängig nachgeprüft; erster exakter Index-Geheimnisscan ohne Befund).
115. [x] Erst nach vollständiger lokaler Abnahme alle zehn RE-Punkte gemeinsam abhaken (RE05/06/09/13/14/15/17/18/19/20 gemeinsam verifiziert; Rezepte 20/20, Gesamtstand 97/180).
116. [ ] Exakten gestagten Index auf Geheimnisse prüfen und lokalen Checkpoint ohne Push sichern.

## Quellen und Nachweisgrenzen

Die Profil- und Transportentscheidungen stützen sich auf [Schema.org Recipe](https://schema.org/Recipe), die [Tandoor-Import-/Export-Dokumentation](https://docs.tandoor.dev/features/import_export/), [Mealie-Funktionsdokumentation](https://mealie.io/documentation/getting-started/features/), [Paprika-Exportformat](https://paprikaapp.zendesk.com/hc/en-us/articles/360051324613-What-export-formats-do-you-support), die [OWASP-SSRF-Anleitung](https://cheatsheetseries.owasp.org/cheatsheets/Server_Side_Request_Forgery_Prevention_Cheat_Sheet.html) und [Go `net/http`](https://pkg.go.dev/net/http). Diese Quellen definieren keine automatische Interoperabilität aller Anbieter-Versionen; nur die dokumentierten und tatsächlich getesteten Teilprofile werden als unterstützt ausgewiesen. Ein kontrollierter Testserver beweist keine beliebige externe Website. Gerätelokale Daten sind kein verschlüsselter Tresor und erkennen unbekannten Remote-Widerruf erst beim nächsten Online-Abgleich. Gehostete CI, Port 8100 und ein produktiver Backup-Zeitplan werden nicht ohne Ausführung als bestanden markiert.
