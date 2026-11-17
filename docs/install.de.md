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

Nur nötig, wenn du die eigene CA gewählt hast. Öffne auf jedem Gerät `http://<Adresse der Box>/ca`. Die Seite erkennt das Gerät (iPhone oder iPad, Android, Mac, Windows, Linux), zeigt zuerst dessen vier Schritte und prüft selbst, wann das Gerät der Box vertraut. Den Fingerabdruck des Zertifikats kannst du dort auch vergleichen (freiwillig). Kurz gesagt:

- **iPhone und iPad:** Profil laden, in den Einstellungen installieren und dann unter Einstellungen, Allgemein, Info, Zertifikatsvertrauenseinstellungen aktivieren.
- **Android:** Zertifikat laden und in den Sicherheitseinstellungen als CA-Zertifikat installieren. Chrome vertraut ihm als Nutzerzertifikat.
- **Computer:** Zertifikat in den System- oder Browser-Zertifikatspeicher importieren.

Die CA ist auf die Box namensbeschränkt und kann daher nicht für andere Websites bürgen. Die Seite zeigt den Fingerabdruck der CA; die Box schreibt ihn auch ins Log (`docker logs hejmo`).

## Von unterwegs: WireGuard mit der FRITZ!Box

Die Box steht in deinem Heimnetz, ein Handy unterwegs erreicht sie deshalb nicht von selbst. Ein VPN holt das Handy zurück ins Heimnetz. Mit einer FRITZ!Box brauchst du dafür keinen weiteren Dienst, denn FRITZ!OS 7.50 und neuer hat WireGuard eingebaut. Nichts davon läuft über Hejmo: Das VPN verbindet dein Handy nur mit deinem Router.

Du brauchst:

- Eine FRITZ!Box mit FRITZ!OS 7.50 oder neuer, die aus dem Internet erreichbar ist.
- Die WireGuard-App auf dem Handy: [App Store](https://apps.apple.com/app/wireguard/id1441195209) (iPhone, iPad), [Google Play](https://play.google.com/store/apps/details?id=com.wireguard.android) (Android), andere Systeme unter [wireguard.com/install](https://www.wireguard.com/install/).

**Geteilte Adresse (DS-Lite, CGNAT)?** Viele Anbieter teilen eine IPv4-Adresse unter mehreren Kunden, dann erreicht von außen über IPv4 nichts deine FRITZ!Box. Du erkennst es in der FRITZ!Box unter **Internet**, **Online-Monitor**: Dort steht ein DS-Lite-Tunnel, oder die IPv4-Adresse beginnt im Bereich 100.64.0.0/10 (100.64.x.x bis 100.127.x.x). Das VPN klappt oft trotzdem über IPv6, wenn das Mobilfunknetz des Handys es anbietet; dann musst du nichts weiter tun. Andernfalls frage deinen Anbieter nach einer öffentlichen IPv4-Adresse.

So richtest du es ein (etwa 10 Minuten, einmal pro Handy, zu Hause):

1. Öffne die FRITZ!Box unter [http://fritz.box](http://fritz.box) und gehe zu **Internet**, **Freigaben**, **VPN (WireGuard)**. Füge eine Verbindung hinzu und wähle, ein einzelnes Gerät mit dem Heimnetz zu verbinden. Gib ihr einen Namen wie „Handy von Mama“. Die FRITZ!Box bietet eine kostenlose MyFRITZ-Adresse an, damit das Handy sie findet, wenn sich deine Adresse zu Hause ändert; nimm sie an.
2. Die FRITZ!Box zeigt einen QR-Code. Füge in der WireGuard-App auf dem Handy einen Tunnel hinzu, indem du ihn scannst.
3. Schalte den Tunnel ein, unterwegs oder mit mobilen Daten, und öffne die Box unter ihrer Adresse im Heimnetz (die Seite **Einstellungen**, **Sichere Verbindung** zeigt sie).

Öffne die Box mit ihrer Adresse oder dem Namen, den der Router ihr gibt, nicht als `hejmo.local`: Über ein VPN findet ein Handy `hejmo.local` meist nicht. In der FRITZ!Box steht der Name der Box unter **Heimnetz**, **Netzwerk**, beim Gerät der Box (zum Beispiel `hejmo.fritz.box`; dort kannst du ihn auch ändern). Trage diesen Namen einmal im Einrichtungsassistenten unter **Mehr Optionen** ein, oder später unter **Einstellungen**, **Sichere Verbindung**, **Name im Router**. Ein später ergänzter Name braucht ein neues Zertifikat der Box, und jedes Gerät muss der Box noch einmal vertrauen (voriges Kapitel).

Andere VPNs funktionieren genauso, solange sie das Handy in dein Heimnetz holen. Diese Anleitung behandelt nur WireGuard in der FRITZ!Box.

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

## Offline-Modus

Mit dem **Offline-Modus** (der Einrichtungsassistent fragt gleich nach „Nur im Heimnetz“; später **Einstellungen › Verbindungen nach außen**) verbindet sich die Box mit nichts außerhalb deines Heimnetzes: kein Wetter, kein iCloud, Google oder andere Kalender-Server, keine abonnierten Kalender, keine Bilder aus dem Web, kein DNS-Dienst. Kalender, Aufgaben, Listen, Essen, Displays und Handys im Heimnetz funktionieren weiter. Die Seite zeigt jede Verbindung mit ihrem letzten Kontakt und, solange der Offline-Modus an ist, jeden Versuch, den die Box abgewiesen hat. Mit einem öffentlichen Namen braucht der Offline-Modus zuerst den Wechsel zu „Nur im Heimnetz“; die Seite zeigt, was das kostet.

## Update ohne Internet

Mit Offline-Modus (**Einstellungen › Verbindungen nach außen**) aktualisiert sich die Box nicht selbst, und der nächtliche Updater auch nicht: Die Box schreibt `offline=yes` in die Datei `update-settings`, der Updater verbindet sich mit nichts. Neue Versionen bringst du selbst auf die Box. Nach 90 Tagen ohne Update (die Zahl kannst du auf dieser Seite ändern) sehen Eltern „Seit 90 Tagen nicht aktualisiert“ mit einem Link zu diesem Kapitel.

Eine Update-Datei in der Weboberfläche hochzuladen gibt es noch nicht (das ist ein eigener Schritt). Bis dahin geht es von Hand, von einem Computer mit Internet aus und mit der Adresse der Box zur Hand:

1. **Auf einem Computer mit Internet** suchst du die neueste Version auf der [Release-Seite](https://github.com/hejmo-home/hejmo/releases) (eine Nummer wie `0.2.4`, keine Vorabversion). Lade das Image und prüfe seine Signatur, genau wie der Updater:
   ```bash
   docker pull ghcr.io/hejmo-home/hejmo:0.2.4
   docker run --rm ghcr.io/sigstore/cosign/cosign:v2.5.3 verify ghcr.io/hejmo-home/hejmo:0.2.4 \
     --certificate-identity https://github.com/hejmo-home/hejmo/.github/workflows/release.yml@refs/tags/v0.2.4 \
     --certificate-oidc-issuer https://token.actions.githubusercontent.com
   ```
   Mach nur weiter, wenn die Signatur angezeigt wird. Ein Image ohne oder mit falscher Signatur darf nicht installiert werden.
2. **Speichere das Image in eine Datei** und kopiere sie auf einen USB-Stick: `docker save ghcr.io/hejmo-home/hejmo:0.2.4 -o hejmo-0.2.4.tar`.
3. **Auf der Box** bindest du den Stick ein, sicherst die Daten (`sudo cp -a /opt/hejmo/data /opt/hejmo/data.vor-0.2.4`) und lädst das Image: `sudo docker load -i /media/usb/hejmo-0.2.4.tar`.
4. **Stelle die Box darauf um:** Trage in `/opt/hejmo/.env` `HEJMO_IMAGE=ghcr.io/hejmo-home/hejmo:0.2.4` ein und führe `cd /opt/hejmo && sudo docker compose up -d` aus. Öffne die Box und prüfe, dass alles läuft; die Seite „Verbindungen nach außen“ zeigt weiter, dass nichts nach draußen ging.
5. Startet die neue Version nicht, setze `HEJMO_IMAGE` auf das alte Tag zurück, stoppe die Box und spiele die Datensicherung zurück.

## Uhrzeit vom Router

Der Offline-Modus von Hejmo gilt nur für Hejmo. Das Gerät, auf dem die Box läuft (zum Beispiel Raspberry Pi OS), kann weiter die Uhrzeit aus dem Internet holen (NTP) und eigene System-Updates laden. Damit es die Uhrzeit ohne Internet bekommt, richte es auf deinen Router aus, den viele Router (etwa die FRITZ!Box) als Zeitserver anbieten:

1. In der FRITZ!Box öffnest du **Heimnetz › Netzwerk › Netzwerkeinstellungen** und notierst die Adresse der FRITZ!Box (meist `192.168.178.1`). Unter **Heimnetz-Einstellungen** muss *Zeitserver (NTP)* für das Heimnetz an sein.
2. Auf der Box bearbeitest du `/etc/systemd/timesyncd.conf`, setzt `NTP=192.168.178.1` und `FallbackNTP=` (leer) und startest mit `sudo systemctl restart systemd-timesyncd` neu.
3. Mit `timedatectl timesync-status` prüfst du, dass der Router der Zeitserver ist.

Die Menünamen unterscheiden sich je nach FRITZ!OS-Version; die Hilfe des Routers zeigt, wo die Einstellung für den Zeitserver liegt. Eine Box mit falscher Uhr kann Zertifikate nicht prüfen und niemanden anmelden, prüfe deshalb die Uhrzeit nach dem ersten Start.

## Aktualisieren und entfernen

- Ein erneuter Lauf des Installationsskripts behält Einstellungen und Daten und erneuert Updater und systemd-Units; die Box selbst aktualisiert er nicht.
- Zum Entfernen: `sudo systemctl disable --now hejmo.service hejmo-update.timer && sudo rm /etc/systemd/system/hejmo.service /etc/systemd/system/hejmo-update.service /etc/systemd/system/hejmo-update.timer && cd /opt/hejmo && sudo docker compose down`. Deine Daten bleiben in `/opt/hejmo/data`, bis du den Ordner löschst.
