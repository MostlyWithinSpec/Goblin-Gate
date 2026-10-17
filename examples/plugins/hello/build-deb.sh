#!/bin/bash
# Builds goblin-gate-plugin-hello_<version>_amd64.deb into $1 (default: dist/)
# from this directory: the layout every plugin package uses (docs/dev/plugins.md).
# RUN_BINARY: use this prebuilt program instead of building it with Go.
set -euo pipefail
here="$(cd "$(dirname "$0")" && pwd)"
out="$(mkdir -p "${1:-dist}" && cd "${1:-dist}" && pwd)"
version=$(sed -n 's/^  "version": "\(.*\)",$/\1/p' "$here/plugin.json")
root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT
install -d "$root/usr/share/goblin-gate/plugins/hello/ui" "$root/usr/lib/goblin-gate/plugins/hello" "$root/DEBIAN"
install -m644 "$here/plugin.json" "$root/usr/share/goblin-gate/plugins/hello/plugin.json"
install -m644 "$here"/ui/* "$root/usr/share/goblin-gate/plugins/hello/ui/"
if [ -n "${RUN_BINARY:-}" ]; then
	install -m755 "$RUN_BINARY" "$root/usr/lib/goblin-gate/plugins/hello/run"
else
	(cd "$here" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o "$root/usr/lib/goblin-gate/plugins/hello/run" .)
fi
cat > "$root/DEBIAN/control" <<CONTROL
Package: goblin-gate-plugin-hello
Version: $version
Architecture: amd64
Maintainer: Network Goblin Labs <goblin@localhost>
Depends: goblin-gate (>= 0.1.0~alpha.4)
Section: net
Priority: optional
Description: Goblin Gate example plugin
 Reads events and says hello; shows how a plugin is packaged and sandboxed.
CONTROL
dpkg-deb --root-owner-group --build "$root" "$out/goblin-gate-plugin-hello_${version}_amd64.deb" >/dev/null
echo "$out/goblin-gate-plugin-hello_${version}_amd64.deb"
