# Installationsanleitung

English: [Install guide](install.md)

Hejmo läuft als Docker-Container auf einer kleinen Box bei euch zu Hause. Auf Raspberry Pi OS installiert ein Befehl alles; auf jedem anderen Linux nutzt du Docker Compose direkt.

> **Stand dieser Anleitung (Vorabversion 0.x).**
> - **Raspberry Pi: in der Emulation getestet.** Gemessener Speicher, Startzeit und die Grenzen, die der Build erzwingt, stehen unter [Hardware](install/hardware.de.md). Das Installationsskript und die Box laufen in CI in einem Raspberry-Pi-OS-64-Bit-Ersatzsystem. Auf echter Hardware hat es noch niemand bestätigt.
> - **E-Ink-Funktionen sind experimentell und noch nicht auf E-Ink-Geräten getestet.** Schwarz-Weiß-Darstellung auf echtem E-Ink, das Geisterbild-Neuladen und das Frontlicht im Ruhemodus sind nur gegen einen simulierten Kiosk-Adapter gebaut und getestet. Alle Funktionen laufen auch auf einem normalen Bildschirm wie einem iPad.

## Voraussetzungen

- Ein Raspberry Pi 4 oder 5 (oder neuer) mit **mindestens 1 GB RAM** und **Raspberry Pi OS (64-Bit)**, per LAN oder WLAN im Heimnetz. 32-Bit-Raspberry-Pi-OS wird nicht unterstützt. Jeder 64-Bit-Rechner mit Debian oder Ubuntu (x86_64 oder arm64) funktioniert genauso.
- Eine **feste Adresse** für die Box: Reserviere ihr im Router eine Adresse, damit sie sich nicht ändert.
- Internetzugang während der Installation (Docker und das Hejmo-Image werden geladen).
- Ein Computer oder Handy im selben Heimnetz für den Einrichtungsassistenten.
- Die Ports **80** (HTTP, leitet auf HTTPS um) und **443** (HTTPS) müssen auf der Box frei sein. Du kannst sie später ändern (siehe unten).

## Installation

Melde dich an der Box an (Tastatur und Bildschirm oder `ssh`) und führe aus:

```bash
curl -fsSL https://raw.githubusercontent.com/hejmo-home/hejmo/main/install.sh | sudo bash
```

Das Skript:

- installiert Docker aus Dockers eigenem apt-Repository, falls es fehlt,
- schreibt die Compose-Datei der Box, eine Einstellungsdatei und eine systemd-Unit nach `/opt/hejmo` (die Unit `hejmo.service` startet die Box bei jedem Systemstart),
- legt die Daten in `/opt/hejmo/data` ab,
- startet die Box, wartet bis sie antwortet und gibt die Adresse des Einrichtungsassistenten aus.

Du kannst das Skript jederzeit erneut ausführen; es ändert nur, was abweicht. Deine Einstellungsdatei `/opt/hejmo/.env` und deine Daten werden nie überschrieben.

Eine bestimmte veröffentlichte Version statt der neuesten installierst du, indem du sie beim ersten Lauf setzt: `curl -fsSL ... | sudo HEJMO_VERSION=0.1.0 bash`. Ohne das Skript kopierst du `compose.yaml` aus dem Repository in einen Ordner, führst dort `docker compose up -d` aus und öffnest die Box im Browser.

Weitere Einstellungen stehen in `/opt/hejmo/.env`:

| Schlüssel | Bedeutung | Standard |
|---|---|---|
| `HEJMO_IMAGE` | Das ausgeführte Image | `ghcr.io/hejmo-home/hejmo:latest` |
| `HEJMO_HTTP_PORT` | HTTP-Port | `80` |
| `HEJMO_HTTPS_PORT` | HTTPS-Port | `443` |

Nach dem Bearbeiten: `cd /opt/hejmo && sudo docker compose up -d`.

## Erste Schritte

1. Öffne die vom Skript ausgegebene Adresse (zum Beispiel `http://192.168.1.50`) in einem Browser im Heimnetz. Der **Einrichtungsassistent** startet.
2. **Zuerst HTTPS** (siehe unten). Ein Passwort fragt der Assistent erst, wenn HTTPS funktioniert.
3. Gib deinen Wohnort (Ort und Bundesland oder Kanton) für die Feiertage ein.
4. Lege das erste Elternkonto mit Passwort und Einmalcodes an (QR-Code mit einer Authenticator-App wie Aegis oder FreeOTP scannen) und wähle die Eltern-PIN für Displays.
5. Füge Personen hinzu, dann Kalender, Listen und Aufgaben.

## HTTPS

Die Box liefert alles über HTTPS aus. Der Assistent bietet zwei Wege:

- **Ein öffentlicher Name (empfohlen).** Der Assistent richtet einen kostenlosen Namen wie `familie.dedyn.io` bei deSEC ein (oder einen Namen deiner eigenen Domain bei deSEC), der auf die Adresse der Box zu Hause zeigt, und holt ein Zertifikat, dem jedes Handy und jeder Browser vertraut. Nur der Name und einige Prüfungen verlassen dein Zuhause; deine Daten bleiben auf der Box. Blockiert dein Router den Namen (DNS-Rebind-Schutz der FRITZ!Box), zeigt der Assistent, wie du ihn erlaubst.
- **Die eigene Zertifizierungsstelle (CA) der Box.** Braucht keinen externen Dienst. Du vertraust der Box einmal pro Gerät (nächster Abschnitt).

Sichere `/opt/hejmo/data` samt `secret.key` (sie schützt gespeicherte Passwörter und das DNS-Token).

## Der eigenen CA vertrauen

Nur nötig, wenn du die eigene CA gewählt hast. Öffne auf jedem Gerät `http://<Adresse der Box>/ca`:

- **iPhone und iPad:** Profil laden, in den Einstellungen installieren und dann unter Einstellungen, Allgemein, Info, Zertifikatsvertrauenseinstellungen aktivieren.
- **Android:** Zertifikat laden und in den Sicherheitseinstellungen als CA-Zertifikat installieren. Chrome vertraut ihm als Nutzerzertifikat.
- **Computer:** Zertifikat in den System- oder Browser-Zertifikatspeicher importieren.

Die CA ist auf die Box namensbeschränkt und kann daher nicht für andere Websites bürgen. Die Seite zeigt den Fingerabdruck der CA; die Box schreibt ihn auch ins Log (`docker logs hejmo`).

## Displays und Handys

- **Displays:** Öffne im Browser des Displays die Display-Adresse der Box. Es zeigt einen QR-Code und einen sechsstelligen Code. Öffne in der Weboberfläche **Displays**, gib den Code ein und vergib Name, Raum und Typ. Ein E-Ink-Display (schwarz-weiß oder farbig) ist **experimentell**; ein normaler Bildschirm, etwa ein altes Tablet, funktioniert vollständig.
- **Handys:** Öffne die Adresse der Box im Browser des Handys und melde dich an. Die Kopplung per QR-Code und die installierbare Handy-Web-App kommen mit den Handy-Bildschirmen der Weboberfläche. Kalender-Apps (iOS-Kalender, DAVx5) können die Box per CalDAV mit einem Geräte-Token von der Seite **CalDAV** abonnieren.
- Nutzt du die eigene CA, vertraue ihr auf dem Handy zuerst.

## Automatische Updates

Das Installationsskript richtet auch eine nächtliche Update-Prüfung ein (ein systemd-Timer, `hejmo-update.timer`). Sie gilt für die Einrichtung per Installationsskript; ein reines Docker-Compose-Setup aktualisierst du selbst.

- **Jede Nacht um 03:30 Uhr** sucht der Updater die neueste veröffentlichte Version (Release-Tags ohne Zusatz wie `-rc.1`; Vorschau-Builds werden nie installiert).
- Er installiert ein Image nur, wenn dessen Signatur aus dem Release-Workflow des Projekts stimmt (cosign, schlüssellos über GitHub OIDC). Ein unsigniertes oder falsch signiertes Image wird nicht installiert.
- Vor dem Wechsel sichert er die Datenbank mit der Online-Sicherung von SQLite, startet die Box mit dem neuen Image neu und wartet auf `/healthz`. Startet die neue Version nicht oder antwortet sie nicht, kehrt er zum vorherigen Image (per Digest gemerkt) zurück und spielt die Sicherung ein. Eltern sehen dann einen Hinweis mit dem Grund; eine fehlgeschlagene Version wird nicht erneut versucht, bis eine neuere erscheint.
- Nach einem Update sehen Eltern einmalig, was neu ist, mit Link zu den Release Notes.
- **Einstellung** (Weboberfläche, **Einstellungen**): *an* (Standard), *aus* oder *nur benachrichtigen* (nichts wird installiert; Eltern sehen, dass ein Update bereitsteht). Die Einstellung erscheint nur, wo das Installationsskript den Updater eingerichtet hat. Willst du bei einer mit `HEJMO_VERSION` festgelegten Version bleiben, stelle die automatischen Updates auf *aus*; sonst hebt der Updater die Box auf die neueste veröffentlichte Version.
- Eine Box, die ihre Version nicht kennt (etwa ein Vorschau-Build), wird nie aktualisiert.
- Der Container bekommt keinen Zugriff auf den Docker-Socket. Box und Updater tauschen nur zwei kleine Dateien im Datenvolumen aus (`update-settings`, `update-status`) mit festen Schlüsseln und Werten.
- Das Updater-Skript selbst ändert sich nur, wenn du das Installationsskript erneut ausführst. Den letzten Lauf siehst du mit `sudo journalctl -u hejmo-update`.
- Schlägt auch die Rückkehr fehl (die Sicherung lässt sich nicht einspielen), bleibt die Box absichtlich **gestoppt**, und Eltern sehen keinen Hinweis, weil die Box aus ist. Antwortet die Box nach einer Update-Nacht nicht, zeigt `sudo journalctl -u hejmo-update`, was passiert ist.

## Aktualisieren und entfernen

- Ein erneuter Lauf des Installationsskripts behält Einstellungen und Daten und erneuert Updater und systemd-Units; die Box selbst aktualisiert er nicht.
- Zum Entfernen: `sudo systemctl disable --now hejmo.service hejmo-update.timer && sudo rm /etc/systemd/system/hejmo.service /etc/systemd/system/hejmo-update.service /etc/systemd/system/hejmo-update.timer && cd /opt/hejmo && sudo docker compose down`. Deine Daten bleiben in `/opt/hejmo/data`, bis du den Ordner löschst.
