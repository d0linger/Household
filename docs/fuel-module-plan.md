# Fahrzeuge & Tanken – verbindlicher Modulblock

## Arbeitsmodus

Aktuelles Modul: **Fahrzeuge & Tanken (FU01–FU20)**.

1. Die Zusammenfassung aller zwanzig Anpassungen wurde vor der weiteren Implementierung vorgelegt.
2. Der gesamte Modulumfang wird umgesetzt. Parallelaufgaben gehören ausschließlich zu diesem Modul.
3. Erst nach vollständiger Umsetzung beginnen die gemeinsame Testphase und die systematische Fehlerbehebung.
4. Ein Wechsel zum nächsten Modul erfolgt erst nach fehlerfreier Modulabnahme.

Stand: **Lokale Gesamtabnahme am28.10.2026 bestanden**. Die vollständige Modulimplementierung wurde in den Entwicklungsstand übernommen. Frühere Einzelprüfungen stammen aus dem vorherigen Arbeitsmodus und ersetzen keine vollständige Modulabnahme. Die neuen Unit-, Integrations-, Browser-, Race-, Build- und Lint-Prüfungen wurden erst nach vollständiger Implementierung begonnen und anschließend abgeschlossen. Quellbearbeitung, Recherche, statische Durchsicht und Formatierung gehören zur Implementierung.

## Vollständiger Umfang

| ID | Umsetzung | Inhalt |
|---|---|---|
| FU01 | Lokal verifiziert | Fahrzeugübersicht mit letzter Tankung, passender Zählerreihe, erklärtem Verbrauch und Terminen. |
| FU02 | Lokal verifiziert | Schnellerfassung mit persönlichen Fahrzeug-/Serien-/Stationsvorgaben und ausdrücklicher Bestätigung. |
| FU03 | Lokal verifiziert | Exakte Berechnung des jeweils fehlenden Wertes aus Menge, Einheitspreis und Betrag; sichtbare Rundung. |
| FU04 | Lokal verifiziert | Strukturierte Gründe für nicht berechenbaren Verbrauch und konkrete nächste Schritte. |
| FU05 | Lokal verifiziert | Gefilterte Verbrauchs-/Kostenverläufe mit zugänglichen Tabellen und konsistenten Einheiten. |
| FU06 | Lokal verifiziert | Weitere Fahrzeugkosten und deduplizierte Ausgabenverknüpfungen über Core-Verträge. |
| FU07 | Lokal verifiziert | Wartungspläne und nachvollziehbare Datums-/Kilometerfälligkeit. |
| FU08 | Lokal verifiziert | Kosten pro dokumentierter Strecke mit erklärtem Zeitraum, Kostenumfang und unbekannten Werten. |
| FU09 | Lokal verifiziert | Ladevorgänge, kWh, Ladeort, Kosten und optionaler Ladezustand; eigenständige Energiebilanz. |
| FU10 | Lokal verifiziert | Manuelle Fahrten, Zweck, Zählerbezug, Korrekturen und Export. |
| FU11 | Lokal verifiziert | Dokumentierte CSV-Profile, begrenztes Staging, Vorschau, Fehler und nachvollziehbare Dublettenentscheidungen. |
| FU12 | Lokal verifiziert | Lokale Tankbeleg-OCR mit prüfbaren Vorschlägen und Herkunft; Bestätigung vor Übernahme. |
| FU13 | Lokal verifiziert | Kilometerfoto mit Vorschlag, Reihen-/Vorwertvergleich und Bestätigung. |
| FU14 | Lokal verifiziert | Deduplizierte Verknüpfung fälliger Wartung mit Aufgaben über Core-Verträge. |
| FU15 | Lokal verifiziert | Berechtigte Fahrzeugdokumente über Core-Verknüpfungen. |
| FU16 | Lokal verifiziert | Große Historien über Zeitfenster und Cursor mit vollständigen benötigten Randtankungen. |
| FU17 | Lokal verifiziert | Versionierte, invalidierbare Verbrauchssegmente und begrenzte Neuberechnung. |
| FU18 | Lokal verifiziert | Geführter Tachowechsel mit tatsächlichem Abschluss- und Anfangsstand. |
| FU19 | Lokal verifiziert | Verbindliche Liter-/Kilogramm-/kWh-Zuordnung; keine stillen Umrechnungsannahmen. |
| FU20 | Lokal verifiziert | Erklärte konfigurierbare Plausibilitätsgrenzen; Originalwerte bleiben erhalten. |

## Gemeinsame Grundlagen

- Fuel-eigene dauerhafte Vorgangsbelege verhindern doppelte Wirkungen nach Wiederholung, Antwortverlust oder Löschung eines Datensatzes. Der bestehende begrenzte Antwortcache genügt dafür nicht.
- Aktuelle Sitzung, Mitgliedschaft, Rolle, Modulzustand und Generation werden vor jeder Mutation und Wiederholung geprüft. Abhängige Core-Verträge prüfen ihre eigenen aktuellen Berechtigungen.
- Beträge, Mengen, Preise und Strecken werden dezimal beziehungsweise rational verarbeitet. Unbekannt ist kein Nullwert; Währungen und Mengenarten bleiben getrennt.
- Änderungen bleiben versioniert und nachvollziehbar. Korrekturen, Löschungen, Wiederholungen und abgeleitete Neuberechnungen besitzen ausdrückliche Regeln.
- Modulschemas bleiben unabhängig. Verknüpfungen zu Ausgaben, Aufgaben und Dokumenten werden als Fuel-Anbindungen über Core-Verträge umgesetzt.
- Unveröffentlichte Finanzvorbereitungen werden nicht pauschal übernommen. Die Freigabe privater Finanzsichtbarkeit bleibt gesperrt.
- CSV- und OCR-Vorschläge erzeugen erst nach ausdrücklicher Prüfung Geschäftsdaten. Dateien, Zeilen, Bilder, Text, Laufzeit und Jobs erhalten feste Grenzen.
- Alle Oberflächen bleiben lokal ausgelieferte, mobile PWA-Ansichten mit vorhandenen Designtokens, sichtbaren Labels,44px-Bedienflächen und erklärten Leer-/Fehlerzuständen.
- Alle bisherigen71SQL-Dateien bleiben unverändert. Neue Schemata und eng begrenzte Schutzfunktionen werden vorwärts ergänzt.

## Implementierungsreihenfolge innerhalb dieses Moduls

1. Gemeinsame Datentypen, Schema und dauerhafte Vorgangsverarbeitung.
2. Einheitensystem, exakte Preisrechnung, Schnellerfassung und Tachowechsel.
3. Lade- und Fahrtdaten sowie weitere Fahrzeugkosten.
4. Wartungspläne und Core-Anbindungen für Ausgaben, Aufgaben und Dokumente.
5. Große Historien, Segmentberechnung, Invalidierung, Erklärungen und Plausibilität.
6. CSV-Staging/Übernahme und lokale OCR-/Fotoprüfung.
7. Vollständige Navigation, Formulare, Diagramme/Tabellen, Einstellungen und Dokumentation.
8. Vollständigkeitsdurchsicht aller zwanzig Punkte; danach Öffnung der Testphase.

## Erledigter Implementierungsfortschritt vor der gemeinsamen Testphase

Diese Haken dokumentieren die vor der gemeinsamen Testphase abgeschlossenen Implementierungsschritte. Zu diesem Zeitpunkt lautete der Stand62verifiziert/20inArbeit/98offen. Die inzwischen bestandene Gesamtabnahme am Ende dieses Dokuments schließt alle20FU-Punkte; der neue Gesamtstand lautet82verifiziert/0inArbeit/98offen.

- [x] Vollständige FU01–FU20-Zusammenfassung und verbindliche Modulgrenze dokumentiert.
- [x] Gemeinsame Datenfelder, Aktionsformen und Zuständigkeiten für dieses Modul festgelegt.
- [x] Quellentwürfe für neue Fuel-Tabellen, Vorgangsbelege, tatsächliche Änderungserfassung und Schema-Prüfvertrag angelegt.
- [x] Quellentwürfe der Core-Anbindungen für Ausgaben, Wartungsaufgaben und Dokumente angelegt.
- [x] Quellentwurf für lokale OCR außerhalb der Geschäftstransaktion einschließlich Größen-, Hash- und Zeitgrenzen angelegt.
- [x] Neue Arbeitsbereiche und Routen in der isolierten Modulvorbereitung registriert.
- [x] CSV-Staging/Übernahme und OCR-/Fotobestätigung vollständig mit dem Writer verbinden.
- [x] Alle Lese-, Export-, Auswertungs- und Segmentabläufe vervollständigen.
- [x] Alle zwanzig Funktionen in den zwölf Oberflächen im Quellcode verbinden.
- [x] Gemeinsame statische Vollständigkeitsdurchsicht und Übernahme des vollständigen Moduls in den Entwicklungsstand.
- [x] Danach gemeinsame Testphase öffnen. Sämtliche Modulfehler werden jetzt systematisch behoben.

Der vollständige Implementierungsentwurf bleibt in `.local/fuel-module-work/` erhalten. Die anschließende Test- und Fehlerbehebungsphase arbeitete im Projektstamm; dieser enthält die abgenommenen Korrekturen. Die71alten SQL-Dateien bleiben unverändert. Vor der vollständigen Modulimplementierung wurden keine neuen Laufzeitprüfungen gestartet. Live8100 bleibt unverändert.

## Testfortschritt innerhalb des Moduls

Diese Haken belegen die genannten ausgeführten Teilprüfungen auf ihrem jeweiligen protokollierten Quellstand, keine vollständige FU-Abnahme. Sie schließen keinen der20FU-Punkte vorzeitig.

- [x] Kompilierungswiederholung des vollständigen Implementierungsstands bestanden.
- [x] Alle169aktuellen Node-Fälle bestanden, einschließlich17Exaktheitsgruppen,25OCR-/Recovery-Schutzverträgen und der Vollständigkeit aller drei Testlisten.
- [x] Aktuelle Store-/Worker-Wiederholung bestanden:55Haupttests, keine übersprungenen Fälle.
- [x] Frische Datenbankmigration, Wiederholung und Audit-Reparatur tatsächlich bestanden.
- [x] Signierte HTTP-Vorschau, Schlüsselbindung und einmalige Übernahme tatsächlich bestanden.
- [x] Alle9nativen Schreib-/Wiederholungs-/Import-/Tachowechsel-/Upgrade-Verträge im gemeinsamen Lauf bestanden.
- [x] Alle3nativen Capture-Verträge einschließlich Originalupload, Berechtigungswechseln und abgelaufenen Aufträgen bestanden. Der Extraktions-Callback ist dabei deterministisch; dies belegt keine Tesseract-Erkennung.
- [x] Historisch rückläufige Stände bleiben vor und nach dem Cache-Aufbau unbekannt; gezielte native Regression bestanden.
- [x] Drei Core-Anbindungen für Ausgaben, Wartungsaufgaben und Dokumente vollständig fehlerfrei wiederholen.
- [x] Große Historie mit20501Originaltankungen vollständig aufbauen, auswerten und durchblättern.
- [x] Betroffene bestehende Anwendungsabläufe einschließlich sämtlicher nach dem früheren Timeout unbestätigter Fälle auf dem aktuellen Fahrzeugmodulstand prüfen:69/69gemeinsam bestanden.
- [x] Tatsächliche lokale OCR, Browser, Wiederherstellung, Race und Sicherheitsprüfungen abschließen.

Die frühere gemeinsame Wiederholung besteht69/69Haupttests,0Fehler/0übersprungen,1020.619s; alle454Quellen und der Pfadbestand bleiben unverändert. Sie umfasst sämtliche21aktuellen Fahrzeugmodul-Verträge,11alte Fahrzeugübersicht-Verträge, die Migrationsregression und alle36zuvor unbestätigten Fälle. Der frühere vollständige Repository-Lauf bleibt476bestanden/6fehlgeschlagen mit50-Minuten-Limit. Der tatsächliche512-Fälle-Abgleich hat keinen Fall ohne bestandene Beobachtung:69aktuell und443nur auf dem früheren Stand; er ist ausdrücklich kein einzelner vollständig bestandener Lauf des aktuellen gesamten Quellstands. Diese Prüfungen lagen damals noch vor der Browser-/OCR-/Restore-/Race-Abnahme. Die späteren erfolgreichen Ergebnisse stehen am Ende dieses Dokuments; Details und erhaltene Fehlläufe: [Abnahmematrix](fuel-module-validation.md).

## Abnahme – erst nach vollständiger Implementierung

- [x] Alle20Funktionen besitzen vollständige Backend- und UI-Abläufe; alle20Funktionsworkflows und die zusätzliche mobile Axe-Abnahme im echten Browser bestanden. Der vollständige Recovery-Abschluss bleibt separat offen.
- [x] Exakte Werte, Einheiten, Zählergrenzen, Rundung und Kalenderlogik durch Unit-, Datenbank- und Browserfälle geprüft.
- [x] Datenbankmigration, Upgrade, Integrität, SQL-Nebenläufigkeit und dauerhafte Wiederholung auf den protokollierten Quellständen geprüft; Go-Race bleibt separat offen.
- [x] Core-Verknüpfungen, Moduldeaktivierung, Rechtewechsel, Löschung und echte verschlüsselte Wiederherstellung geprüft; vollständiger Browser-/Recovery-Lauf bestanden.
- [x] Große Historien, vollständige Segmentveröffentlichung, exakte Quell-/Projektionsversionen und Ressourcenlimits geprüft.
- [x] CSV-Profile, gesperrte Parserfehler und alle6tatsächlichen lokalen OCR-Fälle geprüft; PDF-Seite2bleibt ausgeschlossen.
- [x] Vollständige Browserabläufe, mobile/dunkle Darstellung, Tastatur und Barrierefreiheit geprüft.
- [x] Linux-Race87/87, Sicherheits-, Abhängigkeits- und Workflowprüfungen auf dem dokumentierten Stand ausgeführt; keine gehostete CI-Ausführung behauptet.
- [x] Entdeckte Modulfehler systematisch behoben und betroffene Prüfungen wiederholt.
- [x] README, Nachweise und180erCheckliste stimmen mit dem ausgeführten Ergebnis überein.
- [x] Gesamtes Fahrzeugmodul freigegeben; erst danach nächster Modulblock.

## Erhaltene Arbeitsstände

Zwischenstand nach der Restore-Korrektur, vor den abschließenden Browser-/Race-Läufen:70/70gemeinsame Haupttests bestanden,0Fehler/0übersprungen,456Quellen unverändert. Die separate statische Wiederholung besteht Formatierung, getaggtes vet/Lint und Gosec mit0Befunden. Eine echte verschlüsselte Sicherung sowie20Funktionsabläufe,6lokale OCR-Quellen und mobile Axe-Prüfungen wurden ausgeführt; die erfolgreiche anschließende Wiederherstellung, der28-Komponenten-Regressionstest und Linux-Race bleiben Abnahmebedingungen. Zu diesem Zwischenstand blieb die180erListe62verifiziert/20inArbeit/98offen.

Der aktuelle Fuel-Ausgangspunkt ist der404-Dateien-Stand mit der mobilen CSS-Korrektur, Manifest `5e3e04a8d4a2a6746ae0cceb3f91993980471366ac6ef2443035edbad2f6a36c`. Die62bereits verifizierten Verbesserungspunkte bleiben erhalten. RE05- und EX19-Vorbereitungen bleiben in ihren vorhandenen isolierten Arbeitsverzeichnissen zurückgestellt. Live8100 bleibt unverändert; es erfolgt keine Veröffentlichung.

## Abgeschlossene lokale Modulabnahme

FU01–FU20 sind gemeinsam verifiziert.70aktuelle native Haupttests,87Linux-Race-Haupttests,20echte Browserworkflows/1031Prüfungen,6lokale OCR-Quellen und die28bestehenden Browserkomponenten/908Prüfungen bestehen. Die tatsächliche verschlüsselte Wiederherstellung mit Dateien, Purge und genau eine neue Generation sind bestätigt. Statische Sicherheits-/Abhängigkeits-/Workflowprüfungen und175Node-Fälle bestehen;456Anwendungsquellen bleiben unverändert. Alle74Migrationshashes, darunter71vorherige Dateien, sind erhalten. Frühere offene Zwischenstände oben bleiben zeitlich zugeordnet; maßgeblich ist die [abschließende Abnahmematrix](fuel-module-validation.md#lokale-gesamtabnahme-fu0120). Aktueller Gesamtstand:82verifiziert/0inArbeit/98offen. Live8100 und Geschwisteranwendungen sind unverändert; gehosteteCI/Linux-Browserportabilität und eine komplette aktuelle Repository-Abdeckung bleiben unbehauptet.
