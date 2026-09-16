#!/usr/bin/env bash
# Extract the API endpoint list from the Alta portal bundle and compare it with the
# pinned provenance in api/schema.json. Read-only: fetches public assets, nothing else.
set -euo pipefail

portal="${ALTA_PORTAL_URL:-https://manage.alta.inc}"
schema="$(dirname "$0")/../api/schema.json"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

bundle=$(curl -fsS "$portal/" | grep -oE '/static/js/main\.[0-9a-f]+\.js' | head -1)
[[ -n "$bundle" ]] || { echo "could not find the portal bundle at $portal" >&2; exit 1; }
curl -fsS "$portal$bundle" -o "$tmp/bundle.js"
sum=$(shasum -a 256 "$tmp/bundle.js" | cut -d' ' -f1)

pinned_path=$(grep -o '"path": "[^"]*"' "$schema" | head -1 | cut -d'"' -f4)
pinned_sum=$(grep -o '"sha256": "[^"]*"' "$schema" | head -1 | cut -d'"' -f4)

echo "bundle:  $bundle"
echo "sha256:  $sum"
echo "pinned:  $pinned_path ($pinned_sum)"
[[ "$bundle" == "$pinned_path" ]] || echo "NOTE: the portal has been rebuilt since the schema was pinned." >&2

echo
echo "endpoints in this bundle:"
grep -oE '\.api,"/[a-zA-Z0-9/_?=&.-]+' "$tmp/bundle.js" | sed 's/.*api,"//; s/[?].*//' | sort -u | sed 's/^/  /'

echo
echo "endpoints the provider depends on:"
while read -r path; do
  # the bundle writes either .api,"/site" or .api,"/site/state?id=
  n=$(grep -cE "\.api,\"$path(\"|\?)" "$tmp/bundle.js" || true)
  printf '  %-18s %s\n' "$path" "$([[ "$n" -gt 0 ]] && echo present || echo MISSING)"
done < <(grep -o '"path": "/api/[^"]*"' "$schema" | cut -d'"' -f4 | sed 's|^/api||' | sort -u)
