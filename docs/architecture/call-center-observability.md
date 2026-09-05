# Call-center observability

Call-center metrics are emitted as structured `call_center_metric` logs. This is
the smallest production adapter for the current Cloud Run deployment: Cloud
Logging can create counters and distributions from the fixed `metric` names and
numeric fields without a public metrics endpoint or a vendor SDK in domain
modules.

Every record includes:

- `metric_contract`: contract version;
- `metric`: one fixed name from the table below;
- `runtime_role`: one of `portal-api`, `provider-ingress`, `realtime`, `worker`,
  or `migrate`; and
- `revision`: the bounded Cloud Run `K_REVISION`, or `unknown`.

## Metric contract

Each row is one emitted observation. Counters count matching records;
distributions extract the named duration field; capacity charts use the latest
numeric fields.

| Observation | Numeric fields | Bounded fields | Owner |
| --- | --- | --- | --- |
| `acuity_call_center_webhook_acknowledgement` | `seconds` | `outcome` | Provider ingress |
| `acuity_call_center_receipt_queue` | `depth`, `oldest_age_seconds`, `projection_retry_depth`, `related_fact_depth`, `quarantined_depth` | None | Receipt worker |
| `acuity_call_center_receipt_processing` | `queue_seconds`, `processing_seconds` | `outcome` | Receipt worker |
| `acuity_call_center_provider_command` | `queue_seconds`, `duration_seconds` | `action`, `outcome` | Command worker |
| `acuity_call_center_provider_command_stage` | `seconds` | `action`, `stage`, `outcome` | Command claim and execution |
| `acuity_call_center_database_pool_acquire` | `seconds` | `outcome` | PostgreSQL adapter, non-success only |
| `acuity_backend_database_execution` | `seconds` | `cause` | PostgreSQL adapter, failures only |
| `acuity_call_center_database_pool` | `acquired`, `idle`, `max`, `saturation_ratio` | None | Runtime |
| `acuity_call_center_sse_stream` | `active` | `state`, `reason` | Realtime |
| `acuity_call_center_sse_listener` | None | `state`, `reconnect` | Realtime |
| `acuity_call_center_staff_answer` | None | `outcome` | HumanCalling |
| `acuity_call_center_answer_to_bridge` | `seconds` | None | HumanCalling |

Allowed outcomes and actions are declared in
`backend/internal/observability/observability.go`. Unknown values become
`other`; they never become a new label.

Successful PostgreSQL statements and pool acquisitions are deliberately not
emitted as individual records. Cloud Run and Cloud SQL native metrics own
ordinary traffic and resource utilization, while the 30-second database-pool
snapshot preserves saturation evidence. Every bounded non-success acquisition
and execution cause remains an individual record for alerting and diagnosis.

The legacy provider-command `queue_seconds` measures creation to provider
dispatch on ordinary execution, and creation to observation on reconciliation.
Its `duration_seconds` also includes durable result processing after the provider
request. These historical fields retain their values for continuity; neither is
an isolated database claim or provider-request latency.

The separate stage observation records directly observed durations:

| Stage | Interval |
| --- | --- |
| `claim` | Start of the claim operation through its committed result, including acquisition, SQL and lock waits |
| `created_to_first_claim` | Database creation timestamp through observed first claim completion; first attempts only |
| `claim_to_dispatch` | Claim completion through provider dispatch, including executor handoff and command reload |
| `provider` | External provider execution, before result persistence |
| `persist` | Durable provider-result processing |

Fixed outcomes distinguish success and failure. Successful empty polls do not
emit a timing observation. Each stage is recorded when it completes, so a later
persistence failure cannot erase the earlier timing evidence. Reconciliation
does not emit a new first-claim sample.
The database creation timestamp is a transaction timestamp, not a commit
timestamp; `created_to_first_claim` therefore includes time before insertion
becomes visible, scheduled delay, dependencies and active-command serialization.
It must not be described as eligible-to-claim latency.

Independent `DIAL_STAFF` commands for distinct Staff CallLegs may be claimed
concurrently. An active non-Dial command on the same Call can still block them.
There is no exact durable eligibility timestamp across these blockers. Diagnose
the directly observed stages and dependency state before changing scheduling or
adding such a timestamp. Long-lived realtime SSE request duration is likewise
not ordinary HTTP response latency.

## Privacy and cardinality

Metric records must not contain Practice, Location, User, Call, receipt,
command, provider, phone, email, URL, SQL, raw error, or request identifiers.
They must never contain a webhook `raw_body`.

The receipt lane may derive observations only from bounded state plus numeric
timing/count fields. Its currently safe inputs are `state`,
`projection_attempts`, `projection_error_code`, `last_attempt_at`,
`next_attempt_at`, `quarantined_at`, `duplicate_count`, `event_type`,
`received_at`, and whether `call_id` is set. Neither the actual `call_id`,
`event_type`, nor `projection_error_code` should become a metric label.

## Initial alerts

- Alert if webhook acknowledgement p99 exceeds one second or any
  `unavailable` acknowledgement occurs.
- Alert if oldest receipt age exceeds 30 seconds, receipt depth rises for five
  minutes, or the periodically sampled durable quarantine depth is above zero.
  Split the sampled queue into transient `projection_retry_depth` and
  out-of-order `related_fact_depth`; processing outcomes separately identify
  terminal `obsolete` evidence without turning provider identifiers into labels.
  The quarantine incident remains active until audited requeue clears the
  durable state.
- Alert if Dial queue p95 exceeds one second, provider-command ambiguity rises,
  or pool saturation remains at or above 0.8.
- Alert if any pool acquisition exhausts its deadline, the SSE listener
  repeatedly disconnects, or any reconnect attempt fails. Client and
  shutdown cancellation is reported as `canceled`, not `timeout`.
- Track `lost_race` Staff answers as expected contention, but alert on a sharp
  change in their ratio to all Staff answers.
- Alert if answer-to-bridge p95 exceeds eight seconds.

Thresholds are starting operating hypotheses. The load/failure workstream must
replace them with measured baselines before declaring the production gate
complete.

Deployable Google Cloud `LogMetric` and `AlertPolicy` definitions, their offline
contract checks, and the live delivery gates are documented in
[`deploy/observability/README.md`](../../deploy/observability/README.md).
