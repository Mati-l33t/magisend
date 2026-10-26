#!/usr/bin/env bash
# Runs deploy/update.sh through a whole update in a sandbox: systemctl, curl
# and sleep are stand-ins, the release is a fake one and the installed program
# a script. It checks that a newer release is installed and the service
# stopped and started by its name, and that a release which does not come up
# is rolled back. Needs root, as the updater does: sudo scripts/test-update.sh
set -Eeuo pipefail

[ "$(id -u)" -eq 0 ] || { echo "run as root" >&2; exit 1; }
root=$(cd "$(dirname "$0")/.." && pwd)
# The updater refuses a working folder below one others can write, so the
# sandbox is not under /tmp.
sandbox=$(mktemp -d -p /root magisend-update-test.XXXXXX)
trap 'rm -rf "$sandbox"' EXIT
chmod 0755 "$sandbox"

fake_program() { # path version healthy(0/1)
  cat > "$1" << EOF
#!/usr/bin/env bash
case "\${1:-}" in
  --version) echo $2 ;;
  --verify-release) exit 0 ;;
  --healthcheck) exit $((1 - $3)) ;;
esac
EOF
  chmod 0755 "$1"
}

run_case() { # name new-release-healthy(0/1) expected-state expected-version
  local case=$sandbox/$1 bin release app
  bin=$case/bin
  release=$case/release/v1.0.99
  app=$case/app
  mkdir -p "$bin" "$release" "$app" "$case/data/blobs" "$case/units" "$case/backups"
  echo "database" > "$case/data/magisend.sqlite"
  fake_program "$app/magisend" v1.0.98 1
  echo v1.0.98 > "$app/.magisend-release"

  local name=magisend_1.0.99_linux_amd64
  [ "$(uname -m)" = aarch64 ] && name=magisend_1.0.99_linux_arm64
  mkdir -p "$case/build/$name/deploy"
  fake_program "$case/build/$name/magisend" v1.0.99 "$2"
  cp "$root"/deploy/update.sh "$root"/deploy/*.service "$root"/deploy/*.path "$case/build/$name/deploy/"
  tar -czf "$release/$name.tar.gz" -C "$case/build" "$name"
  (cd "$release" && sha256sum "$name.tar.gz" > SHA256SUMS && echo sig > SHA256SUMS.sig)

  # systemctl keeps the service's state in a file and logs what it was asked.
  cat > "$bin/systemctl" << EOF
#!/usr/bin/env bash
echo "\$*" >> "$case/systemctl.log"
case "\$1" in
  stop) [ "\$2" = magisend ] || exit 5; rm -f "$case/running" ;;
  start) [ "\$2" = magisend ] || exit 5; "$app/magisend" --version > "$case/running" ;;
  is-active) [ "\${*: -1}" = magisend ] && [ -f "$case/running" ] ;;
  show) [ "\${*: -1}" = magisend ] && echo "Environment=MAGISEND_DATA=$case/data" ; exit 0 ;;
esac
exit 0
EOF
  # curl answers the release questions from the fake release, and the status
  # check from the running version.
  cat > "$bin/curl" << EOF
#!/usr/bin/env bash
out="" url=""
while [ \$# -gt 0 ]; do
  case "\$1" in
    -o) out=\$2; shift ;;
    -H | --max-time) shift ;;
    -*) ;;
    *) url=\$1 ;;
  esac
  shift
done
case "\$url" in
  */releases/latest) echo '{"tag_name": "v1.0.99"}' ;;
  */api/status) [ -f "$case/running" ] && [ "\$("$app/magisend" --healthcheck; echo \$?)" = 0 ] &&
    echo "{\"version\":\"\$(cat "$case/running")\"}" ;;
  */v1.0.99/*) cp "$release/\${url##*/}" "\$out" ;;
  *) exit 22 ;;
esac
EOF
  printf '#!/bin/sh\nexit 0\n' > "$bin/sleep"
  chmod 0755 "$bin"/*

  sed -e "s#^APP_DIR=.*#APP_DIR=$app#" \
    -e "s#^ENV_FILE=.*#ENV_FILE=$case/environment#" \
    -e "s#^BACKUP_DIR=.*#BACKUP_DIR=$case/backups#" \
    -e "s#^STATE=.*#STATE=$case/state#" \
    -e "s#/run/magisend-update.lock#$case/lock#" \
    -e "s#/etc/systemd/system/#$case/units/#" \
    "$root/deploy/update.sh" > "$app/update.sh"
  chmod 0755 "$app/update.sh"
  "$app/magisend" --version > "$case/running"

  local code=0
  env -u INVOCATION_ID PATH="$bin:$PATH" "$app/update.sh" > "$case/output" 2>&1 || code=$?
  local state version
  state=$(grep -o '"state":"[^"]*"' "$case/state/update-status.json" | cut -d'"' -f4)
  version=$("$app/magisend" --version)
  if [ "$state" != "$3" ] || [ "$version" != "$4" ] || ! grep -qx "stop magisend" "$case/systemctl.log" ||
    ! grep -qx "start magisend" "$case/systemctl.log" || [ ! -f "$case/running" ]; then
    echo "FAIL $1: exit $code, state $state, version $version" >&2
    cat "$case/output" "$case/systemctl.log" >&2
    exit 1
  fi
  echo "ok   $1 ($state, $version)"
}

run_case update 1 success v1.0.99
run_case rollback 0 rolled_back v1.0.98
