# Install guide

Deutsch: [Installationsanleitung](install.de.md)

Hejmo runs as a Docker container on a small box in your home. One command installs everything on Raspberry Pi OS; on any other Linux you use Docker Compose directly.

> **Status of this guide (pre-release 0.x).**
> - **Raspberry Pi: tested in emulation.** Measured memory, start time and the limits the build enforces are in [Hardware](install/hardware.md). The install script and the box run in CI on a Raspberry Pi OS 64-bit stand-in. Nobody has confirmed it on real hardware yet.
> - **E-Ink functions are experimental, not yet tested on E-Ink devices.** Black-and-white rendering on real E-Ink, the ghosting reload and the front light in quiet mode are built and tested against a fake kiosk adapter only. Every function also works on a normal screen such as an iPad.

## Requirements

- A Raspberry Pi 4 or 5 (or newer) with **1 GB RAM or more**, running **Raspberry Pi OS (64-bit)**, with Ethernet or Wi-Fi at home. 32-bit Raspberry Pi OS is not supported. Any 64-bit Debian or Ubuntu machine (x86_64 or arm64) works the same way.
- A **fixed address** for the box: reserve one for it in your router so that it does not change.
- Internet access during installation (Docker and the Hejmo image are downloaded).
- A computer or personal device on the same home network for the setup wizard.
- The ports **80** (HTTP, redirects to HTTPS) and **443** (HTTPS) free on the box. You can change them later (see below).

## Install

Log in to the box (a keyboard and screen, or `ssh`) and run:

```bash
curl -fsSL https://raw.githubusercontent.com/hejmo-home/hejmo/main/install.sh | sudo bash
```

The script:

- installs Docker from Docker's own apt repository if it is missing,
- writes the box's Compose file, a settings file and a systemd unit to `/opt/hejmo` (the unit `hejmo.service` starts the box at every boot),
- keeps the data in `/opt/hejmo/data`,
- starts the box and waits until it answers, then prints the address of the setup wizard.

You can run the script again at any time; it changes only what differs. Your settings file `/opt/hejmo/.env` and your data are never overwritten.

To install a particular published version instead of the newest one, set it for the first run: `curl -fsSL ... | sudo HEJMO_VERSION=0.1.0 bash`. Without the script, copy `compose.yaml` from the repository to a folder, run `docker compose up -d` there, and open the box in a browser.

Other settings live in `/opt/hejmo/.env`:

| Key | Meaning | Default |
|---|---|---|
| `HEJMO_IMAGE` | Image to run | `ghcr.io/hejmo-home/hejmo:latest` |
| `HEJMO_HTTP_PORT` | HTTP port | `80` |
| `HEJMO_HTTPS_PORT` | HTTPS port | `443` |

After editing it, run `cd /opt/hejmo && sudo docker compose up -d`.

## First steps

1. Open the address the script printed (for example `http://192.168.1.50`) in a browser on your home network. The **setup wizard** starts.
2. **HTTPS first** (see below). The wizard asks for a password only after HTTPS works.
3. Enter your home location (town and state or canton) for the public holidays.
4. Create the first parent account with a password and one-time codes (scan the QR code with an authenticator app such as Aegis or FreeOTP) and choose the parent PIN for displays.
5. Add people, then calendars, lists and tasks.

## HTTPS

The box serves everything over HTTPS. The wizard offers two ways:

- **A public name (recommended).** The wizard sets up a free name such as `familie.dedyn.io` at deSEC (or a name of your own domain at deSEC) that points to the box's address at home, and obtains a certificate that every personal device and browser trusts. Only the name and a few checks leave your home; your data stays on the box. If your router blocks the name (FRITZ!Box DNS rebind protection), the wizard shows how to allow it.
- **The box's own certificate authority (CA).** Needs no outside service. You trust the box once on each device (next section).

Back up `/opt/hejmo/data` including `secret.key` (it protects stored passwords and the DNS token).

## Trust the box's own CA

Only needed if you chose the own CA. Open `http://<box address>/ca` on each device. The page recognises the device (iPhone or iPad, Android, Mac, Windows, Linux), shows its four steps first and checks by itself when the device trusts the box. You can compare the certificate's fingerprint there too (optional). In short:

- **iPhone and iPad:** download the profile, install it in Settings, then switch it on under Settings, General, About, Certificate Trust Settings.
- **Android:** download the certificate and install it as a CA certificate in the security settings. Chrome trusts it as a user certificate.
- **Computers:** import the certificate into the system or browser trust store.

The CA is name-constrained to the box, so it cannot vouch for other websites. The page shows the CA's fingerprint; the box also writes it to its log (`docker logs hejmo`).

## Away from home: WireGuard with the FRITZ!Box

The box lives in your home network, so a personal device away from home does not reach it by itself. A VPN puts the device back into the home network; with a FRITZ!Box you need no extra service for that, because FRITZ!OS 7.50 and newer has WireGuard built in. Nothing of this goes through Hejmo: the VPN connects your phone to your router only.

You need:

- A FRITZ!Box with FRITZ!OS 7.50 or newer that can be reached from the internet.
- The WireGuard app on the device: [App Store](https://apps.apple.com/app/wireguard/id1441195209) (iPhone, iPad), [Google Play](https://play.google.com/store/apps/details?id=com.wireguard.android) (Android), other systems at [wireguard.com/install](https://www.wireguard.com/install/).

**Shared address (DS-Lite, CGNAT)?** Many providers share one IPv4 address between customers, and then nothing reaches your FRITZ!Box from outside over IPv4. You recognise it in the FRITZ!Box under **Internet**, **Online Monitor**: it shows a DS-Lite tunnel, or the IPv4 address starts in the range 100.64.0.0/10 (100.64.x.x to 100.127.x.x). The VPN often still works over IPv6 when the mobile network of the phone offers it; then you need to do nothing more. Otherwise ask your provider for a public IPv4 address.

Set it up (about 10 minutes, once per device, at home):

1. Open the FRITZ!Box at [http://fritz.box](http://fritz.box) and go to **Internet**, **Permit Access**, **VPN (WireGuard)**. Add a connection and choose to connect a single device to the home network. Give it a name such as "Mama's device". The FRITZ!Box offers a free MyFRITZ address so the phone finds it when your home address changes; accept it.
2. The FRITZ!Box shows a QR code. In the WireGuard app on the device add a tunnel by scanning it.
3. Switch the tunnel on, away from home or on mobile data, and open the box under its address in the home network (the page **Settings**, **Secure connection** shows it).

Open the box by its address or by the name the router gives it, not as `hejmo.local`: through a VPN a device usually cannot find `hejmo.local`. In the FRITZ!Box the name of the box is under **Home Network**, **Network**, at the box's device (for example `hejmo.fritz.box`; you can rename it there). Enter that name once in the setup wizard under **More options**, or later under **Settings**, **Secure connection**, **Name in the router**. A name added later needs a new certificate of the box, and every device has to trust the box again (previous chapter).

Other VPNs work the same way as long as they put the device into your home network. This guide covers WireGuard in the FRITZ!Box only.

## Displays and personal devices

- **Displays:** open the box's display address on the display's browser. It shows a QR code and a six-digit code. In the web interface, open **Displays**, enter the code and give the display a name, room and type. An E-Ink display (black and white or colour) is **experimental**; a normal screen, such as an old tablet, works fully.
- **Personal devices (phone, tablet):** open the box's address in the device's browser and sign in. A parent pairs it by QR code under **Settings › Personal devices**; it then runs the web interface as an app. Calendar apps (iOS Calendar, DAVx5) can subscribe to the box over CalDAV with a device token from the **CalDAV** page.
- On a personal device, trust the own CA first if you use one.

## Automatic updates

The install script also sets up a nightly update check (a systemd timer, `hejmo-update.timer`). It applies to the install script's setup; a plain Docker Compose setup is updated by you.

- **Every night at 03:30** the updater looks for the newest published version (release tags without a suffix such as `-rc.1`; preview builds are never installed).
- It installs an image only if its signature from the project's release workflow verifies (cosign, keyless via GitHub OIDC). An unsigned or wrongly signed image is not installed.
- Before switching it takes a backup of the database with SQLite's online backup, restarts the box on the new image and waits for `/healthz`. If the new version does not start or does not answer, it returns to the previous image (recorded by digest) and restores the backup. Parents then see a notice with the reason; a release that failed is not tried again until a newer one is published.
- After an update, parents see once what is new, with a link to the release notes.
- **Settings** (web interface, **Settings**): *on* (default), *off*, or *notify only* (nothing is installed; parents see that an update is available). The setting is shown only where the install script set up the updater. To stay on a version you pinned with `HEJMO_VERSION`, set automatic updates to *off*; otherwise the updater moves the box to the newest published version.
- A box that does not know its version, such as a preview build, is never updated.
- The container gets no access to the Docker socket. The box and the updater exchange only two small files in the data volume (`update-settings`, `update-status`) with fixed keys and values.
- The updater script itself changes only when you run the install script again. Check the last run with `sudo journalctl -u hejmo-update`.
- If going back fails too (the backup cannot be restored), the box stays **stopped** on purpose, and parents cannot see a notice because the box is down. If the box does not answer after a night with an update, run `sudo journalctl -u hejmo-update` to see what happened.

## Offline mode

With **offline mode** (the setup wizard asks right after you choose "Only in the home network"; later **Settings › Connections to the outside**) the box makes no connection to anything outside your home network: no weather, no iCloud, Google or other calendar servers, no subscribed calendars, no web images, no DNS service. Calendar, tasks, lists, meals, displays and personal devices in the home network keep working. The page lists every connection with its last contact and, while offline mode is on, every attempt the box refused. With a public name, offline mode needs the change to "Only in the home network" first; the page shows what that costs.

## Update without the internet

With offline mode on (**Settings › Connections to the outside**), the box does not update itself, and neither does the nightly updater: the box writes `offline=yes` into the `update-settings` file and the updater contacts nothing. You bring new versions to the box yourself. After 90 days without an update (you can change the number on that page) parents see "Not updated for 90 days" with a link to this chapter.

**On a box installed before offline mode existed, run the install script once first** (see "Updating and removing" below): an older updater does not know `offline=yes` and would keep checking for updates every night.

Uploading an update file in the web interface does not exist yet (it is planned as its own step). Until then, do it by hand, from a computer with internet and the box's address at hand:

1. **On a computer with internet**, find the newest version on the [releases page](https://github.com/hejmo-home/hejmo/releases) (use a number such as `0.2.4`, not a pre-release). Pull the image and check its signature, exactly as the updater does:
   ```bash
   docker pull ghcr.io/hejmo-home/hejmo:0.2.4
   docker run --rm ghcr.io/sigstore/cosign/cosign:v2.5.3 verify ghcr.io/hejmo-home/hejmo:0.2.4 \
     --certificate-identity https://github.com/hejmo-home/hejmo/.github/workflows/release.yml@refs/tags/v0.2.4 \
     --certificate-oidc-issuer https://token.actions.githubusercontent.com
   ```
   Go on only if it prints the signature. An unsigned or wrongly signed image must not be installed.
2. **Save the image to a file** and put it on a USB stick: `docker save ghcr.io/hejmo-home/hejmo:0.2.4 -o hejmo-0.2.4.tar`.
3. **On the box**, mount the stick, back up the data (`sudo cp -a /opt/hejmo/data /opt/hejmo/data.before-0.2.4`), then load the image: `sudo docker load -i /media/usb/hejmo-0.2.4.tar`.
4. **Switch the box to it:** set `HEJMO_IMAGE=ghcr.io/hejmo-home/hejmo:0.2.4` in `/opt/hejmo/.env`, then run `cd /opt/hejmo && sudo docker compose up -d`. Open the box and check that it works; the page "Connections to the outside" still shows that nothing went out.
5. **Tell the box that it was updated.** The box cannot see which image it runs, and it only learns about an update from the file the nightly updater writes. Without this step the page keeps showing the old version and parents keep seeing "Not updated for N days". Use the version you installed:
   ```bash
   sudo sh -c 'rm -f /opt/hejmo/data/update-status && printf "state=updated\nversion=0.2.4\ntime=%s\n" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > /opt/hejmo/data/update-status'
   ```
6. If the new version does not start, set `HEJMO_IMAGE` back to the old tag, stop the box and restore the data copy (and skip step 5).

## Time from the router

Hejmo's offline mode covers Hejmo only. The device the box runs on (Raspberry Pi OS, for example) can still fetch the time from the internet (NTP) and load its own system updates. To keep the time without the internet, point the device at your router, which many routers (a FRITZ!Box, for one) offer as a time server:

1. In the FRITZ!Box, open **Home Network › Network › Network Settings** and note the box's address (usually `192.168.178.1`). Under **Home Network Settings** make sure *Time server (NTP)* for the home network is on.
2. On the box, edit `/etc/systemd/timesyncd.conf` and set `NTP=192.168.178.1` and `FallbackNTP=` (empty), then run `sudo systemctl restart systemd-timesyncd`.
3. Check with `timedatectl timesync-status` that the server is the router.

The menu names differ between FRITZ!OS versions; the router's own help shows where the time server setting is. A box whose clock is wrong cannot validate certificates or sign anyone in, so check the time after the first start.

## Updating and removing

- Re-running the install script keeps your settings and data and refreshes the updater and the systemd units; it does not update the box itself.
- To remove the box: `sudo systemctl disable --now hejmo.service hejmo-update.timer && sudo rm /etc/systemd/system/hejmo.service /etc/systemd/system/hejmo-update.service /etc/systemd/system/hejmo-update.timer && cd /opt/hejmo && sudo docker compose down`. Your data stays in `/opt/hejmo/data` until you delete that folder.
