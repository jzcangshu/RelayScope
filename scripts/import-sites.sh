#!/usr/bin/env bash
# Import a site catalog into a running RelayScope server via the admin API.
#
# Usage:
#   RELAYSCOPE_ADMIN_PASSWORD=... scripts/import-sites.sh [sites.json] [base-url]
#
# Arguments:
#   sites.json  Catalog in the sites.example.json schema (default: sites.production.json)
#   base-url    RelayScope origin (default: http://127.0.0.1:8080; pass the public
#               URL when importing from outside the server)
#
# Requires bash, curl and jq (apt-get install jq). Catalog files in this
# repository hold site configuration only — names, URLs, adapter settings —
# and never credentials; sessions are imported separately through the admin
# UI after logging in. Keep credentials out of this repository.
#
# Note: the admin API creates sites unconditionally, so re-running the import
# duplicates entries. Run it once per fresh database (e.g. a new server).
set -euo pipefail

FILE="${1:-sites.production.json}"
BASE="${2:-http://127.0.0.1:8080}"
: "${RELAYSCOPE_ADMIN_PASSWORD:?RELAYSCOPE_ADMIN_PASSWORD must be set}"

command -v jq >/dev/null 2>&1 || { echo "jq is required (apt-get install jq)" >&2; exit 1; }
command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit 1; }
[ -f "$FILE" ] || { echo "site catalog not found: $FILE" >&2; exit 1; }

COOKIE_JAR="$(mktemp)"
RESP="$(mktemp)"
trap 'rm -f "$COOKIE_JAR" "$RESP"' EXIT

curl -fsS -c "$COOKIE_JAR" -H "Content-Type: application/json" \
  -d "$(jq -n --arg password "$RELAYSCOPE_ADMIN_PASSWORD" '{password: $password}')" \
  "$BASE/api/v1/admin/login" >/dev/null

CSRF="$(awk '$6 == "relayscope_csrf" { print $7 }' "$COOKIE_JAR")"
[ -n "$CSRF" ] || { echo "login succeeded but no CSRF token was issued" >&2; exit 1; }

created=0
failed=0
while IFS= read -r payload; do
  code="$(curl -s -o "$RESP" -w '%{http_code}' \
    -b "$COOKIE_JAR" -H "X-CSRF-Token: $CSRF" -H "Content-Type: application/json" \
    -d "$payload" "$BASE/api/v1/admin/sites")" || code="curl-error"
  case "$code" in
    2*) created=$((created + 1)) ;;
    *)
      failed=$((failed + 1))
      echo "FAIL [$code] $(head -c 200 "$RESP")" >&2
      ;;
  esac
done < <(jq -c '.[] | {
  name,
  baseUrl: .base_url,
  sourceUrl: .source_url,
  adapterKey: .adapter,
  adapterConfig: (.adapter_config | tostring),
  sessionRequired: (.session_required // false),
  enabled: true,
  intervalSeconds: 900,
  jitterSeconds: 120
}' "$FILE")

echo "imported=$created failed=$failed ($FILE -> $BASE)"
[ "$failed" -eq 0 ]
