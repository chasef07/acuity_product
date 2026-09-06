# AI diagnostics trends and conversation detail

## Observed problem

The September 6 call drawer put duplicate escalation/transfer badges, a missing-summary block, an empty appointment badge, a four-cell latency panel, per-turn avatars, chat bubbles, timestamps, and timing metrics ahead of a readable conversation. AI diagnostics had aggregate transfer and call counts but no daily trends. The Bookings chart also plotted unrelated call volume on its count axis.

## Change and metric contract

- AI diagnostics Overview shows daily call volume and transfer rate for the selected Practice, Location, and rolling range.
- AIInteraction aggregates every qualifying call in the existing summary query, independently of paginated call rows. Dates use UTC call start, matching diagnostic latency trends. Partial boundary dates remain included.
- Transfer counts retain the existing persisted `ESCALATED` definition. A transfer tool invocation alone does not establish a transfer or a completed staff conversation.
- Daily transfer rate is transferred calls divided by all AI calls on that date. Empty dates have zero calls and no rate; the line leaves gaps. Overall rate uses range totals, not an average of daily percentages.
- Bookings retains its patient-group series and removes call volume from the chart, legend, and tooltip.
- Call detail starts with compact call context and a persistent four-column latency strip. Acuity messages align left and caller messages align right using the existing shadcn Message/Bubble primitives. The shadcn MessageScroller owns transcript scrolling and diagnostic anchors. Per-turn timing is shown by default and can be hidden with Turn timing; selected diagnostic turns reveal their own timing automatically. Selected tool failures expand their request/result or historical execution evidence.
- Appointment evidence remains visible when there are appointment facts, identifiers, receipts, or a determinate outcome. Empty indeterminate appointment sections are omitted. Missing summaries/transcripts remain explicit.

## Validation

All results are local. Browser scenarios use synthetic fixtures and disposable PostgreSQL databases.

- `go test ./backend/internal/interaction -run 'TestAnalyticsDailyTrends|TestSummarizeAnalytics|TestProjectAnalytics' -count=1` — passed. Covers UTC date assignment, empty dates, daily denominators, and call-weighted range rates.
- `TEST_DATABASE_URL=postgres://postgres@127.0.0.1:25487/acuity_diagnostics_test go test -p 1 ./backend/... ./deploy -count=1` — passed, including API scope/pagination checks and the daily totals contract.
- `go generate ./backend/internal/api` and `corepack pnpm@10.34.5 --dir web api:generate` — passed; generated files were not edited by hand.
- `corepack pnpm@10.34.5 --dir web lint` — passed.
- `corepack pnpm@10.34.5 --dir web typecheck` — passed.
- `corepack pnpm@10.34.5 --dir web test:unit` — passed: 238 library tests and 16 component render tests; no skips.
- `corepack pnpm@10.34.5 --dir web audit --prod` — no known vulnerabilities.
- `PATH="/tmp/acuity-diagnostics-pg.Y2ziOy/bin:$PATH" E2E_DATABASE_URL=postgres://postgres@127.0.0.1:25487/acuity_diagnostics_e2e ./scripts/run-e2e.sh operator-ai-diagnostics.spec.ts booking-analytics.spec.ts access-workspace-realtime.spec.ts` — production build passed; all four browser journeys passed in 33.2 seconds. The temporary `pnpm` wrapper invokes `corepack pnpm@10.34.5`.
- Browser checks verified 70 calls and 35 transfers across the full range despite a 50-call first page; daily data sums to 70. They also cover per-turn timing, selected slow turns, expanded tool failures, historical execution focus, appointment receipts, mobile overflow, scope changes, and the remaining three bookings series.
- `git diff --check` — passed.

The initial build revealed a float-width mismatch in the generated contract; adding `format: double` fixed it. A subsequent combined run exhausted local disk space. Generated Next.js cache/dev output was cleared and the isolated test cluster's WAL allocation was bounded; the complete backend and affected browser suites then passed. Browser selectors were narrowed to metric headlines because daily tables intentionally repeat their numeric values.

Visually reviewed synthetic captures:

- [Daily call trends](ai-diagnostics-trends.png)
- [Conversation on desktop](ai-call-conversation.png)
- [Conversation on mobile](ai-call-conversation-mobile.png)

The rest of the browser suite was not rerun. CI, deployed application paths, live provider behavior, and production outcomes remain unverified.

## Scope

Validation was performed locally. This change has not been deployed and made no production-data writes. Existing unrelated edits in `web/AGENTS.md` and the two audit documents are excluded from the pull request.

## Follow-up: bubbles and persistent latency

The requested follow-up restores alternating chat bubbles using the installed shadcn components and keeps P50 STT, TTFT, TTS, and E2E visible above the scrolling call evidence. No dependencies were added. Manual scroll-position calculations were replaced with MessageScroller anchors.

The browser checks now assert sender alignment, Bubble composition, and latency visibility after scrolling. The nested section wrappers keep layout visible so MessageScroller can locate historical execution anchors; the existing historical-failure browser scenario catches regressions at that boundary.

Compared with `acuity_site/app/components/turn-bubble.tsx`, this retains alternating speaker bubbles and inline per-turn STT final, TTFT, and TTS measurements, with the current Product response timing retained as well. Timing footers are visible by default. The existing top-of-call P50 strip remains fixed while scrolling. Tool payloads remain expandable.

Follow-up checks:

- `corepack pnpm@10.34.5 --dir web lint` and `corepack pnpm@10.34.5 --dir web typecheck` — passed.
- `corepack pnpm@10.34.5 --dir web test:unit` — 254 passed.
- `PATH="/tmp/acuity-diagnostics-pg.Y2ziOy/bin:$PATH" E2E_DATABASE_URL=postgres://postgres@127.0.0.1:25487/acuity_diagnostics_e2e ./scripts/run-e2e.sh operator-ai-diagnostics.spec.ts access-workspace-realtime.spec.ts` — production build and all 3 browser journeys passed in 30.0 seconds. Desktop/mobile captures were refreshed. Sender sides, default per-turn timing, fixed latency visibility, and historical execution focus are verified.
- No backend changes in this follow-up; its earlier full-suite result still applies.

Local disk exhaustion recurred during this follow-up. The generated Go compiler cache was cleared, recovering about 5 GB; the temporary database was stopped after verification.
