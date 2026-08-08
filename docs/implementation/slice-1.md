# Slice 1 implementation note

Issue [#2](https://github.com/chasef07/acuity_product/issues/2) and the
product/architecture documents remain the contract. This note records the
approved test seams and the implementation choices made before code.

## Modules and interfaces

`Access` is the first deep product module. Its Go interface exposes only
provisioning, Access Grant activation, actor resolution, Location creation, and
audit lookup. Its implementation owns the focused SQL, transactions, dynamic
scope calculation, Platform Operator binding, operator audit enforcement,
workspace version increments, and audit writes. Deleting the module would
spread those rules across every caller, so the module provides depth, leverage,
and locality.

The other interfaces are:

- the versioned OpenAPI HTTPS interface, generated into the Go server seam and
  the TypeScript browser client;
- the Better Auth JWT/JWKS authentication adapter seam, which returns only a
  verified subject and verified email to `Access`; and
- the browser walking-skeleton seam from verified Google sign-in through Access
  Grant activation, authorized scope, and the empty workspace.

PostgreSQL is local-substitutable infrastructure inside the `Access`
implementation, not a generic repository interface. Tests use real PostgreSQL.
No Task, Call, Message, or Evidence module or model is introduced in this slice.

## Invariants and transitions

- Better Auth owns Google identity, sessions, JWT signing, and JWKS. `Access`
  owns Access Grants, Memberships, scope, Platform Operators, operator audit,
  and authorization.
- Public sign-up is rejected. Better Auth may create a Google-backed User only
  when `Access` finds an unrevoked Access Grant or Platform Operator record for
  the same verified email.
- Access Grant `PENDING -> CLAIMED` is atomic with Membership creation during
  authoritative Access discovery. A revoked grant, mismatched email, or
  conflicting Google subject cannot create another Membership.
- Admin is always dynamic `ALL`. Staff is dynamic `ALL` or explicit `SELECTED`.
  Client Practice/Location IDs are requested context and never authority.
- Platform Operator discovery and write authority are global. Each mutation and
  audit row commit together under the real operator identity; there is no
  impersonation.
- Realtime events are disposable hints. Initial connection and reconnect fetch
  a fresh short-lived JWT and the authoritative versioned workspace snapshot
  from `portal-api`.
- Only `migrate` changes schema. All migrations are forward-only. Every runtime
  uses a bounded PostgreSQL pool and bounded acquisition.

## Visible states and recovery

The browser interface explicitly renders `loading`, `empty`, `unauthorized`,
and `unavailable`. Realtime transport loss keeps the last committed snapshot,
retries quietly during a short grace period, then marks updates as delayed and
uses bounded jittered authoritative polling until the stream recovers. An
unavailable PostgreSQL dependency fails readiness and returns a retryable stable
error; it never renders false success. Authentication denials use stable error
codes without protected data.

SSE streams have a bounded lifetime, heartbeat, and authorization revalidation
interval. Membership revocation and unknown JWT keys are therefore bounded and
recoverable. An unknown JWT `kid` forces one
safe JWKS refresh before denial.

## Required configuration and performance

Required runtime configuration names the role, database URL, pool maximum,
acquisition timeout, Better Auth issuer/audience/JWKS URL, allowed web origin,
HTTP address, and SSE maximum lifetime/jitter/heartbeat/revalidation.
Next.js separately requires a bounded Better Auth pool, auth secret/base URL,
portal API URL, and Google OAuth credentials. Human authentication is
Google-only in every deployed environment. End-to-end tests create sessions
directly through Better Auth's gated test utility and do not carry a second
password or email-verification system. Runtime logs use a fixed PHI-free
structured event vocabulary rather than request or body data.

Each `Access` call uses one bounded acquisition and a short transaction where
needed. Workspace resolution is a bounded set of indexed queries. Non-production
deployment configuration includes service concurrency, maximum instances, pool
limits, overlap, dedicated listener connections, and explicit database
headroom.

## Frontend design plan

- **Color:** Zinc paper `#FAFAFA`, graphite `#18181B`, quiet chrome `#F4F4F5`,
  hairline `#E4E4E7`, and one Acuity teal signal `#0F766E`, all expressed as
  semantic OKLCH tokens with near-black dark equivalents.
- **Type:** DM Sans for the interface; JetBrains Mono/SF Mono only for IDs,
  versions, and connection metadata.
- **Layout:** a persistent compact sidebar and a solid central workspace:

  ```text
  ┌─ scope rail ──────────┬─ living workspace ───────────────────────┐
  │ Acuity                │ Tasks                         context     │
  │ Practice / Location   │                                        │
  │ navigation            │          empty, with direction          │
  │ search + fixed views  │                                        │
  │ user + connection     │                         composer shell  │
  └───────────────────────┴────────────────────────────────────────┘
  ```

- **Signature:** the scope rail keeps the active Practice and Location visible
  as the product's security context, not as decorative breadcrumb text.
- **Restraint:** no analytics cards, fake activity, fake patient data, gradients,
  or invented production facts. Motion is limited to one reconnect indicator
  and respects reduced motion.
