# Vorrat – verbindlicher Abschlussblock PA10, PA13 und PA19

Stand:29.10.2026. Der Vorratsblock ist vollständig lokal abgenommen. Phase1 fasste den gesamten Umfang vor der Umsetzung zusammen; Phase2 schloss alle Implementierungen vor den neuen Tests. Phase3 einschließlich Fehlerbehebung und gemeinsamer Regression ist abgeschlossen. Vorrat hat20/20verifiziertePunkte; insgesamt85von180. Fahrzeuge/Tanken bleibt mit07889d1 lokal angenommen.

## Verbindlicher Arbeitsmodus

1. Gesamten aktuellen Modulblock analysieren und zusammenfassen.
2. Alle drei Erweiterungen einschließlich ihrer gemeinsamen Grundlagen vollständig implementieren.
3. Erst danach Unit-, PostgreSQL-, Browser-, Race-, Sicherheits- und Recovery-Prüfungen ausführen und Fehler für das ganze Modul beheben.
4. Erst nach fehlerfreier lokaler Modulabnahme einen anderen Modulblock beginnen.

Parallelaufgaben betreffen ausschließlich diesen Vorratsblock. Während der Implementierung werden keine neuen Laufzeitprüfungen gestartet. Live8100, Geschwisteranwendungen und zurückgestellte Finanz-/Rezeptvorbereitungen bleiben unberührt. Kein Push und keine Veröffentlichung.

## PA10 – gekochte Reste

- Reste werden normale Pantry-Chargen mit unveränderlichen Bestandsbewegungen; alle bisherigen Verbrauchs-, Verlust-, Inventur- und Umlagerungsabläufe bleiben maßgeblich.
- Einheit `portion` erhält die eigene Dimension `serving`; weder Stück noch Gewicht/Volumen werden stillschweigend gleichgesetzt. Mengen bleiben exakte Dezimalstrings mit vorhandenen Grenzen.
- Der Benutzer benennt das Gericht, die tatsächlich übrigen Portionen, Zubereitungstag und aktive Lagerorte. Eine atomare Prüfung umfasst höchstens20Behälter/Chargen.
- Zubereitung, Einlagerungsdatum und tatsächlicher Buchungszeitpunkt bleiben getrennt. Haltbarkeitsdaten sind ausdrücklich unbekannt oder eine manuelle Angabe; es gibt keine berechnete Lebensmittelsicherheitszusage.
- Optionaler Bezug zu einer aktuellen Rezeptversion oder abgeschlossenen Zubereitung geht über einen Core-Vertrag. Keine optionalen schemaübergreifenden Fremdschlüssel und kein direkter Pantry-Zugriff auf Recipes außerhalb dieses Vertrages.
- Eine abgeschlossene Zubereitung kann genau eine angenommene Resteeinlagerung mit mehreren Behältern besitzen. Eine neue Transportkennung darf diese fachliche Quelle nicht erneut buchen. Ist die tatsächlich gekochte Portionsmenge bekannt, darf die gemeinsame Restmenge diese nicht überschreiten; unbekannte Gesamtmengen werden ausdrücklich gekennzeichnet.
- Manuelle Einlagerung bleibt ohne Recipes-Modul möglich. Ein Zutatenverbrauch wird durch Reste niemals erneut ausgelöst. Eigene bestätigte Chargenbezeichnungen bleiben Pantry-Daten; Live-Rezeptdetails benötigen aktuelle Berechtigung.
- Vorschau, autorisierte Bestätigung, atomarer Zugang, dauerhafter Vorgangsbeleg und Abfrage eines ungewissen Ausgangs gehören zusammen. Keine automatische neue Kennung oder stilles Neuzuordnen nach Antwortverlust.

Mobiler Ablauf: **Reste → tatsächliche Restmenge und Behälter → Gesamtprüfung → Zugangsbeleg → bestehende Chargenaktionen**. Vom abgeschlossenen Kochverlauf führt ein ausdrücklicher Einstieg zur gleichen Prüfung.

## PA13 – optionale Open-Food-Facts-Anbindung

- Pantry-eigene Barcodes/Packungen und die bestehende lokale Lookup-API haben Vorrang; Shopping ist keine Voraussetzung.
- Die Integration ist standardmäßig aus. Erst ein ausdrücklicher Klick für einen geeigneten GTIN-Produktcode darf eine externe Leseabfrage auslösen; interne ASCII-Haushaltscodes bleiben lokal.
- Nur der Household-Server spricht mit einem festen freigegebenen OFF-Endpunkt. APIv3, eigener User-Agent, begrenzte Gesamtdauer/Antwortgröße/Parallelität, verweigerte Weiterleitungen und ein persistentes schonendes Abfragebudget sind verbindlich. Keine Netzwerkaufrufe in offenen Geschäftstransaktionen.
- Geplant sind höchstens12Produktabrufe je Minute/Installation unter dem aktuell dokumentierten15erUpstreamlimit.429/Retry-After, Fehler und ausbleibende Daten bleiben eigene sichtbare Zustände.
- Ein begrenzter Pantry-Cache speichert nur notwendige Textinformationen, Treffer-/Fehlerzustand, Fingerprint, Quelle, Lizenz und Abruf-/Ablaufzeit. Positive Daten gelten zunächst7Tage, negative Treffer höchstens1Stunde; veraltete Angaben bleiben als solche bezeichnet.
- Erste Ausbaustufe: Produktname, Marke und Packungsbeschreibung. Kein direktes Nachladen externer Bilder, HTML, Skripte oder Fonts. Packungsangaben werden nicht stillschweigend als exakte Basismenge interpretiert.
- Benutzer wählen und prüfen lokale Produktfelder, Einheit und Packungsmenge ausdrücklich. Eine bestätigte Anlage von Produkt/Packung/Barcode speichert die Quellenzuordnung und einen dauerhaften Beleg atomar. Vorhandene lokale Zuordnungen werden nicht überschrieben.
- Ein externer Treffer erzeugt keine Charge, Bestandsbewegung, Haltbarkeit oder Allergensicherheitsbehauptung. Nach einer Netzwerkantwort werden aktuelle Identität, Rolle, Modulstatus und Generation erneut geprüft.
- ODbL-/DbCL-Herkunft wird am Vorschlag und bei gespeicherter Herkunft mitgeführt. Private Haushaltsdaten werden nicht an OFF übertragen. Der externe Dienst wird ausschließlich lesend verwendet.

Offizielle Recherche am28.10.2026: [API-/Nutzungsübersicht](https://openfoodfacts.github.io/documentation/docs/Product-Opener/api/), [Produktabfragev3](https://openfoodfacts.github.io/documentation/docs/Product-Opener/v3/products/get-api-v3-product-code/), [Nutzungsbedingungen im offiziellen Hilfecenter](https://support.openfoodfacts.org/help/en-gb/12-donnees-api/94-y-a-t-il-des-conditions-pour-utiliser-l-api). Die Einführung empfiehltv3, benennt15Produktabfragen/min/IP sowie eigenen User-Agent. Angaben sind freiwillige Produktdaten und werden als unbestätigte Vorschläge behandelt. Operator-Konfiguration und Attribution werden dokumentiert; reale Upstream-Ausführung wird erst in Phase3 gesondert protokolliert.

## PA19 – Offline-Scanentwürfe

- Eigener versionierter IndexedDB-Vertrag mit Haushalt, Benutzer, Geräteepoche, Pantry-Generation, Entwurfs-/Zeilen-ID und Revision. Speicherung nur nach ausdrücklichem Geräte-Opt-in.
- Höchstens100Zeilen, gemeinsames begrenztes Bytebudget und7Tage Ablauf. Quote, Besitzer, Epochengültigkeit und Schreiben werden in derselben lokalen Transaktion geprüft.
- Inhalt: exakter Barcode und ausdrücklich eingegebene Vorschlagswerte. Keine Bestandsstände, verfügbaren Chargen, Cookies, CSRF-Werte, Kamerabilder oder ausführbaren Mutationsanfragen.
- Eine öffentliche datenfreie Scan-Shell und lokale Assets ermöglichen Offline-Neustart. Private HTML/API-Daten bleiben ungecacht. Kamera nur auf den gezielt freigegebenen Scan-Dokumenten, ausdrücklich gestartet, begrenzt und vollständig gestoppt.
- Wiederholte Kameraerkennung erzeugt keine zusätzliche Packung. Absichtliche Wiederholung und eventuelles Zusammenfassen verlangen eine bewusste Entscheidung.
- Logout, Kontowechsel und Opt-out löschen Entwürfe, erhöhen die Geräteepoche, stoppen Kameras und verbergen offene Ansichten. Andere Tabs und verspätete Antworten prüfen dieselbe Revision/Epoche erneut.
- Gleicher Benutzer mit neuer Sitzung benötigt ausdrückliche Wiederaufnahme und neue Prüfung. Andere Benutzer/Haushalte sehen alte Inhalte nicht. Alte Modulgenerationen werden gesperrt und niemals automatisch umgebunden.
- Nach Verbindung: aktuelle Identität/Schreibrechte/Generation laden, alle Codes lokal neu auflösen, Produkt/Packung/Mengen/Lagerort prüfen, ausdrücklich in einen privaten Serverentwurf übernehmen und erst anschließend separat buchen.
- Vorhandene Pantry-Sammelentwürfe und deren dauerhafte Belege bilden den Online-Abschluss. Kein automatischer Upload/Background-Sync beim `online`-Ereignis. Ungewisse Abschlüsse behalten ihre Kennung und müssen geklärt werden.

## Gemeinsame Architektur und Migrationsgrenze

- Standardbibliothek-HTTP, explizite parametrisierte SQL-Abfragen, begrenzte Ressourcen und Kontextweitergabe. Aktuelle Sitzung, Haushalt, Rolle und Modulgeneration vor jeder Wirkung und historischen Belegausgabe.
- Neue Pantry-Tabellen/Metadaten und eng begrenzte Trigger werden vorwärts ergänzt. Alle74akzeptierten SQL-Dateien bleiben unverändert.
- Core bleibt zunächst auf26: Shopping8 und Fuel5 besitzen eng geprüfte Core-Vorgänger. Gemeinsame Einheiten-/Linkregistrierung geht über begrenzte Core-Katalogverträge. Neue Portionseinheit und Audit-Erweiterung müssen exakt gegen den bekannten Vorgänger geprüft werden; keine pauschale Tabellenzählung, Fingerprint-Neuerfassung oder Lockerung finanzieller Sperren.
- Neue Geschäfts-/Infrastrukturtabellen werden ausdrücklich klassifiziert. Bereitschaft prüft Spalten, Constraints, Funktionen, Trigger, Rechte und die bestehenden Fuel-/Shopping-Verträge weiterhin streng.
- Purge entfernt neue modulbezogene Daten/Cache und entkoppelt Core-Herkunft geordnet; Generationen verhindern alte Wirkungen. Datenbank und kanonische Dateien müssen eine echte verschlüsselte Restore-Probe überstehen.
- README, Operator-Konfiguration, Funktionsdokumentation und180erCheckliste werden beim strukturellen Übergang aktualisiert. Implementierung und ausgeführte Abnahme bleiben getrennt.

## Implementierungscheckliste

- [x] Bestehende17Vorratspunkte, aktuelle Schemas und Core-Verträge analysiert.
- [x] PA10/PA13/PA19 vollständig vor Umsetzung zusammengefasst.
- [x] Offizielle OFF-Schnittstellen-/Lizenz-/Budgetanforderungen recherchiert.
- [x] Gemeinsame Daten-/API-/Einheiten- und Vorgangsverträge festlegen; Quellenvertrag in `pantry-extension-api.md`.
- [x] Vorwärtsmigration, Integritäts-/Auditvertrag und Lifecycle vollständig im Quellcode umsetzen; noch keine Laufzeitabnahme.
- [x] PA10 Store-/Core-/HTTP-/UI-Ablauf vollständig verbinden, einschließlich leserfähiger Gerichtdetails und wiederholter Umlagerungsherkunft.
- [x] PA13 Provider-/Cache-/Übernahme-/Konfigurations-/UI-Ablauf vollständig verbinden, einschließlich vollständiger begrenzter Herkunftsexporte.
- [x] PA19 lokale Entwürfe, neutrale Shell, Besitzer-/Epochenschutz und Online-Abschluss im Quellcode verbinden; UI-Quellen übergeben, noch nicht ausgeführt.
- [x] Alle drei Punkte gemeinsam auf Vollständigkeit prüfen und erst dann Testphase öffnen; unabhängige Quellenprüfungen abgeschlossen.
- [x] Vollständige registrierte Go-Unit- und Node-Prüfungen ausführen.
- [x] Gemeinsame native PostgreSQL-/HTTP-Regression ausführen: 156/156; aktuelle UI-Assets zusätzlich 28/28.
- [x] Ausgewählte Linux-Race-Prüfungen ausführen: 114/114; Quellstand und spätere reine UI-Korrektur getrennt belegt.
- [x] Formatierung, Vet, Lint, Sicherheits-, Abhängigkeits-, Migrations- und Workflowprüfungen ausführen.
- [x] Gesamten Browserlauf einschließlich Recovery und anschließender bestehender Browserkette abschließen:102Checks/4Axe,14echteRecovery-Schritte und28Komponenten/912Checks/133saubereAxe.
- [x] Echte verschlüsselte Sicherung mit belegtem Inhalt aller 19 Vorratstabellen ausführen.
- [x] Strikte Wiederherstellung, Datenvergleich, echte Löschung und Generationswechsel abschließen: aktueller Lauf5b9df17c,14/14Schritte; abschließender Browser-Logout bleibt gesondert offen.
- [x] Alle gefundenen Modulfehler vollständig beheben und betroffene Prüfungen wiederholen; fehlgeschlagene Läufe und Grenzen erhalten.
- [x] Nachweise/README/Checkliste abgleichen und gesamtes Vorratsmodul lokal abnehmen;85verifiziert/0inArbeit/95offen.

## Analysegrenzen

Die lokale Graphify-Quellkarte verwendet106hashgeprüfte Code-Dateien des07889d1-Stands,760Knoten/2256rohe Kanten und keine LLM-/API-Aufrufe.11HTML-Dateien sind nicht im Codeparser,24SQL-Dateien fehlen wegen nicht vorhandenem SQL-Parser.428offene Kanten und4mögliche gerichtete Kantenkollapsfälle begrenzen den Graphen; SQL-/HTTP-Verbindungen wurden zusätzlich direkt gelesen. Die Karte dient der Orientierung und ist kein vollständiger Vertrags- oder Funktionstest. Rohdaten und Quellfundstellen bleiben unter `.local/pantry-module-map/` erhalten.

## Vollständigkeitskorrekturen vor Öffnung der Testphase

- Restchargen erhalten eine leserfähige Detailansicht für eigene Zubereitungs-/Portionsmetadaten. Die bestehende Umlagerung legt neue Chargen an; eine eng geprüfte Herkunftslinie muss deshalb Vorbereitung und Rezeptbezug über Teil- und Folgeumlagerungen erhalten, ohne ursprüngliche Zugangsmengen oder Quellenlimits erneut zu zählen.
- Herkunft ist auch für Haushaltsleser an übernommenen Produkten sichtbar. Mehr als20Herkunftseinträge ergeben eine ausdrücklich gekennzeichnete neuesteTeilmenge; vollständige Export-Herkunft wird separat gebunden und größenbegrenzt ausgegeben.
- Pantry-Produkt-/Packungs-/Barcode-CSV führt `source_provenance` mit. Ein CSV kann keine vertrauenswürdige Anbieterherkunft erfinden: leere Herkunft bleibt normaler Import, nichtleere Herkunftsspalten sperren eine ungeprüfte Neuerfassung mit erklärter Zeilenmeldung.
- Die HTTP-Providerabfrage hält während des Netzwerks keine Lifecycle-Sperre. Nach Rückkehr prüft der Store aktuelle Sitzung und Generation erneut. Neue Rollback-Aufräumpfade erhalten einen unabhängigen kurzen Kontext.
- Die positive OFF-Browserprüfung bleibt von normalem Offline-/CI-Betrieb getrennt und benötigt einen tatsächlichen frischen Treffer des ausdrücklich ausgewählten festen Endpunkts. Staging-Ausfälle und reguläre Abrufe werden getrennt belegt. Eine Fixture oder ein leerer Cache ist kein Upstream-Nachweis.

Diese Punkte vervollständigen die zuvor zusammengefassten Abläufe. Bis zum Abschluss dieser Quellenprüfung wurden keine neuen Modul-Laufzeitprüfungen ausgeführt.

## Phase 3 geöffnet – 28.10.2026

Die gesamte Modulimplementierung ist geschlossen. PA10 und die sechs neuen Pantry12-Tabellen einschließlich eng begrenzter Funktionen wurden unabhängig geprüft; PA13 besitzt einen bestätigten Acht-Dateien-Quellstand und die UI einen bestätigten 26-Dateien-Quellstand. Die neue Migration ist mit `632e832bf1c400c226726e687b0c79f7cd5ebd4c7d11d00e6d2bd5264cfe762c` gepinnt. Alle74angenommenen Vorgängermigrationen bleiben unverändert. Erst ab diesem Übergang beginnen Compiler-, Unit-, PostgreSQL-, Browser-, Race-, Sicherheits- und Recovery-Prüfungen. Fehlerkorrekturen und neue Quellstände werden mit den tatsächlich ausgeführten Wiederholungen dokumentiert. PA10/PA13/PA19 bleiben bis zur gemeinsamen Modulabnahme in Arbeit.

## Aktueller Prüfstand – 29.10.2026

Der erste echte Browserlauf und ein begrenzter Diagnoseabruf zeigten einen HTTP502 des OFF-Staging-Dienstes; der Client meldet korrekt `upstream_status`. Der erfolgreiche eigenständige Lauf7d0 verwendet ausdrücklich den vorhandenen festen regulären Leseendpunkt und belegt einen frischen Treffer, Übernahme und drei vollständige Herkunftsexporte. Staging bleibt separat als ausgefallen dokumentiert; Teststarter-Standard bleibt Staging, normale28erRegression und CI verwenden keine echte Anbieterabfrage.

Der gefundene Offline-Ablaufgrenzfall ist korrigiert: Vor jedem PUT werden aktuelle Autorität und die genaue lokale Absicht atomar geprüft; nach Ablauf entstehen null weitere PUTs. Statusklärung derselben Kennung bleibt möglich. Reale Browserprüfungen belegen Ablauf/Pruning, getrennte Bestandsbuchung und physische lokale Löschung nach Logout. Die Restore-Abweichung gleichwertiger Standard-ACLs wurde eng im neuen Katalogvergleich korrigiert und durch echte Restore-/Rechteprüfungen bestätigt.

Bestanden: native157/157 auf489unveränderten Quellenb620, GoUnit225, Node197, vier saubereAxe-Scans und102Browserprüfungen sowie14echteRecovery-Schritte. Die abschließende28-Komponenten-Regression bestand912Checks und133Axe-Scans ohneVerstöße; alle147Screenshotkopien wurden unabhängig geprüft. Der frühere LinuxRace114 gehört weiterhin zu seinem dokumentierten Quellstand; Produktions-Go ist seitdem unverändert. Pantry12 trägt SHA256 `660278a1917b2cd440f4d1172c05df07c1287ea11ca0e47c72878fb627854b1b`; alle74Vorgänger sind unverändert. Git erhält die Rohbytes und erkennt echteCRLF als Zeilenende, ohne normaleWhitespace-Prüfungen auszuschalten. PA10/PA13/PA19 sind nach Quellen-/Nachweisabgleich gemeinsam verifiziert. [Ausgeführte Prüfungen und Grenzen](pantry-module-validation.md).
