#!/usr/bin/env bash
# MagiSend installer for Debian, Ubuntu and other systemd-based Linux systems.
#
#   curl -fsSL https://raw.githubusercontent.com/Mati-l33t/magisend/main/install.sh | sudo bash
#
# Optional settings (environment variables):
#   MAGISEND_PORT=8080                         port to listen on
#   MAGISEND_PUBLIC_URL=https://share.example.com   public address behind a reverse proxy
#   MAGISEND_TRUSTED_PROXIES=192.168.1.10      reverse proxy address(es)
#   MAGISEND_VERSION=v1.0.0                    install a specific release (default: latest)
#
# Running it again on an existing installation updates it to the latest release.
set -euo pipefail

REPO=${MAGISEND_UPDATE_REPO:-Mati-l33t/magisend}
API=${MAGISEND_UPDATE_API:-https://api.github.com}
DOWNLOADS=${MAGISEND_RELEASE_DOWNLOADS:-https://github.com/$REPO/releases/download}
VERSION=${MAGISEND_VERSION:-latest}
PORT=${MAGISEND_PORT:-8080}
PUBLIC_URL=${MAGISEND_PUBLIC_URL:-}
TRUSTED_PROXIES=${MAGISEND_TRUSTED_PROXIES:-}
APP_DIR=/opt/magisend
DATA=/var/lib/magisend
ENV_DIR=/etc/magisend

step() { echo -e "\033[1;33m==>\033[0m $1"; }
done_() { echo -e "\033[1;32m✓\033[0m $1"; }
die() {
  echo -e "\033[1;31mError:\033[0m $1" >&2
  exit 1
}

[ "$(id -u)" -eq 0 ] || die "run as root (for example: curl ... | sudo bash)"
command -v systemctl > /dev/null && [ -d /run/systemd/system ] || die "systemd is required"
[[ $PORT =~ ^[0-9]+$ ]] && [ "$PORT" -ge 1 ] && [ "$PORT" -le 65535 ] || die "invalid MAGISEND_PORT"
[ -z "$PUBLIC_URL" ] || [[ $PUBLIC_URL =~ ^https?://[A-Za-z0-9.-]+(:[0-9]+)?$ ]] ||
  die "MAGISEND_PUBLIC_URL must look like https://share.example.com (no path)"
[[ $TRUSTED_PROXIES =~ ^[0-9A-Fa-f:.,/\ ]*$ ]] || die "invalid MAGISEND_TRUSTED_PROXIES"
case $(uname -m) in
  x86_64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) die "unsupported architecture $(uname -m) (amd64 and arm64 are supported)" ;;
esac

if [ -f "$APP_DIR/.magisend-release" ]; then
  step "MagiSend is already installed, updating to the latest release"
  exec "$APP_DIR/update.sh"
fi
[ ! -e "$APP_DIR" ] || die "$APP_DIR exists but is not a MagiSend release installation; move it away first"

missing=()
for tool in curl tar sha256sum flock; do command -v "$tool" > /dev/null || missing+=("$tool"); done
if [ ${#missing[@]} -gt 0 ]; then
  command -v apt-get > /dev/null || die "please install: ${missing[*]}"
  step "Installing ${missing[*]}"
  DEBIAN_FRONTEND=noninteractive apt-get update -qq
  DEBIAN_FRONTEND=noninteractive apt-get install -y -qq curl ca-certificates tar coreutils util-linux > /dev/null
fi

if [ "$VERSION" = latest ]; then
  VERSION=$(curl -fsSL --max-time 30 -H 'Accept: application/vnd.github+json' "$API/repos/$REPO/releases/latest" |
    grep -o '"tag_name": *"[^"]*"' | head -n1 | sed 's/.*"\([^"]*\)"$/\1/') || die "could not reach GitHub"
fi
[[ $VERSION =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "unexpected release version '$VERSION'"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
NAME="magisend_${VERSION#v}_linux_${ARCH}"
step "Downloading MagiSend $VERSION ($ARCH)"
curl -fsSL --max-time 600 -o "$WORK/$NAME.tar.gz" "$DOWNLOADS/$VERSION/$NAME.tar.gz" || die "download failed"
curl -fsSL --max-time 60 -o "$WORK/SHA256SUMS" "$DOWNLOADS/$VERSION/SHA256SUMS" || die "checksum download failed"
(cd "$WORK" && grep -E "  $NAME\.tar\.gz$" SHA256SUMS | sha256sum -c --status) || die "checksum verification failed"
tar -xzf "$WORK/$NAME.tar.gz" -C "$WORK"
[ "$("$WORK/$NAME/magisend" --version)" = "$VERSION" ] || die "downloaded binary reports the wrong version"
done_ "Verified $NAME.tar.gz"

step "Installing"
id -u magisend > /dev/null 2>&1 || useradd --system --home-dir "$DATA" --no-create-home --shell /usr/sbin/nologin magisend
install -d -m 0755 "$APP_DIR"
install -d -o magisend -g magisend -m 0700 "$DATA"
install -m 0755 "$WORK/$NAME/magisend" "$APP_DIR/magisend"
install -m 0755 "$WORK/$NAME/deploy/update.sh" "$APP_DIR/update.sh"
install -m 0644 "$WORK/$NAME/LICENSE" "$WORK/$NAME/CHANGELOG.md" "$APP_DIR/"
echo "$VERSION" > "$APP_DIR/.magisend-release"
install -d -m 0755 "$ENV_DIR"
if [ ! -f "$ENV_DIR/environment" ]; then
  {
    echo "# MagiSend settings. Restart after changes: systemctl restart magisend"
    echo "MAGISEND_LISTEN=0.0.0.0:$PORT"
    echo "MAGISEND_PUBLIC_ORIGIN=$PUBLIC_URL"
    echo "MAGISEND_TRUSTED_PROXIES=$TRUSTED_PROXIES"
  } > "$ENV_DIR/environment"
  chmod 0640 "$ENV_DIR/environment"
  chgrp magisend "$ENV_DIR/environment"
fi
for unit in magisend.service magisend-update.service magisend-update.path; do
  install -m 0644 "$WORK/$NAME/deploy/$unit" "/etc/systemd/system/$unit"
done
systemctl daemon-reload
systemctl enable --now magisend.service magisend-update.path > /dev/null 2>&1

step "Starting"
for _ in $(seq 1 30); do
  MAGISEND_LISTEN="0.0.0.0:$PORT" "$APP_DIR/magisend" --healthcheck && break
  sleep 1
done
MAGISEND_LISTEN="0.0.0.0:$PORT" "$APP_DIR/magisend" --healthcheck || die "MagiSend did not start; see: journalctl -u magisend"

done_ "MagiSend $VERSION is running"
echo
if [ -n "$PUBLIC_URL" ]; then
  echo "  Public address:  $PUBLIC_URL"
fi
for ip in $(hostname -I 2> /dev/null); do
  [[ $ip == *:* ]] && continue
  echo "  Open:            http://$ip:$PORT"
done
echo
echo "  Create the administrator account in the browser now."
echo "  Settings: $ENV_DIR/environment   Logs: journalctl -u magisend"
