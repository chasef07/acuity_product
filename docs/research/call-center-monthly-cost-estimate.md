# Call-center backend monthly cost estimate

Date: 2026-07-29

## Verdict

Budget **$550–$650 per month** for the reviewed Google Cloud production
backend at the current traffic level. A reasonable planning midpoint is
**about $575 per month**.

This estimate covers the checked production runtime contract: seven warm Cloud
Run request-service instances, two continuously running worker-pool instances,
regional high-availability Cloud SQL for PostgreSQL Enterprise Plus, and the
checked observability configuration. It does not include Telnyx call minutes,
phone numbers, SIP or recording charges; LiveKit; Vercel; email; support plans;
or engineering/on-call labor.

The observed 5,544 provider webhook events per day are used only to bound
request activity. Webhook events are not call counts and therefore cannot be
used to price voice-provider usage.

## Planning configuration

- Region: `us-central1`, 730 hours per month.
- Cloud Run request services: request-based billing, default
  `1 vCPU / 512 MiB`, seven total minimum instances.
- Cloud Run worker pool: two fixed `1 vCPU / 512 MiB` instances.
- Cloud SQL: PostgreSQL Enterprise Plus, regional HA,
  `db-perf-optimized-N-2` (`2 vCPU / 16 GiB`), 50 GiB HA SSD, and 50 GiB of
  used backups.
- Cloud SQL data cache: disabled until measured evidence justifies it.
- No committed-use discounts.
- Logging remains below 50 GiB per project per month, and custom metric
  ingestion remains below 150 MiB per billing account per month.

The deploy script currently omits explicit CPU, memory, billing-mode, Cloud SQL
machine, storage, and data-cache flags. Those inputs must be locked before this
becomes a quote rather than an estimate.

## Monthly estimate

| Component | Estimate | Basis |
| --- | ---: | --- |
| Seven warm Cloud Run services | $75–$105 | $63.77 idle floor after the unused free-tier discount, plus active HTTP and workday SSE time |
| Two fixed Cloud Run workers | $57 | Current unit-price table, after the unused worker-pool free-tier discount |
| Cloud SQL Enterprise Plus regional HA compute | $369 | 2 HA vCPUs and 16 GiB HA memory |
| 50 GiB HA SSD plus 50 GiB used backups | $21 | $17 storage plus $4 backups |
| Logging, custom metrics, and 17 alert conditions | $0–$10 | Current volume is expected to remain in ingestion free tiers; allow for the announced alerting charge |
| Artifact Registry, builds, secrets, and small network variance | $5–$15 | Operational allowance |
| **Expected total** | **$550–$650** | **About $575 planning midpoint** |

Cloud Run's current `us-central1` request-based rates are
$0.000024 per active vCPU-second, $0.0000025 per idle minimum-instance
vCPU-second, $0.0000025 per GiB-second, and $0.40 per million requests. The free
tier is a billing-account-level spending discount, not a separate allowance for
each service. Worker-pool rates are $0.000011244 per vCPU-second and
$0.000001235 per GiB-second. See
[Cloud Run pricing](https://cloud.google.com/run/pricing).

Cloud SQL Enterprise Plus regional-HA rates in `us-central1` are $0.1074 per
vCPU-hour and $0.0182 per GiB-hour. HA SSD is $0.000465753 per GiB-hour and
used backups are $0.000109589 per GiB-hour. See
[Cloud SQL pricing](https://cloud.google.com/sql/pricing).

Enterprise Plus uses predefined machines. Its smallest N2 machine is 2 vCPUs
and 16 GiB; the 8 GiB-per-vCPU ratio prevents using a 2-vCPU/8-GiB estimate.
See
[Cloud SQL PostgreSQL instance settings](https://docs.cloud.google.com/sql/docs/postgres/instance-settings).

Cloud Logging includes the first 50 GiB per project per month, and Cloud
Monitoring includes the first 150 MiB of byte-priced metrics per billing
account. Google has announced alerting pricing of $0.35 per metric reference
plus query-point charges, with charging starting no sooner than September 1,
2026. See
[Google Cloud Observability pricing](https://cloud.google.com/products/observability/pricing).

## Sensitivities

- Leaving the default 375 GiB N2 data cache enabled adds about **$120/month**
  for regional HA. Disable it initially; the current workload has no evidence
  that it needs the cache.
- Moving Cloud SQL to `4 vCPU / 32 GiB` adds about **$369/month** before any
  larger storage or cache requirement, bringing the system near
  **$900–$1,050/month**.
- Continuous SSE makes a request-service instance active-billed. Two realtime
  instances active for eight hours on 22 workdays add about **$27/month**
  above their idle minimum-instance cost.
- A one-year Cloud SQL commitment would reduce the database compute portion by
  about 25%, or roughly **$92/month**, but should be purchased only after
  production measurements confirm the machine size.
- A Serverless VPC Access connector, cross-region traffic, unusually verbose
  logs, or longer log retention would add cost and is not included.

## Recommendation

Set an initial **$700 monthly budget alert**. Explicitly pin Cloud Run resource
sizes and request-based billing, deploy the 2-vCPU/16-GiB regional-HA database
with data cache disabled, and compare this estimate with the first seven days
of billing export. Do not shrink the two ingress, realtime, or worker replicas
to save roughly tens of dollars; the database configuration, especially data
cache and machine size, is the meaningful cost lever.
