#!/usr/bin/env sh
set -eu

# Non-production example. Production instance and pool bounds are defined in
# production-runtime-contract.json and must not be replaced by these scale-to-zero values.

: "${GCP_PROJECT:?required}"
: "${GCP_REGION:?required}"
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
: "${SMTP_PASSWORD_SECRET:?required Secret Manager secret name}"
: "${TELNYX_API_KEY_SECRET:?required Secret Manager secret name}"
: "${HANDOFF_TOKEN_KEY_SECRET:?required Secret Manager secret name}"
: "${HANDOFF_SERVICE_TOKEN_SECRET:?required Secret Manager secret name}"
: "${BROWSER_ORIGIN:?required}"
: "${NEXT_PUBLIC_PORTAL_API_URL:?required web image build argument}"
: "${NEXT_PUBLIC_REALTIME_URL:?required web image build argument}"
: "${BETTER_AUTH_JWKS_URL:?required}"
: "${BETTER_AUTH_ISSUER:?required}"
: "${PORTAL_API_AUDIENCE:?required}"
: "${PORTAL_API_INTERNAL_URL:?required}"
: "${SMTP_HOST:?required}"
: "${SMTP_PORT:?required}"
: "${SMTP_USER:?required}"
: "${AUTH_EMAIL_FROM:?required}"
: "${HUMAN_CALLING_SIP_DOMAIN:?required}"
: "${HUMAN_CALLING_STAFF_SIP_DOMAIN:?required}"
: "${HANDOFF_SERVICE_SUBJECT:?required}"
: "${HANDOFF_SERVICE_PRACTICE_ID:?required UUID}"
: "${TELNYX_CALL_CONTROL_ID:?required}"
: "${TELNYX_CREDENTIAL_CONNECTION_ID:?required}"
: "${TELNYX_FROM_NUMBER:?required}"
: "${TELNYX_RINGBACK_URL:?required}"
: "${TELNYX_RECORDING_BUCKET:?required private GCS bucket name}"
: "${TELNYX_WEBHOOK_PUBLIC_KEY:?required base64 Ed25519 public key}"
: "${HUMAN_CALLING_OFFER_SECONDS:=20}"
: "${HUMAN_CALLING_CONNECTION_TIMEOUT_SECONDS:=15}"
: "${HUMAN_CALLING_LEASE_SECONDS:=30}"
: "${HUMAN_CALLING_READINESS_GRACE_SECONDS:=15}"

CALLING_TIMING_ENV="HUMAN_CALLING_OFFER_SECONDS=${HUMAN_CALLING_OFFER_SECONDS},HUMAN_CALLING_CONNECTION_TIMEOUT_SECONDS=${HUMAN_CALLING_CONNECTION_TIMEOUT_SECONDS},HUMAN_CALLING_LEASE_SECONDS=${HUMAN_CALLING_LEASE_SECONDS},HUMAN_CALLING_READINESS_GRACE_SECONDS=${HUMAN_CALLING_READINESS_GRACE_SECONDS}"

deploy_service() {
  role="$1"
  concurrency="$2"
  maximum="$3"
  pool="$4"
  database_secret="$5"
  invocation="$6"
  role_secrets="$7"
  role_env="$8"
  secrets="DATABASE_URL=${database_secret}:latest"
  env_vars="ACUITY_RUNTIME_ROLE=${role},HTTP_PORT=8080,DATABASE_POOL_MAX=${pool},DATABASE_ACQUIRE_TIMEOUT_MS=1500"
  if [ -n "$role_secrets" ]; then
    secrets="${secrets},${role_secrets}"
  fi
  if [ -n "$role_env" ]; then
    env_vars="${env_vars},${role_env}"
  fi
  gcloud run deploy "acuity-${role}" \
    --project "$GCP_PROJECT" \
    --region "$GCP_REGION" \
    --image "$BACKEND_IMAGE_DIGEST" \
    --service-account "${RUNTIME_SERVICE_ACCOUNT_PREFIX}-${role}@${GCP_PROJECT}.iam.gserviceaccount.com" \
    --set-cloudsql-instances "$CLOUD_SQL_INSTANCE" \
    --concurrency "$concurrency" \
    --min-instances 0 \
    --max-instances "$maximum" \
    --set-secrets "$secrets" \
    --set-env-vars "$env_vars" \
    "$invocation"
}

deploy_service portal-api 20 3 4 "$PORTAL_DATABASE_URL_SECRET" --no-invoker-iam-check \
  "HUMAN_CALLING_HANDOFF_TOKEN_KEY=${HANDOFF_TOKEN_KEY_SECRET}:latest,HANDOFF_SERVICE_TOKEN=${HANDOFF_SERVICE_TOKEN_SECRET}:latest,TELNYX_API_KEY=${TELNYX_API_KEY_SECRET}:latest" \
  "BROWSER_ORIGIN=${BROWSER_ORIGIN},BETTER_AUTH_JWKS_URL=${BETTER_AUTH_JWKS_URL},BETTER_AUTH_ISSUER=${BETTER_AUTH_ISSUER},PORTAL_API_AUDIENCE=${PORTAL_API_AUDIENCE},HUMAN_CALLING_SIP_DOMAIN=${HUMAN_CALLING_SIP_DOMAIN},HUMAN_CALLING_STAFF_SIP_DOMAIN=${HUMAN_CALLING_STAFF_SIP_DOMAIN},HANDOFF_SERVICE_SUBJECT=${HANDOFF_SERVICE_SUBJECT},HANDOFF_SERVICE_PRACTICE_ID=${HANDOFF_SERVICE_PRACTICE_ID},TELNYX_CALL_CONTROL_ID=${TELNYX_CALL_CONTROL_ID},TELNYX_CREDENTIAL_CONNECTION_ID=${TELNYX_CREDENTIAL_CONNECTION_ID},TELNYX_FROM_NUMBER=${TELNYX_FROM_NUMBER},TELNYX_RINGBACK_URL=${TELNYX_RINGBACK_URL},TELNYX_RECORDING_BUCKET=${TELNYX_RECORDING_BUCKET},${CALLING_TIMING_ENV}"
deploy_service provider-ingress 20 2 2 "$PROVIDER_DATABASE_URL_SECRET" --no-invoker-iam-check \
  "" \
  "TELNYX_WEBHOOK_PUBLIC_KEY=${TELNYX_WEBHOOK_PUBLIC_KEY}"
deploy_service realtime 50 2 3 "$REALTIME_DATABASE_URL_SECRET" --no-invoker-iam-check \
  "" \
  "BROWSER_ORIGIN=${BROWSER_ORIGIN},BETTER_AUTH_JWKS_URL=${BETTER_AUTH_JWKS_URL},BETTER_AUTH_ISSUER=${BETTER_AUTH_ISSUER},PORTAL_API_AUDIENCE=${PORTAL_API_AUDIENCE},REALTIME_HEARTBEAT_SECONDS=15,REALTIME_STREAM_SECONDS=300,REALTIME_REVALIDATE_SECONDS=30,REALTIME_RECONNECT_MIN_MS=250,REALTIME_RECONNECT_MAX_SECONDS=5"

# WEB_IMAGE_DIGEST must already contain the required NEXT_PUBLIC origins;
# Next.js embeds them during the image build rather than at runtime.
gcloud run deploy acuity-web \
  --project "$GCP_PROJECT" \
  --region "$GCP_REGION" \
  --image "$WEB_IMAGE_DIGEST" \
  --service-account "${RUNTIME_SERVICE_ACCOUNT_PREFIX}-web@${GCP_PROJECT}.iam.gserviceaccount.com" \
  --set-cloudsql-instances "$CLOUD_SQL_INSTANCE" \
  --concurrency 40 \
  --min-instances 0 \
  --max-instances 2 \
  --set-secrets "AUTH_DATABASE_URL=${WEB_AUTH_DATABASE_URL_SECRET}:latest,BETTER_AUTH_SECRET=${BETTER_AUTH_SECRET_SECRET}:latest,SMTP_PASSWORD=${SMTP_PASSWORD_SECRET}:latest" \
  --set-env-vars "AUTH_DB_POOL_MAX=3,AUTH_DB_ACQUIRE_TIMEOUT_MS=1500,BETTER_AUTH_URL=${BROWSER_ORIGIN},BETTER_AUTH_TRUSTED_ORIGINS=${BROWSER_ORIGIN},PORTAL_API_INTERNAL_URL=${PORTAL_API_INTERNAL_URL},PORTAL_API_AUDIENCE=${PORTAL_API_AUDIENCE},AUTH_EMAIL_MODE=smtp,SMTP_HOST=${SMTP_HOST},SMTP_PORT=${SMTP_PORT},SMTP_USER=${SMTP_USER},AUTH_EMAIL_FROM=${AUTH_EMAIL_FROM}" \
  --no-invoker-iam-check

gcloud run worker-pools deploy acuity-worker \
  --project "$GCP_PROJECT" \
  --region "$GCP_REGION" \
  --image "$BACKEND_IMAGE_DIGEST" \
  --instances 2 \
  --service-account "${RUNTIME_SERVICE_ACCOUNT_PREFIX}-worker@${GCP_PROJECT}.iam.gserviceaccount.com" \
  --set-cloudsql-instances "$CLOUD_SQL_INSTANCE" \
  --set-secrets "DATABASE_URL=${WORKER_DATABASE_URL_SECRET}:latest,TELNYX_API_KEY=${TELNYX_API_KEY_SECRET}:latest" \
  --set-env-vars "ACUITY_RUNTIME_ROLE=worker,DATABASE_POOL_MAX=2,DATABASE_ACQUIRE_TIMEOUT_MS=1500,TELNYX_CALL_CONTROL_ID=${TELNYX_CALL_CONTROL_ID},TELNYX_CREDENTIAL_CONNECTION_ID=${TELNYX_CREDENTIAL_CONNECTION_ID},TELNYX_FROM_NUMBER=${TELNYX_FROM_NUMBER},TELNYX_RINGBACK_URL=${TELNYX_RINGBACK_URL},TELNYX_RECORDING_BUCKET=${TELNYX_RECORDING_BUCKET},${CALLING_TIMING_ENV}"

gcloud run jobs deploy acuity-migrate \
  --project "$GCP_PROJECT" \
  --region "$GCP_REGION" \
  --image "$BACKEND_IMAGE_DIGEST" \
  --tasks 1 \
  --max-retries 0 \
  --service-account "${RUNTIME_SERVICE_ACCOUNT_PREFIX}-migrate@${GCP_PROJECT}.iam.gserviceaccount.com" \
  --set-cloudsql-instances "$CLOUD_SQL_INSTANCE" \
  --set-secrets "DATABASE_URL=${MIGRATE_DATABASE_URL_SECRET}:latest" \
  --set-env-vars "ACUITY_RUNTIME_ROLE=migrate,DATABASE_POOL_MAX=2,DATABASE_ACQUIRE_TIMEOUT_MS=5000"
