#!/usr/bin/env sh
set -eu

# This command creates the checked production database target. Review the
# rendered values and run it only as an approved production change.

: "${GCP_PROJECT:?required}"
: "${CLOUD_SQL_INSTANCE_NAME:?required}"
: "${GCP_REGION:=us-east1}"

SCRIPT_DIRECTORY=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
CONTRACT_ROWS=$(
  node "$SCRIPT_DIRECTORY/render-production-runtime-contract.mjs" \
    "$SCRIPT_DIRECTORY/production-runtime-contract.json"
)
database_row=$(
  printf '%s\n' "$CONTRACT_ROWS" |
    awk -F '\t' '$1 == "database" { print }'
)
if ! IFS="$(printf '\t')" read -r \
  row_name \
  row_kind \
  row_concurrency \
  row_minimum \
  row_maximum \
  row_pool \
  row_dedicated \
  row_timeout \
  row_request_timeout \
  row_stream_maximum \
  row_stream_jitter \
  row_retries \
  vcpus \
  memory_mib \
  row_billing \
  region \
  version \
  edition \
  availability \
  storage_gib \
  storage_type \
  backup_start_time \
  transaction_log_days \
  retained_backups \
  automated_backups \
  point_in_time_recovery \
  deletion_protection \
  data_cache \
  storage_auto_increase \
  backup_location <<EOF
$database_row
EOF
then
  echo "database contract row is missing" >&2
  exit 1
fi

if [ "$GCP_REGION" != "$region" ]; then
  echo "production region must be ${region}; configured ${GCP_REGION}" >&2
  exit 1
fi
if [ "$row_name" != database ] ||
  [ "$row_kind" != database ] ||
  [ "$backup_location" != "$region" ]; then
  echo "database contract row does not match the regional production target" >&2
  exit 1
fi
if [ "$automated_backups" != 1 ] ||
  [ "$point_in_time_recovery" != 1 ] ||
  [ "$deletion_protection" != 1 ] ||
  [ "$data_cache" != 0 ] ||
  [ "$storage_auto_increase" != 1 ]; then
  echo "database recovery and cost controls do not match the checked contract" >&2
  exit 1
fi

gcloud sql instances create "$CLOUD_SQL_INSTANCE_NAME" \
  --project "$GCP_PROJECT" \
  --region "$region" \
  --database-version "$version" \
  --edition "$edition" \
  --availability-type "$availability" \
  --cpu "$vcpus" \
  --memory "${memory_mib}MiB" \
  --storage-size "$storage_gib" \
  --storage-type "$storage_type" \
  --backup \
  --backup-location "$backup_location" \
  --backup-start-time "$backup_start_time" \
  --enable-point-in-time-recovery \
  --retained-transaction-log-days "$transaction_log_days" \
  --retained-backups-count "$retained_backups" \
  --deletion-protection \
  --no-enable-data-cache \
  --storage-auto-increase
