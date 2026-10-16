#!/usr/bin/env bash
#
# reauth.sh — self-service reset of the cainban OAuth grant cached by kiro-cli.
#
# Why this exists: kiro-cli caches the cainban OAuth grant under
# ~/.aws/sso/cache/ as a <hash>.{token,registration,metadata}.json triple. When
# the cached access token expires and the client fails to refresh, the grant
# goes stale: cainban tool calls 401, and because a grant still EXISTS the client
# does not re-trigger the interactive consent flow — the dashboard just shows
# "Not verified" with no banner. The documented recovery is to remove the stale
# grant so the next fresh chat is forced to re-authorize. This script does that
# SAFELY: it matches ONLY cainban's grant by its issuer host and never touches
# any other server's cache (and never does a blanket wipe).
#
# Usage:
#   infra/scripts/reauth.sh                 # eu-central-1 cainban (default)
#   CAINBAN_ISSUER_HOST=<host> infra/scripts/reauth.sh   # override env
#
# After it runs: start a NEW chat and call a cainban tool; kiro-cli finds no
# grant, hits the 401, and begins the OAuth flow (consent link / loopback).
set -euo pipefail

# Default to the eu-central-1 cainban API Gateway host. Override for another env
# (e.g. the eu-north-1 deployment) via CAINBAN_ISSUER_HOST.
ISSUER_HOST="${CAINBAN_ISSUER_HOST:-o9ftud9cma.execute-api.eu-central-1.amazonaws.com}"
CACHE_DIR="${AWS_SSO_CACHE_DIR:-$HOME/.aws/sso/cache}"

if [[ ! -d "$CACHE_DIR" ]]; then
	echo "reauth: no cache dir at $CACHE_DIR — nothing to reset (authorize from a fresh chat)."
	exit 0
fi

# Find the cache hash whose metadata issuer matches ISSUER_HOST. Match on the
# non-secret metadata file only; never read token files.
found_hash=""
for meta in "$CACHE_DIR"/*.metadata.json; do
	[[ -e "$meta" ]] || continue
	if grep -q "$ISSUER_HOST" "$meta" 2>/dev/null; then
		found_hash="$(basename "$meta" .metadata.json)"
		break
	fi
done

if [[ -z "$found_hash" ]]; then
	echo "reauth: no cached grant for issuer '$ISSUER_HOST' — already clean."
	echo "        Start a fresh chat and call a cainban tool to authorize."
	exit 0
fi

echo "reauth: removing stale cainban grant for '$ISSUER_HOST' (hash ${found_hash:0:12}…)"
removed=0
for ext in token registration metadata; do
	f="$CACHE_DIR/$found_hash.$ext.json"
	if [[ -e "$f" ]]; then
		rm -f "$f"
		echo "  removed $found_hash.$ext.json"
		removed=$((removed + 1))
	fi
done

echo "reauth: removed $removed file(s). Now start a NEW chat and call a cainban"
echo "        tool (e.g. list the cainban tasks) to re-authorize."
