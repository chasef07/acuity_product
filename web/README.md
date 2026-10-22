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

Components never handle transport. Each backend feature has one module in
`src/lib/clients/` that exports named hooks and commands built on
`portalRequest`, which owns the access token, aborts, and `ErrorEnvelope`
normalization. A `signedOut` failure means nothing was sent; `unauthenticated`
means the backend answered 401. ESLint enforces the boundary.

Reads go through `useRead` and `usePages` in `src/lib/data/`, the only code
that imports TanStack Query. A read's `identity` names what it shows (scope,
filters, ids, search); TypeScript rejects identity fields named like
`revision`, `version`, `*Hint`, or `*Generation`. Live updates never reach
components as props: `useLiveRefresh` in the workspace invalidates the reads
that a workspace version, task counts, or detail refresh affects, and the cache
refetches them while keeping loaded pages on screen. A failed refresh keeps the
data and reports `refreshFailure`; lost access hides the data. While `usePages`
gets `hold`, live updates and retries only mark the loaded pages `behind`, and
releasing the hold refetches them. The conversation holds while the reader is
scrolled away from the latest activity and shows `behind` as "New activity".
`DataProvider` wraps only `/workspace` (`src/app/workspace/layout.tsx`), so
marketing pages ship without TanStack Query.

Code carries no comments. `pnpm lint` rejects them in TypeScript, JavaScript,
and CSS, allowing only tool directives such as `eslint-disable`.

Run `pnpm lint`, `pnpm typecheck`, and `pnpm build` before committing.
