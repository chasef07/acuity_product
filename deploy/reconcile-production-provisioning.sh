#!/usr/bin/env bash

set -Eeuo pipefail

readonly project_id="${GCP_PROJECT_ID:-}"
readonly region="${GCP_REGION:-}"
readonly commit_sha="${GITHUB_SHA:-}"
readonly confirmation="${PROVISION_CONFIRMATION:-}"
readonly expected_access_grants="${EXPECTED_ACCESS_GRANTS_CREATED:-}"
readonly job_name="acuity-migrate"
readonly provisioning_input="/etc/acuity/production-provisioning.json"
readonly provisioning_output="/tmp/production-provisioning-output.json"

if [[ "$project_id" != "acuity-health-prod" || "$region" != "us-east1" ]]; then
  echo "Provisioning is restricted to acuity-health-prod in us-east1." >&2
  exit 1
fi
if [[ "$confirmation" != "PROVISION" ]]; then
  echo "PROVISION_CONFIRMATION must be exactly PROVISION." >&2
  exit 1
fi
if [[ ! "$expected_access_grants" =~ ^(0|[1-9][0-9]*)$ ]]; then
  echo "EXPECTED_ACCESS_GRANTS_CREATED must be a non-negative integer." >&2
  exit 1
fi
if [[ ! "$commit_sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "GITHUB_SHA must be a full 40-character Git commit SHA." >&2
  exit 1
fi

job_container() {
  jq -ce '
    .spec.template.spec.template.spec.containers[0] //
    .spec.template.template.containers[0] //
    error("Cloud Run job container was not found")
  '
}

has_provisioning_environment() {
  job_container | jq -e '
    (.env // []) |
    any(.name == "PROVISIONING_INPUT" or .name == "PROVISIONING_OUTPUT")
  ' >/dev/null
}

job="$(
  gcloud run jobs describe "$job_name" \
    --project "$project_id" \
    --region "$region" \
    --format json
)"
if has_provisioning_environment <<<"$job"; then
  echo "The migration job already has provisioning enabled; inspect it before continuing." >&2
  exit 1
fi

job_image="$(job_container <<<"$job" | jq -er '.image')"
if [[ ! "$job_image" =~ ^(.+-docker\.pkg\.dev/acuity-health-prod/.+)@sha256:[0-9a-f]{64}$ ]]; then
  echo "The migration job does not use an immutable production image." >&2
  exit 1
fi
image_repository="${BASH_REMATCH[1]}"
expected_image="$(
  gcloud artifacts docker images describe "$image_repository:$commit_sha" \
    --project "$project_id" \
    --format 'value(image_summary.fully_qualified_digest)'
)"
if [[ "$job_image" != "$expected_image" ]]; then
  echo "The migration job image is not the image built from the selected main commit." >&2
  exit 1
fi

execution_name="$(
  gcloud run jobs execute "$job_name" \
    --project "$project_id" \
    --region "$region" \
    --tasks 1 \
    --update-env-vars \
    "PROVISIONING_INPUT=$provisioning_input,PROVISIONING_OUTPUT=$provisioning_output" \
    --wait \
    --quiet \
    --format 'value(metadata.name)'
)"
if [[ ! "$execution_name" =~ ^acuity-migrate-[a-z0-9-]+$ ]]; then
  echo "Cloud Run did not return the provisioning execution name." >&2
  exit 1
fi

execution="$(
  gcloud run jobs executions describe "$execution_name" \
    --project "$project_id" \
    --region "$region" \
    --format json
)"
if ! jq -e '
  any(.status.conditions[]?; .type == "Completed" and .status == "True") and
  ((.status.succeededCount // 0) | tonumber) == 1
' >/dev/null <<<"$execution"; then
  echo "$execution_name did not complete exactly one successful task." >&2
  exit 1
fi

logs="$(
  gcloud logging read \
    "resource.type=\"cloud_run_job\" AND resource.labels.job_name=\"$job_name\" AND resource.labels.location=\"$region\" AND labels.\"run.googleapis.com/execution_name\"=\"$execution_name\" AND jsonPayload.msg=\"migrations_applied\"" \
    --project "$project_id" \
    --order desc \
    --limit 10 \
    --format json
)"
receipt="$(
  jq -cer '
    first(.[] | select(.jsonPayload.msg == "migrations_applied")) |
    .jsonPayload // error("provisioning receipt was not found")
  ' <<<"$logs"
)"
if ! jq -e '.provisioning == true' >/dev/null <<<"$receipt"; then
  echo "$execution_name did not emit a provisioning receipt." >&2
  exit 1
fi
actual_access_grants="$(jq -er '.access_grant_count | tonumber' <<<"$receipt")"
if [[ "$actual_access_grants" != "$expected_access_grants" ]]; then
  echo "Expected $expected_access_grants new Access Grants, but $execution_name created $actual_access_grants." >&2
  exit 1
fi

printf 'Provisioning receipt verified: %s created %s Access Grants.\n' \
  "$execution_name" \
  "$actual_access_grants"
