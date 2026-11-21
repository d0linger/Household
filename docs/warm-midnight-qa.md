# Warm Midnight — Abnahme vom 20. November 2026

Prüfung der echten Anwendung gegen alle 17 freigegebenen Referenzbilder. Screenshots verwenden ausschließlich isolierte synthetische Prüfdaten. Ergebnisse und Grenzen sind unten getrennt aufgeführt.

## A. Globales Design und Navigation

- [x] Die neue linke Sidebar entspricht Warm Midnight (dunkel Plum, Kupfer-Auswahl, korrekte Linktexte und Modulreihenfolge), ohne zweite Admin-Sidebar. **bestanden** — Gesamtmatrix und Tray-Suite.
- [x] Consumer-Seiten haben warme Cremeflächen, einzelne große redaktionelle Serifentitel, fein getrennte Formulare/Listen und konsistente nutzbare Abstände. **bestanden** — Gesamtmatrix und Tray-Suite.
- [x] Admin-C-Seiten haben dunkles Navy, feine Linien, hoch kontrastierendes Sans und sparsame Mint-Akzente. **bestanden** — Gesamtmatrix und Tray-Suite.
- [x] Kein altes blaugraues „Calm Living“-Betriebslayout und kein B-/D-Aurora-/Pop-Design versehentlich geblieben. **bestanden** — Gesamtmatrix und Tray-Suite.
- [x] Bestehende SVG-Icons, lokale Fonts, Fokus, Min-44px-Ziele und alle ARIA-/`data-*`-Attribute erhalten. **bestanden** — Gesamtmatrix und Tray-Suite.
- [x] `aria-current` entspricht tatsächlicher Route/Modulzustand; die im Konzept manchmal aktivierte „Heute“-Zeile wird nicht blind kopiert. **bestanden** — Gesamtmatrix und Tray-Suite.
- [x] Modulnavigation auf Mobil scrollt weiterhin horizontal durch alle freigeschalteten Module, ohne neue künstliche „Mehr“-Destination; Safe Area und 320px funktionieren. **bestanden** — Gesamtmatrix und Tray-Suite.
- [x] Keine globale horizontale Seitenüberläufe bei 320, 390, 760, 1024, 1440px und 200% Zoom. **bestanden für Reflow** — 200%-Äquivalent mit 720×450 CSS-Pixeln bei DPR 2; echter Browser-Menüzoom nicht automatisiert.
- [x] Ohne CDN, Remote-Font oder neues JS-/Frontend-Framework implementiert; Browser-Assets aktualisiert/rebuild nur nach Projektregeln. **bestanden** — Gesamtmatrix und Tray-Suite.

## B. Alle 8 Consumer-Bilder

- [x] `01_heute_desktop.png`: dieselbe Informationshierarchie und Warm-Midnight-Komposition, aber ausschließlich reale vorhandene Today-Gruppen/Daten. **bestanden** — Modulsuiten Phasen 2–6 sowie Gesamtmatrix.
- [x] `02_heute_mobil.png`: einspaltig, korrekte Navigation, keine abgeschnittenen Aktionen. **bestanden** — Modulsuiten Phasen 2–6 sowie Gesamtmatrix.
- [x] `03_einkaufen_desktop.png`: Erfassung und Einkaufsliste nebeneinander, echte Mengen, Check-/Review-Verhalten. **bestanden** — Modulsuiten Phasen 2–6 sowie Gesamtmatrix.
- [x] `04_einkaufen_mobil.png`: Erfassung oben, große Check-Aktionen, kein Kontextverlust bei Scroll/Fokus. **bestanden** — Modulsuiten Phasen 2–6 sowie Gesamtmatrix.
- [x] `05_rezepte_desktop.png`: echtes Rezeptdetail, Zutaten/Portionen/Schritte, realer Kochworkflow, optionales zulässiges Bild. **bestanden** — Modulsuiten Phasen 2–6 sowie Gesamtmatrix.
- [x] `06_rezepte_mobil.png`: Portionen, Schritte, Kochen auch auf kleinen Geräten bedienbar. **bestanden** — Modulsuiten Phasen 2–6 sowie Gesamtmatrix.
- [x] `07_finanzen_desktop.png`: warme App-Shell und dark-C-Datenpanel mit echten autorisierten Summen/Tabellen. **bestanden** — Modulsuiten Phasen 2–6 sowie Gesamtmatrix.
- [x] `08_finanzen_mobil.png`: legible Beträge, reale Details/Rechte und keine neuen privaten Offline-Daten. **bestanden** — Modulsuiten Phasen 2–6 sowie Gesamtmatrix.
- [x] Weitere Module (Vorräte, Fahrzeuge, Essensplanung, Aufgaben, Zähler, Dokumente) übernehmen dieselben konkreten Rollen/Komponenten. **bestanden** — Modulsuiten Phasen 2–6 sowie Gesamtmatrix.

## C. Alle 9 Admin-Bilder und rechter Dialog

- [x] `09_general_desktop.png`: vorhandene Darstellung, Offline-Freigabe, wartende Änderungen, Shopping-Hinweise, Lesekopien; keine erfundenen Einstellungen. **bestanden** — Admin-, Notification-, Security-, Audit- und Tray-Suiten.
- [x] `10_modules_desktop.png`: echte Aktiv/Pausiert-Daten, Rollen und kontrollierte Purge-Anforderung, keine Direktlöschung. **bestanden** — Admin-, Notification-, Security-, Audit- und Tray-Suiten.
- [x] `11_notifications_desktop.png`: Browserfreigabe nur nach Klick, reale Kategorie-/Gerätesequenz, keine Fake-Push-Erfolge. **bestanden** — Admin-, Notification-, Security-, Audit- und Tray-Suiten.
- [x] `12_security_desktop.png`: 2FA-/Passkey-/Sitzungsprozesse und Reauth/Bestätigung intakt; keine Scheinauthentifizierung. **bestanden** — Admin-, Notification-, Security-, Audit- und Tray-Suiten.
- [x] `13_user_desktop.png`: echte Rollen/Status, Neuer-Benutzer-Dialog, Save-/Revoke-Folgen. **bestanden** — Admin-, Notification-, Security-, Audit- und Tray-Suiten.
- [x] `14_audit_desktop.png`: echte UTC-/Suche-/Bereich-/Aktion-/UUID-Filter, Pagination/Details und keine Offenlegung geschützter Felder. **bestanden** — Admin-, Notification-, Security-, Audit- und Tray-Suiten.
- [x] `15_rechtes_popout_desktop.png`: Admin-Menü **rechts als Overlay**; normale Modul-Sidebar bleibt **links**, darunterliegende C-Seite wird gedimmt. **bestanden** — Admin-, Notification-, Security-, Audit- und Tray-Suiten.
- [x] `16_general_mobil.png`: responsive Admin-C-Formulare, sichtbare Labels, Scroll-/Safe-Area-Verhalten, 16px Inputs. **bestanden** — Admin-, Notification-, Security-, Audit- und Tray-Suiten.
- [x] `17_rechtes_popout_mobil.png`: von **rechts** einfahrender, fokussierender Menü-Dialog, mit echter Schließen-/Escape-/Fokus-Rückgabe. **bestanden** — Admin-, Notification-, Security-, Audit- und Tray-Suiten.
- [x] Alle Adminlinks sind in der vorhandenen Reihenfolge erreichbar, Rollenfilter für Reader/Editor/Admin intakt. **bestanden** — Admin-, Notification-, Security-, Audit- und Tray-Suiten.
- [x] „App installieren“ und „Abmelden“ bleiben als echte, unabhängig zugängliche Session-Aktionen erhalten. **bestanden** — Admin-, Notification-, Security-, Audit- und Tray-Suiten.
- [x] In allen vorhandenen adminbezogenen nativen Modals/Details: C-Farbrollen, echte Fokusindikatoren, Escape-/Backdrop-Verhalten, Tabreihenfolge. **bestanden** — Admin-, Notification-, Security-, Audit- und Tray-Suiten.

## D. Semantik und technische Zustände

- [x] Theme-Auswahl Ocean/Pink, Light/Dark/System und echtes System-Following funktionieren nach dem neuen Mapping; Konflikt mit `DESIGN.md` dokumentiert und freigegeben. **bestanden** — Modulsuiten, Theme-Matrix und Gesamtmatrix.
- [x] UI ist vollständig in DE/EN und bei langem Text, 200%-Zoom, schmalen Formfeldern und großen Systemfonts lesbar. **bestanden für Reflow** — 200%-Äquivalent mit 720×450 CSS-Pixeln bei DPR 2; echter Browser-Menüzoom nicht automatisiert.
- [x] Tastatur und Screenreader/Axe-Prüfungen auf Navigations- und Sicherheitsflows ohne neue Blocker. **bestanden mit Axe und Tastaturprüfungen** — keine manuelle Screenreader-Sitzung.
- [x] `prefers-reduced-motion`, Forced Colors, Zoom/Fokus-Scroll und Touch funktionieren. **bestanden für Reflow** — 200%-Äquivalent mit 720×450 CSS-Pixeln bei DPR 2; echter Browser-Menüzoom nicht automatisiert.
- [x] Leer/Laden/Fehler/Offline/Konflikt/Nur-Lesen/fehlende Berechtigung haben eindeutige verständliche Zustände ohne „falschen Erfolg“. **bestanden** — Modulsuiten, Theme-Matrix und Gesamtmatrix.
- [x] CSRF/Sessions, Finanz- und Dokumentenschutz, Konflikt- und Review-Verhalten, Auditlogs, Modul-Purge und Benutzerrolleneffekte unverändert. **bestanden** — Modulsuiten, Theme-Matrix und Gesamtmatrix.
- [x] Keine Test-Zahlen/Namen/Bilder/Anmelde- oder 2FA-Zustände aus den Konzepten im echten Produkt. **bestanden** — Modulsuiten, Theme-Matrix und Gesamtmatrix.

## E. Testbelege, Abschlussbericht und Freigaben

- [x] Relevante Go-Tests mit tatsächlichem Kommando und Ergebnis angegeben. **bestanden** — Testprotokoll unten.
- [x] `npm test` und betroffene gezielte Node-Tests ausgeführt oder begründet nicht verfügbar. **bestanden** — Testprotokoll unten.
- [x] Browser-Suite und fokussierte Tray/Theme/A11y/Shopping/Recipe/Finance/Notification/Security-Checks mit Belegen oder dokumentierten Blockern. **bestanden** — Testprotokoll unten.
- [x] Echte Implementierungs-Screenshots für 1440×900 und 390×844 zu allen Hauptmodulen und repräsentativen C-Adminseiten gesichert. **bestanden** — Testprotokoll unten.
- [x] Responsive Checks für 1024×768, 760×900, 320×720 und Zoom 200% durchgeführt. **bestanden für Reflow** — 200%-Äquivalent mit 720×450 CSS-Pixeln bei DPR 2; echter Browser-Menüzoom nicht automatisiert.
- [x] Nicht ausgeführte OS-/WebAuthn-/Push-/DB-/Browserchecks ausdrücklich als **nicht geprüft** markiert. **bestanden** — Testprotokoll unten.
- [x] Geänderte Dateien und Design-/Produktkontrakte dokumentiert; `DESIGN.md` und relevante Implementierungsdokumentation angepasst, falls freigegeben. **bestanden** — Testprotokoll unten.
- [x] Ausdrückliche Freigabe vor Git-Push, PR, Release oder Remote-Deployment eingeholt (nicht durch dieses Paket erteilt). **eingehalten** — kein Push, PR, Release oder Remote-Deployment; lokaler Docker-Neubau separat vom Nutzer genehmigt.

## Ausgeführte Prüfungen und Belege

Browserbefehle liefen gegen die besitzgeprüfte, isolierte PostgreSQL-/Docker-Prüfinstanz auf `localhost:8101`, mit ausschließlich synthetischen Konten. Die reguläre Anwendung auf Port 8100 wurde dabei nicht verändert. Private Sitzungsdateien und Aufnahmen bleiben ignoriert.

| Befehl / Kampagne | Ergebnis | Beleg |
|---|---|---|
| `go test ./...` mit `GOMAXPROCS=2` | 27 Pakete bestanden | `outputs/warm-midnight/go-unit.log` |
| `go test ./internal/web ./internal/i18n` nach letzter Template-/Katalogänderung | bestanden | Konsolenausgabe vom 20.11.2026 |
| `npm test` | 524 bestanden, 0 übersprungen | `outputs/warm-midnight/npm-test.log` |
| `node --test scripts/finance-workspace.test.cjs scripts/localization.test.cjs` | 30 bestanden | `outputs/warm-midnight/final-finance-node.log` |
| `node scripts/browser-warm-midnight-check.cjs` | 457 Prüfungen, 36 Axe-Scans, 40 Aufnahmen | `outputs/warm-midnight/acceptance/report.json` |
| `node scripts/browser-module-polish-check.cjs` | 216 Prüfungen, 1 Axe-Scan, 8 Aufnahmen | `outputs/deep-polish-review/acceptance/browser-module-polish-report.json` |
| `node scripts/browser-theme-check.cjs` | 204 Prüfungen, 60 Axe-Scans | `outputs/ocean-design-review/browser-theme-report.json` |
| `node scripts/browser-navigation-tray-check.cjs` | 400 Prüfungen, 16 Axe-Scans | `outputs/warm-midnight/tray/report.json` |
| `node scripts/browser-notification-settings-check.cjs` | 512 Prüfungen, 18 Axe-Scans | `outputs/warm-midnight/notifications/report.json` |
| `node scripts/browser-settings-security-language-check.cjs` | 209 Prüfungen, 34 Axe-Scans | `outputs/warm-midnight/users/report.json` |
| `node scripts/browser-audit-check.cjs` | 16 Prüfungen, 2 Axe-Scans | `outputs/security-review/browser-audit-report.json` |
| Weitere Shopping-/Recipe-/Cooking-/Pantry-/Finance-/Fuel-/Planning-/Chores-/Meters-/Documents-Suiten | bestanden, Einzelzahlen und Abweichungen dokumentiert | [Phasencheckliste](warm-midnight-rollout.md) |
| Hallmark `sloplint.mjs` auf sechs geänderten CSS-Dateien, `--scope app --json` | 0 FAIL, 0 REFLEX, 0 WARN | `outputs/warm-midnight/hallmark-static.json` |
| `git diff --check`, `node scripts/check-local-artifacts.cjs --index` | bestanden | Abschlussprüfung; nichts gestagt |
| Gitleaks `dir` auf Kopie ausschließlich geänderter/neuer Quelldateien, `--redact=100` | keine Funde | `outputs/warm-midnight/gitleaks.json` |

Der unabhängige Hallmark-Reviewer hat alle 17 Sollbilder und reale Haushalts-/Admin-/Tray-Aufnahmen angesehen. Ein Finish-Befund wurde behoben: Der Finanzchart erklärt jetzt horizontales Verschieben und Tastatursteuerung, wenn sein Inhalt breiter als der sichtbare Bereich ist. Exakte Zahlen und die Tabelle blieben unverändert. Sichtbarkeit und echtes ArrowRight-Scrolling wurden bei 1440/390/320 geprüft.

## Ausdrücklich nicht geprüft

- Physische Push-Zustellung auf Windows, Android oder iOS; die erreichbare HTTPS-Installation steht weiterhin aus.
- Physische WebAuthn-Hardware. Der echte Serverablauf wurde mit einem CDP-Authenticator geprüft.
- Manuelle Screenreader-Nutzung und Zoom über das Betriebssystem-/Browsermenü. Axe, Tastatur, Forced Colors, Reduced Motion und 200%-Reflow-Äquivalent wurden geprüft.
- Hallmarks eingebauter Renderadapter: `--render` wurde aufgerufen, konnte ohne `puppeteer-core` aber nicht starten. Kein Pass für diesen Adapter; Playwright/Edge und unabhängige Sichtprüfung lieferten die Renderbelege.
- Vollständige Go-Integration-/Race- und Fuel-OCR-/Backup-Wiederherstellungskampagnen wurden für diese UI-Änderung nicht wiederholt. Die reale PDF-Verarbeitung und die dokumentierten PostgreSQL-gestützten Browserabläufe wurden ausgeführt.

## Änderungsumfang

Gemeinsame CSS-Rollen, Modul- und Admin-Layouts, lokale PWA-Icons, kleine Template-Anpassungen, DE/EN-Texte und Testvorbereitung. Die Kochaktion ist am Rezeptkopf erreichbar; der Finanzchart hat einen Scrollhinweis. Keine neuen Abhängigkeiten, Datenbankschemata, Berechtigungen oder Offline-Caches. `DESIGN.md`, `.impeccable/design.json`, `PRODUCT.md` und `README.md` beschreiben dieselbe implementierte Gestaltung. Der Versionskontroll-Diff bleibt lokal auf `dev`.

## Lokaler Betrieb und Aufräumen

`docker build --target ocr` und die lokale Compose-Neuerstellung von `app` und `backup` wurden ausgeführt. `http://localhost:8100/health/ready` liefert 200, die App ist healthy, der Backup-Dienst läuft. Drei ausgelieferte gehashte Assets stimmen bytegenau mit dem Quellstand überein; das Manifest trägt die neuen Farben. Datenbankcontainer, benannte Volumes und Umgebungsdatei sind unverändert. Beleg: `outputs/warm-midnight/local-docker.json`.

Die vorherige Testdatenbank wurde aus dem geprüften Archiv wiederhergestellt und strukturell geprüft. Beide eigenen Prüfcontainer, ihre temporären Image-Tags und das private Cross-Build-Verzeichnis wurden entfernt. Eine geprüfte Sicherung, die ignorierten Prüfbelege und ein lokales Rollback-Image bleiben erhalten. Beleg: `outputs/warm-midnight/cleanup.json`. Es wurden keine fremden Container oder gemeinsamen Abhängigkeiten/Build-Caches bereinigt.
