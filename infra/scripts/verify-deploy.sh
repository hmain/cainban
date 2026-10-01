#!/usr/bin/env bash
#
# verify-deploy.sh — post-deploy smoke check of the LIVE API edge.
#
# Catches the class of bug that unit tests and `cdk synth` cannot see: the
# deployed gateway behaving differently from what the code implies. Concretely
# it asserts, against the real endpoints:
#
#   1. CORS PREFLIGHT succeeds — an unauthenticated `OPTIONS` (as a browser
#      sends before a cross-origin POST, with NO Authorization header) returns
#      2xx with Access-Control-* headers. This is exactly the check that would
#      have caught the authorizer-on-OPTIONS 401 that silently broke the
#      browser's list_activity call.
#   2. AUTH IS STILL ENFORCED — an unauthenticated data request (POST tools/list
#      on MCP; GET /connect/repos on Connect) returns 401. A preflight fix must
#      never accidentally open the data path.
#
# Endpoints are read from the deployed CloudFormation stack OUTPUTS (not
# hardcoded), so this stays correct across environments. Requires AWS creds that
# can read the stack (set AWS_PROFILE / AWS_REGION), plus curl.
#
# Usage:
#   AWS_PROFILE=aws-test-hamin AWS_REGION=eu-north-1 ./infra/scripts/verify-deploy.sh
#   # or override the stack / origin:
#   STACK=CainbanPhase2Stack ORIGIN=https://main.d1x7br21qa4ohy.amplifyapp.com ./infra/scripts/verify-deploy.sh
#
set -euo pipefail

STACK="${STACK:-CainbanPhase2Stack}"
# The browser Origin the preflight claims to come from. Any value works for the
# check (the API allows '*'); default to the deployed connect page.
ORIGIN="${ORIGIN:-https://main.d1x7br21qa4ohy.amplifyapp.com}"

fail=0
pass() { printf '  \033[32mPASS\033[0m %s\n' "$1"; }
bad()  { printf '  \033[31mFAIL\033[0m %s\n' "$1"; fail=1; }

# stack_output KEY -> the OutputValue, or empty if absent.
stack_output() {
  aws cloudformation describe-stacks --stack-name "$STACK" \
    --query "Stacks[0].Outputs[?OutputKey=='$1'].OutputValue | [0]" \
    --output text 2>/dev/null
}

# http_code METHOD URL [extra curl args...] -> prints the numeric status.
http_code() {
  local method="$1" url="$2"; shift 2
  curl -sS -o /dev/null -w '%{http_code}' -X "$method" "$url" "$@"
}

echo "verify-deploy: reading stack outputs from $STACK ..."
MCP_API="$(stack_output McpApiUrl)"
CONNECT_API="$(stack_output ConnectApiUrl)"

if [ -z "$MCP_API" ] || [ "$MCP_API" = "None" ]; then
  echo "ERROR: could not read McpApiUrl from stack $STACK (AWS creds / region?)." >&2
  exit 2
fi
echo "  MCP_API     = $MCP_API"
echo "  CONNECT_API = ${CONNECT_API:-<none>}"
echo

# --- 1. CORS preflight on the MCP endpoint (root + a sub-path) -------------
echo "[1] CORS preflight (unauthenticated OPTIONS must be 2xx):"
for path in "" "somepath"; do
  url="${MCP_API%/}/${path}"
  code="$(http_code OPTIONS "$url" \
    -H "Origin: $ORIGIN" \
    -H "Access-Control-Request-Method: POST" \
    -H "Access-Control-Request-Headers: authorization,content-type,x-cainban-repo")"
  if [ "$code" -ge 200 ] && [ "$code" -lt 300 ]; then
    pass "OPTIONS ${url} -> $code"
  else
    bad "OPTIONS ${url} -> $code (want 2xx; a non-2xx preflight blocks the browser)"
  fi
done

# The preflight must also carry the Access-Control-Allow-Origin header.
acao="$(curl -sS -D - -o /dev/null -X OPTIONS "${MCP_API%/}/" \
  -H "Origin: $ORIGIN" -H "Access-Control-Request-Method: POST" 2>/dev/null \
  | grep -i '^access-control-allow-origin:' || true)"
if [ -n "$acao" ]; then
  pass "preflight carries ${acao%$'\r'}"
else
  bad "preflight is missing the Access-Control-Allow-Origin header"
fi
echo

# --- 2. Auth still enforced on the data path -------------------------------
echo "[2] Auth gate (unauthenticated data request must be 401):"
code="$(http_code POST "${MCP_API%/}/" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}')"
if [ "$code" = "401" ]; then
  pass "unauth POST tools/list -> 401 (MCP data path gated)"
else
  bad "unauth POST tools/list -> $code (want 401; the data path must require a token)"
fi

if [ -n "${CONNECT_API:-}" ] && [ "$CONNECT_API" != "None" ]; then
  code="$(http_code GET "${CONNECT_API%/}/connect/repos")"
  if [ "$code" = "401" ]; then
    pass "unauth GET /connect/repos -> 401 (Connect data path gated)"
  else
    bad "unauth GET /connect/repos -> $code (want 401)"
  fi
fi
echo

if [ "$fail" -eq 0 ]; then
  echo "verify-deploy: ALL CHECKS PASSED"
else
  echo "verify-deploy: ONE OR MORE CHECKS FAILED" >&2
fi
exit "$fail"
