#!/usr/bin/env bash

set -Eeuo pipefail

evidence_path="${CALLLEG_CUTOVER_EVIDENCE_PATH:-}"
if [[ -z "$evidence_path" || ! -f "$evidence_path" ]]; then
  echo "CALLLEG_CUTOVER_EVIDENCE_PATH must name the sanitized cutover evidence file." >&2
  exit 1
fi
if [[ "${CALLLEG_CUTOVER_WINDOW_CONFIRMED:-false}" != true ]]; then
  echo "CALLLEG_CUTOVER_WINDOW_CONFIRMED=true is required after admission is closed and the legacy runtime is drained." >&2
  exit 1
fi

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
node \
  "$script_directory/check-telnyx-callleg-cutover.mjs" \
  "$script_directory/telnyx-callleg-cutover-contract.json" \
  "$evidence_path"

export CALLLEG_CUTOVER_EVIDENCE_VERIFIED=true
export CALLLEG_DESTRUCTIVE_CUTOVER=true
exec "$script_directory/deploy-production-release.sh"
