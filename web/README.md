# Acuity Portal web

This is the narrow Next.js and Better Auth frontend for the Acuity Portal.
Product authorization remains in the Go `Access` module and PostgreSQL.

Use Node 24. Install dependencies and run the development server:

```bash
corepack enable
pnpm install --frozen-lockfile
pnpm dev
```

Required server configuration:

- `AUTH_DATABASE_URL`
- `AUTH_DB_POOL_MAX`
- `AUTH_DB_ACQUIRE_TIMEOUT_MS`
- `BETTER_AUTH_URL`
- `BETTER_AUTH_SECRET`
- `BETTER_AUTH_TRUSTED_ORIGINS`
- `PORTAL_API_INTERNAL_URL`
- `PORTAL_API_AUDIENCE`
- `GOOGLE_CLIENT_ID`
- `GOOGLE_CLIENT_SECRET`

Required build-time browser origins:

- `NEXT_PUBLIC_PORTAL_API_URL`
- `NEXT_PUBLIC_REALTIME_URL`

The public working-session form posts to Formspree form `xgaevpbr`, configured
to notify `kyle@acuityhealth.io`. It does not require a database or deployment
secret.

Human authentication is Google-only. End-to-end tests use Better Auth's direct
test-session utility behind `AUTH_ALLOW_TEST_SESSION=true`; it creates no
password, verification email, recovery flow, or production HTTP capability.

Run `pnpm lint`, `pnpm typecheck`, and `pnpm build` before committing.
