#!/usr/bin/env sh
set -eu

# Non-production remains the default. Production is an explicit, fail-closed
# profile whose capacity values come only from production-runtime-contract.json.

: "${GCP_PROJECT:?required}"
: "${GCP_REGION:=us-east1}"
: "${CLOUD_SQL_INSTANCE:?required project:region:instance connection name}"
: "${BACKEND_IMAGE_DIGEST:?required}"
: "${WEB_IMAGE_DIGEST:?required; build with the two NEXT_PUBLIC origins below}"
: "${RUNTIME_SERVICE_ACCOUNT_PREFIX:?required}"
: "${PORTAL_DATABASE_URL_SECRET:?required Secret Manager secret name}"
: "${PROVIDER_DATABASE_URL_SECRET:?required Secret Manager secret name}"
: "${REALTIME_DATABASE_URL_SECRET:?required Secret Manager secret name}"
: "${WORKER_DATABASE_URL_SECRET:?required Secret Manager secret name}"
: "${MIGRATE_DATABASE_URL_SECRET:?required Secret Manager secret name}"
: "${WEB_AUTH_DATABASE_URL_SECRET:?required Secret Manager secret name}"
: "${BETTER_AUTH_SECRET_SECRET:?required Secret Manager secret name}"
: "${GOOGLE_CLIENT_ID_SECRET:?required Secret Manager secret name}"
: "${GOOGLE_CLIENT_SECRET_SECRET:?required Secret Manager secret name}"
: "${TELNYX_API_KEY_SECRET:?required Secret Manager secret name}"
: "${MESSAGING_MEDIA_SIGNING_KEY_SECRET:?required Secret Manager secret name}"
: "${HANDOFF_TOKEN_KEY_SECRET:?required Secret Manager secret name}"
: "${PLAYBACK_SIGNING_KEY_SECRET:?required Secret Manager secret name}"
: "${ACUITY_DEMO_SERVICE_TOKEN_SECRET:?required Secret Manager secret name}"
: "${ABITA_EYE_GROUP_SERVICE_TOKEN_SECRET:?required Secret Manager secret name}"
: "${BROWSER_ORIGIN:?required}"
: "${BROWSER_ALLOWED_ORIGINS:=$BROWSER_ORIGIN}"
: "${NEXT_PUBLIC_PORTAL_API_URL:?required web image build argument}"
: "${NEXT_PUBLIC_REALTIME_URL:?required web image build argument}"
: "${BETTER_AUTH_JWKS_URL:?required}"
: "${BETTER_AUTH_ISSUER:?required}"
: "${PORTAL_API_AUDIENCE:?required}"
: "${PORTAL_API_INTERNAL_URL:?required}"
: "${HUMAN_CALLING_SIP_DOMAIN:?required}"
: "${HUMAN_CALLING_STAFF_SIP_DOMAIN:?required}"
: "${ACUITY_DEMO_SERVICE_SUBJECT:?required}"
: "${ACUITY_DEMO_SERVICE_PRACTICE_ID:?required UUID}"
: "${ABITA_EYE_GROUP_SERVICE_SUBJECT:?required}"
: "${ABITA_EYE_GROUP_SERVICE_PRACTICE_ID:?required UUID}"
: "${TELNYX_CALL_CONTROL_ID:?required}"
: "${TELNYX_CREDENTIAL_CONNECTION_ID:?required}"
: "${TELNYX_FROM_NUMBER:?required}"
: "${TELNYX_RINGBACK_URL:?required}"
: "${TELNYX_WEBHOOK_PUBLIC_KEY:?required base64 Ed25519 public key}"
: "${TELNYX_WEBHOOK_NEXT_PUBLIC_KEY:=}"
: "${MESSAGING_WEBHOOK_BASE_URL:?required provider-ingress messaging webhook URL}"
: "${MESSAGING_MEDIA_PUBLIC_BASE_URL:?required provider-ingress messaging media URL}"
: "${MESSAGING_ATTACHMENT_BUCKET:?required private shared Cloud Storage bucket}"
: "${MESSAGING_ATTACHMENT_BUCKET_LOCATION:?required measured Cloud Storage bucket location}"
: "${MESSAGING_ATTACHMENT_DIRECTORY:=/mnt/acuity-messaging}"
: "${HUMAN_CALLING_RING_WINDOW_SECONDS:=20}"
: "${HUMAN_CALLING_LEASE_SECONDS:=30}"
: "${HUMAN_CALLING_READINESS_GRACE_SECONDS:=15}"
: "${ACUITY_DEPLOYMENT_PROFILE:=nonproduction}"

CALLING_TIMING_ENV="HUMAN_CALLING_RING_WINDOW_SECONDS=${HUMAN_CALLING_RING_WINDOW_SECONDS},HUMAN_CALLING_LEASE_SECONDS=${HUMAN_CALLING_LEASE_SECONDS},HUMAN_CALLING_READINESS_GRACE_SECONDS=${HUMAN_CALLING_READINESS_GRACE_SECONDS}"
TELNYX_WEBHOOK_ENV="TELNYX_WEBHOOK_PUBLIC_KEY=${TELNYX_WEBHOOK_PUBLIC_KEY}"
if [ -n "$TELNYX_WEBHOOK_NEXT_PUBLIC_KEY" ]; then
  TELNYX_WEBHOOK_ENV="${TELNYX_WEBHOOK_ENV},TELNYX_WEBHOOK_NEXT_PUBLIC_KEY=${TELNYX_WEBHOOK_NEXT_PUBLIC_KEY}"
fi

SCRIPT_DIRECTORY=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
load_contract_row() {
  requested_runtime="$1"
  rendered_row=$(
    printf '%s\n' "$CONTRACT_ROWS" |
      awk -F '\t' -v requested_runtime="$requested_runtime" \
        '$1 == requested_runtime { print }'
  )
  if ! IFS="$(printf '\t')" read -r \
    runtime_name \
    runtime_kind \
    runtime_concurrency \
    runtime_minimum \
    runtime_maximum \
    runtime_pool \
    runtime_dedicated \
    runtime_timeout \
    runtime_request_timeout \
    runtime_stream_maximum \
    runtime_stream_jitter \
    runtime_retries \
    runtime_vcpus \
    runtime_memory_mib \
    runtime_billing_mode \
    runtime_region <<EOF
$rendered_row
EOF
  then
    echo "runtime contract row is missing: ${requested_runtime}" >&2
    exit 1
  fi
  if [ "$runtime_name" != "$requested_runtime" ]; then
    echo "runtime contract row does not match ${requested_runtime}" >&2
    exit 1
  fi
}

case "$ACUITY_DEPLOYMENT_PROFILE" in
  production)
    : "${USABLE_DATABASE_CONNECTIONS:?required measured usable Cloud SQL connections}"
    CONTRACT_ROWS=$(
      node "$SCRIPT_DIRECTORY/render-production-runtime-contract.mjs" \
        "$SCRIPT_DIRECTORY/production-runtime-contract.json"
    )
    load_contract_row capacity
    required_connections="$runtime_maximum"
    if [ "$GCP_REGION" != "$runtime_region" ]; then
      echo "production region must be ${runtime_region}; configured ${GCP_REGION}" >&2
      exit 1
    fi
    case "$USABLE_DATABASE_CONNECTIONS" in
      ''|*[!0-9]*)
        echo "USABLE_DATABASE_CONNECTIONS must be a positive integer" >&2
        exit 1
        ;;
    esac
    if [ "$USABLE_DATABASE_CONNECTIONS" -lt "$required_connections" ]; then
      echo "production requires at least ${required_connections} usable database connections; measured ${USABLE_DATABASE_CONNECTIONS}" >&2
      exit 1
    fi
    ;;
  nonproduction)
    CONTRACT_ROWS=$(
      printf 'web\tservice\t40\t0\t2\t3\t0\t1500\t0\t0\t0\t0\t1\t512\trequest-based\t%s\n' "$GCP_REGION"
      printf 'portal-api\tservice\t20\t0\t3\t4\t0\t1500\t0\t0\t0\t0\t1\t512\trequest-based\t%s\n' "$GCP_REGION"
      printf 'provider-ingress\tservice\t20\t0\t2\t2\t0\t1500\t0\t0\t0\t0\t1\t512\trequest-based\t%s\n' "$GCP_REGION"
      printf 'realtime\tservice\t50\t0\t2\t3\t1\t1500\t300\t30\t5\t0\t1\t512\trequest-based\t%s\n' "$GCP_REGION"
      printf 'worker\tworker-pool\t0\t2\t2\t2\t0\t1500\t0\t0\t0\t0\t1\t512\tinstance-based\t%s\n' "$GCP_REGION"
      printf 'migrate\tjob\t0\t0\t1\t2\t0\t5000\t0\t0\t0\t0\t1\t512\tinstance-based\t%s\n' "$GCP_REGION"
    )
    ;;
  *)
    echo "ACUITY_DEPLOYMENT_PROFILE must be production or nonproduction" >&2
    exit 1
    ;;
esac

case "$CLOUD_SQL_INSTANCE" in
  *":${GCP_REGION}:"*) ;;
  *)
    echo "Cloud SQL instance must be in ${GCP_REGION}: ${CLOUD_SQL_INSTANCE}" >&2
    exit 1
    ;;
esac
if [ "$MESSAGING_ATTACHMENT_BUCKET_LOCATION" != "$GCP_REGION" ]; then
  echo "messaging attachment bucket must be in ${GCP_REGION}; measured ${MESSAGING_ATTACHMENT_BUCKET_LOCATION}" >&2
  exit 1
fi

deploy_service() {
  database_secret="$1"
  invocation="$2"
  role_secrets="$3"
  role_env="$4"
  minimum="$runtime_minimum"
  maximum="$runtime_maximum"
  pool="$runtime_pool"
  case "$runtime_billing_mode" in
    request-based) billing_flag=--cpu-throttling ;;
    *)
      echo "request service ${runtime_name} must use request-based billing" >&2
      exit 1
      ;;
  esac
  secrets="DATABASE_URL=${database_secret}:latest"
  env_vars="ACUITY_RUNTIME_ROLE=${runtime_name},HTTP_PORT=8080,DATABASE_POOL_MAX=${pool},DATABASE_ACQUIRE_TIMEOUT_MS=${runtime_timeout}"
  if [ -n "$role_secrets" ]; then
    secrets="${secrets},${role_secrets}"
  fi
  if [ -n "$role_env" ]; then
    env_vars="${env_vars},${role_env}"
  fi
  set -- gcloud run deploy "acuity-${runtime_name}" \
    --project "$GCP_PROJECT" \
    --region "$GCP_REGION" \
    --image "$BACKEND_IMAGE_DIGEST" \
    --service-account "${RUNTIME_SERVICE_ACCOUNT_PREFIX}-${runtime_name}@${GCP_PROJECT}.iam.gserviceaccount.com" \
    --set-cloudsql-instances "$CLOUD_SQL_INSTANCE" \
    --cpu "$runtime_vcpus" \
    --memory "${runtime_memory_mib}Mi" \
    "$billing_flag" \
    --concurrency "$runtime_concurrency" \
    --min "$minimum" \
    --max "$maximum" \
    --set-secrets "$secrets" \
    --set-env-vars "$env_vars"
  if [ "$runtime_request_timeout" -gt 0 ]; then
    set -- "$@" --timeout "$runtime_request_timeout"
  fi
  set -- "$@" "$invocation"
  case "$runtime_name" in
    portal-api)
      set -- "$@" \
        --add-volume "name=messaging-attachments,type=cloud-storage,bucket=${MESSAGING_ATTACHMENT_BUCKET}" \
        --add-volume-mount "volume=messaging-attachments,mount-path=${MESSAGING_ATTACHMENT_DIRECTORY}"
      ;;
    provider-ingress)
      set -- "$@" \
        --add-volume "name=messaging-attachments,type=cloud-storage,bucket=${MESSAGING_ATTACHMENT_BUCKET},readonly=true" \
        --add-volume-mount "volume=messaging-attachments,mount-path=${MESSAGING_ATTACHMENT_DIRECTORY}"
      ;;
  esac
  "$@"
}

load_contract_row portal-api
deploy_service "$PORTAL_DATABASE_URL_SECRET" --no-invoker-iam-check \
  "HUMAN_CALLING_HANDOFF_TOKEN_KEY=${HANDOFF_TOKEN_KEY_SECRET}:latest,HUMAN_CALLING_PLAYBACK_SIGNING_KEY=${PLAYBACK_SIGNING_KEY_SECRET}:latest,ACUITY_DEMO_SERVICE_TOKEN=${ACUITY_DEMO_SERVICE_TOKEN_SECRET}:latest,ABITA_EYE_GROUP_SERVICE_TOKEN=${ABITA_EYE_GROUP_SERVICE_TOKEN_SECRET}:latest,TELNYX_API_KEY=${TELNYX_API_KEY_SECRET}:latest" \
  "BROWSER_ORIGIN=${BROWSER_ALLOWED_ORIGINS},BETTER_AUTH_JWKS_URL=${BETTER_AUTH_JWKS_URL},BETTER_AUTH_ISSUER=${BETTER_AUTH_ISSUER},PORTAL_API_AUDIENCE=${PORTAL_API_AUDIENCE},HUMAN_CALLING_SIP_DOMAIN=${HUMAN_CALLING_SIP_DOMAIN},HUMAN_CALLING_STAFF_SIP_DOMAIN=${HUMAN_CALLING_STAFF_SIP_DOMAIN},ACUITY_DEMO_SERVICE_SUBJECT=${ACUITY_DEMO_SERVICE_SUBJECT},ACUITY_DEMO_SERVICE_PRACTICE_ID=${ACUITY_DEMO_SERVICE_PRACTICE_ID},ABITA_EYE_GROUP_SERVICE_SUBJECT=${ABITA_EYE_GROUP_SERVICE_SUBJECT},ABITA_EYE_GROUP_SERVICE_PRACTICE_ID=${ABITA_EYE_GROUP_SERVICE_PRACTICE_ID},TELNYX_CALL_CONTROL_ID=${TELNYX_CALL_CONTROL_ID},TELNYX_CREDENTIAL_CONNECTION_ID=${TELNYX_CREDENTIAL_CONNECTION_ID},TELNYX_FROM_NUMBER=${TELNYX_FROM_NUMBER},TELNYX_RINGBACK_URL=${TELNYX_RINGBACK_URL},MESSAGING_WEBHOOK_BASE_URL=${MESSAGING_WEBHOOK_BASE_URL},MESSAGING_ATTACHMENT_DIRECTORY=${MESSAGING_ATTACHMENT_DIRECTORY},${CALLING_TIMING_ENV}"
load_contract_row provider-ingress
deploy_service "$PROVIDER_DATABASE_URL_SECRET" --no-invoker-iam-check \
  "MESSAGING_MEDIA_SIGNING_KEY=${MESSAGING_MEDIA_SIGNING_KEY_SECRET}:latest" \
  "${TELNYX_WEBHOOK_ENV},MESSAGING_ATTACHMENT_DIRECTORY=${MESSAGING_ATTACHMENT_DIRECTORY}"
load_contract_row realtime
deploy_service "$REALTIME_DATABASE_URL_SECRET" --no-invoker-iam-check \
  "" \
  "BROWSER_ORIGIN=${BROWSER_ALLOWED_ORIGINS},BETTER_AUTH_JWKS_URL=${BETTER_AUTH_JWKS_URL},BETTER_AUTH_ISSUER=${BETTER_AUTH_ISSUER},PORTAL_API_AUDIENCE=${PORTAL_API_AUDIENCE},REALTIME_HEARTBEAT_SECONDS=15,REALTIME_STREAM_SECONDS=${runtime_stream_maximum},REALTIME_STREAM_JITTER_SECONDS=${runtime_stream_jitter},REALTIME_REVALIDATE_SECONDS=30,REALTIME_RECONNECT_MIN_MS=250,REALTIME_RECONNECT_MAX_SECONDS=5"

# WEB_IMAGE_DIGEST must already contain the required NEXT_PUBLIC origins;
# Next.js embeds them during the image build rather than at runtime.
load_contract_row web
gcloud run deploy acuity-web \
  --project "$GCP_PROJECT" \
  --region "$GCP_REGION" \
  --image "$WEB_IMAGE_DIGEST" \
  --service-account "${RUNTIME_SERVICE_ACCOUNT_PREFIX}-web@${GCP_PROJECT}.iam.gserviceaccount.com" \
  --set-cloudsql-instances "$CLOUD_SQL_INSTANCE" \
  --cpu "$runtime_vcpus" \
  --memory "${runtime_memory_mib}Mi" \
  --cpu-throttling \
  --concurrency "$runtime_concurrency" \
  --min "$runtime_minimum" \
  --max "$runtime_maximum" \
  --set-secrets "AUTH_DATABASE_URL=${WEB_AUTH_DATABASE_URL_SECRET}:latest,BETTER_AUTH_SECRET=${BETTER_AUTH_SECRET_SECRET}:latest,GOOGLE_CLIENT_ID=${GOOGLE_CLIENT_ID_SECRET}:latest,GOOGLE_CLIENT_SECRET=${GOOGLE_CLIENT_SECRET_SECRET}:latest" \
  --set-env-vars "AUTH_DB_POOL_MAX=${runtime_pool},AUTH_DB_ACQUIRE_TIMEOUT_MS=${runtime_timeout},BETTER_AUTH_URL=${BROWSER_ORIGIN},BETTER_AUTH_TRUSTED_ORIGINS=${BROWSER_ALLOWED_ORIGINS},PORTAL_API_INTERNAL_URL=${PORTAL_API_INTERNAL_URL},PORTAL_API_AUDIENCE=${PORTAL_API_AUDIENCE}" \
  --no-invoker-iam-check

load_contract_row worker
gcloud run worker-pools deploy acuity-worker \
  --project "$GCP_PROJECT" \
  --region "$GCP_REGION" \
  --image "$BACKEND_IMAGE_DIGEST" \
  --instances "$runtime_maximum" \
  --service-account "${RUNTIME_SERVICE_ACCOUNT_PREFIX}-worker@${GCP_PROJECT}.iam.gserviceaccount.com" \
  --set-cloudsql-instances "$CLOUD_SQL_INSTANCE" \
  --cpu "$runtime_vcpus" \
  --memory "${runtime_memory_mib}Mi" \
  --set-secrets "DATABASE_URL=${WORKER_DATABASE_URL_SECRET}:latest,TELNYX_API_KEY=${TELNYX_API_KEY_SECRET}:latest,MESSAGING_MEDIA_SIGNING_KEY=${MESSAGING_MEDIA_SIGNING_KEY_SECRET}:latest,HUMAN_CALLING_HANDOFF_TOKEN_KEY=${HANDOFF_TOKEN_KEY_SECRET}:latest,HUMAN_CALLING_PLAYBACK_SIGNING_KEY=${PLAYBACK_SIGNING_KEY_SECRET}:latest" \
  --set-env-vars "ACUITY_RUNTIME_ROLE=worker,DATABASE_POOL_MAX=${runtime_pool},DATABASE_ACQUIRE_TIMEOUT_MS=${runtime_timeout},HUMAN_CALLING_SIP_DOMAIN=${HUMAN_CALLING_SIP_DOMAIN},HUMAN_CALLING_STAFF_SIP_DOMAIN=${HUMAN_CALLING_STAFF_SIP_DOMAIN},TELNYX_CALL_CONTROL_ID=${TELNYX_CALL_CONTROL_ID},TELNYX_CREDENTIAL_CONNECTION_ID=${TELNYX_CREDENTIAL_CONNECTION_ID},TELNYX_FROM_NUMBER=${TELNYX_FROM_NUMBER},TELNYX_RINGBACK_URL=${TELNYX_RINGBACK_URL},MESSAGING_WEBHOOK_BASE_URL=${MESSAGING_WEBHOOK_BASE_URL},MESSAGING_ATTACHMENT_DIRECTORY=${MESSAGING_ATTACHMENT_DIRECTORY},MESSAGING_MEDIA_PUBLIC_BASE_URL=${MESSAGING_MEDIA_PUBLIC_BASE_URL},${CALLING_TIMING_ENV}" \
  --add-volume "name=messaging-attachments,type=cloud-storage,bucket=${MESSAGING_ATTACHMENT_BUCKET}" \
  --add-volume-mount "volume=messaging-attachments,mount-path=${MESSAGING_ATTACHMENT_DIRECTORY}"

load_contract_row migrate
gcloud run jobs deploy acuity-migrate \
  --project "$GCP_PROJECT" \
  --region "$GCP_REGION" \
  --image "$BACKEND_IMAGE_DIGEST" \
  --tasks "$runtime_maximum" \
  --max-retries "$runtime_retries" \
  --service-account "${RUNTIME_SERVICE_ACCOUNT_PREFIX}-migrate@${GCP_PROJECT}.iam.gserviceaccount.com" \
  --set-cloudsql-instances "$CLOUD_SQL_INSTANCE" \
  --cpu "$runtime_vcpus" \
  --memory "${runtime_memory_mib}Mi" \
  --set-secrets "DATABASE_URL=${MIGRATE_DATABASE_URL_SECRET}:latest" \
  --set-env-vars "ACUITY_RUNTIME_ROLE=migrate,DATABASE_POOL_MAX=${runtime_pool},DATABASE_ACQUIRE_TIMEOUT_MS=${runtime_timeout}"
