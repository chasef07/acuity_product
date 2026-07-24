#!/usr/bin/env sh
set -eu

: "${GCP_PROJECT:?required}"
: "${GCP_REGION:?required}"
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

deploy_service() {
  role="$1"
  concurrency="$2"
  maximum="$3"
  pool="$4"
  database_secret="$5"
  invocation="$6"
  shift 6
  gcloud run deploy "acuity-${role}" \
    --project "$GCP_PROJECT" \
    --region "$GCP_REGION" \
    --image "$BACKEND_IMAGE_DIGEST" \
    --service-account "${RUNTIME_SERVICE_ACCOUNT_PREFIX}-${role}@${GCP_PROJECT}.iam.gserviceaccount.com" \
    --concurrency "$concurrency" \
    --min-instances 0 \
    --max-instances "$maximum" \
    --set-secrets "DATABASE_URL=${database_secret}:latest" \
    --set-env-vars "ACUITY_RUNTIME_ROLE=${role},DATABASE_POOL_MAX=${pool},DATABASE_ACQUIRE_TIMEOUT_MS=1500" \
    "$invocation" \
    "$@"
}

deploy_service portal-api 20 3 4 "$PORTAL_DATABASE_URL_SECRET" --allow-unauthenticated \
  --update-env-vars "BROWSER_ORIGIN=${BROWSER_ORIGIN},BETTER_AUTH_JWKS_URL=${BETTER_AUTH_JWKS_URL},BETTER_AUTH_ISSUER=${BETTER_AUTH_ISSUER},PORTAL_API_AUDIENCE=${PORTAL_API_AUDIENCE}"
deploy_service provider-ingress 20 2 2 "$PROVIDER_DATABASE_URL_SECRET" --no-allow-unauthenticated
deploy_service realtime 50 2 3 "$REALTIME_DATABASE_URL_SECRET" --allow-unauthenticated \
  --update-env-vars "BROWSER_ORIGIN=${BROWSER_ORIGIN},BETTER_AUTH_JWKS_URL=${BETTER_AUTH_JWKS_URL},BETTER_AUTH_ISSUER=${BETTER_AUTH_ISSUER},PORTAL_API_AUDIENCE=${PORTAL_API_AUDIENCE},REALTIME_HEARTBEAT_SECONDS=15,REALTIME_STREAM_SECONDS=300,REALTIME_REVALIDATE_SECONDS=30,REALTIME_RECONNECT_MIN_MS=250,REALTIME_RECONNECT_MAX_SECONDS=5"

# WEB_IMAGE_DIGEST must already contain the required NEXT_PUBLIC origins;
# Next.js embeds them during the image build rather than at runtime.
gcloud run deploy acuity-web \
  --project "$GCP_PROJECT" \
  --region "$GCP_REGION" \
  --image "$WEB_IMAGE_DIGEST" \
  --service-account "${RUNTIME_SERVICE_ACCOUNT_PREFIX}-web@${GCP_PROJECT}.iam.gserviceaccount.com" \
  --concurrency 40 \
  --min-instances 0 \
  --max-instances 2 \
  --set-secrets "AUTH_DATABASE_URL=${WEB_AUTH_DATABASE_URL_SECRET}:latest,BETTER_AUTH_SECRET=${BETTER_AUTH_SECRET_SECRET}:latest,SMTP_PASSWORD=${SMTP_PASSWORD_SECRET}:latest" \
  --set-env-vars "AUTH_DB_POOL_MAX=3,AUTH_DB_ACQUIRE_TIMEOUT_MS=1500,BETTER_AUTH_URL=${BROWSER_ORIGIN},BETTER_AUTH_TRUSTED_ORIGINS=${BROWSER_ORIGIN},PORTAL_API_INTERNAL_URL=${PORTAL_API_INTERNAL_URL},PORTAL_API_AUDIENCE=${PORTAL_API_AUDIENCE},AUTH_EMAIL_MODE=smtp,SMTP_HOST=${SMTP_HOST},SMTP_PORT=${SMTP_PORT},SMTP_USER=${SMTP_USER},AUTH_EMAIL_FROM=${AUTH_EMAIL_FROM}" \
  --allow-unauthenticated

gcloud run worker-pools deploy acuity-worker \
  --project "$GCP_PROJECT" \
  --region "$GCP_REGION" \
  --image "$BACKEND_IMAGE_DIGEST" \
  --instances 2 \
  --service-account "${RUNTIME_SERVICE_ACCOUNT_PREFIX}-worker@${GCP_PROJECT}.iam.gserviceaccount.com" \
  --set-secrets "DATABASE_URL=${WORKER_DATABASE_URL_SECRET}:latest" \
  --set-env-vars "ACUITY_RUNTIME_ROLE=worker,DATABASE_POOL_MAX=2,DATABASE_ACQUIRE_TIMEOUT_MS=1500"

gcloud run jobs deploy acuity-migrate \
  --project "$GCP_PROJECT" \
  --region "$GCP_REGION" \
  --image "$BACKEND_IMAGE_DIGEST" \
  --tasks 1 \
  --max-retries 0 \
  --service-account "${RUNTIME_SERVICE_ACCOUNT_PREFIX}-migrate@${GCP_PROJECT}.iam.gserviceaccount.com" \
  --set-secrets "DATABASE_URL=${MIGRATE_DATABASE_URL_SECRET}:latest" \
  --set-env-vars "ACUITY_RUNTIME_ROLE=migrate,DATABASE_POOL_MAX=2,DATABASE_ACQUIRE_TIMEOUT_MS=5000"
