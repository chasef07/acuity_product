#!/usr/bin/env sh
# Real local application with disposable synthetic data and test authentication.
set -eu
: "${E2E_DATABASE_URL:?set this to a disposable local database ending in _e2e}"
export E2E_SERVE=true
exec "$(dirname "$0")/run-e2e.sh"
