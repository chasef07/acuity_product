# Lean production monthly cost estimate

Expected Google Cloud infrastructure cost is approximately **$200/month USD**,
with a **$225/month planning budget**, before credits, free tiers, committed-use
discounts, taxes, and Telnyx charges.

This is a rate-card model dated 2026-07-29, not a billing quote or deployed
usage measurement. It assumes `us-east1`, 730 hours/month, 10 Staff logged in
for 176 hours/month, one realtime instance active for those hours, the initial
50 GiB Cloud SQL disk, 50 GiB of used Cloud SQL backups, and 50 GB of
recordings in regional Standard storage.

| Item | Assumption | Monthly |
| --- | --- | ---: |
| Cloud SQL CPU | Enterprise general-purpose, 2 × $30.149/vCPU-month | $60.30 |
| Cloud SQL memory | 8 × $5.11/GiB-month | $40.88 |
| Cloud SQL SSD | 50 × $0.34/GiB-month | $17.00 |
| Cloud SQL backups | 50 GiB in `us-east1` × $0.08/GiB-month | $4.00 |
| Worker pool | 1 vCPU / 0.5 GiB, one fixed instance for 730 hours | $31.17 |
| Warm portal + ingress | two request-billed minimum instances, idle baseline | $19.71 |
| Realtime | one warm instance; 176 active hours and 554 idle hours | $23.48 |
| Web, request bursts, migration | low observed pilot usage allowance | $2.00 |
| Recording storage | 50 GB Standard storage in `us-east1` | $1.00 |
| Secrets and image storage | 15 active secret versions and about 1 GB image storage | $0.64 |
| Logging | below the first 50 GiB/project/month | $0.00 |
| **Expected total** | before free tiers or discounts | **$200.18** |

Rates and billing behavior come from Google's current
[Cloud SQL pricing](https://cloud.google.com/sql/pricing),
[Cloud Run pricing](https://cloud.google.com/run/pricing),
[Cloud Storage pricing example](https://cloud.google.com/storage/pricing-examples),
[Secret Manager pricing](https://cloud.google.com/secret-manager/pricing), and
[Cloud Logging pricing](https://cloud.google.com/logging).

The $225 planning budget leaves about $25 for higher active-request time,
artifact growth, secret access, log volume, and small rate/usage variance. The
estimate excludes Telnyx voice/SMS/recording charges, SMTP, domains, internet
egress, Cloud Build, support plans, and the temporary Cloud SQL instance used
during a restore rehearsal. Storage and backup costs grow with retained data.
Cloud SQL storage auto-increase protects availability, but any increase makes
this estimate stale and requires a reviewed cost-baseline update.

The major accepted saving is the zonal Enterprise database. It removes the
Enterprise Plus, data-cache, and regional-HA premium, but it also removes
automatic failover. The resulting availability tradeoff is part of the runtime
contract and cannot be hidden by the cost estimate.
