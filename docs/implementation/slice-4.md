# Slice 4 implementation note

Issue [#11](https://github.com/chasef07/acuity_product/issues/11) is the
controlling product contract. This implementation changes Acuity Product only.
It accepts the request shape already emitted by Abita's `create_staff_task`
tool; it does not change Abita or redirect its production destination.

## Request path and ownership

1. `POST /v1/tasks` authenticates the existing opaque Abita service credential.
2. `Access` owns the Practice-scoped service identity, its `CREATE_TASK`
   capability, and the explicit `officeKey` to Location route. Provisioning
   reconciles those routes so removed configuration cannot retain access.
3. `Work` validates the complete command and opens one PostgreSQL transaction.
   It locks the current route, creates one `OPEN` `ABITA_AI` Task, appends one
   service-authored `TASK_CREATED` Activity, and advances the existing Practice
   workspace version.
4. A repeated service-subject and idempotency-key pair returns the existing Task
   only when its immutable-input fingerprint matches. Changed content returns a
   conflict without mutating the committed Task.
5. The existing SSE message remains a disposable Practice/version hint.
   Browsers refetch the protected Task query and PostgreSQL remains the source
   of truth.

The current tool's optional `patient.name` is reduced to source-labeled Contact
Context. Its compatibility-only `patient.id` and `patient.dob` fields are
accepted so the existing tool can call this interface, but their raw values are
not stored or returned. Unknown request fields are rejected.

## Task contract

- State remains exactly `OPEN` or `COMPLETED`.
- Human follow-up Tasks retain their Acuity Call link and `normal` urgency.
- AI Tasks have no Acuity Call link. They retain immutable source call ID,
  request message, category, urgency, callback phone, optional caller name, and
  service provenance.
- The Abita summary is the initial editable Task title. Rename, complete, and
  reopen reuse the Slice 3 lifecycle and optimistic version behavior.
- Default Open ordering remains oldest first. The optional priority ordering is
  `high_priority`, `normal`, then `non_urgent`, oldest first within each group.
  Completed ordering remains newest completion first.
- Search remains limited to Task title and normalized phone. Immutable AI source
  detail does not become a new search index.

## Browser behavior

The rail adds one compact time/priority control persisted per User and Practice.
AI Tasks have a quiet `AI` marker, and opening one shows its immutable source
card. Refetches preserve the current selection; creation adds no modal, sound,
notification, or automatic focus change.

Provisioning maps each Abita source office key to one controlled operational
Location. Several source offices may converge on one Location; Hollywood and
Sweetwater intentionally converge on South Florida Medical. Production routes
remain inert until an operator runs the reviewed one-time provisioning command
and separately completes the provider and service-credential gates.

## Deterministic proof

PostgreSQL integration tests cover authenticated HTTP acceptance of the current
tool payload, source projection, invalid fields, authorization, safe and
concurrent replay, changed-payload conflict, multiple outcomes from one source
call, immutable source detail, Activity/workspace atomicity, protected search,
and time/priority cursor ordering. The production provisioning contract is also
executed through the real migrate command against PostgreSQL and asserted at
the resulting Abita Office Route, voice-number, Messaging-sender, and voicemail rows.

The generated Go and TypeScript clients, backend tests, frontend lint,
typecheck, unit tests, production build, and existing Playwright journeys are
the repository verification boundary. A real Abita tool invocation and a
production routing change remain separate operational gates because this
implementation does not modify `abita_agent`.
