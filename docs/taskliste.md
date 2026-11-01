# Household – Aufgabenliste

Stand: 01.11.2026, 07:10 (Europe/Vienna).

**101 / 180 verifiziert und abgehakt · 19 in Arbeit · 60 offen.**

Ein Haken bedeutet: umgesetzt und durch ausgeführte Prüfungen belegt. Vorbereitete Änderungen und einzelne bestandene Zwischentests schließen einen Verbesserungspunkt noch nicht ab.

Aktuell in Arbeit: **EX01 – Monatsübersicht**, **EX03 – Buchungszustände**, **EX04 – Gespeicherte Finanzfilter**, **EX05 – Belegprüfung**, **EX06 – Konten und Einnahmen**, **EX07 – Kategoriebudgets**, **EX08 – Sparziele**, **EX09 – Personenaufteilung**, **EX10 – Ausgleichszahlungen**, **EX11 – Wiederkehrende Buchungen**, **EX12 – Kategorisierungsregeln**, **EX13 – Bankdateiimport**, **EX14 – Belegsumme prüfen**, **EX15 – Liquiditätsvorschau**, **EX16 – Finanzberichte**, **EX17 – Kontoabstimmung**, **EX18 – Mehrere Währungen**, **EX19 – Finanzsichtbarkeit**, **EX20 – Buchungsherkunft**.

**Essensplanung vollständig lokal abgenommen: 20 / 20 Punkte.** Geplante Reste, Wochenbudget, Kalender und Autoplan sind nach nativen Tests, Browserprüfung und verschlüsseltem Restore/Purge gemeinsam abgehakt. [Modulplan](mealplan-module-plan.md) · [Abnahmenachweise und Grenzen](mealplan-module-validation.md).

**Vorrat vollständig lokal abgenommen: 20 / 20 Punkte.** PA10, PA13 und PA19 sind nach nativen Tests, eigenständigem Browserlauf, echtem Restore/Purge und vollständiger 28-Komponenten-Regression abgehakt. [Modulplan](pantry-module-plan.md) · [Abnahmenachweise](pantry-module-validation.md).

**Rezepte vollständig lokal abgenommen: 20 / 20 Punkte.** Alle zehn Erweiterungen sind nach nativer Komposition, Linux-Race, eigener und vollständiger Browserprüfung sowie echtem Restore/Purge gemeinsam abgehakt. [Modulplan](recipes-module-plan.md) · [Abnahmenachweise und Grenzen](recipes-module-validation.md).

**Aktueller Modulblock: Finanzen (19 Erweiterungen in Arbeit, EX02 bleibt erhalten).**

- [x] Finaler Stand a6e871cc: zwölf Qualitätsprüfungen, 307 Go-Unit-Haupttests und 297 Node-Tests bestanden; Produktionscode unverändert.
- [x] Geprüfte Datenbankkorrekturen gemeinsam übertragen; Finanzquellen, echte Zeilenlöschung, Pantry14 und sichere Auditprojektion nachgeprüft.
- [x] Aktuelle Runtime-, OCR- und Backup-Images auf 515c3433 als nicht privilegierter Benutzer gebaut.
- [x] Aktuelle lokale OCI-/SBOM-/Provenienzprüfung auf 515c3433 bestanden.
- [x] Finanz-Browser auf 515c3433: 208 Prüfpunkte, 16 manuell geprüfte Screenshots und 16 saubere Axe-Scans.
- [x] Verschlüsselter Restore/Purge auf 515c3433: 15 tatsächliche Stufen und vollständig verifizierte Bereinigung.
- [x] Historische Fuel-Testvorbereitung korrigiert; alle vier beschädigten Altbestände getrennt bestanden.
- [x] Git-sichtbaren Stand 515c3433 mit 892 Dateien gescannt: keine Funde; private Prüfkopie entfernt.
- [x] Alle 716 Haupttests tatsächlich ausgeführt: 696 bestanden, 20 fehlgeschlagen, keine übersprungen; Originalbefunde und Bereinigung dokumentiert.
- [x] Vollständige Linux-Race-Regression bestanden: 717 Haupttests, sechs strenge Shards, alle 20 früheren Fehlerfälle erfolgreich; 15 Bereinigungen verifiziert.
- [x] Vollständige Browserkette auf 515c3433: 31 Komponenten, 1.273 Prüfpunkte, 179 Screenshots und 167 saubere Axe-Scans.
- [x] Mobile Korrektur getrennt geprüft: 208 Prüfpunkte, überlappungsfreie Werte und 16 saubere Axe-Scans; verschlüsselter Restore/Purge bestanden.
- [x] Acht korrigierte Finanzverträge und sechs Capture-/Aufräumfälle vollständig nachgeprüft.
- [x] Sparziel-Gegenbuchung separat korrigiert: reguläre Rückbuchung und beschädigter Nachbarbestand beide nachgeprüft.
- [x] Dreizehn geprüfte Korrekturdateien auf Stand 515c3433 übertragen; alle 85 freigegebenen SQL-Dateien unverändert, 92 aktuelle Pins bestanden.
- [x] Liquiditätsvorschau mit zwei zusätzlichen nativen und Linux-Race-Fällen bestanden; keine Fehler/Skips/Races, Produktionscode unverändert.
- [x] Ergänzter Teststand a6e871cc: Formatierung, vet und vollständiger getaggter Linter auf tatsächlicher Quellkopie bestanden.
- [x] Zwei zusätzliche Liquiditätstests quellenqualifiziert übertragen; 719 eindeutige Race-Haupttests belegt, 70,86 % beobachtete Statement-Abdeckung.
- [ ] Abschließende Indexprüfung, gemeinsame Modulabnahme und lokaler Checkpoint.

Fehlversuche bleiben als solche erhalten. Die 19 Finanzverbesserungen werden nach der gemeinsamen Modulabnahme abgehakt. [Modulplan mit Haken](finance-module-plan.md) · [Prüfnachweise und Quellenstände](finance-module-validation.md).

Diese Ansicht wird aus der [verbindlichen Liste mit Abnahmekriterien und Nachweisen](improvements-180.md) erzeugt. Aktualisieren: `node scripts/update-tasklist.cjs`.

| Modul | Abgehakt | In Arbeit | Offen |
|---|---:|---:|---:|
| Einkaufen | 20 / 20 | 0 | 0 |
| Fahrzeuge und Tanken | 20 / 20 | 0 | 0 |
| Rezepte | 20 / 20 | 0 | 0 |
| Vorrat | 20 / 20 | 0 | 0 |
| Finanzen | 1 / 20 | 19 | 0 |
| Aufgaben | 0 / 20 | 0 | 20 |
| Zähler und Energie | 0 / 20 | 0 | 20 |
| Dokumente | 0 / 20 | 0 | 20 |
| Essensplanung | 20 / 20 | 0 | 0 |

## Einkaufen

- [x] **SH01 Schnellerfassung** — Name direkt in der Liste eingeben; optionale Details aufklappen und ohne Seitenwechsel speichern.
- [x] **SH02 Einkaufsmodus** — Offene Artikel gruppiert bedienen, erledigte einklappen und Fortschritt anzeigen.
- [x] **SH03 Geschäftsreihenfolge** — Geschäftsprofile mit eigener Gang- und Gruppenreihenfolge auswählen.
- [x] **SH04 Produktvarianten** — Marke, Packung und Bild auf Einkaufspositionen verständlich darstellen.
- [x] **SH05 Mengenerkennung** — Eingaben wie 2×500 g Nudeln lokal erkennen und vor Speicherung bearbeitbar bestätigen.
- [x] **SH06 Favoriten und Vorlagen** — Favoriten und wiederverwendbare Listen erzeugen unabhängige neue Positionen.
- [x] **SH07 Kontrolliertes Zusammenführen** — Identische Produkte und kompatible Mengen zusammenführen; unklare Varianten zur Prüfung zeigen.
- [x] **SH08 Ersatz und nicht verfügbar** — Verfügbarkeit und Ersatzbezug nachvollziehbar speichern und bearbeiten.
- [x] **SH09 Einkaufszuständigkeit** — Personen zuweisen und zeitlich begrenzten Einkaufsstatus anzeigen.
- [x] **SH10 Warenkorbschätzung** — Datierte Geschäftspreise verwenden; unbekannte Preise getrennt ausweisen.
- [x] **SH11 Einkauf einlagern** — Gekaufte Artikel mit Produkt, Menge, Ort und Datum geprüft in den Vorrat übernehmen.
- [x] **SH12 Belegabgleich** — Abgeschlossenen Einkauf mit Beleg verknüpfen und Plan/Ist-Abweichungen prüfen.
- [x] **SH13 Barcode-Erfassung** — Kamera oder Scanner nutzen; lokale Zuordnung zuerst, externe Suche nur optional.
- [x] **SH14 Nachkaufvorschläge** — Lokale Kaufabstände erklären und Vorschläge manuell übernehmen.
- [x] **SH15 Listenbenachrichtigungen** — Abonnements, gebündelte Hinweise und optional konfigurierten Web Push bereitstellen.
- [x] **SH16 Live-Aktualisierung** — Autorisierte Änderungen ohne Verlust laufender Eingaben und Versionskontrolle nachladen.
- [x] **SH17 Offline-Leselisten** — Ausgewählte Listen ausdrücklich pro Gerät freigeben; Eigentümer, Ablauf und Abmeldung beachten.
- [x] **SH18 Konfliktprüfung** — Eigene und aktuelle Werte verständlich vergleichen und bewusst erneut speichern.
- [x] **SH19 Suchbare Auswahl** — Referenzen jenseits von 100 Einträgen mit Suche und stabiler Cursor-Paginierung auswählen.
- [x] **SH20 Atomarer Abschluss** — Einkauf, Bestand und Belege mit wiederholbarer Operation ohne Doppelbuchung abschließen.

## Fahrzeuge und Tanken

- [x] **FU01 Fahrzeugübersicht** — Letzte Tankung, Kilometerstand, Verbrauch und anstehende Termine je Fahrzeug anzeigen.
- [x] **FU02 Tank-Schnellerfassung** — Persönliche Vorgaben für Fahrzeug, aktive Serie und Station verwenden.
- [x] **FU03 Preisberechnung** — Zwei Werte aus Menge, Preis und Summe exakt zum dritten ergänzen; Rundungsabweichungen zeigen.
- [x] **FU04 Verbrauch erklären** — Fehlende Auswertung mit strukturiertem Grund und nächstem Handlungsschritt erklären.
- [x] **FU05 Verbrauchsdiagramme** — Verbrauch und Kosten filtern; zugängliche Datentabelle ergänzen.
- [x] **FU06 Weitere Fahrzeugkosten** — Wartung, Versicherung und Reifen mit Ausgaben verknüpfen, ohne doppelte Buchung.
- [x] **FU07 Wartungstermine** — Datum oder Kilometerstand lösen nachvollziehbare Fälligkeit aus.
- [x] **FU08 Gesamtkosten pro Kilometer** — Kostenumfang, Zeitraum und verwendete Fahrstrecke offen ausweisen.
- [x] **FU09 Elektro-Ladevorgänge** — kWh, Ladeort, Kosten und optional Ladezustand getrennt vom Fahrverbrauch verwalten.
- [x] **FU10 Fahrtenbuch** — Manuelle Fahrten mit Zweck erfassen und exportieren; keine steuerliche Zertifizierung behaupten.
- [x] **FU11 Tankdatenimport** — Fuelio-, Drivvo- und Spritmonitor-CSV anhand dokumentierter Profile prüfen und deduplizieren.
- [x] **FU12 Tankbeleg-Erkennung** — Datum, Menge und Betrag aus OCR als prüfbare Vorschläge mit Herkunft anzeigen.
- [x] **FU13 Kilometerfoto** — Erkannten Kilometerstand mit vorherigem Stand abgleichen und bestätigen lassen.
- [x] **FU14 Wartung als Aufgabe** — Fällige Wartung über einen deduplizierten Kernvertrag mit Aufgaben verknüpfen.
- [x] **FU15 Fahrzeugdokumente** — Fahrzeug und berechtigte Dokumente über Kernverknüpfungen verbinden.
- [x] **FU16 Große Tankhistorie** — Zeitfenster und Cursor ohne Verlust benötigter Randtankungen über 10.000 Zeilen auswerten.
- [x] **FU17 Verbrauchssegmente** — Abgeleitete Segmente versioniert berechnen und nach relevanten Änderungen invalidieren.
- [x] **FU18 Tachowechsel** — Alte Serie beenden und neue mit Datum und Anfangsstand geführt beginnen.
- [x] **FU19 Kraftstoffeinheiten** — Kraftstoff und Mengenart verbindlich auf Liter, Kilogramm oder kWh abstimmen.
- [x] **FU20 Ausreißerhinweise** — Konfigurierbare Plausibilitätsgrenzen erklären; Originalwerte nicht automatisch ändern.

## Rezepte

- [x] **RE01 Rezepteditor** — Grunddaten, Zutaten und Schritte gemeinsam mit Entwurf und Feldfehlern bearbeiten.
- [x] **RE02 Kochmodus** — Aktuellen Schritt, abgehakte Zutaten, Fortschritt und optional Wake Lock anbieten.
- [x] **RE03 Portionen im Kochmodus** — Mengen exakt skalieren, ohne das Originalrezept zu ändern.
- [x] **RE04 Dauerhafte Küchentimer** — Mehrere beschriftete Timer über Navigation und Neuladen hinweg wiederherstellen.
- [x] **RE05 Rezeptfilter** — Dauer, Tags, Zutaten und letzte Zubereitung kombinieren und Ansichten speichern.
- [x] **RE06 Geprüfter Rezeptimport** — Strukturierte Dateien und Schema.org-Rezepte vor Übernahme prüfen.
- [x] **RE07 Zutatenkatalog** — Aliase normalisieren und Vorratsprodukte ausdrücklich zuordnen.
- [x] **RE08 Versionen und Varianten** — Rezeptstände und abgeleitete Varianten unabhängig vom Audit erhalten.
- [x] **RE09 Zutatenersatz** — Austauschregeln mit Mengenhinweisen anzeigen und manuell wählen.
- [x] **RE10 Kochhistorie** — Tatsächliche Portionen, Bewertung und Notizen einer Zubereitung speichern.
- [x] **RE11 Vorratsabgleich** — Verfügbare Mengen, Fehlmengen und unbekannte Zuordnungen getrennt anzeigen.
- [x] **RE12 Vorrat nach Kochen** — Verbrauch mit Mengen und Chargenauswahl prüfen und atomar buchen.
- [x] **RE13 Portionskosten** — Geschätzte Kosten mit Preisalter, Quellen und fehlenden Preisen anzeigen.
- [x] **RE14 Nährwerte und Allergene** — Strukturierte Angaben mit Quelle und Vollständigkeit; unbekannt bleibt unbekannt.
- [x] **RE15 Portabler Austausch** — Household-JSON und ausgewählte Tandoor-, Mealie- und Paprika-Profile dokumentiert unterstützen.
- [x] **RE16 Gebündelte Rezeptdaten** — Details mit begrenzten Abfragen statt einer Abfrage pro Zutat laden.
- [x] **RE17 Offline-Rezepte** — Ausgewählte Rezepte mit begrenzten Bildern ausdrücklich lokal speichern und löschen.
- [x] **RE18 Kücheneinheiten** — TL/EL umrechnen; Masse/Volumen nur mit bekannter zutatenbezogener Dichte.
- [x] **RE19 Sicherer URL-Import** — SSRF, Weiterleitungen, Größen und HTML begrenzen; Importarbeit isolieren.
- [x] **RE20 Rezept-Dubletten** — URL und Inhaltsfingerabdruck vergleichen; Zusammenführung immer prüfen lassen.

## Vorrat

- [x] **PA01 Produktübersicht** — Gesamtbestand, Orte und nächstes Datum anzeigen; Chargen aufklappen.
- [x] **PA02 Zuerst verbrauchen** — Datumsart, Öffnung und Restmenge für erklärbare Priorisierung verwenden.
- [x] **PA03 Schnellverbrauch** — Packung, Hälfte oder eigene Menge buchen; Rücknahme als Gegenbewegung.
- [x] **PA04 Einlagerungsassistent** — Mehrere Produkte mit Menge, Ort und Datum erfassen und Fortschritt bewahren.
- [x] **PA05 Inventur** — Zählbestand mit Buchbestand je Ort vergleichen und Differenzen begründet buchen.
- [x] **PA06 Packungsgrößen** — Packung und Basiseinheit getrennt speichern und exakt umrechnen.
- [x] **PA07 Mehrere Barcodes** — Produkt- und Packungsvarianten mehrere eindeutige Barcodes zuordnen.
- [x] **PA08 Geöffnete Haltbarkeit** — Nutzer- oder Herstellerangaben mit Herkunft als Hinweis anzeigen.
- [x] **PA09 FEFO-Vorschlag** — Passende Chargen nach Datum vorschlagen; manuelle Auswahl und Datumsarten erhalten.
- [x] **PA10 Gekochte Reste** — Rezeptbezug, Portionen, Zubereitung und Lagerort erfassen.
- [x] **PA11 Zielbestand** — Mindest- und Zielbestand unterscheiden; Nachkauf auf Packungen aufrunden.
- [x] **PA12 Reservierungen** — Freien und für Mahlzeiten reservierten Bestand getrennt ausweisen.
- [x] **PA13 Open Food Facts** — Optionalen Adapter mit Quellenangabe und lokalem Cache bereitstellen.
- [x] **PA14 Abfallanalyse** — Gründe und optional geschätzte Kosten der Verluste auswerten.
- [x] **PA15 Rezeptvorschläge** — Vorhandene und bald fällige Zutaten transparent gewichtet berücksichtigen.
- [x] **PA16 Große Bestandslisten** — Gefilterte Paginierung und exakte Gesamtsummen ohne 100-Produkte-Grenze anbieten.
- [x] **PA17 Bestandsprojektion** — Transaktionale Salden pflegen und gegen das Bewegungsjournal prüfen.
- [x] **PA18 Verbrauchskonflikte** — Verbleibende Menge nach gleichzeitiger Buchung zeigen; kein blindes Wiederholen.
- [x] **PA19 Offline-Scans** — Scans als lokale Entwürfe prüfen; Offline-Daten sind keine Bestandsautorität.
- [x] **PA20 Historischer Bestand** — Bestand zum Stichtag aus Bewegungen rekonstruieren und Korrekturzeitpunkte zeigen.

## Finanzen

- [ ] **EX01 Monatsübersicht** — **In Arbeit** — Gebuchte Beträge, Entwürfe und Hauptkategorien getrennt darstellen.
- [x] **EX02 Aufgeteilte Erfassung** — Buchung und Kategoriezeilen gemeinsam bearbeiten; Restdifferenz sichtbar halten.
- [ ] **EX03 Buchungszustände** — **In Arbeit** — Entwurf, gebucht, erstattet und korrigiert mit passenden Aktionen erklären.
- [ ] **EX04 Gespeicherte Finanzfilter** — **In Arbeit** — Person, Kategorie, Empfänger, Zeitraum und Status kombinieren.
- [ ] **EX05 Belegprüfung** — **In Arbeit** — Beleg und Buchung nebeneinander beziehungsweise mobil mit erhaltenem Formularzustand zeigen.
- [ ] **EX06 Konten und Einnahmen** — **In Arbeit** — Konten, Einnahmen und Umbuchungen modellieren; Transfers nicht als Verbrauch zählen.
- [ ] **EX07 Kategoriebudgets** — **In Arbeit** — Monatsbudgets und ausdrücklich gewählte Übertragsregeln auswerten.
- [ ] **EX08 Sparziele** — **In Arbeit** — Zielbetrag, Termin und geplante Beiträge von tatsächlichem Kontostand unterscheiden.
- [ ] **EX09 Personenaufteilung** — **In Arbeit** — Gleich, Prozent, Anteile oder Festbetrag mit deterministischer Centverteilung aufteilen.
- [ ] **EX10 Ausgleichszahlungen** — **In Arbeit** — Personensalden und Zahlungen getrennt von ursprünglichen Ausgaben führen.
- [ ] **EX11 Wiederkehrende Buchungen** — **In Arbeit** — Vorlagen erzeugen deduplizierte, prüfbare Entwürfe.
- [ ] **EX12 Kategorisierungsregeln** — **In Arbeit** — Prioritäten und zutreffende Empfängerregeln vor Übernahme erklären.
- [ ] **EX13 Bankdateiimport** — **In Arbeit** — Dokumentierte CSV-Profile, Kontoauswahl, Vorschau und Dublettenkontrolle anbieten.
- [ ] **EX14 Belegsumme prüfen** — **In Arbeit** — Positionen und Gesamtsumme mit OCR-Herkunft und Abweichungen vergleichen.
- [ ] **EX15 Liquiditätsvorschau** — **In Arbeit** — Bestätigte Fälligkeiten und Wiederholungen mit Unsicherheit projizieren.
- [ ] **EX16 Finanzberichte** — **In Arbeit** — Konten-, Personen-, Kategorien- und Zeitaggregate serverseitig nachvollziehbar berechnen.
- [ ] **EX17 Kontoabstimmung** — **In Arbeit** — Externen Stichtagssaldo vergleichen; keine stillen Ausgleichsbuchungen.
- [ ] **EX18 Mehrere Währungen** — **In Arbeit** — Originalbetrag, Basisbetrag, Kurs, Datum und Rundungsregel unveränderlich nachvollziehen.
- [ ] **EX19 Finanzsichtbarkeit** — **In Arbeit** — Konten- und Datensatzrechte in Suche, Aggregaten, Dateien, Export und Audit anwenden.
- [ ] **EX20 Buchungsherkunft** — **In Arbeit** — Manuell, Bank, Tankung und Beleg über gemeinsame Herkunft und Dublettenschutz verbinden.

## Aufgaben

- [ ] **CH01 Heute für mich** — Zugewiesene, fällige und übernommene Aufgaben persönlich bündeln.
- [ ] **CH02 Räume und Bereiche** — Aufgaben nach Bereichen mit Anzahl und Fälligkeit ordnen.
- [ ] **CH03 Wiederholung erklären** — Letzte Erledigung und Berechnung der nächsten Fälligkeit sichtbar machen.
- [ ] **CH04 Arbeitsansicht** — Anleitung, Material, Unteraufgaben und Abschluss konzentriert bedienen.
- [ ] **CH05 Verschieben und Pausieren** — Termin verschieben, Vorkommen auslassen und Serie pausieren unterscheiden und vorab zeigen.
- [ ] **CH06 Kalenderwiederholungen** — Wochentage, Monate und Saison mit begrenzten Regeln und Vorschau unterstützen.
- [ ] **CH07 Wiederholungsanker** — Festen Zeitplan oder letzte Erledigung als ausdrücklichen Anker verwenden.
- [ ] **CH08 Zuständigkeitsrotation** — Rotation, Ausnahmen und Vertretung nachvollziehbar anwenden.
- [ ] **CH09 Aufwand und Eignung** — Optionale Dauer, Schwierigkeit und geeignete Personen pflegen.
- [ ] **CH10 Unteraufgaben und Abhängigkeiten** — Abhängigkeiten ohne Zyklen validieren und Abschlussbedingungen zeigen.
- [ ] **CH11 Tagesplanung** — Zeitbudget und Priorität für einen erklärbaren Tagesvorschlag verwenden.
- [ ] **CH12 Abwesenheiten** — Personenbezogene Pausen und Ersatzregeln in Zuweisungen beachten.
- [ ] **CH13 Modulereignisse** — Aufgaben aus Kernereignissen mit deduplizierter Herkunft erstellen.
- [ ] **CH14 Aufgaben-Kalender** — ICS-Export und widerrufbare Feeds mit stabilen Ereigniskennungen anbieten.
- [ ] **CH15 Erinnerungsbündel** — Ruhezeiten, Zusammenfassungen und ausdrücklich gewählte Eskalation berücksichtigen.
- [ ] **CH16 Vorkommenshorizont** — Begrenzten rollierenden Horizont mit eindeutigen Vorkommenskennungen erzeugen.
- [ ] **CH17 Erledigungshistorie** — Aufwand, Häufigkeit und tatsächliche Person ohne erzwungene Rangliste zeigen.
- [ ] **CH18 Paralleler Abschluss** — Gleichzeitige Erledigung genau einmal buchen und Ergebnis verständlich zurückgeben.
- [ ] **CH19 Serienänderungen** — Einzeltermin und zukünftige Serie unterscheiden; historische Vorlage erhalten.
- [ ] **CH20 Zeitzonen und Sommerzeit** — Lokale Wiederholung über Zeitumstellungen hinweg deterministisch berechnen.

## Zähler und Energie

- [ ] **ME01 Zählerübersicht** — Letzten Stand, Datum, Verbrauch und überfällige Ablesung darstellen.
- [ ] **ME02 Mobile Ablesung** — Aktives Gerät, letzten Stand und Foto in einem geführten Ablauf anbieten.
- [ ] **ME03 Intervallverbrauch** — Verbrauch und Tagesdurchschnitt mit genauer Zeitspanne erklären.
- [ ] **ME04 Messqualität** — Messung, Schätzung und Datenlücken in Diagramm und Tabelle unterscheiden.
- [ ] **ME05 Zählerwechsel** — Alten Endstand und neuen Anfangsstand mit Datum und möglicher Lücke erfassen.
- [ ] **ME06 Zeitabhängige Tarife** — Arbeits- und Grundpreis mit Gültigkeitsintervallen versionieren.
- [ ] **ME07 Kosten und Abschläge** — Geschätzte Kosten mit tatsächlichen Abschlägen vergleichen und kennzeichnen.
- [ ] **ME08 Periodenvergleich** — Jahre und Zeiträume nur mit sichtbarer Datenabdeckung vergleichen.
- [ ] **ME09 Mehrere Register** — Bezug, Einspeisung und Erzeugung mit Einheiten und Flussrichtung trennen.
- [ ] **ME10 Ableseplan** — Fällige Ablesungen nach tatsächlichem passenden Messwert erledigen.
- [ ] **ME11 Home Assistant** — Optionalen Adapter mit ausdrücklicher Entitäts-, Einheiten- und Typzuordnung bereitstellen.
- [ ] **ME12 MQTT** — Optionale begrenzte Topics, validierte Nachrichten, Authentifizierung und Dublettenschutz.
- [ ] **ME13 Versorgerimport** — CSV mit Zeitzone, Einheit, Register und geprüfter Vorschau importieren.
- [ ] **ME14 Anomaliehinweise** — Konfigurierbare Verbrauchs- und Grundlasthinweise erst bei ausreichender Historie zeigen.
- [ ] **ME15 Rechnungen als Ausgabe** — Nur bestätigte Rechnungen oder Abschläge in Finanzen übernehmen.
- [ ] **ME16 Ablesung und Telemetrie** — Manuelle Stände von hochfrequenten Messdaten strukturell trennen.
- [ ] **ME17 Zeitaggregate** — Stunden-, Tages- und Monatswerte samt Rohdatenaufbewahrung verwalten.
- [ ] **ME18 Korrektur-Neuberechnung** — Benachbarte Intervalle und Kosten nach Änderung gezielt invalidieren.
- [ ] **ME19 Messgrößentypen** — Kumulativ, Intervall und Leistung strikt unterscheiden; kW ist nicht kWh.
- [ ] **ME20 Große Messreihen** — Zeitgefilterte, aggregierte Auswertungen jenseits von 10.000 Rohwerten liefern.

## Dokumente

- [ ] **DO01 Dokumenteneingang** — Neue, ungeprüfte und fehlgeschlagene Dokumente mit nächstem Schritt bündeln.
- [ ] **DO02 Dokumentenprüfung** — Vorschau und Metadaten gemeinsam mit mobil erhaltenem Bearbeitungsstand zeigen.
- [ ] **DO03 Gespeicherte Dokumentansichten** — Rechnungen, Garantien und Fristen über kombinierte Filter anbieten.
- [ ] **DO04 Fristenagenda** — Fristen anzeigen und zugehörige Aufgaben über Kernverträge erstellen oder bearbeiten.
- [ ] **DO05 Stapelbearbeitung** — Ausgewählte Metadaten begrenzt, geprüft und transaktional ändern.
- [ ] **DO06 Dokumenttypen** — Typisierte, validierte Zusatzfelder pro Dokumenttyp unterstützen.
- [ ] **DO07 Dateiversionen** — Originale unverändert erhalten und aktuelle Version ausdrücklich festlegen.
- [ ] **DO08 Mehrseitige Scans** — Seitenreihenfolge, Zusammenführen und Aufteilen vorab zeigen; Originale erhalten.
- [ ] **DO09 Fristarten** — Rückgabe, Garantie und weitere Fristen mit Nutzerbestätigung und Quelle unterscheiden.
- [ ] **DO10 Objektverknüpfungen** — Fahrzeuge, Geräte und Buchungen über berechtigte Kernverträge verknüpfen.
- [ ] **DO11 PDF-Erfassung** — Vorhandenen PDF-Text nutzen; lokale OCR begrenzt im Hintergrund ausführen.
- [ ] **DO12 Klassifikationsregeln** — Regelwirkung vor Übernahme erklären und manuelle Korrektur bewahren.
- [ ] **DO13 Scannerordner** — Optionalen überwachten Eingang atomar, dedupliziert und wiederholbar verarbeiten.
- [ ] **DO14 IMAP-Eingang** — Optional eingegrenzte Anhänge prüfen; keine ungefragte Mail-Löschung.
- [ ] **DO15 Paperless-Anbindung** — Optionalen Adapter mit dokumentierter Datenhoheit und Synchronisationsumfang anbieten.
- [ ] **DO16 Verarbeitungsstatus** — Fortschritt, Abbruch und Wiederholung über bestehende Jobverträge bereitstellen.
- [ ] **DO17 Dokumentvolltext** — Autorisierte PostgreSQL-Suche mit geschützten OCR-Textausschnitten anbieten.
- [ ] **DO18 Dokumentdubletten** — Identische und wahrscheinliche Dubletten anzeigen; nie automatisch löschen.
- [ ] **DO19 Dokumentrechte** — Metadaten, Dateien, Volltext, Export und Verknüpfungen konsistent autorisieren.
- [ ] **DO20 Portables Archiv** — Originale, Metadaten und Links exportieren; Aufbewahrung, Audit und Backups erklären.

## Essensplanung

- [x] **MP01 Wochenansicht** — Desktop-Wochenraster und mobile Tagesagenda aus denselben Daten darstellen.
- [x] **MP02 Mahlzeiten bewegen** — Verschieben und Tauschen per Tastatur vollständig, Drag-and-drop ergänzend anbieten.
- [x] **MP03 Rezept im Zeitfenster** — Rezept mit Portionen und Aufwand direkt im Slot auswählen.
- [x] **MP04 Mahlzeitentypen** — Reste, auswärts, Bestellung und freie Planung ohne falschen Einkaufsbedarf unterscheiden.
- [x] **MP05 Planungsstatus** — Offen, geplant, eingekauft, gekocht und abgesagt nachvollziehbar verwalten.
- [x] **MP06 Teilnehmer und Gäste** — Portionen ableiten und manuell überschreibbar halten.
- [x] **MP07 Wochenvorlagen** — Neue Wochen ohne alte Erledigungshistorie oder falsche Links aus Vorlage erzeugen.
- [x] **MP08 Geplante Reste** — Erwartete Restportionen mit Folgemahlzeit verknüpfen und vom realen Bestand unterscheiden.
- [x] **MP09 Mehrere Komponenten** — Beilagen und Hauptgerichte mit eigenen Portionen und gemeinsamer Bedarfsvorschau planen.
- [x] **MP10 Zeitbudget** — Vorbereitung und Kochen pro Tag berechnen; Parallelität nur ausdrücklich annehmen.
- [x] **MP11 Vorratsbasierte Vorschläge** — Verfügbarkeit anhand ausdrücklicher Produktzuordnungen bewerten.
- [x] **MP12 Verfallsorientierte Planung** — Geöffnete und bald fällige Produkte mit erklärbarem Grund priorisieren.
- [x] **MP13 Wochenbudget** — Geschätzte Kosten und Alternativen mit fehlenden Preisen anzeigen.
- [x] **MP14 Differenzieller Bedarf** — Freien, reservierten und bereits vorgemerkten Bestand korrekt vom Bedarf abziehen.
- [x] **MP15 Essens-Kalender** — ICS mit stabilen Kennungen, Aktualisierung und widerrufbaren Feeds anbieten.
- [x] **MP16 Rezeptstand im Plan** — Verwendete Rezeptversion erhalten und Änderungen zur bewussten Übernahme anzeigen.
- [x] **MP17 Wiederholbare Einkaufsübernahme** — Bedarf über Plan-, Komponenten- und Zutatenidentität aktualisieren statt duplizieren.
- [x] **MP18 Reservierungen freigeben** — Bei Verschieben, Absage und Ablauf versionsgeprüft freigeben.
- [x] **MP19 Regelbasierter Autoplan** — Gewichtete Kriterien erklären und jede Mahlzeit austauschbar lassen; kein KI-Zwang.
- [x] **MP20 Gebündelte Wochendaten** — Autorisierte Wochenansicht mit Versionen, verzögerten Bildern und gezielten Updates laden.
