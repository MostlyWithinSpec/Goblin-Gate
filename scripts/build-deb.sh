#!/bin/bash
# Builds dist/goblin-gate_<version>_amd64.deb (task 038). Runs on Linux
# with Go and dpkg-deb; the web UI must already be built in web/dist
# (scripts/build-deb.ps1 does both on Windows via Docker).
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION=${VERSION:-$(git describe --tags --always 2>/dev/null | sed 's/^v//' || echo 0.0.0)}
case "$VERSION" in [0-9]*.[0-9]*) ;; *) VERSION="0.0.0+g$VERSION" ;; esac
[ -f web/dist/index.html ] || { echo "web/dist missing: run npm run build in web/ first" >&2; exit 1; }

ROOT=$(mktemp -d)
trap 'rm -rf "$ROOT"' EXIT
LDFLAGS="-s -w -X github.com/networkgoblinlabs/goblin-gate/internal/version.Version=$VERSION"
export GOOS=linux GOARCH=amd64 CGO_ENABLED=0
install -d "$ROOT/usr/sbin" "$ROOT/usr/bin"
go build -trimpath -ldflags "$LDFLAGS" -o "$ROOT/usr/sbin/goblind" ./cmd/goblind
go build -trimpath -ldflags "$LDFLAGS" -o "$ROOT/usr/bin/goblin" ./cmd/goblin
go build -trimpath -ldflags "$LDFLAGS" -o "$ROOT/usr/sbin/goblin-console" ./cmd/goblin-console

install -d "$ROOT/usr/share/goblin-gate/web"
cp -r web/dist/. "$ROOT/usr/share/goblin-gate/web/"
install -m644 packaging/ulogd/ulogd.conf "$ROOT/usr/share/goblin-gate/ulogd.conf"
install -m644 cmd/goblin-console/logo.txt "$ROOT/usr/share/goblin-gate/logo.txt"
install -d "$ROOT/etc/update-motd.d"
install -m755 packaging/motd/05-goblin-gate "$ROOT/etc/update-motd.d/05-goblin-gate"
if [ -f packaging/blockpage/index.html ]; then
	install -m644 packaging/blockpage/index.html "$ROOT/usr/share/goblin-gate/blockpage.html"
fi
install -d "$ROOT/lib/systemd/system" "$ROOT/etc/systemd/system/systemd-networkd-wait-online.service.d" "$ROOT/etc/systemd/system/getty@tty1.service.d"
install -m644 packaging/systemd/goblind.service packaging/systemd/goblin-suricata@.service "$ROOT/lib/systemd/system/"
install -m644 packaging/systemd/systemd-networkd-wait-online.service.d/goblin.conf "$ROOT/etc/systemd/system/systemd-networkd-wait-online.service.d/"
install -m644 packaging/systemd/getty@tty1.service.d/goblin-console.conf "$ROOT/etc/systemd/system/getty@tty1.service.d/"
install -d -m750 "$ROOT/etc/goblingate"
install -m640 packaging/default/goblind.json "$ROOT/etc/goblingate/goblind.json"
install -d "$ROOT/usr/share/doc/goblin-gate"
install -m644 packaging/README.md "$ROOT/usr/share/doc/goblin-gate/README.md"

install -d "$ROOT/DEBIAN"
sed "s/@VERSION@/$VERSION/" packaging/deb/control.in > "$ROOT/DEBIAN/control"
echo "Installed-Size: $(du -sk "$ROOT" | cut -f1)" >> "$ROOT/DEBIAN/control"
install -m755 packaging/deb/postinst packaging/deb/prerm packaging/deb/postrm "$ROOT/DEBIAN/"
install -m644 packaging/deb/conffiles "$ROOT/DEBIAN/conffiles"

mkdir -p dist
dpkg-deb --root-owner-group -Zxz --build "$ROOT" "dist/goblin-gate_${VERSION}_amd64.deb"
echo "built dist/goblin-gate_${VERSION}_amd64.deb"
