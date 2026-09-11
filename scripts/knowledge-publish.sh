#!/usr/bin/env bash
set -euo pipefail
# Never enable shell tracing: the database secret exists only in process memory.
for name in KNOWLEDGE_OFFICE KNOWLEDGE_EXPECTED_REVISION KNOWLEDGE_GOOGLE_PROJECT KNOWLEDGE_SQL_INSTANCE KNOWLEDGE_DATABASE_SECRET KNOWLEDGE_OPERATOR_EMAIL GITHUB_SHA; do
  if [[ -z "${!name:-}" ]]; then echo "$name is required" >&2; exit 1; fi
done
if [[ ! "$KNOWLEDGE_OFFICE" =~ ^[a-zA-Z0-9][a-zA-Z0-9_-]*$ ]]; then
  echo 'Invalid knowledge office filename.' >&2
  exit 1
fi
source="knowledge/offices/${KNOWLEDGE_OFFICE}.yaml"
cases="knowledge/evals/${KNOWLEDGE_OFFICE}.json"
if [[ -f "$cases" ]]; then
  : "${KNOWLEDGE_API_URL:?required for retrieval verification}"
  : "${KNOWLEDGE_SERVICE_TOKEN_SECRET:?required for retrieval verification}"
  python3 scripts/knowledge-evaluate.py --cases "$cases" --validate-only
fi
if [[ "$(git rev-parse HEAD)" != "$GITHUB_SHA" ]]; then
  echo 'Checkout does not match the publication commit.' >&2
  exit 1
fi
git ls-files --error-unmatch "$source" >/dev/null
git diff --exit-code HEAD -- "$source" >/dev/null
binary=$(mktemp)
proxy_pid=''
cleanup() {
  if [[ -n "$proxy_pid" ]]; then kill "$proxy_pid" 2>/dev/null || true; wait "$proxy_pid" 2>/dev/null || true; fi
  rm -f "$binary"
}
trap cleanup EXIT
go build -o "$binary" ./backend/cmd/knowledge-import
"$binary" --source "$source" --commit "$GITHUB_SHA" --expected-revision "$KNOWLEDGE_EXPECTED_REVISION"
cloud-sql-proxy --address 127.0.0.1 --port 5432 "$KNOWLEDGE_SQL_INSTANCE" &
proxy_pid=$!
python3 - <<'PY'
import socket, time
for attempt in range(30):
    try:
        with socket.create_connection(('127.0.0.1', 5432), timeout=1):
            break
    except OSError:
        time.sleep(1)
else:
    raise SystemExit('Cloud SQL proxy did not become ready.')
PY
KNOWLEDGE_IMPORT_DATABASE_URL=$(gcloud secrets versions access latest --project "$KNOWLEDGE_GOOGLE_PROJECT" --secret "$KNOWLEDGE_DATABASE_SECRET")
export KNOWLEDGE_IMPORT_DATABASE_URL
export KNOWLEDGE_DATABASE_HOST=127.0.0.1
"$binary" --source "$source" --commit "$GITHUB_SHA" --expected-revision "$KNOWLEDGE_EXPECTED_REVISION" --apply | tee knowledge-publication.json
if [[ -f "$cases" ]]; then
  KNOWLEDGE_SERVICE_TOKEN=$(gcloud secrets versions access latest --project "$KNOWLEDGE_GOOGLE_PROJECT" --secret "$KNOWLEDGE_SERVICE_TOKEN_SECRET")
  export KNOWLEDGE_SERVICE_TOKEN
  revision=$(python3 -c 'import json; print(json.load(open("knowledge-publication.json"))["revision"]["id"])')
  python3 scripts/knowledge-evaluate.py --cases "$cases" --office "$KNOWLEDGE_OFFICE" \
    --url "$KNOWLEDGE_API_URL" --revision "$revision" | tee knowledge-evaluation.json
  unset KNOWLEDGE_SERVICE_TOKEN
fi
if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  {
    echo '### Knowledge publication'
    echo "Source: \`$source\`"
    echo "Git commit: \`$GITHUB_SHA\`"
    echo '```json'
    cat knowledge-publication.json
    echo '```'
  } >> "$GITHUB_STEP_SUMMARY"
fi
