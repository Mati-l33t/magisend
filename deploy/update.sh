#!/usr/bin/env bash
# MagiSend updater. Installs the latest official release, verified by checksum.
#
# Started as root by magisend-update.service when an administrator presses
# Update, or run by hand: sudo /opt/magisend/update.sh
#
# Before switching versions the service is stopped and the database is copied
# and the file store snapshotted with hard links (instant, no extra space).
# If the new version does not come up healthy, the previous binary, database
# and files are restored and the old version is started again.
set -euo pipefail

APP_DIR=/opt/magisend
ENV_FILE=/etc/magisend/environment
BACKUP_DIR=/var/backups/magisend
KEEP_BACKUPS=5
SERVICE=magisend

setting() { # value from the environment file, else from the service unit
  local value=""
  [ -r "$ENV_FILE" ] && value=$(grep -E "^$1=" "$ENV_FILE" | tail -n1 | cut -d= -f2- | tr -d "\"'" || true)
  if [ -z "$value" ]; then
    value=$(systemctl show -p Environment "$SERVICE" | tr ' ' '\n' | grep -E "^(Environment=)?$1=" | tail -n1 | cut -d= -f2- || true)
    value=${value#*=}
  fi
  printf '%s' "${value:-$2}"
}

DATA=$(setting MAGISEND_DATA /var/lib/magisend)
REPO=$(setting MAGISEND_UPDATE_REPO Mati-l33t/magisend)
API=$(setting MAGISEND_UPDATE_API https://api.github.com)
DOWNLOADS=$(setting MAGISEND_RELEASE_DOWNLOADS "https://github.com/$REPO/releases/download")
export MAGISEND_LISTEN
MAGISEND_LISTEN=$(setting MAGISEND_LISTEN 0.0.0.0:8080)
SNAPSHOT="$DATA/.update-backup"
FROM=""
TO=""

clean() { printf '%s' "$1" | tr -cd 'A-Za-z0-9.+_-'; }
status() { # state message
  local file="$DATA/update-status.json"
  printf '{"state":"%s","from":"%s","to":"%s","message":"%s","time":"%s"}\n' \
    "$1" "$(clean "$FROM")" "$(clean "$TO")" "$2" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$file.tmp"
  chmod 0644 "$file.tmp"
  mv -f "$file.tmp" "$file"
  echo "==> $1: $2"
}
fail() {
  status failed "$1"
  exit 1
}

healthy() { # wait until the service answers and runs the expected version
  local port=${MAGISEND_LISTEN##*:}
  for _ in $(seq 1 45); do
    if systemctl is-active --quiet "$SERVICE" && "$APP_DIR/magisend" --healthcheck &&
      curl -fsS --max-time 5 "http://127.0.0.1:$port/api/status" | grep -q "\"version\":\"$1\""; then
      return 0
    fi
    sleep 2
  done
  return 1
}

[ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }
[ -f "$APP_DIR/.magisend-release" ] || { echo "$APP_DIR is not a MagiSend release installation" >&2; exit 1; }
exec 9> /run/magisend-update.lock
flock -n 9 || { echo "another update is running" >&2; exit 0; }

case $(uname -m) in
  x86_64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) fail "Unsupported architecture" ;;
esac
FROM=$("$APP_DIR/magisend" --version)
status running "Checking for the latest release"

TO=$(curl -fsSL --max-time 30 -H 'Accept: application/vnd.github+json' "$API/repos/$REPO/releases/latest" |
  grep -o '"tag_name": *"[^"]*"' | head -n1 | sed 's/.*"\([^"]*\)"$/\1/') || fail "Could not reach the release server"
[[ $TO =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "Unexpected release version"
if [ "$TO" = "$FROM" ]; then
  status success "Already up to date"
  exit 0
fi

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
NAME="magisend_${TO#v}_linux_${ARCH}"
status running "Downloading $TO"
curl -fsSL --max-time 600 -o "$WORK/$NAME.tar.gz" "$DOWNLOADS/$TO/$NAME.tar.gz" || fail "Download failed"
curl -fsSL --max-time 60 -o "$WORK/SHA256SUMS" "$DOWNLOADS/$TO/SHA256SUMS" || fail "Checksum download failed"
(cd "$WORK" && grep -E "  $NAME\.tar\.gz$" SHA256SUMS | sha256sum -c --status) || fail "Checksum verification failed"
tar -xzf "$WORK/$NAME.tar.gz" -C "$WORK"
[ "$("$WORK/$NAME/magisend" --version)" = "$TO" ] || fail "Downloaded binary reports the wrong version"

status running "Backing up"
mkdir -p "$BACKUP_DIR"
chmod 0700 "$BACKUP_DIR"
systemctl stop "$SERVICE"
rm -rf "$SNAPSHOT"
mkdir -p "$SNAPSHOT"
if ! { cp -a "$DATA"/magisend.sqlite* "$SNAPSHOT/" &&
  cp -al "$DATA/blobs" "$SNAPSHOT/blobs" &&
  cp -a "$APP_DIR/magisend" "$SNAPSHOT/magisend" &&
  tar -czf "$BACKUP_DIR/magisend-db-$(clean "$FROM")-$(date -u +%Y%m%dT%H%M%SZ).tar.gz" -C "$SNAPSHOT" \
    --exclude=blobs --exclude=magisend .; }; then
  rm -rf "$SNAPSHOT"
  systemctl start "$SERVICE"
  fail "Backup failed, nothing was changed"
fi
ls -1t "$BACKUP_DIR"/magisend-db-*.tar.gz 2> /dev/null | tail -n +$((KEEP_BACKUPS + 1)) | xargs -r rm -f

status running "Installing $TO"
install -m 0755 "$WORK/$NAME/magisend" "$APP_DIR/magisend.new"
mv -f "$APP_DIR/magisend.new" "$APP_DIR/magisend"
systemctl start "$SERVICE" || true
if healthy "$TO"; then
  # Only a release that runs takes over the updater and service files.
  install -m 0755 "$WORK/$NAME/deploy/update.sh" "$APP_DIR/update.sh.new"
  mv -f "$APP_DIR/update.sh.new" "$APP_DIR/update.sh"
  for unit in magisend.service magisend-update.service magisend-update.path; do
    sed "s#/var/lib/magisend#$DATA#g" "$WORK/$NAME/deploy/$unit" > "/etc/systemd/system/$unit"
  done
  systemctl daemon-reload
  echo "$TO" > "$APP_DIR/.magisend-release"
  rm -rf "$SNAPSHOT"
  status success "Updated to $TO"
  exit 0
fi

echo "==> $TO did not start correctly, restoring $FROM" >&2
systemctl stop "$SERVICE" || true
install -m 0755 "$SNAPSHOT/magisend" "$APP_DIR/magisend"
rm -f "$DATA"/magisend.sqlite*
cp -a "$SNAPSHOT"/magisend.sqlite* "$DATA/"
rm -rf "$DATA/blobs"
mv "$SNAPSHOT/blobs" "$DATA/blobs"
rm -rf "$SNAPSHOT"
systemctl start "$SERVICE" || true
if healthy "$FROM"; then
  status rolled_back "The new version did not start, the previous version was restored"
else
  status failed "Rollback did not start, see journalctl -u magisend"
fi
exit 1
