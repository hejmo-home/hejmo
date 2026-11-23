#!/usr/bin/env bash
# Hejmo install script for Raspberry Pi OS (64-bit) and other Debian-based
# systems (ADR 0018). Installs Docker if missing, writes the box's Compose
# file and a systemd unit under /opt/hejmo, and starts the box.
#
#   curl -fsSL https://raw.githubusercontent.com/hejmo-home/hejmo/main/install.sh | sudo bash
#
# It also installs the box's auto-update (ADR 0018): /opt/hejmo/hejmo-update and a
# systemd timer that runs it at 03:30, and a path unit that runs it when the box
# hands over an uploaded update file (offline mode, ticket 221). Running this
# script again is the only way the updater itself changes.
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
UPDATE_INSTALL_UNIT="$R/etc/systemd/system/hejmo-update-install.service"
UPDATE_PATH="$R/etc/systemd/system/hejmo-update.path"
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
      HEJMO_HOST_UPDATER_INSTALL: "1" # ... and that it also installs an update file the box hands over (ticket 221)
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
# with fixed keys and values: update-settings (written by the box; mode=on, off
# or notify, offline=yes while offline mode is on or the update check is
# switched off, then it contacts nothing, and, after a parent's yes to an
# uploaded update file, install, install_sha256 and install_time) and
# update-status (written here). It reads nothing else from the data volume except
# the uploaded file named update-file, never sources, evals or interpolates what
# it reads, and the container has no Docker socket.
#
# With --install (run by hejmo-update.path when the settings file changes) it
# does only one thing: install the update file the box handed over (ticket 221).
# It copies the file out of the data volume, checks it again on its own (the
# container is not trusted): the SHA-256 the box named, exactly the three
# members of an update file, the cosign signature of the release workflow of
# that version, offline against the Sigstore root kept in /opt/hejmo, the
# architecture, the version and the image's hash. Then it loads the image and
# goes on like the nightly update: backup, switch, health check, rollback.
set -uo pipefail

R="${HEJMO_ROOT:-}"
DIR="$R/opt/hejmo"
DATA="$DIR/data"
HOLD="$DIR/state" # root-only: the backup and notes the container never sees
SETTINGS="$DATA/update-settings"
STATUS="$DATA/update-status"
IMAGE_REPO="ghcr.io/hejmo-home/hejmo"
COSIGN="ghcr.io/sigstore/cosign/cosign:v2.5.3@sha256:f1946d0f30fc8e3777b02f2201e02efdba9fe38f4918162f937052fac98e083f"
TRUSTED_ROOT="$DIR/trusted-root.json" # the Sigstore public-good root, for the offline check of an uploaded file
SIGNER="https://github.com/hejmo-home/hejmo/.github/workflows/release.yml@refs/tags/v"
ISSUER="https://token.actions.githubusercontent.com"
LATEST_URL="https://api.github.com/repos/hejmo-home/hejmo/releases/latest"

log() { printf 'hejmo-update: %s\n' "$*"; }

# GNU mv can replace a destination that is a directory-link without entering it.
MV=(mv -f)
if mv --help 2>&1 | grep -q -- ' -T'; then MV+=(-T); fi

mkdir -p "$HOLD"
chmod 0700 "$HOLD"

CHECKED=""
UPLOADED=0 # 1 while an uploaded file is installed
STEP=""   # where an installation of an uploaded file is: verify, backup, start or health
BACKUP="" # when its data was saved

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
		if [ -n "$STEP" ]; then printf 'step=%s\n' "$STEP"; fi
		if [ -n "$BACKUP" ]; then printf 'backup=%s\n' "$BACKUP"; fi
		printf 'time=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
		if [ -n "$CHECKED" ]; then printf 'checked=%s\n' "$CHECKED"; fi
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

# offline: the box says, with the exact line offline=yes, that this script must
# not connect to anything (offline mode, or the update check switched off).
# Anything else, a link included, means online.
offline_requested() {
	[ -f "$SETTINGS" ] && [ ! -L "$SETTINGS" ] && head -c 256 "$SETTINGS" | grep -qx 'offline=yes'
}

# mark_checked records, in the status file, when the release feed was last
# asked. The report parents have not seen yet stays as it is.
mark_checked() {
	local tmp="$HOLD/status.$$"
	CHECKED="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
	{
		if [ -f "$STATUS" ] && [ ! -L "$STATUS" ]; then
			head -c 1024 "$STATUS" | grep -E '^(state|version|target|reason|time)=' || true
		fi
		printf 'checked=%s\n' "$CHECKED"
	} >"$tmp"
	chmod 0644 "$tmp"
	if [ -L "$STATUS" ]; then rm -f "$STATUS"; fi
	"${MV[@]}" "$tmp" "$STATUS"
}

# mark_version VERSION records, in the status file, which version runs, so the
# page for an update file can say what it replaces; the report parents have not
# seen yet stays as it is.
mark_version() {
	local tmp="$HOLD/status.$$"
	{
		if [ -f "$STATUS" ] && [ ! -L "$STATUS" ]; then
			head -c 1024 "$STATUS" | grep -E '^(state|target|reason|step|backup|time|checked)=' || true
		fi
		printf 'version=%s\n' "$1"
	} >"$tmp"
	chmod 0644 "$tmp"
	if [ -L "$STATUS" ]; then rm -f "$STATUS"; fi
	"${MV[@]}" "$tmp" "$STATUS"
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

# step NAME tells parents where an installation of an uploaded file is (only then).
step() {
	if [ "$UPLOADED" = 1 ]; then
		STEP="$1"
		report installing "$current" "$latest"
	fi
}

# start_box: bring the box up and wait for /healthz. 1 = did not start, 2 = no answer.
start_box() {
	(cd "$DIR" && docker compose up -d --wait --wait-timeout 180) >/dev/null 2>&1 || return 1
	step health
	local port i
	port="$(sed -n 's/^HEJMO_HTTP_PORT=//p' "$DIR/.env" | head -n 1)"
	[[ "$port" =~ ^[0-9]+$ ]] || port=80
	for ((i = 0; i < 60; i++)); do
		if curl -fsS --max-time 5 "http://127.0.0.1:$port/healthz" >/dev/null 2>&1; then return 0; fi
		sleep 2
	done
	return 2
}

# The architecture an update file must be for.
host_arch() {
	case "$(uname -m)" in
	aarch64 | arm64) echo arm64 ;;
	x86_64 | amd64) echo amd64 ;;
	esac
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

# epoch_of "2026-11-23T21:04:12Z" prints the seconds since 1970 (GNU and BSD date).
epoch_of() {
	date -u -d "$1" +%s 2>/dev/null || date -u -j -f '%Y-%m-%dT%H:%M:%SZ' "$1" +%s 2>/dev/null
}

# check_running TARGET: the box must be running: a stopped one cannot make the
# backup. (A missing container has no version and ended the run before.)
check_running() {
	if [ "$(docker inspect hejmo --format '{{.State.Running}}' 2>/dev/null)" != true ]; then
		report failed "$current" "$1" box_not_running
		log "the box is not running; no update"
		exit 1
	fi
}

# remember_previous TARGET sets $previous to what runs now, by digest.
remember_previous() {
	local image_id
	image_id="$(docker inspect hejmo --format '{{.Image}}' 2>/dev/null)"
	previous=""
	if [ -n "$image_id" ]; then
		previous="$(repo_digest "$image_id")"
		[ -n "$previous" ] || previous="$image_id"
	fi
	if [ -z "$previous" ]; then
		report failed "$current" "$1" box_not_running
		log "the box has no image to go back to; no update"
		exit 1
	fi
}

# switch_to IMAGE: the new version is $latest, the old one $current and $previous.
# Online backup by the running box, copied out of the data volume (docker cp
# does not follow links, so a link in the volume cannot make this read a host
# file); switch, and return to the previous image and database if it does not
# work. An update that failed is not tried again every night unless it came as an uploaded file.
switch_to() {
	local new="$1"
	step backup
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
	if [ "$UPLOADED" = 1 ]; then BACKUP="$(date -u +%Y-%m-%dT%H:%M:%SZ)"; fi

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
			if [ "$UPLOADED" = 0 ]; then printf '%s\n' "$latest" >"$HOLD/skip-version"; fi
			STEP=""
			report rolled_back "$current" "$latest" "$reason"
			exit 0
		fi
		STEP=""
		report failed "$current" "$latest" rollback_failed
		log "going back failed too (database restored: $restored)"
		exit 1
	}

	use_image "$new"
	step start
	start_box
	case $? in
	0) ;;
	1) rollback start_failed ;;
	*) rollback health_check_failed ;;
	esac
	STEP=""
	report updated "$latest"
	docker image prune -f >/dev/null 2>&1
	log "updated to $latest"
}

# ---- an uploaded update file (ticket 221, Q154) ---------------------------------

REQ_VERSION=""
REQ_SHA=""
REQ_TIME=""

# read_request takes the install request out of the settings file: three lines
# with exactly these shapes, from a regular file. Anything else is no request.
read_request() {
	REQ_VERSION=""
	REQ_SHA=""
	REQ_TIME=""
	[ -f "$SETTINGS" ] && [ ! -L "$SETTINGS" ] || return 1
	local text
	text="$(head -c 1024 "$SETTINGS")"
	REQ_VERSION="$(printf '%s\n' "$text" | grep -Ex 'install=[0-9]{1,4}\.[0-9]{1,4}\.[0-9]{1,4}' | head -n 1)"
	REQ_SHA="$(printf '%s\n' "$text" | grep -Ex 'install_sha256=[0-9a-f]{64}' | head -n 1)"
	REQ_TIME="$(printf '%s\n' "$text" | grep -Ex 'install_time=[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z' | head -n 1)"
	REQ_VERSION="${REQ_VERSION#install=}"
	REQ_SHA="${REQ_SHA#install_sha256=}"
	REQ_TIME="${REQ_TIME#install_time=}"
	[ -n "$REQ_VERSION" ] && [ -n "$REQ_SHA" ] && [ -n "$REQ_TIME" ]
}

# fixed_field FILE NAME PATTERN prints the value of a line of the manifest, which
# the release builder writes with two spaces of indent; nothing else matches.
fixed_field() {
	sed -n "s/^  \"$2\": \"\\($3\\)\",\\{0,1\\}\$/\\1/p" "$1" | head -n 1
}

# refuse_file STATE REASON: an uploaded file that is not installed. Nothing was changed.
refuse_file() {
	STEP=""
	report "$1" "$current" "$latest" "$2"
	log "the uploaded file for $latest was not installed ($2)"
	printf '%s\n' "$REQ_SHA:$REQ_TIME" >"$HOLD/install-done"
	rm -rf "$HOLD/incoming" "$HOLD/incoming.update"
	rm -f "$DATA/update-file"
	exit 0
}

run_install() {
	read_request || exit 0
	if [ -f "$HOLD/install-done" ] && [ "$(head -c 200 "$HOLD/install-done")" = "$REQ_SHA:$REQ_TIME" ]; then
		exit 0 # this yes was answered already
	fi
	# A yes is good for two hours; a request left in the file is not an order.
	local now asked
	now="$(date -u +%s)"
	asked="$(epoch_of "$REQ_TIME")"
	if [ -z "$asked" ] || [ $((now - asked)) -gt 7200 ] || [ $((asked - now)) -gt 600 ]; then
		log "the request to install $REQ_VERSION is too old or from the future; ignoring it"
		exit 0
	fi
	UPLOADED=1
	latest="$REQ_VERSION"
	current="$(current_version)"
	STEP=verify
	if [ -z "$current" ]; then
		log "the running version is unknown (a preview build?); no update"
		refuse_file failed file_invalid
	fi
	report installing "$current" "$latest"
	check_running "$latest"

	# 1. Take the file out of the data volume, where the container could change it.
	local file="$DATA/update-file" copy="$HOLD/incoming.update" dir="$HOLD/incoming"
	rm -rf "$dir" "$copy"
	if [ ! -f "$file" ] || [ -L "$file" ] || [ "$(wc -c <"$file")" -gt 524288000 ]; then
		refuse_file failed file_invalid
	fi
	cp "$file" "$copy"
	rm -f "$file"
	if [ "$(sha256_of "$copy")" != "$REQ_SHA" ]; then
		refuse_file failed file_invalid
	fi

	# 2. Exactly three members, plain files, extracted into a directory only root can reach.
	if [ "$(tar -tf "$copy" 2>/dev/null | sort | tr '\n' ' ')" != "hejmo-update.json hejmo-update.sigstore.json image.tar " ]; then
		refuse_file failed file_invalid
	fi
	mkdir -p "$dir"
	chmod 0755 "$dir"
	if ! tar -xf "$copy" -C "$dir" --no-same-owner hejmo-update.json hejmo-update.sigstore.json image.tar 2>/dev/null; then
		refuse_file failed file_invalid
	fi
	local f
	for f in hejmo-update.json hejmo-update.sigstore.json image.tar; do
		if [ ! -f "$dir/$f" ] || [ -L "$dir/$f" ]; then refuse_file failed file_invalid; fi
		chmod 0644 "$dir/$f"
	done
	if [ "$(wc -c <"$dir/hejmo-update.json")" -gt 65536 ] || [ "$(wc -c <"$dir/hejmo-update.sigstore.json")" -gt 262144 ]; then
		refuse_file failed file_invalid
	fi

	# 3. The manifest says what the file holds; the signature says the project wrote it.
	local m_version m_arch m_sha
	m_version="$(fixed_field "$dir/hejmo-update.json" version '[0-9]\{1,4\}\.[0-9]\{1,4\}\.[0-9]\{1,4\}')"
	m_arch="$(fixed_field "$dir/hejmo-update.json" arch '[a-z0-9]\{1,10\}')"
	m_sha="$(fixed_field "$dir/hejmo-update.json" image_sha256 '[0-9a-f]\{64\}')"
	if [ "$m_version" != "$latest" ] || [ -z "$m_sha" ] || ! grep -qx '  "format": 1,' "$dir/hejmo-update.json"; then
		refuse_file failed file_invalid
	fi
	if ! docker image inspect "$COSIGN" >/dev/null 2>&1; then
		log "the program that checks signatures is not on this box"
		refuse_file failed verifier_missing
	fi
	if ! docker run --rm --network none -v "$dir:/in:ro" -v "$TRUSTED_ROOT:/trusted-root.json:ro" "$COSIGN" verify-blob \
		--new-bundle-format --offline --trusted-root /trusted-root.json \
		--bundle /in/hejmo-update.sigstore.json \
		--certificate-identity "$SIGNER$latest" \
		--certificate-oidc-issuer "$ISSUER" /in/hejmo-update.json >/dev/null 2>&1; then
		refuse_file rejected signature_invalid
	fi
	# From here on the manifest is the project's word.
	if [ "$m_arch" != "$(host_arch)" ] || ! version_gt "$latest" "$current" || [ "$(sha256_of "$dir/image.tar")" != "$m_sha" ]; then
		refuse_file failed file_invalid
	fi

	# 4. Load the image; it must be the version the file says.
	if ! docker load -i "$dir/image.tar" >/dev/null 2>&1 ||
		[ "$(docker image inspect "$IMAGE_REPO:$latest" --format '{{index .Config.Labels "org.opencontainers.image.version"}}' 2>/dev/null)" != "$latest" ]; then
		docker rmi "$IMAGE_REPO:$latest" >/dev/null 2>&1
		refuse_file failed load_failed
	fi
	printf '%s\n' "$REQ_SHA:$REQ_TIME" >"$HOLD/install-done" # the yes is used up from here on
	rm -rf "$dir" "$copy"

	# 5. Back up, switch, check, and go back if it does not answer.
	remember_previous "$latest"
	switch_to "$IMAGE_REPO:$latest"
	exit 0
}

if [ "${1:-}" = "--install" ]; then
	run_install
fi

mode="$(read_mode)"
if [ "$mode" = off ]; then
	log "automatic updates are off"
	exit 0
fi

if offline_requested; then
	log "offline mode: no check for updates"
	# Parents are told which version runs; the host script is the only one who can see it.
	current="$(current_version)"
	if [ -n "$current" ]; then mark_version "$current"; fi
	exit 0
fi

latest="$(latest_version)" || {
	log "cannot tell the newest version (offline?); trying again tomorrow"
	exit 0
}
mark_checked
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

# 1. The box must be running.
check_running "$latest"

# 2. Remember what runs now, by digest.
remember_previous "$latest"

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

# 4. and 5. Back up, switch, and go back if it does not work.
switch_to "$new"
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

# The Sigstore public-good root (Fulcio, Rekor and the timestamp authority), for
# the offline check of an uploaded update file. It is the file the box checks
# with (internal/update/trusted_root.json; a test keeps the two the same).
write_if_changed "$DIR/trusted-root.json" 0644 <<'ROOT' || true
{"mediaType":"application/vnd.dev.sigstore.trustedroot+json;version=0.1","tlogs":[{"baseUrl":"https://rekor.sigstore.dev","hashAlgorithm":"SHA2_256","publicKey":{"rawBytes":"MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAE2G2Y+2tabdTV5BcGiBIx0a9fAFwrkBbmLSGtks4L3qX6yYY0zufBnhC8Ur/iy55GhWP/9A/bY2LhC30M9+RYtw==","keyDetails":"PKIX_ECDSA_P256_SHA_256","validFor":{"start":"2021-01-12T11:53:27Z"}},"logId":{"keyId":"wNI9atQGlz+VWfO6LRygH4QUfY/8W4RFwiT5i5WRgB0="}},{"baseUrl":"https://log2025-1.rekor.sigstore.dev","hashAlgorithm":"SHA2_256","publicKey":{"rawBytes":"MCowBQYDK2VwAyEAt8rlp1knGwjfbcXAYPYAkn0XiLz1x8O4t0YkEhie244=","keyDetails":"PKIX_ED25519","validFor":{"start":"2025-09-23T00:00:00Z"}},"logId":{"keyId":"zxGZFVvd0FEmjR8WrFwMdcAJ9vtaY/QXf44Y1wUeP6A="}}],"certificateAuthorities":[{"subject":{"organization":"sigstore.dev","commonName":"sigstore"},"uri":"https://fulcio.sigstore.dev","certChain":{"certificates":[{"rawBytes":"MIIB+DCCAX6gAwIBAgITNVkDZoCiofPDsy7dfm6geLbuhzAKBggqhkjOPQQDAzAqMRUwEwYDVQQKEwxzaWdzdG9yZS5kZXYxETAPBgNVBAMTCHNpZ3N0b3JlMB4XDTIxMDMwNzAzMjAyOVoXDTMxMDIyMzAzMjAyOVowKjEVMBMGA1UEChMMc2lnc3RvcmUuZGV2MREwDwYDVQQDEwhzaWdzdG9yZTB2MBAGByqGSM49AgEGBSuBBAAiA2IABLSyA7Ii5k+pNO8ZEWY0ylemWDowOkNa3kL+GZE5Z5GWehL9/A9bRNA3RbrsZ5i0JcastaRL7Sp5fp/jD5dxqc/UdTVnlvS16an+2Yfswe/QuLolRUCrcOE2+2iA5+tzd6NmMGQwDgYDVR0PAQH/BAQDAgEGMBIGA1UdEwEB/wQIMAYBAf8CAQEwHQYDVR0OBBYEFMjFHQBBmiQpMlEk6w2uSu1KBtPsMB8GA1UdIwQYMBaAFMjFHQBBmiQpMlEk6w2uSu1KBtPsMAoGCCqGSM49BAMDA2gAMGUCMH8liWJfMui6vXXBhjDgY4MwslmN/TJxVe/83WrFomwmNf056y1X48F9c4m3a3ozXAIxAKjRay5/aj/jsKKGIkmQatjI8uupHr/+CxFvaJWmpYqNkLDGRU+9orzh5hI2RrcuaQ=="}]},"validFor":{"start":"2021-03-07T03:20:29Z","end":"2022-12-31T23:59:59.999Z"}},{"subject":{"organization":"sigstore.dev","commonName":"sigstore"},"uri":"https://fulcio.sigstore.dev","certChain":{"certificates":[{"rawBytes":"MIICGjCCAaGgAwIBAgIUALnViVfnU0brJasmRkHrn/UnfaQwCgYIKoZIzj0EAwMwKjEVMBMGA1UEChMMc2lnc3RvcmUuZGV2MREwDwYDVQQDEwhzaWdzdG9yZTAeFw0yMjA0MTMyMDA2MTVaFw0zMTEwMDUxMzU2NThaMDcxFTATBgNVBAoTDHNpZ3N0b3JlLmRldjEeMBwGA1UEAxMVc2lnc3RvcmUtaW50ZXJtZWRpYXRlMHYwEAYHKoZIzj0CAQYFK4EEACIDYgAE8RVS/ysH+NOvuDZyPIZtilgUF9NlarYpAd9HP1vBBH1U5CV77LSS7s0ZiH4nE7Hv7ptS6LvvR/STk798LVgMzLlJ4HeIfF3tHSaexLcYpSASr1kS0N/RgBJz/9jWCiXno3sweTAOBgNVHQ8BAf8EBAMCAQYwEwYDVR0lBAwwCgYIKwYBBQUHAwMwEgYDVR0TAQH/BAgwBgEB/wIBADAdBgNVHQ4EFgQU39Ppz1YkEZb5qNjpKFWixi4YZD8wHwYDVR0jBBgwFoAUWMAeX5FFpWapesyQoZMi0CrFxfowCgYIKoZIzj0EAwMDZwAwZAIwPCsQK4DYiZYDPIaDi5HFKnfxXx6ASSVmERfsynYBiX2X6SJRnZU84/9DZdnFvvxmAjBOt6QpBlc4J/0DxvkTCqpclvziL6BCCPnjdlIB3Pu3BxsPmygUY7Ii2zbdCdliiow="},{"rawBytes":"MIIB9zCCAXygAwIBAgIUALZNAPFdxHPwjeDloDwyYChAO/4wCgYIKoZIzj0EAwMwKjEVMBMGA1UEChMMc2lnc3RvcmUuZGV2MREwDwYDVQQDEwhzaWdzdG9yZTAeFw0yMTEwMDcxMzU2NTlaFw0zMTEwMDUxMzU2NThaMCoxFTATBgNVBAoTDHNpZ3N0b3JlLmRldjERMA8GA1UEAxMIc2lnc3RvcmUwdjAQBgcqhkjOPQIBBgUrgQQAIgNiAAT7XeFT4rb3PQGwS4IajtLk3/OlnpgangaBclYpsYBr5i+4ynB07ceb3LP0OIOZdxexX69c5iVuyJRQ+Hz05yi+UF3uBWAlHpiS5sh0+H2GHE7SXrk1EC5m1Tr19L9gg92jYzBhMA4GA1UdDwEB/wQEAwIBBjAPBgNVHRMBAf8EBTADAQH/MB0GA1UdDgQWBBRYwB5fkUWlZql6zJChkyLQKsXF+jAfBgNVHSMEGDAWgBRYwB5fkUWlZql6zJChkyLQKsXF+jAKBggqhkjOPQQDAwNpADBmAjEAj1nHeXZp+13NWBNa+EDsDP8G1WWg1tCMWP/WHPqpaVo0jhsweNFZgSs0eE7wYI4qAjEA2WB9ot98sIkoF3vZYdd3/VtWB5b9TNMea7Ix/stJ5TfcLLeABLE4BNJOsQ4vnBHJ"}]},"validFor":{"start":"2022-04-13T20:06:15Z"}}],"ctlogs":[{"baseUrl":"https://ctfe.sigstore.dev/test","hashAlgorithm":"SHA2_256","publicKey":{"rawBytes":"MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEbfwR+RJudXscgRBRpKX1XFDy3PyudDxz/SfnRi1fT8ekpfBd2O1uoz7jr3Z8nKzxA69EUQ+eFCFI3zeubPWU7w==","keyDetails":"PKIX_ECDSA_P256_SHA_256","validFor":{"start":"2021-03-14T00:00:00Z","end":"2022-10-31T23:59:59.999Z"}},"logId":{"keyId":"CGCS8ChS/2hF0dFrJ4ScRWcYrBY9wzjSbea8IgY2b3I="}},{"baseUrl":"https://ctfe.sigstore.dev/2022","hashAlgorithm":"SHA2_256","publicKey":{"rawBytes":"MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEiPSlFi0CmFTfEjCUqF9HuCEcYXNKAaYalIJmBZ8yyezPjTqhxrKBpMnaocVtLJBI1eM3uXnQzQGAJdJ4gs9Fyw==","keyDetails":"PKIX_ECDSA_P256_SHA_256","validFor":{"start":"2022-10-20T00:00:00Z"}},"logId":{"keyId":"3T0wasbHETJjGR4cmWc3AqJKXrjePK3/h4pygC8p7o4="}}],"timestampAuthorities":[{"subject":{"organization":"sigstore.dev","commonName":"sigstore-tsa-selfsigned"},"uri":"https://timestamp.sigstore.dev/api/v1/timestamp","certChain":{"certificates":[{"rawBytes":"MIICEDCCAZagAwIBAgIUOhNULwyQYe68wUMvy4qOiyojiwwwCgYIKoZIzj0EAwMwOTEVMBMGA1UEChMMc2lnc3RvcmUuZGV2MSAwHgYDVQQDExdzaWdzdG9yZS10c2Etc2VsZnNpZ25lZDAeFw0yNTA0MDgwNjU5NDNaFw0zNTA0MDYwNjU5NDNaMC4xFTATBgNVBAoTDHNpZ3N0b3JlLmRldjEVMBMGA1UEAxMMc2lnc3RvcmUtdHNhMHYwEAYHKoZIzj0CAQYFK4EEACIDYgAE4ra2Z8hKNig2T9kFjCAToGG30jky+WQv3BzL+mKvh1SKNR/UwuwsfNCg4sryoYAd8E6isovVA3M4aoNdm9QDi50Z8nTEyvqgfDPtTIwXItfiW/AFf1V7uwkbkAoj0xxco2owaDAOBgNVHQ8BAf8EBAMCB4AwHQYDVR0OBBYEFIn9eUOHz9BlRsMCRscsc1t9tOsDMB8GA1UdIwQYMBaAFJjsAe9/u1H/1JUeb4qImFMHic6/MBYGA1UdJQEB/wQMMAoGCCsGAQUFBwMIMAoGCCqGSM49BAMDA2gAMGUCMDtpsV/6KaO0qyF/UMsX2aSUXKQFdoGTptQGc0ftq1csulHPGG6dsmyMNd3JB+G3EQIxAOajvBcjpJmKb4Nv+2Taoj8Uc5+b6ih6FXCCKraSqupe07zqswMcXJTe1cExvHvvlw=="},{"rawBytes":"MIIB9zCCAXygAwIBAgIUV7f0GLDOoEzIh8LXSW80OJiUp14wCgYIKoZIzj0EAwMwOTEVMBMGA1UEChMMc2lnc3RvcmUuZGV2MSAwHgYDVQQDExdzaWdzdG9yZS10c2Etc2VsZnNpZ25lZDAeFw0yNTA0MDgwNjU5NDNaFw0zNTA0MDYwNjU5NDNaMDkxFTATBgNVBAoTDHNpZ3N0b3JlLmRldjEgMB4GA1UEAxMXc2lnc3RvcmUtdHNhLXNlbGZzaWduZWQwdjAQBgcqhkjOPQIBBgUrgQQAIgNiAAQUQNtfRT/ou3YATa6wB/kKTe70cfJwyRIBovMnt8RcJph/COE82uyS6FmppLLL1VBPGcPfpQPYJNXzWwi8icwhKQ6W/Qe2h3oebBb2FHpwNJDqo+TMaC/tdfkv/ElJB72jRTBDMA4GA1UdDwEB/wQEAwIBBjASBgNVHRMBAf8ECDAGAQH/AgEAMB0GA1UdDgQWBBSY7AHvf7tR/9SVHm+KiJhTB4nOvzAKBggqhkjOPQQDAwNpADBmAjEAwGEGrfGZR1cen1R8/DTVMI943LssZmJRtDp/i7SfGHmGRP6gRbuj9vOK3b67Z0QQAjEAuT2H673LQEaHTcyQSZrkp4mX7WwkmF+sVbkYY5mXN+RMH13KUEHHOqASaemYWK/E"}]},"validFor":{"start":"2025-07-04T00:00:00Z"}}]}
ROOT

write_if_changed "$UPDATE_INSTALL_UNIT" 0644 <<'UNIT' && units_changed=1
[Unit]
Description=Hejmo install of an update file
Requires=docker.service
After=docker.service

[Service]
Type=oneshot
ExecStart=/opt/hejmo/hejmo-update --install
TimeoutStartSec=1800
UNIT

write_if_changed "$UPDATE_PATH" 0644 <<'UNIT' && units_changed=1
[Unit]
Description=Hejmo: the box handed over an update file

[Path]
PathChanged=/opt/hejmo/data/update-settings
Unit=hejmo-update-install.service

[Install]
WantedBy=multi-user.target
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
if ! systemctl is-enabled --quiet hejmo-update.path 2>/dev/null; then
	systemctl enable --now hejmo-update.path
fi

# An uploaded update file is checked offline with the cosign image (ticket 221);
# fetch it now, while the box has internet, so that it is there when it has not.
COSIGN_IMAGE="ghcr.io/sigstore/cosign/cosign:v2.5.3@sha256:f1946d0f30fc8e3777b02f2201e02efdba9fe38f4918162f937052fac98e083f"
if ! docker image inspect "$COSIGN_IMAGE" >/dev/null 2>&1; then
	docker pull "$COSIGN_IMAGE" >/dev/null 2>&1 || say "Could not fetch the signature checker; installing an uploaded update file needs it. Run this script again once the box has internet."
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
