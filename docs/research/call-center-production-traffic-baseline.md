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
deadline and a two-connection ingress pool.

The test requires:

- every response is HTTP 204;
- acknowledgement p99 is under one second;
- acknowledgement commits receipts and duplicates without projecting Calls;
- the worker later converges every unique receipt exactly once; and
- the independent portal and worker pools remain responsive.

Five repeated local runs on 2026-07-29 passed. For 25 requests, the reported
nearest-rank p99 is the strict burst maximum rather than a production percentile.
The test holds the receipt table until both ingress database sessions are
observably blocked, then proves the portal and worker pools remain responsive
before releasing the burst. Across ten synchronized runs in two independent
passes, acknowledgement p95 ranged from about 32 ms to 316 ms and the burst
maximum from about 33 ms to 317 ms.

This combines a Vercel production traffic baseline with deterministic local
PostgreSQL evidence. It does not prove live Telnyx delivery, Cloud Run
scheduling, Cloud SQL capacity, network behavior, or a production latency SLA.
