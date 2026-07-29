# Call-center production traffic baseline

This is a sizing input, not Cloud Run or Cloud SQL acceptance evidence.
Production Vercel logs for the `website-rebrand` project were sampled on
2026-07-29 for `/api/telnyx/webhooks`.

## Observed legacy traffic

- About 5,544 webhook requests over 24 hours, 3,708 over 6 hours, 783 over one
  hour, and 52 over ten minutes.
- The sampled busy minute contained 35 requests. The highest visible second
  contained 7.
- The recent status sample contained about 5,517 HTTP 200 responses and 24
  HTTP 500 responses.
- Route error clustering found 76 `timeout exceeded when trying to connect`
  occurrences.
- Sampled legacy projection durations ranged from roughly 78 ms to 1.66 s.

These are webhook events, not Calls. The legacy route performs projection and
command work before acknowledgement, so its duration and error rates do not
describe the new receipt-only ingress path.

## Local acceptance proof

The provider-ingress HTTP proof sends 25 simultaneous correctly signed
requests, more than three times the highest visible production second, through
the real handler with the production-shaped 1.5-second request/database
deadline and a one-connection ingress pool.

The test requires:

- every response is HTTP 204;
- acknowledgement p99 is under one second;
- acknowledgement commits receipts and duplicates without projecting Calls;
- the worker later converges every unique receipt exactly once; and
- independent portal and realtime authorization paths remain responsive while
  ingress is observably blocked.

Ten repeated local runs on 2026-07-29 passed. For 25 requests, the reported
nearest-rank p99 is the strict burst maximum rather than a production percentile.
The test holds the receipt table until the single ingress database session is
observably blocked, proves real portal and realtime authorization queries plus
the independent worker pool remain responsive, then releases the burst. Across
ten synchronized runs, HTTP acknowledgement p95 stayed below the one-second
gate and the burst maximum ranged from about 56 ms to 91 ms.

The mixed-role proof uses one ingress connection, one portal connection, and
one worker connection. Across ten repeated runs, mixed-role webhook p99 ranged
from about 74 ms to 100 ms and ten concurrent Staff-command p99 ranged from
about 47 ms to 63 ms. The command duration includes transaction and pool wait;
cumulative portal pool-acquisition wait during each ten-command window ranged
from about 370 ms to 501 ms. A held provider command did not stop the single
worker from applying receipt work, and duplicate provider command IDs did not
occur.

This combines a Vercel production traffic baseline with deterministic local
PostgreSQL evidence. It does not prove live Telnyx delivery, Cloud Run
scheduling, Cloud SQL capacity, network behavior, or a production latency SLA.
