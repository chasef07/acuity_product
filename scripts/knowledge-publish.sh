#!/usr/bin/env bash
set -euo pipefail
# Never enable tracing: credentials exist only in process memory.
for name in KNOWLEDGE_GOOGLE_PROJECT KNOWLEDGE_SQL_INSTANCE KNOWLEDGE_DATABASE_SECRET KNOWLEDGE_OPERATOR_EMAIL GITHUB_SHA; do
  if [[ -z "${!name:-}" ]]; then echo "$name is required" >&2; exit 1; fi
done
selection=${KNOWLEDGE_OFFICE:-all}
shopt -s nullglob
if [[ "$selection" == all ]]; then
  sources=(knowledge/offices/*.yaml)
elif [[ "$selection" =~ ^[a-zA-Z0-9][a-zA-Z0-9_-]*$ ]]; then
  sources=("knowledge/offices/${selection}.yaml")
else
  echo 'Invalid office selection.' >&2
  exit 1
fi
if ((${#sources[@]} == 0)); then echo 'No office sources found.' >&2; exit 1; fi
if [[ "$(git rev-parse HEAD)" != "$GITHUB_SHA" ]]; then
  echo 'Checkout does not match the publication commit.' >&2
  exit 1
fi
runtime=$(mktemp -d)
proxy_pid=''
cleanup() {
  if [[ -n "$proxy_pid" ]]; then kill "$proxy_pid" 2>/dev/null || true; wait "$proxy_pid" 2>/dev/null || true; fi
  rm -rf "$runtime"
}
trap cleanup EXIT
binary="$runtime/knowledge-import"
go build -o "$binary" ./backend/cmd/knowledge-import
needs_api=false
# Validate every selected source before the first database write.
for source in "${sources[@]}"; do
  git ls-files --error-unmatch "$source" >/dev/null
  git diff --exit-code HEAD -- "$source" >/dev/null
  "$binary" --source "$source" --commit "$GITHUB_SHA"
  office=$(basename "$source" .yaml)
  cases="knowledge/evals/${office}.json"
  if [[ -f "$cases" ]]; then
    : "${KNOWLEDGE_API_URL:?required for retrieval verification}"
    : "${KNOWLEDGE_SERVICE_TOKEN_SECRET:?required for retrieval verification}"
    python3 scripts/knowledge-evaluate.py --cases "$cases" --validate-only
    needs_api=true
  fi
done
output=${KNOWLEDGE_OUTPUT_DIRECTORY:-${RUNNER_TEMP:-${TMPDIR:-/tmp}}/knowledge-publication-${GITHUB_RUN_ID:-$$}}
mkdir -p "$output"
if [[ "$needs_api" == true ]]; then
  KNOWLEDGE_SERVICE_TOKEN=$(gcloud secrets versions access latest --project "$KNOWLEDGE_GOOGLE_PROJECT" --secret "$KNOWLEDGE_SERVICE_TOKEN_SECRET")
  export KNOWLEDGE_SERVICE_TOKEN
fi
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
status=0
if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  printf '### Knowledge publication\n\nCommit: `%s`\n\n| Office | Result |\n| --- | --- |\n' "$GITHUB_SHA" >> "$GITHUB_STEP_SUMMARY"
fi
for source in "${sources[@]}"; do
  office=$(basename "$source" .yaml)
  echo "Publishing $office"
  result='failed'
  # Each office is atomic. Continue independent offices and report any failure.
  if "$binary" --source "$source" --commit "$GITHUB_SHA" --apply | tee "$output/$office.json"; then
    cases="knowledge/evals/${office}.json"
    if metadata=$(python3 -c 'import json,sys; r=json.load(open(sys.argv[1])); print(("unchanged" if r.get("unchanged") else "published")+"\t"+r["revision"]["id"])' "$output/$office.json"); then
      IFS=$'\t' read -r result revision <<< "$metadata"
      if [[ -f "$cases" ]] && ! python3 scripts/knowledge-evaluate.py --cases "$cases" --office "$office" \
        --url "$KNOWLEDGE_API_URL" --revision "$revision" | tee "$output/$office-evaluation.json"; then
        result="$result; retrieval verification failed"
        status=1
      fi
    else
      result='invalid publication receipt'
      status=1
    fi
  else
    status=1
  fi
  if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
    printf '| %s | %s |\n' "$office" "$result" >> "$GITHUB_STEP_SUMMARY"
  fi
done
unset KNOWLEDGE_SERVICE_TOKEN
exit "$status"
