#!/usr/bin/env bash
# MagiSend updater. Installs the latest official release, verified by its
# signature and checksum.
#
# Started as root by magisend-update.service when an administrator presses
# Update, or run by hand: sudo /opt/magisend/update.sh
#
# Only a newer release whose SHA256SUMS carries a valid release signature is
# installed; the installed program checks the signature. Before switching
# versions the service is stopped and the database files are saved; the
# stored files are snapshotted with hard links when they are on the same file
# system as the working folder (they are never changed in place, so they are
# never copied). If anything fails after the service was stopped, it is
# stopped by a signal, or the new version does not come up healthy, the
# previous program and database are put back and started again. Files on
# added storage locations are not snapshotted: a new version only reads them
# while it starts.
#
# Root keeps its status and snapshot in /var/lib/magisend-update, a folder
# only root can write. It writes into the data folder only to put the saved
# database and file store back, after MagiSend was stopped, and only to those
# exact names, which it clears first.
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
STATE=/var/lib/magisend-update
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
  # A second signal must not run this again halfway: the snapshot is gone
  # once it is put back.
  trap "" TERM INT HUP
  echo "==> $1, restoring ${FROM:-the previous version}" >&2
  systemctl stop "$SERVICE"
  if [ "$INSTALLED" = 1 ] && [ "$SNAPSHOT_DONE" = 1 ] && [ -f "$SNAPSHOT/magisend.sqlite" ]; then
    install -m 0755 "$SNAPSHOT/magisend" "$APP_DIR/magisend"
    # Only the exact files that were saved go back. Whatever now has their
    # names (a link or a folder the app made, say) is removed first, so root
    # never writes through anything in the data folder.
    for f in magisend.sqlite magisend.sqlite-wal magisend.sqlite-shm; do
      rm -rf -- "${DATA:?}/$f"
      [ -f "$SNAPSHOT/$f" ] && cp -p -- "$SNAPSHOT/$f" "$DATA/$f"
    done
    if [ -d "$SNAPSHOT/blobs" ]; then
      rm -rf -- "${DATA:?}/blobs"
      mv -- "$SNAPSHOT/blobs" "$DATA/blobs"
    fi
  fi
  INSTALLED=0
  SNAPSHOT_DONE=0
  rm -rf "$SNAPSHOT"
  systemctl start "$SERVICE"
  if healthy "$FROM"; then
    status rolled_back "$1; the previous version was restored"
  else
    status failed "$1; the previous version did not start, see journalctl -u magisend"
  fi
  exit 1
}
unexpected() { # line, or "signal"
  local reason="Unexpected error at line $1"
  [ "$1" = signal ] && reason="The update was stopped"
  trap "" TERM INT HUP
  if [ "$STOPPED" = 1 ]; then
    recover "$reason"
  fi
  trap - ERR
  status failed "$reason"
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
# Every folder above it must be root's alone, or the app could move it.
for dir in "$(dirname "$STATE")" "$(dirname "$(dirname "$STATE")")" /; do
  [ "$(stat -c %u "$dir")" = 0 ] && [ $(($(stat -c %a "$dir" | sed 's/^.*\(..\)$/0\1/') & 022)) = 0 ] ||
    { echo "$dir must be owned by root and writable only by root" >&2; exit 1; }
done
trap 'unexpected $LINENO' ERR
# A stop or kill signal (systemd's timeout, say) also puts the old version back.
trap 'unexpected signal' TERM INT HUP

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
# The database files are saved only if they are plain files. The stored
# files are snapshotted with hard links when they share a file system with
# the working folder; otherwise they are left as they are (stored files are
# never changed in place, so the database is what matters), never copied.
save_database() {
  local f
  for f in magisend.sqlite magisend.sqlite-wal magisend.sqlite-shm; do
    if [ -L "$DATA/$f" ] || { [ -e "$DATA/$f" ] && [ ! -f "$DATA/$f" ]; }; then
      return 1
    fi
    if [ -f "$DATA/$f" ]; then
      cp -p -- "$DATA/$f" "$SNAPSHOT/$f" || return 1
    fi
  done
  [ -f "$SNAPSHOT/magisend.sqlite" ]
}
snapshot_files() {
  # A file store moved elsewhere behind a link is left as it is, like one on
  # another file system.
  [ -L "$DATA/blobs" ] && return 0
  [ -e "$DATA/blobs" ] || return 0
  [ -d "$DATA/blobs" ] || return 1
  [ "$(stat -c %d "$DATA/blobs")" = "$(stat -c %d "$STATE")" ] || return 0
  cp -al -- "$DATA/blobs" "$SNAPSHOT/blobs"
}
if ! { save_database &&
  snapshot_files &&
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
# From here on the new version stays: a signal must not roll it back.
trap "" TERM INT HUP
trap - ERR
rm -rf "$SNAPSHOT"
status success "Updated to $TO"
