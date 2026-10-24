# Household — 220-step master plan

Approved 2026-10-24. This is the chronological acceptance plan exported from the interactive concept. Current implementation status, executed checks and unresolved findings are maintained in [the live checklist](implementation.md). The sequence below is a specification, not a claim that every acceptance check has passed.

Selected scope: one household, German UI, EUR, Europe/Vienna, Go net/http, PostgreSQL, embedded local assets and Docker Compose. Shopping, fuel, recipes, pantry and expenses start enabled; chores, meters, documents and meal planning are optional. Local manual capture ships by default; Tesseract is an optional Docker image. Web Push, cloud AI and passkeys are deferred. Public deployment requires its own origin and instruction.

## Phase 1: Umfang & Freigabe

Ein bestätigter Produktumfang mit überprüfbaren Abnahmekriterien.

Dependency: approved product scope.

1. Modulare Architektur und die ersten fünf Module freigeben.
2. Einen Haushalt pro Installation bestätigen.
3. Deutsch als Oberfläche und Englisch als Dokumentation bestätigen.
4. Berechtigungen für Admin, Editor und Leser freigeben.
5. Visuelle Richtung und mobile Navigation bestätigen.
6. Lokale OCR für das erste Bereitstellungsprofil auswählen.
7. Über die optionale Web-Push-Funktion entscheiden.
8. Erste Erweiterungsmodule auswählen.
9. Aufbewahrung und Wiederherstellungsziele freigeben.
10. Anforderungen mit Abnahmekriterien verknüpfen.

## Phase 2: Repository & Regeln

Ein nachvollziehbarer Entwicklungsprozess mit synthetischen Testdaten.

Dependency: Phase 1.

11. Modulname, Lizenz und unterstützte Plattformen festlegen.
12. Architekturentscheidungen dokumentieren.
13. Paket- und Modulstruktur definieren.
14. Formatierung, Benennung und Fehlerbehandlung festlegen.
15. Einen reproduzierbaren lokalen Prüfeinstieg definieren.
16. Eine grundlegende CI-Prüfung einrichten.
17. Geheimnisse und Haushaltsdaten aus Git ausschließen.
18. Unnötige Dateien aus dem Docker-Build ausschließen.
19. README-Pflege als verbindliche Review-Regel festhalten.
20. Deterministische Beispieldaten für die Module definieren.

## Phase 3: Go-Laufzeit

Ein geordnet startender und herunterfahrender HTTP-Dienst.

Dependency: Phase 2.

21. Abhängigkeiten explizit im Anwendungseinstieg zusammensetzen.
22. Typisierte Konfiguration mit Secret-Dateien definieren.
23. Ungültige Produktionseinstellungen zurückweisen.
24. Strukturiertes Logging mit Datenmaskierung einrichten.
25. Request-IDs durch alle Schichten weiterreichen.
26. HTTP-Zeitlimits konfigurieren.
27. Liveness-Prüfung bereitstellen.
28. Readiness für Datenbank und Schema bereitstellen.
29. Abbrechbare Hintergrundprozesse verwalten.
30. Geordnetes Herunterfahren und Request-Draining einrichten.

## Phase 4: PostgreSQL & Migrationen

Versionierte Schemata mit sicheren Rollen und Upgrade-Prüfungen.

Dependency: Phase 3.

31. Eigentümer- und Laufzeitrollen definieren.
32. Begrenzten PostgreSQL-Verbindungspool konfigurieren.
33. Migrationsnamensraum für den Kern anlegen.
34. Eigene Migrationsnamensräume für Module festlegen.
35. Migrationen in die Distribution einbetten.
36. Automatischen Migrationsbefehl einrichten.
37. Migrationen mit einem Advisory Lock serialisieren.
38. Beschädigte oder inkompatible Schemastände erkennen.
39. Unveränderbarkeit veröffentlichter Migrationen prüfen.
40. Neuinstallation und Upgrade-Pfade verifizieren.

## Phase 5: Modul-Framework

Eine geprüfte Registry verbindet eigenständige Module.

Dependency: Phase 4.

41. Descriptor- und Capability-Verträge definieren.
42. Explizite Registry kompilierter Module aufbauen.
43. Stabile IDs und reservierte Namen prüfen.
44. Navigation, Übersetzungen und Icons prüfen.
45. Template- und Asset-Namensräume prüfen.
46. Abhängigkeiten und Zyklen prüfen.
47. Modulrouten für HTML und API montieren.
48. Unveränderliche Aktivierungssnapshots einrichten.
49. Navigation aus aktiven Deskriptoren erzeugen.
50. Framework-Verträge mit synthetischen Modulen testen.

## Phase 6: Modul-Lebenszyklus

Deaktivieren behält Daten; Löschen bleibt ein getrennter Vorgang.

Dependency: Phase 5.

51. Modulzustände und Revisionen dauerhaft speichern.
52. Aktivierung auf Administratoren beschränken.
53. Snapshots erst nach erfolgreichem Commit veröffentlichen.
54. Deaktivierte Modulrouten mit 404 beantworten.
55. Aktivierung für Dateien, Suche und Exporte prüfen.
56. Hintergrundarbeiten deaktivierter Module pausieren.
57. Schreibvorgänge gegen paralleles Abschalten absichern.
58. Datengenerationen von Modulen prüfen.
59. Geprüfte Löschanforderungen und Wartungsausführung definieren.
60. Zurückgelassene Schemata entfernter Module erkennen.

## Phase 7: Anmeldung & Benutzer

Sichere Sitzungen, persistente Limits und optionale Zwei-Faktor-Anmeldung.

Dependency: Phase 6.

61. Einmaligen Administrator-Bootstrap einrichten.
62. Bootstrap-Sitzungen bis zum Passwortwechsel beschränken.
63. Bcrypt mit expliziter Byte-Längenprüfung einsetzen.
64. Einheitliche Anmeldefehler behandeln.
65. Persistente Limits für mehrere Angriffsumfänge einrichten.
66. Sitzungshashes und sichere Cookies speichern.
67. Gleitende Ablaufzeiten mit absolutem Limit einrichten.
68. Sitzungsgebundene HMAC-CSRF-Prüfung einrichten.
69. Benutzerverwaltung vor Verlust des letzten Admins schützen.
70. Optionale TOTP-Anmeldung und Wiederherstellungscodes einrichten.

## Phase 8: HTTP-Sicherheit

Jede Anfrage überschreitet kontrollierte Sicherheitsgrenzen.

Dependency: Phase 7.

71. Vertrauenswürdige Proxy-Netze prüfen.
72. HTTPS und sichere Produktionsursprünge erzwingen.
73. CSP-Nonces pro Antwort erzeugen.
74. CSP und Browser-Sicherheitsheader setzen.
75. Origin und Fetch Metadata prüfen.
76. Eingaben begrenzt und strikt dekodieren.
77. Sichere, einheitliche Fehlerantworten definieren.
78. Berechtigungen für jeden Handler prüfen.
79. Unveränderliche Sicherheitsereignisse protokollieren.
80. Fehlerfälle von Anmeldung, CSRF und Headern verifizieren.

## Phase 9: Anhänge

Private Dateien mit Größenlimits, Vorschaubildern und kontrollierter Löschung.

Dependency: Phase 8.

81. Privaten Dateispeicher und opake IDs einrichten.
82. Upload- und Bildgrößen begrenzen.
83. Erlaubte MIME-Typen prüfen.
84. Fotos ohne Standortmetadaten neu kodieren.
85. Begrenzte Vorschaubild-Jobs einrichten.
86. Dateizugriff auf Ressourcenebene prüfen.
87. Speicherquoten für den Haushalt einrichten.
88. Unvollständige Uploads bereinigen.
89. Dateibereinigung erst nach bestätigten Metadatenänderungen ausführen.
90. Upload-Abbrüche und unerlaubte Downloads prüfen.

## Phase 10: Jobs & Erinnerungen

Dauerhafte Jobs, wiederholbare Integrationen und zustellbare Erinnerungen.

Dependency: Phase 9.

91. PostgreSQL-gestützte Job-Speicherung einrichten.
92. Worker mit ablaufenden Leases koordinieren.
93. Wiederholungen und endgültige Fehler behandeln.
94. Transaktionale Outbox einrichten.
95. Ereigniszustellung deduplizieren.
96. Erinnerungen in der Haushaltszeitzone planen.
97. Interne Benachrichtigungen zustellen.
98. Lesen und Verwerfen von Benachrichtigungen speichern.
99. Optionale datensparsame Push-Zustellung einrichten.
100. Abstürze und doppelte Zustellungen verifizieren.

## Phase 11: Erfassung & Suche

Belege werden geprüft übernommen; Suchergebnisse beachten aktive Module.

Dependency: Phase 10.

101. Versionierte Zielschema-Verträge für Erfassung definieren.
102. Bildbasierte Erfassungsjobs einrichten.
103. Manuelle Prüfmaske für Vorschläge erstellen.
104. Optionalen lokalen OCR-Adapter einrichten.
105. Optionalen lokalen KI-Adapter einrichten.
106. Bestätigung vor dem Anlegen von Datensätzen erzwingen.
107. Suchprojektionen pro Modul definieren.
108. PostgreSQL-Volltextsuche und gemessene Fehlertoleranz einrichten.
109. Suchergebnisse nach aktuellen Berechtigungen filtern.
110. Ungültige Extraktion und veraltete Suchprojektionen prüfen.

## Phase 12: Einkaufsliste

Gemeinsame Listen mit schnellen, konfliktfähigen Aktionen.

Dependency: Phase 11.

111. Einkaufslisten dauerhaft speichern.
112. Artikel mit Mengen- und Einheitenprüfung anlegen.
113. Artikel mit optimistischer Nebenläufigkeit bearbeiten.
114. Idempotentes Abhaken und Zurücksetzen einrichten.
115. Gruppierung und stabile Reihenfolge einrichten.
116. Erledigte Artikel verständlich darstellen.
117. Zeitlich begrenztes Rückgängigmachen einrichten.
118. Listen archivieren.
119. Import- und Exportverträge definieren.
120. Gleichzeitige Änderungen und wiederholte Anfragen prüfen.

## Phase 13: Tankbuch

Tankvorgänge mit belastbaren Kosten- und Verbrauchswerten.

Dependency: Phase 12.

121. Fahrzeuge und kompatible Kraftstoffeinheiten anlegen.
122. Kilometerstandsreihen für Zählerwechsel einrichten.
123. Tankvorgänge validieren.
124. Belege mit Tankvorgängen verknüpfen.
125. Gesamtpreis und Rundungsregeln definieren.
126. Verbrauch zwischen vollständigen Tankungen berechnen.
127. Teilbetankungen und fehlende Vorgänge behandeln.
128. Fahrzeugverlauf und Diagrammdaten bereitstellen.
129. Explizite Übernahme in die Ausgaben einrichten.
130. Grenzfälle und doppelte Ausgaben prüfen.

## Phase 14: Kochbuch

Rezepte skalieren und ausgewählte Zutaten in Listen übernehmen.

Dependency: Phase 13.

131. Rezeptmetadaten speichern.
132. Geordnete Zutatenabschnitte einrichten.
133. Geordnete Zubereitungsschritte einrichten.
134. Portionen skalieren.
135. Kompatible Einheiten umrechnen.
136. Rezeptfotos verarbeiten.
137. Tags und Filter einrichten.
138. Einkaufsvorschau aus Rezepten bereitstellen.
139. Ausgewählte Zutaten idempotent übertragen.
140. Bruchteile und fehlende Integrationsmodule prüfen.

## Phase 15: Vorräte

Bestände mit Chargen, Ablaufdaten und nachvollziehbaren Bewegungen.

Dependency: Phase 14.

141. Vorratsprodukte anlegen.
142. Lagerorte anlegen.
143. Chargen mit unterschiedlichen Ablaufdatumstypen speichern.
144. Unveränderliche Bestandsbewegungen einrichten.
145. Gleichzeitigen Verbrauch transaktional sperren.
146. Korrekturen als Ausgleichsbewegungen einrichten.
147. Umlagerungen zwischen Orten einrichten.
148. Mindestbestand und Ablauf-Erinnerungen erzeugen.
149. Bestätigte Einkaufsvorschläge aus Vorräten erzeugen.
150. Parallelen Verbrauch und inkompatible Einheiten prüfen.

## Phase 16: Ausgaben

Exakte Beträge, nachvollziehbare Aufteilungen und Korrekturen.

Dependency: Phase 15.

151. Ausgabenkategorien anlegen.
152. Entwürfe für Ausgaben und Erstattungen speichern.
153. Geldbeträge exakt in Untereinheiten dekodieren.
154. Kategorie-Aufteilungen einrichten.
155. Aufteilungssummen verzögert prüfen.
156. Gebuchte Datensätze vor stillen Änderungen schützen.
157. Storno- und Ersatzabläufe einrichten.
158. Monats- und Kategorieübersichten erzeugen.
159. Validierten CSV-Import und Export einrichten.
160. Rundungen und verknüpfte Tankvorgänge prüfen.

## Phase 17: Oberfläche & Design

Die beschlossene Gestaltung funktioniert auf Handy und Desktop.

Dependency: Phase 16.

161. Design-Tokens aus dem Artefakt festlegen.
162. Schriften und Lizenzen lokal einbetten.
163. Responsive Anwendungshülle aufbauen.
164. Dynamische Tab-Leiste und Mehr-Seite aufbauen.
165. Zugängliche Formular- und Dialogkomponenten aufbauen.
166. Arbeitsansichten für die fünf Module erstellen.
167. Modulverwaltung und Sicherheitsseiten erstellen.
168. Lade-, Leer-, Fehler- und Konfliktzustände gestalten.
169. Helle, dunkle und systemabhängige Darstellung einrichten.
170. Mobile und Desktop-Layouts visuell prüfen.

## Phase 18: PWA & Offline

Lokale Entwürfe werden mit aktuellen Rechten sicher übertragen.

Dependency: Phase 17.

171. Manifest und Installationsicons erstellen.
172. Versionierte Asset-Manifeste erzeugen.
173. Application-Shell im Service Worker zwischenspeichern.
174. Offline-Ersatzseite mit passender CSP bereitstellen.
175. Optionalen benutzergebundenen Gerätespeicher einrichten.
176. Versionierte Offline-Vorgänge speichern.
177. Übertragung nach erneuter Authentifizierung einrichten.
178. Konflikte und fehlgeschlagene Vorgänge sichtbar auflösen.
179. Alte Modulgenerationen ablehnen und Logout bereinigen.
180. Offline-Nutzung und unterbrochene Updates prüfen.

## Phase 19: Erweiterungsmodule

Nur ausgewählte Erweiterungen gehören zum freigegebenen Umfang.

Dependency: Phase 18.

181. Aufgabenvorlagen für das gewählte Aufgabenmodul anlegen.
182. Wiederkehrende Aufgaben deterministisch erzeugen.
183. Zuweisung und Aufgaben-Erinnerungen einrichten.
184. Logische Zähler und physische Geräte anlegen.
185. Zählerstände und Gerätewechsel validieren.
186. Verbrauchsübersichten erzeugen.
187. Dokumentmetadaten und Fristen einrichten.
188. Mahlzeitenpläne anlegen.
189. Einkaufsvorschau aus Mahlzeitenplänen einrichten.
190. Isolation und Lebenszyklus der Erweiterungen prüfen.

## Phase 20: Docker & Wiederherstellung

Reproduzierbare Container und nachgewiesene Wiederherstellung.

Dependency: Phase 19.

191. Reproduzierbares mehrstufiges Image erstellen.
192. Nicht privilegierte Laufzeit und Dateisystemrechte festlegen.
193. Isolierten PostgreSQL-Compose-Dienst einrichten.
194. Health-abhängigen Start von Migrationen und Anwendung einrichten.
195. Dauerhafte Datei- und Backup-Volumes einrichten.
196. Versionskompatible PostgreSQL-Dump-Werkzeuge bereitstellen.
197. Konsistente verschlüsselte Sicherungen erzeugen.
198. Backup-Aufbewahrung und Prüfung einrichten.
199. Isolierte Wiederherstellung automatisieren.
200. Wiederherstellungszeit gegen die Ziele messen.

## Phase 21: CI & Produktionsprüfung

Sicherheits-, Browser- und Lastprüfungen liefern Freigabeevidenz.

Dependency: Phase 20.

201. GOLint mit festgelegten Werkzeugversionen vervollständigen.
202. GOSec vervollständigen.
203. GOVul vervollständigen.
204. GODep einschließlich Modul- und Lizenzprüfungen vervollständigen.
205. Dependabot für Go und Actions einrichten.
206. Containerprüfung, SBOM und Herkunftsnachweis einrichten.
207. Nebenläufigkeit und fehlgeschlagene Migrationen prüfen.
208. Browser, Barrierefreiheit und Offline-Nutzung prüfen.
209. Repräsentative Leistung und Ressourcenbedarf messen.
210. Release-blockierende Befunde beheben oder dokumentiert entscheiden.

## Phase 22: Dokumentation & Release

Erst nach Probeläufen und ausdrücklicher Freigabe veröffentlichen.

Dependency: Phase 21.

211. Installations- und Konfigurationsdokumentation vervollständigen.
212. Modulentwicklung und Lebenszyklus dokumentieren.
213. Sicherheit und Kontowiederherstellung dokumentieren.
214. Backup-, Restore- und Upgrade-Anleitungen vervollständigen.
215. Benutzerhilfe für Erfassung und Offline-Arbeit vervollständigen.
216. Neuinstallation aus Release-Artefakten proben.
217. Upgrade mit bestehenden Haushaltsdaten proben.
218. Rollback oder Vorwärtswiederherstellung proben.
219. Versionierte Release-Artefakte und Hinweise vorbereiten.
220. Ausdrückliche Release-Freigabe einholen und freigegeben veröffentlichen.

## Release evidence

A local release candidate requires passing source checks, isolated PostgreSQL integration/race checks, browser and accessibility checks, image health checks, encrypted backup and restore verification, and a completed visual review. Infrastructure-specific HTTPS, external backup storage and public publication remain operator decisions. See [operations](operations.md), [security](security.md), [database](database.md) and [architecture](architecture.md).
