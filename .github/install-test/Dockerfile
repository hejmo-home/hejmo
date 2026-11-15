# A Raspberry Pi OS 64-bit stand-in for testing install.sh (ADR 0018, ticket
# #100): Debian bookworm (what Raspberry Pi OS 64-bit is built on) for arm64,
# with Raspberry Pi's own apt archive and systemd as PID 1. It is not the
# official Raspberry Pi OS image; the kernel and boot loader are the host's.
FROM debian:bookworm
ENV container=docker DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
 && apt-get install -y --no-install-recommends systemd systemd-sysv ca-certificates curl gnupg sudo iproute2 procps \
 && mkdir -p /etc/apt/keyrings \
 && curl -fsSL https://archive.raspberrypi.com/debian/raspberrypi.gpg.key | gpg --dearmor -o /etc/apt/keyrings/raspberrypi.gpg \
 && echo "deb [signed-by=/etc/apt/keyrings/raspberrypi.gpg] http://archive.raspberrypi.com/debian/ bookworm main" > /etc/apt/sources.list.d/raspi.list \
 && echo "Raspberry Pi reference 2025-05-06 (test stand-in)" > /etc/rpi-issue \
 && rm -rf /var/lib/apt/lists/*
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
