# Second Staff Dial latency review

Read-only source/evidence review of the backend hardening branch before publication.
This does not refresh production measurements or change runtime behavior.

## Conclusion

The patch removes a proven scheduling inefficiency and reduces avoidable
analytical/recovery work. It does not yet establish the dominant cause of the
previously measured approximately 2.63-second Staff Dial command-age p95.

The batch change removes a 250 ms wait after eight successful claims when more
ready work remains. The first eight commands in a batch do not receive that
particular saving. A single smaller fanout may never reach the batch boundary;
other concurrent Calls can still fill a batch. Both original 250 ms idle/blocked
polling and dependency serialization remain.

Analytics is a large improvement to the analytics route. Its effect on dialing
is indirect through shared PostgreSQL CPU, I/O and lock pressure: portal and
worker have separate connection pools. Recovery improvements preserve evidence
and reduce repeated observation work; they are not a measured reduction in first
Dial dispatch time.

## Remaining contributors, in review priority order

1. **Receipt/answer projection and fanout transaction.** The new integration
   tests provision and project inbound fanout before starting the measured Runner
   window. Their mixed-load receipts are synthetic UNKNOWN events, not the
   `call.answered` projection that creates Staff Dials. Consequently these tests
   do not measure the full incoming receipt-to-fanout-commit interval. The real
   fanout snapshots eligible Staff under locks and inserts each CallLeg and
   command separately, then records workspace/timeline changes before commit.
   The stored command creation timestamp comes from the transaction, so legacy
   command age can include work before the command becomes visible.
2. **Same-Call blocking.** Active non-Dial commands, including START_RING_WINDOW,
   block Staff Dials on the same Call. The deterministic test proves this rule,
   but not that it is the largest production delay. A blocked scan still waits
   250 ms before checking again. Measure ringback execution plus persistence and
   dependency release before considering any concurrency change. Caller-audio
   and ring-window semantics remain acceptance criteria.
3. **Worker claim and executor capacity.** Ten provider executors already exist,
   while claim work coordinates through two database connections. Saturated
   executors or slow claim/result transactions can delay new work even though
   the full-batch sleep is gone. The added stage observations distinguish actual
   claim work from claim-to-dispatch and provider/result time; they do not yet
   identify an exact eligibility timestamp or all reasons for pre-claim waiting.
4. **Provider and browser ring delivery.** Provider dispatch is not actual Staff
   ringing. The new local timing endpoint is provider invocation, using a stub.
   The recorded legacy provider duration also includes persistence and cannot
   be treated as pure network latency. The Telnyx adapter explicitly disables
   SDK automatic retries, so an assumed SDK default retry multiplier is not a
   supported explanation.

## Next bounded proof

Extend the latency fixture to start an already-running production-shaped Runner,
then submit the caller-answer receipt through the durable ingress seam. Measure
receipt processing, fanout transaction completion, first Staff Dial dispatch,
last Staff Dial dispatch, and provider/browser ring evidence where available.
Exercise 1/5/10 Staff with controlled ringback delay and database contention.
Preserve the same workload and outcomes for every comparison. Do not add
percentiles from different stages to estimate an end-to-end percentile.

Use the newly added stages after an authorized deployment to determine whether
production waiting is before claim, inside claim, between claim and dispatch,
at the provider, or during persistence. Select the next optimization from that
measurement. Notification wakeups, batching fanout inserts, larger pools and
relaxing ringback serialization remain conditional ideas, not proven remedies.

Source locations: `backend/internal/worker/runner.go` (coordinator),
`backend/internal/humancalling/callleg_projection.go` (caller answer fanout),
`outgoing_callleg_control.go` (claim/dispatch), `telnyx.go` (provider options),
`staff_dial_latency_integration_test.go` (measurement start and mixed fixture).
No additional code changes or tests were required for this review; prior test
results remain local evidence and production benefits are still unverified.
