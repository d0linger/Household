# Aufgaben – vollständiger Modulplan CH01–CH20

Stand: 1. November 2026. Voraussetzung erfüllt: Finanzen ist mit 20/20 Punkten lokal abgenommen; Code-Checkpoint `0102380`, Abnahme-Checkpoint `66e63c5`. Gesamtstand vor diesem Block: **120 verifiziert, 60 offen**.

Dieser Block umfasst alle 20 Aufgabenverbesserungen. Reihenfolge: vollständigen Umfang beschreiben, gemeinsam implementieren, danach gemeinsam testen und Fehler beheben. Einzelne vorbereitete Tabellen oder Schaltflächen schließen keinen CH-Punkt ab. Der nächste Modulblock beginnt nach bestandener Aufgabenabnahme.

## 1. Bestandsaufnahme und Grenzen

Vorhanden sind `chores.task_templates`, `chores.task_occurrences`, einfache Tagesintervalle, generische Formulare sowie die Core-Verknüpfung zu Fahrzeugwartungen. Der bisherige Abschluss sperrt das Vorkommen und erzeugt bei Bedarf einen Nachfolger. Er unterscheidet noch keine Kalenderregeln, Vorlagenstände, Unteraufgaben, Abwesenheiten oder persönlichen Tagesvorschläge.

Die vorhandene Wartungsschnittstelle bleibt ein Core-Vertrag. Optionale Module erhalten keine Fremdschlüssel zu anderen optionalen Modulen. Es gibt keine Pflichtverbindung zu einem externen Kalenderdienst und keinen automatischen Export privater Finanzdaten. Keine Produktionsbeispiele, externen Skripte, neuen Frameworks oder CDN-Ressourcen.

Die 92 freigegebenen Migrationen bleiben bytegleich. Geplant sind neue Barrieren für Chores 2 und Core 32 sowie eng begrenzte Kompatibilitätsmigrationen für Fuel, Pantry und Mealplan. Deren bisherige Sicherheits- und Katalogprüfungen werden nicht pauschal neu eingefangen. Jede beabsichtigte Änderung muss gegen den geprüften Vorgänger und ihre genaue erlaubte Differenz validiert werden.

## 2. Vollständiger Funktionsumfang

| ID | Geplante Umsetzung und Abnahme |
|---|---|
| CH01 | „Heute für mich“ bündelt eigene Zuweisungen, ausdrücklich übernommene und fällige Aufgaben. Übernehmen und Freigeben sind atomare Aktionen; abgelaufene Übernahmen werden erklärt. Reader erhalten keine Schreibrechte. |
| CH02 | Benannte, sortierbare Bereiche mit offenen/überfälligen Anzahlen, Filter und leerem Zustand. Ein archivierter Bereich bleibt in der Historie erkennbar. |
| CH03 | Bei jedem Termin stehen ursprüngliche Fälligkeit, letzte tatsächliche Erledigung, Regel, Zeitzone und nachvollziehbarer nächster Termin. Unbekannte Altangaben bleiben unbekannt. |
| CH04 | Eigene Arbeitsansicht mit Anleitung, Material, Checkliste, Voraussetzungen, optionaler tatsächlicher Dauer und ausdrücklich bestätigtem Abschluss. Lade- und Fehlerzustände erhalten Eingaben und Fokus. |
| CH05 | Einzeltermin verschieben, genau ein Vorkommen auslassen und Serie pausieren/fortsetzen sind getrennte, vorab erklärte Aktionen. Ursprünglicher Termin und Historie bleiben erhalten. |
| CH06 | Begrenzte Tagesintervalle, ausgewählte Wochentage, Monatsabstände und Saisonmonate; Vorschau statt freier ungeprüfter RRULE-Ausdrücke. Der 31. wird pro Zielmonat geklemmt, ohne den ursprünglichen Monatsanker zu verlieren. |
| CH07 | Fester Zeitplan oder ausdrücklich „X Tage nach tatsächlicher Erledigung“. Erledigungsanker werden nicht stillschweigend mit Kalenderwochenregeln vermischt. Auslassen gilt nicht als Erledigung. |
| CH08 | Geordnete Rotation unter geeigneten aktiven Personen; Abwesenheiten, Vertretung und fehlende Kandidaten werden berücksichtigt. Vergangene tatsächliche Zuständigkeiten bleiben eingefroren. |
| CH09 | Optionale geschätzte Minuten, Schwierigkeit und geeignete Personen. Fehlende Dauer wird nicht als null Minuten in Planungsbudgets eingerechnet. |
| CH10 | Versionierte, optional verpflichtende Unteraufgaben und begrenzte Abhängigkeiten. Zyklen, Selbstbezug und fremder Haushalt werden abgewiesen. Abschluss erklärt unerledigte oder fehlende Voraussetzungen. |
| CH11 | Persönliches Zeitbudget und Priorität liefern einen deterministischen Tagesvorschlag mit Auswahlgründen, Restbudget und gesondert ausgewiesenen unbekannten Dauern. Keine automatische Erledigung oder heimliche Zuweisung. |
| CH12 | Abwesenheitszeiträume und ausdrücklich gewählte Vertretung. Neue Vorschläge berücksichtigen sie; bereits angenommene Termine wechseln nicht unbemerkt die Person. |
| CH13 | Bestehende reale Wartungsaufgaben aus Core-Ereignissen bleiben dedupliziert, mit sichtbarer Herkunft und eigenständigem Aufgabenbestand. Neue Ereignisadapter verwenden geschlossene Typen und Core-Verträge, keine frei ausführbaren Ausdrücke oder Kopien privater Finanzfelder. |
| CH14 | Authentisierter ICS-Download und ausdrücklich erzeugter persönlicher, widerrufbarer Feed. Stabile Vorkommens-UIDs, Sequenzen und Absagen; gehashte Geheimnisse und sofortige Prüfung von Nutzer-/Modul-/Generationsgültigkeit. |
| CH15 | Personenbezogene Zusammenfassungen, Ruhezeiten und nur ausdrücklich gewählte Eskalation. Wiederholte Workerläufe liefern dieselbe Meldung nicht mehrfach. Deaktivierung/Entzug stoppt ausstehende Zustellung. |
| CH16 | Rollierender, begrenzter Horizont mit stabilen logischen Termin-IDs und höchstens einem offenen Nachfolger bei Erledigungsankern. Rückstände werden begrenzt sichtbar gemacht, nicht stillschweigend abgeschnitten. |
| CH17 | Historie nach Zeitraum, Bereich und tatsächlicher Person; Aufwand, Häufigkeit und unbekannte Werte. Keine erzwungene Rangliste. Historische Vorlagen und Zuständigkeiten bleiben nachvollziehbar. |
| CH18 | Gleichzeitige Abschlüsse erzeugen genau einen Erledigungsfakt. Wiederholung derselben Operation ist sicher; veraltete andere Entscheidungen erhalten einen verständlichen Konflikt. Checkliste, Voraussetzungen, Folgeplanung und Audit gehören zur selben Transaktion. |
| CH19 | Einzelterminänderung und Änderung zukünftiger Serie sind getrennt. Unveränderliche Vorlagenstände erhalten vergangene Termine; entfernte Zukunftstermine werden abgesagt. Vorschau erklärt betroffene offene Termine. |
| CH20 | Gespeicherte IANA-Zeitzone, lokale Kalenderrechnung und versionierte DST-Regel: frühere Instanz bei doppelter Uhrzeit, Verschiebung um die Lücke bei nicht existierender Uhrzeit. Keine 24-Stunden-Arithmetik für Kalendertage. |

## 3. Datenmodell

Die beiden vorhandenen Tabellen bleiben die stabilen Identitäten für Referenzen, Wartungslinks, Suche und generische Leser. Neue typisierte Erweiterungen liegen ausschließlich im Schema `chores`. Alle Geschäftstabellen tragen Haushalt und nachvollziehbaren Urheber/Zeit-/Versionsbezug; haushaltsbezogene Fremdschlüssel verhindern Vermischung.

| Neue Tabelle | Zweck und wesentliche Integrität |
|---|---|
| `areas` | Name, Sortierung, Archivierung; stabile Bereichs-ID und historischer Name im Vorlagenstand. |
| `series_profiles` | Genau ein Profil je Vorlage; aktueller Vorlagenstand, Bereich, Pause, Verwaltungsmodus und Prüfbedarf alter Regeln. |
| `template_versions` | Unveränderlicher Stand von Titel, Anleitung, Material, Aufwand, Priorität, Regel, lokalem Start, Uhrzeit und Zeitzone. Eindeutige Revisionsnummer je Vorlage. |
| `version_checklist_items` | Geordnete, begrenzte Checkliste je Vorlagenstand; stabile Punktkennung und Pflichtkennzeichen. |
| `version_dependencies` | Voraussetzungen des Vorlagenstands, gleiche Haushaltszugehörigkeit, kein Selbstbezug; begrenzte, serialisierte Zyklusprüfung. |
| `rotation_members` | Reihenfolge, Eignung und ausdrücklich gewählte Kandidaten je Vorlagenstand; aktive Mitgliedschaft vor tatsächlicher Zuweisung erneut prüfen. |
| `absences` | Begrenzter Zeitraum einer Person und optionale Vertretung desselben Haushalts. Keine unnötigen privaten Begründungen. |
| `occurrence_details` | Eingefrorener Vorlagenbezug, logischer Schlüssel, ursprünglicher lokaler/UTC-Termin, Overrides, Zuständigkeitsgrund und Übernahme. |
| `occurrence_checklist_items` | Tatsächlicher Checklistenstand eines Vorkommens, Bearbeiter und Zeitpunkt; nach Abschluss unveränderlich. |
| `completions` | Höchstens ein tatsächlicher Abschluss je Vorkommen mit Person, optionalen Minuten und eingefrorenen Voraussetzungen/Ergebnissen. Alte unbekannte Personen oder Dauern werden nicht erfunden. |
| `member_preferences` | Persönliches Tagesbudget, Planungspräferenzen, Digest-Zeit, Ruhefenster und ausdrückliche Eskalationswahl. |
| `calendar_feeds` | Eigentümer, SHA-256-Tokenhash, Auth-/Modulgeneration, Ablauf, Widerruf und begrenzte Filter. Kein dauerhaft gespeicherter Klartext-Link. |
| `reminder_deliveries` | Deduplizierte Empfänger-/Zeitraum-/Eskalationsstufe und Core-Zustellbezug; keine Pflicht zu externem Versand. |
| `schedule_state` | Begrenzter Erzeugungsfortschritt je Serie und Regelstand; wiederholbare Workerplanung und ausdrücklicher Rückstand. |

Geplant sind 14 zusätzliche Auditklassifikationen, damit insgesamt 135. Die endgültige Zahl wird aus den tatsächlich implementierten Tabellen und ihren expliziten Policies abgeglichen. Tokens, geheime Links und unbeschränkte Antwort-JSON werden nicht als Auditwerte veröffentlicht.

Bestehende Einträge werden geprüft übernommen: ursprüngliche UTC-Termine bleiben erhalten, bekannte Tagesregeln bleiben verständlich, fehlende Startdaten oder ungültige alte Zeitzonen verlangen eine bewusste Regelprüfung. Das Aktivieren des neuen Bereichs erzeugt keine erfundene Erledigung und verschiebt keine alte Fälligkeit. Bestehende einmalige Fahrzeugwartungen bleiben einmalig.

## 4. Architektur und Ressourcen

- Reine Kalender-/Planungslogik in einem begrenzten Go-Paket; Store führt ausschließlich parametrisierte SQL-Abfragen mit weitergereichtem Kontext aus.
- Standardbibliothek-HTTP und serverseitige Templates mit lokal eingebetteten JavaScript-/CSS-Dateien. Der bestehende Worker übernimmt nur neue, benannte Jobarten; keine zweite Scheduler-Infrastruktur.
- Schreibvorgänge prüfen aktuelle Session, Rolle, Haushalt, aktiviertes Modul, Datengeneration und erwartete Version. Schlösser werden in fester Reihenfolge erworben. Abschlussfakt und Terminidentität bleiben auch nach Ablauf einer Antwortquittung eindeutig.
- Vorhandene Core-Idempotenz und Modul-Lebenszyklusverträge werden verwendet. Ein Worker hält eine reale Lease und prüft sie vor Veröffentlichung erneut. Kalenderfähigkeiten werden bei jedem Abruf aktuell geprüft.
- Startgrenzen: höchstens 500 aktive Serien je Haushalt, 50 Checklistenpunkte und 16 Voraussetzungen je Stand, 32 Rotationspersonen, 90 Tage Erzeugungshorizont, begrenzte Vorschau und 100 Zeilen je Liste. Eine überschrittene Grenze liefert eine Erklärung statt unvollständiger Behauptungen.
- Keine Erhöhung der bestehenden SQL-/HTTP-Produktgrenzen zur Behebung von Fehlern. Große Historien erhalten Zeitfilter und stabile Cursor; große Auswertungen bleiben serverseitig begrenzt.
- Deaktivieren bewahrt Daten; Löschen ist weiterhin ein eigener, gesicherter Owner-Wartungslauf. Feeds, Jobs, Benachrichtigungen und alte Befehle beachten die Generationsänderung.

## 5. Oberfläche und mobile Abläufe

Die etablierte Household-Identität aus `DESIGN.md` bleibt erhalten: lokale Systemschrift, vorhandene Farben und Abstände, native Dialoge, sichtbare Labels und 44-px-Ziele. Kein neuer visueller Stil und keine Beispielinhalte im Betrieb.

```text
Aufgaben                         [+ Neue Serie]
Heute | Bereiche | Serien | Tagesplan | Verlauf | Einstellungen

Heute für mich      Datum / Filter / Haushalt anzeigen
Überfällig
  Küche aufräumen   Küche · 15 min · heute / ursprünglicher Termin
  Zugewiesen: ...   Warum fällig?  [Öffnen]
Heute
  ...              Nicht zugewiesene Aufgaben ausdrücklich übernehmen

Arbeitsansicht
Aufgabe / Bereich / tatsächlicher Status
Anleitung · Material
[ ] Pflichtpunkt  [ ] weiterer Punkt
Voraussetzungen: erledigt / fehlt / noch offen
Dauer optional   [Erledigung prüfen] [Verschieben] [Auslassen]
```

Mobil werden Navigation und Filter umgebrochen, keine Gesamtseite scrollt horizontal. Die Arbeitsansicht ist eine eigene fokussierte Seite. Große Serienformulare sind in beschriftete Abschnitte gegliedert. Vorschau nennt reale nächste Termine, Zeitzone, Zuständigkeitsgründe und Änderungen an offenen Vorkommen. Konflikte erhalten Eingaben, zeigen aktuellen Stand und verlangen erneute Entscheidung. Offline werden Finanz-/Aufgabenmutationen nicht lokal vorgemerkt; der Zustand wird verständlich erklärt.

## 6. HTTP-/Core-Verträge

Arbeitsbereich unter `/m/chores/workspace/{pane}` mit typisierten, begrenzten API-Routen unter `/api/v1/chores/`. Vorgesehen sind Lesen/Filtern, Serienvorschau und Speicherung, Terminaktionen, Unteraufgaben, Tagesvorschau, Abwesenheiten, persönliche Einstellungen, Historie sowie ICS-/Feedverwaltung. Bestehende Aufgabenlinks und die Fahrzeugwartungsaktion bleiben unterstützt. Reader lesen, richten gegebenenfalls nur ausdrücklich eigene Kalenderfähigkeiten ein und erhalten dadurch keine Aufgaben-Schreibrechte.

Konkrete Request-/Response-Felder, Konfliktregeln, DST-Verhalten, Feed-Grenzen und Adaptertypen werden vor Ende der Implementierung im englischen Laufzeitvertrag dokumentiert. Unbekannte Felder, nicht unterstützte Regelkombinationen und fremde IDs werden abgewiesen.

## 7. Chronologische Checkliste

### Phase 1 – vollständigen Umfang vor Verhalten festlegen

- [x] 001. Abgenommene Finanzvoraussetzung und 120/180-Stand verifizieren.
- [x] 002. PRODUCT, DESIGN und Implementierungsregeln lesen.
- [x] 003. Alle 20 CH-Kriterien vollständig erfassen.
- [x] 004. Bestehende Chores-Tabellen und Abschlussaktion analysieren.
- [x] 005. Echte Fahrzeugwartungsanbindung und Core-Grenze prüfen.
- [x] 006. Audit-, Readiness- und Versionsbarrieren analysieren.
- [x] 007. Unveränderliche Vorgängermigrationen und neue Kompatibilitätsstufen festlegen.
- [x] 008. Datenmodell, Altdatenübernahme und unbekannte Werte beschreiben.
- [x] 009. Wiederholungs-, Anker- und DST-Semantik festlegen.
- [x] 010. Mobile Ansichten und bestehende Gestaltungsregeln festlegen.
- [x] 011. Rollen, Ressourcen, Kalenderfähigkeiten und Integrationsgrenzen beschreiben.
- [x] 012. Gesamten Modulplan und gemeinsame Testphase veröffentlichen.

### Phase 2a – Daten und sichere Kompatibilität vollständig implementieren

- [x] 013. Chores-1-Startbarriere im Migrationsrunner erhalten.
- [x] 014. Bereichstabelle und haushaltsbezogene Schlüssel ergänzen.
- [x] 015. Serienprofile mit Prüfbedarf und Pausenzustand ergänzen.
- [x] 016. Unveränderliche Vorlagenstände mit begrenzter Regeldefinition ergänzen.
- [x] 017. Versionierte Checklisten ergänzen.
- [x] 018. Versionierte Voraussetzungen und Selbstbezugsschutz ergänzen.
- [x] 019. Geordnete Rotationspersonen und Eignung ergänzen.
- [x] 020. Abwesenheiten und Vertretungsbeziehungen ergänzen.
- [x] 021. Vorkommensdetails und stabile logische Schlüssel ergänzen.
- [x] 022. Tatsächliche Vorkommens-Checklisten ergänzen.
- [x] 023. Eindeutige, unveränderliche Abschlussfakten ergänzen.
- [x] 024. Persönliche Planungs-/Erinnerungseinstellungen ergänzen.
- [x] 025. Gehashte, widerrufbare Kalenderfähigkeiten ergänzen.
- [x] 026. Deduplizierte Erinnerungszustellung und Erzeugungsfortschritt ergänzen.
- [x] 027. Sichere alte Vorlagen-/Vorkommensstände ohne erfundene Werte übernehmen.
- [x] 028. SQL-Invarianten für Zustand, Haushalt und historischen Abschluss installieren.
- [x] 029. Core-32-Auditregistrierung mit geprüfter Vorgängerdifferenz ergänzen.
- [x] 030. Finance-Readiness nur um die genaue Auditdifferenz fortschreiben.
- [x] 031. Fuel-Coreversionskompatibilität eng begrenzt fortschreiben.
- [x] 032. Pantry-Auditkatalogkompatibilität eng begrenzt fortschreiben.
- [x] 033. Mealplan-Policykompatibilität eng begrenzt fortschreiben.
- [x] 034. Chores-Katalog-/Readinessprüfungen und aktuelle Datenbankbereitschaft ergänzen.
- [x] 035. Neue Migrationspins ergänzen; alle 92 Vorgängerbytes erhalten.

### Phase 2b – gesamte Fachlogik implementieren

- [x] 036. Begrenzte normalisierte Regeln und Validierung implementieren.
- [x] 037. Tagesintervalle aus lokalem Kalenderanker berechnen.
- [x] 038. Wochentage und Wochenabstände berechnen.
- [x] 039. Monatsabstände ohne Ankerdrift berechnen.
- [x] 040. Saisonmonate und endliche Vorschaufenster berücksichtigen.
- [x] 041. Deterministische DST-Lücken-/Doppelzeitauflösung implementieren.
- [x] 042. Echte Erledigungsanker und Auslass-Semantik implementieren.
- [x] 043. Authentisierte, konsistente Lesertransaktion implementieren.
- [x] 044. Versions-/Generationsprüfung und wiederholbare Schreibtransaktion implementieren.
- [x] 045. Bereichserstellung, Änderung und Archivierung implementieren.
- [x] 046. Vollständige Serienvorschau und neue Vorlagenstände implementieren.
- [x] 047. Begrenzte Abhängigkeits-/Zyklusprüfung implementieren.
- [x] 048. Rollierenden Horizont mit eindeutigen Vorkommen erzeugen.
- [x] 049. Geeignete Personen, Rotation und Erklärungen berechnen.
- [x] 050. Abwesenheit und ausdrücklich gewählte Vertretung berücksichtigen.
- [x] 051. Persönliche fällige/übernommene Aufgaben paginiert lesen.
- [x] 052. Übernehmen und Freigeben atomar implementieren.
- [x] 053. Arbeitsdetails, Material und Voraussetzungen lesen.
- [x] 054. Checklistenänderungen mit aktuellem Versionsstand implementieren.
- [x] 055. Genau einen Abschlussfakt inklusive tatsächlicher Dauer speichern.
- [x] 056. Folgeplanung und Abschluss in derselben Transaktion verbinden.
- [x] 057. Bestehende Abschluss- und Wartungspfade sicher integrieren.
- [x] 058. Verschieben, Auslassen, Pause und Fortsetzen getrennt implementieren.
- [x] 059. Einzeltermin-/Zukunftsänderung und Absagen implementieren.
- [x] 060. Deterministischen Tagesvorschlag mit Aufwandserklärung implementieren.
- [x] 061. Zeit-/Personen-/Bereichshistorie mit unbekannten Werten implementieren.
- [x] 062. Persönliche Erinnerungseinstellungen und Ruhezeiten implementieren.
- [x] 063. Lease-gebundene Erzeugung und deduplizierte Digests implementieren.
- [x] 064. ICS-Download, stabile UID, Sequenz und Absagen implementieren.
- [x] 065. Persönliche Feed-Erzeugung, Rotation, Widerruf und aktuelle Prüfung implementieren.
- [x] 066. Deaktivierung, Purge, Suche und Core-Verknüpfungen vollständig integrieren.

### Phase 2c – gesamte API und Oberfläche implementieren

- [x] 067. Begrenzte, typisierte Request-/Response-Modelle ergänzen.
- [x] 068. Rollen-/CSRF-/Versionsfehler konsistent abbilden.
- [x] 069. Arbeitsbereich und Modulnavigation ergänzen.
- [x] 070. Persönliche Heute-Ansicht ergänzen.
- [x] 071. Haushaltsansicht, Bereiche und Zählungen ergänzen.
- [x] 072. Filter, Cursor und aktuelle Auswahl erhalten.
- [x] 073. Serienliste mit unbekanntem alten Prüfbedarf ergänzen.
- [x] 074. Vollständigen Serieneditor ergänzen.
- [x] 075. Regel-/DST-/Folgetermin-Vorschau anzeigen.
- [x] 076. Rotations-/Eignungs-/Vertretungsbedienung ergänzen.
- [x] 077. Checklisten-/Voraussetzungseditor ergänzen.
- [x] 078. Fokussierte Arbeitsansicht ergänzen.
- [x] 079. Pflichtpunkte und Blockierungsgründe anzeigen.
- [x] 080. Optionalen tatsächlichen Aufwand und Abschlussbestätigung ergänzen.
- [x] 081. Verschiebe-/Auslassdialoge mit Vorschau ergänzen.
- [x] 082. Serienpause/-fortsetzung mit vorhandenen Terminen erklären.
- [x] 083. Einzeltermin-/Zukunftsänderung getrennt bedienen.
- [x] 084. Tagesbudget und begründeten Vorschlag anzeigen.
- [x] 085. Abwesenheiten und persönliche Einstellungen bedienen.
- [x] 086. Historie und nachvollziehbare ursprüngliche Vorlagen anzeigen.
- [x] 087. ICS-Download und einmalige Feed-Linkanzeige bedienen.
- [x] 088. Reader-, Leer-, Lade-, Konflikt- und Offlinezustände ergänzen.
- [x] 089. Mobile Struktur, Fokus, Tastatur und Hell-/Dunkelzustände vollständig gestalten.
- [x] 090. Alle statischen Ressourcen lokal einbetten und sensible Linkprotokollierung verhindern.

### Phase 2d – Implementierung abschließen, Prüfquellen vorbereiten

- [x] 091. Englischen API-/Betriebsvertrag und Konfigurationsgrenzen dokumentieren.
- [x] 092. README, Modulverfügbarkeit und Aufgabenliste aktualisieren.
- [x] 093. Reine Regel-/Planungs-Unit-Testquellen vorbereiten.
- [x] 094. Native Rollen-/Race-/Upgrade-/Purge-Testquellen vorbereiten.
- [x] 095. Echte Browserabläufe, Accessibility und Bildnachweise vorbereiten.
- [x] 096. Gesamte Implementierung auf Vollständigkeit prüfen; erst danach Testphase öffnen.

### Phase 3 – erst nach kompletter Implementierung gemeinsam testen und beheben

- [x] 097. Tatsächliches Quellenmanifest und Testinventar einfrieren.
- [x] 098. Alle reinen Regeln, Monatsanker und DST-Fälle ausführen.
- [x] 099. Neuinstallation und befülltes Chores-1/Core-31-Upgrade ausführen.
- [x] 100. Readiness, Audit, Tabellen-/Funktionsdrift und unveränderte Vorgänger prüfen.
- [x] 101. Heute, Bereiche, Arbeitsansicht und Historie nativ prüfen.
- [x] 102. Kalenderregeln, Nachholen, Pause, Auslassen und Serienwechsel nativ prüfen.
- [x] 103. Rotation, Inaktivierung, Abwesenheit und Vertretung nativ prüfen.
- [x] 104. Pflichtchecklisten, Zyklen und fremde Voraussetzungen nativ prüfen.
- [x] 105. Echte gleichzeitige Abschlüsse, sichere Wiederholung und Rollback prüfen.
- [x] 106. Reader, fremder Haushalt, Sessionentzug und Modulbarrieren prüfen.
- [x] 107. Leaseverlust, Digest-Deduplikation und Ruhezeiten prüfen.
- [x] 108. Tatsächlichen ICS-Export, UID/Absagen und Feed-Widerruf prüfen.
- [x] 109. Vollständige Go-/Linux-Race-Regression mit ehrlicher Quellenbindung ausführen.
- [x] 110. Formatierung, vet, Linter, Gosec, govulncheck und Abhängigkeiten ausführen.
- [x] 111. Node-, Workflow-, Migrationspin- und Checklistenprüfungen ausführen.
- [x] 112. Desktop-/Mobilabläufe mit echten APIs im Browser ausführen.
- [x] 113. Tastatur, Fokus, Axe und tatsächliche Screenshots prüfen.
- [x] 114. Alle festgestellten Modulfehler systematisch beheben und betroffene Nachweise erneuern.
- [x] 115. Aktuelle non-root Docker-/OCR-/Backup-Images und lokale OCI-Evidenz prüfen.
- [x] 116. Echten verschlüsselten Restore und restaurierten Chores-Purge ausführen.
- [x] 117. Vollständige bestehende Modul-Browserregression ausführen.
- [ ] 118. Abschließenden exakten Index-/Geheimnis-/SQL-Rohbyte-Scan ausführen.

### Phase 4 – erst nach bestandenen gemeinsamen Gates abhaken

- [ ] 119. Alle 20 CH-Kriterien mit tatsächlichen Nachweisen verifizieren und lokal checkpointen.
- [ ] 120. Aufgabenliste aktualisieren und erst danach den Zählermodulblock beginnen.

## Ausführungsstand

117 von 120 Unterpunkten sind erledigt; offen bleiben 118–120. Die Haken dokumentieren ausgeführte Arbeitsschritte mit ihren jeweiligen Quellen. Unterpunkt114 bleibt bis zu den erforderlichen Wiederholungen auf der korrigierten Quelle offen; die20CH-Abnahmekriterien werden erst gemeinsam freigegeben.

Aktuelle gemeinsame Quelle8b31a1ef (712Dateien): alle zwölf Qualitätsgates,318gewöhnliche Go-Haupttests/1.674Terminals,297Node-Tests, drei non-root Images und lokale OCI-/SBOM-/Provenienzprüfung bestanden. Aktuelle native Modulwiederholung9682d89b:20Haupttests/28Terminals bestanden, einschließlich Rezept-Upgrade mit unverändertem20Sekunden-Budget und Fahrzeugwartung. Separate aktuelle Prüfung01b56fd7: vollständiger AuditTrail, tatsächlicher Quellenexport mit Zukunfts-/Dirty-Sperre und Quellen-/Lizenzzugriff für Reader bestanden. Alle169Produktionsdateien unter Web sind bytegleich mit der tatsächlich anhand von16Bildern manuell geprüften Quelle.

Die neue vollständige746er Linux-Race-Regression b0081891 läuft mit drei unabhängigen Workern über alle sechs unveränderten strikten Shards. Der vorherige fehlerhafte Lauf27d309a3 ist nach618bestandenen/vier fehlgeschlagenen Haupttests kontrolliert beendet; unveränderte Quellen und19entfernte eigene Ressourcen sind verifiziert, private Belege bleiben geschützt erhalten. Die Befunde betreffen die alte Audit-Zahl92statt106, ein20Sekunden-Migrationsbudget unter Sechsfachlast und die zweite echte Pantry-Versionsgrenze im Quellenexport, die auch Reader betraf. Produktionsgrenze und Audit-Erwartung sind korrigiert; kein Zeitbudget wurde erhöht.

Im aktuellen Lauf erreichen zwei Web-Pakete ihr unverändertes60Minuten-Gesamtbudget, ohne fehlgeschlagene fachliche Assertion. Alle18dadurch unvollständigen Testidentitäten sind anschließend auf identischen Quellen vollständig unter Linux-Race nachgeprüft:18Haupttests/50Terminals bestanden, keine Fehler/Skips/Race-Warnungen, keine Overlays, keine erhöhten Budgets und verifizierte eigene Bereinigung. Die ursprünglichen Paket-Timeouts bleiben Fehler. Erst nach den übrigen vier vollständig erfolgreichen Shards und verifizierter Eltern-Bereinigung darf die strikte Gesamtauswertung746eindeutige vollständige Testbäume freigeben; ein einzelner komplett bestandener746er Versuch wird nicht behauptet.

Der aktuelle Standalone-Browser848d9184 besteht74Prüfungen/16Screenshots/16saubereAxe-Scans. Echter aktueller Restore75470633 besteht15Stufen und12verifizierte Bereinigungen mit ausdrücklich leerem Uploadbestand. Aktuelle32er Browserkettebc6cd1ef besteht1.438Prüfungen/195Screenshots/183saubereAxe-Scans; Quellen und eigene Bereinigung sind verifiziert. Ein vorheriger Finanz-Browsertest hing nach erfolgreicher Buchung in unbegrenztem Transportwarten; ausschließlich der Prüfer wurde auf begrenzte sichtbare Bestätigung plus echten gleichschlüsseligen Dauerbeleg gehärtet. Separate298er Finanzprüfung und neueGesamtkette bestehen; Go-/Produktionsquellen bleiben unverändert. Frühere erfolgreiche AFD-Nachweise und alle Fehlversuche bleiben historisch erhalten. Census-only-Qualifikations-/Übertragungswerkzeuge wurden nur vorbereitet und wegen der realen Produktionsänderung nie ausgeführt.

Die Hauptliste bleibt 120 verifiziert/20 in Arbeit/40 offen. Details: docs/chores-module-validation.md; Kriterienzuordnung: docs/chores-acceptance-map.md. Finanzvorgänger66e63c5, Live8100, Geschwisterprojekte und Veröffentlichung bleiben unverändert.

Aktueller Abschlussstand: Der ursprüngliche Laufb0081891 ist nach726vollständigen Haupttest-Erfolgen beendet. Drei Web-Pakete überschreiten das unveränderte60Minuten-Budget; alle13eigenen Laufzeitressourcen sind nachweislich entfernt. Keine Testassertion ist fehlgeschlagen/übersprungen und kein Data Race beobachtet.18der20unvollständigen Identitäten sind vollständig erfolgreich wiederholt; die letzten zwei laufen auf identischen Quellen. Die ursprünglichen Versuche bleiben fehlgeschlagen. Gemeinsame Qualifikation, Indexscan und Abnahme sind noch offen.


### Tatsächlich abgeschlossene Laufzeitprüfungen

Alle746Testidentitäten sind vollständig auf identischen Quellen bestanden:726im ursprünglichen Versuch sowie18und2in vollständigen Wiederholungen. Die drei ursprünglichen Paket-Timeouts bleiben fehlgeschlagen. Ausschließlich vollständige erfolgreiche Coverage-Profile ergeben56,86%beobachtete Statement-Abdeckung. Alle zwölf gemeinsamen Laufzeitgates sowie die Nachweiszuordnung aller20CH-Kriterien sind geprüft; exakter Indexscan, lokaler Checkpoint und Statusfreigabe bleiben offen.
