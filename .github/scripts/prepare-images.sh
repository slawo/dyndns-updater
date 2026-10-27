#!/usr/bin/env bash
set -euo pipefail
version="${1:?release version required}"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.-]+)?$ ]]
(cd dist && sha256sum --check checksums.txt)
while IFS= read -r platform; do
  arch="${platform#linux/}"
  case "$arch" in
    arm/v7) archive_arch=armv7 ;;
    *) archive_arch="$arch" ;;
  esac
  destination=".bin/$platform"
  mkdir -p "$destination"
  tar -xzf "dist/dyndns-updater_${version}_linux_${archive_arch}.tar.gz" -C "$destination" dyndns-updater
  chmod 755 "$destination/dyndns-updater"
done < .github/image-platforms.txt
