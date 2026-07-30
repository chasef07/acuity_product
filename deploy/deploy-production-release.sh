#!/usr/bin/env bash

set -Eeuo pipefail

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

promoted_services=()
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

rollback() {
  local exit_code=$?
  trap - ERR
  if [[ "$release_complete" == true ]]; then
    return
  fi
  set +e
  for ((index = ${#promoted_services[@]} - 1; index >= 0; index--)); do
    service="${promoted_services[$index]}"
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

gcloud run jobs update acuity-migrate \
  --project "$PROJECT_ID" \
  --region "$REGION" \
  --image "$backend_digest" \
  --tasks 1 \
  --max-retries 0 \
  --quiet
gcloud run jobs execute acuity-migrate \
  --project "$PROJECT_ID" \
  --region "$REGION" \
  --wait \
  --quiet

for service in "${backend_services[@]}"; do
  revision="$service-$DEPLOYMENT_ID"
  gcloud run deploy "$service" \
    --project "$PROJECT_ID" \
    --region "$REGION" \
    --image "$backend_digest" \
    --revision-suffix "$DEPLOYMENT_ID" \
    --no-traffic \
    --startup-probe "httpGet.path=/health/live,httpGet.port=8080,timeoutSeconds=1,periodSeconds=2,failureThreshold=15" \
    --readiness-probe "httpGet.path=/health/ready,httpGet.port=8080,timeoutSeconds=1,periodSeconds=2,failureThreshold=3" \
    --quiet
  verify_service_revision "$revision" "$backend_digest"
done

worker_revision="acuity-worker-$DEPLOYMENT_ID"
gcloud run worker-pools deploy acuity-worker \
  --project "$PROJECT_ID" \
  --region "$REGION" \
  --image "$backend_digest" \
  --revision-suffix "$DEPLOYMENT_ID" \
  --no-promote \
  --quiet
worker_image="$(
  gcloud run worker-pools revisions describe "$worker_revision" \
    --project "$PROJECT_ID" \
    --region "$REGION" \
    --format 'value(spec.containers[0].image)'
)"
if [[ "$worker_image" != "$backend_digest" ]]; then
  echo "$worker_revision does not use the expected immutable image." >&2
  exit 1
fi
gcloud run worker-pools update-instance-split acuity-worker \
  --project "$PROJECT_ID" \
  --region "$REGION" \
  --to-revisions "$worker_revision=100" \
  --quiet
worker_promoted=true
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
  revision="$service-$DEPLOYMENT_ID"
  gcloud run services update-traffic "$service" \
    --project "$PROJECT_ID" \
    --region "$REGION" \
    --to-revisions "$revision=100" \
    --quiet
  promoted_services+=("$service")
  smoke "$(service_url "$service")/health/ready"
done

web_revision="acuity-web-$DEPLOYMENT_ID"
gcloud run deploy acuity-web \
  --project "$PROJECT_ID" \
  --region "$REGION" \
  --image "$web_digest" \
  --revision-suffix "$DEPLOYMENT_ID" \
  --no-traffic \
  --quiet
verify_service_revision "$web_revision" "$web_digest"
gcloud run services update-traffic acuity-web \
  --project "$PROJECT_ID" \
  --region "$REGION" \
  --to-revisions "$web_revision=100" \
  --quiet
promoted_services+=("acuity-web")
web_url="$(service_url acuity-web)"
smoke "$web_url/sign-in"
smoke "$web_url/api/auth/get-session"

release_complete=true
trap - ERR
printf 'Released %s as %s and %s.\n' "$IMAGE_TAG" "$backend_digest" "$web_digest"
