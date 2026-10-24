#!/usr/bin/env bash
# MagiSend updater. Installs the latest official release, verified by its
# signature and checksum.
#
# Started as root by magisend-update.service when an administrator presses
# Update, or run by hand: sudo /opt/magisend/update.sh
#
# Only a newer release whose SHA256SUMS carries a valid release signature is
# installed; the installed program checks the signature. Before switching
# versions the service is stopped, the database is copied and the local file
# store is snapshotted with hard links. If anything fails after the service
# was stopped, or the new version does not come up healthy, the previous
# program, database and local files are restored and started again. Files on
# added storage locations are not snapshotted: a new version only reads them
# while it starts.
#
# Everything root writes goes to a folder only root can write
# (/var/lib/magisend-update next to the data folder), never into the data
# folder the app itself can change.
set -Eeuo pipefail

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
DATA=${DATA%/}
REPO=$(setting MAGISEND_UPDATE_REPO Mati-l33t/magisend)
API=$(setting MAGISEND_UPDATE_API https://api.github.com)
DOWNLOADS=$(setting MAGISEND_RELEASE_DOWNLOADS "https://github.com/$REPO/releases/download")
export MAGISEND_LISTEN
MAGISEND_LISTEN=$(setting MAGISEND_LISTEN 0.0.0.0:8080)
STATE="$(dirname "$DATA")/magisend-update"
SNAPSHOT="$STATE/snapshot"
FROM=""
TO=""
STOPPED=0   # the service was stopped by this run
SNAPSHOT_DONE=0
INSTALLED=0 # the new program replaced the old one

clean() { printf '%s' "$1" | tr -cd 'A-Za-z0-9.+_-'; }
status() { # state message
  local tmp
  tmp=$(mktemp "$STATE/.status.XXXXXX")
  printf '{"state":"%s","from":"%s","to":"%s","message":"%s","time":"%s"}\n' \
    "$1" "$(clean "$FROM")" "$(clean "$TO")" "$2" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$tmp"
  chmod 0644 "$tmp"
  mv -f "$tmp" "$STATE/update-status.json"
  echo "==> $1: $2"
}
fail() { # before the service was stopped: nothing to undo
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

# Puts the previous version back after a failure once the service was
# stopped, and starts it again. Never exits early.
recover() { # reason
  set +e
  trap - ERR
  echo "==> $1, restoring ${FROM:-the previous version}" >&2
  systemctl stop "$SERVICE"
  if [ "$INSTALLED" = 1 ] && [ "$SNAPSHOT_DONE" = 1 ]; then
    install -m 0755 "$SNAPSHOT/magisend" "$APP_DIR/magisend"
    rm -f "$DATA"/magisend.sqlite*
    cp -a "$SNAPSHOT"/magisend.sqlite* "$DATA/"
    rm -rf "$DATA/blobs"
    mv "$SNAPSHOT/blobs" "$DATA/blobs"
  fi
  rm -rf "$SNAPSHOT"
  systemctl start "$SERVICE"
  if healthy "$FROM"; then
    status rolled_back "$1; the previous version was restored"
  else
    status failed "$1; the previous version did not start, see journalctl -u magisend"
  fi
  exit 1
}
unexpected() { # line
  if [ "$STOPPED" = 1 ]; then
    recover "Unexpected error at line $1"
  fi
  trap - ERR
  status failed "Unexpected error at line $1"
  exit 1
}

[ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }
[ -f "$APP_DIR/.magisend-release" ] || { echo "$APP_DIR is not a MagiSend release installation" >&2; exit 1; }
exec 9> /run/magisend-update.lock
flock -n 9 || { echo "another update is running" >&2; exit 0; }

# The working folder must be a real folder owned by root.
if [ -L "$STATE" ] || { [ -e "$STATE" ] && [ ! -d "$STATE" ]; }; then
  echo "$STATE is not a folder" >&2
  exit 1
fi
install -d -o root -g root -m 0755 "$STATE"
[ "$(stat -c %u "$STATE")" = 0 ] || { echo "$STATE is not owned by root" >&2; exit 1; }
trap 'unexpected $LINENO' ERR

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
# Only ever forwards: an older release, even a signed one, is not installed.
[ "$(printf '%s\n%s\n' "${FROM#v}" "${TO#v}" | sort -V | tail -n1)" = "${TO#v}" ] ||
  fail "The latest release $TO is older than the installed $FROM"

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
NAME="magisend_${TO#v}_linux_${ARCH}"
status running "Downloading $TO"
curl -fsSL --max-time 600 -o "$WORK/$NAME.tar.gz" "$DOWNLOADS/$TO/$NAME.tar.gz" || fail "Download failed"
curl -fsSL --max-time 60 -o "$WORK/SHA256SUMS" "$DOWNLOADS/$TO/SHA256SUMS" || fail "Checksum download failed"
curl -fsSL --max-time 60 -o "$WORK/SHA256SUMS.sig" "$DOWNLOADS/$TO/SHA256SUMS.sig" || fail "Signature download failed"
"$APP_DIR/magisend" --verify-release "$WORK/SHA256SUMS" "$WORK/SHA256SUMS.sig" || fail "The release signature is not valid"
(cd "$WORK" && grep -E "  $NAME\.tar\.gz$" SHA256SUMS | sha256sum -c --status) || fail "Checksum verification failed"
tar -xzf "$WORK/$NAME.tar.gz" -C "$WORK" || fail "The release archive could not be unpacked"
[ "$("$WORK/$NAME/magisend" --version)" = "$TO" ] || fail "Downloaded program reports the wrong version"

status running "Backing up"
install -d -m 0700 "$BACKUP_DIR"
STOPPED=1
systemctl stop "$SERVICE"
rm -rf "$SNAPSHOT"
mkdir -m 0700 "$SNAPSHOT"
# Hard links take no time or space but need the same file system.
link=-l
[ "$(stat -c %d "$DATA")" = "$(stat -c %d "$STATE")" ] || link=""
if ! { cp -a "$DATA"/magisend.sqlite* "$SNAPSHOT/" &&
  cp -a $link "$DATA/blobs" "$SNAPSHOT/blobs" &&
  cp -a "$APP_DIR/magisend" "$SNAPSHOT/magisend" &&
  tar -czf "$BACKUP_DIR/magisend-db-$(clean "$FROM")-$(date -u +%Y%m%dT%H%M%SZ).tar.gz" -C "$SNAPSHOT" \
    --exclude=blobs --exclude=magisend .; }; then
  recover "Backup failed, nothing was changed"
fi
SNAPSHOT_DONE=1
ls -1t "$BACKUP_DIR"/magisend-db-*.tar.gz 2> /dev/null | tail -n +$((KEEP_BACKUPS + 1)) | xargs -r rm -f || true

status running "Installing $TO"
install -m 0755 "$WORK/$NAME/magisend" "$APP_DIR/magisend.new"
mv -f "$APP_DIR/magisend.new" "$APP_DIR/magisend"
INSTALLED=1
systemctl start "$SERVICE" || true
healthy "$TO" || recover "$TO did not start correctly"

# Only a release that runs takes over the updater and service files.
install -m 0755 "$WORK/$NAME/deploy/update.sh" "$APP_DIR/update.sh.new"
mv -f "$APP_DIR/update.sh.new" "$APP_DIR/update.sh"
for unit in magisend.service magisend-update.service magisend-update.path; do
  sed "s#/var/lib/magisend#$DATA#g" "$WORK/$NAME/deploy/$unit" > "/etc/systemd/system/$unit"
done
systemctl daemon-reload
echo "$TO" > "$APP_DIR/.magisend-release"
rm -rf "$SNAPSHOT"
trap - ERR
status success "Updated to $TO"
