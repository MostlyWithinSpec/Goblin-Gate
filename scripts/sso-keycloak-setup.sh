#!/bin/bash
# Sets up a throwaway Keycloak (container gg-keycloak, started by
# scripts/sso-e2e.ps1) for the single sign-on test: realm gg, confidential
# client "gate" with any redirect URL, group gg-admins with user alice, group
# gg-help with user sam, and a "groups" claim in the ID token.
set -euo pipefail
K=/opt/keycloak/bin/kcadm.sh
for i in $(seq 60); do
	$K config credentials --server http://localhost:8080 --realm master --user admin --password admin >/dev/null 2>&1 && break
	sleep 2
done
$K create realms -s realm=gg -s enabled=true >/dev/null
CID=$($K create clients -r gg -s clientId=gate -s enabled=true -s publicClient=false -s secret=gate-secret \
	-s standardFlowEnabled=true -s 'redirectUris=["*"]' -s directAccessGrantsEnabled=false -i)
$K create clients/$CID/protocol-mappers/models -r gg -s name=groups -s protocol=openid-connect \
	-s protocolMapper=oidc-group-membership-mapper \
	-s 'config."claim.name"=groups' -s 'config."full.path"=false' -s 'config."id.token.claim"=true' \
	-s 'config."access.token.claim"=true' -s 'config."userinfo.token.claim"=true' >/dev/null
for g in gg-admins gg-help; do $K create groups -r gg -s name=$g >/dev/null; done
mkuser() { # mkuser <name> <group>
	local uid gid
	uid=$($K create users -r gg -s username=$1 -s enabled=true -s email=$1@example.com -s emailVerified=true \
		-s firstName=$1 -s lastName=Test -i)
	$K set-password -r gg --username $1 --new-password "pw-$1"
	gid=$($K get groups -r gg -q search=$2 --fields id --format csv --noquotes | head -1)
	$K update users/$uid/groups/$gid -r gg -s realm=gg -s userId=$uid -s groupId=$gid -n
}
mkuser alice gg-admins
mkuser sam gg-help
echo "keycloak ready"
