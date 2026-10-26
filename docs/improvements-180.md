# Checkliste: 180 genehmigte Verbesserungen

Stand: 2026-10-26. Auftrag: alle 180 Vorschläge aus dem Modulvergleich implementieren und gründlich testen.

## Status und Abnahme

- **Offen**: noch nicht vollständig umgesetzt.
- **In Arbeit**: Umsetzung oder zugehörige Prüfung läuft.
- **Implementiert**: Verhalten vorhanden; erforderliche Verifikation noch offen.
- **Verifiziert**: Abnahmekriterium mit benannten, ausgeführten Prüfungen belegt.
- **Extern offen**: funktionsfähige optionale Integration vorhanden; reale Gegenstelle beziehungsweise Zugang fehlt. Dies zählt nicht als vollständige Betriebsabnahme.

Ein Schema, eine Schaltfläche oder ein Mock allein schließt keinen Punkt ab. Bestehende Teilfunktionen bleiben erhalten; ihr Vorhandensein ersetzt die neue Abnahme nicht. Tests früherer Stände gelten nicht für neue Änderungen. Jeder Punkt behält seine feste Kennung.

## Reihenfolge

1. Gemeinsame Auswahl-, Konflikt-, Status- und Formularabläufe; Einkaufserfassung und Einkaufsmodus.
2. Produktidentität, Packungen, sichere Zusammenführung, Einkaufsabschluss und Vorratsjournal.
3. Rezepteditor, Kochmodus, Timer, Rezeptstände und Essensplanung.
4. Reservierungen, differenzieller Bedarf, Vorratshilfen und Vorschläge.
5. Finanzkonten, Personenaufteilung, Budgets, Abstimmung und Importe.
6. Aufgabenwiederholung, Zuständigkeit, Fristen und Kalenderverträge.
7. Dokumenteingang, Verarbeitung, Volltext, Rechte und portable Archive.
8. Fahrzeugkosten, Wartung, Energiepreise, Register und große Auswertungen.
9. Optionale Integrationen, lokale Offline-Lesefunktionen, Benachrichtigungen und regelbasierte Planung.
10. Gemeinsame Regression, Race-/Security-Prüfungen, Migration/Restore, Browser-/Accessibility-Prüfung und lokale Docker-Auslieferung.

## Fortschritt

180 Punkte: **55 verifiziert, 0 implementiert, 2 in Arbeit, 123 offen**. Dieser Zähler wird mit jedem abgeschlossenen Arbeitspaket abgeglichen.

## Einkaufen

| ID | Verbesserung | Abnahmekriterium | Status | Nachweis |
|---|---|---|---|---|
| SH01 | Schnellerfassung | Name direkt in der Liste eingeben; optionale Details aufklappen und ohne Seitenwechsel speichern. | Verifiziert | Native + Linux-Race; 33 Expansion-Browserchecks / 2 axe; unabhängige F01–F04-Abnahme bestanden |
| SH02 | Einkaufsmodus | Offene Artikel gruppiert bedienen, erledigte einklappen und Fortschritt anzeigen. | Verifiziert | Native + Linux-Race; 33 Expansion-Browserchecks / 2 axe; unabhängige F01–F04-Abnahme bestanden |
| SH03 | Geschäftsreihenfolge | Geschäftsprofile mit eigener Gang- und Gruppenreihenfolge auswählen. | Verifiziert | Native Gesamtregression + fokussierte Linux-Race; 27 Katalog-Browserchecks / 2 axe; Security und K01-Abnahme bestanden |
| SH04 | Produktvarianten | Marke, Packung und Bild auf Einkaufspositionen verständlich darstellen. | Verifiziert | Native Gesamtregression + fokussierte Linux-Race; 27 Katalog-Browserchecks / 2 axe; Security und K01-Abnahme bestanden |
| SH05 | Mengenerkennung | Eingaben wie 2×500 g Nudeln lokal erkennen und vor Speicherung bearbeitbar bestätigen. | Verifiziert | Native + Linux-Race; 33 Expansion-Browserchecks / 2 axe; unabhängige F01–F04-Abnahme bestanden |
| SH06 | Favoriten und Vorlagen | Favoriten und wiederverwendbare Listen erzeugen unabhängige neue Positionen. | Verifiziert | PostgreSQL-Grenztests, bestückte Migration + Linux-Race bestanden; 17 Browserchecks/4 axe; R01 unabhängig abgenommen |
| SH07 | Kontrolliertes Zusammenführen | Identische Produkte und kompatible Mengen zusammenführen; unklare Varianten zur Prüfung zeigen. | Verifiziert | Exakte Unit-Tests; PostgreSQL-Gesamtsuite + fokussierte Linux-Race; 13 Browserchecks/2 axe; unabhängige Abnahme bestanden |
| SH08 | Ersatz und nicht verfügbar | Verfügbarkeit und Ersatzbezug nachvollziehbar speichern und bearbeiten. | Verifiziert | Native Gesamtregression + fokussierte Linux-Race; 27 Katalog-Browserchecks / 2 axe; Security und K01-Abnahme bestanden |
| SH09 | Einkaufszuständigkeit | Personen zuweisen und zeitlich begrenzten Einkaufsstatus anzeigen. | Verifiziert | Native Gesamtregression + fokussierte Linux-Race; 27 Katalog-Browserchecks / 2 axe; Security und K01-Abnahme bestanden |
| SH10 | Warenkorbschätzung | Datierte Geschäftspreise verwenden; unbekannte Preise getrennt ausweisen. | Verifiziert | Native Gesamtregression + fokussierte Linux-Race; 27 Katalog-Browserchecks / 2 axe; Security und K01-Abnahme bestanden |
| SH11 | Einkauf einlagern | Gekaufte Artikel mit Produkt, Menge, Ort und Datum geprüft in den Vorrat übernehmen. | Verifiziert | Native Gesamtsuite, fokussierte Linux-Race, bestückter Upgrade/Restore; 32 Checkout-Browserchecks/4 axe, Gesamtkette 296; C01–C03 unabhängig abgenommen |
| SH12 | Belegabgleich | Abgeschlossenen Einkauf mit Beleg verknüpfen und Plan/Ist-Abweichungen prüfen. | Verifiziert | Native Gesamtsuite, fokussierte Linux-Race, bestückter Upgrade/Restore; 32 Checkout-Browserchecks/4 axe, Gesamtkette 296; C01–C03 unabhängig abgenommen |
| SH13 | Barcode-Erfassung | Kamera oder Scanner nutzen; lokale Zuordnung zuerst, externe Suche nur optional. | Offen | — |
| SH14 | Nachkaufvorschläge | Lokale Kaufabstände erklären und Vorschläge manuell übernehmen. | Offen | — |
| SH15 | Listenbenachrichtigungen | Abonnements, gebündelte Hinweise und optional konfigurierten Web Push bereitstellen. | Offen | — |
| SH16 | Live-Aktualisierung | Autorisierte Änderungen ohne Verlust laufender Eingaben und Versionskontrolle nachladen. | Verifiziert | Native + Linux-Race; 33 Expansion-Browserchecks / 2 axe; unabhängige F01–F04-Abnahme bestanden |
| SH17 | Offline-Leselisten | Ausgewählte Listen ausdrücklich pro Gerät freigeben; Eigentümer, Ablauf und Abmeldung beachten. | Offen | — |
| SH18 | Konfliktprüfung | Eigene und aktuelle Werte verständlich vergleichen und bewusst erneut speichern. | Verifiziert | Native + Linux-Race; 33 Expansion-Browserchecks / 2 axe; unabhängige F01–F04-Abnahme bestanden |
| SH19 | Suchbare Auswahl | Referenzen jenseits von 100 Einträgen mit Suche und stabiler Cursor-Paginierung auswählen. | Verifiziert | Native + Linux-Race; 33 Expansion-Browserchecks / 2 axe; unabhängige F01–F04-Abnahme bestanden |
| SH20 | Atomarer Abschluss | Einkauf, Bestand und Belege mit wiederholbarer Operation ohne Doppelbuchung abschließen. | Verifiziert | Native Gesamtsuite, fokussierte Linux-Race, bestückter Upgrade/Restore; 32 Checkout-Browserchecks/4 axe, Gesamtkette 296; C01–C03 unabhängig abgenommen |

## Fahrzeuge und Tanken

| ID | Verbesserung | Abnahmekriterium | Status | Nachweis |
|---|---|---|---|---|
| FU01 | Fahrzeugübersicht | Letzte Tankung, Kilometerstand, Verbrauch und anstehende Termine je Fahrzeug anzeigen. | Offen | — |
| FU02 | Tank-Schnellerfassung | Persönliche Vorgaben für Fahrzeug, aktive Serie und Station verwenden. | Offen | — |
| FU03 | Preisberechnung | Zwei Werte aus Menge, Preis und Summe exakt zum dritten ergänzen; Rundungsabweichungen zeigen. | Offen | — |
| FU04 | Verbrauch erklären | Fehlende Auswertung mit strukturiertem Grund und nächstem Handlungsschritt erklären. | Offen | — |
| FU05 | Verbrauchsdiagramme | Verbrauch und Kosten filtern; zugängliche Datentabelle ergänzen. | Offen | — |
| FU06 | Weitere Fahrzeugkosten | Wartung, Versicherung und Reifen mit Ausgaben verknüpfen, ohne doppelte Buchung. | Offen | — |
| FU07 | Wartungstermine | Datum oder Kilometerstand lösen nachvollziehbare Fälligkeit aus. | Offen | — |
| FU08 | Gesamtkosten pro Kilometer | Kostenumfang, Zeitraum und verwendete Fahrstrecke offen ausweisen. | Offen | — |
| FU09 | Elektro-Ladevorgänge | kWh, Ladeort, Kosten und optional Ladezustand getrennt vom Fahrverbrauch verwalten. | Offen | — |
| FU10 | Fahrtenbuch | Manuelle Fahrten mit Zweck erfassen und exportieren; keine steuerliche Zertifizierung behaupten. | Offen | — |
| FU11 | Tankdatenimport | Fuelio-, Drivvo- und Spritmonitor-CSV anhand dokumentierter Profile prüfen und deduplizieren. | Offen | — |
| FU12 | Tankbeleg-Erkennung | Datum, Menge und Betrag aus OCR als prüfbare Vorschläge mit Herkunft anzeigen. | Offen | — |
| FU13 | Kilometerfoto | Erkannten Kilometerstand mit vorherigem Stand abgleichen und bestätigen lassen. | Offen | — |
| FU14 | Wartung als Aufgabe | Fällige Wartung über einen deduplizierten Kernvertrag mit Aufgaben verknüpfen. | Offen | — |
| FU15 | Fahrzeugdokumente | Fahrzeug und berechtigte Dokumente über Kernverknüpfungen verbinden. | Offen | — |
| FU16 | Große Tankhistorie | Zeitfenster und Cursor ohne Verlust benötigter Randtankungen über 10.000 Zeilen auswerten. | Offen | — |
| FU17 | Verbrauchssegmente | Abgeleitete Segmente versioniert berechnen und nach relevanten Änderungen invalidieren. | Offen | — |
| FU18 | Tachowechsel | Alte Serie beenden und neue mit Datum und Anfangsstand geführt beginnen. | Offen | — |
| FU19 | Kraftstoffeinheiten | Kraftstoff und Mengenart verbindlich auf Liter, Kilogramm oder kWh abstimmen. | Offen | — |
| FU20 | Ausreißerhinweise | Konfigurierbare Plausibilitätsgrenzen erklären; Originalwerte nicht automatisch ändern. | Offen | — |

## Rezepte

| ID | Verbesserung | Abnahmekriterium | Status | Nachweis |
|---|---|---|---|---|
| RE01 | Rezepteditor | Grunddaten, Zutaten und Schritte gemeinsam mit Entwurf und Feldfehlern bearbeiten. | Verifiziert | Native Gesamtsuite + fokussierte Korrekturprüfung, Linux-Race, Upgrade/Restore; 37 Rezept-Browserchecks/4 axe, Gesamtkette333; RW01 unabhängig abgenommen |
| RE02 | Kochmodus | Aktuellen Schritt, abgehakte Zutaten, Fortschritt und optional Wake Lock anbieten. | Verifiziert | Native Gesamtsuite, Linux-Race, Upgrade/Restore; 38 Koch-Browserchecks/4 axe, Gesamtkette371; DB-Prüfung ohne Befund, CK01 und Dokumentation abgenommen |
| RE03 | Portionen im Kochmodus | Mengen exakt skalieren, ohne das Originalrezept zu ändern. | Verifiziert | Native Gesamtsuite, Linux-Race, Upgrade/Restore; 38 Koch-Browserchecks/4 axe, Gesamtkette371; DB-Prüfung ohne Befund, CK01 und Dokumentation abgenommen |
| RE04 | Dauerhafte Küchentimer | Mehrere beschriftete Timer über Navigation und Neuladen hinweg wiederherstellen. | Verifiziert | Native Gesamtsuite, Linux-Race, Upgrade/Restore; 38 Koch-Browserchecks/4 axe, Gesamtkette371; DB-Prüfung ohne Befund, CK01 und Dokumentation abgenommen |
| RE05 | Rezeptfilter | Dauer, Tags, Zutaten und letzte Zubereitung kombinieren und Ansichten speichern. | Offen | — |
| RE06 | Geprüfter Rezeptimport | Strukturierte Dateien und Schema.org-Rezepte vor Übernahme prüfen. | Offen | — |
| RE07 | Zutatenkatalog | Aliase normalisieren und Vorratsprodukte ausdrücklich zuordnen. | Verifiziert | Native Gesamtsuite68,3 %, Linux-Race201,979s, finale12 DB-/Upgradeverträge91,149s;29 Browserchecks/5axe, Gesamtkette481; Restore, DB-, UI- und Dokumentationsabnahme bestanden. |
| RE08 | Versionen und Varianten | Rezeptstände und abgeleitete Varianten unabhängig vom Audit erhalten. | Verifiziert | Native Gesamtsuite + fokussierte Korrekturprüfung, Linux-Race, Upgrade/Restore; 37 Rezept-Browserchecks/4 axe, Gesamtkette333; RW01 unabhängig abgenommen |
| RE09 | Zutatenersatz | Austauschregeln mit Mengenhinweisen anzeigen und manuell wählen. | Offen | — |
| RE10 | Kochhistorie | Tatsächliche Portionen, Bewertung und Notizen einer Zubereitung speichern. | Verifiziert | Native Gesamtsuite, Linux-Race, Upgrade/Restore; 38 Koch-Browserchecks/4 axe, Gesamtkette371; DB-Prüfung ohne Befund, CK01 und Dokumentation abgenommen |
| RE11 | Vorratsabgleich | Verfügbare Mengen, Fehlmengen und unbekannte Zuordnungen getrennt anzeigen. | Verifiziert | Native Gesamtsuite68,3 %, Linux-Race201,979s, finale12 DB-/Upgradeverträge91,149s;29 Browserchecks/5axe, Gesamtkette481; Restore, DB-, UI- und Dokumentationsabnahme bestanden. |
| RE12 | Vorrat nach Kochen | Verbrauch mit Mengen und Chargenauswahl prüfen und atomar buchen. | Verifiziert | Native Gesamtsuite68,8 % (Web642,856s), fokussierte8 DB-Verträge59,220s, Linux-Race127,427s;34 Browserchecks/5axe, Gesamtkette515; bestücktes Upgrade/Restore, DB-Findings geschlossen, F01 resolved/ship und separate Dokumentationsprüfung. |
| RE13 | Portionskosten | Geschätzte Kosten mit Preisalter, Quellen und fehlenden Preisen anzeigen. | Offen | — |
| RE14 | Nährwerte und Allergene | Strukturierte Angaben mit Quelle und Vollständigkeit; unbekannt bleibt unbekannt. | Offen | — |
| RE15 | Portabler Austausch | Household-JSON und ausgewählte Tandoor-, Mealie- und Paprika-Profile dokumentiert unterstützen. | Offen | — |
| RE16 | Gebündelte Rezeptdaten | Details mit begrenzten Abfragen statt einer Abfrage pro Zutat laden. | Verifiziert | Native Gesamtsuite + fokussierte Korrekturprüfung, Linux-Race, Upgrade/Restore; 37 Rezept-Browserchecks/4 axe, Gesamtkette333; RW01 unabhängig abgenommen |
| RE17 | Offline-Rezepte | Ausgewählte Rezepte mit begrenzten Bildern ausdrücklich lokal speichern und löschen. | Offen | — |
| RE18 | Kücheneinheiten | TL/EL umrechnen; Masse/Volumen nur mit bekannter zutatenbezogener Dichte. | Offen | — |
| RE19 | Sicherer URL-Import | SSRF, Weiterleitungen, Größen und HTML begrenzen; Importarbeit isolieren. | Offen | — |
| RE20 | Rezept-Dubletten | URL und Inhaltsfingerabdruck vergleichen; Zusammenführung immer prüfen lassen. | Offen | — |

## Vorrat

| ID | Verbesserung | Abnahmekriterium | Status | Nachweis |
|---|---|---|---|---|
| PA01 | Produktübersicht | Gesamtbestand, Orte und nächstes Datum anzeigen; Chargen aufklappen. | Verifiziert | PostgreSQL/Migration/Restore + Linux-Race; Pantry 18 Browserchecks/3 axe; unabhängige DB- und UI-Abnahme bestanden |
| PA02 | Zuerst verbrauchen | Datumsart, Öffnung und Restmenge für erklärbare Priorisierung verwenden. | Verifiziert | Native Gesamtsuite67,2 %, Linux-Race259,069s, bestücktes Upgrade/Restore, direkte50/100-Grenzen; Vorrat35Browserchecks/5axe, Gesamtkette448; DB-, UI- und Dokumentationsabnahme bestanden. |
| PA03 | Schnellverbrauch | Packung, Hälfte oder eigene Menge buchen; Rücknahme als Gegenbewegung. | Verifiziert | Native Gesamtsuite67,2 %, Linux-Race259,069s, bestücktes Upgrade/Restore, direkte50/100-Grenzen; Vorrat35Browserchecks/5axe, Gesamtkette448; DB-, UI- und Dokumentationsabnahme bestanden. |
| PA04 | Einlagerungsassistent | Mehrere Produkte mit Menge, Ort und Datum erfassen und Fortschritt bewahren. | Verifiziert | Native Gesamtsuite69,4 % (5758/8299; web772,207s),15 DB-Verträge, Linux-Race272,812s;37 Browserchecks/5axe, Gesamtkette552. Bestücktes Upgrade/echtes Purge/verschlüsseltes Restore, DB-Befunde geschlossen, UI F01/F02 resolved/ship und separate Designprüfung. |
| PA05 | Inventur | Zählbestand mit Buchbestand je Ort vergleichen und Differenzen begründet buchen. | Verifiziert | Native Gesamtsuite69,4 % (5758/8299; web772,207s),15 DB-Verträge, Linux-Race272,812s;37 Browserchecks/5axe, Gesamtkette552. Bestücktes Upgrade/echtes Purge/verschlüsseltes Restore, DB-Befunde geschlossen, UI F01/F02 resolved/ship und separate Designprüfung. |
| PA06 | Packungsgrößen | Packung und Basiseinheit getrennt speichern und exakt umrechnen. | Verifiziert | Native Gesamtsuite67,2 %, Linux-Race259,069s, bestücktes Upgrade/Restore, direkte50/100-Grenzen; Vorrat35Browserchecks/5axe, Gesamtkette448; DB-, UI- und Dokumentationsabnahme bestanden. |
| PA07 | Mehrere Barcodes | Produkt- und Packungsvarianten mehrere eindeutige Barcodes zuordnen. | Verifiziert | Native Gesamtsuite67,2 %, Linux-Race259,069s, bestücktes Upgrade/Restore, direkte50/100-Grenzen; Vorrat35Browserchecks/5axe, Gesamtkette448; DB-, UI- und Dokumentationsabnahme bestanden. |
| PA08 | Geöffnete Haltbarkeit | Nutzer- oder Herstellerangaben mit Herkunft als Hinweis anzeigen. | Verifiziert | Native Gesamtsuite67,2 %, Linux-Race259,069s, bestücktes Upgrade/Restore, direkte50/100-Grenzen; Vorrat35Browserchecks/5axe, Gesamtkette448; DB-, UI- und Dokumentationsabnahme bestanden. |
| PA09 | FEFO-Vorschlag | Passende Chargen nach Datum vorschlagen; manuelle Auswahl und Datumsarten erhalten. | Verifiziert | Native Gesamtsuite67,2 %, Linux-Race259,069s, bestücktes Upgrade/Restore, direkte50/100-Grenzen; Vorrat35Browserchecks/5axe, Gesamtkette448; DB-, UI- und Dokumentationsabnahme bestanden. |
| PA10 | Gekochte Reste | Rezeptbezug, Portionen, Zubereitung und Lagerort erfassen. | Offen | — |
| PA11 | Zielbestand | Mindest- und Zielbestand unterscheiden; Nachkauf auf Packungen aufrunden. | Verifiziert | Native70,1%, sieben finale PostgreSQL-Verträge, fokussierte Linux-Race; 32 Nachkauf-Browserchecks/sechs axe, Gesamtkette584; Upgrade/Restore und F01 unabhängig abgenommen. |
| PA12 | Reservierungen | Freien und für Mahlzeiten reservierten Bestand getrennt ausweisen. | Verifiziert | [Abnahme](stock-reservations-verification.md): Core19/Pantry7/Mealplan3,12 DB-Verträge, native70,4%, gezielter Linux-Race322,439s,625 Browserprüfungen,10 saubere axe-Scans und exakter synthetischer Restore. |
| PA13 | Open Food Facts | Optionalen Adapter mit Quellenangabe und lokalem Cache bereitstellen. | Offen | — |
| PA14 | Abfallanalyse | Gründe und optional geschätzte Kosten der Verluste auswerten. | Offen | — |
| PA15 | Rezeptvorschläge | Vorhandene und bald fällige Zutaten transparent gewichtet berücksichtigen. | Offen | — |
| PA16 | Große Bestandslisten | Gefilterte Paginierung und exakte Gesamtsummen ohne 100-Produkte-Grenze anbieten. | Verifiziert | PostgreSQL/Migration/Restore + Linux-Race; Pantry 18 Browserchecks/3 axe; unabhängige DB- und UI-Abnahme bestanden |
| PA17 | Bestandsprojektion | Transaktionale Salden pflegen und gegen das Bewegungsjournal prüfen. | Verifiziert | PostgreSQL/Migration/Restore + Linux-Race; Pantry 18 Browserchecks/3 axe; unabhängige DB- und UI-Abnahme bestanden |
| PA18 | Verbrauchskonflikte | Verbleibende Menge nach gleichzeitiger Buchung zeigen; kein blindes Wiederholen. | Verifiziert | Native Gesamtsuite67,2 %, Linux-Race259,069s, bestücktes Upgrade/Restore, direkte50/100-Grenzen; Vorrat35Browserchecks/5axe, Gesamtkette448; DB-, UI- und Dokumentationsabnahme bestanden. |
| PA19 | Offline-Scans | Scans als lokale Entwürfe prüfen; Offline-Daten sind keine Bestandsautorität. | Offen | — |
| PA20 | Historischer Bestand | Bestand zum Stichtag aus Bewegungen rekonstruieren und Korrekturzeitpunkte zeigen. | Verifiziert | PostgreSQL/Migration/Restore + Linux-Race; Pantry 18 Browserchecks/3 axe; unabhängige DB- und UI-Abnahme bestanden |

## Finanzen

| ID | Verbesserung | Abnahmekriterium | Status | Nachweis |
|---|---|---|---|---|
| EX01 | Monatsübersicht | Gebuchte Beträge, Entwürfe und Hauptkategorien getrennt darstellen. | Offen | — |
| EX02 | Aufgeteilte Erfassung | Buchung und Kategoriezeilen gemeinsam bearbeiten; Restdifferenz sichtbar halten. | Offen | — |
| EX03 | Buchungszustände | Entwurf, gebucht, erstattet und korrigiert mit passenden Aktionen erklären. | Offen | — |
| EX04 | Gespeicherte Finanzfilter | Person, Kategorie, Empfänger, Zeitraum und Status kombinieren. | Offen | — |
| EX05 | Belegprüfung | Beleg und Buchung nebeneinander beziehungsweise mobil mit erhaltenem Formularzustand zeigen. | Offen | — |
| EX06 | Konten und Einnahmen | Konten, Einnahmen und Umbuchungen modellieren; Transfers nicht als Verbrauch zählen. | Offen | — |
| EX07 | Kategoriebudgets | Monatsbudgets und ausdrücklich gewählte Übertragsregeln auswerten. | Offen | — |
| EX08 | Sparziele | Zielbetrag, Termin und geplante Beiträge von tatsächlichem Kontostand unterscheiden. | Offen | — |
| EX09 | Personenaufteilung | Gleich, Prozent, Anteile oder Festbetrag mit deterministischer Centverteilung aufteilen. | Offen | — |
| EX10 | Ausgleichszahlungen | Personensalden und Zahlungen getrennt von ursprünglichen Ausgaben führen. | Offen | — |
| EX11 | Wiederkehrende Buchungen | Vorlagen erzeugen deduplizierte, prüfbare Entwürfe. | Offen | — |
| EX12 | Kategorisierungsregeln | Prioritäten und zutreffende Empfängerregeln vor Übernahme erklären. | Offen | — |
| EX13 | Bankdateiimport | Dokumentierte CSV-Profile, Kontoauswahl, Vorschau und Dublettenkontrolle anbieten. | Offen | — |
| EX14 | Belegsumme prüfen | Positionen und Gesamtsumme mit OCR-Herkunft und Abweichungen vergleichen. | Offen | — |
| EX15 | Liquiditätsvorschau | Bestätigte Fälligkeiten und Wiederholungen mit Unsicherheit projizieren. | Offen | — |
| EX16 | Finanzberichte | Konten-, Personen-, Kategorien- und Zeitaggregate serverseitig nachvollziehbar berechnen. | Offen | — |
| EX17 | Kontoabstimmung | Externen Stichtagssaldo vergleichen; keine stillen Ausgleichsbuchungen. | Offen | — |
| EX18 | Mehrere Währungen | Originalbetrag, Basisbetrag, Kurs, Datum und Rundungsregel unveränderlich nachvollziehen. | Offen | — |
| EX19 | Finanzsichtbarkeit | Konten- und Datensatzrechte in Suche, Aggregaten, Dateien, Export und Audit anwenden. | Offen | — |
| EX20 | Buchungsherkunft | Manuell, Bank, Tankung und Beleg über gemeinsame Herkunft und Dublettenschutz verbinden. | Offen | — |

## Aufgaben

| ID | Verbesserung | Abnahmekriterium | Status | Nachweis |
|---|---|---|---|---|
| CH01 | Heute für mich | Zugewiesene, fällige und übernommene Aufgaben persönlich bündeln. | Offen | — |
| CH02 | Räume und Bereiche | Aufgaben nach Bereichen mit Anzahl und Fälligkeit ordnen. | Offen | — |
| CH03 | Wiederholung erklären | Letzte Erledigung und Berechnung der nächsten Fälligkeit sichtbar machen. | Offen | — |
| CH04 | Arbeitsansicht | Anleitung, Material, Unteraufgaben und Abschluss konzentriert bedienen. | Offen | — |
| CH05 | Verschieben und Pausieren | Termin verschieben, Vorkommen auslassen und Serie pausieren unterscheiden und vorab zeigen. | Offen | — |
| CH06 | Kalenderwiederholungen | Wochentage, Monate und Saison mit begrenzten Regeln und Vorschau unterstützen. | Offen | — |
| CH07 | Wiederholungsanker | Festen Zeitplan oder letzte Erledigung als ausdrücklichen Anker verwenden. | Offen | — |
| CH08 | Zuständigkeitsrotation | Rotation, Ausnahmen und Vertretung nachvollziehbar anwenden. | Offen | — |
| CH09 | Aufwand und Eignung | Optionale Dauer, Schwierigkeit und geeignete Personen pflegen. | Offen | — |
| CH10 | Unteraufgaben und Abhängigkeiten | Abhängigkeiten ohne Zyklen validieren und Abschlussbedingungen zeigen. | Offen | — |
| CH11 | Tagesplanung | Zeitbudget und Priorität für einen erklärbaren Tagesvorschlag verwenden. | Offen | — |
| CH12 | Abwesenheiten | Personenbezogene Pausen und Ersatzregeln in Zuweisungen beachten. | Offen | — |
| CH13 | Modulereignisse | Aufgaben aus Kernereignissen mit deduplizierter Herkunft erstellen. | Offen | — |
| CH14 | Aufgaben-Kalender | ICS-Export und widerrufbare Feeds mit stabilen Ereigniskennungen anbieten. | Offen | — |
| CH15 | Erinnerungsbündel | Ruhezeiten, Zusammenfassungen und ausdrücklich gewählte Eskalation berücksichtigen. | Offen | — |
| CH16 | Vorkommenshorizont | Begrenzten rollierenden Horizont mit eindeutigen Vorkommenskennungen erzeugen. | Offen | — |
| CH17 | Erledigungshistorie | Aufwand, Häufigkeit und tatsächliche Person ohne erzwungene Rangliste zeigen. | Offen | — |
| CH18 | Paralleler Abschluss | Gleichzeitige Erledigung genau einmal buchen und Ergebnis verständlich zurückgeben. | Offen | — |
| CH19 | Serienänderungen | Einzeltermin und zukünftige Serie unterscheiden; historische Vorlage erhalten. | Offen | — |
| CH20 | Zeitzonen und Sommerzeit | Lokale Wiederholung über Zeitumstellungen hinweg deterministisch berechnen. | Offen | — |

## Zähler und Energie

| ID | Verbesserung | Abnahmekriterium | Status | Nachweis |
|---|---|---|---|---|
| ME01 | Zählerübersicht | Letzten Stand, Datum, Verbrauch und überfällige Ablesung darstellen. | Offen | — |
| ME02 | Mobile Ablesung | Aktives Gerät, letzten Stand und Foto in einem geführten Ablauf anbieten. | Offen | — |
| ME03 | Intervallverbrauch | Verbrauch und Tagesdurchschnitt mit genauer Zeitspanne erklären. | Offen | — |
| ME04 | Messqualität | Messung, Schätzung und Datenlücken in Diagramm und Tabelle unterscheiden. | Offen | — |
| ME05 | Zählerwechsel | Alten Endstand und neuen Anfangsstand mit Datum und möglicher Lücke erfassen. | Offen | — |
| ME06 | Zeitabhängige Tarife | Arbeits- und Grundpreis mit Gültigkeitsintervallen versionieren. | Offen | — |
| ME07 | Kosten und Abschläge | Geschätzte Kosten mit tatsächlichen Abschlägen vergleichen und kennzeichnen. | Offen | — |
| ME08 | Periodenvergleich | Jahre und Zeiträume nur mit sichtbarer Datenabdeckung vergleichen. | Offen | — |
| ME09 | Mehrere Register | Bezug, Einspeisung und Erzeugung mit Einheiten und Flussrichtung trennen. | Offen | — |
| ME10 | Ableseplan | Fällige Ablesungen nach tatsächlichem passenden Messwert erledigen. | Offen | — |
| ME11 | Home Assistant | Optionalen Adapter mit ausdrücklicher Entitäts-, Einheiten- und Typzuordnung bereitstellen. | Offen | — |
| ME12 | MQTT | Optionale begrenzte Topics, validierte Nachrichten, Authentifizierung und Dublettenschutz. | Offen | — |
| ME13 | Versorgerimport | CSV mit Zeitzone, Einheit, Register und geprüfter Vorschau importieren. | Offen | — |
| ME14 | Anomaliehinweise | Konfigurierbare Verbrauchs- und Grundlasthinweise erst bei ausreichender Historie zeigen. | Offen | — |
| ME15 | Rechnungen als Ausgabe | Nur bestätigte Rechnungen oder Abschläge in Finanzen übernehmen. | Offen | — |
| ME16 | Ablesung und Telemetrie | Manuelle Stände von hochfrequenten Messdaten strukturell trennen. | Offen | — |
| ME17 | Zeitaggregate | Stunden-, Tages- und Monatswerte samt Rohdatenaufbewahrung verwalten. | Offen | — |
| ME18 | Korrektur-Neuberechnung | Benachbarte Intervalle und Kosten nach Änderung gezielt invalidieren. | Offen | — |
| ME19 | Messgrößentypen | Kumulativ, Intervall und Leistung strikt unterscheiden; kW ist nicht kWh. | Offen | — |
| ME20 | Große Messreihen | Zeitgefilterte, aggregierte Auswertungen jenseits von 10.000 Rohwerten liefern. | Offen | — |

## Dokumente

| ID | Verbesserung | Abnahmekriterium | Status | Nachweis |
|---|---|---|---|---|
| DO01 | Dokumenteneingang | Neue, ungeprüfte und fehlgeschlagene Dokumente mit nächstem Schritt bündeln. | Offen | — |
| DO02 | Dokumentenprüfung | Vorschau und Metadaten gemeinsam mit mobil erhaltenem Bearbeitungsstand zeigen. | Offen | — |
| DO03 | Gespeicherte Dokumentansichten | Rechnungen, Garantien und Fristen über kombinierte Filter anbieten. | Offen | — |
| DO04 | Fristenagenda | Fristen anzeigen und zugehörige Aufgaben über Kernverträge erstellen oder bearbeiten. | Offen | — |
| DO05 | Stapelbearbeitung | Ausgewählte Metadaten begrenzt, geprüft und transaktional ändern. | Offen | — |
| DO06 | Dokumenttypen | Typisierte, validierte Zusatzfelder pro Dokumenttyp unterstützen. | Offen | — |
| DO07 | Dateiversionen | Originale unverändert erhalten und aktuelle Version ausdrücklich festlegen. | Offen | — |
| DO08 | Mehrseitige Scans | Seitenreihenfolge, Zusammenführen und Aufteilen vorab zeigen; Originale erhalten. | Offen | — |
| DO09 | Fristarten | Rückgabe, Garantie und weitere Fristen mit Nutzerbestätigung und Quelle unterscheiden. | Offen | — |
| DO10 | Objektverknüpfungen | Fahrzeuge, Geräte und Buchungen über berechtigte Kernverträge verknüpfen. | Offen | — |
| DO11 | PDF-Erfassung | Vorhandenen PDF-Text nutzen; lokale OCR begrenzt im Hintergrund ausführen. | Offen | — |
| DO12 | Klassifikationsregeln | Regelwirkung vor Übernahme erklären und manuelle Korrektur bewahren. | Offen | — |
| DO13 | Scannerordner | Optionalen überwachten Eingang atomar, dedupliziert und wiederholbar verarbeiten. | Offen | — |
| DO14 | IMAP-Eingang | Optional eingegrenzte Anhänge prüfen; keine ungefragte Mail-Löschung. | Offen | — |
| DO15 | Paperless-Anbindung | Optionalen Adapter mit dokumentierter Datenhoheit und Synchronisationsumfang anbieten. | Offen | — |
| DO16 | Verarbeitungsstatus | Fortschritt, Abbruch und Wiederholung über bestehende Jobverträge bereitstellen. | Offen | — |
| DO17 | Dokumentvolltext | Autorisierte PostgreSQL-Suche mit geschützten OCR-Textausschnitten anbieten. | Offen | — |
| DO18 | Dokumentdubletten | Identische und wahrscheinliche Dubletten anzeigen; nie automatisch löschen. | Offen | — |
| DO19 | Dokumentrechte | Metadaten, Dateien, Volltext, Export und Verknüpfungen konsistent autorisieren. | Offen | — |
| DO20 | Portables Archiv | Originale, Metadaten und Links exportieren; Aufbewahrung, Audit und Backups erklären. | Offen | — |

## Essensplanung

| ID | Verbesserung | Abnahmekriterium | Status | Nachweis |
|---|---|---|---|---|
| MP01 | Wochenansicht | Desktop-Wochenraster und mobile Tagesagenda aus denselben Daten darstellen. | Verifiziert | Native Gesamtsuite 66,8 %, fokussierte Linux-Race, Upgrade/Purge/Restore; Planung 38 Browserchecks/4 axe, Gesamtkette 409; DB-Prüfung und PW01/PW02-Abnahme bestanden. |
| MP02 | Mahlzeiten bewegen | Verschieben und Tauschen per Tastatur vollständig, Drag-and-drop ergänzend anbieten. | Verifiziert | Native Gesamtsuite 66,8 %, fokussierte Linux-Race, Upgrade/Purge/Restore; Planung 38 Browserchecks/4 axe, Gesamtkette 409; DB-Prüfung und PW01/PW02-Abnahme bestanden. |
| MP03 | Rezept im Zeitfenster | Rezept mit Portionen und Aufwand direkt im Slot auswählen. | Verifiziert | Native Gesamtsuite 66,8 %, fokussierte Linux-Race, Upgrade/Purge/Restore; Planung 38 Browserchecks/4 axe, Gesamtkette 409; DB-Prüfung und PW01/PW02-Abnahme bestanden. |
| MP04 | Mahlzeitentypen | Reste, auswärts, Bestellung und freie Planung ohne falschen Einkaufsbedarf unterscheiden. | Verifiziert | Native Gesamtsuite 66,8 %, fokussierte Linux-Race, Upgrade/Purge/Restore; Planung 38 Browserchecks/4 axe, Gesamtkette 409; DB-Prüfung und PW01/PW02-Abnahme bestanden. |
| MP05 | Planungsstatus | Offen, geplant, eingekauft, gekocht und abgesagt nachvollziehbar verwalten. | Verifiziert | Native Gesamtsuite 66,8 %, fokussierte Linux-Race, Upgrade/Purge/Restore; Planung 38 Browserchecks/4 axe, Gesamtkette 409; DB-Prüfung und PW01/PW02-Abnahme bestanden. |
| MP06 | Teilnehmer und Gäste | Portionen ableiten und manuell überschreibbar halten. | Verifiziert | Core23/Mealplan7; vollständige native Regression272Tests,72,7% interne Abdeckung(Web1219,400s); gezielter Linux-Race bestanden(Web624,225s). Neun neue native Verträge, finale Browserkette740Checks mit30 Teilnehmerchecks/7 sauberen axe-Scans, exakter synthetischer Restore und enge Reviewer-Korrektur resolved/ship. Kein Live-Upgrade. [Nachweise](planning-participants-verification.md). |
| MP07 | Wochenvorlagen | Neue Wochen ohne alte Erledigungshistorie oder falsche Links aus Vorlage erzeugen. | Verifiziert | Native Gesamtsuite 66,8 %, fokussierte Linux-Race, Upgrade/Purge/Restore; Planung 38 Browserchecks/4 axe, Gesamtkette 409; DB-Prüfung und PW01/PW02-Abnahme bestanden. |
| MP08 | Geplante Reste | Erwartete Restportionen mit Folgemahlzeit verknüpfen und vom realen Bestand unterscheiden. | Offen | — |
| MP09 | Mehrere Komponenten | Beilagen und Hauptgerichte mit eigenen Portionen und gemeinsamer Bedarfsvorschau planen. | Verifiziert | Native Gesamtsuite 66,8 %, fokussierte Linux-Race, Upgrade/Purge/Restore; Planung 38 Browserchecks/4 axe, Gesamtkette 409; DB-Prüfung und PW01/PW02-Abnahme bestanden. |
| MP10 | Zeitbudget | Vorbereitung und Kochen pro Tag berechnen; Parallelität nur ausdrücklich annehmen. | Verifiziert | Native Gesamtsuite 66,8 %, fokussierte Linux-Race, Upgrade/Purge/Restore; Planung 38 Browserchecks/4 axe, Gesamtkette 409; DB-Prüfung und PW01/PW02-Abnahme bestanden. |
| MP11 | Vorratsbasierte Vorschläge | Verfügbarkeit anhand ausdrücklicher Produktzuordnungen bewerten. | In Arbeit | Getrennte, ignorierte Vorbereitung: exakte unabhängige Alternativenbewertung; konsistenter begrenzter Store-/Claim-Lesedienst kompiliert,14 reine Tests bestanden(Store1,401s). Fünf PostgreSQL-Verträge kompiliert, noch nicht ausgeführt; keine HTTP/PWA-Anbindung oder Feature-Abnahme. MP06-Prüfstand unverändert. [Vertrag](planning-suggestions.md). |
| MP12 | Verfallsorientierte Planung | Geöffnete und bald fällige Produkte mit erklärbarem Grund priorisieren. | In Arbeit | Getrennte Vorbereitung mit vollständiger begrenzter Chargenbewertung, MHD-/Verbrauchs-/Öffnungsgründen,0-Tage-Abgrenzung, Prüfhinweisen und Ranking.14 gemeinsame reine Tests bestanden; vergangene Planungstage für aktuellen Vorrat abgelehnt. Datenbankverträge, API/PWA und Abnahme noch offen. [Vertrag](planning-suggestions.md). |
| MP13 | Wochenbudget | Geschätzte Kosten und Alternativen mit fehlenden Preisen anzeigen. | Offen | — |
| MP14 | Differenzieller Bedarf | Freien, reservierten und bereits vorgemerkten Bestand korrekt vom Bedarf abziehen. | Verifiziert | Vollständige native Regression72,3% (259 Haupttests),37 Planungs-Verträge in gezielter Linux-Race708,702s mit weiteren Integrations-/Auditfällen;710 Browserchecks, exakter synthetischer Restore. Zurechenbare Einkäufe über MP17, keine doppelte Bestandsdeckung. [Gemeinsame Abnahme](planning-shopping-verification.md). |
| MP15 | Essens-Kalender | ICS mit stabilen Kennungen, Aktualisierung und widerrufbaren Feeds anbieten. | Offen | — |
| MP16 | Rezeptstand im Plan | Verwendete Rezeptversion erhalten und Änderungen zur bewussten Übernahme anzeigen. | Verifiziert | Native Gesamtsuite 66,8 %, fokussierte Linux-Race, Upgrade/Purge/Restore; Planung 38 Browserchecks/4 axe, Gesamtkette 409; DB-Prüfung und PW01/PW02-Abnahme bestanden. |
| MP17 | Wiederholbare Einkaufsübernahme | Bedarf über Plan-, Komponenten- und Zutatenidentität aktualisieren statt duplizieren. | Verifiziert | Vollständiger Sync/No-op/Replay, Checkout, ausdrückliche Entscheidungen, Beenden/Entkoppeln und deaktivierte Recovery über API/PWA. Native72,3%, Linux-Race708,702s,710 Browserchecks (42 neue/10 saubere axe-Scans), verschlüsselter synthetischer Restore, Security und PP-F01–PP-F04 resolved/ship. [Nachweise und Grenzen](planning-shopping-verification.md). |
| MP18 | Reservierungen freigeben | Bei Verschieben, Absage und Ablauf versionsgeprüft freigeben. | Verifiziert | [Gemeinsame Abnahme mit PA12](stock-reservations-verification.md): Quellenänderung, konkrete Lease-Freigabe, Ablauf, echte Purges, Replay bei abweichendem Nachfolger und späte Fehler-Rollbacks geprüft. |
| MP19 | Regelbasierter Autoplan | Gewichtete Kriterien erklären und jede Mahlzeit austauschbar lassen; kein KI-Zwang. | Offen | — |
| MP20 | Gebündelte Wochendaten | Autorisierte Wochenansicht mit Versionen, verzögerten Bildern und gezielten Updates laden. | Verifiziert | Native Gesamtsuite 66,8 %, fokussierte Linux-Race, Upgrade/Purge/Restore; Planung 38 Browserchecks/4 axe, Gesamtkette 409; DB-Prüfung und PW01/PW02-Abnahme bestanden. |

## Prüfprotokoll dieser Erweiterung

- MP01/02/03/04/05/07/09/10/16/20 abgeschlossen: Wochenraster/mobile Agenda, mehrere unabhängige Rezeptkomponenten, bewusste Quellenübernahme, Planungsarten/-status, Verschieben/Tauschen, Aufwand und dauerhafte Wochenkopien. Native Gesamtsuite 66,8 % (4304/6439; Web 461,034 s); fokussierte Linux-Race Planung/Rezepte/Kochen: Web 267,217 s. Sechs fokussierte PostgreSQL-Tests bestanden (36,885 s), tatsächlicher Rezept-Purge und dauerhafter Copy-Replay zusätzlich (10,229 s). Verify: 14,6 % (969/6618), 49 Node-Tests, 35 Migrationsprüfsummen; kompatibles Lint ohne Befund und Gosec: 66 Dateien/11431 Zeilen ohne Befund. Bestückter Restore erhielt exakte Komponentenportionen, eingefrorene Zutaten und Kopiereindeutigkeit sowie bisherige Werte und zwei Dateihashes. Vollständige zwölfteilige Browserkette: 409 Checks; Planung: 38 Checks, vier saubere axe-Scans, vier geprüfte Aufnahmen. PW01/PW02 korrigiert und eng unabhängig abgenommen. Die 37 verifizierten Punkte sind keine Freigabe aller 180; Live-Port 8100 bleibt unverändert.

- Vorratsgrundlage PA01/PA16/PA17/PA20 abgeschlossen: exakte Saldenprojektion, Produkt- und Chargenpagination, vollständige gefilterte Einheitensummen und historische Tagesgrenzen mit späteren Korrekturzeitpunkten. Vollständige native Integration: 63,8 % (3103/4862), Web 316,636 s; Linux-Race Vorrat 71,601 s; abschließende Zeitpunktergänzung gezielt mit 7,256 s erneut geprüft. Bestückte Docker-Sicherung/Wiederherstellung bestätigte exakte Salden und zwei Dateihashes.
- Vollständige Browserkette dieses Stands bestanden: 103 allgemein, 38 Dialog/Fehler, 15 Audit, 33 Expansion, 27 Katalog, 17 Wiederverwendung, 13 Zusammenführen und 18 Vorrat — 264 Checks. Vorrat: drei saubere axe-Scans und drei aktuelle Screenshots. Erster Kettenlauf stoppte am veralteten Pantry-Berichtsstub; nach der API-Anpassung prüft dieser den weiter begrenzten Tankbericht. Der neue Vorratsfehlerpfad wird separat geprüft. Schriftgröße und Alt-Chargenlink korrigiert und unabhängig abgenommen.
- Abschließende Verify-Prüfung: 14,5 % Unitabdeckung (730/5041), 28 Node-Tests, 27 Migrationsprüfsummen; Linter ohne Befund; Gosec 52 Dateien/8618 Zeilen ohne Findings oder Go-Fehler. Die 17 verifizierten Punkte sind weiterhin keine Gesamtfreigabe der 180 Verbesserungen.


- SH06/SH07 abgeschlossen: Favoriten/Vorlagen mit 17 Browserchecks und vier sauberen axe-Scans; Zusammenführen mit 13 Checks und zwei sauberen axe-Scans. R01 (Filterlinks) sowie Vorschau-Blocker und Fokusfehler korrigiert und gezielt erneut geprüft; unabhängige Abnahmen bestanden. Frühere gescrollte Merge-Screenshots wurden durch Aufnahmen vom Dokumentanfang ersetzt.
- Vollständige native Integration dieses Pakets bestanden: 63,3 % kombinierte interne Abdeckung (2982/4708). Linux-Race Shopping/Reference bei SH06: 159,266 s; zusätzliche Merge-Race nach SH07: 27,889 s. Aktuelle Verify-Prüfung: 14,7 % Unitabdeckung (716/4878), 28 Node-Tests, 25 Migrationsprüfsummen. Linter und Gosec (49 Dateien/8295 Zeilen) ohne Befund. Bestehende Einkaufs-Expansion erneut mit 33 Checks/zwei axe bestanden. Kein vollständiger abschließender Browser-Kettenlauf behauptet.


- Aktuelle Abnahme: erste sechs Punkte mit 33 Expansion-Browserchecks und unabhängig aufgelösten F01–F04; Katalog mit 27 Checks, zwei sauberen axe-Scans und aufgelöstem K01. Der aktive Einkaufsstatus steht nun vor Artikeln; Langnamensdarstellung und erste sichtbare Check-Aktion bei 390×844 sind geprüft. Weitere Browserkomponenten: 103 allgemein, 38 Dialog/Fehler, 15 Audit. Die Komponenten wurden nach gezielten Korrekturen einzeln wiederholt, keine abschließende komplette Kette behauptet.
- Fokussierte Linux-Race-Prüfung `TestShopping|TestReference` bestanden (Webpaket 112,216 s). Der frühere vollständige Race-Lauf des ersten Pakets bleibt separate Evidenz. Die fünf Katalogpunkte und die ersten sechs Verbesserungen sind verifiziert; das sind **11 von 180**, keine Gesamtfreigabe.

- Katalogpaket (SH03/04/08/09/10): aktuelle vollständige native PostgreSQL-Suite bestanden, 62,6 % kombinierte interne Abdeckung (2831/4522). Gezielte Tests prüfen exakte Pack-/Inhaltsrechnung, datierte Preise, inaktive Zuordnungen, konkurrierende und ablaufende Einkaufszuständigkeit, Altdaten-Upgrade, privaten Bildzugriff, Einzelreferenzen und lange gültige Cursor.
- Aktueller Unit/vet-Lauf, 28 Node-Tests, Modulverifikation, Workflowregeln und 22 Migrationsprüfsummen bestanden; Unitabdeckung 14,4 % (677/4692). Projektgebundener golangci-lint ohne Befund; GOSec: 47 Dateien / 7981 Zeilen / keine Befunde oder Go-Fehler. Ein erster Versuch mit veralteten Systempfad-Scannern scheiterte am Go-Exportformat und zählt nicht als Prüfung.
- Unabhängige fokussierte Sicherheitsnachprüfung aller benannten Katalogbefunde abgeschlossen. Ausgewählte optionale Referenzen sind auch beim bestätigten Konflikt-Retry vor Auslassung geschützt. Die neuen Browserregressionen laufen; die folgenden älteren Browserstände bleiben historische Evidenz.

- Referenzsuche: vollständige native Integration einschließlich 107 identischer Namen, Cursor-Manipulation, Ablauf, Suchbindung und Modulschutz bestanden; 61,7 % kombinierte interne Abdeckung (2679/4339) für diesen Zwischenstand.
- Mengenerkennung: 15 Node-Tests einschließlich 1.000 deterministischer Eingaben bestanden. Ein zunächst erkannter Grenzfall „2 kg“ ohne Artikelname wurde korrigiert.
- Einkaufspanel und neue Einkaufspaginierung: vollständige native Integration bestanden; interne Abdeckung 61,9 % (2723/4398). Neue Browserprüfung: 20 Checks und zwei axe-Scans bestanden. Vorhandene Komponenten: 95 allgemeine Browserchecks, 38 Accessibilitychecks mit drei Dialogscans, 15 Auditchecks mit zwei Scans bestanden.
- Unit/vet, 28 Node-Tests, Modulverifikation, Workflowrichtlinien und 20 Migrationsprüfsummen bestanden; golangci-lint und Gosec ohne Befund. Unitabdeckung 14,8 % (675/4568).
- Zwei Wartebedingungen der neuen Browserprüfung wurden korrigiert: asynchron geladene Konfliktdetails sowie bewusst eingeklappte erledigte Artikel. Danach bestand die komplette neue Prüfung.
- Erste Sichtprüfung Desktop/390px dunkel erfolgt; die unabhängige UI- und Sicherheitsprüfung war zu diesem Zwischenstand noch offen und ist inzwischen wie oben beschrieben abgeschlossen. Der statische Design-Detektor konnte den Go-Template-CSS-Link nicht auflösen und meldete deshalb eine flache Hierarchie; separate CSS-Prüfung ohne weitere Befunde. Tatsächliche Browser- und axe-Ergebnisse sind maßgeblich.
- Die Änderungen laufen bisher auf dem isolierten Prüfserver 8101; die Live-Docker-Instanz 8100 wurde noch nicht aktualisiert.
- Vorherige Prüfstände bleiben in `docs/implementation.md` dokumentiert.

- RE02/RE03/RE04/RE10 abgeschlossen: eingefrorene persönliche Kochsitzungen, exakte Portionsrechnung, dauerhafte Timer und bestätigter unveränderlicher Kochverlauf. Native Gesamtregression 65,8 % (3854/5861), Web 406,497 s; fokussierte Linux-Race für Kochen/Rezepte 168,684 s; Verify 15,3 % (924/6040), 38 Node-Tests, 33 Migrationshashes. Lint und Gosec ohne Befund. Synthetischer Restore erhielt UTC-Endzeit, pausierte Millisekunden und genau einen Abschluss. Elfteilige Browserkette: 371 Checks, davon Kochen 38 mit vier sauberen axe-Scans und vier geprüften Aufnahmen. CK01-Mengenbezug korrigiert und eng abgenommen; separate Dokumentation in cooking-session-verification.md. Live-Port8100 unverändert.
