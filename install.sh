#!/usr/bin/env bash
# Hejmo install script for Raspberry Pi OS (64-bit) and other Debian-based
# systems (ADR 0018). Installs Docker if missing, writes the box's Compose
# file and a systemd unit under /opt/hejmo, and starts the box.
#
#   curl -fsSL https://raw.githubusercontent.com/hejmo-home/hejmo/main/install.sh | sudo bash
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
YAML

if [ ! -e "$DIR/.env" ]; then
	write_if_changed "$DIR/.env" 0644 <<ENV || true
HEJMO_IMAGE=$IMAGE
HEJMO_HTTP_PORT=80
HEJMO_HTTPS_PORT=443
ENV
fi

unit_changed=0
write_if_changed "$UNIT" 0644 <<'UNIT' && unit_changed=1
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

if [ "$unit_changed" = 1 ]; then
	systemctl daemon-reload
fi
if ! systemctl is-enabled --quiet hejmo.service 2>/dev/null; then
	systemctl enable hejmo.service
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
