#!/bin/bash
# Builds what scripts/iso/update-test.sh needs (task 051), in a
# golang:1.26-bookworm container with the repo at /src and the work dir at
# /work/update: a throwaway signing key (key/), and versions 1-3 of the
# goblin-gate package that trust it (debs/). Version 3's goblind exits at once.
# Usage: update-test-build.sh <v1> <v2> <v3>   (Debian versions)
set -euo pipefail
W=/work/update
mkdir -p "$W/key" "$W/debs"
if [ ! -f "$W/key/test.asc" ]; then
	export GNUPGHOME=$(mktemp -d)
	gpg --batch --quiet --passphrase '' --quick-gen-key 'Goblin Gate update test <test@invalid>' ed25519 sign never
	gpg --batch --armor --export-secret-keys > "$W/key/test.asc"
	gpg --batch --export > "$W/key/test.gpg"
	echo "generated the test signing key"
fi
cd /src
for v in "$@"; do
	VERSION=$v OUT_DIR=$W/debs ARCHIVE_KEYRING=$W/key/test.gpg bash scripts/build-deb.sh
done
V3=$3
X=$(mktemp -d)
dpkg-deb -R "$W/debs/goblin-gate_${V3}_amd64.deb" "$X"
printf '#!/bin/sh\necho "goblind: broken test build" >&2\nexit 1\n' > "$X/usr/sbin/goblind"
chmod 755 "$X/usr/sbin/goblind"
dpkg-deb --root-owner-group -Zxz --build "$X" "$W/debs/goblin-gate_${V3}_amd64.deb" >/dev/null
echo "version $V3 has a broken goblind"
