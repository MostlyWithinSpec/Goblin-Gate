#!/bin/bash
# Builds goblin-gate-plugin-unifi_<version>_amd64.deb into $1 (default: dist/)
# from this directory: the layout every plugin package uses (docs/dev/plugins.md).
# RUN_BINARY: use this prebuilt program instead of building it with Go.
# VERSION: build it as part of a Goblin Gate release (same form as
# scripts/build-deb.sh): the package, the manifest's version and min_goblind
# all become that version, so the plugin needs a goblind at least as new as
# the one it was released with. Without it: plugin.json as it is.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
out="$(mkdir -p "${1:-dist}" && cd "${1:-dist}" && pwd)"
version=$(sed -n 's/^  "version": "\(.*\)",$/\1/p' "$here/plugin.json")
min=$(sed -n 's/^  "min_goblind": "\(.*\)",$/\1/p' "$here/plugin.json")
if [ -n "${VERSION:-}" ]; then
	version=$(echo "${VERSION#v}" | sed -E 's/-([0-9]+)-g([0-9a-f]+)$/+\1.g\2/; s/-/~/')
	min=$version
fi
root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT
install -d "$root/usr/share/goblin-gate/plugins/unifi" "$root/usr/lib/goblin-gate/plugins/unifi" "$root/DEBIAN"
sed -e "s/^  \"version\": \".*\",\$/  \"version\": \"$version\",/" \
	-e "s/^  \"min_goblind\": \".*\",\$/  \"min_goblind\": \"$min\",/" \
	"$here/plugin.json" > "$root/usr/share/goblin-gate/plugins/unifi/plugin.json"
chmod 644 "$root/usr/share/goblin-gate/plugins/unifi/plugin.json"
if [ -n "${RUN_BINARY:-}" ]; then
	install -m755 "$RUN_BINARY" "$root/usr/lib/goblin-gate/plugins/unifi/run"
else
	(cd "$here" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o "$root/usr/lib/goblin-gate/plugins/unifi/run" .)
fi
cat > "$root/DEBIAN/control" <<CONTROL
Package: goblin-gate-plugin-unifi
Version: $version
Architecture: amd64
Maintainer: Network Goblin Labs <goblin@localhost>
Depends: goblin-gate (>= $min)
Section: net
Priority: optional
Description: UniFi plugin for Goblin Gate
 Fills live address objects with the clients of UniFi networks and
 SSIDs, and shows which switch port or access point each device is on.
CONTROL
dpkg-deb --root-owner-group --build "$root" "$out/goblin-gate-plugin-unifi_${version}_amd64.deb" >/dev/null
echo "$out/goblin-gate-plugin-unifi_${version}_amd64.deb"
