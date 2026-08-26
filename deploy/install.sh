#!/usr/bin/env bash
# Install remotevibe on a Debian/Ubuntu VPS that already runs Docker and
# Tailscale. Run from a checkout of this repository, as root.
#
# This script has been written but not exercised end to end — read it before
# you run it.
set -euo pipefail

[[ $EUID -eq 0 ]] || { echo "run as root" >&2; exit 1; }
command -v docker >/dev/null || { echo "docker is required" >&2; exit 1; }
command -v tailscale >/dev/null || { echo "tailscale is required" >&2; exit 1; }

cd "$(dirname "$0")/.."

echo "==> building daemon"
make build
install -m 0755 bin/remotevibed /usr/local/bin/remotevibed

echo "==> building agent image"
make image

echo "==> creating service user"
id -u remotevibe >/dev/null 2>&1 || useradd --system --no-create-home --shell /usr/sbin/nologin remotevibe
usermod -aG docker remotevibe

echo "==> state directory"
install -d -o remotevibe -g docker -m 0700 /var/lib/remotevibe

echo "==> environment file"
if [[ ! -f /etc/remotevibe.env ]]; then
  install -m 0600 -o root -g root .env.example /etc/remotevibe.env
  echo "    edit /etc/remotevibe.env before starting the service"
fi

echo "==> systemd unit"
install -m 0644 deploy/remotevibed.service /etc/systemd/system/remotevibed.service
systemctl daemon-reload
systemctl enable remotevibed

cat <<'TXT'

Next:
  1. edit /etc/remotevibe.env       (GitHub PAT + agent auth)
  2. RV_ENV_FILE=/etc/remotevibe.env ./scripts/bootstrap-auth.sh
  3. RV_ENV_FILE=/etc/remotevibe.env ./scripts/verify-remote-control.sh
  4. systemctl start remotevibed
  5. tailscale serve --bg 8787
     tailscale serve status   # prints the https URL to open on your phone
TXT
