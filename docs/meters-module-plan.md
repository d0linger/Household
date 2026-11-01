# Zähler und Energie – vollständiger Modulplan ME 01–ME 20

Stand:1.November 2026. Aufgaben ist vollständig lokal abgenommen: Code 74 e 049 f, Abnahme 94 a 2 d 4 a. Ausgangsstand:140/180 verifiziert,40 offen.

## Vollständige Zusammenfassung vor der Umsetzung

Der Block umfasst alle 20 freigegebenen Verbesserungen. Zähler, physische Geräte und logische Register werden getrennt; Ablesungen,Telemetrie und Leistungswerte behalten ihre fachliche Bedeutung. Verbrauch und Kosten zeigen Datenabdeckung, Schätzungen und Wechselgrenzen ausdrücklich. Korrekturen bleiben nachvollziehbar; Tarife und Intervalle werden gezielt neu berechnet. Optionale Adapter bleiben ohne Betreiberkonfiguration ausgeschaltet. Dokumente beginnen nach gemeinsamer Zählerabnahme.

| Kriterien | Geplante vollständige Anpassungen |
|---|---|
| ME 01–ME 04 | Mobile Übersicht, geführte Ablesung mit Foto, genaue Intervalle, Messqualität und Lücken in Diagramm und Tabelle. |
| ME 05–ME 09 | Geräteaustausch, versionierte Tarife, Kosten/Abschläge, ehrlicher Periodenvergleich, getrennte Bezug/Einspeisung/Erzeugungsregister. |
| ME 10–ME 15 | Echte Ableseerfüllung, optionale HA-/MQTT-Adapter, geprüfter CSV-Import, belegte Anomaliehinweise und bestätigte Finanzübergabe. |
| ME 16–ME 20 | Getrennte Rohtelemetrie, Stunden/Tages/Monatsaggregate, gezielte Korrekturinvalidierung, strikte Messgrößentypen und große Zeitreihen. |

## Architektur und Grenzen

Standard-library HTTP undlokaleembed.FS-Assets. Alle Datenabfragen sindparameterisiert, zeitlich undmengenmäßig begrenzt. Core-Verträge übernehmen Dateien, bestätigte Finanzquellen, Audit und Jobs; keine FKs zuoptionalen Geschwistermodulen. Neue Vorwärtsmigrationen erhalten alle 97 freigegebenen SQL-Rohbytes. Bestandswerte bleiben erhalten; unbekannte Endstände werden nicht erfunden. Manuelle Werte und Telemetrie erhalten getrennte Speicher-/Retentionverträge. Exakte Dezimalarithmetik ersetzt Gleitkommazahlen.

Writes prüfen aktuelle Session, Haushaltsrolle, Modulzustand, Generation und Version vor dauerhaftem Idempotenzbeleg. Secretwerte werden ausschließlich aus Betreiber-Dateigeheimnissen geladen und nie an Browser,Audit oder Logs ausgegeben. Netzwerkadapter verwenden nur explizitkonfigurierte Ziele, feste Topics/Entitäten, kurze Timeouts, begrenzte Bodies/Frames undkeine Wildcard-Abonnements. Reale Gegenstellenprüfungen gehören zur Phase 9; fehlende produktive Zugänge dürfen nicht als Betriebsabnahme gelten.

## Mobile Struktur

Übersicht → Zähler → Register/Verlauf → „Ablesen“. Der Ableseablauf zeigt Gerät/Einheit/letzten Stand und fordert Messung oder Schätzung ausdrücklich an. „Gerätwechseln“ stellt alten Endstand, neuen Anfangsstand und Datum nebeneinander. Kosten erklären Zeitraum, Tarif,Abdeckung und Schätzmethodik. CSV und Adapter zeigen Zuordnung/Vorschau vor Übernahme. Jede Diagrammfläche hat einegleichwertige Tabelle; Konflikte und Offlinezustände erhalten Eingaben im Seitenspeicher.

## Chronologische Checkliste

### Phase 1 – vollständiger Umfang und Grenzen vor Umsetzung

- [x] 001. Abgenommene Aufgaben-Voraussetzung und beide lokalen Checkpoints dokumentieren.
- [x] 002. Alle 20 ME-Kriterien mit bisherigen Tabellen und Abläufen abgleichen.
- [x] 003. Bestehende generische Erfassung und 10.000 er Altberichtgrenze aufnehmen.
- [x] 004. Mobile Übersicht, Erfassung, Verlauf, Kosten, Ableseplan und Integrationen festlegen.
- [x] 005. Zähler, physisches Gerät und logisches Register getrennt modellieren.
- [x] 006. Kumulative Stände, Intervallmengen und momentane Leistung strikt trennen.
- [x] 007. UTC-Instanten, Haushaltszeitzone und sichtbare DST-Entscheidungen festlegen.
- [x] 008. Messung, Schätzung, Lücke und unbekannte Wechselgrenze fachlich definieren.
- [x] 009. Exakte Dezimalwerte, Einheiten und Rundungsregeln vereinbaren.
- [x] 010. Alle Zugriffs-,Generations-,Wiederholungs- und Betriebsgrenzen beschreiben.

### Phase 2 – Datenmodell und überprüfbare Verträge

- [x] 011. Normalisierte Register, Gerätemappings und Wechselvorgänge spezifizieren.
- [x] 012. Unveränderliche Messrevisionen und nachvollziehbare Korrekturen spezifizieren.
- [x] 013. Rohtelemetrie und manuelle Ablesungen getrennt modellieren.
- [x] 014. Zeitintervalle, Stunden/Tages/Monatsaggregate und Dirty-Bereiche definieren.
- [x] 015. Tarifstände mit Gültigkeit, Grundpreis und Arbeitspreis definieren.
- [x] 016. Abschläge, bestätigte Rechnungen und Übergabequellen definieren.
- [x] 017. Ablesepläne und echte erfüllende Messidentitäten definieren.
- [x] 018. Importläufe, Quellen, Dublettenschlüssel und Validierungsberichte definieren.
- [x] 019. Optionale HA-/MQTT-Zuordnungen samt Betriebsstatus und Aufbewahrung definieren.
- [x] 020. Alle 20 Abnahmen, Negativfälle und spätere gemeinsame Prüfgates zuordnen.

### Phase 3 – gesamte Datenbankimplementierung

- [x] 021. Unveränderte 97 SQL-Vorgänger erfassen und neue Vorwärtsmigration abgrenzen.
- [x] 022. Core 32/Meters 1-Vorgänger einschließlich vorhandener Audit-/Finanzverträge sicher prüfen.
- [x] 023. Registertabellen samt Haushalts-/Geräte-Fremdschlüsseln erstellen.
- [x] 024. Bestandsregister und bekannte Gerätegrenzen ohne erfundene Endstände übernehmen.
- [x] 025. Messwertzuordnung, exakte Typ-/Einheitenchecks und eindeutige Quellenidentitäten ergänzen.
- [x] 026. Unveränderliche Revisions- und Wechselnachweise anlegen.
- [x] 027. Tarif-,Abschlags- und bestätigte Rechnungstabellen anlegen.
- [x] 028. Plan-,Erfüllungs- und Anomaliekonfiguration anlegen.
- [x] 029. Begrenzte Import-/Adapter- und Betriebsmetadaten anlegen.
- [x] 030. Getrennte Telemetrie-,Intervall-,Aggregat- und Invalidierungstabellen anlegen.
- [x] 031. Zeit-/Registerindizes für große Messreihen ergänzen.
- [x] 032. Nichtüberlappende Gerätegrenzen und Tarife unter serialisierten Schreibsperren schützen.
- [x] 033. Unveränderliche Fakten mit eng begrenzten Triggern und Rollenrechten sichern.
- [x] 034. Alle neuen Tabellen in den zentralen Auditvertrag aufnehmen.
- [x] 035. Exakte Readiness-,Vorwärtsreihenfolge-,Purge- und Rohbyte-Pins integrieren.

### Phase 4 – gesamte fachliche Rechenlogik

- [x] 036. Exakte Dezimalparser und begrenzte Quantisierung implementieren.
- [x] 037. Typ-/Einheiten-/Flussrichtungsvalidierung implementieren.
- [x] 038. Echte Nachbarintervalle kumulativer Stände berechnen.
- [x] 039. Bestätigten Überlauf von ungeklärtem Rücksprung unterscheiden.
- [x] 040. Intervallmengen ohne erneute Differenzbildung verarbeiten.
- [x] 041. Momentane Leistung nur mit expliziterzeitlicher Integration in Energie überführen.
- [x] 042. Schätzungen, Messlücken und Abdeckungsanteile berechnen.
- [x] 043. Gerätewechsel mit altem Endstand/neuem Anfangsstand und möglicher Lücke berechnen.
- [x] 044. Tarifgrenzen exaktaufteilen und unvermeidbare zeitliche Verteilung als Schätzung kennzeichnen.
- [x] 045. Grundpreis nach ziviler Gültigkeit und Preisrundung berechnen.
- [x] 046. Kosten gegen tatsächliche Abschläge vergleichen.
- [x] 047. Periodenvergleich nur mit expliziter Abdeckung und gleichem Registertyp zulassen.
- [x] 048. Konfigurierbare Anomalien erst mit ausreichender Historie berechnen.
- [x] 049. Stunden/Tages/Monats-Buckets nach tatsächlichen Zeitzonengrenzen bilden.
- [x] 050. Gezielte Nachbar-/Tarifinvalidierung ohne globalen Vollscan implementieren.

### Phase 5 – gesamte autorisierte Store-/API-Implementierung

- [x] 051. Autorisierte begrenzte Read-Snapshots und aktuelle Sessionprüfung implementieren.
- [x] 052. Haushalts-/Generations-/Register-Schreibsperren in fester Reihenfolge integrieren.
- [x] 053. Dauerhafte Idempotenz mit aktuellem Recht vor Belegwiederholung implementieren.
- [x] 054. Zähler-/Register-/Geräteverwaltung implementieren.
- [x] 055. Mobile Ablesung einschließlich Foto über autorisierten Core-Dateivertrag implementieren.
- [x] 056. Messkorrektur mit unveränderlicher Alterfassung und Nachbarprüfung implementieren.
- [x] 057. Nebenläufigsicheren Gerätewechsel vollständigtransaktional implementieren.
- [x] 058. Tarifänderung mit Überschneidungsprüfung und Neuberechnung implementieren.
- [x] 059. Abschlags-/Rechnungsprüfung mit ausdrücklicher Finanzfreigabe implementieren.
- [x] 060. Echte Core-Finanzübergabe mit deduplizierter Quellenidentität implementieren.
- [x] 061. Ableseplan mit passender tatsächlicher Messung und transparentem Fälligkeitsstand implementieren.
- [x] 062. CSV-Profil mit expliziter Zeitzone,Register,Einheit und Vorschaubericht implementieren.
- [x] 063. Importübernahme mit begrenzten Zeilen und dauerhafter Dublettenkontrolle implementieren.
- [x] 064. Zeitgefilterte Messwertseiten und vollständigaggregierte Auswertung implementieren.
- [x] 065. Geschlossene JSON-/Query-Verträge,CSRF,Reader-/Fremdhaushalts- und Versionssperren integrieren.

### Phase 6 – gesamte Adapter-/Worker-Implementierung

- [x] 066. Optionale Betreiberkonfiguration für freigegebene Adapterziele und Dateigeheimnisse ergänzen.
- [x] 067. Home-Assistant-Entitätszuordnung mit explizitem Messgrößentyp und Einheit implementieren.
- [x] 068. Begrenzten HA-Abruf mit Timeout,TLS-/Zielbindung undohnefreie URL implementieren.
- [x] 069. Authentifizierten MQTT-Transport mit exakten Topics undbegrenzten Frames implementieren.
- [x] 070. MQTT-Nachrichten validieren,Quellzeitund Dubletten behandeln und Quittierungen korrekt binden.
- [x] 071. Rohtelemetrie in eigenem begrenztem Ingest-Vertrag speichern.
- [x] 072. Adapterstatus,Fehlergründe,Backoffund Deaktivierung sichtbar machen.
- [x] 073. Geleaste Aggregat-/Invalidierungsverarbeitung mit endlichen Batches implementieren.
- [x] 074. Aufbewahrung nur nach erfolgreicher Aggregatbildung undexpliziter Konfiguration durchführen.
- [x] 075. Worker-/Shutdown-/Generationsbarrieren und unabhängige Jobfehler integrieren.

### Phase 7 – gesamte mobile Oberfläche

- [x] 076. Bestehende Designsprache und tatsächliche Referenzansichten für den Zählerarbeitsbereich laden.
- [x] 077. Übersicht mit letztem Stand,Qualität,Verbrauchundfälliger Ablesung implementieren.
- [x] 078. Mobile Schritterfassung mit aktivem Gerät,Register,letztem Standund Foto implementieren.
- [x] 079. Zähler-/Register-/Geräteeditor mit sichtbarem Messgrößentyp implementieren.
- [x] 080. Gerätewechsel-Vorschau mit Lückeundbeiden Grenzständen implementieren.
- [x] 081. Verlauf mit zugänglichem Diagrammundgleichwertiger Datentabelle implementieren.
- [x] 082. Tarife,Kosten,Abschlägeundexplizite Schätzkennzeichnung implementieren.
- [x] 083. Periodenvergleich mit Abdeckungundfehlenden Zeiträumen implementieren.
- [x] 084. Ableseplanundbegründete Anomaliehinweise implementieren.
- [x] 085. CSV-Vorschau mit Zeilenfehlern,Einheitenund Dubletten implementieren.
- [x] 086. HA-/MQTT-Zuordnungund Betriebsstatus ohne Geheimnisoffenlegung implementieren.
- [x] 087. Rechnungsprüfungundbestätigte Finanzübergabe implementieren.
- [x] 088. Tastatur,Fokus,44 px Bedienflächenundresponsive Leerdatenzustände ergänzen.
- [x] 089. Netzverlust,Kollisionundgleichschlüsselige Wiederholung im Seitenspeicher integrieren.
- [x] 090. Lokale Assets,Navigationund README-Konfiguration vollständig aktualisieren.

### Phase 8 – vollständiger Quellabschluss vor Tests

- [x] 091. Alle 20 Kriterien gegen die gesamtefertige Implementierung abgleichen.
- [x] 092. Neue Unit-/SQL-/HTTP-/Adapter-/Browser-Testquellen vollständig vorbereiten.
- [x] 093. Altebestückte Upgrade-/Audit-/Purge-Fixtures auf die echten Vorgänger abgleichen.
- [x] 094. Cross-Modul-Quellenrechte bei Finanzübergabe und Dateien abschließend prüfen.
- [x] 095. Adaptergeheimnisse,Netzwerkziele und Auditredaktion abschließend prüfen.
- [x] 096. Alle Job-Batches und Abbruch-/Leasegrenzen im Quellstand abgleichen.
- [x] 097. Dievollständige Migrationsreihenfolge einschließlich Altvorgängern dokumentieren.
- [x] 098. Betreiberkonfiguration und Deaktivierung sämtlicher Adapter dokumentieren.
- [x] 099. Allefreigegebenen SQL-Rohbytes undneuen Migrationspins vergleichen.
- [x] 100. Unveränderlichen Phasenabschluss und alle noch nichtausgeführten Prüfungen dokumentieren.

### Phase 9 – erst nach vollständiger Umsetzung testen und korrigieren

- [x] 101. Vollständige gewöhnliche Go-Suite ausführen.
- [x] 102. Typen,Dezimalgrenzen,Überlauf,Schätzungund Datenlücken gemeinsam prüfen.
- [x] 103. Gerätewechsel,Tarifgrenzenund Kostenrundung nativ prüfen.
- [x] 104. Bestücktes Core 32/Meters 1-Upgrade mit historischen Werten prüfen.
- [x] 105. Readiness-,Triggerkörper-,Owner-/ACL-Driftund Dirty-/Zukunftsgrenzen prüfen.
- [x] 106. Aktuelle Session,Reader,Fremdhaushalt,CSRF,Versionund Generation prüfen.
- [x] 107. Echteparallele Ablesung,Korrektur,Wechselundgleicher Schlüssel prüfen.
- [x] 108. CSV-/HA-/MQTT-Adapter gegen tatsächlichlaufende Testgegenstellen prüfen.
- [x] 109. Leases,Backoff,Shutdown,Retentionundgezielte Invalidierung prüfen.
- [x] 110. Mehrals 10.000 Rohwerte über begrenzte Zeitaggregateund Seiten prüfen.
- [x] 111. Allefestgestellten Fehler systematisch auf gemeinsamer Quelle beheben.
- [x] 112. Betroffeneundvollständige Linux-Race-Regression mit ehrlicher Quellenbindung ausführen.
- [x] 113. Node,Formatierung,vet,Linter,Gosec,govulncheck,Moduleund Workflows ausführen.
- [x] 114. Desktop/Mobil,Tastatur,Axeundtatsächliche Screenshots prüfen.
- [x] 115. Aktuelle Runtime/OCR/Backup-Imagesundlokale OCI-/SBOM-/Provenienzbindung prüfen.

### Phase 10 – gemeinsame Modulabnahme vor Dokumenten

- [x] 116. Echteverschlüsselte Sicherung/Wiederherstellungundrestaurierten Zähler-Purge ausführen.
- [x] 117. Vollständigebestehende Modul-Browserregression auf gleicher Produktionsquelle ausführen.
- [ ] 118. Exakten Git-Index auf Geheimnisseundunveränderte SQL-Vorgänger prüfen.
- [ ] 119. Alle 20 ME-Kriterien erstnach Nachweisenverifizierenundlokalcheckpointen.
- [ ] 120. Checklisteauf 160 aktualisierenunderstdanach Dokumentebeginnen.

## Aktueller Stand

117/120 Unterpunkte bearbeitet. Vollständige776Haupttest-Identitäten haben tatsächliche, vollständig bestandene und quellenqualifizierte Linux-Race-Bäume;70,51%Statement-Abdeckung. Ursprüngliche Fehler/Timeouts bleiben dokumentiert. Alle16geprüften Fixture-Korrekturen sind exakt übernommen; keine Produktions-/SQL-Änderung. Die erneut ausgeführten12Qualitätsgates (339gewöhnliche Go-Haupttests/1.787Terminals) undNode297bestehen auf finaler Quelle32c7e4d8. Reale117Browserprüfungen/14manuell geprüfte Ansichten/14Axe,33Komponenten/1.556Prüfungen/209Screenshots/197Axe,16Recovery-/Purge-Stufen sowie3Images/strenge OCI-Prüfung sind über identische Produktionsdateien qualifiziert. Exakter Indexscan, lokaler Code-Checkpoint und gemeinsame ME-Promotion bleiben offen. [Prüfnachweise](meters-module-validation.md).
