# Plan: Gerätebenachrichtigungen und sichere Zustellung

Stand: 2026-11-10 · **Phasen A–E einschließlich Essensplan N44 automatisiert geprüft. Phase F: Schlüsselverwaltung, Sicherheitsereignisse und Wiederherstellung sind implementiert; die vollständige Abnahme läuft.** Physische Gerätetests und die abschließende lokale Docker-Aktualisierung bleiben offen. Fortschritt und Nachweise: [Implementierungsvertrag](notifications-implementation.md).

## 1. Ziel und empfohlene Reihenfolge

Household erhält persönliche Benachrichtigungen für Windows, Android und iOS auf Grundlage des vorhandenen Web-Push-Dienstes. Zuerst werden Gerätefreigabe, Diagnose und gemeinsame Zustellregeln vervollständigt; danach folgen Aufgaben, Kochtimer und anschließend die weiteren Module. Die In-App-Inbox bleibt ohne Gerätefreigabe nutzbar. Für den Start sind weder ein zusätzlicher Broker noch Redis, Firebase-SDK, CDN oder ein kostenpflichtiger Dienst erforderlich.

**Verbindliche Produktgrenze:** Web Push ist eine bestmögliche Zustellung, kein garantierter Wecker. Ein gesperrtes, ausgeschaltetes, offline befindliches oder vom Betriebssystem eingeschränktes Gerät kann eine Meldung verspätet oder gar nicht anzeigen. Kochtimer bleiben in der geöffneten App nutzbar; für einen zwingend pünktlichen Alarm ist zusätzlich der Gerätewecker erforderlich. Eine native Begleit-App wäre ein eigenes Folgeprojekt, falls zuverlässige lokale Hintergrundalarme zur Muss-Anforderung werden.

Der Benutzer hat die Umsetzung in der empfohlenen Reihenfolge einschließlich der empfohlenen Standardwerte freigegeben. Die Implementierung aktiviert nicht automatisch Push für bestehende Benutzer und rotiert keine bestehenden Schlüssel ohne den getesteten Migrationsweg. Jede Installation konfiguriert ihren eigenen öffentlichen HTTPS-Origin; eine persönliche Domain wird nicht im Code festgeschrieben. Windows-, Android- und iOS-Testgeräte stehen laut Benutzer zur Verfügung. Am 10.11.2026 hat der Benutzer bestätigt, dass noch keine erreichbare HTTPS-Adresse existiert und die reale Geräteabnahme ausdrücklich offen bleiben soll. N23/N30/N51 werden daher nicht als bestanden markiert; automatisierte Abnahme und lokales Docker-Update laufen weiter.

## 2. Geprüfter Ist-Stand und Lücken

Die Angaben stammen aus dem vorhandenen Quellcode, nicht aus vermuteten Produktfähigkeiten. Es wurden keine privaten Schlüssel ausgelesen, keine Live-Abonnements abgefragt und keine Gerätebenachrichtigungen versendet. Physische Zustellung auf Windows/Android/iOS wurde für diesen Plan nicht getestet.

| Bereich | Im Quellcode vorhanden | Geplante Ergänzung |
|---|---|---|
| Gerätekanal | Optionaler Web Push; Zustände `disabled`, `not_configured`, `ready`; ausdrückliche Browserfreigabe und persönliche Registrierung | Einheitliche Geräteansicht mit verständlicher Diagnose, Testmeldung und Kategorien pro Gerät |
| Einkauf | Persönliche Listenbeobachtung, In-App-Hinweise, zusammengefasste Push-Jobs | Bestehendes Verhalten in gemeinsamen Vertrag einbinden; kein erneutes Erfinden der Listenlogik |
| Push-Nachrichtenformat | Backend und Service Worker akzeptieren ausdrücklich nur `shopping_notice`; Text und Ziel sind fest vorgegeben | Versioniertes, streng erlaubtes Format für weitere Kategorien; alter Einkaufsvertrag bleibt während des Übergangs lesbar |
| Aufgaben | Serien/Termine, In-App-Tageszusammenfassung, persönliche Zeitzone/Ruhezeiten und ausdrücklich erlaubte Eskalationen | Gerätekanal für dieselben Regeln; opt-in für einzelne Fälligkeiten; keine doppelte Präferenzlogik |
| Kochen | Gespeicherte Sitzungen und Timer mit Deadline, Version, Pause/Fortsetzen/Bestätigen; Anzeige anhand Serverzeit und monotoner Browserzeit | Dauerhafte Zustellabsicht je Timerrevision, kurze Gültigkeit und gerätebezogene Auswahl |
| Timeranzeige | `cooking.js` meldet Ablauf im geöffneten Kochbereich; kein Timer-Push-Producer im geprüften Pfad | Vordergrundsignal, optionale Soundfreigabe und ergänzender Web Push; keine Hintergrundgarantie |
| Vorrat/Dokumente | Core-Erinnerungen, Ablaufdaten, Mindestbestandsmeldungen bzw. Dokumentfristen erzeugen In-App-Hinweise | Gruppenweise gerätebezogene Zustellung mit Datensparsamkeit |
| Andere Module | Kein allgemeiner Gerätekanal aus beliebigen Modulereignissen; der untersuchte Push-Sender ist auf Einkauf begrenzt | Explizite Adapter und Abnahmetests pro Modul; vorhandene Fachregeln erst prüfen und wiederverwenden |
| Dauerhafte Verarbeitung | `core.outbox_events`, `event_deliveries`, `jobs`, `reminders`, `notifications`; geleaste Jobs, Deduplizierung und Modulgenerationen | Gemeinsamer Notification-Consumer mit widerrufbaren Zustellabsichten und personenbezogener Berechtigungsprüfung |
| Ressourcengrenzen | Push max. 5 Geräte, 2 parallele Sends, HTTP-Timeout 4 s; persistente Budgets 60/Installation/Minute und 20/Benutzer/Minute | Grenzen zunächst beibehalten; Tests für Burst-Verteilung und reservierte Timerkapazität |
| Zeitplanung | Allgemeiner Worker tickt alle 30 s | Kleine unabhängige Deadline-Schleife für Kochtimer, ohne alle Housekeeping-Schritte sekündlich auszuführen |
| Gerätebindung | Verschlüsselte Registrierung an Benutzer, Haushalt, Sitzung, Auth-Version, Rolle, VAPID-Key und Einwilligung gebunden; maximal 30 Tage | Laufzeit sichtbar machen, absichtliche Wiederverbindung; kein stilles Umgehen der Sitzungsbindung |
| Datenschutz | Einkaufspayload enthält Referenzen, Ablauf und Audience-Scope; generischer Sperrbildschirmtext | Generischer Text bleibt Standard für alle Kategorien; Inhaltsvorschau erst separat freigeben |
| Kryptografie | AES-GCM für gespeicherte Geheimnisse; separate Encryption-/CSRF-/Rate-Secrets; bcrypt Kostenfaktor 12; Passkeys/TOTP/Recovery | Versionierte Schlüsselrotation und bereichs-/datensatzgebundene Verschlüsselungsformate als eigene Sicherheitsphase |

Quellanker: [Web-Push-Service](../internal/webpush/service.go), [Subscriptions](../internal/webpush/subscriptions.go), [Send-Jobs](../internal/webpush/jobs.go), [Transport](../internal/webpush/transport.go), [Budgets](../internal/webpush/budget.go), [Service Worker](../internal/web/static/push-worker.js), [Geräte-UI](../internal/web/static/push-device.js), [Worker](../internal/worker/worker.go), [Aufgaben-Worker](../internal/store/chores_worker.go), [Kochtimer-Mutationen](../internal/store/cooking_mutation.go), [Kochansicht](../internal/web/static/cooking.js), [Core-Ausgangsschema](../internal/database/migrations/core/000001_init.up.sql).

### Begrenzte Datenbank-/Sicherheitsprüfung

In den untersuchten Push-Pfaden werden SQL-Parameter, Zeitlimits, begrenzte Listen, kurze Transaktionen und Lease-Tokens verwendet. Der Versand erfolgt nach dem Commit der Berechtigungs-/Budgetreservierung; Netzwerkzugriffe halten damit nicht die vorherige Datenbanksperre. Der Endpunkt wird erneut geprüft; DNS-Auflösung und Dialer verweigern nicht öffentliche Adressen, Redirects und Proxy-Nutzung. Das sind zu erhaltende Eigenschaften, keine Aussage über eine vollständig geprüfte Anwendung.

Gezielte Erweiterungslücken: einkaufsspezifisches Payload-/Audience-Modell, keine Timerzustellung, 30-s-Haupttakt, kein allgemeines Preference-/Delivery-Modell, keine überlappende Encryption-Key-Rotation. Die heutige Verschlüsselung nutzt das globale AAD `household-v1`; die Subscription bindet ihre Identitäten zusätzlich im verschlüsselten Envelope und prüft diese beim Öffnen. Das ist nicht gleichbedeutend mit einem bereits implementierten, datensatzspezifischen AEAD-AAD oder Schlüsselring. Die automatische Erneuerung, tatsächliche Providerreichweite und konkrete Host-Konfiguration wurden hier nicht operativ geprüft.

## 3. Plattformen und ehrliche Zusagen

| Ziel | Unterstützter Weg | Einrichtung und Einschränkungen |
|---|---|---|
| Windows | Installierte PWA in aktuellem Edge als erste Abnahmeplattform; aktuelles Chrome zusätzlich | HTTPS, aktiver Service Worker, Browserfreigabe und Windows-Benachrichtigungseinstellungen prüfen. Fokus/Nicht stören, beendeter Browserhintergrund und Energiesparen getrennt testen. Eine geschlossene PWA ist nicht mit einem vollständig beendeten Browserprozess gleichzusetzen. |
| Android | Installierte PWA in aktuellem Chrome; weitere Browser nach Featureprüfung | HTTPS, Push-/Notification-Unterstützung und Browser-/OS-Freigabe. Akkuoptimierung/Doze, Herstellerbeschränkungen und Netzwechsel können Zustellung beeinflussen. Installation für den Produktablauf empfohlen, nicht als allgemeine Web-Push-Protokollpflicht dargestellt. |
| iOS/iPadOS | Zum Home Screen hinzugefügte Web-App ab iOS/iPadOS 16.4; Abnahme auf aktuell unterstütztem System | App vom Home Screen öffnen; Berechtigungsanfrage nur nach direkter Benutzeraktion. Ein normaler Safari-Tab ersetzt diesen Ablauf nicht. OS-Fokus und Benachrichtigungseinstellungen gelten auch für die PWA. |
| Ohne Push/abgelehnt | In-App-Inbox und sichtbare Timer | Keine wiederholten Erlaubnis-Popups. Schritte zur OS-/Browserfreigabe und weiterhin verfügbare Funktionen anzeigen. |

Microsoft dokumentiert Notifications/Badges und die Berechtigungsabfrage nach Benutzeraktion. Mobile Browser sollen über `ServiceWorkerRegistration.showNotification()` bedient werden; HTTPS ist erforderlich. [Microsoft-PWA-Dokumentation](https://learn.microsoft.com/en-us/microsoft-edge/progressive-web-apps/how-to/notifications-badges), [MDN Notifications](https://developer.mozilla.org/en-US/docs/Web/API/Notifications_API/Using_the_Notifications_API).

Apple dokumentiert Home-Screen-Web-Push ab 16.4, die direkte Benutzeraktion und Fokusintegration; ein kostenpflichtiges Apple-Developer-Konto ist dafür nicht erforderlich. [WebKit: Web Push für iOS/iPadOS](https://webkit.org/blog/13878/web-push-for-web-apps-on-ios-and-ipados/).

Push kann den Service Worker bei nicht geöffneter App aktivieren. Daraus folgt keine Frist- oder Zustellgarantie. Android dokumentiert CPU-/Netzbeschränkungen durch Doze; diese Plattforminformation ist keine Zusage, dass eine Web-App native FCM-Ausnahmen oder Alarmberechtigungen steuern kann. [MDN Push API](https://developer.mozilla.org/en-US/docs/Web/API/Push_API), [Android: Doze und App Standby](https://developer.android.com/training/monitoring-device-state/doze-standby).

**Kein Einsatz der eingestellten Notification-Triggers-API**, kein `setTimeout()` im Service Worker als dauerhafter Alarm und kein Periodic Background Sync als Uhr. Chrome hat die Entwicklung von Notification Triggers wegen fehlender konsistenter Zuverlässigkeit beendet. [Chrome: Notification Triggers](https://developer.chrome.com/docs/web-platform/notification-triggers).

Alle Plattformzustände werden über Feature Detection festgestellt. Versionsnummern allein sind kein Ersatz für `isSecureContext`, Service-Worker-/Push-/Notification-Prüfung und einen bewusst ausgelösten Gerätetest. Badges, Aktionsbuttons, Sounds und Vibration sind progressive Ergänzungen; jede Meldung muss auch als einfacher Text mit Öffnen-Aktion funktionieren.

## 4. Empfänger und Auslöser je Modul

Die folgenden Trigger sind **Zielverhalten**, sofern in Abschnitt 2 nicht ausdrücklich vorhanden. P0 = Fundament, P1 = erste Ausbaustufe, P2 = anschließende Modulfreigabe. Kein automatisches Abonnieren bestehender Benutzer bei einer Migration.

| Modul | Geplante Auslöser | Empfänger und Standard | Frische-/Spamregel | Priorität |
|---|---|---|---|---|
| Aufgaben `chores` | Fälligkeit, persönliche Tagesübersicht, ausdrücklich aktivierte Überfälligkeit/Eskalation | Zuständige Person; Vertretung anhand aktueller Aufgabenregel; Eskalation nur an autorisierte und einwilligende Empfänger | Pro Vorkommen/Revision/Stufe; Erledigen, Übertragen, Pausieren und Serienänderung entwerten offene Meldungen | P1 |
| Rezepte/Kochen `recipes` | Timerablauf; optional Warnung, dass ein gestarteter Timer nicht serverseitig bestätigt wurde | Startender Benutzer, zunächst gewähltes Küchengerät; weitere Geräte bewusst aktivierbar | Kurze Gültigkeit; Pause/Neustart/Bestätigung/Sitzungsende entwerten alte Revision | P1 |
| Einkauf `shopping` | Bestehende beobachtete Listenänderungen; optional geplanter Einkauf | Listenbeobachter; eigene Änderungen wie bisher ausnehmen | Bestehende Sammelfenster, Stummschaltung und Watch-Revision erhalten | P1, Regression |
| Vorrat `pantry` | Mindestbestand, baldiges Mindesthaltbarkeits-/Verbrauchsdatum | Freiwillige Verantwortliche; tägliche Zusammenfassung bevorzugt | Eine Meldung pro Bestandsmangel-Episode; verbrauchte/entfernte Charge ausschließen | P2 |
| Fahrzeuge `fuel` | Service-/Prüftermine, Versicherungs-/Vertragsfristen, kilometerbasierte Wartung bei ausreichend aktuellen Daten | Explizite Fahrzeugzuständigkeit; Wartungspläne ausschließlich an ihre eigene zugeordnete Person | Einmal je Friststufe; archivierte Fahrzeuge ausschließen; Kilometerunsicherheit kennzeichnen | P2 |
| Finanzen `expenses` | Bestätigte Zahlungsfälligkeit, Budgetschwelle, freigegebene Abrechnung | Nur tatsächlich berechtigte Konten-/Finanzbereichsteilnehmer | Keine privaten Beträge, Konten oder Schuldner im Standard-Push; Schwellen mit Hysterese; erledigte Zahlung entwertet Reminder | P2 |
| Zähler `meters` | Ablesetermin, ausdrücklich definierte Verbrauchsabweichung, veraltete Adapterdaten | Berechtigte Verantwortliche | Messlücken nicht als Verbrauchsspitze interpretieren; Cooldown und Entwarnung statt Daueralarm | P2 |
| Dokumente `documents` | Ablauf, Garantie, Vertragskündigungs-/Wiedervorlagefrist; optional fehlgeschlagener Import | Nutzer mit aktueller Dokumentberechtigung; operative Importfehler nur Zuständige | Generischer Hinweis; kein Dokumenttitel/Anhang im Push; Löschung und Friständerung widerrufen Pending-Jobs | P2 |
| Essensplan `mealplan` | Geplante Zubereitungszeit, vorbereitender Schritt/Auftauen, relevante Planänderung | Teilnehmer/Verantwortliche, keine stillschweigende Haushalt-Rundsendung | Tagesdigest bevorzugt; Rezept- und Planänderungen ersetzen alte Revision; kein Erfinden von Auftauzeiten | P2 |
| Core/Sicherheit | Neue Gerätefreigabe, Passkey-/TOTP-Änderung, Kennwortreset, Sitzungswiderruf | Betroffenes Konto; Betriebsstörung zusätzlich nur Administrator | Generisch, dedupliziert, keine OTPs/Recovery-Codes; explizite Sicherheitskategorie | P1/P2 |

Sicherheitsmeldungen können einen kompromittierten Account nicht alleine absichern. Ein unabhängiger, verifizierter E-Mail-Kanal ist ein optionales späteres Projekt; bestehende E-Mail-Infrastruktur wird nicht vorausgesetzt. Backupfehler/volle Datenträger brauchen zusätzlich einen externen Betriebsmonitor, weil ein ausgefallener Household-Server seinen eigenen Push nicht zuverlässig versenden kann.

**Konkretisierung für N33/N34:** Der Vorratsadapter übernimmt zunächst die bestehenden Ablauf-Erinnerungen: Verbrauchsdatum, sonst Mindesthaltbarkeitsdatum, jeweils ab 09:00 Uhr in der Haushaltszeitzone. Eine zusätzliche Vorwarnfrist ist damit noch nicht eingeführt. Mindestbestandshinweise beruhen auf einem tatsächlich beobachteten Wechsel von ausreichendem zu niedrigem physischem Bestand; ein bereits niedriger Anfangsbestand bleibt stumm. Einwilligung muss vor der letzten ausreichenden Beobachtung liegen. Kurze Änderungen vollständig zwischen zwei Prüfungen können unbemerkt bleiben. Jede Person und jedes Gerät wählen den Kanal ausdrücklich. Eine Tagesgruppe endet spätestens um Mitternacht ihrer gewählten Zeitzone; Ruhezeiten dürfen sie nicht in den nächsten Tag verschieben. Jede Quelle behält zusätzlich ihre ursprüngliche Frische von höchstens 24 Stunden. Bestände, Datum, Berechtigung und Einwilligung werden vor dem Versand erneut geprüft. Die vollständige automatische Modulabnahme steht unter N34; physische Geräte bleiben separat unter N51 offen.

## 5. Bedienkonzept und Zustände

### Einstieg und kompakte Ansicht

Im seitlichen Einstellungstray kommt ein eigener Punkt **Benachrichtigungen / Notifications** hinzu. Die Glocke öffnet weiterhin die persönliche Inbox. Die vorhandenen Regeln für Navigation, Abmelden und persönliche Modulreihenfolge bleiben maßgeblich.

```text
Benachrichtigungen                     [Alle als gelesen]
Persönliche Einstellungen             Dieses Gerät
─────────────────────────────────────────────────────────
In-App-Hinweise   Aktiv                Küche · Android
Gerätehinweise    Noch nicht erlaubt   [Auf diesem Gerät aktivieren]
Sperrbildschirm   Inhalte ausblenden   [Testnachricht senden]
Ruhezeit         22:00–07:00           Status: Registrierung bestätigt
Zeitzone         Europe/Vienna        Letzter Test: noch nicht bestätigt

Kategorie          In App   Geräte    Häufigkeit
Aufgaben           ✓        ○         Tagesübersicht
Kochtimer          ✓        ○         Bei Ablauf
Einkauf            ✓        ○         Zusammengefasst
Weitere Module …

Eigene Geräte                          [Gerät entfernen]
Küche · Android · aktuelle Sitzung · Freigabe bis …
Laptop · Windows · erneute Verbindung erforderlich
```

Auf Mobilgeräten stehen diese Bereiche untereinander; kein breites Formularraster. Ocean/Pink, hell/dunkel/system, DE/EN, Fokusführung, Screenreader-Beschriftungen, mindestens 44-px-Ziele und reduzierte Animationen werden übernommen. „Auf diesem Gerät aktivieren“ ist eine bewusste Aktion. Der Browserdialog erscheint weder beim Seitenaufruf noch nur wegen eines neuen Timers.

### Persönliche und gerätebezogene Regeln

- Konto: aktivierte Kategorien, sofort/Zusammenfassung/aus, Ruhezeiten, IANA-Zeitzone, Vorschauprivatsphäre und optionale Sicherheitsmeldungen. Aufgabenpräferenzen bleiben für Aufgaben die fachliche Quelle.
- Gerät: Anzeigename, ausgewählte Kategorien, „Kochtimer auf diesem Gerät“, pausiert/aktiv, Freigabeablauf und Entfernen. Eine Geräteauswahl darf eine konto- oder modulweite Sperre nicht aufheben.
- Neue Kategorie: standardmäßig kein externer Versand. In-App-Verhalten wird nur verändert, wenn dies ausdrücklich Teil der jeweiligen Modulfreigabe ist.
- Kochtimer: einmalige Auswahl „Diesen Timer zusätzlich auf diesem Gerät melden“ oder bewusst gespeicherte Vorwahl. Ruhezeiten dürfen dafür nur ausdrücklich innerhalb Household übergangen werden; OS-Fokus bleibt unangetastet.
- Ablehnung: sachliche Anleitung zum Freigeben in Browser-/OS-Einstellungen. Keine erneute Aufforderungsschleife. In-App-Funktionen bleiben sichtbar.
- Identitätswechsel/Logout: lokale Zuordnung, passende bereits sichtbare Household-Notifications und persönliche Caches entfernen; fremdes Konto niemals mit einer alten Subscription verbinden. Abgelaufene Sitzung verlangt erneute Anmeldung und bewusste Registrierung.
- Test: nur eigenes Gerät, festes generisches Testformat, eigener Rate-Limit-Bucket. Zwischen „Server registriert“, „Push-Dienst angenommen“, „Service Worker hat Anzeige angefordert“ und „von dir bestätigt gesehen“ unterscheiden. Ein SW-Acknowledgement beweist nicht die tatsächlich sichtbare OS-Anzeige.
- Diagnose enthält HTTPS/Installation/API/Permission/Service-Worker/Serverkonfiguration/Registrierung/letzten Fehlercode; niemals Endpoint, Schlüssel, Token oder vollständigen User-Agent. Testdruck verändert keine Modulabonnements.

## 6. Architektur und zeitliche Korrektheit

```mermaid
flowchart LR
  M[Modulaktion oder gespeicherte Deadline] --> T[Transaktion: Fachdaten und Core-Ereignis]
  T --> O[Vorhandene Outbox und Reminder]
  O --> C[Versionierter Notification-Consumer]
  C --> A[Aktuelle Berechtigung und Präferenzen]
  A --> I[Persönliche In-App-Inbox]
  A --> J[Zustellabsicht und vorhandene Core-Jobs]
  J --> W[Begrenzter Worker mit Lease und Budget]
  W --> R[Erneute Prüfung vor Versand]
  R --> P[Verschlüsselter Web Push über Provider]
  P --> S[Service Worker: Format, Audience, Ablauf]
  S --> N[Generischer OS-Hinweis]
  N --> L[App öffnen und erneut autorisieren]
```

### Verträge und Zustellung

1. **Fachliches Eigentum:** Jedes Modul bestimmt Ereignis, Ressource, Revision, gültige Empfänger und Frische über einen expliziten Core-Vertrag. Keine beliebigen Tabellen-/Spaltennamen aus Jobs und keine neuen schemaübergreifenden Fach-Fremdschlüssel.
2. **Atomare Absicht:** Fachmutation und Ereignis/Reminder werden in derselben PostgreSQL-Transaktion gespeichert. Der Versand erfolgt außerhalb der Transaktion. Reconciliation repariert fehlende Zustellabsichten nach Neustarts, ohne fachliche Aktionen erneut auszuführen.
3. **Berechtigung:** Bei Erzeugung, Claim, unmittelbar vor dem externen Request und beim Öffnen des Ziels prüfen: Haushalt, aktive Mitgliedschaft, Modul aktiv/Generation, Rolle, Ressourcensichtbarkeit, Benutzerstatus, Sitzung/Auth-Version, Geräteeinwilligung und aktuelle Revision. Finanz- und Dokumentregeln bleiben verbindlich; „Reader“ bedeutet nicht Zugriff auf sämtliche Ressourcen.
4. **Deduplizierung:** Stabiler Schlüssel aus Haushalt, Modulgeneration, Ressource, fachlichem Vorkommen, Revision, Stufe, Empfänger und Kanal; pro Gerät zusätzliche Subscription-ID. Datenbank-Unique-Constraint entscheidet bei Konkurrenz. Topic/Notification-Tag unterstützen Ersetzung; sie garantieren keine globale Exactly-once-Anzeige.
5. **Zustände:** Geplant → bereit → beansprucht → Gateway angenommen oder erneut versuchen/abgelaufen/aufgehoben/fehlgeschlagen. „Gelesen“, „Timer bestätigt“ und „fachlich erledigt“ sind getrennte Aktionen. Öffnen eines Hinweises darf keine Aufgabe abschließen oder Zahlung verbuchen.
6. **Crash-Fenster:** Nach Providerannahme und vor DB-Commit kann ein Worker abstürzen. Retry ist damit potenziell doppelt; stabile Tags und Dedupe begrenzen die Folgen. Dies ist At-least-once-Verarbeitung mit Wiederholschutz, keine Exactly-once-Zustellung.
7. **Widerruf:** Änderung/Erledigung/Löschung/Modulpause entwertet offene Jobs anhand Revision und Generation. Bereits beim Provider angenommene oder angezeigte Hinweise können nicht verlässlich auf allen Geräten zurückgerufen werden. Kurze TTL, generische Texte, lokale Aufräumung und aktuelle Prüfung beim Öffnen begrenzen dieses unvermeidbare Rennen.
8. **Sicherer Client:** Push enthält nur erlaubte Kategorie, Schema, Referenz, Revision, Audience und Ablauf; keine frei wählbare URL, HTML, Bilder oder API-Route. Ziel wird aus einer serverseitig erlaubten Route bestimmt. Ein optionaler Statusabruf darf nur einen festen Same-Origin-Endpunkt nutzen und muss zeitlich begrenzt sein. Offline wird keine sicherheitskritische Aktion vorgenommen.
9. **Keine stillen Sync-Nachrichten als Grundlage:** Benutzerseitige Zustellfreigabe und sichtbarer Benachrichtigungszweck bleiben erhalten. Ungültige/abgelaufene Payloads werden verworfen; Badges und serverseitiger Zustand dürfen nicht auf unbeschränkte stille Hintergrundausführung angewiesen sein.

### Timer, Neustart und Zeitumstellung

- Vorhandene `recipes.cooking_timers.deadline`, `version` und `remaining_millis` bleiben maßgeblich. Start/Fortsetzen erzeugt eine passende Absicht; Pause/Entfernen/Bestätigen/Sitzungsabschluss entwertet diese atomar. Ein alter Send-Job darf keinen neu gestarteten Timer quittieren.
- Browser rendert aus absoluter Serverdeadline plus monotoner Laufzeit, nicht aus heruntergezählten Sekunden. Nach Resume/Fokus/Uhrsprung wird neu synchronisiert. Sound wird erst nach Benutzeraktion freigeschaltet; kein garantierter Alarmton bei gesperrtem Gerät.
- Eine einzelne begrenzte Deadline-Schleife beansprucht fällige Timer per Index und Lease. PostgreSQL bleibt Zeitquelle. Unter aktiven Timern darf sie etwa sekündlich prüfen; ohne Timer schläft sie bis zum nächsten begrenzten Prüfzeitpunkt. Keine Goroutine pro Timer, keine sekündliche Vollausführung des bestehenden Workers. Ein DB-Notify wäre nur ein Aufweckhinweis mit Polling-Fallback.
- Vorgeschlagenes internes Ziel: unter kontrollierter Last p95 höchstens 3 s zwischen Deadline und Beginn des Providerrequests; gesondert messen. Das ist keine Zusage für den Empfang oder OS-Alarm.
- Vorgeschlagene Timerfrische: bis 60 s nach Deadline, maximal zwei Versuche innerhalb dieses Fensters, Provider-TTL höchstens Restfrische. Danach nur „Timer während Abwesenheit abgelaufen“ in der App; kein verspätetes wiederholtes Klingeln.
- Aufgaben: absolute Termine als UTC-Instant; wiederkehrende lokale Uhrzeit zusätzlich mit IANA-Zeitzone. Vorschlag für DST: nicht vorhandene lokale Zeit auf den ersten gültigen Zeitpunkt danach verschieben; doppelte lokale Zeit nur beim ersten Auftreten auslösen. Einstellungstext und Tests müssen diese Regel bestätigen. Bestehende Seriensemantik vor einer Änderung abgleichen.
- Tageszusammenfassungen verwenden einen lokalen Tages-/Empfängerschlüssel. Zeitzonenwechsel erzeugt keine zweite Zusammenfassung desselben bereits abgearbeiteten Ereignisses. Ruhezeiten über Mitternacht und DST werden als lokale Intervalle berechnet.
- Offline gestarteter Timer wird nur dann als lokal bestätigt dargestellt, wenn dieser Ablauf bewusst umgesetzt wurde. Bis zum bestätigten Server-Commit steht sichtbar „Nur dieses geöffnete Gerät / Gerätehinweis nicht eingerichtet“. Reconnect spielt keinen bereits abgelaufenen Alarm nach.

### Retry, Fairness und Aufbewahrung

HTTP 404/410 deaktiviert ausschließlich die tatsächlich verwendete Subscription-Version. 429 beachtet begrenztes `Retry-After` und den bestehenden persistenten Provider-Cooldown. Netzwerkfehler/5xx erhalten begrenztes exponentielles Backoff mit Jitter; 401/403 lösen eine Betreiberdiagnose statt endloser Wiederholung aus. Bei jeder Wiederholung gelten Ablauf, Einwilligung und Berechtigung erneut.

Die vorhandene Versandparallelität 2 und Budgets 60/Installation sowie 20/Benutzer pro Minute bleiben zunächst bestehen. Eine faire Auswahl reserviert einen Anteil für kurze Timer und Sicherheitsmeldungen, ohne normale Hinweise dauerhaft zu verdrängen. Bei erschöpftem Budget zeigt die UI keine garantierte Zustellung. Normale Hinweise werden gebündelt; Wiederanlauf nach langer Ausfallzeit produziert keinen Nachrichtensturm.

Vorgeschlagene Frische: Timer 60 s, Aufgaben-Fälligkeit bis 1 h, Tagesdigest bis 4 h, sonstige Fristen maximal 24 h; jede Kategorie darf strenger sein. Frische endet spätestens bei fachlicher Überholung. Der Push-TTL-Header begrenzt die Provideraufbewahrung, nicht die historische Inbox. Provider dürfen eine kürzere Haltedauer wählen. [RFC 8030, TTL/Urgency/Topic](https://www.rfc-editor.org/rfc/rfc8030.html).

Aktuell umgesetzt: Die asynchrone Versandbereinigung entfernt terminale Zustellabsichten nach 30 Tagen und In-App-Hinweise nach 90 Tagen, jeweils in Batches von höchstens 128 Zeilen pro Löschabfrage. Die Bereinigung kann verzögert erfolgen. Eine separate 14-Tage-Historie einzelner Zustellversuche wurde nicht angelegt; Zähler und sichere Statuscodes dienen der Diagnose. Diese Fristen sind derzeit nicht durch eine Betreibereinstellung änderbar. Sicherheits-Audit und verschlüsselte historische Backups bleiben von diesem Cleanup unberührt und benötigen eine eigene Aufbewahrungsregel. Einrichtung, Domainwechsel und Datenschutz beschreibt der [Betriebsleitfaden](operations.md#device-notifications).

## 7. Vorgeschlagene Schemaergänzungen — keine Migration

Bestehende Tabellen werden erweitert, wo ihre Semantik passt. Die folgenden Namen/Felder sind ein Entwurf; endgültige Migrationen folgen erst nach Vertragsprüfung und Freigabe.

| Objekt | Vorgesehene Felder/Beziehungen | Wichtige Invarianten/Indizes |
|---|---|---|
| `core.notification_preferences` neu | `household_id`, `user_id`, `category`, `channel`, `mode`, `timezone`, `quiet_start/end`, `preview_policy`, `version`, `updated_at` | Eindeutig je Konto/Kategorie/Kanal; aktive Mitgliedschaft bei Mutation; fest erlaubte Kategorien und Modi; bestehende Aufgabenpräferenzen referenzieren statt kopieren |
| `core.notification_device_preferences` neu | Haushalts-/Benutzer-/Subscription-Bezug, Geräteanzeigename, erlaubte Kategorien, Pause, optionale Timer-Vorwahl, Version | Subscription muss demselben Haushalt/Benutzer gehören; kein frei setzbares fremdes Gerät; kurze Namen, begrenzte Kategorien |
| `core.notifications` ergänzen | `source_reference`, `source_revision`, `module_generation`, `expires_at`, optionale Kategorie-/Templateversion | In-App-Datensatz bleibt personenbezogen; eindeutige Dedupe; Index Benutzer/Haushalt/Zeit; keine Push-Erlaubnis allein durch Existenz dieser Zeile |
| `core.notification_intents` neu | ID, Notification-ID, Modul/Generation, Ressourcentyp/-ID als logische Referenz, Revision, Empfänger, Kategorie, `not_before`, `expires_at`, Status, Dedupe, Version | Fachdaten nicht duplizieren; keine FK in Modulschemas; erlaubte Statusübergänge; `expires_at > not_before`; partieller Fälligkeitsindex |
| `core.notification_deliveries` neu | Intent-/Subscription-ID, zugehörige Job-ID, Subscription-/Einwilligungsrevision, Gatewaystatus, Versuchszähler, sichere Fehlerklasse, Zeitpunkte | Ein Datensatz je Intent/Gerät; Job-Lease bleibt allein in `core.jobs`; „angenommen“ nicht „zugestellt“ nennen |
| `core.notification_attempts` optional | Delivery-ID, Versuch, Start/Ende, HTTP-Klasse, standardisierter Grund, nächste erlaubte Zeit | Keine Response-Bodies, Endpoints, Tokens, Titel oder IPs; maximal geplante Versuche; begrenzte Retention |
| `core.push_subscriptions` ergänzen | Formatversion, Encryption-Key-ID beibehalten, gegebenenfalls datensatzgebundenes Crypto-Envelope; sichere Diagnosezeiten | Endpoint nur verschlüsselt plus Hash zur Eindeutigkeit; keine Klartextkopie in Diagnosefeldern; 5-Geräte-Grenze atomar; Einwilligung nicht still verlängern |
| `core.reminders` / Modul-Timer | Bestehende Deadline-/Generations-/Dedupefelder wiederverwenden; revisionsgebundene Cancel-Verknüpfung ergänzen, falls Vertrag dies benötigt | Bestehende Reminder dürfen nicht gleichzeitig zwei Zusteller auslösen; Änderungen in einer Transaktion |
| `core.jobs` / `core.event_deliveries` | Neue feste Job-Kinds, Payloadversion, deduplizierter Consumer `notifications-v1`; vorhandene Lease-/Budgetfelder | Kein zweites Queue-System; externe Aufrufe ohne gehaltene Fachdatensperre; Eindeutigkeit je Event/Consumer |
| Schlüsselkonfiguration | Versionierte Schlüsselkennungen und erlaubte aktive/alte Entschlüsselungsschlüssel über Secret-Dateien | Datenbank enthält keine privaten Schlüssel; Konfiguration ist kein Klartext-Sicherheitseditor für normale Benutzer |

Alle Queries bleiben parametrisiert und haushalts-/benutzerspezifisch. Paralleltests müssen Dedupe, Caps, Deadlocks und Sperrreihenfolge prüfen. Lokale Datum-/Zeitzonenvalidierung erfolgt vor Speicherung; UTC-Zeitpunkte verwenden `timestamptz`. Audit zeichnet Freigabe, Widerruf, Kategorienänderung und Key-Rotation als sichere Metadaten auf. Geheimnisse und vollständige verschlüsselte Envelopes werden nicht im Änderungsverlauf repliziert.

Migrationen müssen alte Einkaufs-Subscriptions/Jobs und noch laufende Kochsitzungen erhalten. Der neue Consumer darf keine komplette historische Outbox als neue Gerätebenachrichtigung nachsenden. Ein expliziter Aktivierungs-Cutoff plus Einwilligungszeitpunkt verhindern dies. Das Release dokumentiert, welche alte Binary-Version das neue Schema noch lesen kann; ohne belegte Kompatibilität nur zusammengehörigen DB-/Datei-/Key-Snapshot zurückspielen.

## 8. Verschlüsselung und sichere Einstellungen

### Was tatsächlich geschützt wird

| Daten | Aktuell bzw. Ziel | Ehrliche Grenze |
|---|---|---|
| Kennwörter | Bestehendes bcrypt, Kostenfaktor 12; mindestens 14 Zeichen, maximal 72 Bytes erhalten | Hashing, keine reversible Verschlüsselung; niemals Passwort in Notifications/Audit |
| Sitzung/Recovery | Bestehende Hashes und einmalige Recovery-Nutzung, Widerruf/Step-up erhalten | Hashes sind kein verschlüsselter Datenspeicher; Cookie-/CSRF-/Same-Origin-Schutz bleibt zusätzlich notwendig |
| TOTP | Geheimnis mit AES-GCM verschlüsselt; Replay-Schutz und Bestätigung erhalten | Server kann bei Anmeldung entschlüsseln; Root-/Prozesskompromittierung ist dadurch nicht ausgeschlossen |
| Passkeys | Private Schlüssel beim Authenticator bzw. dessen geschütztem Synchronisierungsanbieter; Server hält öffentliche Credential-Daten und verschlüsselte Protokolldaten | Household besitzt keine biometrischen Daten und keinen Passkey-Privatschlüssel; „alles serverseitig verschlüsselt“ beschreibt Passkeys falsch |
| Push-Subscription | Endpoint und Empfängerschlüssel gemeinsam AES-GCM-verschlüsselt; SHA-256-Hash zur Eindeutigkeit; Envelope-Bindung erhalten | Benutzer-/Haushaltszuordnung, Status und Zeitpunkte bleiben sichtbare DB-Metadaten; Endpoint nicht loggen |
| Web-Push-Inhalt | Standardspezifische Payloadverschlüsselung nach RFC 8291 über die gepflegte bestehende Bibliothek | Geschützt zwischen Application Server und User Agent gegenüber dem Push-Relay; der Household-Server kennt den Inhalt |
| VAPID | Separater privater P-256-Schlüssel zum Signieren/Identifizieren des Application Servers | VAPID authentifiziert den Sender beim Provider; es ist nicht die Payloadverschlüsselung |
| HTTPS | Browser→Household und Household→Push-Provider, korrekt geprüfte Zertifikate | Ein selbstsigniertes oder ungültiges Handy-Zertifikat ist keine produktionsfähige Freigabe |
| App-/Browserdaten | Bisherige private Cache-/Logoutregeln beibehalten; nur nötige Device-Metadaten lokal | IndexedDB/localStorage/Cache sind keine durch Household verschlüsselte Datenbank; OS-Geräteschutz vorausgesetzt |
| Fachdaten/Uploads | Sichtbarkeitsregeln, minimierte Logs, verschlüsselte Backups, Host-/Volumeverschlüsselung als Betreibermaßnahme | PostgreSQL-Fachdaten und Uploads sind nicht pauschal anwendungsseitig Ende-zu-Ende verschlüsselt |

RFC 8291 beschreibt die vertrauliche, integritätsgeschützte Web-Push-Payload; RFC 8292 die separate VAPID-Senderidentität. Provider kennen weiterhin Endpunkt, Absender, Zeitpunkt, Frequenz und Größen-/Routingmetadaten. Daher keine Behauptung einer vollständig selbst gehosteten Zustellinfrastruktur oder einer Ende-zu-Ende-verschlüsselten gesamten Household-Anwendung. [RFC 8291](https://www.rfc-editor.org/rfc/rfc8291), [RFC 8292](https://www.rfc-editor.org/rfc/rfc8292).

**Sperrbildschirm:** Standard „Household · Eine Erinnerung ist verfügbar“. Kategoriebezogene generische Texte wie „Ein Kochtimer ist abgelaufen“ nur entsprechend persönlicher Datenschutzwahl. Rezept-/Aufgabennamen, Geldbeträge, Dokumenttitel und Personen werden standardmäßig nicht übertragen. Ein späteres Inhaltspreview benötigt eine eigene ausdrückliche Konto-/Gerätefreigabe und erneute Ressourcenprüfung; ein OS-Screenshot oder physischer Blick auf den Bildschirm wird durch Payloadverschlüsselung nicht verhindert.

### Übernommene und bewusst angepasste Referenzmuster

Öffentliche GitHub-API-Abfrage am 2026-11-03 bestätigt als aktuellen `main`:

- [Treckrr `86c70c3305ba60d0742a4b60dafc390c4da7a69b`](https://github.com/d0linger/Treckrr/tree/86c70c3305ba60d0742a4b60dafc390c4da7a69b), Commitzeit 2026-11-02 09:03:27 UTC.
- [Parkrr `d6368e8d0d7bb29505a98fa1235c3809c4179cc3`](https://github.com/d0linger/Parkrr/tree/d6368e8d0d7bb29505a98fa1235c3809c4179cc3), Commitzeit 2026-11-02 08:52:19 UTC.

| Tatsächlich geprüftes Muster | Household-Entscheidung |
|---|---|
| Treckrr verschlüsselt mit AES-GCM und zufälliger Nonce; Kennwörter via bcrypt | Beibehalten, aber Households stärkere bestehende Kennwortlänge/Kostenfaktor nicht auf Referenzwerte absenken. [Treckrr Auth](https://github.com/d0linger/Treckrr/blob/86c70c3305ba60d0742a4b60dafc390c4da7a69b/internal/auth/auth.go) |
| Parkrr leitet den TOTP-AES-GCM-Schlüssel mit Bereichspräfix aus seinem Session-Secret ab | Keine Kopie dieses Schlüsselzuschnitts: Household hat bereits ein unabhängiges Encryption-Secret. Für neue Envelopes versionierte, bereichsgetrennte Schlüssel/AAD ergänzen. [Parkrr Crypto](https://github.com/d0linger/Parkrr/blob/d6368e8d0d7bb29505a98fa1235c3809c4179cc3/internal/auth/crypto.go) |
| Parkrr bietet Sessionliste, Widerruf und transaktionale Sessionrotation; DB-Inserts verwenden Tokenhash | Bestehende Household-Sessionverwaltung beibehalten; Pushbindung bei Rotation/Widerruf konsequent invalidieren und sichtbar erklären. [Parkrr Sessions](https://github.com/d0linger/Parkrr/blob/d6368e8d0d7bb29505a98fa1235c3809c4179cc3/internal/auth/sessions.go) |
| Treckrr TOTP lehnt leere Geheimnisse ab, vergleicht konstantzeitig und liefert den geprüften Zeitschritt für Replay-Schutz | Entsprechende Household-Invarianten durch Negative-/Replaytests erhalten; eine Entschlüsselungsstörung darf keinen Login-Fallback öffnen. [Treckrr TOTP](https://github.com/d0linger/Treckrr/blob/86c70c3305ba60d0742a4b60dafc390c4da7a69b/internal/totp/totp.go) |
| Treckrr speichert personenbezogene Sicherheitsmeldungen dedupliziert; neue Geräte können eine Nachricht und Mailabsicht erzeugen | Passkey-/TOTP-/Geräteänderungen in eigener Kategorie; Household erhält damit nicht automatisch den Treckrr-Mailkanal oder dessen Gerätemodell. [Treckrr Store](https://github.com/d0linger/Treckrr/blob/86c70c3305ba60d0742a4b60dafc390c4da7a69b/internal/store/security_notifications.go), [Server](https://github.com/d0linger/Treckrr/blob/86c70c3305ba60d0742a4b60dafc390c4da7a69b/internal/server/security_notify.go) |

Es wurden nur öffentliche API-Inhalte gelesen; keine lokalen Schwesterprojekte oder deren Geheimnisse verwendet. Ergänzende frühere Passkey-/Audit-Prüfung steht in [Settings-Referenzübernahme](settings-reference-adoption.md). Beide Referenzen sind MIT-lizenziert; eventuelle spätere substantielle Codeübernahme muss deren Hinweise erhalten. Dieser Plan kopiert keinen Implementierungscode.

### Konkrete Härtung vor breiter Freigabe

- Neue Crypto-Envelopes mit Formatversion und Key-ID; HKDF oder explizite unabhängige Schlüssel pro Verwendungszweck. AAD bindet mindestens Anwendung, Schema/Format, Zweck, Haushalt, Benutzer und Datensatz-ID. Zufällige Nonce pro Verschlüsselung; kein selbst entworfenes Kryptoprotokoll.
- Lesekompatibler, getesteter Übergang vom heutigen Format; Bestandsdaten nicht durch bloßen Austausch von `HOUSEHOLD_ENCRYPTION_KEY` unlesbar machen. Die aktuelle Key-ID-Prüfung von Subscriptions und vorhandene TOTP-/Passkey-Daten müssen ausdrücklich im Migrationspfad berücksichtigt werden.
- Step-up für Schlüssel-/Faktor-/administrative Sicherheitsänderungen; normale persönliche Benachrichtigungswahl benötigt gültige Sitzung, Eigentümerprüfung, CSRF und Rate-Limit. Eine Browser-Permission ersetzt keine serverseitige Autorisierung.
- Keine geheimen Werte in UI, Audit, Fehlertext, URLs, Healthcheck oder Metriken. Keine private VAPID-/Encryption-/CSRF-Konfiguration in Adminformularen anzeigen. Diagnosen nur mit Status, sicherer Key-ID und eindeutiger Fehlerklasse.
- Bestehende CSP, Nonce-/Same-Origin-Regeln, sichere Cookies, HSTS für echte HTTPS-Bereitstellung und lokale eingebettete Assets beibehalten. Push-Anbindung benötigt keine externen Scripts und keine Öffnung von `connect-src` für frei wählbare Hosts im Browser.
- Provider-Allowlist mit tatsächlichen unterstützten Browserendpunkten prüfen. Änderungen nur konkret freigeben; SSRF-Schutz, Public-IP-Prüfung, TLS-Hostname, Redirect-/Proxyverbot und Response-/Requestgrenzen nicht zugunsten eines neuen Browsers abschalten.

## 9. Betrieb, Schlüssel und Wiederherstellung

1. Bestehende `compose.webpush.yaml`-Erweiterung und Konfiguration wiederverwenden: `HOUSEHOLD_WEB_PUSH_ENABLED`, `HOUSEHOLD_WEB_PUSH_VAPID_CONTACT` und privater VAPID-Schlüssel über `_FILE`. Der vorhandene `push-keygen` erzeugt Dateien, überschreibt keine bestehenden und benötigt keine DB. Im Plan wird er nicht ausgeführt.
2. Stabilen HTTPS-Origin mit gültiger Zertifikatskette, unverändertem Manifest-ID/Scope und passendem WebAuthn-Origin/RP bereitstellen. `127.0.0.1:8100` bleibt lokaler Entwicklungszugang; ein Handy erreicht damit seinen eigenen Loopback. Für physische Tests ausdrücklich eingerichteten HTTPS-Zugang verwenden, nicht einfach HTTP auf eine LAN-IP verlagern.
3. Geprüfter ausgehender HTTPS-Zugriff zu den unterstützten Providern. Smartphones benötigen ebenfalls Internet zum Push-Service; Zugriff auf die eigentlichen Household-Inhalte kann weiterhin VPN/LAN erfordern. Push öffnet keine eingehenden Routerports. Ein nicht erreichbarer Household-Origin verhindert gegebenenfalls das Öffnen der Details.
4. Unabhängige zufällige Encryption-, CSRF-, Rate- und VAPID-Geheimnisse mit mindestens 256 Bit Entropie aus einer sicheren Quelle. Der bestehende Längencheck allein belegt diese Entropie nicht. Secret-Dateien minimal berechtigen und nur benötigten Containern read-only geben; keine Schlüssel in Images/Git/Logs/CI-Artefakten.
5. Vor neuen Migrationen konsistenten, verschlüsselten DB-/Uploads-/Konfigurationsstand sichern und Restore verifizieren. Entschlüsselungsschlüssel und VAPID-Privatschlüssel separat geschützt, mit Versionsinventar und Recovery-Anleitung sichern. Ein DB-Backup ohne passende Keys ist kein vollständiges Recovery.
6. Encryption-Rotation erst mit Schlüsselring: neuer aktiver Schreibschlüssel, begrenzte alte Leseschlüssel, batchesweise Re-Encryption unter Version/Lock-Prüfung, vollständige Zähl-/Entschlüsselungsprüfung, Restoreprobe. Alte Keys erst nach Ablauf aller benötigten Backup-/Rollbackhorizonte sicher ausmustern. Entschlüsselungsfehler sichtbar melden und fehlschlagen lassen.
7. VAPID-Rotation separat planen. Aktuell verlangt Household eine bewusste Gerätewiederverbindung; diesen Zustand klar anzeigen. Eine spätere überlappende Key-Ring-Lösung muss alte Subscription-Keys bis zur Erneuerung auswählen können. Niemals still annehmen, dass bestehende Subscriptions den neuen `applicationServerKey` akzeptieren.
8. Kompromittierung: Versand deaktivieren, betroffene Geräte-/Sitzungsfreigaben widerrufen, Schlüssel betroffenheitsbezogen erneuern, Vorfälle ohne Secretinhalt protokollieren. Bereits ausgelieferte Meldungen sind nicht rückrufbar; Rotation löscht keine Provider-/OS-Historie.
9. Health/Readiness prüfen lokale Konfiguration, DB und Workerfähigkeit; ein optionaler Provider-Ausfall darf nicht die gesamte App unbereit machen. Diagnose/Metriken unterscheiden Queuealter, ältesten fälligen Timer, Attempts, Limitierungen, 410-Quote und Gatewayannahme. Keine Endpunkte oder Benutzer-IDs als hochkardinale Metriklabels.
10. Nur Household-Testressourcen anlegen/entfernen. Kleine Test-DB, begrenzte Build-/Browserparallelität und vorhandene Container nutzen; keine pauschalen Docker-Prunes, keine Entfernung benannter Daten-/Uploadvolumes. Nach implementierten, bestandenen Änderungen lokal neu bauen/migrieren/recreaten und Readiness/Assets prüfen; keine Remote-Bereitstellung oder Git-Veröffentlichung ohne gesonderte Freigabe.

## 10. Phasencheckliste und Aufwand

Alle folgenden Punkte bleiben offen, bis Implementierung und jeweilige Prüfung tatsächlich ausgeführt wurden. Erst das aktuelle Modul vollständig umsetzen, danach dessen Test-/Fehlerbehebungsrunde abschließen; dann zum nächsten Modul wechseln. Schätzungen gelten für einen erfahrenen Entwickler mit vorhandener Testinfrastruktur, ohne Wartezeit auf physische Geräte/Zertifikate. Sie sind Planungsspannen, keine Lieferzusage.

### Phase A — Vertrag und Sicherheitsfundament · P0 · 2–3 Tage

- [x] N01: Kategorien, Empfänger, Standardfreigaben, Frische und Sperrbildschirmtexte entscheiden. Empfohlene Werte freigegeben; geschlossener Vertrag in `internal/notification/contract.go`, keine Inhaltsvorschau.
- [x] N02: Bestehende Aufgaben-/Einkaufspräferenzen und Modulberechtigungen auf den Core-Vertrag abbilden. Aufgaben bleiben fachliche Quelle für Ruhezeit/Digest/Eskalation; Einkaufs-Watch-/Mute-/Revisionsregeln bleiben verpflichtend. Neue Empfänger benötigen einen expliziten Moduladapter statt einer Haushalts-Rundsendung.
- [x] N03: Schema-/Payloadversionen, Aktivierungs-Cutoff und Alt-Job-Kompatibilität festlegen. Separater Jobvertrag, kein historisches Replay, vorhandenes Einkaufsformat bleibt lesbar.
- [x] N04: Schlüssel-/AAD-Rotation einschließlich Bestands-TOTP, Passkeys und Subscriptions spezifizieren. Versionierter Schlüsselring, expliziter Legacy-Pfad und Re-Encryption/Retirement-Vertrag im Implementierungsdokument; Ausführung bleibt N45–N49.
- [x] N05: Tabellen/Indizes und sichere Auditfelder finalisieren. Core38: Eigentümer-Fremdschlüssel, feste Kategorien/Frische, indizierte Absichten und 165 geprüfte Audit-Policies.
- [x] N06: Migrations-/Rollbackplan und Backup-/Restore-Voraussetzungen dokumentieren. Frische Core37-Sicherung und tatsächlicher isolierter Restore vor dem lokalen Upgrade vorgeschrieben; dieser Restore ist noch nicht ausgeführt und bleibt Freigabebedingung von N54.
- [x] N07: Core-Verträge und Migrationsgrundlage implementieren. Einwilligungszeilensperre, optimistische Versionen, Dedupe, begrenzte Widerrufsbereinigung und vollständige Readiness-Verträge vorhanden.
- [x] N08: Nach Umsetzung Schema-, Autorisierungs-, Dedupe- und Altbestandsintegrationstests ausführen; Fehler vor Phase B schließen. Fünf neue PostgreSQL-Testgruppen plus drei Theme-/Security-/Navigation-Upgrades, Unit-/Race-Verträge, Übersetzungen und 111 Checksummen grün. Fehler und Wiederholungen dokumentiert.

### Phase B — Gemeinsamer Gerätekanal und UI · P0 · 3–5 Tage

- [x] N09: Notification-Consumer und dauerhafte Zustellabsichten implementieren.
- [x] N10: Bestehenden Jobsender mit begrenztem Routing für neue Kategorien erweitern.
- [x] N11: Cancel-/Generation-/Session-/Revisionsprüfung und Privacy-Templates einbauen.
- [x] N12: Retry/TTL/Fairness, sichere Fehlercodes und begrenzte Retention implementieren.
- [x] N13: Service Worker für erlaubte Formate, Versionsübergang und sichere Navigation erweitern.
- [x] N14: Geräte-/Kategorienansicht im Einstellungstray mit DE/EN und allen Designs ergänzen.
- [x] N15: Opt-in, Ablehnung, Ablauf, Logout und Konto-/Sitzungswechsel durchgängig behandeln.
- [x] N16: Testnotification und getrennte Diagnose-/Bestätigungszustände implementieren.
- [x] N17: Core-Unit-, reale PG-, Worker-Race-, Browser-/A11y- und Negativtests ausführen; Fehler schließen.

### Phase C — Aufgaben vollständig · P1 · 2–3 Tage

- [x] N18: Vorhandenen Aufgaben-Digest und autorisierte Eskalation mit Gerätekanal verbinden. Store-Integration inklusive Widerruf bestanden.
- [x] N19: Opt-in für einzelne Fälligkeiten ergänzen, ohne bestehende Standardregeln zu duplizieren. Verwaltete und ältere Aufgaben sowie Aktivierungs-Cutoffs geprüft.
- [x] N20: Erledigung, Vertretung, Zuweisung, Serienänderung und Pause an Cancel/Revision anbinden. Aktuelle Zuständigkeit und unveränderte Bestandszuweisung bei neuer Abwesenheit geprüft.
- [x] N21: DST-, Zeitzonen- und Ruhezeitregeln mit sichtbarer Beschreibung ergänzen. Beide Zeitumstellungen und unveränderte Frischefristen geprüft; Browser-Abnahme bleibt N23.
- [x] N22: Aufgabenmodul nach vollständiger Umsetzung mit konkurrierenden Worker-/Benutzeraktionen testen. Neue Store-/HTTP-Tests, Scheduler-Fairness und alle 17 bisherigen Aufgaben-Testgruppen bestanden; korrigierte historische Test-Fixture separat wiederholt.
- [ ] N23: Aufgaben-Browserabläufe und reale Gerätematrix prüfen; alle Fehler vor dem Kochmodul beheben. Automatisierte Abnahme bestanden: 75 Aufgaben- und 156 Notification-Prüfungen, 34 saubere Axe-Scans; reale Windows-/Android-/iOS-Zustellung bleibt bis zur HTTPS-Testinstallation offen und wird nicht als bestanden gewertet.

### Phase D — Rezepte/Kochtimer vollständig · P1 · 3–5 Tage

- [x] N24: Timerrevision und Zustellabsicht atomar bei Start/Fortsetzen speichern. Reale Lifecycle-/Rollback-/Consent-Tests bestanden.
- [x] N25: Pause/Neustart/Entfernen/Bestätigen/Sitzungsende konsistent entwerten. Aktuelle Revision und finale Versandprüfung getestet.
- [x] N26: Begrenzte Deadline-Schleife und Reconciliation nach Neustart implementieren. Reale Worker-Prüfung mit abgelaufener Lease und 600 alten Jobs bestanden.
- [x] N27: Geräteauswahl, Frische, bewusstes Sound-Opt-in und ehrlichen Offlinezustand ergänzen. Explizite Geräte-Voreinstellungen und temporärer Vordergrundton; Node-Prüfungen bestanden, Browser-Abnahme in N29.
- [x] N28: Vordergrund-/Push-Dedupe und sichere „Timer öffnen“-Navigation implementieren. Sichtbare Systemmeldung bleibt für WebKit erhalten; nur nach tatsächlich abgespieltem Vordergrundton wird Stille angefordert. Private Kontext-/Routing-Tests und Service-Worker-Negativfälle bestanden.
- [x] N29: Nach vollständiger Umsetzung Timerzustände, Clock Skew, Neustart/Lease-Recovery und Lastziel testen. Native und Linux-Race-Kampagnen, gezielte Sperrkonkurrenz und drei Latenzwiederholungen bestanden; 149 Koch- und 157 Settings-Browserprüfungen mit 38 sauberen Axe-Scans. Reproduzierten Cooldown-Sperrfehler behoben, keine Grenzwerte gelockert.
- [ ] N30: Gesperrte/geschlossene/offline Geräte testen, beobachtete Verzögerungen dokumentieren und Fehler schließen. Browser-Offlinefehler und Lifecycle-Aufräumen geprüft; echte Windows-/Android-/iOS-Anzeige und hörbarer Geräteton bleiben bis zur HTTPS-Geräteabnahme offen.

### Phase E — Weitere Module nacheinander · P2 · 5–9 Tage

- [x] N31: Einkauf in gemeinsamen Vertrag übernehmen; Watch/Mute/Digest-Rechte unverändert erhalten. Exklusive Kanalwahl, atomare Digest-Absicht, erste zentrale Speicherung und finale Legacy-Prüfung implementiert; 14 neue Store- und sechs kontrollierte Versandfälle bestanden. Gesamte Modulabnahme folgt in N32.
- [x] N32: Einkauf vollständig auf Double-Sends, Widerruf, Offline/Konto-/Sitzungswechsel testen und Fehler schließen. Alle 19 bisherigen Integrationsgruppen, neue Store-/Transportfälle und Linux-Race-Prüfungen bestanden; 215 Browserprüfungen mit 21 sauberen Axe-Scans. Physische Provider-/Geräteanzeige bleibt separat in N51 offen.
- [x] N33: Vorratsablauf und Mindestbestands-Episoden an ausgewählte Empfänger anbinden. Pantry18, konservative Bestandsbeobachtung, persönliche Tagesgruppen und finale Konto-/Geräte-/Quellenprüfung implementiert. Neue Store-/Versand-, Upgrade-/Readiness- und echte Worker-Prüfungen bestanden; vollständige Modulabnahme läuft in N34.
- [x] N34: Vorrat nach Umsetzung auf Verbrauch/Löschung/Restock und Summierung testen und Fehler schließen. Neue Store-/Transport-/Upgrade-/Readiness- und Worker-Prüfungen, 74 bestehende Vorrats- und 18 gemeinsame Integrationsgruppen, Linux Race und 314 Browserprüfungen mit 26 sauberen Axe-Scans bestanden. Screenshots sowie 893 Quelldateien und Testbinary geprüft; eigener 8101-Prozess beendet. Physische Geräte bleiben N51.
- [x] N35: Fahrzeugfristen und aktuelle kilometerbasierte Wartung anbinden. Fuel12 mit ausdrücklicher Zuständigkeit, Friststufen, konservativen Kilometer-Episoden und finaler Konto-/Geräte-/Quellenprüfung implementiert. Neue Store-/Versand-/Worker-/Upgrade-/Readiness-Prüfungen bestanden; vollständige Modulabnahme läuft in N36.
- [x] N36: Fahrzeuge nach Umsetzung auf Archivierung, Friständerung und veraltete Daten testen und Fehler schließen. Neue Vertrags-/Race-Tests, alle11 bisherigen Übersicht- und21 Modulgruppen sowie1.035 Prüfungen im vollständigen OCR-/Backup-/Restore-/Purge-Browserlauf bestanden; zusätzliche Settings-/Zuordnungsprüfungen dokumentiert. Quellstand und Bereinigung verifiziert, physische Geräte bleiben N51.
- [x] N37: Finanz-Fälligkeiten/Budgetschwellen mit bestehenden Sichtbarkeitsverträgen anbinden. Core39/Expenses6, bestätigte Zahlungserinnerungen und persönliche80%/100%-Budgetepisoden implementiert; automatisierte Modulabnahme in N38 abgeschlossen.
- [x] N38: Finanzen nach Umsetzung auf private Daten, Berechtigungswechsel, Zahlung und Schwellenhysterese getestet. Native Vertrags-/Regressionsprüfungen, Linux-Race-Abnahme und 829 Browserprüfungen mit 50 sauberen Axe-Scans bestanden; Quellen und Bereinigung verifiziert. Physische Geräte bleiben gesondert offen.
- [x] N39: Zähler-Ablesefristen und ausdrücklich konfigurierte Anomalien anbinden. Persönliche Zuständigkeit, getrennte Freigaben, aktuelle Mess-/Quellenrevisionen und sparsame Sperrbildschirmtexte sind implementiert; die abschließende Abnahme bleibt N40.
- [x] N40: Zähler nach Umsetzung auf Datenlücken, Adapterausfall, Entwarnung und Cooldown getestet. Sechs neue Store-Gruppen und kontrollierte Zustellung unter Linux Race, fünf native Web-Gruppen, neun bestehende Zähler-Regressionsgruppen und 636 Browserprüfungen mit 48 sauberen Axe-Scans bestanden. Quellen, Testbinary und Bereinigung verifiziert; physische Geräte bleiben N51.
- [x] N41: Dokumentfristen und persönliche Importprobleme angebunden. Ausdrückliche Fristzuständigkeit, serverseitige Quellenzeitstempel und persönliche Fehlerereignisse sind implementiert; die vollständige Modulabnahme bleibt N42.
- [x] N42: Dokumente auf aktuelle Berechtigungen, Frist-/Quellenänderungen, Widerruf und inhaltsfreie Zustellung geprüft. Neue Store-/Web-/Upgrade-/Worker-Gruppen, bestehende Dokumentenregressionen und Linux Race bestanden; 649 Browserprüfungen mit 50 sauberen Axe-Scans. Screenshots, 948 Quelldateien, Runtime-Binary und Ressourcenbereinigung verifiziert. Physische Geräte bleiben N51.
- [x] N43: Essensplan-Zubereitung und ausdrücklich bestätigte Vorbereitungsschritte angebunden. Eigene Settings-Version, aktuelle Mahlzeiten-/Vorlagen-/Zeitzonenbindung, begrenzter Scanner und DE/EN-Steuerung sind implementiert; die vollständige Modulabnahme läuft in N44.
- [x] N44: Essensplan auf Änderungen/Absagen, Revisionsdubletten und Empfänger geprüft. Insgesamt158 reguläre Testgruppen,43 Linux-Race-Gruppen und688 Browserprüfungen/53 saubere Axe-Scans qualifiziert. Die93 alten Web-Gruppen sind ausdrücklich aus91 unveränderten Linux-Ergebnissen und zwei korrigierten Windows-Migrationstests zusammengesetzt; ursprüngliche Fehler bleiben dokumentiert. Quellenumfang, Bereinigung und Lint geprüft. Physische Geräte und Deployment bleiben separat offen.

### Phase F — Kryptografie, Sicherheitsmeldungen und Abnahme · P0/P1 · 3–5 Tage

- [x] N45: Versionierten Schlüsselring und datensatzgebundene Crypto-Envelopes mit Legacy-Leser implementiert. Aktuelle Unit-/gebundene Datensatzprüfungen und tatsächlicher Restore aller sechs geschützten Zwecke bestanden; vollständige Regression bleibt N50.
- [x] N46: Unterbrechbare Re-Encryption und kontrollierten Key-Retirement-Prozess implementiert. Tatsächliche Rotation in zwei Batches, unveränderte fachliche Zustände und zurückhaltende Retirement-Auskunft ohne automatische Schlüssellöschung geprüft.
- [x] N47: Persönliche Sicherheitsereignisse an eigenen Notification-Kanal angebunden. Echte Authentifizierungs-/Geräteänderungen, eigene Inbox, geschlossene Ereignisidentität, fehlende Schlüssel und finale Versandberechtigungen in aktuellen Linux-Race-Integrationsprüfungen bestanden.
- [x] N48: Mutationstests für Ciphertext, Nonce, AAD, fremde Datensätze und fehlende/alte Keys bestanden. Aktuelle Unit-/Datensatzprüfungen und tatsächliche Ablehnung eines Restores mit fehlendem archiviertem Schlüssel dokumentiert.
- [x] N49: Rotation und verschlüsseltes Backup/Restore mit synthetischen TOTP-/Passkey-/Push-Beständen tatsächlich bestanden. Teilweise und vollständig rotierte Bestände, archivierte statt laufender Schlüssel, vollständige Schema-Gleichheit und negative Schlüsselprobe geprüft; eigene Ressourcen bereinigt.
- [x] N50: Betroffene Unit-/Integration-/Race-/Browser-/A11y-/Securitykampagne bestanden:420 Go- und498 JS-Prüfungen,19 Authentifizierungs- und4 Einkaufsgruppen,91 Linux-Race-Mains,722 Browserprüfungen/52 saubere Axe-Scans sowie Katalog-/Recovery-/Scannerprüfungen. Die21 alten Versandgruppen sind ausdrücklich20 unveränderte Erfolge plus ein minimal korrigierter, nativ und unter Race erneut bestandener Test; ursprüngliche Fehlversuche bleiben dokumentiert.
- [ ] N51: Windows-, Android- und iOS-Geräteprotokoll mit Versionsstand und beobachteten Zuständen abschließen.
- [x] N52: README, Betriebsanleitung, Datenschutz-/Retentionstexte und Implementierungsnachweise aktualisiert; vollständige Compose-Overlays, Origin-Wechsel, archivierte Schlüssel und kompatibler Rollback beschrieben. Reale Geräte bleiben ausdrücklich offen.
- [x] N53: Aktuelle1.296 Quelldateien ohne Secret-Fund geprüft; vorhandener1.055-Dateien-Index ohne lokale/private Artefakte. Minimaler Test-Diff, unveränderte Anwendungsbinary und Migrations-/Rollbackwirkung geprüft. Keine neuen Abhängigkeiten durch den Abschlussfix; keine Veröffentlichung.
- [x] N54: Lokalen Stack neu gebaut und Core37→40 aktualisiert. Frische verschlüsselte Sicherung, tatsächlicher Restore mit Vorgängerbinary, Migration der isolierten Kopie und Prüfung archivierter Schlüssel gingen der echten Migration voraus. Datenbank/Uploads/Secrets erhalten.
- [x] N55: Neuer lokaler Stand gesund;97 öffentliche HTTP-Prüfungen und90 bytegleiche eingebettete Assets bestanden. In-App-Regression mit identischer Binary:722 Prüfungen/52 saubere Axe-Scans. Reale Geräte ausdrücklich nach N51 verschoben; keine authentifizierten Änderungen an echten Haushaltsdaten als Test.
- [x] N56: Eigene Test-/Scanner-/Restorecontainer und abgeschlossener Migrationscontainer bereinigt; isolierte Testdatenbank entfernt. Zwei überholte Kandidatenimages und111.732.544 Bytes ungenutzte Test-/Exportbinaries entfernt. Aktuelles Image, Rollbackimage, Datenvolumes, Schlüssel und Prüfnachweise erhalten.

Gesamtvorschlag: etwa **18–30 Entwicklungstage** für alle neun Module einschließlich Sicherheitsrotation und physischer Abnahme. Ein erster nutzbarer Stand aus Fundament, Geräteansicht, Aufgaben und Kochtimern liegt vor den übrigen Modulen. Die Kryptografiephase muss vor breiter produktiver Freigabe abgeschlossen sein; Phase-A-Entscheidungen verhindern einen späteren inkompatiblen Envelope-Umbau.

## 11. Abnahmekriterien und tatsächliche Gerätetests

### Automatisierbare Anforderungen

| Testgruppe | Bestehensbedingung |
|---|---|
| Zugriff | Fremder Haushalt, fremdes Gerät, deaktivierter Benutzer, widerrufene Sitzung, fehlende Finanz-/Dokumentberechtigung und alte Modulgeneration erzeugen keinen neuen Send |
| Einwilligung | Ohne aktuelle Konto-/Gerätefreigabe kein Versand; neue Kategorie und neue Anmeldung erweitern Einwilligung nicht still |
| Datenbank/Rennen | Gleichzeitige Worker/Änderungen erzeugen genau eine logische Absicht je Schlüssel; Lease-Verlust darf keinen fremden Job abschließen |
| Mutation | Timerpause/Neustart und Aufgabenerledigung entwerten noch nicht gesendete alte Revisionen; verspätete Antworten verändern keine neue Subscription |
| Provider | 2xx heißt nur angenommen; 404/410, 429, 401/403, 5xx, Timeout und unklare Netzwerkantwort werden korrekt behandelt |
| Netzwerk | Loopback/private/mixed DNS, Redirect, unzulässiger Host/Port, riesiger Body und langsame Antwort werden begrenzt abgewiesen |
| Kryptografie | Manipulierte/verschobene Envelopes schlagen fehl; Rotation liest Altbestände; fehlender Key führt zu sicherem Fehler; Restore reproduziert nutzbare Daten |
| Client | Unbekannte Payloadfelder/-version, abgelaufene Referenz, fremde Audience, falsches Konto und unsichere Navigation werden abgewiesen |
| Zeit | DST-Lücke/-Dopplung, Mitternachtsruhezeit, Zeitzonenwechsel, Browseruhrsprung und Neustart erzeugen keine unkontrollierten Doppelalarme |
| UI | DE/EN; Ocean/Pink hell/dunkel/system; Tastatur/Screenreader; kleine Displays; verweigerte/fehlende API und unklare Registrierung sinnvoll dargestellt |
| Ressourcen | Begrenzte Worker/Connections/Queries/Retentionsbatches; kontrollierte Lastmessung bestätigt den internen Timer-Schedulerwert oder dokumentiert notwendige Anpassung |

### Physische Matrix — aktuell vollständig offen

Browserautomation oder synthetische Push-Events ersetzen diese Abnahme nicht. Für jede Zeile Gerät, OS-/Browserbuild, Origin, Installationsart, Permission/Fokus/Akku, Serverdeadline, Requestannahme und beobachtete Anzeigezeit notieren. Keine realen Haushaltsinhalte als Testdaten.

| Szenario | Windows/Edge-PWA | Android/Chrome-PWA | iOS/Home-Screen-PWA |
|---|---|---|---|
| Bewusste Erstfreigabe + generischer Test | Offen | Offen | Offen; Installation und Start vom Home Screen gesondert prüfen |
| PWA im Vordergrund, anderer Tab/andere App | Offen | Offen | Offen |
| PWA geschlossen, Gerät gesperrt | Offen; Browserhintergrund dokumentieren | Offen | Offen |
| Browser/OS vollständig beendet bzw. Neustart | Offen; Ausfallgrenze erfassen | Offen; Force-stop gesondert | Offen; keine Start-/Weckgarantie annehmen |
| Fokus/Nicht stören und Batteriesparen/Doze | Offen | Offen; Herstellerzustand notieren | Offen; Fokus/Benachrichtigungsübersicht |
| Offline über Timer-TTL, danach Reconnect | Kein alter Alarm; App zeigt abgelaufen | Gleiches Kriterium | Gleiches Kriterium |
| Timerpause/Neustart kurz vor Ablauf | Offene Altjobs weg; unvermeidbare bereits gesendete Race dokumentieren | Gleiches Kriterium | Gleiches Kriterium |
| Mehrere Geräte, Bestätigung an einem Gerät | Keine zweite fachliche Mutation; Anzeige anderer Geräte best effort schließen | Gleiches Kriterium | Gleiches Kriterium |
| Logout, Nutzerwechsel, Remote-Sessionwiderruf | Lokale Zuordnung weg; keine neuen Sends; angenommene alte generische Meldung als Grenze dokumentieren | Gleiches Kriterium | Gleiches Kriterium |
| Freigabe entziehen, Browserdaten löschen, App neu installieren | Verständlicher Zustand, bewusste Neuverbindung | Gleiches Kriterium | Home-Screen-Neuinstallation ausdrücklich prüfen |
| Sprache/Design/Barrierefreiheit | DE/EN + alle Designs | DE/EN + große Schrift | DE/EN + VoiceOver/große Schrift |
| VPN/LAN-Origin nicht erreichbar, Provider erreichbar | Push und Detailzugriff getrennt diagnostiziert | Gleiches Kriterium | Gleiches Kriterium |

Abnahme verlangt erfolgreiche kontrollierte Normalzustände und korrekt dargestellte Ausfallzustände. Eine gemessene p95-Latenz auf drei Geräten ist keine plattformweite Zustellgarantie. Ein Gerät ohne verfügbare Testhardware bleibt als **nicht geprüft** dokumentiert; keine automatische Freigabe aus einem grünen Unit-Test ableiten.

## 12. Freigabe und offene Betreiberentscheidungen

Empfohlene Freigabe: Fundament → Geräte-UI → Aufgaben → Kochtimer → Einkauf → Vorrat → Fahrzeuge → Finanzen → Zähler → Dokumente → Essensplan → abschließende Crypto-/Recovery-/Geräteabnahme. Sicherheitsvertrag und Rotation werden bereits im Fundament entworfen.

Vor der Implementierung zu bestätigen bzw. mit den empfohlenen Werten freizugeben: generischer Sperrbildschirmstandard, maximal fünf Geräte, Timerfrische 60 s, ausdrückliches Push-Opt-in, vorgeschlagene Retention, bestehende Sitzungsbindung und phasenweiser Umfang. Für reale Gerätetests werden ein gültiger erreichbarer HTTPS-Origin und jeweils ein Windows-/Android-/iOS-Gerät benötigt. Konkreter Domain-/Zertifikats-/Schlüsselbetrieb wird im Implementierungsauftrag eingerichtet, nicht durch dieses Dokument verändert.

**Historischer Planungsstand bei Erstellung:** fokussierte Codeanalyse, öffentliche Referenzprüfung und zitierter Plattform-/Architektur-/Sicherheitsplan; damals noch keine Implementierungs- oder Betriebsabnahme. Den aktuellen Umsetzungsstand zeigen die Checkliste oben und [die ausgeführten Prüfungen](notifications-implementation.md). Produktion und reale Geräte werden durch automatisierte Tests nicht als freigegeben dargestellt.
