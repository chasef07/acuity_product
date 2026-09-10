#!/usr/bin/env bash
set -euo pipefail

# Prepare the existing project for embeddings. This does not deploy services,
# install database extensions, import knowledge, or activate an office.
: "${GCP_PROJECT:?required existing Google Cloud project ID}"
if [[ ! "$GCP_PROJECT" =~ ^[a-z][a-z0-9-]{4,28}[a-z0-9]$ ]]; then
  echo "GCP_PROJECT must be a Google Cloud project ID." >&2
  exit 1
fi

service_account="acuity-portal-api@${GCP_PROJECT}.iam.gserviceaccount.com"
role_id="acuityKnowledgePredict"
role_name="projects/${GCP_PROJECT}/roles/${role_id}"

# Verify the intended existing identity before changing project settings.
gcloud iam service-accounts describe "$service_account" \
  --project "$GCP_PROJECT" --format='value(email)' >/dev/null
existing_role="$(gcloud iam roles list --project "$GCP_PROJECT" \
  --filter="name=${role_name}" --format='value(name)')"
gcloud services enable aiplatform.googleapis.com --project "$GCP_PROJECT" --quiet

if [[ -z "$existing_role" ]]; then
  gcloud iam roles create "$role_id" --project "$GCP_PROJECT" \
    --title='Acuity Knowledge Prediction' \
    --description='Predict embeddings for scoped office knowledge search.' \
    --permissions=aiplatform.endpoints.predict --stage=GA --quiet
else
  # This dedicated role intentionally contains only prediction permission.
  gcloud iam roles update "$role_id" --project "$GCP_PROJECT" \
    --permissions=aiplatform.endpoints.predict --stage=GA --quiet
fi

gcloud projects add-iam-policy-binding "$GCP_PROJECT" \
  --member="serviceAccount:${service_account}" \
  --role="$role_name" --condition=None --quiet --format=none
printf 'Prepared Vertex prediction access for %s. No deployment or database changes performed.\n' "$service_account"
