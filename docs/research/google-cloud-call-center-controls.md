# Google Cloud call-center control verification

Verified against current Google Cloud documentation on 2026-07-29. This review is static: it did not call Google Cloud APIs or change cloud resources.

## Result

The Cloud Logging metric definitions and Cloud Monitoring condition shapes conform to the documented APIs. The Cloud Run commands use supported flags, worker-pool logs can use `cloud_run_worker_pool`, and `gcloud logging metrics update ... --config-from-file` is an upsert.

The review found two implementation defects:

1. The service deploys use revision-level minimum and maximum instance flags even though the production contract treats the maximum as a service-wide database-capacity bound.
2. Each alert-policy update replaces conditions without preserving their server-assigned names, so an unchanged re-apply deletes and recreates every condition.

A third, lower-severity control risk was that alert-policy identity was inferred from a non-unique display name. The implementation now uses service-level bounds, preserves policy and condition resource names, and resolves policies by an immutable per-policy label.

## Cloud Run contracts

### Confirmed interface

- `gcloud run deploy` supports `--concurrency`, `--min-instances`, and `--max-instances`. It also supports the distinct service-level flags `--min` and `--max`. `--concurrency` is the per-container concurrent-request limit. [`gcloud run deploy`](https://cloud.google.com/sdk/gcloud/reference/run/deploy)
- `--min-instances` and `--max-instances` apply to one revision. `--min` and `--max` apply to the service and are divided among revisions receiving traffic. [`gcloud run deploy`](https://cloud.google.com/sdk/gcloud/reference/run/deploy)
- `gcloud run worker-pools deploy ... --instances=N` is the supported command and configures a fixed positive instance count. [`gcloud run worker-pools deploy`](https://cloud.google.com/sdk/gcloud/reference/run/worker-pools/deploy)
- `cloud_run_worker_pool` is a current monitored-resource type. Its labels are `project_id`, `worker_pool_name`, `revision_name`, `location`, and `configuration_name`. [`cloud_run_worker_pool` resource descriptor](https://cloud.google.com/monitoring/api/resources#tag_cloud_run_worker_pool)

### Finding: the database-capacity maximum was revision-scoped

The reviewed version of `deploy/cloud-run-commands.example.sh` passed the production contract values through `--min-instances` and `--max-instances`. Those flags are valid, but Google documents them as revision-level controls. During a traffic split, more than one revision could therefore consume instances under separate revision limits. That made the script's calculated connection requirement an incomplete upper bound for the service during rollout.

For a service-wide database safety invariant, apply the contract's total cap with `--max "$maximum"` (and its warm-instance target with `--min "$minimum"`). A revision-level cap can remain as an additional inner bound, but it cannot be the only control used to prove the total Cloud SQL connection budget.

The worker-pool command is contract-correct: `--instances "$5"` is a fixed worker-pool count, and the worker log-metric filters use the documented monitored-resource type.

Resolution: the deployment command now uses service-level `--min` and `--max`
for request services. Capacity counts those service maxima once across traffic
splits and applies the explicit overlap multiplier only to worker-pool
revisions. The fixed worker-pool instance count remains unchanged.

## Logs-based metric contracts

### Confirmed JSON shape

The [`LogMetric` REST resource](https://cloud.google.com/logging/docs/reference/v2/rest/v2/projects.metrics#LogMetric) defines:

- top-level `name`, `description`, `filter`, `metricDescriptor`, `valueExtractor`, `labelExtractors`, and `bucketOptions`;
- a default counter descriptor of `DELTA` / `INT64` when no descriptor is supplied;
- a distribution descriptor of `DELTA` / `DISTRIBUTION`, with a required `valueExtractor` whose result converts to a double;
- one `labelExtractors` entry for every label declared in `metricDescriptor.labels`; and
- required `bucketOptions` for distributions. Linear, exponential, and explicit layouts have the same JSON shapes used here. [`Distribution metric buckets`](https://cloud.google.com/logging/docs/logs-based-metrics/distribution-metrics#histogram-buckets)

The current `deploy/observability/log-metrics.json` contains 22 metrics: 7 counters and 15 distributions. Static validation found:

- every metric label has a matching extractor and no extractor lacks a descriptor label;
- every counter omits `valueExtractor` and `bucketOptions`;
- every distribution is `DELTA` / `DISTRIBUTION` and supplies both `valueExtractor` and `bucketOptions`; and
- each bucket layout uses the documented field names.

No JSON-schema defect was found in this file.

### `update` creates an absent metric

The Logging API's update method explicitly “creates or updates” a logs-based metric, and states that a new metric is created when the named metric does not exist. [`projects.metrics.update`](https://cloud.google.com/logging/docs/reference/v2/rest/v2/projects.metrics/update)

Therefore `apply.mjs:63-73` can intentionally use:

```sh
gcloud logging metrics update NAME --config-from-file FILE
```

for both first creation and later updates. No preceding `describe` or separate `create` branch is required.

One immutable-schema constraint still matters operationally: after initial creation, `metricDescriptor.metricKind` and `valueType` cannot be changed, and existing labels cannot be removed or have their types changed. [`LogMetric.metricDescriptor`](https://cloud.google.com/logging/docs/reference/v2/rest/v2/projects.metrics#LogMetric.FIELDS.metric_descriptor) A future incompatible schema change therefore needs a new metric name rather than an in-place apply.

## Alert-policy contracts

### Thresholds, ratios, aggregation, and duration

Cloud Monitoring represents both ordinary thresholds and metric ratios with `conditionThreshold`. A ratio adds `denominatorFilter` and `denominatorAggregations`; there is no separate `conditionRatio` union member. Numerator and denominator aggregations must use the same alignment period and produce time series with the same periodicity and labels. [`AlertPolicy MetricThreshold`](https://cloud.google.com/monitoring/api/ref_v3/rest/v3/projects.alertPolicies#MetricThreshold)

For metric-threshold conditions:

- `duration` is required and must be zero or a multiple of 60 seconds;
- `alignmentPeriod` must be at least 60 seconds;
- a `crossSeriesReducer` requires a non-`ALIGN_NONE` `perSeriesAligner` and an `alignmentPeriod`; and
- percentile aligners are valid for `GAUGE` or `DELTA` distribution metrics. [`MetricThreshold and Aggregation`](https://cloud.google.com/monitoring/api/ref_v3/rest/v3/projects.alertPolicies#Aggregation)

The current `deploy/observability/alert-policies.json` contains 8 policies and 17 conditions. Static validation found:

- every condition uses the documented `conditionThreshold` member;
- every duration is `0s`, `60s`, or `300s`;
- every reducer has both a valid aligner and an alignment period of at least 60 seconds;
- all percentile conditions select distribution metrics; and
- the accept-contention ratio's numerator and denominator aggregations are identical, preserving `resource.label.service_name` on both sides.

No threshold, ratio, aggregation, or duration-schema defect was found in this file.

### Confirmed `gcloud` policy commands

- Creation accepts a JSON or YAML policy through `--policy-from-file` and notification-channel IDs or full resource names through `--notification-channels`. [`gcloud monitoring policies create`](https://cloud.google.com/sdk/gcloud/reference/monitoring/policies/create)
- Update requires the alert-policy ID or full resource name, accepts `--policy-from-file`, and supports `--set-notification-channels` to replace the channel list. Without `--fields`, a file-based update replaces the policy and then applies explicit setting flags. [`gcloud monitoring policies update`](https://cloud.google.com/sdk/gcloud/reference/monitoring/policies/update)

The command and notification-channel flags in `apply.mjs:137-161` are therefore valid.

### Finding: updates recreated every condition

The policy files intentionally omit all server-assigned names, and the reviewed `apply.mjs` performed a full policy replacement. Google documents that, on update, a condition with its existing `name` is updated, a condition without a name is added, and existing conditions not named in the update are deleted. Google specifically recommends preserving condition IDs for small threshold, duration, or trigger changes. [`AlertPolicy Condition`](https://cloud.google.com/monitoring/api/ref_v3/rest/v3/projects.alertPolicies#Condition)

The required correction was to fetch the existing policy and carry each matched condition's `name` into the replacement document before calling update, failing closed if existing condition display names are absent or duplicated.

Resolution: the apply path now reads existing policies once, preserves the
matched policy and condition names, and fails closed on missing or duplicate
existing condition identities.

### Risk: display name is not a stable policy identity

The reviewed apply path located policies only by `displayName`. Google explicitly says an alert-policy display name is not unique. [`AlertPolicy.displayName`](https://cloud.google.com/monitoring/api/ref_v3/rest/v3/projects.alertPolicies#AlertPolicy.FIELDS.display_name)

The multiple-match check prevents an arbitrary update, which is good, but a rename creates a second policy instead of updating the first, and concurrent first applies can race. For a repeatable production control, persist the returned alert-policy resource name or assign a per-policy immutable user label and resolve exactly one policy by that identity. Keep the current fail-closed duplicate check.

Resolution: each checked policy now carries a unique immutable `acuity_policy`
label. Apply resolves that label locally from the policy list and fails closed
if more than one resource has the key.

## Implemented correction order

1. Enforce service-level `--max` before treating the database connection calculation as a hard production bound.
2. Preserve condition resource names on policy update so re-applying an unchanged definition is identity-stable.
3. Replace display-name discovery with stable policy identity.
4. Keep the metric and alert JSON shapes unchanged unless live API validation identifies a project-specific incompatibility.
