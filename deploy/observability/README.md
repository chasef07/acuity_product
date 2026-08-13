# Call-center Google Cloud observability

`log-metrics.json` contains checked Google `LogMetric` objects.
`alert-policies.json` contains checked diagnostic `AlertPolicy` objects.
`backend-service.json`, `backend-availability-slo.json`, and
`slo-burn-policies.json` define the backend availability service, SLI/SLO, and
multi-window error-budget policies. The
application logs only the fixed `call_center_metric` contract; the metric
definitions extract no patient, Practice, Location, User, Call, receipt,
command, provider, phone, email, URL, SQL, error, request, or raw-body value.

The only metric labels are `metric_contract`, `runtime_role`, `revision`, and
the fixed `route`, `failure_stage`, `outcome`, `action`, and `cause` values
where relevant. SSE state is selected by fixed filters instead of becoming
another label.

## Backend availability SLI and SLO

The customer-journey SLI counts only two authenticated, read-only portal route
templates:

- `GET /v1/access`: `200` is available.
- `GET /v1/calling/state`: `200` and cache-valid `304` are available.

Every other response on those templates is unavailable and receives one fixed
failure stage: `authentication`, `authorization`, `dependency`, or `handler`.
`/health/ready` is deliberately excluded: readiness reports whether an
instance should receive traffic; it does not prove that a signed-in User can
authorize and read critical calling state.

The checked SLO is 99.9% availability over a rolling 28 days, a 40.32-minute
error budget. The fast policy requires both 5-minute and 1-hour burn rates to
exceed 14.4x. The slow policy requires both 30-minute and 6-hour burn rates to
exceed 6x. The two-window `AND` conditions avoid treating a one-sample spike as
a sustained budget incident.

The current `apply.mjs` applies log metrics and the existing diagnostic
threshold policies only. It intentionally does not create the custom service,
SLO, or burn policies yet. Before applying those three checked payloads, render
`${PROJECT_ID}`, create or reconcile the `acuity-portal-backend` custom service,
validate the SLO filter against ingested `acuity_backend_availability_count`
series, attach notification channels, and exercise one reversible fast- and
slow-burn test incident. No Cloud resources were changed while producing these
artifacts.

## Authenticated portal canary

`run-portal-canary.mjs` performs the same safe critical-read journey as the
SLI: authenticated Access discovery followed by Calling State. It does not
acquire a softphone lease, place an outbound Call, write database state, or
print response bodies or credentials.

```sh
PORTAL_API_URL=https://portal-api.example \
PORTAL_CANARY_BEARER_TOKEN="$(your-secret-provider read portal-canary-token)" \
  node deploy/observability/run-portal-canary.mjs
```

The remaining production acceptance gate is to provision a dedicated
least-privilege portal User through the normal identity and Access Grant flow,
store its refreshable credential outside this repository, schedule the runner
from an approved egress environment, and prove SLI ingestion plus alert
delivery. Automating a softphone lease or outbound Call remains intentionally
out of scope because both mutate production and the latter can incur provider
cost.

## Apply

Dry-run the exact `gcloud` commands without reading or changing Google Cloud:

```sh
GCP_PROJECT=your-project \
  node deploy/observability/apply.mjs
```

Apply the log metrics and create or update the uniquely named alert policies:

```sh
GCP_PROJECT=your-project \
MONITORING_NOTIFICATION_CHANNELS=projects/your-project/notificationChannels/123 \
  node deploy/observability/apply.mjs --apply
```

Metric updates are idempotent by metric name. Policy updates resolve exactly one
existing policy by its immutable `acuity_policy` label and fail closed if
duplicates exist. Existing policy and condition resource names are preserved,
so an unchanged apply does not replace the conditions. Notification channels
are required for apply and are never stored in the repo.

Run the offline contract and identity checks with:

```sh
go test ./deploy -count=1
node --test deploy/observability/policy-identity.test.mjs
```

## Diagnostic alert conditions

These thresholds are supporting operating hypotheses, not substitutes for the
availability SLO:

| Condition | Initial trigger |
| --- | --- |
| Any unavailable webhook acknowledgement | any in 60 seconds |
| Webhook acknowledgement p99 above one second | p99 over 10 minutes |
| Oldest receipt above 30 seconds | p99 over 10 minutes for 60 seconds |
| Receipt depth above 64 for five minutes | p99 over 10 minutes for 5 minutes |
| Any quarantined provider receipt | durable depth above zero in 60 seconds, until audited requeue |
| Dial command queue p95 above one second | p95 over 10 minutes for 60 seconds |
| Rejected start ring-window command | any definitive rejection in 60 seconds |
| Degraded caller audio after stop ring-window failure | any definitive or ambiguous failure in 60 seconds |
| Ambiguous service provider command | any in 60 seconds |
| Ambiguous worker provider command | any in 60 seconds |
| Service database acquisition timeout | any deadline exhaustion in 60 seconds |
| Worker database acquisition timeout | any deadline exhaustion in 60 seconds |
| Service database saturation above 0.8 | p99 over 10 minutes for 5 minutes |
| Worker database saturation above 0.8 | p99 over 10 minutes for 5 minutes |
| More than three listener disconnects in five minutes | four in 5 minutes |
| Any listener reconnect failure | any in 60 seconds |
| Lost Staff answer race ratio above 0.5 | more than half in 5 minutes |
| At least ten Staff answers in five minutes | contention-volume guard |
| Answer-to-Bridge p95 above eight seconds | p95 over 10 minutes for 60 seconds |
| Any terminal Staff occupancy beyond reconciliation window | any sampled occupancy for 60 seconds after the 60-second reconciliation window |

Load evidence must replace the receipt-depth and Staff-answer-contention hypotheses
with measured baselines before the production gate is complete.

## Live gates

- Apply into the real project and verify every metric descriptor is accepted.
- Send one PHI-free synthetic observation for each signal and confirm ingestion.
- Trigger one reversible test incident per policy.
- Confirm the configured notification channel receives and resolves the test
  incident.
- Verify worker-pool metrics use `cloud_run_worker_pool` and request runtime
  metrics use `cloud_run_revision` in the deployed project.

## Google Cloud references

- [Counter log-based metrics](https://docs.cloud.google.com/logging/docs/logs-based-metrics/counter-metrics)
- [Distribution log-based metrics](https://docs.cloud.google.com/logging/docs/logs-based-metrics/distribution-metrics)
- [LogMetric REST schema](https://docs.cloud.google.com/logging/docs/reference/v2/rest/v2/projects.metrics)
- [AlertPolicy REST schema](https://docs.cloud.google.com/monitoring/api/ref_v3/rest/v3/projects.alertPolicies)
- [`gcloud monitoring policies create`](https://docs.cloud.google.com/sdk/gcloud/reference/monitoring/policies/create)
- [`gcloud monitoring policies update`](https://docs.cloud.google.com/sdk/gcloud/reference/monitoring/policies/update)
- [Cloud Run monitored resource types](https://docs.cloud.google.com/run/docs/monitoring)
