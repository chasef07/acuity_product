# Portal polling saturation — 2026-08-11

## Finding

The August 10 portal degradation combined an abnormal browser request storm
with a one-connection portal pool. It was not a Cloud SQL CPU, memory, storage,
or global connection-capacity event.

During a representative degraded minute, request logs showed up to 21
`/v1/calling/softphone/lease` requests and 18 `/v1/calling/state` requests in one
second. The intended visible-owner cadence is one state read every two seconds
and one lease renewal every five seconds. The excess came from the Calling dock
using every general workspace revision as an immediate trigger for both routes;
lease renewal also lacked a single-flight guard.

The server-side constraint amplified that traffic. Production permits 20
concurrent portal requests per instance but configured one PostgreSQL connection
per instance. Sampled portal acquisition latency during degradation reached
249 ms at p95 and 462 ms at p99, and pool samples reached 100 percent
saturation. Failed HTTP requests clustered at the 1.5-second acquisition
deadline.

## Focused correction

- General workspace revisions no longer trigger Calling state or lease polls.
  Calling retains its authoritative two-second visible and ten-second hidden
  state loop.
- Lease renewal remains on its five-second owner heartbeat and is now
  single-flight within a browser tab.
- Only the production `portal-api` pool increases from one to two connections.
  Web, provider ingress, realtime, worker, and migration pools are unchanged.
- The checked production connection reservation increases from 22 to 26:
  three additional configured portal connections plus one additional portal
  autoscaler-overshoot connection.

## Deterministic evidence

The integration load loop holds one portal connection busy, then concurrently
requests the real Calling state and softphone lease HTTP interfaces. With a
one-connection pool neither request succeeds; with a two-connection pool both
return HTTP 200. The loop passed five consecutive runs.

Production deploy-contract tests went red against the old pool and connection
budget, then green after the focused changes. Web type checking, lint, and all
63 unit tests also pass.

## Remaining live gate

This branch is not deployment proof. Before promotion, the release preflight
must measure at least 26 usable database connections. After promotion, compare
per-second state/lease request counts, portal pool saturation, acquisition p95
and p99, and HTTP 503s through a representative live interval. Roll back if
lease traffic materially exceeds the five-second owner cadence or acquisition
timeouts recur.
