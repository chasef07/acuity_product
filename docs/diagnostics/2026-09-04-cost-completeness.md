# AI cost completeness

The cost calculator classified LiveKit's `unknown / FallbackAdapter` LLM
usage as an unpriced provider. This disqualified otherwise complete calls
from both cost averages, while their recognized provider costs still appeared
in the total and breakdown.

## Evidence

- Reproduced with the installed `@livekit/agents` 1.7.1 runtime: a synthetic
  model returning 1,000 input tokens, 200 cached tokens, and 20 output tokens
  through `llm.FallbackAdapter` produces two usage rows with identical token
  quantities: `livekit / google/gemma-4-31b-it` and
  `unknown / FallbackAdapter`. The adapter forwards provider metrics and its
  outer stream emits another metrics event.
- A read-only production evidence request confirmed the same duplicate pattern.
  Only usage fields were inspected; no conversation content was retained.
- A bounded sample of 20 recent completed production calls had 16 calls with
  Gemma, AssemblyAI, and Coda usage plus the wrapper, and four missing at least
  one provider usage category. No other LLM model appeared in that sample.
  This is a sample, not a recalculation of the full seven-day report.
- Before the fix, `TestCostAnalyticsFallbackAdapterUsage` failed with
  `priced=0 unpriced=1` for complete provider usage plus the duplicate wrapper.

## Change

Exclude only the known LLM wrapper identity from provider pricing. Actual
provider records still establish coverage and costs. Wrapper-only reports
remain incomplete; unsupported real models and invalid provider quantities
still prevent a complete cost average. No rates or recorded totals change.

Tests cover native and raw collector fields, either row order, absent provider
evidence, unsupported real fallback models, invalid quantities, daily coverage,
and averages without double-counting. The database-backed HTTP test includes
the wrapper in persisted native usage and verifies the scoped API response.

## Verification

Disposable local database:
`postgres://chasefagen@127.0.0.1:55448/acuity_cost_test?sslmode=disable`.

- `go test ./backend/internal/interaction -run Cost -count=1`: passed.
- `TEST_DATABASE_URL=... go test ./backend/internal/httpapi -run TestOperatorAIAnalyticsIsScopedPaginatedAndNormalized -count=1`: passed.
- `TEST_DATABASE_URL=... go test -p 1 ./backend/... ./deploy -count=1`: passed,
  including database integration tests.
- `go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./backend/...`: no vulnerabilities.
- `bash ./scripts/test-release-container.sh`: unable to run because the local
  Docker daemon is unavailable (`/var/run/docker.sock` does not exist).

Production source evidence confirms the cause. Deployment and the resulting
production report remain unverified; the fix is local.
