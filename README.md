# Treckrr

Treckrr ist eine mobile‑optimierte **PWA** zur **Abrechnung von Traktor‑ und
Maschinenkosten** in der bäuerlichen Nachbarschaftshilfe. Die Anwendung ersetzt
die Excel‑Datei *Noppanschoftshilfe.xlsx*: Kosten werden pro **Nachbar** und
**Jahr** erfasst, automatisch berechnet und lassen sich als CSV exportieren.

Geschrieben in **Go**, Daten in **PostgreSQL**, Auslieferung über **Docker**.

---

## Funktionsumfang

- **Kostenberechnung** nach dem Modell der Original‑Excel:
  - Traktor‑Stundensatz = `PS × Kosten €/PS·h` (Belastungsstufe *leicht/mittel/schwer*)
  - Maschinen‑Stundensatz = `Arbeitsbreite × Kosten €/AB·h`
  - Gespann‑Stundensatz = Traktor + Summe der Maschinen
  - Buchungskosten = `Stunden × Gespann‑Stundensatz`
- **Abrechnungsjahre** (Abrechnung je Kalenderjahr) – vom Nutzer selbst angelegt:
  - Jedes Jahr **wählt eine Bemessungsgrundlage** aus (siehe unten) und hat eine
    **eigene Nachbarn‑Auswahl**.
  - Schneller Wechsel zwischen Jahren über die **Jahres‑Pills** in der Übersicht.
  - **Status je Jahr**: *In Bearbeitung* oder *Abgeschlossen*.
    - Nach dem Abschließen sind **keine neuen Buchungen und keine Löschungen** mehr
      möglich (Jahr ist gesperrt); bei Bedarf lässt sich das Jahr wieder öffnen.
    - Nach dem Abschließen steht je Nachbar ein **Zahlungsstatus** zur Verfügung,
      der **standardmäßig auf *Offen*** steht und per Klick auf *Bezahlt*
      umgeschaltet wird, inkl. Summen „Bezahlt / Offen".
  - Nachbarn werden je Jahr **hinzugefügt** (bestehende auswählen oder neu anlegen)
    oder **granular vom Vorjahr übernommen** (Häkchen je Nachbar bzw. alle).
- **Zentrale Nachbarn‑Verwaltung** (Menü *Nachbarn*): global anlegen und umbenennen.
  Nachbarn **mit Buchungen können nicht gelöscht**, sondern nur **deaktiviert**
  (und später **reaktiviert**) werden – bestehende Buchungen bleiben unverändert.
- **Bemessungsgrundlagen** (erscheinen nur alle paar Jahre neu):
  - Werden von **mehreren Abrechnungsjahren gemeinsam genutzt**.
  - **Name und „gültig ab"-Jahr editierbar**; eine neue Grundlage kann die Werte
    einer bestehenden **übernehmen**; die Ausgangsgrundlage bleibt **unverändert**.
  - **Löschbar, solange keine** Abrechnungsjahr sie verwendet; sonst **sperrbar**
    (schreibgeschützt einfrieren).
  - **Kosten** und **Gespanne** werden je Grundlage in einem Arbeitsbereich mit
    **Zurück‑Button** und **Unter‑Tabs** verwaltet.
- **Globale Verwaltung** von Traktoren, Maschinen und Belastungsstufen je Grundlage;
  Traktoren und Maschinen sind **deaktivierbar/reaktivierbar** – deaktivierte werden
  bei neuen Buchungen nicht mehr angeboten, bleiben aber für bestehende Buchungen
  erhalten (nachvollziehbar, keine Datenverluste).
- **Stammdaten‑Komfort**: **Reihenfolge** (Sortierung) für Traktoren, Maschinen und
  Gespanne; **Kategorien/Tags** für Maschinen mit Filter; **Gespann‑Kostenaufschlüsselung**
  (Traktor + je Maschine + Summe); **Grundlagen‑Vergleich** zeigt die Auswirkung
  (Diff/%) einer Grundlage gegenüber einer anderen.
- **Fixe Gespanne** (z. B. *Mähen = 4095 + Heckmähwerk + Frontmähwerk + mittel*)
  **oder** freie, manuelle Kombination bei der Buchung – mit Live‑Vorschau des
  Stundensatzes.
- **Buchungen**: einzeln anlegen, **bearbeiten**, **Schnellerfassung** mehrerer Zeilen,
  **stornieren** (bleibt sichtbar, zählt nicht mehr; wieder aktivierbar) oder löschen;
  clientseitige **Pflichtfeld‑Validierung**. In abgeschlossenen Jahren gesperrt.
- **Nachbar‑Ansicht** wie in der Excel: übersichtliche **Gesamtübersicht** aller
  Buchungen (Datum, Tätigkeit inkl. Gespann‑Detail, Stunden, Kosten) mit Summenzeile
  plus **Zusammenfassung nach Tätigkeit**; **Jahres‑Verlauf** je Nachbar mit
  **Zahlungshistorie**; **CSV‑Export** (Excel‑kompatibel) je Jahr und Nachbar.
- **Statistik** (`/stats`): Kennzahlen (Umsatz, Stunden, bezahlt/offen), **Balken‑Diagramme**
  je Nachbar/Tätigkeit/Traktor (lokal gerendert, kein JS‑Framework) und **Jahresvergleich**.
- **Rollen & Rechte**: *Administrator*, *Erfasser*, *Nur‑Lesen*. **Passwort‑Richtlinie**
  (min. 8 Zeichen, Buchstaben + Ziffern), erzwungene Passwortänderung, **Zwei‑Faktor (TOTP)**,
  **Sitzungsverwaltung** (aktive Sitzungen anzeigen/beenden) und **Login‑Rate‑Limit**
  gegen Brute‑Force. **Admin** wird per Docker‑ENV festgelegt.
- **Dark Mode** (Hell / Dunkel / Automatisch, im Profil umschaltbar) und **Live‑Suche**
  in Nachbarn‑Listen.
- **Logging & Audit‑Trail**: Jeder Zugriff wird als Zeile in den Server‑Logs
  protokolliert (`docker compose logs app`: Methode, Pfad, Status, Dauer, Benutzer, IP);
  daten‑ und sicherheitsrelevante Aktionen landen zusätzlich im **Protokoll**
  (Admin → *Protokoll*, `/admin/audit`) mit **Suche, Aktions‑Filter und CSV‑Export**.
- **Automatische DB‑Backups** (optionaler Compose‑Dienst): `docker compose --profile backup up -d`
  legt täglich Dumps in `./backups` ab (Aufbewahrung konfigurierbar). Manuell:
  `sh scripts/backup.sh`, Wiederherstellung: `sh scripts/restore.sh <dump>`.
- **PWA**: installierbar, Offline‑Fallback, lokale Assets (kein CDN), moderne CSS‑UI
  mit **modalen Dialogen** (natives `<dialog>`) für Bestätigungen statt Browser‑Popups.
  Statische Assets sind **content‑gehasht versioniert** (`?v=…`) und der Service‑Worker
  aktualisiert seinen Cache automatisch bei neuen Builds.

---

## Schnellstart (Docker)

Voraussetzung: Docker mit Compose.

```bash
# 1. Konfiguration vorbereiten
cp .env.example .env
#    In .env mindestens setzen:
#    SESSION_SECRET, ADMIN_PASSWORD, POSTGRES_PASSWORD, DATABASE_URL

# 2. Starten (baut App-Image und startet PostgreSQL als eigenständigen Container)
docker compose up -d --build

# 3. Öffnen
#    http://localhost:8080  (HOST_PORT aus .env)
```

Beim ersten Start werden Schema‑Migrationen ausgeführt, der Admin‑Benutzer aus
den ENV‑Variablen angelegt sowie eine Beispiel‑**Bemessungsgrundlage 2023**
(Werte aus der Excel, inkl. Gespanne) und ein **Abrechnungsjahr 2025** mit den
drei Beispiel‑Nachbarn erzeugt. Weitere Jahre legst du unter **Jahre** an.

### Wichtige Umgebungsvariablen

| Variable | Bedeutung |
|---|---|
| `ADMIN_USERNAME` / `ADMIN_PASSWORD` | Bootstrap‑Admin (bei jedem Start abgeglichen) |
| `SESSION_SECRET` | Zufallswert, min. 16 Zeichen (`openssl rand -hex 32`) |
| `COOKIE_SECURE` | `true` hinter HTTPS setzen (oder `TRUST_PROXY` nutzen) |
| `TRUST_PROXY` | `true` hinter einem vertrauenswürdigen Reverse Proxy |
| `DATABASE_URL` | Postgres‑Verbindung (Standard zeigt auf den `db`‑Container) |
| `POSTGRES_USER/PASSWORD/DB` | Zugangsdaten des Datenbank‑Containers |
| `APP_PORT` / `HOST_PORT` | Container‑ bzw. Host‑Port |
| `BACKUP_INTERVAL` / `BACKUP_KEEP` | Intervall/Anzahl der automatischen Backups |

> Der Admin‑Benutzer wird bei **jedem** Start mit dem Passwort aus der ENV
> abgeglichen – so ist der Zugang immer über die Docker‑Konfiguration wiederherstellbar.

### Betrieb hinter einem Reverse Proxy (z. B. Nginx Proxy Manager)

Die App spricht **einfaches HTTP auf Port 8080** – TLS übernimmt der Proxy.

1. In `.env` setzen: `TRUST_PROXY=true` (damit echte Client‑IPs für Audit/Rate‑Limit
   verwendet werden und Cookies bei HTTPS automatisch das `Secure`‑Flag erhalten).
   Nur aktivieren, wenn die App **ausschließlich** über den Proxy erreichbar ist.
2. In **Nginx Proxy Manager** einen *Proxy Host* anlegen:
   - *Forward Hostname/IP*: `treckrr-app` (bzw. Host‑IP), *Forward Port*: `8080`
   - *Websockets Support*: nicht erforderlich
   - Reiter **SSL**: Zertifikat wählen, *Force SSL* aktivieren
3. Der Proxy sollte `X-Forwarded-For` und `X-Forwarded-Proto` setzen (NPM tut dies
   standardmäßig). Die App wird unter dem **Domain‑Root** erwartet (kein Unterpfad).
4. Optional den Host‑Port nicht öffentlich mappen – nur der Proxy braucht Zugriff
   (im selben Docker‑Netzwerk genügt der Servicename `treckrr-app:8080`).

---

## Architektur

```
cmd/treckrr            Programmeinstieg (HTTP-Server, Graceful Shutdown)
internal/config        Konfiguration aus ENV
internal/db            Verbindungspool + eingebettete SQL-Migrationen
internal/models        Domänentypen
internal/calc          Kostenmodell (mit Tests gegen die Excel-Werte)
internal/auth          Passwort-Hashing (bcrypt) + Session-Token
internal/store         Datenbankzugriff (Users, Grundlagen, Gespanne, Buchungen)
internal/server        HTTP-Routing, Middleware, Handler
internal/web           Eingebettete HTML-Templates & lokale Assets (CSS/JS/Icons)
```

Nur zwei externe Go‑Abhängigkeiten (PostgreSQL‑Treiber `pgx`, `x/crypto/bcrypt`);
alle CSS/JS/Icons werden **lokal** ausgeliefert.

---

## Entwicklung

Ohne Docker (lokales Go ≥ 1.23 und eine erreichbare PostgreSQL‑Instanz):

```bash
export DATABASE_URL="postgres://treckrr:treckrr@localhost:5432/treckrr?sslmode=disable"
export SESSION_SECRET="dev-secret-please-change-01"
export ADMIN_USERNAME=admin
export ADMIN_PASSWORD=admin123
go mod tidy
go run ./cmd/treckrr
```

Tests & Prüfungen:

```bash
go test ./...
go vet ./...
```

---

## CI / Sicherheit

GitHub‑Workflows unter `.github/workflows/`:

- **CI** – `go vet`, Tests (mit Race‑Detector), Build und **GOLint**
  (golangci‑lint).
- **Security** – **GOSec** (statische Sicherheitsanalyse) und **GOVul**
  (`govulncheck`).
- **GODep** – Dependency‑Review auf Pull Requests.

**Dependabot** (`.github/dependabot.yml`) hält Go‑Module, GitHub‑Actions und die
Docker‑Basis‑Images aktuell.

---

## Datenmodell (kurz)

- `price_bases` – Bemessungsgrundlage (sperrbar); `year` = „gültig ab".
- `load_levels`, `tractors`, `machines` – Preisdaten je Grundlage.
- `gespanne` (+ `gespann_machines`) – fixe Kombinationen je Grundlage.
- `billing_years` – Abrechnungsjahr; verweist auf **eine** `price_bases`.
- `billing_year_neighbors` – welche Nachbarn in einem Jahr teilnehmen.
- `neighbors` – global (über Jahre hinweg wiederverwendbar).
- `entries` (+ `entry_machines`) – Buchungen je `billing_years` mit
  **eingefrorenen** Preis‑Snapshots, damit Exporte und Historie stabil bleiben.

---

## Lizenz

Es werden ausschließlich freie, lizenzkostenfreie Werkzeuge und Bibliotheken
verwendet. Projektcode: siehe [LICENSE](LICENSE) (MIT).
