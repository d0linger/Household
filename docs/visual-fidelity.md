# Warm Midnight · visueller Abgleich

Das freigegebene Designpaket enthält 17 Bildreferenzen in `screens/haushalt/` und `screens/administration/`. Maßgeblich ist die cremefarbene Arbeitsfläche mit dunkler Navigation in **Warm Midnight · Hell**. Die gleiche Komposition erscheint bei der automatischen Standardauswahl auf einem hellen System. Pink und die beiden dunklen Darstellungen verwenden dieselben Komponenten mit ihren eigenen Farben. Die Administration nutzt in jeder Auswahl die dunkle C-Oberfläche.

Die lokale [Vergleichsgalerie](../outputs/visual-fidelity/index.html) stellt Referenz, Vorher und Nachher gegenüber. Die Originalaufnahmen lassen sich einzeln öffnen. Aufnahmen und Kontodaten aus dem Prüfhaushalt werden getrennt vom Quellcode aufbewahrt.

## Vergleichsmatrix

Die Dateinummern entsprechen den unveränderten PNG-Namen im Paket. **D** bezeichnet Edge mit 1440 × 1000 CSS-Pixeln, **M** 390 × 844. Die regulären Aufnahmen zeigen Deutsch, Warm Midnight · Hell und 100 % Zoom. „Alex“ ist ein synthetisches Administratorkonto. Die Spalte Zugang nennt die tatsächliche Zugangsgrenze für die Seite. Administrationsaktionen können zusätzlich eine bestätigte Anmeldung erfordern. Jede der sechs Verwaltungsseiten hat auch bei fehlendem mobilen Referenz-PNG eine eigene Mobilaufnahme.

| PNG | Seite und Route | Zugang | Ausgangslage und Korrektur | Bilder | Status |
|---|---|---|---|---|---|
| 01 | Heute · `/` | Angemeldetes Mitglied | Allgemeiner Titel und Textblöcke. Begrüßung, persönlicher Aufgabenstand und Einkauf stehen zuerst. | [D](../outputs/visual-fidelity/index.html#heute-1440) | geprüft |
| 02 | Heute · `/` | Angemeldetes Mitglied | Der Arbeitsbereich beginnt unter einer kompakten Kopfzeile. Die Karten werden einspaltig. | [M](../outputs/visual-fidelity/index.html#heute-390) | abweichend |
| 03 | Offene Liste · `/m/shopping/lists/{id}` | Mitglied, Änderungen mit Schreibrecht | Der konkrete Listenname, der echte Abschlussstand und die gruppierten Artikel erhalten Vorrang vor Zusatzaktionen. | [D](../outputs/visual-fidelity/index.html#einkauf-1440) | geprüft |
| 04 | Offene Liste | Wie 03 | Einspaltige Erfassung und bedienbare Artikelzeilen. Sieben Artikel, zwei erledigt. | [M](../outputs/visual-fidelity/index.html#einkauf-390) | abweichend |
| 05 | Rezept · `/m/recipes/recipes/{id}/workspace` | Mitglied, Bearbeitung mit Schreibrecht | Das Formular dominierte. Die Leseansicht verwendet gespeicherte Zutaten, Zubereitung und das freigegebene Foto des Rezepts. | [D](../outputs/visual-fidelity/index.html#rezept-1440) | geprüft |
| 06 | Rezeptdetail | Wie 05 | Foto und Lesespalten stapeln sich, der Kochstart und der Editor bleiben erreichbar. | [M](../outputs/visual-fidelity/index.html#rezept-390) | abweichend |
| 07 | Finanzen · `/m/expenses/workspace/overview` | Aktuelle Finanzfreigaben | Umfangreiche Filter verdrängten die Zahlen. Der Monat und die berechtigten Beträge bilden den Einstieg, weitere Filter sind aufklappbar. | [D](../outputs/visual-fidelity/index.html#finanzen-1440) | abweichend |
| 08 | Finanzübersicht | Wie 07 | Das Monatsfeld und die Kennzahlen bleiben bei schmaler Breite lesbar. | [M](../outputs/visual-fidelity/index.html#finanzen-390) | abweichend |
| 09 | Allgemein · `/settings` | Angemeldetes Mitglied | Geräteeinstellungen und Einkaufsfunktionen bilden eigene Gruppen. Die persönliche Modulreihenfolge bleibt veränderbar. | [D](../outputs/visual-fidelity/index.html#general-1440) | geprüft |
| 10 | Module · `/settings/modules` | Administration für Änderungen | Lange Zeilen werden zu drei, zwei oder einer Kartenspalte mit Fachicon und echtem Status. Die Löschanforderung hat einen eigenen Bereich. | [D](../outputs/visual-fidelity/index.html#modules-1440) · [M](../outputs/visual-fidelity/index.html#modules-390) | geprüft |
| 11 | Benachrichtigungen · `/settings/notifications` | Eigenes Konto | Gerätestatus, Kategorien und verbundene Geräte bleiben getrennt. Der Testserver meldet seine tatsächliche Push-Konfiguration. | [D](../outputs/visual-fidelity/index.html#notifications-1440) · [M](../outputs/visual-fidelity/index.html#notifications-390) | abweichend |
| 12 | Sicherheit · `/settings/security` | Eigenes Konto | Passwort, zweite Faktoren, Passkeys und Sitzungen zeigen ihre jeweiligen Aktionen und reale Zustände. | [D](../outputs/visual-fidelity/index.html#security-1440) · [M](../outputs/visual-fidelity/index.html#security-390) | geprüft |
| 13 | Benutzer · `/settings/users` | Administration | Mitglied und Rolle bleiben scanbar. Der Dialog zum Anlegen öffnet auf Anforderung. | [D](../outputs/visual-fidelity/index.html#user-1440) · [M](../outputs/visual-fidelity/index.html#user-390) | geprüft |
| 14 | Protokoll · `/settings/audit` | Administration | Filter und Feldhistorie zeigen die tatsächlichen Änderungen des Prüfhaushalts. | [D](../outputs/visual-fidelity/index.html#audit-1440) · [M](../outputs/visual-fidelity/index.html#audit-390) | geprüft |
| 15 | Rechtes Popout über dem Protokoll | Nach Rolle gefiltert | Abgedunkelter Seiteninhalt, sichtbarer Kontokopf, botanischer Akzent und aktiver Protokolleintrag. | [D](../outputs/visual-fidelity/index.html#drawer-1440) | geprüft |
| 16 | Allgemein · `/settings` | Eigenes Konto | Dieselben Gruppen stapeln sich auf dem Handy. Einwilligungen und Löschdialoge behalten ihre Bedeutung. | [M](../outputs/visual-fidelity/index.html#general-390) | abweichend |
| 17 | Rechtes Popout über dem Protokoll | Nach Rolle gefiltert | Die normale Modulnavigation liegt hinter dem nativen Dialog. Sein Sitzungsbereich enthält die Abmeldung. | [M](../outputs/visual-fidelity/index.html#drawer-390) | geprüft |

„Geprüft“ bezeichnet den Sichtvergleich durch den Agenten. Bei „abweichend“ wurde die Ansicht ebenfalls geöffnet und verglichen. Die größere Abweichung steht unten. Die persönliche Designfreigabe ist offen.

## Prioritäten und Korrekturschleifen

| Priorität | Beobachtung im Browser | Korrektur und Beleg |
|---|---|---|
| Hoch | Auf Heute fehlte die persönliche Gewichtung. Offene und erledigte Aufgaben sowie das geplante Gericht wirkten wie gleichartige Textlisten. | Begrüßung und echter Fortschritt stehen im Kopf. Vier Fachkarten führen zu den vorhandenen Arbeitsbereichen. Das private Foto füllt die linke Hälfte der Essenskarte. [Heute](../outputs/visual-fidelity/index.html#heute-1440) |
| Hoch | Der Rezepteditor stand vor der Leseansicht. Die letzten Buchungen lagen unter dem Filterformular. | Das gespeicherte Rezept beginnt mit Bild, Fakten und Leseansicht. Finanzen zeigt den ausgewählten Monat, die tatsächlich sichtbaren Beträge und die letzten Ausgaben. [Rezept](../outputs/visual-fidelity/index.html#rezept-390) · [Finanzen](../outputs/visual-fidelity/index.html#finanzen-1440) |
| Hoch | Eine Zwischenfassung der Einkaufsliste blieb bei „Artikel werden geladen“. Die verschobene Überschrift lag außerhalb des Suchbereichs des Controllers. | Der Controller adressiert die eindeutige Überschrift. Die Bildaufnahme wartet auf den tatsächlichen Zählerstand der gefüllten und leeren Liste. [Einkauf](../outputs/visual-fidelity/index.html#einkauf-390) |
| Mittel | Auf dem Handy umbrach die Kopfzeile. Wiederholte Überschriften, Bearbeitung und Suchfelder verdrängten Listeninhalt und Rezeptfoto. | Eine Kopfzeile, kompakte Schnellerfassung und Suche sowie ein vorgezogener Fotobereich. In den Ansichten 02, 04 und 06 stehen bei 390 px weniger vollständige Karten oder Zeilen im ersten Ausschnitt als im Mockup. Touchziele und die tatsächlichen Aktionen bleiben benutzbar. |
| Mittel | Kartenstatus und gefährliche Aktionen gingen in einer langen Verwaltungsseite unter. | Das Modulraster richtet Status und Aktionen aus. Die endgültige Löschanforderung bleibt separat. Der rechte Dialog kennzeichnet die aktive Seite. [Module](../outputs/visual-fidelity/index.html#modules-1440) · [Kontomenü](../outputs/visual-fidelity/index.html#drawer-390) |

Die Zwischenstände sind lokal einsehbar. Das erste Shellbild dokumentiert die Reihenfolge der Arbeit, die Rezeptansicht die darauffolgende Überarbeitung des mobilen Bildausschnitts. [Erster Shellvergleich](../outputs/visual-fidelity/shell/heute-390.png) · [Erste Rezeptfassung](../outputs/visual-fidelity/erste-fassung/rezept-390.png) · [Letzte Rezeptfassung](../outputs/visual-fidelity/after/rezept-390.png)

## Bildmaterial

| Lokale Quelle | Herkunft und Verwendung |
|---|---|
| [icons.svg](../internal/web/static/icons.svg) | Native SVG-Symbole für das Linienhaus und das Konto. Die Wortmarke bleibt echter Text. Die vorhandenen Fachicons werden wiederverwendet. |
| [botanical-sprig.svg](../internal/web/static/botanical-sprig.svg) | Lokal gezeichnete kupferfarbene Blattsilhouette im Seitenmenü und im rechten Dialog. |
| [copper-branch.webp](../internal/web/static/copper-branch.webp) | Mit ImageGen erzeugter transparenter Eukalyptuszweig. 760 × 383, 41.700 Byte. Wird als eingebettetes Asset mit Versionspfad ausgeliefert. |
| [market-bag.webp](../internal/web/static/market-bag.webp) | Mit ImageGen erzeugte, freigestellte Einkaufstasche. 371 × 440, 39.890 Byte. Rein dekorativ in der Einkaufskarte. |
| [Privates Testfoto](../outputs/visual-fidelity/belege/curry-fixture.jpg) | Das synthetische Curryfoto wurde im isolierten Haushalt als JPEG über die reguläre Anhangsfunktion hochgeladen. Die App lädt die geschützte Vorschau über `/attachments/{id}/preview`. Rezepte ohne Foto verwenden ein Buchsymbol. Vorbereitungs- und Kochzeit erscheinen im Rezeptkopf ab einer erfassten Minute. Der Datenbank-Vorgabewert 0 erzeugt keine Zeitangabe. |

Das neu gezeichnete Hauszeichen verwendet im lokalen Sprite die Kennung `house-mark`, das Kontosymbol `account`. Der botanische Vektor ist ebenfalls eine Projektzeichnung. Für die Projektquellen gilt die [MIT-Lizenz](../LICENSE). Die beiden WebP-Dekorationen und das separate Testfoto wurden für diesen Auftrag generiert. Die Schriftlizenz liegt bei der jeweiligen Betriebssysteminstallation. Das Stylesheet verwendet die dort installierten Familiennamen.

## Daten und fachliche Unterschiede

- „Heute“ zeigt die eigenen offenen, bereits fälligen Aufgaben und die für den Haushaltstag fälligen erledigten Aufgaben. Eine Prozentzahl setzt zwei vollständig geladene Ergebnismengen voraus. Bei fehlenden oder abgeschnittenen Daten bleibt der Ring aus.
- Der Prüfhaushalt enthält vier eigene fällige Aufgaben, eine Einkaufsliste mit sieben Artikeln, ein geplantes Curry mit sechs Zutaten und drei Schritten sowie einen gespeicherten Ausgabenentwurf über 38,60 EUR. Die Finanzansicht unterscheidet den gebuchten Verbrauch von bekannten Entwurfsbeträgen. Entwürfe ohne Betrag werden separat gezählt. Die Kategorieauswertung setzt gebuchte, kategorisierte Ausgaben voraus.
- Rezepte und Einkaufslisten besitzen getrennte Übersichts- und Detailansichten. Galerie und Protokoll unterscheiden eine echte leere Liste, eine Suche ohne Treffer und ein Rezept ohne Foto.
- Schriftfamilien der PNGs sind nicht mitgeliefert. Die Implementierung verwendet lokale Georgia und die System-Sans. Sicherheitsaktionen, Artikelgrößen und deutsche Texte benötigen auf kleinen Bildschirmen teilweise mehr Höhe als in den Referenzbildern.
- Der tägliche Status, Benutzernamen und die Navigation stammen aus der laufenden Anwendung. Beim rechten Menü richtet sich der aktive Punkt nach der geöffneten Seite. Push und Drittanbieteranfragen sind in der Prüfinstanz deaktiviert.

Die Vorlagen 07/08 enthalten Einnahmen, Nettoergebnis und eine mehrmonatige Reihe. Die hier verfügbare Übersicht wertet berechtigte Ausgaben aus. Das Datenpanel zeigt gebuchte Beträge, bezifferte Entwürfe und die letzten sichtbaren Ausgaben. Seine kreisförmige Einfassung hat keine Prozentbedeutung. Die Ansichten 11/16 brauchen mehr Platz für Einwilligungen, Speicherung und den tatsächlichen Gerätestatus. Der Testhaushalt hat ein Mitglied, eine aktive Sitzung und keine registrierten Passkeys.

## Prüfumgebung und Werkzeuge

Die sieben mitgelieferten Ist-Aufnahmen und alle 17 Ziel-PNGs wurden geöffnet. Die eigenen Vorher-Bilder stammen aus dem ursprünglichen lokalen Anwendungsimage. Vorher und Nachher liefen auf demselben isolierten Loopback-Testziel und zeigen dieselben Fachdaten, denselben Browser und dieselben Viewports. Die Prüfinstanz hatte ein eigenes internes Docker-Netz, eine eigene Upload-Ablage und einen eigenen Datenbankserver. Für den Leerzustand erhielt eine eigene, regulär migrierte Datenbank nur das Testkonto und die aktivierten Module. Eine zuvor aus einem SQL-Dump wiederhergestellte Testkopie wurde durch die vorhandene Barcode-Bereitschaftsprüfung zurückgewiesen und verworfen. Dadurch konnten leere Startseiten und anschließend pausierte Module über echte Routen geprüft werden.

Für Hierarchie, Abstände und responsive Details wurde `frontend-skill` verwendet. `imagegen` lieferte die freigestellten Bildzutaten und das private Testfoto. Der Sitzungsmodus von `playwright-interactive` erforderte hier weitergehende Hostrechte. Den Sichtvergleich übernahm die vorhandene Projektintegration mit Playwright, Microsoft Edge und Axe. Die Schriften wurden am gerenderten Text über das Chrome DevTools Protocol ermittelt. Auf dem Testsystem renderten Georgia Bold und Segoe UI. Verwendet werden nur lokal vorhandene Systemschriften. Die konkrete Ersatzschrift hängt auf anderen Geräten vom Betriebssystem ab.

## Prüfprotokoll

| Prüfung | Ergebnis |
|---|---|
| `go test -p 2 ./... -count=1` und `go vet -p 2 ./...` | 27 Pakete mit Tests bestanden. Statische Go-Prüfung bestanden. |
| Alle in `package.json` unter `test` aufgeführten Node-Tests mit `node --test --test-concurrency=2` | 526 bestanden, 0 fehlgeschlagen, 0 übersprungen. |
| `node scripts/check-localization.cjs` | 6.527 verwendete Meldungen mit geprüfter englischer Übersetzung. |
| `go test -tags=integration,testtools ./internal/web -run '^TestTodayPersonalAuthorityAndReadOnlyHome$' -count=1` | Bestanden mit isoliertem PostgreSQL. Die persönliche Abfrage trennt Haushalte und Berechtigungen und bleibt lesend. |
| `go test -tags=integration,testtools ./internal/store -run '^TestFinanceSupportBudgetCarryUsesActualAuthorizedMonthConsumption$' -count=1` | Bestanden mit isoliertem PostgreSQL. Beträge, Buchungstag und zuletzt sichtbarer Ausgabeneintrag wurden gegen die berechtigte Auswahl geprüft. |
| `node .local/fidelity/review.cjs after` sowie die getrennten Leer- und Pausenläufe | 23 reguläre Ansichten auf Desktop und Mobil. 46 Vorher- und 46 Nachherbilder, zusätzlich je acht Bilder eines vollständig leeren Haushalts sowie vier Aufnahmen mit pausierten Modulen. Keine JavaScript-Ausnahme und kein horizontaler Seitenüberlauf in den Aufnahmeläufen. [Vorher](../outputs/visual-fidelity/before/report.json) · [Nachher](../outputs/visual-fidelity/after/report.json) · [Leer](../outputs/visual-fidelity/nachher-leer/report.json) · [Pausiert](../outputs/visual-fidelity/pausiert/report.json) |
| `node .local/fidelity/review.cjs matrix` und `node .local/fidelity/zoom.cjs` | 53 protokollierte Beobachtungen und Bedienproben bei 320, 390, 430, 1024, 1440 und 1728 CSS-Pixeln. DE/EN, vier Farbpaletten, beide Systemmodi, privates Foto, echte Einkaufs- und Finanzfilter, Rezeptlesen/-bearbeiten, Fokus-Rückgabe und aufklappbare Feldhistorie geprüft. Ein simulierter fehlgeschlagener Heute-Abruf leerte private Karteninhalte. Die Wiederholung lud die echten Daten. Nativer Edge-Zoom auf 200 % verkleinerte die Layoutbreite von 1416 auf 708 CSS-Pixel ohne seitlichen Seitenüberlauf. [Interaktionen](../outputs/visual-fidelity/matrix/report.json) · [Zoom](../outputs/visual-fidelity/matrix/zoom.json) |
| Axe für WCAG 2 A/AA und 2.1 AA | 46 Scans in den regulären Ansichten, vier zusätzliche Scans für die Farbpaletten von Heute. In diesen Scans wurden 0 Verstöße gemeldet. |
| `node .local/fidelity/gallery-check.cjs` | Alle 29 Auswahlmöglichkeiten in beiden Größen geöffnet. Die Galerie enthält alle 17 Referenzen und funktioniert lokal ohne externe Anfragen. [Galerieprüfung](../outputs/visual-fidelity/belege/galerie.json) |

Die Prüfskripte, Testfotos und Screenshots liegen lokal im Entwicklungsarbeitsplatz. Die Testdatenbanken und Browserprofile werden nach dem Abgleich verworfen. Die Repository-Tests für Aufgabenfortschritt, Rezeptzeiten und berechtigte Finanzauswahl bleiben wiederholbar. [Testergebnisse](../outputs/visual-fidelity/belege/ergebnis.json)

## Betriebsnachweis

Der lokale Entwicklungsstack nutzt das neu gebaute OCR-Image. App und Backup wurden neu erstellt. PostgreSQL behielt seinen Container, seine Daten und die benannten Volumes. Die Bereitschaftsprüfung auf Port 8100 lieferte HTTP 200. Die Antwortinhalte von 13 ausgelieferten Assets entsprachen ihren Quelldateien. WebP-Medientyp, Cache-Header und PWA-Manifest wurden geprüft. Der vorige Stand ist lokal als `household:fidelity-before-56331acc` vorhanden. [Docker-Prüfprotokoll](../outputs/visual-fidelity/local-docker.json)

Die persönliche Designfreigabe, die Sichtprüfung auf physischen Android-/iOS-Geräten und die manuelle Screenreader-Abnahme sind offen. Die automatisierten Barrierefreiheitstests decken die oben festgehaltenen Seiten und Zustände ab. Die vollständige Datenbank-, Race- und Disaster-Recovery-Kampagne gehört ebenfalls zu den hier offenen Prüfungen.
