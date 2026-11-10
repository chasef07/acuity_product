# Staging environment

Staging is a separate GCP project that will receive every merge to `main`.
Production still deploys only when release-please creates a release. Both use
`cloudbuild.release.yaml` and `deploy-production-release.sh`.

Staging keeps production's service names (`acuity-web`, `acuity-portal-api`,
`acuity-provider-ingress`, `acuity-realtime`, `acuity-worker`,
`acuity-migrate`) and production's sizing. The release script checks every
live service against `production-runtime-contract.json`, so staging runs the
production profile, not `nonproduction-runtime-contract.md`.

Staging must hold synthetic data only: no patients, production numbers, clinic
staff, or production credentials. The only real accounts are the Acuity team's.

## Staging data

Each backend image carries one environment's data, chosen at build time by
`_RELEASE_ENVIRONMENT` (`production` or `staging`):

- `config/<environment>-provisioning.json`: practices, locations, Telnyx
  numbers, and Access Grants.
- `config/<environment>-task-responsibilities.json`: the task roster.

The migration loads both on every release. The staging files keep production's
practice, location, and Abita office keys. Staff are synthetic
`@staging.acuity.test` accounts, and Platform Operators are the Acuity team.
`TestStagingReleaseReusesNoProductionStaffOrNumbers` fails if a staging file
reuses a production staff email, number, or messaging profile.

The staging files have no Telnyx numbers yet, so staging calling and messaging
stay off. Once step 5 exists, add its numbers and messaging profile to
`config/staging-provisioning.json`. To let an Acuity team member sign in as
staff, add an Access Grant for their Google account.

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
7. Build the backend image with `RELEASE_ENVIRONMENT=staging` and the web image
   with those URLs, then bootstrap every service with
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
push to `main` with `_RELEASE_ENVIRONMENT=staging` and its own
`_PORTAL_API_URL` and `_REALTIME_URL` (from step 6) as literals, as the
production job does. Production then deploys a release only when its commit has
already passed staging.
