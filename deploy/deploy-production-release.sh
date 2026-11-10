#!/usr/bin/env bash

set -Eeuo pipefail

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Required deployment command is unavailable: $1" >&2
    exit 1
  fi
}

# Check every local dependency before even reading cloud state. In particular,
# runtime verification must be executable before migration or traffic changes.
for executable in dirname awk curl gcloud python3; do
  require_command "$executable"
done

script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
database_contract="$script_directory/production-runtime-contract.json"
python3 "$script_directory/verify-production-runtime.py" "$database_contract" --check-contract

require_value() {
  local name="$1"
  local value="${!name:-}"
  if [[ -z "$value" || "$value" == "DO_NOT_DEPLOY" ]]; then
    echo "$name must be configured." >&2
    exit 1
  fi
}

for name in \
  PROJECT_ID \
  REGION \
  REPOSITORY \
  BACKEND_IMAGE \
  WEB_IMAGE \
  IMAGE_TAG \
  DEPLOYMENT_ID \
  USABLE_DATABASE_CONNECTIONS \
  PORTAL_API_URL \
  REALTIME_URL; do
  require_value "$name"
done

if [[ ! "$IMAGE_TAG" =~ ^[0-9a-f]{40}$ ]]; then
  echo "IMAGE_TAG must be a full 40-character Git commit SHA." >&2
  exit 1
fi
if [[ ! "$DEPLOYMENT_ID" =~ ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ ]] ||
  ((${#DEPLOYMENT_ID} > 38)); then
  echo "DEPLOYMENT_ID must be a short lowercase Cloud Run revision suffix." >&2
  exit 1
fi

runtime_contract_values="$(
  awk '
    /"requiredDatabaseConnections"[[:space:]]*:/ {
      value = $0
      sub(/^.*"requiredDatabaseConnections"[[:space:]]*:[[:space:]]*/, "", value)
      sub(/[^0-9].*$/, "", value)
      required = value
      required_matches++
    }
    /"runtimes"[[:space:]]*:[[:space:]]*\[/ { in_runtimes = 1; next }
    in_runtimes && /^[[:space:]]*\{/ {
      in_runtime = 1
      name = minimum = maximum = pool = timeout = ""
      next
    }
    in_runtime && /"name"[[:space:]]*:/ {
      value = $0
      sub(/^.*"name"[[:space:]]*:[[:space:]]*"/, "", value)
      sub(/".*$/, "", value)
      name = value
    }
    in_runtime && /"minimumInstances"[[:space:]]*:/ {
      value = $0
      sub(/^.*"minimumInstances"[[:space:]]*:[[:space:]]*/, "", value)
      sub(/[^0-9].*$/, "", value)
      minimum = value
    }
    in_runtime && /"maximumInstances"[[:space:]]*:/ {
      value = $0
      sub(/^.*"maximumInstances"[[:space:]]*:[[:space:]]*/, "", value)
      sub(/[^0-9].*$/, "", value)
      maximum = value
    }
    in_runtime && /"poolMaximum"[[:space:]]*:/ {
      value = $0
      sub(/^.*"poolMaximum"[[:space:]]*:[[:space:]]*/, "", value)
      sub(/[^0-9].*$/, "", value)
      pool = value
    }
    in_runtime && /"acquisitionTimeoutMilliseconds"[[:space:]]*:/ {
      value = $0
      sub(/^.*"acquisitionTimeoutMilliseconds"[[:space:]]*:[[:space:]]*/, "", value)
      sub(/[^0-9].*$/, "", value)
      timeout = value
    }
    in_runtime && /^[[:space:]]*\}[,]?[[:space:]]*$/ {
      if (name == "worker") {
        worker_matches++
        worker_minimum = minimum
        worker_maximum = maximum
        worker_pool = pool
        worker_timeout = timeout
      }
      in_runtime = 0
      next
    }
    in_runtimes && /^[[:space:]]*\][,]?[[:space:]]*$/ { in_runtimes = 0 }
    END {
      if (required_matches != 1 || worker_matches != 1) exit 1
      print required "\t" worker_minimum "\t" worker_maximum "\t" worker_pool "\t" worker_timeout
    }
  ' "$database_contract"
)" || {
  echo "production runtime contract must define one capacity and worker profile." >&2
  exit 1
}
IFS=$'\t' read -r \
  required_database_connections \
  worker_contract_instances \
  worker_contract_maximum \
  worker_contract_pool \
  worker_contract_acquire_timeout <<<"$runtime_contract_values"
if [[ ! "$required_database_connections" =~ ^[0-9]+$ ]] ||
  ((10#$required_database_connections < 1)); then
  echo "production runtime contract must render positive database capacity." >&2
  exit 1
fi
for value in \
  "$worker_contract_instances" \
  "$worker_contract_maximum" \
  "$worker_contract_pool" \
  "$worker_contract_acquire_timeout"; do
  if [[ ! "$value" =~ ^[0-9]+$ ]] || ((10#$value < 1)); then
    echo "production runtime contract must render a positive worker profile." >&2
    exit 1
  fi
done
if [[ "$worker_contract_instances" != "$worker_contract_maximum" ]]; then
  echo "production worker instance range must be fixed." >&2
  exit 1
fi
if [[ ! "$USABLE_DATABASE_CONNECTIONS" =~ ^[0-9]+$ ]] ||
  ((10#$USABLE_DATABASE_CONNECTIONS < 1)); then
  echo "USABLE_DATABASE_CONNECTIONS must be a measured positive integer." >&2
  exit 1
fi
if ((10#$USABLE_DATABASE_CONNECTIONS < 10#$required_database_connections)); then
  echo "production requires at least $required_database_connections usable database connections; measured $USABLE_DATABASE_CONNECTIONS" >&2
  exit 1
fi

MIDDLEWARE_BASE_URL="${MIDDLEWARE_BASE_URL:-}"
MIDDLEWARE_API_SECRET_SECRET="${MIDDLEWARE_API_SECRET_SECRET:-}"
if [[ -n "$MIDDLEWARE_BASE_URL" || -n "$MIDDLEWARE_API_SECRET_SECRET" ]]; then
  if [[ -z "$MIDDLEWARE_BASE_URL" || -z "$MIDDLEWARE_API_SECRET_SECRET" ]]; then
    echo "MIDDLEWARE_BASE_URL and MIDDLEWARE_API_SECRET_SECRET must be set together or not at all." >&2
    exit 1
  fi
  if [[ ! "$MIDDLEWARE_BASE_URL" =~ ^https://[A-Za-z0-9.-]+(:[0-9]+)?(/[A-Za-z0-9._~/-]*)?$ ]]; then
    echo "MIDDLEWARE_BASE_URL must be an https:// base URL without credentials, query, or commas." >&2
    exit 1
  fi
  if [[ ! "$MIDDLEWARE_API_SECRET_SECRET" =~ ^[A-Za-z0-9_-]{1,255}$ ]]; then
    echo "MIDDLEWARE_API_SECRET_SECRET must be a Secret Manager secret name." >&2
    exit 1
  fi
fi

backend_tag="$REGION-docker.pkg.dev/$PROJECT_ID/$REPOSITORY/$BACKEND_IMAGE:$IMAGE_TAG"
web_tag="$REGION-docker.pkg.dev/$PROJECT_ID/$REPOSITORY/$WEB_IMAGE:$IMAGE_TAG"
backend_digest="$(
  gcloud artifacts docker images describe "$backend_tag" \
    --project "$PROJECT_ID" \
    --format 'value(image_summary.fully_qualified_digest)'
)"
web_digest="$(
  gcloud artifacts docker images describe "$web_tag" \
    --project "$PROJECT_ID" \
    --format 'value(image_summary.fully_qualified_digest)'
)"
if [[ ! "$backend_digest" =~ ^$REGION-docker\.pkg\.dev/$PROJECT_ID/$REPOSITORY/$BACKEND_IMAGE@sha256:[0-9a-f]{64}$ ]]; then
  echo "Backend tag did not resolve to the expected immutable digest." >&2
  exit 1
fi
if [[ ! "$web_digest" =~ ^$REGION-docker\.pkg\.dev/$PROJECT_ID/$REPOSITORY/$WEB_IMAGE@sha256:[0-9a-f]{64}$ ]]; then
  echo "Web tag did not resolve to the expected immutable digest." >&2
  exit 1
fi

services=(
  acuity-portal-api
  acuity-provider-ingress
  acuity-realtime
  acuity-web
)
backend_services=(
  acuity-portal-api
  acuity-provider-ingress
  acuity-realtime
)
service_runtime() {
  runtime_environment="DATABASE_POOL_MAX=1,DATABASE_ACQUIRE_TIMEOUT_MS=1500"
  runtime_secrets=""
  runtime_timeout=0
  case "$1" in
    acuity-portal-api)
      read -r concurrency minimum maximum <<<"8 1 3"
      runtime_environment="DATABASE_POOL_MAX=4,DATABASE_ACQUIRE_TIMEOUT_MS=1500"
      runtime_environment+=",HUMAN_CALLING_RING_WINDOW_SECONDS=20"
      if [[ -n "$MIDDLEWARE_BASE_URL" ]]; then
        runtime_environment+=",MIDDLEWARE_BASE_URL=${MIDDLEWARE_BASE_URL}"
        runtime_secrets="MIDDLEWARE_API_SECRET=${MIDDLEWARE_API_SECRET_SECRET}:latest"
      fi
      ;;
    acuity-provider-ingress)
      read -r concurrency minimum maximum <<<"20 1 2"
      ;;
    acuity-realtime)
      read -r concurrency minimum maximum <<<"50 1 2"
      runtime_environment+=",REALTIME_STREAM_SECONDS=270,REALTIME_STREAM_JITTER_SECONDS=30"
      runtime_timeout=300
      ;;
    acuity-web) read -r concurrency minimum maximum <<<"40 1 2" ;;
    *)
      echo "Unknown production service $1." >&2
      return 1
      ;;
  esac
}
service_url() {
  gcloud run services describe "$1" \
    --project "$PROJECT_ID" \
    --region "$REGION" \
    --format 'value(status.url)'
}

require_service_environment() {
  local service="$1"
  local name="$2"
  local configured_names
  configured_names="$(
    gcloud run services describe "$service" \
      --project "$PROJECT_ID" \
      --region "$REGION" \
      --format 'value(spec.template.spec.containers[0].env[].name)'
  )"
  case ";$configured_names;" in
    *";$name;"*) ;;
    *)
      echo "$service must configure $name before release." >&2
      return 1
      ;;
  esac
}
ensure_service_traffic() {
  local service="$1"
  local revision="$2"
  local configured_revisions
  local configured_percents
  configured_revisions="$(
    gcloud run services describe "$service" \
      --project "$PROJECT_ID" \
      --region "$REGION" \
      --format 'value(spec.traffic[].revisionName)'
  )"
  configured_percents="$(
    gcloud run services describe "$service" \
      --project "$PROJECT_ID" \
      --region "$REGION" \
      --format 'value(spec.traffic[].percent)'
  )"
  if [[ "$configured_revisions" == "$revision" && "$configured_percents" == "100" ]]; then
    return
  fi
  gcloud run services update-traffic "$service" \
    --project "$PROJECT_ID" \
    --region "$REGION" \
    --to-revisions "$revision=100" \
    --quiet
}
previous_portal_revision="$(
  gcloud run services describe acuity-portal-api \
    --project "$PROJECT_ID" \
    --region "$REGION" \
    --format 'value(status.latestReadyRevisionName)'
)"
previous_ingress_revision="$(
  gcloud run services describe acuity-provider-ingress \
    --project "$PROJECT_ID" \
    --region "$REGION" \
    --format 'value(status.latestReadyRevisionName)'
)"
previous_realtime_revision="$(
  gcloud run services describe acuity-realtime \
    --project "$PROJECT_ID" \
    --region "$REGION" \
    --format 'value(status.latestReadyRevisionName)'
)"
previous_web_revision="$(
  gcloud run services describe acuity-web \
    --project "$PROJECT_ID" \
    --region "$REGION" \
    --format 'value(status.latestReadyRevisionName)'
)"
for revision in \
  "$previous_portal_revision" \
  "$previous_ingress_revision" \
  "$previous_realtime_revision" \
  "$previous_web_revision"; do
  if [[ -z "$revision" ]]; then
    echo "Every service must have a ready rollback revision." >&2
    exit 1
  fi
done
previous_worker_revision="$(
  gcloud run worker-pools describe acuity-worker \
    --project "$PROJECT_ID" \
    --region "$REGION" \
    --format 'value(status.instanceSplits.revisionName)'
)"
if [[ -z "$previous_worker_revision" || "$previous_worker_revision" == *";"* ]]; then
  echo "acuity-worker must have exactly one active rollback revision." >&2
  exit 1
fi
require_service_environment \
  acuity-portal-api \
  HUMAN_CALLING_PLAYBACK_SIGNING_KEY
require_service_environment acuity-web GOOGLE_CLIENT_ID
require_service_environment acuity-web GOOGLE_CLIENT_SECRET
# The web image bakes in the API URLs it calls, so it must have been built for
# this project's services. Otherwise a staging portal would call production.
live_portal_api_url="$(service_url acuity-portal-api)"
live_realtime_url="$(service_url acuity-realtime)"
if [[ "$live_portal_api_url" != "$PORTAL_API_URL" ]]; then
  echo "PORTAL_API_URL must be this project's acuity-portal-api URL." >&2
  exit 1
fi
if [[ "$live_realtime_url" != "$REALTIME_URL" ]]; then
  echo "REALTIME_URL must be this project's acuity-realtime URL." >&2
  exit 1
fi

touched_services=()
worker_promoted=false
release_complete=false
previous_service_revision() {
  case "$1" in
    acuity-portal-api) printf '%s\n' "$previous_portal_revision" ;;
    acuity-provider-ingress) printf '%s\n' "$previous_ingress_revision" ;;
    acuity-realtime) printf '%s\n' "$previous_realtime_revision" ;;
    acuity-web) printf '%s\n' "$previous_web_revision" ;;
    *)
      echo "Unknown rollback service $1." >&2
      return 1
      ;;
  esac
}
for service in "${services[@]}"; do
  ensure_service_traffic "$service" "$(previous_service_revision "$service")"
done

rollback() {
  local exit_code=$?
  trap - ERR
  if [[ "$release_complete" == true ]]; then
    return
  fi
  set +e
  for ((index = ${#touched_services[@]} - 1; index >= 0; index--)); do
    service="${touched_services[$index]}"
    gcloud run services update-traffic "$service" \
      --project "$PROJECT_ID" \
      --region "$REGION" \
      --to-revisions "$(previous_service_revision "$service")=100" \
      --quiet
  done
  if [[ "$worker_promoted" == true ]]; then
    gcloud run worker-pools update-instance-split acuity-worker \
      --project "$PROJECT_ID" \
      --region "$REGION" \
      --to-revisions "$previous_worker_revision=100" \
      --quiet
  fi
  exit "$exit_code"
}
trap rollback ERR

verify_service_revision() {
  local revision="$1"
  local expected_image="$2"
  local actual_image
  local ready
  actual_image="$(
    gcloud run revisions describe "$revision" \
      --project "$PROJECT_ID" \
      --region "$REGION" \
      --format 'value(spec.containers[0].image)'
  )"
  ready="$(
    gcloud run revisions describe "$revision" \
      --project "$PROJECT_ID" \
      --region "$REGION" \
      --format 'value(status.conditions[0].status)'
  )"
  if [[ "$actual_image" != "$expected_image" || "$ready" != "True" ]]; then
    echo "$revision is not ready on the expected immutable image." >&2
    return 1
  fi
}

smoke() {
  curl \
    --fail \
    --silent \
    --show-error \
    --retry 5 \
    --retry-connrefused \
    --retry-delay 2 \
    "$1" >/dev/null
}

stage_backend_services() {
  for service in "${backend_services[@]}"; do
    service_runtime "$service"
    revision="$service-$DEPLOYMENT_ID"
    touched_services+=("$service")
    set -- gcloud run deploy "$service" \
      --project "$PROJECT_ID" \
      --region "$REGION" \
      --image "$backend_digest" \
      --revision-suffix "$DEPLOYMENT_ID" \
      --no-traffic \
      --cpu 1 \
      --memory 512Mi \
      --concurrency "$concurrency" \
      --min "$minimum" \
      --max "$maximum" \
      --update-env-vars "$runtime_environment"
    if [[ -n "$runtime_secrets" ]]; then
      set -- "$@" --update-secrets "$runtime_secrets"
    fi
    if ((runtime_timeout > 0)); then
      set -- "$@" --timeout "$runtime_timeout"
    fi
    set -- "$@" \
      --startup-probe "httpGet.path=/health/live,httpGet.port=8080,timeoutSeconds=1,periodSeconds=2,failureThreshold=15" \
      --readiness-probe "httpGet.path=/health/ready,httpGet.port=8080,timeoutSeconds=1,periodSeconds=2,failureThreshold=3" \
      --quiet
    "$@"
    verify_service_revision "$revision" "$backend_digest"
  done
}

stage_worker() {
  worker_revision="acuity-worker-$DEPLOYMENT_ID"
  gcloud run worker-pools deploy acuity-worker \
    --project "$PROJECT_ID" \
    --region "$REGION" \
    --image "$backend_digest" \
    --revision-suffix "$DEPLOYMENT_ID" \
    --no-promote \
    --cpu 1 \
    --memory 512Mi \
    --instances "$worker_contract_instances" \
    --update-env-vars "DATABASE_POOL_MAX=$worker_contract_pool,DATABASE_ACQUIRE_TIMEOUT_MS=$worker_contract_acquire_timeout,HUMAN_CALLING_RING_WINDOW_SECONDS=20" \
    --quiet
  worker_image="$(
    gcloud run worker-pools revisions describe "$worker_revision" \
      --project "$PROJECT_ID" \
      --region "$REGION" \
      --format 'value(spec.containers[0].image)'
  )"
  if [[ "$worker_image" != "$backend_digest" ]]; then
    echo "$worker_revision does not use the expected immutable image." >&2
    return 1
  fi
  gcloud run worker-pools update-instance-split acuity-worker \
    --project "$PROJECT_ID" \
    --region "$REGION" \
    --to-revisions "$worker_revision=100" \
    --quiet
  worker_promoted=true
}

run_migration() {
  gcloud run jobs update acuity-migrate \
    --project "$PROJECT_ID" \
    --region "$REGION" \
    --image "$backend_digest" \
    --tasks 1 \
    --max-retries 0 \
    --update-env-vars "DATABASE_POOL_MAX=1,DATABASE_ACQUIRE_TIMEOUT_MS=5000,PROVISIONING_INPUT=/etc/acuity/provisioning.json,PROVISIONING_OUTPUT=/tmp/provisioning-output.json,RESPONSIBILITIES_INPUT=/etc/acuity/task-responsibilities.json" \
    --quiet
  gcloud run jobs execute acuity-migrate \
    --project "$PROJECT_ID" \
    --region "$REGION" \
    --wait \
    --quiet
}

run_migration
stage_backend_services
stage_worker
for service in "${backend_services[@]}"; do
  revision="$service-$DEPLOYMENT_ID"
  gcloud run services update-traffic "$service" \
    --project "$PROJECT_ID" \
    --region "$REGION" \
    --to-revisions "$revision=100" \
    --quiet
done

worker_ready="$(
  gcloud run worker-pools describe acuity-worker \
    --project "$PROJECT_ID" \
    --region "$REGION" \
    --format 'value(status.conditions[0].status)'
)"
if [[ "$worker_ready" != "True" ]]; then
  echo "$worker_revision did not become ready." >&2
  false
fi
for service in "${backend_services[@]}"; do
  smoke "$(service_url "$service")/health/ready"
done

service_runtime acuity-web
web_revision="acuity-web-$DEPLOYMENT_ID"
touched_services+=("acuity-web")
gcloud run deploy acuity-web \
  --project "$PROJECT_ID" \
  --region "$REGION" \
  --image "$web_digest" \
  --revision-suffix "$DEPLOYMENT_ID" \
  --no-traffic \
  --cpu 1 \
  --memory 512Mi \
  --concurrency "$concurrency" \
  --min "$minimum" \
  --max "$maximum" \
  --update-env-vars "AUTH_DB_POOL_MAX=1,AUTH_DB_ACQUIRE_TIMEOUT_MS=1500" \
  --quiet
verify_service_revision "$web_revision" "$web_digest"
gcloud run services update-traffic acuity-web \
  --project "$PROJECT_ID" \
  --region "$REGION" \
  --to-revisions "$web_revision=100" \
  --quiet
web_url="$(service_url acuity-web)"
smoke "$web_url/sign-in"
smoke "$web_url/api/auth/get-session"
python3 \
  "$script_directory/verify-production-runtime.py" \
  "$database_contract" \
  "$PROJECT_ID" \
  "$REGION"

release_complete=true
trap - ERR
printf 'Released %s as %s and %s.\n' "$IMAGE_TAG" "$backend_digest" "$web_digest"
