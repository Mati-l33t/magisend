#!/usr/bin/env bash
# Builds release archives with Docker:
#   scripts/build-release.sh v1.0.0            # this machine's architecture
#   scripts/build-release.sh v1.0.0 amd64 arm64
# Output: dist-release/magisend_<version>_linux_<arch>.tar.gz and SHA256SUMS.
set -euo pipefail

version=${1:?usage: scripts/build-release.sh vX.Y.Z [amd64|arm64 ...]}
[[ $version =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "version must look like v1.2.3" >&2; exit 1; }
shift
if [ $# -eq 0 ]; then
  case $(uname -m) in
    x86_64) set -- amd64 ;;
    aarch64 | arm64) set -- arm64 ;;
    *) echo "unsupported architecture $(uname -m)" >&2; exit 1 ;;
  esac
fi

root=$(cd "$(dirname "$0")/.." && pwd)
out="$root/dist-release"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir -p "$out"

for arch in "$@"; do
  name="magisend_${version#v}_linux_${arch}"
  docker buildx build --platform "linux/$arch" --target artifact \
    --build-arg "VERSION=$version" --output "type=local,dest=$work/bin-$arch" "$root"
  mkdir -p "$work/$name/deploy"
  install -m 0755 "$work/bin-$arch/magisend" "$work/$name/magisend"
  install -m 0644 "$root/LICENSE" "$root/README.md" "$root/CHANGELOG.md" "$work/$name/"
  install -m 0644 "$root"/deploy/*.service "$root"/deploy/*.path "$work/$name/deploy/"
  install -m 0755 "$root/deploy/update.sh" "$root/install.sh" "$work/$name/deploy/"
  tar --sort=name --owner=0 --group=0 --numeric-owner --mtime='@0' \
    -czf "$out/$name.tar.gz" -C "$work" "$name"
  echo "built $out/$name.tar.gz"
done
(cd "$out" && sha256sum magisend_*.tar.gz > SHA256SUMS)
