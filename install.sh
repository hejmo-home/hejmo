#!/usr/bin/env bash
# Hejmo install script for Raspberry Pi OS (64-bit) and other Debian-based
# systems (ADR 0018). Installs Docker if missing, writes the box's Compose
# file and a systemd unit under /opt/hejmo, and starts the box.
#
#   curl -fsSL https://raw.githubusercontent.com/hejmo-home/hejmo/main/install.sh | sudo bash
#
# It also installs the box's auto-update (ADR 0018): /opt/hejmo/hejmo-update and a
# systemd timer that runs it at 03:30. Running this script again is the only way
# the updater itself changes.
#
# Safe to run again: it changes only what differs and never overwrites
# /opt/hejmo/.env (your ports and image) or the data.
#
# Settings (environment, used only when creating .env):
#   HEJMO_VERSION  image tag, default "latest" (a published version)
#   HEJMO_IMAGE    full image name, overrides HEJMO_VERSION
#   HEJMO_ROOT     prefix for every path, for tests only
set -euo pipefail

R="${HEJMO_ROOT:-}"
DIR="$R/opt/hejmo"
UNIT="$R/etc/systemd/system/hejmo.service"
UPDATE_UNIT="$R/etc/systemd/system/hejmo-update.service"
UPDATE_TIMER="$R/etc/systemd/system/hejmo-update.timer"
IMAGE="${HEJMO_IMAGE:-ghcr.io/hejmo-home/hejmo:${HEJMO_VERSION:-latest}}"

say() { printf '==> %s\n' "$*"; }
die() { printf 'Error: %s\n' "$*" >&2; exit 1; }

# write_if_changed FILE MODE: stdin becomes FILE; returns 0 if it changed.
write_if_changed() {
	local file="$1" mode="$2" tmp
	tmp="$(mktemp)"
	cat >"$tmp"
	if [ -f "$file" ] && cmp -s "$tmp" "$file"; then
		rm -f "$tmp"
		return 1
	fi
	mkdir -p "$(dirname "$file")"
	chmod "$mode" "$tmp"
	mv "$tmp" "$file"
	return 0
}

# --- checks -----------------------------------------------------------------
[ "$(id -u)" = "0" ] || die "run as root: curl -fsSL <url> | sudo bash"
case "$(uname -m)" in
aarch64 | arm64 | x86_64 | amd64) ;;
*) die "Hejmo needs a 64-bit system (Raspberry Pi OS 64-bit); this one reports $(uname -m)." ;;
esac
# A Pi 4 or 5 running 32-bit Raspberry Pi OS boots a 64-bit kernel, so uname
# says aarch64 while the system (and Docker's images) are 32-bit.
if [ -r "$R/etc/os-release" ]; then
	# shellcheck disable=SC1090,SC1091
	[ "$(. "$R/etc/os-release" && echo "${ID:-}")" != "raspbian" ] || die "32-bit Raspberry Pi OS is not supported; install the 64-bit version."
fi
case "$(dpkg --print-architecture 2>/dev/null || true)" in
armhf | armel | i386) die "Hejmo needs a 64-bit system (Raspberry Pi OS 64-bit); this one is a 32-bit system." ;;
esac

# --- Docker -----------------------------------------------------------------
if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
	say "Docker with Compose is already installed"
else
	[ -r "$R/etc/os-release" ] || die "cannot read /etc/os-release"
	# shellcheck disable=SC1090,SC1091
	. "$R/etc/os-release"
	case "${ID:-}" in
	debian | ubuntu) ;;
	raspbian) die "32-bit Raspberry Pi OS is not supported; install the 64-bit version." ;;
	*) die "cannot install Docker on '${ID:-unknown}'. Install Docker with the Compose plugin yourself, then run this script again." ;;
	esac
	codename="${VERSION_CODENAME:-}"
	[ -n "$codename" ] || die "no VERSION_CODENAME in /etc/os-release"
	say "Installing Docker from Docker's apt repository"
	arch="$(dpkg --print-architecture)"
	apt-get update
	apt-get install -y ca-certificates curl
	mkdir -p "$R/etc/apt/keyrings"
	chmod 0755 "$R/etc/apt/keyrings"
	curl -fsSL "https://download.docker.com/linux/$ID/gpg" -o "$R/etc/apt/keyrings/docker.asc"
	chmod a+r "$R/etc/apt/keyrings/docker.asc"
	mkdir -p "$R/etc/apt/sources.list.d"
	printf 'deb [arch=%s signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/%s %s stable\n' \
		"$arch" "$ID" "$codename" >"$R/etc/apt/sources.list.d/docker.list"
	apt-get update
	apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
	systemctl enable --now docker.service
fi

# --- the box ----------------------------------------------------------------
say "Writing $DIR"
mkdir -p "$DIR/data"
chown 65532:65532 "$DIR/data" # the container runs as a non-root user

write_if_changed "$DIR/compose.yaml" 0644 <<'YAML' || true
# Written by install.sh; running the script again restores it.
# Change ports and the image in .env next to this file, not here.
services:
  hejmo:
    image: ${HEJMO_IMAGE:-ghcr.io/hejmo-home/hejmo:latest}
    container_name: hejmo
    restart: unless-stopped
    ports:
      - "${HEJMO_HTTP_PORT:-80}:8080"
      - "${HEJMO_HTTPS_PORT:-443}:8443"
    volumes:
      - /opt/hejmo/data:/data
    environment:
      TZ: Europe/Berlin
      HEJMO_PUBLIC_HTTPS_PORT: ${HEJMO_HTTPS_PORT:-443}
      HEJMO_HOST_UPDATER: "1" # tells the box that hejmo-update runs on this host
YAML

if [ ! -e "$DIR/.env" ]; then
	write_if_changed "$DIR/.env" 0644 <<ENV || true
HEJMO_IMAGE=$IMAGE
HEJMO_HTTP_PORT=80
HEJMO_HTTPS_PORT=443
ENV
fi

units_changed=0
write_if_changed "$UNIT" 0644 <<'UNIT' && units_changed=1
[Unit]
Description=Hejmo family organizer
Requires=docker.service
After=docker.service network-online.target
Wants=network-online.target

[Service]
Type=oneshot
RemainAfterExit=yes
WorkingDirectory=/opt/hejmo
ExecStart=/usr/bin/docker compose up -d --wait
ExecStop=/usr/bin/docker compose stop
TimeoutStartSec=300

[Install]
WantedBy=multi-user.target
UNIT

# --- auto-update (ADR 0018) -------------------------------------------------
write_if_changed "$DIR/hejmo-update" 0755 <<'UPDATER' || true
#!/usr/bin/env bash
# Hejmo auto-update (ADR 0018), run nightly at 03:30 by hejmo-update.timer.
# Installed by install.sh and changed only by running install.sh again; the
# script never replaces itself.
#
# Each night it fetches the newest PUBLISHED version (a release tag without a
# pre-release suffix), verifies the image's keyless cosign signature from the
# project's release workflow, takes an online backup of the database, restarts
# the box on the new image and waits for /healthz. If anything fails after the
# switch it returns to the previous image (recorded by digest) and database.
#
# It talks to the box only through two small files in the data volume, both
# with fixed keys and values: update-settings (written by the box) and
# update-status (written here). It reads nothing else from the data volume,
# never sources, evals or interpolates what it reads, and the container has no
# Docker socket.
set -uo pipefail

R="${HEJMO_ROOT:-}"
DIR="$R/opt/hejmo"
DATA="$DIR/data"
HOLD="$DIR/state" # root-only: the backup and notes the container never sees
SETTINGS="$DATA/update-settings"
STATUS="$DATA/update-status"
IMAGE_REPO="ghcr.io/hejmo-home/hejmo"
COSIGN="ghcr.io/sigstore/cosign/cosign:v2.5.3@sha256:f1946d0f30fc8e3777b02f2201e02efdba9fe38f4918162f937052fac98e083f"
SIGNER="https://github.com/hejmo-home/hejmo/.github/workflows/release.yml@refs/tags/v"
ISSUER="https://token.actions.githubusercontent.com"
LATEST_URL="https://api.github.com/repos/hejmo-home/hejmo/releases/latest"

log() { printf 'hejmo-update: %s\n' "$*"; }

# GNU mv can replace a destination that is a directory-link without entering it.
MV=(mv -f)
if mv --help 2>&1 | grep -q -- ' -T'; then MV+=(-T); fi

mkdir -p "$HOLD"
chmod 0700 "$HOLD"

# report STATE [VERSION [TARGET [REASON]]] replaces the status file. It is built
# in the root-only directory and moved over the old file, so a link the
# container planted is replaced, never written through.
report() {
	local tmp="$HOLD/status.$$"
	{
		printf 'state=%s\n' "$1"
		if [ -n "${2:-}" ]; then printf 'version=%s\n' "$2"; fi
		if [ -n "${3:-}" ]; then printf 'target=%s\n' "$3"; fi
		if [ -n "${4:-}" ]; then printf 'reason=%s\n' "$4"; fi
		printf 'time=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	} >"$tmp"
	chmod 0644 "$tmp"
	if [ -L "$STATUS" ]; then rm -f "$STATUS"; fi
	"${MV[@]}" "$tmp" "$STATUS"
}

# The mode is exactly one of three words on a line of its own. Missing means
# the default (on); anything doubtful means notify, never install.
read_mode() {
	if [ ! -e "$SETTINGS" ] && [ ! -L "$SETTINGS" ]; then
		echo on
		return
	fi
	if [ -f "$SETTINGS" ] && [ ! -L "$SETTINGS" ]; then
		local line
		line="$(head -c 256 "$SETTINGS" | grep -Ex 'mode=(on|off|notify)' | head -n 1)"
		if [ -n "$line" ]; then
			echo "${line#mode=}"
			return
		fi
	fi
	echo notify
}

# The newest published release, as x.y.z; fails on anything else.
latest_version() {
	local body tag
	body="$(curl -fsS --max-time 30 -H 'Accept: application/vnd.github+json' "$LATEST_URL")" || return 1
	tag="$(printf '%s\n' "$body" | sed -n 's/.*"tag_name" *: *"\([^"]*\)".*/\1/p' | head -n 1)"
	[[ "$tag" =~ ^v([0-9]+\.[0-9]+\.[0-9]+)$ ]] || return 1
	echo "${BASH_REMATCH[1]}"
}

# version_gt A B: A is a newer version than B.
version_gt() {
	local -a a b
	IFS=. read -r -a a <<<"$1"
	IFS=. read -r -a b <<<"$2"
	local i
	for i in 0 1 2; do
		if ((${a[$i]:-0} > ${b[$i]:-0})); then return 0; fi
		if ((${a[$i]:-0} < ${b[$i]:-0})); then return 1; fi
	done
	return 1
}

current_version() {
	local v
	v="$(docker inspect hejmo --format '{{index .Config.Labels "org.opencontainers.image.version"}}' 2>/dev/null)"
	if [[ "$v" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then echo "$v"; fi
}

# repo_digest IMAGE: IMAGE's reference by digest, empty if it has none.
repo_digest() {
	docker image inspect --format '{{range .RepoDigests}}{{println .}}{{end}}' "$1" 2>/dev/null |
		grep -E "^${IMAGE_REPO}@sha256:[0-9a-f]{64}\$" | head -n 1
}

# use_image REF points the Compose file's HEJMO_IMAGE at REF.
use_image() {
	local tmp="$HOLD/env.$$"
	{
		grep -v '^HEJMO_IMAGE=' "$DIR/.env" 2>/dev/null
		printf 'HEJMO_IMAGE=%s\n' "$1"
	} >"$tmp"
	mv -f "$tmp" "$DIR/.env"
}

# start_box: bring the box up and wait for /healthz. 1 = did not start, 2 = no answer.
start_box() {
	(cd "$DIR" && docker compose up -d --wait --wait-timeout 180) >/dev/null 2>&1 || return 1
	local port i
	port="$(sed -n 's/^HEJMO_HTTP_PORT=//p' "$DIR/.env" | head -n 1)"
	[[ "$port" =~ ^[0-9]+$ ]] || port=80
	for ((i = 0; i < 60; i++)); do
		if curl -fsS --max-time 5 "http://127.0.0.1:$port/healthz" >/dev/null 2>&1; then return 0; fi
		sleep 2
	done
	return 2
}

mode="$(read_mode)"
if [ "$mode" = off ]; then
	log "automatic updates are off"
	exit 0
fi

latest="$(latest_version)" || {
	log "cannot tell the newest version (offline?); trying again tomorrow"
	exit 0
}
current="$(current_version)"
# A box that does not know its version (a preview build has no version label)
# may be newer than the release; installing it could be a downgrade onto a
# database the older version cannot read. Nothing is installed or reported.
if [ -z "$current" ]; then
	log "the running version is unknown (a preview build?); no update"
	exit 0
fi
if ! version_gt "$latest" "$current"; then
	# An "available" report that is out of date would stay for good.
	if [ -f "$STATUS" ] && [ ! -L "$STATUS" ] && head -c 256 "$STATUS" | grep -qx 'state=available'; then
		report idle "$current"
	fi
	log "version $current is up to date"
	exit 0
fi

if [ "$mode" = notify ]; then
	report available "$current" "$latest"
	log "version $latest is available (notify only)"
	exit 0
fi

# A release that was rejected or rolled back is not tried again every night.
if [ -f "$HOLD/skip-version" ] && [ "$(head -c 32 "$HOLD/skip-version")" = "$latest" ]; then
	log "version $latest failed before; waiting for a newer one"
	exit 0
fi

# 1. The box must be running: a stopped one cannot make the backup. (A missing
# container has no version and ended the run above.)
if [ "$(docker inspect hejmo --format '{{.State.Running}}' 2>/dev/null)" != true ]; then
	report failed "$current" "$latest" box_not_running
	log "the box is not running; no update"
	exit 1
fi

# 2. Remember what runs now, by digest.
image_id="$(docker inspect hejmo --format '{{.Image}}' 2>/dev/null)"
previous=""
if [ -n "$image_id" ]; then
	previous="$(repo_digest "$image_id")"
	[ -n "$previous" ] || previous="$image_id"
fi
if [ -z "$previous" ]; then
	report failed "$current" "$latest" box_not_running
	log "the box has no image to go back to; no update"
	exit 1
fi

# 3. Fetch the new image, then verify it before anything is started or changed.
if ! docker pull "$IMAGE_REPO:$latest" >/dev/null 2>&1; then
	report failed "$current" "$latest" pull_failed
	exit 1
fi
new="$(repo_digest "$IMAGE_REPO:$latest")"
if [ -z "$new" ] || ! docker run --rm "$COSIGN" verify "$new" \
	--certificate-identity "$SIGNER$latest" \
	--certificate-oidc-issuer "$ISSUER" >/dev/null 2>&1; then
	docker rmi "$IMAGE_REPO:$latest" >/dev/null 2>&1
	printf '%s\n' "$latest" >"$HOLD/skip-version"
	report rejected "$current" "$latest" signature_invalid
	log "version $latest was not installed: its signature did not verify"
	exit 0
fi

# 4. Online backup by the running box, copied out of the data volume. docker cp
# does not follow links, so a link in the volume cannot make this read a host file.
rm -f "$DATA/pre-update.db" "$HOLD/backup.new"
if ! docker exec hejmo /hejmo backup /data/pre-update.db >/dev/null 2>&1 ||
	! docker cp hejmo:/data/pre-update.db "$HOLD/backup.new" >/dev/null 2>&1 ||
	[ -L "$HOLD/backup.new" ] || [ ! -s "$HOLD/backup.new" ]; then
	rm -f "$DATA/pre-update.db" "$HOLD/backup.new"
	report failed "$current" "$latest" backup_failed
	log "the backup failed; nothing was changed"
	exit 1
fi
rm -f "$DATA/pre-update.db"
mv -f "$HOLD/backup.new" "$HOLD/pre-update.db"

# 5. Switch, and return to the previous image and database if it does not work.
rollback() {
	local reason="$1"
	log "version $latest failed ($reason); going back to the previous image and database"
	(cd "$DIR" && docker compose stop) >/dev/null 2>&1
	local restored=0
	if cp "$HOLD/pre-update.db" "$HOLD/restore.db" &&
		rm -f "$DATA/hejmo.db" "$DATA/hejmo.db-wal" "$DATA/hejmo.db-shm" &&
		mv -f "$HOLD/restore.db" "$DATA/hejmo.db" &&
		chown 65532:65532 "$DATA/hejmo.db" &&
		chmod 0600 "$DATA/hejmo.db"; then
		restored=1
	fi
	# If the restore failed the box stays stopped: starting the old image on a
	# half-restored or newer database could do more harm.
	use_image "$previous"
	if [ "$restored" = 1 ] && start_box; then
		printf '%s\n' "$latest" >"$HOLD/skip-version"
		report rolled_back "$current" "$latest" "$reason"
		exit 0
	fi
	report failed "$current" "$latest" rollback_failed
	log "going back failed too (database restored: $restored)"
	exit 1
}

use_image "$new"
start_box
case $? in
0) ;;
1) rollback start_failed ;;
*) rollback health_check_failed ;;
esac
report updated "$latest"
docker image prune -f >/dev/null 2>&1
log "updated to $latest"
UPDATER

write_if_changed "$UPDATE_UNIT" 0644 <<'UNIT' && units_changed=1
[Unit]
Description=Hejmo update check
Requires=docker.service
After=docker.service network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/opt/hejmo/hejmo-update
TimeoutStartSec=1800
UNIT

write_if_changed "$UPDATE_TIMER" 0644 <<'UNIT' && units_changed=1
[Unit]
Description=Hejmo nightly update check

[Timer]
OnCalendar=*-*-* 03:30:00
RandomizedDelaySec=15min
Persistent=true

[Install]
WantedBy=timers.target
UNIT

if [ "$units_changed" = 1 ]; then
	systemctl daemon-reload
fi
if ! systemctl is-enabled --quiet hejmo.service 2>/dev/null; then
	systemctl enable hejmo.service
fi
if ! systemctl is-enabled --quiet hejmo-update.timer 2>/dev/null; then
	systemctl enable --now hejmo-update.timer
fi

say "Starting the box"
(cd "$DIR" && docker compose up -d --wait --wait-timeout 300)
systemctl is-active --quiet hejmo.service || systemctl start hejmo.service

# --- wait until it answers --------------------------------------------------
port="$(sed -n 's/^HEJMO_HTTP_PORT=//p' "$DIR/.env" | head -n 1)"
port="${port:-80}"
ok=0
for ((i = 0; i < 60; i++)); do
	if curl -fsS "http://127.0.0.1:$port/healthz" >/dev/null 2>&1; then
		ok=1
		break
	fi
	sleep 1
done
[ "$ok" = 1 ] || die "the box did not answer on port $port; see: docker compose -f $DIR/compose.yaml logs"

ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
[ -n "$ip" ] || ip="$(hostname)"
url="http://$ip"
[ "$port" = 80 ] || url="$url:$port"
say "Hejmo is running. Open $url in a browser on your home network to start the setup wizard."
