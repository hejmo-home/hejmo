#!/usr/bin/env bash
# Acceptance test of install.sh in a Raspberry Pi OS 64-bit stand-in (ticket
# #100), run by CI and by hand: ./.github/install-test/run.sh
# Only Docker containers are created; nothing on the host changes. The box image
# is built here and handed to the stand-in through a throw-away local registry.
set -euo pipefail
cd "$(dirname "$0")/../.."

net=hejmo-install-test
pi=hejmo-pios
reg=hejmo-registry
vol=hejmo-pios-docker
cleanup() {
	docker rm -f "$pi" "$reg" >/dev/null 2>&1 || true
	docker network rm "$net" >/dev/null 2>&1 || true
	docker volume rm "$vol" "$vol-containerd" >/dev/null 2>&1 || true
}
trap cleanup EXIT
cleanup

fail() { echo "FAIL: $*" >&2; docker exec "$pi" journalctl -u hejmo --no-pager -n 50 2>&1 || true; exit 1; }
in_pi() { docker exec "$pi" bash -c "$*"; }

echo "==> Building the stand-in and the box image"
docker build -q -t hejmo-pios -f .github/install-test/Dockerfile .github/install-test
docker build -q -t hejmo:ci .

echo "==> Local registry for the box image"
docker network create "$net" >/dev/null
docker run -d --name "$reg" --network "$net" --network-alias registry -p 127.0.0.1:5000:5000 registry:2 >/dev/null
for _ in $(seq 1 30); do curl -fsS http://localhost:5000/v2/ >/dev/null 2>&1 && break; sleep 1; done
# Docker treats localhost registries as insecure, so the host can push without setup.
docker tag hejmo:ci localhost:5000/hejmo:ci
docker push -q localhost:5000/hejmo:ci >/dev/null

echo "==> Starting the Raspberry Pi OS stand-in"
docker run -d --name "$pi" --network "$net" --privileged --cgroupns=host \
	-v /sys/fs/cgroup:/sys/fs/cgroup:rw -v "$vol":/var/lib/docker -v "$vol-containerd":/var/lib/containerd \
	--tmpfs /run --tmpfs /run/lock hejmo-pios >/dev/null
for _ in $(seq 1 30); do
	state=$(docker exec "$pi" systemctl is-system-running 2>/dev/null || true)
	case "$state" in running | degraded) break ;; esac
	sleep 1
done
in_pi 'mkdir -p /etc/docker && echo "{\"insecure-registries\":[\"registry:5000\"]}" > /etc/docker/daemon.json'
docker cp install.sh "$pi":/root/install.sh

echo "==> First run"
in_pi 'HEJMO_IMAGE=registry:5000/hejmo:ci bash /root/install.sh' || fail "first run failed"
in_pi 'curl -fsS http://localhost/healthz' | grep -q '"status":"ok"' || fail "healthz not ok"
code=$(in_pi 'curl -s -o /dev/null -w "%{http_code}" -L -k http://localhost/') || true
[ "$code" = 200 ] || fail "setup wizard answered $code, want 200"
in_pi 'curl -sk -L http://localhost/ | grep -qi "https"' || fail "the first page is not the HTTPS step of the setup wizard"
in_pi 'systemctl is-enabled hejmo.service && systemctl is-active hejmo.service' || fail "hejmo.service is not enabled and active"

snapshot() {
	in_pi 'cd /opt/hejmo && sha256sum compose.yaml .env /etc/systemd/system/hejmo.service && stat -c "%Y %n" compose.yaml .env /etc/systemd/system/hejmo.service && docker inspect -f "{{.Id}} {{.State.StartedAt}} {{.Config.Image}}" hejmo'
}
before=$(snapshot)

echo "==> Second run (must change nothing)"
in_pi 'HEJMO_IMAGE=registry:5000/hejmo:ci bash /root/install.sh' || fail "second run failed"
after=$(snapshot)
[ "$before" = "$after" ] || { diff <(echo "$before") <(echo "$after") || true; fail "second run changed something"; }

echo "OK: install.sh works on the Raspberry Pi OS stand-in and is idempotent"
