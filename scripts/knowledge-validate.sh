#!/usr/bin/env bash
set -euo pipefail
shopt -s nullglob
sources=(knowledge/offices/*.yaml)
if ((${#sources[@]} == 0)); then
  echo 'No versioned office knowledge sources found.' >&2
  exit 1
fi
directory=$(mktemp -d)
binary="$directory/knowledge-import"
receipts="$directory/receipts.jsonl"
trap 'rm -rf "$directory"' EXIT
go build -o "$binary" ./backend/cmd/knowledge-import
for source in "${sources[@]}"; do
  "$binary" --source "$source" | tee -a "$receipts"
done
for cases in knowledge/evals/*.json; do
  python3 scripts/knowledge-evaluate.py --cases "$cases" --validate-only
done
python3 - "$receipts" <<'PYTHON'
import json, sys
seen = set()
with open(sys.argv[1]) as receipts:
    for line in receipts:
        receipt = json.loads(line)
        scope = (receipt['practiceId'], receipt['officeKey'])
        if scope in seen:
            raise SystemExit(f'Duplicate knowledge source scope: {scope}')
        seen.add(scope)
PYTHON
