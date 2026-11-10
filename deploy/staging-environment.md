# Staging environment

Staging is a separate GCP project that will receive every merge to `main`.
Production still deploys only when release-please creates a release. Both use
`cloudbuild.release.yaml` and `deploy-production-release.sh`.

Staging keeps production's service names (`acuity-web`, `acuity-portal-api`,
`acuity-provider-ingress`, `acuity-realtime`, `acuity-worker`,
`acuity-migrate`) and production's sizing. The release script checks every
live service against `production-runtime-contract.json`, so staging runs the
production profile, not `nonproduction-runtime-contract.md`.

Staging must hold synthetic data only: no patients, production numbers, real
staff, or production credentials.

## Unmet prerequisite: staging provisioning

Every release runs `acuity-migrate` with
`PROVISIONING_INPUT=/etc/acuity/production-provisioning.json` and
`RESPONSIBILITIES_INPUT=/etc/acuity/task-responsibilities.json`, which
`Dockerfile.backend` bakes in from `config/`. Those files hold production
practices, Telnyx numbers and messaging profiles, real staff Access Grants, and
the real staff roster for task responsibilities. Deploying today's release to
staging would write all of it into the staging database.

Before the staging job is wired, add synthetic staging versions of both files,
built from the staging Telnyx resources in step 5. Then make both files release
inputs, so staging and production each migrate with their own.

## Provision once

1. Create the project and enable Cloud Run, Cloud SQL Admin, Artifact Registry,
   Cloud Build, Secret Manager, IAM Credentials, and Cloud Storage.
2. Create the `acuity-product` Docker repository in Artifact Registry, in
   `us-east1`.
3. Create the database with `cloud-sql-commands.example.sh`, with
   `GCP_PROJECT` set to the staging project. Create one database user per
   runtime role, as `backend/internal/migrations/database-grants.sql` expects.
   The instance must supply at least the contract's
   `requiredDatabaseConnections`. Measure that value; it becomes
   `USABLE_DATABASE_CONNECTIONS`.
4. Create one runtime service account per role under a shared prefix
   (`RUNTIME_SERVICE_ACCOUNT_PREFIX`), and the private messaging attachment
   bucket (`MESSAGING_ATTACHMENT_BUCKET`).
5. Create every Secret Manager secret that `cloud-run-commands.example.sh`
   names, with staging values:
   - one database URL per runtime role, using the users from step 3;
   - a new Better Auth secret and a staging Google OAuth client whose redirect
     URI is the staging web URL;
   - a staging Telnyx Call Control application, credential connection, and
     number, with webhooks pointed at staging `acuity-provider-ingress`;
   - staging-only service tokens for the demo and Abita practices.
6. Create `acuity-portal-api` and `acuity-realtime` first with a placeholder
   `gcloud run deploy <service> --project <staging-project> --region us-east1 --image <backend-image>`.
   The step 7 bootstrap replaces them. Read each URL with
   `gcloud run services describe <service> --project <staging-project> --region us-east1 --format 'value(status.url)'`.
   Use those exact values. The release fails before any change when they
   differ from the URLs baked into the web image. Cloud Run's
   `-<project-number>` alias URL does not match.
7. Build the web image with those URLs, then bootstrap every service with
   `cloud-run-commands.example.sh` and `ACUITY_DEPLOYMENT_PROFILE=production`.
   The release script needs a ready revision of every service to roll back to.
8. Create a deploy service account with the production deploy account's roles,
   and a Workload Identity provider that trusts only this repository's
   `staging` GitHub environment.
9. Create the `staging` GitHub environment with `GCP_PROJECT_ID`, `GCP_REGION`,
   `GCP_WORKLOAD_IDENTITY_PROVIDER`, `GCP_DEPLOY_SERVICE_ACCOUNT`, and
   `USABLE_DATABASE_CONNECTIONS`. Add `MIDDLEWARE_BASE_URL` and
   `MIDDLEWARE_API_SECRET_SECRET` only for a staging middleware.

## Then

Add a staging job to `.github/workflows/release.yml` that deploys each verified
push to `main`. Like the production job, it passes its own
`_PORTAL_API_URL` and `_REALTIME_URL` (from step 6) as literals. Production then
deploys a release only when its commit has already passed staging.
