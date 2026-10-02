#!/usr/bin/env bash
set -euo pipefail
output=${GITHUB_OUTPUT:-/dev/stdout}
decide() {
  echo "publish=$1" >> "$output"
  exit 0
}
if [[ "${EVENT_NAME:?EVENT_NAME is required}" != push ]]; then
  decide true
fi
head=${HEAD_SHA:?HEAD_SHA is required}
before=${BEFORE_SHA:-}
if [[ -z "$before" || "$before" =~ ^0+$ ]]; then
  echo 'No previous commit to compare; publishing every office.'
  decide true
fi
changed=$(git diff --name-only "$before" "$head")
if ! grep -qE '^knowledge/(offices|evals)/' <<<"$changed"; then
  echo 'No office knowledge changed; nothing to publish.'
  decide false
fi
if grep -qE '^(backend/|go\.(mod|sum)$)' <<<"$changed"; then
  echo '::notice title=Knowledge not published::Office knowledge changed together with backend code. After the release deploys, publish with Actions → Knowledge → Run workflow.'
  decide false
fi
decide true
