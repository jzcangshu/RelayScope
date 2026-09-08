#!/usr/bin/env bash
# Sync git proxy from Windows system proxy settings (Internet Options / registry).
# Read-only and safe: never blocks git operations. Run manually or via pre-push hook.
#
# Behavior:
#   - If system proxy is enabled, set git http.proxy / https.proxy to it (with http:// scheme).
#   - If disabled or unset, clear git local proxy config so git falls back to direct.
#   - ProxyServer can be "host:port" or "http=h:p;https=h:p;socks=h:p"; we pick the first host:port.
set -uo pipefail

REG_KEY='HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings'

read_proxy_value() {
  reg query "$REG_KEY" //v "$1" 2>/dev/null | grep -i "$1" | awk '{print $NF}' | tr -d '\r'
}

proxy_server=$(read_proxy_value ProxyServer)
proxy_enable=$(read_proxy_value ProxyEnable)

clear_git_proxy() {
  git config --local --unset http.proxy 2>/dev/null || true
  git config --local --unset https.proxy 2>/dev/null || true
}

# Disabled or missing -> go direct
if [[ -z "$proxy_enable" || "$proxy_enable" == "0x0" || -z "$proxy_server" ]]; then
  clear_git_proxy
  exit 0
fi

# Take the first host:port segment before any ';'
first="${proxy_server%%;*}"
# If it looks like "proto=host:port", strip the proto= prefix
host_port="${first##*=}"
# If somehow empty, fall back to the whole thing
[[ -z "$host_port" ]] && host_port="$first"

# Skip socks entries (git http.proxy doesn't support socks via http://)
if [[ "$host_port" == socks* || "$first" == socks* ]]; then
  # Try to find an http= or https= entry instead
  host_port=$(echo "$proxy_server" | tr ';' '\n' | grep -iE '^(http|https)=' | head -1 | cut -d= -f2)
  if [[ -z "$host_port" ]]; then
    clear_git_proxy
    exit 0
  fi
fi

proxy_url="http://${host_port}"
git config --local http.proxy "$proxy_url"
git config --local https.proxy "$proxy_url"
