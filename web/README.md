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
- `AUTH_EMAIL_MODE=smtp`
- `SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASSWORD`, `AUTH_EMAIL_FROM`

Required build-time browser origins:

- `NEXT_PUBLIC_PORTAL_API_URL`
- `NEXT_PUBLIC_REALTIME_URL`

The captured email adapter is test-only and requires both
`AUTH_EMAIL_MODE=test` and `AUTH_ALLOW_TEST_EMAIL=true`.

Run `pnpm lint`, `pnpm typecheck`, and `pnpm build` before committing.
