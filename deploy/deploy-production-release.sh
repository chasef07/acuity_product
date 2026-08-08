#!/usr/bin/env bash

set -Eeuo pipefail

destructive_cutover="${CALLLEG_DESTRUCTIVE_CUTOVER:-false}"
schema_cutover_complete="${CALLLEG_SCHEMA_CUTOVER_COMPLETE:-false}"
script_directory="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if [[ "$destructive_cutover" == true ]]; then
  evidence_path="${CALLLEG_CUTOVER_EVIDENCE_PATH:-}"
  if [[ "${CALLLEG_CUTOVER_EVIDENCE_VERIFIED:-false}" != true ||
    "${CALLLEG_CUTOVER_WINDOW_CONFIRMED:-false}" != true ||
    -z "$evidence_path" || ! -f "$evidence_path" ]]; then
    echo "The destructive CallLeg release requires a confirmed zero-runtime window and verified cutover evidence." >&2
    exit 1
  fi
  node \
    "$script_directory/check-telnyx-callleg-cutover.mjs" \
    "$script_directory/telnyx-callleg-cutover-contract.json" \
    "$evidence_path"
elif [[ "$schema_cutover_complete" != true ]]; then
  echo "Automatic release is paused until the scheduled CallLeg cutover completes." >&2
  exit 1
fi

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
  DEPLOYMENT_ID; do
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
  runtime_timeout=0
  case "$1" in
    acuity-portal-api)
      read -r concurrency minimum maximum <<<"20 1 3"
      runtime_environment+=",HUMAN_CALLING_RING_WINDOW_SECONDS=20"
      if [[ "$destructive_cutover" == true ]]; then
        runtime_environment+=",HUMAN_CALLING_HANDOFF_ADMISSION=closed"
      fi
      ;;
    acuity-provider-ingress)
      read -r concurrency minimum maximum <<<"20 1 2"
      if [[ "$destructive_cutover" == true ]]; then
        runtime_environment+=",HUMAN_CALLING_HANDOFF_ADMISSION=closed"
      fi
      ;;
    acuity-realtime)
      read -r concurrency minimum maximum <<<"50 1 2"
      runtime_environment+=",REALTIME_STREAM_SECONDS=270,REALTIME_STREAM_JITTER_SECONDS=30"
      runtime_timeout=300
      ;;
    acuity-web) read -r concurrency minimum maximum <<<"40 0 2" ;;
    *)
      echo "Unknown production service $1." >&2
      return 1
      ;;
  esac
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

touched_services=()
worker_promoted=false
release_complete=false
migration_started=false
disabled_service_count=0
worker_stop_attempted=false
previous_scaling_modes=()
previous_manual_instances=()
previous_worker_instances=""
if [[ "$destructive_cutover" == true ]]; then
  for service in "${services[@]}"; do
    previous_scaling_modes+=("$(
      gcloud run services describe "$service" \
        --project "$PROJECT_ID" \
        --region "$REGION" \
        --format 'value(spec.scaling.scalingMode)'
    )")
    previous_manual_instances+=("$(
      gcloud run services describe "$service" \
        --project "$PROJECT_ID" \
        --region "$REGION" \
        --format 'value(spec.scaling.manualInstanceCount)'
    )")
  done
  previous_worker_instances="$(
    gcloud run worker-pools describe acuity-worker \
      --project "$PROJECT_ID" \
      --region "$REGION" \
      --format 'value(spec.template.scaling.manualInstanceCount)'
  )"
fi
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
if [[ "$destructive_cutover" != true ]]; then
  for service in "${services[@]}"; do
    ensure_service_traffic "$service" "$(previous_service_revision "$service")"
  done
fi

rollback() {
  local exit_code=$?
  trap - ERR
  if [[ "$release_complete" == true ]]; then
    return
  fi
  if [[ "$destructive_cutover" == true && "$migration_started" == true ]]; then
    echo "CallLeg cutover failed after migration began. Keep handoff admission closed; restore the recorded snapshot and old revisions, or forward-fix the replacement." >&2
    exit "$exit_code"
  fi
  set +e
  if [[ "$destructive_cutover" == true ]]; then
    for ((index = disabled_service_count - 1; index >= 0; index--)); do
      service="${services[$index]}"
      if [[ "${previous_scaling_modes[$index]}" == MANUAL ]]; then
        scaling="${previous_manual_instances[$index]}"
      else
        scaling=auto
      fi
      gcloud run services update "$service" \
        --project "$PROJECT_ID" \
        --region "$REGION" \
        --scaling "$scaling" \
        --quiet
    done
    if [[ "$worker_stop_attempted" == true && -n "$previous_worker_instances" ]]; then
      gcloud run worker-pools update acuity-worker \
        --project "$PROJECT_ID" \
        --region "$REGION" \
        --instances "$previous_worker_instances" \
        --quiet
    fi
  fi
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

service_url() {
  gcloud run services describe "$1" \
    --project "$PROJECT_ID" \
    --region "$REGION" \
    --format 'value(status.url)'
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
    if ((runtime_timeout > 0)); then
      set -- "$@" --timeout "$runtime_timeout"
    fi
    set -- "$@" \
      --startup-probe "httpGet.path=/health/live,httpGet.port=8080,timeoutSeconds=1,periodSeconds=2,failureThreshold=15" \
      --readiness-probe "httpGet.path=/health/ready,httpGet.port=8080,timeoutSeconds=1,periodSeconds=2,failureThreshold=3" \
      --quiet
    "$@"
    verify_service_revision "$revision" "$backend_digest"
    if [[ "$destructive_cutover" == true && \
      ("$service" == acuity-portal-api || "$service" == acuity-provider-ingress) ]]; then
      admission="$(
        gcloud run revisions describe "$revision" \
          --project "$PROJECT_ID" \
          --region "$REGION" \
          --format "value(spec.containers[0].env[?name='HUMAN_CALLING_HANDOFF_ADMISSION'].value)"
      )"
      if [[ "$admission" != closed ]]; then
        echo "$revision must close handoff admission before cutover traffic." >&2
        return 1
      fi
    fi
  done
}

stage_worker() {
  local instances="$1"
  local worker_environment="DATABASE_POOL_MAX=1,DATABASE_ACQUIRE_TIMEOUT_MS=1500,HUMAN_CALLING_RING_WINDOW_SECONDS=20"
  if [[ "$destructive_cutover" == true ]]; then
    worker_environment+=",HUMAN_CALLING_HANDOFF_ADMISSION=closed"
  fi
  worker_revision="acuity-worker-$DEPLOYMENT_ID"
  gcloud run worker-pools deploy acuity-worker \
    --project "$PROJECT_ID" \
    --region "$REGION" \
    --image "$backend_digest" \
    --revision-suffix "$DEPLOYMENT_ID" \
    --no-promote \
    --cpu 1 \
    --memory 512Mi \
    --instances "$instances" \
    --update-env-vars "$worker_environment" \
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
  if [[ "$destructive_cutover" == true ]]; then
    worker_admission="$(
      gcloud run worker-pools revisions describe "$worker_revision" \
        --project "$PROJECT_ID" \
        --region "$REGION" \
        --format "value(spec.containers[0].env[?name='HUMAN_CALLING_HANDOFF_ADMISSION'].value)"
    )"
    if [[ "$worker_admission" != closed ]]; then
      echo "$worker_revision must close handoff admission during cutover." >&2
      return 1
    fi
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
    --update-env-vars "DATABASE_POOL_MAX=1,DATABASE_ACQUIRE_TIMEOUT_MS=5000" \
    --remove-env-vars "MIGRATE_VOICE_PRACTICE_KEY,MIGRATE_VOICE_LOCATION_KEY,MIGRATE_VOICE_NUMBER,PROVISIONING_INPUT,PROVISIONING_OUTPUT" \
    --quiet
  migration_started=true
  gcloud run jobs execute acuity-migrate \
    --project "$PROJECT_ID" \
    --region "$REGION" \
    --wait \
    --quiet
}

disable_legacy_runtime() {
  for service in "${services[@]}"; do
    gcloud run services update "$service" \
      --project "$PROJECT_ID" \
      --region "$REGION" \
      --scaling 0 \
      --quiet
    disabled_service_count=$((disabled_service_count + 1))
    scaling_mode="$(
      gcloud run services describe "$service" \
        --project "$PROJECT_ID" \
        --region "$REGION" \
        --format 'value(spec.scaling.scalingMode)'
    )"
    manual_instances="$(
      gcloud run services describe "$service" \
        --project "$PROJECT_ID" \
        --region "$REGION" \
        --format 'value(spec.scaling.manualInstanceCount)'
    )"
    reconciled="$(
      gcloud run services describe "$service" \
        --project "$PROJECT_ID" \
        --region "$REGION" \
        --format 'value(status.conditions[0].status)'
    )"
    if [[ "$scaling_mode" != MANUAL || "$manual_instances" != 0 ||
      "$reconciled" != True ]]; then
      echo "$service did not reach the disabled zero-instance state." >&2
      return 1
    fi
  done
  worker_stop_attempted=true
  gcloud run worker-pools update acuity-worker \
    --project "$PROJECT_ID" \
    --region "$REGION" \
    --instances 0 \
    --quiet
  worker_instances="$(
    gcloud run worker-pools describe acuity-worker \
      --project "$PROJECT_ID" \
      --region "$REGION" \
      --format 'value(spec.template.scaling.manualInstanceCount)'
  )"
  worker_reconciled="$(
    gcloud run worker-pools describe acuity-worker \
      --project "$PROJECT_ID" \
      --region "$REGION" \
      --format 'value(status.conditions[0].status)'
  )"
  if [[ "$worker_instances" != 0 || "$worker_reconciled" != True ]]; then
    echo "acuity-worker did not reach the disabled zero-instance state." >&2
    return 1
  fi
}

if [[ "$destructive_cutover" == true ]]; then
  disable_legacy_runtime
  run_migration
  stage_backend_services
  stage_worker 0
  for service in "${backend_services[@]}"; do
    revision="$service-$DEPLOYMENT_ID"
    gcloud run services update-traffic "$service" \
      --project "$PROJECT_ID" \
      --region "$REGION" \
      --to-revisions "$revision=100" \
      --quiet
    ensure_service_traffic "$service" "$revision"
    gcloud run services update "$service" \
      --project "$PROJECT_ID" \
      --region "$REGION" \
      --scaling auto \
      --quiet
  done
  gcloud run worker-pools update acuity-worker \
    --project "$PROJECT_ID" \
    --region "$REGION" \
    --instances 1 \
    --quiet
else
  run_migration
  stage_backend_services
  stage_worker 1
  for service in "${backend_services[@]}"; do
    revision="$service-$DEPLOYMENT_ID"
    gcloud run services update-traffic "$service" \
      --project "$PROJECT_ID" \
      --region "$REGION" \
      --to-revisions "$revision=100" \
      --quiet
  done
fi

worker_ready="$(
  gcloud run worker-pools describe acuity-worker \
    --project "$PROJECT_ID" \
    --region "$REGION" \
    --format 'value(status.conditions[0].status)'
)"
if [[ "$worker_ready" != "True" ]]; then
  echo "$worker_revision did not become ready." >&2
  exit 1
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
if [[ "$destructive_cutover" == true ]]; then
  ensure_service_traffic acuity-web "$web_revision"
  gcloud run services update acuity-web \
    --project "$PROJECT_ID" \
    --region "$REGION" \
    --scaling auto \
    --quiet
fi
web_url="$(service_url acuity-web)"
smoke "$web_url/sign-in"
smoke "$web_url/api/auth/get-session"

release_complete=true
trap - ERR
printf 'Released %s as %s and %s.\n' "$IMAGE_TAG" "$backend_digest" "$web_digest"
