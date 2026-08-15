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
| `acuity_call_center_database_pool_acquire` | `seconds` | `outcome` | PostgreSQL adapter |
| `acuity_call_center_database_pool` | `acquired`, `idle`, `max`, `saturation_ratio` | None | Runtime |
| `acuity_call_center_sse_stream` | `active` | `state`, `reason` | Realtime |
| `acuity_call_center_sse_listener` | None | `state`, `reconnect` | Realtime |
| `acuity_call_center_staff_answer` | None | `outcome` | HumanCalling |
| `acuity_call_center_answer_to_bridge` | `seconds` | None | HumanCalling |

Allowed outcomes and actions are declared in
`backend/internal/observability/observability.go`. Unknown values become
`other`; they never become a new label.

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
