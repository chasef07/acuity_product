# Browser call-center answer-to-audio latency — 2026-08-31

## Decision

Do not replace Acuity's explicit Bridge with `bridge_on_answer`. The attached
review is right on that point: Telnyx documents `bridge_on_answer` as an
automatic bridge when the dialed call answers, while explicit Bridge lets the
application commit the winning Staff CallLeg before asking Telnyx to connect
the legs. Telnyx also documents that `prevent_double_bridge` defaults to
`false`, so Acuity is right to set it explicitly on Bridge
([Dial](https://developers.telnyx.com/api-reference/call-commands/dial),
[Bridge](https://developers.telnyx.com/api-reference/call-commands/bridge-calls)).

The production pattern to aim for is **durable row first, immediate wake second,
bounded polling fallback always**. That means PostgreSQL `NOTIFY` can be a good
latency optimization, but only as a hint to rescan committed receipt/command
rows. It must never carry the work or replace the 250 ms scan.

For Acuity, deploy and measure the new 250 ms receipt cadence before adding the
listener. After that change, a receipt-only wake can remove no more than one
0–250 ms receipt-pickup interval; the Bridge-command coordinator has its own
0–250 ms pickup interval. If measured dead air is still meaningful and those
two pickup intervals are visible in the trace, add one worker wake mechanism
that wakes both scans (or wakes the receipt scan and directly signals the
command scan when projection commits a Bridge). Do not add `NOTIFY` merely
because it is theoretically faster.

## What production-grade systems do

| Concern | Documented fact | Production inference | Acuity now |
|---|---|---|---|
| Winner selection | Telnyx says `bridge_on_answer` automatically bridges the answered call to `link_to`; explicit Bridge is a separate command. `prevent_double_bridge` is disabled by default. | Use automatic bridge only when no application-side ownership or concurrency gate must run. With competing agents, commit the winner first and use explicit Bridge with a stable command ID. | Already follows this pattern for inbound and outbound: `bridge_on_answer: false`, a database winner/occupancy transaction, then explicit Bridge with `prevent_double_bridge: true` ([inbound dial](../../backend/internal/humancalling/callleg_projection.go), [outbound flow](../../backend/internal/humancalling/outbound.go), [provider guard](../../backend/internal/humancalling/telnyx.go)). |
| Webhook ingestion | Telnyx warns that voice webhooks can be out of order, simultaneous, and duplicated. Its best practice is to return `2xx` immediately, process asynchronously, deduplicate on event `id`, use command IDs, monitor failures, and configure a failover URL ([receiving webhooks](https://developers.telnyx.com/docs/voice/programmable-voice/receiving-webhooks), [webhook reference](https://developers.telnyx.com/docs/voice/programmable-voice/voice-api-webhooks)). | Keep ingress small: verify, atomically persist, acknowledge, then project. Low latency should come from waking the projector, not from weakening durable receipt or doing remote provider work before acknowledgement. | `ReceiveWebhook` verifies the signed body, inserts a unique raw receipt, and returns only after commit ([webhook.go](../../backend/internal/humancalling/webhook.go)). |
| Wake-up | PostgreSQL delivers `NOTIFY` only after commit. The documented pattern is to keep data in tables and use the notification to tell consumers to look for changes ([NOTIFY](https://www.postgresql.org/docs/current/sql-notify.html)). | Treat notification as edge-triggered invalidation, not a queue. Payload can be empty or an opaque row key; the worker still claims rows with the existing idempotent query. | Calling receipts now scan every 250 ms rather than backing off to two seconds ([runner.go](../../backend/internal/worker/runner.go), [regression test](../../backend/internal/worker/runner_test.go)). No worker `LISTEN` wake exists. |
| Listener failure | `LISTEN` registrations are session-scoped and cleared when the session ends. PostgreSQL documents an initial-listen race and requires: commit `LISTEN`, inspect current database state, then rely on later notifications. Notifications are delivered between transactions, and long listener transactions delay delivery ([LISTEN](https://www.postgresql.org/docs/current/sql-listen.html), [NOTIFY](https://www.postgresql.org/docs/current/sql-notify.html)). | Reconnect, re-`LISTEN`, immediately drain the authoritative tables, and continue periodic scanning. Monitor disconnects, reconnect failures, and notification-queue usage. | The architecture already applies this hint-not-truth rule to realtime SSE, but adding it to the worker would consume another dedicated direct database connection and require revising the checked connection budget. |
| Browser readiness | Telnyx separates the long-lived authenticated signaling client from the per-call media session; `telnyx.ready` means the client is authenticated and usable ([SDK anatomy](https://developers.telnyx.com/docs/voice/webrtc/js-sdk/anatomy)). | Register the browser and acquire media permission before the operator answers. Do not make the answer click pay login, token, microphone-permission, or application-navigation setup. | Already pre-registers the persistent TelnyxRTC client, pre-acquires the microphone while the Staff member owns the softphone, and keeps the runtime above navigation ([softphone-runtime.ts](../../web/src/lib/calling/softphone-runtime.ts), [media-adapter.ts](../../web/src/lib/calling/media-adapter.ts)). |
| ICE/media tuning | TelnyxRTC 2.27.10 added `earlySdpAnswer` to request faster media negotiation, default `false`; the same release changed ICE candidate prefetching from default-on to default-off because of wasted allocations, while allowing explicit opt-in ([2.27.10 release](https://github.com/team-telnyx/webrtc/releases/tag/webrtc%402.27.10)). | Do not call either toggle a universal best practice. Run controlled A/B tests on representative practice networks; keep it only if answer-to-first-audio improves without higher setup failures or resource use. | Acuity pins 2.27.10 and does not set either option ([package.json](../../web/package.json), [calling client options](../../web/src/lib/calling/media-adapter.ts)). Therefore ICE prefetch is currently off. |
| Measurement | `call.bridged` proves signaling connection, not audible media. WebRTC stats expose inbound packet/sample counters, audio energy, jitter-buffer data, and last-packet timestamps; audio playout counters exist but remain marked at risk in the W3C draft ([WebRTC Stats](https://www.w3.org/TR/webrtc-stats/)). Telnyx exposes SDK stats and pre-call diagnostics; first-party contact-center guidance also separates endpoint/network tests from per-call audio-quality metrics ([Telnyx 2.27.10](https://github.com/team-telnyx/webrtc/releases/tag/webrtc%402.27.10), [Amazon Connect diagnostics](https://docs.aws.amazon.com/connect/latest/adminguide/troubleshoot-audio-quality.html)). | Measure both control-plane and media-plane intervals. A green Bridge metric is insufficient when the reported symptom is dead air. | Acuity has provider-confirmed Staff-answer-to-Bridge telemetry, but no first-inbound-RTP or first-audible-synthetic-tone metric ([metric contract](../../deploy/observability/log-metrics.json)). |
| Recovery | Telnyx explicitly supports webhook retry policies (maximum five retries, total configured delay at most 60 seconds), failover webhook URLs, event-ID dedupe, and command IDs ([Dial retry policy](https://developers.telnyx.com/api-reference/call-commands/dial), [webhook best practices](https://developers.telnyx.com/docs/voice/programmable-voice/voice-api-webhooks)). | A fast path never removes reconciliation. Provider retries cover short ingress failures; durable rows plus idempotent scans cover worker/listener loss; a bounded provider reconciliation path covers missing terminal facts or ambiguous commands. | Durable receipts, stable commands, retries, and reconciliation already exist. Preserve them if `NOTIFY` is added. |

## Recommended test and rollout

1. Deploy the 250 ms receipt-lane change and run at least 20 PHI-free inbound
   and 20 outbound calls over representative office networks. Use a synthetic
   tone source, not speech.
2. Record a correlated stage trace for each Call: provider
   `call.answered.occurred_at` → ingress receipt commit → receipt claim → Bridge
   command commit → Bridge claim → Telnyx response → `call.bridged.occurred_at`
   → first inbound RTP/sample increase in the browser → synthetic-tone
   detection. Store only bounded durations and opaque correlation IDs.
3. Report p50/p95/p99 separately for inbound and outbound. The current
   answer-to-Bridge metric should be retained, but the eight-second alert is a
   broad failure alarm, not an answer-to-audio experience target.
4. Add `LISTEN/NOTIFY` only if receipt or command pickup is a material part of
   the remaining p95. Implement one dedicated worker connection with jittered
   reconnect; after every reconnect, drain the rows before waiting. Keep the
   250 ms timer active as the recovery fallback.
5. Separately A/B `earlySdpAnswer: true`. Test
   `prefetchIceCandidates: true` only if first-media delay, rather than backend
   pickup, remains material. Telnyx 2.27.10 changed that default for a reason;
   measure setup failures and TURN/ICE behavior as well as speed.

## Unknowns and limits

- Telnyx publishes no answer-to-first-audible-audio SLO for this exact
  two-leg WebRTC/Call Control topology.
- The Telnyx docs establish what `bridge_on_answer` does, but do not document
  an application-visible atomic tie-break across near-simultaneous answers.
- No current live Acuity trace proves how much of answer-to-audio time is
  receipt pickup, Bridge-command pickup, Telnyx processing, WebRTC negotiation,
  jitter buffering, or device playout.
- Browser `play()` resolution and `RTCPeerConnection.connectionState` are
  readiness signals, not proof that a human heard audio. The synthetic-tone
  test is the acceptance proof; WebRTC stats localize the delay.
- This review did not inspect or mutate live Telnyx application settings, live
  Cloud SQL listener capacity, or deployed revision behavior.

## Bottom line

The attached review's durable explicit-Bridge direction is aligned with the
strongest provider evidence. The state-of-the-art shape is not “polling versus
notifications”; it is **notifications for speed, durable rows and polling for
correctness**. Acuity should earn the extra listener with measurements after
the 250 ms deployment. If the worker still dominates p95, add the wake path. If
it does not, spend the next optimization on browser media negotiation or
provider/network placement instead.
