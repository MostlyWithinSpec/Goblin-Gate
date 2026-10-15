#!/bin/bash
# Builds dist/goblin-gate_<version>_amd64.deb (task 038). Runs on Linux
# with Go and dpkg-deb; the web UI must already be built in web/dist
# (scripts/build-deb.ps1 does both on Windows via Docker).
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION=${VERSION:-$(git describe --tags --always 2>/dev/null | sed 's/^v//' || echo 0.0.0)}
case "$VERSION" in [0-9]*.[0-9]*) ;; *) VERSION="0.0.0+g$VERSION" ;; esac
# Debian ordering: a pre-release must sort before its release (0.1.0~alpha.1
# < 0.1.0), and builds after a tag after it (git describe "-3-gabc" -> "+3.gabc").
VERSION=$(echo "$VERSION" | sed -E 's/-([0-9]+)-g([0-9a-f]+)$/+\1.g\2/; s/-/~/')
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
# APT keyrings for the sources goblind renders (task 051): ours, and Tailscale's
install -d "$ROOT/usr/share/keyrings"
# ARCHIVE_KEYRING: a test key (scripts/vm-update-test.ps1); releases ship ours
install -m644 "${ARCHIVE_KEYRING:-packaging/keys/goblin-gate-archive.gpg}" "$ROOT/usr/share/keyrings/goblin-gate-archive.gpg"
curl -fsSL https://pkgs.tailscale.com/stable/debian/bookworm.noarmor.gpg -o "$ROOT/usr/share/keyrings/tailscale-archive-keyring.gpg"
install -d "$ROOT/etc/update-motd.d"
install -m755 packaging/motd/05-goblin-gate "$ROOT/etc/update-motd.d/05-goblin-gate"
if [ -f packaging/blockpage/index.html ]; then
	install -m644 packaging/blockpage/index.html "$ROOT/usr/share/goblin-gate/blockpage.html"
fi
install -d "$ROOT/lib/systemd/system" "$ROOT/etc/systemd/system/systemd-networkd-wait-online.service.d" "$ROOT/etc/systemd/system/nftables.service.d" "$ROOT/etc/systemd/system/getty@tty1.service.d"
install -m644 packaging/systemd/goblind.service packaging/systemd/goblin-suricata@.service "$ROOT/lib/systemd/system/"
install -m644 packaging/systemd/systemd-networkd-wait-online.service.d/goblin.conf "$ROOT/etc/systemd/system/systemd-networkd-wait-online.service.d/"
install -m644 packaging/systemd/nftables.service.d/goblin.conf "$ROOT/etc/systemd/system/nftables.service.d/"
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

OUT_DIR=${OUT_DIR:-dist}
mkdir -p "$OUT_DIR"
dpkg-deb --root-owner-group -Zxz --build "$ROOT" "$OUT_DIR/goblin-gate_${VERSION}_amd64.deb"
echo "built $OUT_DIR/goblin-gate_${VERSION}_amd64.deb"
