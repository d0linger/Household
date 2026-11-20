# Household installieren (einfache Installation)

Diese Anleitung braucht nur Docker mit Compose v2. Quellcode, Go oder Skripte werden nicht benötigt. Alles Weitere, etwa Schlüsselrotation und Wiederherstellung, steht im [Betriebshandbuch](operations.md).

## 1. Ordner anlegen und Compose-Datei holen

Lege einen leeren Ordner an, zum Beispiel `household`, und speichere darin [`compose.yaml`](../compose.yaml):

```bash
curl -fsSLO https://raw.githubusercontent.com/d0linger/Household/main/compose.yaml
```

## 2. Einstellungen und Passwörter erzeugen

```bash
(umask 077; set -C; docker run --rm ghcr.io/d0linger/household:production init > .env)
```

Der Befehl erzeugt eine `.env` mit frischen, zufälligen Passwörtern und Schlüsseln. Auf den Bildschirm schreibt er sie absichtlich nicht. `umask 077` sorgt dafür, dass nur dein Benutzer die Datei lesen kann, und `set -C` verhindert, dass eine vorhandene `.env` mit den Schlüsseln einer laufenden Installation überschrieben wird. Bricht der Befehl ab, lösche eine leere `.env` vor dem nächsten Versuch; prüfe das bei einer vorhandenen Datei mit `ls -l .env` (erwartet: `-rw-------`) und korrigiere es sonst mit `chmod 600 .env`.

**Die `.env` ist der Schlüssel zu deinen Daten.** Bewahre eine Kopie sicher getrennt vom Server auf, zum Beispiel in einem Passwortmanager. Ohne sie lassen sich verschlüsselte Daten und Backups nicht mehr lesen. Gib sie niemandem weiter.

## 3. Adresse eintragen

Öffne `.env` mit einem Texteditor und trage bei `HOUSEHOLD_ORIGIN` die Adresse ein, unter der Household im Browser erreichbar sein soll, zum Beispiel `https://haushalt.example.org`.

- **Im Betrieb** braucht Household HTTPS. Davor steht ein Reverse Proxy wie Caddy, Traefik oder nginx, der auf `127.0.0.1:8100` weiterleitet. Trage dessen Netzwerk bei `HOUSEHOLD_TRUSTED_PROXIES` ein.
- **Nur zum Ausprobieren** auf dem eigenen Rechner erzeugst du die Datei stattdessen mit `init --development > .env`. Dann gilt `http://localhost:8100`.

Alles andere in Abschnitt 3 der Datei bleibt unverändert.

## 4. Starten

```bash
docker compose up -d
```

Der erste Start richtet die Datenbank ein und kann einige Minuten dauern. `docker compose ps` zeigt `healthy`, sobald Household bereit ist.

## 5. Anmelden

Öffne die Adresse im Browser. Melde dich als `admin` mit dem Passwort aus `HOUSEHOLD_ADMIN_PASSWORD` an und ändere es sofort.

## Was läuft da?

| Dienst | Aufgabe |
| --- | --- |
| `db` | PostgreSQL-Datenbank, nur im internen Netz erreichbar |
| `app` | Household. Beim Start legt es die Datenbankrollen an, migriert und startet die Oberfläche auf `127.0.0.1:8100`. |
| `backup` | Sicherungsdienst: verschlüsselte Backups von Datenbank und Dateien nach dem Zeitplan aus **Einstellungen → Sicherung** (standardmäßig täglich um 03:00 Uhr), auf Wunsch zusätzlich extern, dazu Probewiederherstellungen und Wiederherstellungen aus der App. Über diesen Dienst laufen auch die Wartungsbefehle. |

Alle Dienste laufen ohne Root-Rechte: die Datenbank als Benutzer `postgres` (70), Household als Benutzer 10001.

- **Daten:** Die Daten liegen in Docker-Volumes, nicht in einem Ordner. `docker compose down` behält sie. **`docker compose down -v` löscht alles.**
- **Texterkennung (OCR):** Ist im Image enthalten und automatisch aktiv.
- **Benachrichtigungen (Web Push):** Sind aktiv, weil `init` einen Schlüssel erzeugt. Ein echtes Gerät braucht die HTTPS-Adresse.
- **Optionale Anbindungen:** Dokumente per E-Mail oder Paperless sowie Zählerstände aus Home Assistant oder MQTT schaltest du ein, indem du in Abschnitt 4 der `.env` die passenden Zeilen ausfüllst, das `#` entfernst und `docker compose up -d` ausführst.
- **Scanner-Ordner:** Entferne in `compose.yaml` das `#` vor der Zeile `./scanner:/inbox:ro` und setze in `.env` `HOUSEHOLD_DOCUMENT_WATCH_DIR=/inbox`.
- **Eigene CA für Zähler:** Ergänze beim Dienst `app` unter `volumes` die Zeile `- ./meters-ca.pem:/config/meters-ca.pem:ro` und setze in `.env` `HOUSEHOLD_METERS_CA_FILE=/config/meters-ca.pem`.

## Updates

```bash
docker compose pull
docker compose up -d
```

Migrationen laufen beim Start automatisch. Lies vorher die Versionshinweise. Behalte die letzte Backup-Kopie, bis die neue Version läuft. Wer Updates genau steuern will, ersetzt `:production` in `compose.yaml` durch einen festen Digest.

## Backups

Alles Wichtige steht in der App unter **Einstellungen → Sicherung** (nur für Administratoren):

- **Zustand:** ob die letzte Sicherung geklappt hat, wann die nächste fällig ist und wann zuletzt eine Sicherung probeweise wiederhergestellt wurde.
- **Jetzt sichern**, **Prüfen** und **Wiederherstellen** per Knopfdruck. Die App gibt den Auftrag nur weiter; ausgeführt wird er vom Dienst `backup`, der als einziger die Zugangsdaten kennt.
- **Zeitplan und Aufbewahrung:** Uhrzeit, Häufigkeit und wie viele tägliche, wöchentliche und monatliche Sicherungen bleiben. Die Probewiederherstellung läuft standardmäßig wöchentlich.
- **Warnungen:** Schlägt eine Sicherung fehl oder ist sie überfällig, erscheint eine Mitteilung für alle Administratoren. Zusätzlich kann der Sicherungsdienst Warnungen nach außen schicken, etwa auf dein Handy über [ntfy](https://ntfy.sh) oder per E-Mail: Fülle dazu in Abschnitt 4 der `.env` die Zeilen `HOUSEHOLD_ALERT_…` aus.
**Speicherort:** Das lokale Backup liegt im Volume `backups` auf demselben Rechner. Das schützt vor Bedienfehlern, aber nicht vor Plattenausfall, Diebstahl oder Brand. Richte deshalb ein **externes Ziel** ein.

### Externes Backup (S3, Backblaze, NAS)

Trage in Abschnitt 4 der `.env` das Ziel ein, entferne das `#` und führe `docker compose up -d` aus:

```bash
HOUSEHOLD_BACKUP_OFFSITE_REPOSITORY=s3:https://s3.eu-central-1.amazonaws.com/mein-bucket/household
HOUSEHOLD_BACKUP_OFFSITE_PASSWORD=ein-eigenes-langes-passwort
HOUSEHOLD_BACKUP_S3_ACCESS_KEY_ID=…
HOUSEHOLD_BACKUP_S3_SECRET_ACCESS_KEY=…
```

- **S3-kompatibel:** AWS, Backblaze B2, Wasabi, Hetzner Object Storage oder ein eigenes MinIO, jeweils mit `s3:https://adresse/bucket/ordner`.
- **restic-REST-Server:** `rest:https://adresse/pfad`, Benutzer und Passwort über `HOUSEHOLD_BACKUP_REST_USERNAME` und `HOUSEHOLD_BACKUP_REST_PASSWORD`.
- **NAS oder USB-Platte:** ein absoluter Pfad, den du beim Dienst `backup` unter `volumes` einbindest, zum Beispiel `- /mnt/nas/household:/offsite` und `HOUSEHOLD_BACKUP_OFFSITE_REPOSITORY=/offsite`.

Danach erscheint in der App das externe Ziel. Teste die Verbindung mit **Externe Verbindung testen** und schalte **Automatisch extern kopieren** ein. Verwende für das externe Ziel ein **eigenes Passwort** und bewahre es zusammen mit der `.env` getrennt vom Server auf. Zugangsdaten gehören nie in die Adresse selbst; Household lehnt das ab.

### Wiederherstellen

- **Was du brauchst:** Zum Wiederherstellen brauchst du das Backup **und** die `.env`.
- **In der App:** Bei jedem Wiederherstellungspunkt gibt es **Wiederherstellen**. Du bestätigst deine Anmeldung und tippst die angezeigte Bestätigung (zum Beispiel `WIEDERHERSTELLEN 0123ABCD`) ein. Household sichert zuerst den aktuellen Stand, prüft die Sicherung vollständig, pausiert kurz und schaltet dann um. Danach melden sich alle neu an. Der vorherige Stand bleibt aufbewahrt, bis du ihn verwirfst.
- **Per Befehl:** `docker compose run --rm --no-deps backup restore SNAPSHOT_ID --confirm "WIEDERHERSTELLEN 0123ABCD"` (mit `--offsite` aus dem externen Ziel). Zurück zum vorherigen Stand: `docker compose stop app`, `docker compose run --rm --no-deps backup restore-rollback`, `docker compose up -d`.
- **Neuer Rechner:** Wie du nach einem Totalausfall wiederherstellst, steht im [Betriebshandbuch](operations.md#disaster-recovery).

Backup prüfen ohne etwas zu verändern: Der Befehl stellt das Backup in eine eigene vorübergehende Datenbank (`household_restore_auto_…`) auf demselben Datenbankserver wieder her, prüft alles und löscht sie danach wieder, auch nach einem Fehler. In der App heißt das **Prüfen**.

```bash
docker compose run --rm --no-deps backup restore-check latest
```

## Wartung

Wartungsbefehle laufen über den Dienst `backup`, weil nur er die Rechte des Datenbank-Owners hat:

| Aufgabe | Befehl |
| --- | --- |
| Sofort ein Backup | `docker compose run --rm --no-deps backup backup` (mit `--offsite` auch extern) |
| Admin-Passwort vergessen | Neues Passwort bei `HOUSEHOLD_ADMIN_PASSWORD` in `.env` eintragen, dann `docker compose stop app`, `docker compose run --rm --no-deps backup recover admin` und `docker compose up -d` |
| Datenbank nur einrichten/migrieren | `docker compose run --rm --no-deps app setup` |
| Logs ansehen | `docker compose logs --tail=100 app` |

Weitere Befehle (Schlüsselrotation, Löschen von Modulen, Lagerbestand prüfen) stehen im [Betriebshandbuch](operations.md).

## Export, Teilen und Hilfe

- **Alle Daten exportieren:** Unter *Einstellungen → Sicherung* lädt ein Administrator alle Einträge als ZIP-Datei mit CSV-Tabellen herunter, etwa für einen Umzug. Fotos und Dokumente sind nicht enthalten; dafür gibt es den Dokumente-Export und die Sicherung.
- **Änderungsprotokoll exportieren:** Unter *Einstellungen → Protokoll* exportierst du die gefilterte Historie als JSONL-Datei. Jede Zeile trägt eine Prüfsumme, die auf der vorherigen aufbaut. So fällt später auf, wenn jemand die Datei verändert.
- **Teilen:** Ist Household auf dem Handy installiert, erscheint es im Teilen-Menü anderer Apps. Fotos und PDFs landen zuerst nur auf dem Gerät; unter *Geteilte Dateien* speicherst du sie als Dokument.
- **Hilfe:** Unter *Einstellungen → Hilfe* stehen die wichtigsten Begriffe kurz erklärt.
- **Optionale Prüfungen:** Mit `HOUSEHOLD_UPDATE_CHECK=true` zeigt die Systemseite an, ob es eine neuere Version gibt. Mit `HOUSEHOLD_PASSWORD_BREACH_CHECK=true` lehnt Household neue Passwörter ab, die aus Datenlecks bekannt sind. Dabei verlässt nur ein kurzer Teil einer Prüfsumme den Server, nie das Passwort. Beide Prüfungen sind standardmäßig aus.

## Rootless Docker

Mit Rootless Docker läuft schon der Docker-Dienst selbst ohne Root. Ein Ausbruch aus einem Container landet dann bei einem normalen Benutzer. `compose.yaml` funktioniert unverändert:
- Alle Container laufen ohne Root.
- Der Port 8100 liegt über 1024.
- Die Datei setzt keine cgroup-Grenzen, die Rootless Docker erst freigeschaltet bekommen müsste.

Einrichtung unter Debian oder Ubuntu (Linux mit systemd und cgroup v2), als der Benutzer, der Household betreiben soll:

```bash
# einmalig als root: Pakete und User-Namespaces
sudo apt-get install -y uidmap dbus-user-session docker-ce-rootless-extras
sudo systemctl disable --now docker.service docker.socket   # optional: rootful Docker abschalten
# als normaler Benutzer
dockerd-rootless-setuptool.sh install
systemctl --user enable --now docker
sudo loginctl enable-linger "$USER"                         # Dienste laufen auch ohne Anmeldung
echo 'export DOCKER_HOST=unix://$XDG_RUNTIME_DIR/docker.sock' >> ~/.bashrc
export DOCKER_HOST=unix://$XDG_RUNTIME_DIR/docker.sock
docker info --format '{{.SecurityOptions}}'                 # muss "rootless" enthalten
```

`docker-ce-rootless-extras` stammt aus dem offiziellen Docker-Paketarchiv. Danach gelten die Schritte 1–5 oben unverändert, nur eben als dieser Benutzer.

Unterschiede zu rootful Docker:
- **Speicherort der Daten:** Die Volumes liegen unter `~/.local/share/docker` des Benutzers. Sichere diesen Ordner nicht als root, sondern über das Household-Backup.
- **Reverse Proxy:** Läuft er auf demselben Rechner, erreicht er Household wie gewohnt über `127.0.0.1:8100`. Die Quelladresse, die Household sieht, ist aber die des Rootless-Netzwerks. Ermittle sie über die Logs (`docker compose logs app`) und trage dieses Netz bei `HOUSEHOLD_TRUSTED_PROXIES` ein. Vertraue nie pauschal allen Adressen.
- **Start ohne Anmeldung:** Ohne `enable-linger` stoppen die Container, wenn sich der Benutzer abmeldet.

## Umstieg von einer älteren Installation mit `.local/secrets`

Bis November 2026 bestand die Installation aus mehreren Compose-Dateien mit Passwort-Dateien unter `.local/secrets`. Die Daten bleiben beim Umstieg erhalten. Die bisherigen Werte müssen aber in die neue `.env`, sonst passen Datenbankrollen und Verschlüsselung nicht mehr zusammen.

1. Mit dem alten Setup ein Backup machen und mit `restore-check` prüfen.
2. Die alte Installation stoppen, **ohne** `-v`: `docker compose down`.
3. Eine **neue** Datei erzeugen, ohne eine vorhandene `.env` anzutasten: `(umask 077; set -C; docker run --rm ghcr.io/d0linger/household:production init > .env.new)`. Darin diese Werte durch den Inhalt der alten Dateien ersetzen und Einstellungen wie `HOUSEHOLD_ORIGIN` und `HOUSEHOLD_TRUSTED_PROXIES` aus einer alten `.env` übernehmen. Erst danach eine alte `.env` sichern (zum Beispiel `mv .env .env.old`) und die neue aktivieren: `mv .env.new .env`. Waren `owner_password` und `app_password` bisher identisch, übernimm nur `owner_password` und behalte für `HOUSEHOLD_APP_PASSWORD` den neu erzeugten Wert: Die beiden Passwörter müssen sich unterscheiden, und `start` setzt das neue App-Passwort beim ersten Start selbst.

   | Einstellung in `.env` | alte Datei in `.local/secrets` |
   | --- | --- |
   | `POSTGRES_PASSWORD` | `postgres_password` |
   | `HOUSEHOLD_OWNER_PASSWORD` | `owner_password` |
   | `HOUSEHOLD_APP_PASSWORD` | `app_password` |
   | `HOUSEHOLD_CSRF_SECRET` | `csrf_secret` |
   | `HOUSEHOLD_RATE_SECRET` | `rate_secret` |
   | `HOUSEHOLD_ENCRYPTION_KEY` | `encryption_key` (bei einem Schlüsselring stattdessen `HOUSEHOLD_ENCRYPTION_KEYRING`, siehe Betriebshandbuch) |
   | `HOUSEHOLD_ADMIN_PASSWORD` | `bootstrap_password` |
   | `RESTIC_PASSWORD` | `restic_password` |
   | `HOUSEHOLD_WEB_PUSH_VAPID_PRIVATE_KEY` | `web_push_private_key` (falls vorhanden, sonst den neuen Wert behalten) |

   Übernimm auch Adresse, Proxy und Namen aus der alten `.env` sowie die Einstellungen für Documents und Meters. Die Passwort-Dateien dieser Anbindungen kommen als Werte in Abschnitt 4.
4. Die neue `compose.yaml` in den Ordner legen und `docker compose up -d` ausführen. Die Volumes `postgres` und `uploads` werden weiterverwendet, wenn der Ordner (also der Projektname) gleich bleibt.
5. Das alte Backup-Volume (`…_backup_repository`) wird nicht mehr benutzt. Das neue Backup beginnt im Volume `backups`. Behalte das alte, bis du es nicht mehr brauchst.

## Sicherheitshinweise

- Die Passwörter stehen in `.env` und damit in der Umgebung der Container. Der Dienst `backup` kennt auch das PostgreSQL-Admin-Passwort, weil er für `restore-check` eine vorübergehende Datenbank anlegt. Wer Docker auf dem Rechner bedienen darf, kann sie lesen. Gib Docker-Rechte deshalb nur Administratoren.
- Nach der Einrichtung läuft die Oberfläche nur mit den Rechten des App-Datenbankkontos. Das PostgreSQL-Admin-Passwort und das Owner-Passwort werden nur beim Start verwendet und danach aus dem Prozess entfernt.
- Die `.env` sollte nur für den Betreiber lesbar sein: `chmod 600 .env`.
- Unter **Einstellungen → Sicherheit** kannst du als Administrator verlangen, dass alle Administratoren (und auf Wunsch Bearbeiter) einen zweiten Faktor einrichten. Wer sich von einem neuen Browser oder Gerät anmeldet, bekommt eine Sicherheitsmitteilung.
