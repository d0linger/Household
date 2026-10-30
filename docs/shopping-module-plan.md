# Einkaufen – verbindlicher Abschlussblock SH15 und SH17

Stand: 29.10.2026. Ausgangspunkt ist der lokal abgenommene Vorrats-Checkpoint `c94072aa49533319947f32d91c1894f222675f9f`: 85/180 Punkte verifiziert, Einkaufen18/20. Dieser Plan fasst den **vollständigen nächsten Modulblock vor dessen Umsetzung** zusammen. Phase1 ist Quellenanalyse und Spezifikation; neue Anwendungstests beginnen erst nach vollständig integrierter Phase2.

## Reihenfolge und feste Grenzen

1. Gesamten Umfang einschließlich gemeinsamer Verträge und Abnahme festlegen.
2. SH15 und SH17 vollständig implementieren, Testquellen vorbereiten und unabhängig prüfen.
3. Erst danach gemeinsam kompilieren, testen und gefundene Fehler beheben.
4. Erst nach vollständiger Modulabnahme einen weiteren Modulblock beginnen.

Parallelarbeit bleibt innerhalb dieses Shopping-Blocks. Live8100, Geschwisterprojekte und die zurückgestellte Finanz-Dokumentation bleiben unberührt. Arbeits-HTTP8101 und PostgreSQL55434/household_test sind ausschließlich isolierte Tests. Kein Push oder Veröffentlichen. Alle75 akzeptierten SQL-Dateien, Versionen, Audit-Policies und Ready-Fingerprints bleiben unverändert; diese Erweiterungen benötigen **keine neue Migration**.

## SH15 – persönliche Listenhinweise

- Aktive normale Einkaufslisten ausdrücklich abonnieren, abbestellen und für60Minuten/24Stunden/7Tage stummschalten. Eigene Änderungen erzeugen keine Hinweise. Persönliche Einstellungen stehen auch Readern zur Verfügung; Schreibrechte auf Artikel oder fremde Einstellungen entstehen dadurch nicht.
- Dauerhafte In-App-Mitteilungen funktionieren ohne externe Dienste. Änderungen werden je Empfänger/Liste/Generation in60-Sekunden-Fenstern gebündelt. Ein Vorgangszähler wäre ein Ereigniszähler, keine Artikelanzahl; die erste UI verwendet allgemeine Zusammenfassungen.
- Abgedeckt sind Liste/Position anlegen, Inhaltsänderung, Abhaken, Reihenfolge, Verschieben in beide betroffenen Listen, Löschen, Kopieren, Merge, Import, Barcode-/Nachkauf-/Rezept-/Pantry-/Planungsübernahme und Einkaufsabschluss. Shopper-Aktivitätsleases, reine Lesevorgänge und alleinige Produkt-/Preis-/Gangstammdatenänderungen sind keine Listeninhaltsmitteilung.
- Änderungen werden innerhalb ihrer tatsächlichen Geschäftstransaktion veröffentlicht. Ein Consumer darf die frühere Liste gelöschter oder verschobener Positionen nicht nachträglich erraten. Rollback und idempotenter Replay erzeugen keine zusätzliche fachliche Zustellung; inhaltlich unveränderte Aktualisierungen erzeugen keinen neuen Inhaltsdigest.
- Keine rückwirkenden Hinweise vor dem jeweiligen Abonnement-/Freigabezeitpunkt. Stummschaltung entwertet wartende Hinweise; nach ihrem Ablauf wird kein stummer Rückstau zugestellt. Pausierte Module liefern nicht aus; alte Generationen und überholte Berechtigungen werden verworfen.
- Die Mitteilungsansicht erhält eine geschlossene, generationengeprüfte Shopping-Projektion. Bestehende andere Mitteilungstypen behalten ihr Verhalten.

## Optionaler Web Push

- Standardmäßig aus. In-App-Hinweise benötigen weder Browsererlaubnis noch VAPID-Konfiguration.
- Betreiber konfigurieren einen dauerhaften VAPID-Privatschlüssel und eine Kontakt-URI. Der öffentliche Schlüssel wird daraus abgeleitet. Ein expliziter CLI-Schlüsselgenerator arbeitet ohne Datenbank und überschreibt keine vorhandenen Schlüssel stillschweigend. Ein optionales Compose-Overlay bindet ausschließlich den zusätzlichen benötigten Schlüssel ein; die normale Installation verlangt keine neue Secret-Datei.
- Geplant sind `HOUSEHOLD_WEB_PUSH_ENABLED`, `HOUSEHOLD_WEB_PUSH_VAPID_PRIVATE_KEY_FILE` und `HOUSEHOLD_WEB_PUSH_VAPID_CONTACT`. Ausgeschaltet, nicht eingerichtet, gültig eingerichtet und fehlerhaft eingerichtet bleiben getrennte Zustände; fehlerhaft vorhandene Schlüssel werden vor Bootstrap abgelehnt.
- Browserfunktion, Berechtigung, Serverregistrierung und Annahme durch den Push-Dienst sind unterschiedliche Tatsachen. Keine Berechtigungsabfrage beim Laden, keine stille Verbindungserneuerung und kein Erfolgswort „zugestellt“ allein aufgrund eines HTTP-Erfolgs.
- Serverabonnements liegen ausschließlich verschlüsselt in der vorhandenen Core-Tabelle. Der geschlossene Envelope bindet tatsächliche Zeilen-ID, Haushalt, Benutzer, Gerät, ausstellende Sitzung, Authentisierungsstand, Zustimmung und Ablauf. Nach Entschlüsselung werden alle Bindungen gegen aktuelle Daten geprüft. Die bestehende AEAD-Funktion besitzt konstantes AAD; eine bereits vorhandene zeilenbezogene Kryptobindung wird nicht unterstellt.
- Höchstens5 aktive Geräte je Benutzer, begrenzte Registrierungs-/Versandbudgets in PostgreSQL. Ein fremder bestehender Endpoint darf nicht durch blindes Upsert einem anderen Benutzer zugeordnet werden.
- HTTPS443, eng geprüfte Produktions-Gateways von Google, Mozilla, Apple und Microsoft, echte DNS-/IP-Prüfung und feste TLS-Hostbindung. Endpointpfad und Query bleiben opaque. Keine Proxys, Redirects, IP-Literale, internen/privaten/reservierten Zieladressen oder frei konfigurierbaren Versand-URLs. Jeder Versuch erhält kurze Zeit-, Körper- und Parallelitätsgrenzen.
- Netzwerkversand findet außerhalb von DB-Transaktionen statt.404/410 deaktivieren abgelaufene Ziele;429 berücksichtigt begrenztes Retry-After, temporäre Fehler erhalten begrenzte persistente Wiederholungen. Vor jedem Versuch werden Empfänger, Sitzung, Modul, Generation, Abonnement und Lease erneut geprüft.
- Verlorene externe Antworten können einen erneuten allgemeinen Hinweis verursachen. Ein stabiler Notification-Tag begrenzt sichtbare Duplikate; Exactly-once-Zustellung beim externen Dienst wird nicht versprochen.
- Sperrbildschirmtext bleibt allgemein: „Änderungen im Einkauf. Household öffnen.“ Keine Listen-/Artikel-/Personennamen, Mengen, Preise oder Änderungszahlen. Die geschlossene Push-Payload enthält nur Schema/Typ, Mitteilungs-ID, feste Ablaufzeit und nicht berechtigende Audience-Kennung.
- Service Worker öffnet ausschließlich die feste Same-Origin-Seite `/notifications`. Nachrichtendaten dürfen keine Ziel-URL, keinen HTML-Inhalt, keine Bilder oder auszuführende Aktion bestimmen. Push lädt keine Lesekopie nach und führt keine Mutation aus.
- Logout/Sitzungswiderruf unterbindet weitere zugeordnete Versuche; eine bereits vom externen Dienst angenommene Nachricht lässt sich nicht zurückholen. Gerätefences und allgemeine Inhalte schützen auch diesen Fall.

Für Protokoll/Kryptografie ist die kleine MIT-Bibliothek `webpush-go v1.4.0` mit Context und eigenem HTTP-Client vorgesehen. Ihr JWT-v5-Zweig wird kompatibel auf `v5.3.1` gepinnt; vorhandenes `x/crypto v0.58.0` wird nicht zurückgestuft. Abhängigkeiten werden nach vollständiger Umsetzung regulär geprüft. Keine eigene Implementierung der Web-Push-Verschlüsselung.

## SH17 – ausdrückliche Offline-Lesekopien

- Aktuell berechtigte Benutzer wählen konkrete Listen und speichern/aktualisieren sie ausdrücklich auf diesem Gerät. Die gemeinsame Gerätefreigabe wird sinnvoll als „Offline-Daten“ beschriftet; bestehende Änderungswarteschlange und Pantry-Scans behalten eigene Datenverträge.
- Ein neuer geschlossener Snapshot-Endpunkt liefert die vollständigen ausgewählten Listen aus einem konsistenten Read-only-RR-Snapshot. Offene, erledigte und nicht verfügbare Positionen werden unabhängig vom aktuellen UI-Filter berücksichtigt. Zusammenkleben von100erAPI-Seiten ist kein Ersatz.
- Feste Grenzen:10Listen, insgesamt1.000Positionen,2MiB UTF-8-Speicherbudget,7Tage Laufzeit. Ein1001-Sentinel und vollständige Byteprüfung verweigern Überschreitung als Ganzes. Kein stiller Teilstand und keine automatische Verdrängung anderer gültiger Kopien.
- Geschlossene Lesefelder: Listenname, Positionstitel, Menge als exakter String oder ausdrücklich `null`, Einheit, Gruppe, reine Produkt-/Packungs-/Ersatzbezeichnung, Artikelnotiz, gespeicherter Erledigungs-/Verfügbarkeitszustand und gegebenenfalls Zuständigkeits-Anzeigename. Die Freigabe benennt ausdrücklich die lokale Speicherung von Texten/Notizen. Keine Bilder/Bildkennungen, Belege, berechneten Preis-/Geldfelder, fremden Moduldetails, Cookies, Sitzungstoken, CSRF-Werte oder Mutationsanfragen. Freitext kann vom Benutzer erwähnte Preise enthalten; die UI verspricht keine Inhaltsklassifizierung.
- Kontextbindungen sind nicht berechtigende Digests/Bezeichner. Eine tatsächliche interne Sitzungs-UUID für Push bleibt serverseitig; Bearer- oder CSRF-Werte werden nicht in Lesekopien abgelegt.
- Eigenes IndexedDB-Schema `household-shopping-readlists` mit Meta und Snapshots. Haushalt, Benutzer, Sitzungsscope, Generation, Geräteepoche, Revision, Ablauf und gemeinsames Budget werden atomar geprüft. Späte Antworten nach Löschen/Logout/Opt-out dürfen nichts wiederherstellen.
- Erneutes Öffnen oder Aktivieren verlängert keine Laufzeit. Ein neuer Ablauf entsteht nur durch ausdrücklichen aktuellen vollständigen Abruf. Gleicher Benutzer mit neuer Sitzung benötigt aktuelle Prüfung und vollständigen Neuabruf der ausgewählten Listen; eine andere Generation verlangt ausdrückliches Verwerfen alter Kopien.
- Bekannter Kontowechsel, Logout, Opt-out oder beobachteter Berechtigungsverlust sperrt die UI sofort, entwertet offene Abrufe, signalisiert andere Tabs und löscht die vorgesehenen Rohdaten. Bereinigungsfehler werden nicht als gelöschte Daten ausgegeben. Ablauf verhindert Lesen und wird beim nächsten Zugriff bereinigt, ohne ein physisches Löschen auf einem inaktiven Gerät zu einem exakten Zeitpunkt zu behaupten.
- Die neutrale öffentliche Seite `/offline/shopping` enthält selbst keine Identität oder Listendaten. Nutzer öffnen bewusst ihre gespeicherten Kopien. Jede Ansicht zeigt „Gespeicherte Lesekopie“, Erfassungszeit, Ablauf und „Serverstand offline nicht bestätigt“.
- Erledigungszustände sind statische Anzeigen. Keine Offline-Checkboxaktion, Erfassung, Preisberechnung, Barcode-Schreibaktion, Hintergrundübertragung oder Einkaufsbuchung. „Online öffnen“, „Aktuell neu speichern“ und „Gerätekopie löschen“ sind klar getrennte Aktionen.
- Private HTML-/API-Antworten bleiben ungecacht. Der Service Worker ergänzt nur die neutrale Shell und lokale Assets. Eine entfernte Rechteänderung kann ein vollständig offline befindliches Gerät nicht sofort erkennen; die letzte zeitlich begrenzte Freigabe bleibt ausdrücklich als gespeicherter Stand gekennzeichnet.

## Bestehende PostgreSQL-Datenverträge – keine DDL

| Speicher | Neuer eng begrenzter Zweck |
|---|---|
| `core.module_states.settings.shopping_notify_v1` | Private typisierte Abonnements, aktuelle Generation, dauerhafte eigene Revisionen;128KiB Namespace,20aktive Watches/Benutzer,256gesamt. |
| `core.outbox_events` | Bestehendes `record_changed` für Shopping-Listen/Positionen um Event-Version2 mit minimalem Listenbezug erweitern; andere Module/alte Consumer behalten Version1. |
| `core.event_deliveries` | Eigener Consumername und transaktionale Fortschritts-/Deduplizierungsbelege. |
| `core.jobs` | Begrenzte Fanout-Fortsetzung,60-Sekunden-Digests und verschlüsselungsfreie Push-Referenzen; vorhandene Lease/Retry-Zustände. Kein erfundener Zustand `cancelled`. |
| `core.notifications` | Einmalige aktuelle In-App-Mitteilung; neuer strenger Kind-/Dedupe-Vertrag bindet Haushalt, Generation, Liste und Empfänger. |
| `core.push_subscriptions` | Vorhandene Hash-/Ciphertext-/Key-ID-Felder für aktuelle eigene Geräte. Keine Endpoints oder Browser-Schlüssel in API-Antworten. |
| `core.rate_limit_buckets` | Bestehende dauerhafte Verwaltungs-/Versandbudgets. |
| Eigener Browser-IndexedDB-Namensraum | Ausschließlich autorisierte ausdrücklich gewählte Lesekopien, ohne Server-Schreibautorität. |

Die Präferenzstruktur enthält Schema1, Generation und `users[userUUID]` mit eigener dezimaler Revisionszeichenkette und einer Watch-Map. Ein Watch enthält Aktivierungszeit, Push-Freigabe/-Zeit und optionales serverberechnetes Stumm-bis. Nach dem letzten Abbestellen bleibt die eigene Revision mit leerer Map erhalten; ein alter Request mit Revision0 kann nichts neu anlegen. GET erfindet und persistiert keine Vorgaben. Unbekannte oder beschädigte Strukturen werden nicht stillschweigend repariert.

Reader-Mutationen verwenden **Haushalt SHARE → aktuelle Sitzung/Mitgliedschaft → Modulzustand UPDATE NOWAIT**. Sie benutzen nicht den Editor-`BeginMutation` mit anschließender SHARE→UPDATE-Sperrverstärkung. Nur der eigene Zweig ändert sich; andere Settings, `state_version`, `changed_at` und `changed_by` bleiben erhalten. Strukturierte Settings bleiben im bestehenden Audit redigiert.

Fachliche Schreiber veröffentlichen ausschließlich innerhalb ihrer vorhandenen Transaktion und ändern dort keine Watch-Settings. Der zentrale Producer übernimmt gesperrte Vorherbilder für alte/neue Listen; zusätzliche Anschlüsse sind Generic Delete und Planning-Purchase-Delete. Version2 behält `entity/id` und ergänzt eine geschlossene `shopping`-Payload mit Schema, Änderungsart und höchstens zwei eindeutigen Listen-IDs. Keine Artikeltexte. Ereigniszeit ist datenbankseitig `clock_timestamp()`; Deduplizierung beruht auf serverseitigen Identitäten, nicht auf einem frei gelieferten Trace-Header.

Der Consumer nutzt aktuelle Verarbeitungszeit für noch offene60-Sekunden-Buckets, transaktionale Receipts und bei Bedarf begrenzte Fortsetzungsjobs. Aktive Mitgliedschaft, Generation, Watch-Revision und Opt-in-/Mute-Zeiten werden vor jeder Wirkung erneut geprüft. In-App-Empfänger brauchen keine gerade offene Browsersitzung; Push braucht zusätzlich die noch gültige registrierende Gerätesitzung. Netzwerktransaktionen halten keine DB-Sperren. Archivierte Listen pausieren, entfernte Listen erhalten höchstens den ausdrücklich definierten allgemeinen Abschluss; verwaiste Watches werden separat begrenzt bereinigt. Purge entfernt nur den Shopping-Namensraum zusätzlich zu bereits vorhandenen Jobs/Notifications/Outbox und erhöht die Generation; andere Settings bleiben erhalten.

## API-Flächen

| Methode / Pfad | Geschlossener Zweck |
|---|---|
| `GET /api/v1/shopping/extensions/context` | Aktueller eigener Kontext, nicht berechtigende Scopes, Generation, Limits, Serverzeit und Push-Verfügbarkeit. |
| `GET /api/v1/shopping/lists/{id}/notification-preferences` | Eigene tatsächliche Revision/Watch oder leere nicht persistierte Vorgaben. |
| `PUT /api/v1/shopping/lists/{id}/notification-preferences` | Eigene Revision/Generation geprüft: aktivieren, abbestellen, Push-Wunsch oder begrenztes Stummschalten. Keine Benutzer-ID aus dem Body. |
| `GET /api/v1/shopping/notification-preferences` | Begrenzte eigene Übersicht mit aktuell erlaubten Listenbezeichnungen. |
| `POST /api/v1/shopping/offline-snapshots` | Ausdrücklich gewählte eindeutige Listen-IDs; vollständiger konsistenter Snapshot oder gesamte Ablehnung. |
| `GET /api/v1/core/push/context` | Eigener aktueller Geräte-/Konfigurationsstatus und öffentlicher VAPID-Schlüssel. |
| `GET /api/v1/core/push/subscriptions` | Ausschließlich eigene begrenzte Gerätestatus, niemals Endpoint-/Schlüsselmaterial. |
| `POST /api/v1/core/push/subscriptions` | Aktuelle eigene Geräteverbindung mit streng validiertem verschlüsseltem Envelope. |
| `DELETE /api/v1/core/push/subscriptions/{id}` | Eigene Verbindung widerrufen; auch ohne aktive Shopping-Bearbeitung möglich. |
| Bestehende `/notifications` und Read/Dismiss | Für den neuen Shopping-Typ generationen-/zielgeprüfte Projektion; bestehende andere Typen erhalten. |
| `GET /offline/shopping` | Öffentliche neutrale Shell; keinerlei eingebettete private Daten. |

Alle privaten Flächen bleiben Cookie-authentifiziert; Mutationen und ausdrückliche Snapshot-POSTs verwenden die bestehende HMAC-CSRF-Prüfung. Unbekannte Felder und übergroße Bodies werden abgelehnt. Eigene Revisionen und Mengen bleiben exakt; fremde Ressourcen liefern keine Details. Nach dem konsistenten Read-only-Snapshot werden aktuelle Autorität/Generation vor Ausgabe erneut geprüft. Fehler erhalten die bisherige gültige vollständige lokale Kopie und erzeugen keinen Teilstand.

## Visuelle Abläufe

Online-Listenheader: **Einkaufen → aktive Liste → Fortschritt → Hinweise / Offline lesen**. Die beiden kompakten persönlichen Aktionen liegen außerhalb des Editor-only-Bereichs. „Hinweise“ öffnet einen nativen Aufklapper mit aktiv/stumm/aus und getrennten Browser-/Server-/Gerätestatus. Kein neuer Dialog beim Laden, keine überladene Hauptnavigation und kein Verlust laufender Eingaben.

Offline: **Öffentliche neutrale Shell → eigene gespeicherte Listen bewusst öffnen → Stand/Ablauf/Umfang → statische Artikelzeilen**. Oben bleibt die Kennzeichnung als gespeicherter, serverseitig unbestätigter Stand sichtbar. Lange Texte umbrechen; lokale100erAnzeige-Seiten verändern nicht den vollständig gespeicherten Umfang. Aktionen zum Online-Öffnen, Neuabruf und Löschen sind getrennt. Einstellungen zeigen Kapazität, Ablauf und einzelne Löschaktionen. Bestehende Farben, Systemschriften,44px-Ziele, Fokus, Hell/Dunkel und reduzierte Bewegung bleiben maßgeblich.

## Chronologische Checkliste

### Phase1 – vollständige Zusammenfassung vor Umsetzung

1. [x] Vorherigen Vorratsblock vollständig lokal abnehmen und separat committen.
2. [x] SH15/SH17 und die18bestehenden Shopping-Punkte abgrenzen.
3. [x] PRODUCT/DESIGN/Implementierungsnachweise und vorhandene Quellpfade lesen.
4. [x] Sämtliche heutigen Positions-/Listen-Schreibpfade inventarisieren.
5. [x] Vorhandene Core-Tabellen und fehlende Generation-/JSON-Felder prüfen.
6. [x] No-DDL-Ansatz gegen eingefrorene Fresh-/Upgrade-/Ready-Verträge prüfen.
7. [x] Reader-Eigentum, Sperrreihenfolge, Revisionsfences und Budgets festlegen.
8. [x] Worker-, Fanout-, Dedupe- und Lebenszyklusvertrag beschreiben.
9. [x] Offizielle Push-Spezifikation, Bibliothek, Lizenz und Providergrenzen recherchieren.
10. [x] Vollständige Snapshot-Felder, Limits und private Cache-Grenzen festlegen.
11. [x] Mobile/Offline-/Push-Zustände und spätere Beweisgrenzen beschreiben.
12. [x] Gesamten Modulplan vor neuer Anwendungsimplementierung veröffentlichen.

### Phase2 – gesamtes Modul implementieren, noch keine neuen Tests ausführen

13. [x] Geschlossene Kontext-/Scope-Datentypen für Shopping definieren.
14. [x] Exakte Revisionsparser mit Überlaufverweigerung ergänzen.
15. [x] Privaten Namespace begrenzt lesen und strukturell validieren.
16. [x] Eigene leere Präferenzen ohne Schreibnebenwirkung lesen.
17. [x] Eigene Reader-taugliche Präferenztransaktion mit direkter Sperrreihenfolge implementieren.
18. [x] Benutzer- und Gesamtquoten atomar durchsetzen.
19. [x] Aktivieren/Abbestellen samt dauerhafter eigener Revision verbinden.
20. [x] Mute-Dauern und Aktivierungs-/Push-Zeitgrenzen implementieren.
21. [x] Andere Settings und Modulversionsmetadaten ausdrücklich erhalten.
22. [x] Aktuell autorisierte eigene Watch-Übersicht implementieren.
23. [x] Version2-Listenevent und geschlossene Änderungsarten definieren.
24. [x] Shopping-afterWrite ohne zusätzliche doppelte Legacy-Events erweitern.
25. [x] Vorherbild für Positionsänderungen und Moves durchreichen.
26. [x] Beide betroffenen Listen beim Move atomar erfassen.
27. [x] Generic Item Delete mit altem Elternbezug verbinden.
28. [x] Generic List Delete vor Cascade korrekt signalisieren.
29. [x] Direkten Planning-Purchase-Delete verbinden.
30. [x] Copy/Merge/Import/Barcode/Nachkauf-Anschlüsse überprüfen und vervollständigen.
31. [x] Rezept-/Pantry-/Planungs- und Checkout-Anschlüsse vervollständigen.
32. [x] Aktivitätsleases und unveränderte Inhaltswerte von Mitteilungen ausschließen.
33. [x] Datenbankseitigen tatsächlichen Ereigniszeitpunkt setzen.
34. [x] Andere Module und bestehenden lokalen Consumer unverändert bedienen.
35. [x] Eigenen dauerhaften Outbox-Consumer implementieren.
36. [x] Receipts und begrenzte Fanout-Fortsetzung implementieren.
37. [x] Aktuelle Watch-/Empfänger-/Generationsprüfung verbinden.
38. [x] Offene60-Sekunden-Buckets aus Verarbeitungszeit ableiten.
39. [x] Digest-Job-Upsert und Deduplizierung atomar verbinden.
40. [x] In-App-Mitteilung unter aktueller Autorität genau einmal anlegen.
41. [x] Neue Jobarten vor Unsupported-Kind-Behandlung verdrahten.
42. [x] Lease-Verlust, Retry und terminale Drop-Gründe behandeln.
43. [x] Archiv-/Delete-/Mute-/Pause-Zustände konsistent behandeln.
44. [x] Verwaiste Watches in eigener begrenzter Transaktion bereinigen.
45. [x] Shopping-Purge um gezieltes Entfernen des Namespace erweitern.
46. [x] Eigenen Notification-Kind/Dedupe-Parser implementieren.
47. [x] Aktuelle generationen-/zielgeprüfte Inbox-Projektion verbinden.
48. [x] Eigene Read-/Dismiss-Rechte erhalten und ungültige Ziele schließen.
49. [x] Neue eigene Retention begrenzen, fremde Datensätze erhalten.
50. [x] Gepinnte MIT-Web-Push-/JWT-Abhängigkeiten integrieren.
51. [x] Optionalen Push-Konfigurationsparser vor Bootstrap ergänzen.
52. [x] VAPID-Schlüsselgenerator ohne Datenbankabhängigkeit implementieren.
53. [x] Separates optionales Compose-Overlay und Secret-Dokumentation ergänzen.
54. [x] Geschlossenen verschlüsselten Subscription-Envelope implementieren.
55. [x] Aktuelle eigene Geräteregistrierung mit Endpoint-Bindung implementieren.
56. [x] Eigene Geräteübersicht und Widerruf implementieren.
57. [x] Gerätequota, Auth-/Sitzungsbindung und Key-ID-Prüfung durchsetzen.
58. [x] Feste Gateway-/HTTPS-/DNS-/TLS-Zielprüfung implementieren.
59. [x] Context-, Proxy-, Redirect-, Antwort- und Parallelitätsgrenzen setzen.
60. [x] Dauerhafte Verwaltungs-/Versandbudgets verbinden.
61. [x] Neutrale Push-Payload und feste Annahmestatus definieren.
62. [x] Push-Referenzjobs ohne Endpoint-/Inhaltspayload anlegen.
63. [x] Lease/Autorität vor Versand prüfen und DB-Transaktion vor HTTP beenden.
64. [x]404/410-Deaktivierung und begrenzte429/Fehler-Wiederholung implementieren.
65. [x] Geheimnisfreie Fehler-/Statusprotokollierung und eigene Retention verbinden.
66. [x] Geschlossene vollständige Offline-Snapshot-Datentypen definieren.
67. [x] Konsistenten Read-only-RR-Abruf mit aktueller eigener Autorität implementieren.
68. [x] Vollständige ungefilterte1001-Sentinel-Abfrage implementieren.
69. [x] Exakte Mengen/Nullable-Felder und erlaubte Leselabels projizieren.
70. [x] UTF-8-Gesamtbudget vor vollständiger Ausgabe prüfen.
71. [x] Aktuelle Autorität/Generation nach Abruf erneut prüfen.
72. [x] Aufnahme-/Ablaufzeit, Umfang und nicht berechtigenden Fingerprint liefern.
73. [x] Kontext-/Watch-/Snapshot-/Push-Routen mit bestehenden Sicherheitsmitteln verbinden.
74. [x] Eigenen Shopping-IndexedDB-Vertrag implementieren.
75. [x] Besitzer-/Sitzungs-/Generations-/Epochen-/Revisionsprüfung atomar implementieren.
76. [x] Listen-/Artikel-/Bytequoten und vollständige atomare Ersetzung implementieren.
77. [x] Ablauf/Pruning ohne Laufzeitverlängerung beim Öffnen implementieren.
78. [x] Späte Antwort nach Löschen/Opt-out/Logout abweisen.
79. [x] Expliziten vollständigen Neuabruf für neue Sitzung implementieren.
80. [x] Alte Generation sperren und ausdrückliches Verwerfen anbieten.
81. [x] Öffentliche neutrale Shopping-Shell und statischen Viewer implementieren.
82. [x] Persönlichen Hinweisbereich außerhalb des Editorbereichs implementieren.
83. [x] Mute-/Speicher-/Konflikt-/Ungewissheitszustände implementieren.
84. [x] Getrennte Push-Konfigurations-/Berechtigungs-/Gerätezustände implementieren.
85. [x] Ausdrückliche Browserfreigabe, Verbindung und Widerruf implementieren.
86. [x] Neutrale SW-Push-/Notificationclick-Verarbeitung implementieren.
87. [x] Offline-Abruf-/Löschaktionen und Geräteübersicht implementieren.
88. [x] Globale Offline-Daten-Beschriftung und gezielte Privacy-Hooks verbinden.
89. [x] Logout/Opt-out/Kontowechsel samt tabübergreifender Rohdatenbereinigung verbinden.
90. [x] Öffentliche SW-Cache-Positivliste und Shopping-Navigationsfallback erweitern.
91. [x] Reader-/Tastatur-/Hell-Dunkel-/Langtext-Zustände im Quellcode vervollständigen.
92. [x] Native Producer-/Worker-/Push-/Snapshot-Testquellen vorbereiten.
93. [x] Pure IndexedDB-/SW-/UI-Vertrags- und Browser-Testquellen vorbereiten.
94. [x] CI-/Verify-/Node-Registrierung und README/API-Dokumentation vervollständigen.
95. [x] Sämtliche Backend-/Frontend-/Netzwerk-/Lebenszyklusquellen unabhängig prüfen.
96. [x] Vollständigen integrierten Modulstand festhalten und erst dann Phase3 öffnen.

### Phase3 – erst jetzt gemeinsam testen und Fehler beheben

97. [x] Vollständige Go-Unit-Suite ausführen.
98. [x] Vollständige registrierte Node-Suite ausführen.
99. [x] Formatierung, Vet und getaggten unbegrenzten Linter ausführen.
100. [x] Modul-/Lizenz-/Vulnerabilitäts-/Gosec-/Secret-Prüfungen ausführen.
101. [x] Alle75SQL-Pins und unveränderte aktuelle Ready-Verträge tatsächlich prüfen.
102. [x] Fresh-/Repeat-/bestückte Vorgängerkomposition prüfen.
103. [x] Alle inventarisierten Producer einschließlich Delete/Move/Planning/Checkout prüfen.
104. [x] Rollback, Replay, No-op und zeitliche Opt-in-Grenzen prüfen.
105. [x] Reader-Eigentum, Namespace-Auditredaktion und Quoten prüfen.
106. [x] Gleichzeitige Präferenz-/Domain-Schreiber und Sperrreihenfolge prüfen.
107. [x] Zwei Worker, Lease-Verlust, Fanout-Fortsetzung und Digest-Dedupe prüfen.
108. [x] Mute, Abbestellen, Pause, Berechtigungsverlust und alte Generationen prüfen.
109. [x] Push-Envelope-Verschlüsselung und aktuelle Zeilen-/Sitzungsbindung prüfen.
110. [x] Kontrollierte echte Protokoll-/HTTP-/SSRF-/Proxy-/Redirect-/DNS-Grenzen prüfen.
111. [x] Providerannahme,404/410,429, Timeout und ungewissen Ausgang prüfen.
112. [x] Vollständige Snapshots über100Positionen und1000/1001-Grenze prüfen.
113. [x]10/11Listen,2MiB/+1Byte, Mehrbyte-/Null-/exakte Mengenfälle prüfen.
114. [x] Gleichzeitige Tabs, Quota, Epochen und späte Antworten prüfen.
115. [x] Laufzeit, neue Sitzung, neue Generation und Rollen-/Kontowechsel prüfen.
116. [x] Tatsächlichen Offline-Neustart über neutrale Shell ausführen.
117. [x] Null Offline-Mutationen/Autoupload und private Cache-Ausschlüsse prüfen.
118. [x] Tatsächliche rohe IndexedDB-Anzahl/Besitzer nach Logout/Opt-out prüfen.
119. [x] UI-Push-Zustände und sichere neutrale SW-Klickverarbeitung prüfen.
120. [x] Mobile/Tastatur/Fokus/Langtext/Hell-Dunkel und Axe tatsächlich prüfen.
121. [x] Betroffene vorhandene Shopping-/Pantry-/Planungs-/Core-Regression ausführen.
122. [x] Fokussierte Linux-Race-Prüfung ausführen.
123. [x] Bestückte verschlüsselte Sicherung/Restore/Purge/Generation tatsächlich prüfen.
124. [x] Aktuelle vollständige Browserkette ausführen.
125. [x] Gefundene Fehler vollständig korrigieren und betroffene Kampagnen wiederholen.
126. [x] Unabhängige Quellen-/DB-/UI-/Nachweisabnahme und verbleibende Grenzen abgleichen.
127. [x] SH15/SH17 erst nach vollständiger lokaler Abnahme gemeinsam abhaken.
128. [x] Exakten lokalen Git-Checkpoint sichern und erst dann nächsten Modulblock beginnen.

## Nachweisgrenzen und offizielle Recherche

Protokolltests mit kontrolliertem Empfänger, synthetische Browser-Push-Events und echte Anbieterzustellung sind getrennte Beweise. Eine tatsächliche externe Zustellung wird nur so bezeichnet, wenn sie mit eingerichteter Gegenstelle und ausdrücklich freigegebenem eigenen Gerät ausgeführt wurde. Operator-Konfiguration und Browser-/OS-Unterstützung bleiben sichtbare Voraussetzungen des optionalen Push-Zweigs. In-App-Hinweise und Offline-Lesekopien benötigen diese Gegenstelle nicht. Kein tatsächlicher Push an andere Personen gehört zur Entwicklung.

Primärquellen, gelesen am29.10.2026: [Web-Push-Verschlüsselung RFC8291](https://www.rfc-editor.org/rfc/rfc8291), [VAPID RFC8292](https://www.rfc-editor.org/rfc/rfc8292.html), [PushManager.subscribe](https://developer.mozilla.org/en-US/docs/Web/API/PushManager/subscribe), [Notificationclick](https://developer.mozilla.org/en-US/docs/Web/API/ServiceWorkerGlobalScope/notificationclick_event), [MIT-Webpush-Tag1.4.0](https://github.com/SherClockHolmes/webpush-go/tree/v1.4.0), [JWT-v5-Releases](https://github.com/golang-jwt/jwt/releases), [Mozilla-Produktionsendpoint](https://mozilla-services.github.io/autopush-rs/), [Apple-Serveranforderungen](https://developer.apple.com/documentation/usernotifications/sending-web-push-notifications-in-web-apps-and-browsers), [Microsoft-WNS-Cloudziele](https://learn.microsoft.com/en-us/windows/apps/develop/notifications/push-notifications/firewall-allowlist-config).

Stand dieses Dokuments: Phase 1 und die vollständige Implementierung/Quellenprüfung in Phase 2 sind abgeschlossen. Alle 40 vorbereiteten Zieldateien wurden mit Vorversions-, SHA- und Byteprüfung übernommen; der vollständige Stand umfasst 527 Anwendungsquellen. Freeze: `411fefccf41f3aa31134836f2e6b3a5e3b17e5293865da11b4ff3d879dbe9324`. Phase 3 beginnt erst nach diesem Gate. Zum Gate-Zeitpunkt wurde noch kein neuer Anwendungstest ausgeführt. Häkchen in Phase 2 sind ausschließlich Implementierungsnachweise; SH15/SH17 bleiben bis zur tatsächlichen vollständigen Phase-3-Abnahme In Arbeit.

Aktualisierung 30.10.2026: Phase3 läuft auf dem dokumentierten aktuellen527-Quellenstand58c. Go247, Node243, Syntax106, getaggter Linter und75SQL-Pins sind bestanden. Die betroffene122er-Datenbankregression ist durch24aktuelle plus98quellgleiche frühere bestandene Haupttests belegt; der ursprüngliche121/1-Lauf bleibt als Fehlernachweis erhalten. Die oben abgehakten nativen Prüfungen beziehen sich auf diese tatsächlichen Kampagnen. Browser-, Race-, operativer Recovery- und abschließender Commit-Nachweis bleiben offen. [Ausgeführte Prüfungen und genaue Grenzen](shopping-module-validation.md).

Aktualisierung 30.10.2026, korrigiertes Layout: Der aktuelle527-Quellenstand ist42c434abc668879ace5c018a505be7e436d47944dc640308f5f58a742266d610. Nach dem echten Mobilfehler wurde nur der unveränderte persönliche Steuerblock unter die Listenpositionen verschoben. Go247, Node245 und24frische native Haupttests bestehen. Der eigenständige Shopping-Browserlauf besteht37Prüfungen/3Axe ohne Verstöße, die bestückte verschlüsselte Recovery11Stufen und die vollständige bestehende Browserkette28Komponenten/912Prüfungen/135Axe/147verifizierte Screenshots. Die390×844-Sichtbarkeitsprüfung blieb unverändert und besteht jetzt. Linux-Race44 wurde tatsächlich auf der Go-quellgleichen Vorversion58c ausgeführt und der ursprüngliche Exportfehler separat reconciliert; die122betroffenen nativen Haupttests sind24aktuell plus98quellgleich historisch. Externe Gerätezustellung, gehostete CI und produktiver Backup-Zeitplan sind keine bestandenen Checks. Unabhängige Schlussabnahme, finaler Index-Scan und lokaler Git-Checkpoint stehen noch aus. [Ausgeführte Prüfungen](shopping-module-validation.md).

Die unabhängige Schlussabnahme hat alle527aktuellen Quellen und alle28Browserberichtshashes erneut geprüft, ohne Abweichung oder neuen Datenschutz-/UI-Befund. SH15/SH17 sind nach lokaler Abnahme gemeinsam in der180er-Liste abgehakt (87verifiziert/0inArbeit/93offen, Einkauf20/20).

Abschluss des Modulblocks: Der Git-Index umfasst genau77 Shopping-Dateien; der zurückgestellte Finanz-Dokumententwurf ist nicht gestaged. Alle720Index-Blobs und75unveränderten SQL-Rohdateien sind gegengeprüft; der vollständige Gitleaks-Scan des Index-Abbilds meldet0Funde. Der lokale Checkpoint wird ohne Remote-Push gesichert; erst danach beginnt der Rezept-Modulblock.
