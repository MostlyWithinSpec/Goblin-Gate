#!/bin/bash
# Adds packages to Goblin Gate's APT repository and re-signs it (task 051).
# Usage: apt-repo.sh <repo dir> <channel> <deb>...
#
# Layout (served by GitHub Pages at .../Goblin-Gate/apt):
#   pool/<channel>/*.deb                   every version stays (rollback needs the old one)
#   dists/<channel>/main/binary-amd64/Packages(.gz)
#   dists/<channel>/Release, InRelease, Release.gpg
#   goblin-gate-archive.gpg / .asc         the public key, for people who add the repo by hand
# The signing key comes from $APT_SIGNING_KEY (armored private key) and is
# imported into a throwaway GNUPGHOME. Needs apt-utils (apt-ftparchive) and gnupg.
set -euo pipefail
REPO=$1 CHANNEL=$2
shift 2
case "$CHANNEL" in stable | beta | nightly) ;; *) echo "unknown channel $CHANNEL" >&2; exit 2 ;; esac
[ -n "${APT_SIGNING_KEY:-}" ] || { echo "APT_SIGNING_KEY is not set" >&2; exit 2; }

mkdir -p "$REPO/pool/$CHANNEL" "$REPO/dists/$CHANNEL/main/binary-amd64"
for deb in "$@"; do cp -f "$deb" "$REPO/pool/$CHANNEL/"; done
# nightly keeps only the newest 10 builds
if [ "$CHANNEL" = nightly ]; then
	ls -1t "$REPO/pool/nightly"/*.deb 2>/dev/null | tail -n +11 | xargs -r rm -f
fi

cd "$REPO"
apt-ftparchive packages "pool/$CHANNEL" > "dists/$CHANNEL/main/binary-amd64/Packages"
gzip -9 -k -f "dists/$CHANNEL/main/binary-amd64/Packages"
# the old Release and its signatures would be listed in the new Release
rm -f "dists/$CHANNEL/Release" "dists/$CHANNEL/InRelease" "dists/$CHANNEL/Release.gpg"
apt-ftparchive \
	-o APT::FTPArchive::Release::Origin="Goblin Gate" \
	-o APT::FTPArchive::Release::Label="Goblin Gate" \
	-o APT::FTPArchive::Release::Suite="$CHANNEL" \
	-o APT::FTPArchive::Release::Codename="$CHANNEL" \
	-o APT::FTPArchive::Release::Architectures="amd64" \
	-o APT::FTPArchive::Release::Components="main" \
	-o APT::FTPArchive::Release::Description="Goblin Gate packages ($CHANNEL channel)" \
	release "dists/$CHANNEL" > "Release.tmp" # not inside dists/: it would list itself
mv "Release.tmp" "dists/$CHANNEL/Release"

GNUPGHOME=$(mktemp -d)
export GNUPGHOME
trap 'rm -rf "$GNUPGHOME"' EXIT
printf '%s\n' "$APT_SIGNING_KEY" | gpg --batch --quiet --import
KEY=$(gpg --batch --list-secret-keys --with-colons | awk -F: '/^fpr/ {print $10; exit}')
gpg --batch --yes --quiet --default-key "$KEY" --clearsign -o "dists/$CHANNEL/InRelease" "dists/$CHANNEL/Release"
gpg --batch --yes --quiet --default-key "$KEY" --armor --detach-sign -o "dists/$CHANNEL/Release.gpg" "dists/$CHANNEL/Release"
gpg --batch --export "$KEY" > goblin-gate-archive.gpg
gpg --batch --armor --export "$KEY" > goblin-gate-archive.asc
echo "channel $CHANNEL: $(grep -c '^Package:' "dists/$CHANNEL/main/binary-amd64/Packages") package version(s), signed by $KEY"
