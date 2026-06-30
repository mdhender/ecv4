#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"

printf 'Checking %s/healthz\n' "$BASE_URL"
curl -fsS "$BASE_URL/healthz"
printf '\n\nChecking %s/openapi.yaml\n' "$BASE_URL"
curl -fsS "$BASE_URL/openapi.yaml" >/dev/null
printf 'ok\n'
