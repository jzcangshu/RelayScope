#!/usr/bin/env bash
# Import model rules into a running RelayScope server via the admin API.
#
# Usage:
#   RELAYSCOPE_ADMIN_PASSWORD=... scripts/import-rules.sh [rules.json] [base-url]
#
# Arguments:
#   rules.json  Rule catalog (default: rules.production.json); entries use the
#               admin API rule schema (provider, canonicalName, requiredTerms,
#               anyTerms, excludedTerms, aliases, pattern, priority, enabled,
#               generated).
#   base-url    RelayScope origin (default: http://127.0.0.1:8080; pass the
#               public URL when importing from outside the server)
#
# Requires bash, curl and jq (apt-get install jq). Existing rules (matched by
# canonicalName) are skipped, so the script is safe to re-run. The upstream
# example seed rules (gpt-4o, gpt-4o-mini, deepseek-chat, claude-sonnet-4,
# gemini-pro) are disabled instead of deleted because EnsureInitialRules
# re-creates them at boot whenever their canonical names are absent.
set -euo pipefail

FILE="${1:-rules.production.json}"
BASE="${2:-http://127.0.0.1:8080}"
: "${RELAYSCOPE_ADMIN_PASSWORD:?RELAYSCOPE_ADMIN_PASSWORD must be set}"

command -v jq >/dev/null 2>&1 || { echo "jq is required (apt-get install jq)" >&2; exit 1; }
command -v curl >/dev/null 2>&1 || { echo "curl is required" >&2; exit 1; }
[ -f "$FILE" ] || { echo "rule catalog not found: $FILE" >&2; exit 1; }

COOKIE_JAR="$(mktemp)"
RESP="$(mktemp)"
trap 'rm -f "$COOKIE_JAR" "$RESP"' EXIT

curl -fsS -c "$COOKIE_JAR" -H "Content-Type: application/json" \
  -d "$(jq -n --arg password "$RELAYSCOPE_ADMIN_PASSWORD" '{password: $password}')" \
  "$BASE/api/v1/admin/login" >/dev/null

CSRF="$(awk '$6 == "relayscope_csrf" { print $7 }' "$COOKIE_JAR")"
[ -n "$CSRF" ] || { echo "login succeeded but no CSRF token was issued" >&2; exit 1; }

existing="$(curl -fsS -b "$COOKIE_JAR" "$BASE/api/v1/admin/rules")"

created=0
skipped=0
failed=0
while IFS= read -r rule; do
  name="$(jq -r '.canonicalName' <<<"$rule")"
  if jq -e --arg n "$name" 'any(.rules[]; .canonicalName == $n)' >/dev/null 2>&1 <<<"$existing"; then
    skipped=$((skipped + 1))
    continue
  fi
  code="$(curl -s -o "$RESP" -w '%{http_code}' \
    -b "$COOKIE_JAR" -H "X-CSRF-Token: $CSRF" -H "Content-Type: application/json" \
    -d "$rule" "$BASE/api/v1/admin/rules")" || code="curl-error"
  case "$code" in
    2*) created=$((created + 1)) ;;
    *)
      failed=$((failed + 1))
      echo "FAIL [$code] $name: $(head -c 200 "$RESP")" >&2
      ;;
  esac
done < <(jq -c '.[]' "$FILE")

disabled=0
for name in deepseek-chat gpt-4o gpt-4o-mini claude-sonnet-4 gemini-pro; do
  row="$(jq -c --arg n "$name" '.rules[] | select(.canonicalName == $n)' <<<"$existing")" || true
  [ -n "$row" ] || continue
  id="$(jq -r '.id' <<<"$row")"
  body="$(jq -c '.enabled = false' <<<"$row")"
  code="$(curl -s -o "$RESP" -w '%{http_code}' -X PUT \
    -b "$COOKIE_JAR" -H "X-CSRF-Token: $CSRF" -H "Content-Type: application/json" \
    -d "$body" "$BASE/api/v1/admin/rules/$id")" || code="curl-error"
  case "$code" in
    2*) disabled=$((disabled + 1)) ;;
    *) echo "WARN [$code] could not disable example rule $name" >&2 ;;
  esac
done

echo "rules imported=$created skipped=$skipped failed=$failed examples_disabled=$disabled ($FILE -> $BASE)"
