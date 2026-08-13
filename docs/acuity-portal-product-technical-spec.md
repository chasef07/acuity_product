# Acuity Portal Product and Technical Specification

**Repository:** `acuity_product`
**Release:** Full production
**Release date:** Thursday, August 6, 2026
**Team:** Two people working daily
**Status:** Slice 1 ready for implementation

## Problem Statement

Medical-practice staff receive patient work through calls, voicemails, texts, and the AI receptionist. The work is fragmented across communication channels, individual memory, and disconnected follow-up processes. Staff cannot reliably see what still needs action, who owns it, what has already happened, or whether a patient need reached an outcome.

The result is avoidable administrative work and a persistent risk that a patient callback, scheduling request, message, or escalation falls through the cracks.

The product must make unresolved patient work durable without turning every communication into queue noise. It must automate task creation when the need is clear, keep humans responsible for ambiguous or consequential decisions, and preserve the complete communication evidence around each task.

## Solution

Acuity Portal is the operating workspace for patient work and discussion.

The existing AI receptionist remains in place. It either completes the caller's request, creates an asynchronous follow-up task, or transfers the caller to an available human. A live transfer does not create a task by itself. After an answered call, staff records `Resolved on call` or creates a follow-up task. Voicemails, meaningful unanswered calls, explicit message follow-up actions, and manual staff actions may also create tasks.

**Task is the primary product object.** A task contains one accountable piece of patient work. Calls, voicemails, recordings, transcripts, texts, notes, assignments, and status changes form its activity timeline. Opening a task also shows the practice/location's complete engagement history for the same normalized phone number while visually distinguishing older phone-number history from interactions attached to the current task.

Every task is visible in a shared queue. Assignment changes accountability, not visibility. Staff can claim, assign, discuss, call, message, prioritize, complete, and reopen work from one workspace. Completion is one click when linked outcome evidence already exists; otherwise staff selects a short completion reason. Completed work moves to the bottom instead of disappearing.

## Product Principles

1. **Unresolved work becomes durable.** A meaningful patient need must not exist only in a transcript, voicemail, notification, or staff member's memory.
2. **Resolved interactions do not create noise.** If the AI or a human completes the request during the interaction, no task is required.
3. **One task, one accountable outcome.** A task represents one piece of work, not an entire patient or communication channel.
4. **Assignment is accountability, not concealment.** Everyone with access to the location can still see assigned work.
5. **Communication is evidence.** Calls, recordings, transcripts, texts, and notes attach to the relevant task and remain visible in phone-number engagement history.
6. **Provider events are facts.** Browser intent is not proof that a call connected, a message delivered, or a recording completed.
7. **The database is authoritative.** PostgreSQL owns durable workflow state; realtime transports only project it.
8. **Automation removes work without hiding decisions.** AI-created tasks show their source and can be corrected by staff.
9. **Contact context is not patient identity.** Phone number, optional name, and AI handoff details help staff work but do not establish a canonical medical identity.
10. **Production means proven.** The product does not ship until every release-bar journey works against production-like dependencies.

## User Stories

1. As a practice staff member, I want to see every active task for my authorized locations, so that work does not disappear when another person claims it.
2. As a practice staff member, I want to see who owns each task, so that accountability is obvious.
3. As a practice staff member, I want to filter the queue to my tasks, so that I can focus on my work.
4. As a practice staff member, I want to see unassigned tasks, so that new work can be claimed quickly.
5. As a practice staff member, I want Emergency tasks surfaced before other work, so that the most important needs are seen first.
6. As a practice staff member, I want to sort tasks by priority, age, status, assignee, and recent activity, so that I can work in the order appropriate to the moment.
7. As a practice staff member, I want the default queue ordered by Emergency, Priority, and Routine and then oldest first, so that urgent work and aging work are both protected.
8. As a practice staff member, I want completed tasks to move to the bottom, so that the active queue stays focused without losing history.
9. As a practice staff member, I want to reopen a completed task, so that an accidental or premature completion is recoverable.
10. As a practice staff member, I want to create a task by entering only a title, so that manual capture is faster than remembering the work.
11. As a practice staff member, I want contact context, priority, and assignee to be optional during manual creation, so that incomplete context does not block capture.
12. As a practice staff member, I want manual tasks to default to Open, Routine, the current location, and unassigned, so that creation requires no setup work.
13. As a practice staff member, I want to change a task between Open and In progress, so that the queue reflects whether work has started.
14. As a practice staff member, I want to complete a task with one click when outcome evidence exists, or select a short completion reason otherwise, so that completion stays both fast and trustworthy.
15. As a practice staff member, I want completion to record the actor and time automatically, so that the history remains trustworthy.
16. As a practice staff member, I want to change a task's title, contact context, priority, assignee, and status, so that AI and human mistakes are correctable.
17. As a practice staff member, I want to add an internal note to a task, so that the team can coordinate without contacting the patient.
18. As a practice staff member, I want patient messages and internal notes to have clearly different composer modes, so that an internal comment is never accidentally sent.
19. As a practice staff member, I want calls, texts, notes, recordings, transcripts, assignments, and status changes in one task timeline, so that I do not reconstruct context across pages.
20. As a practice staff member, I want activity attached to the current task distinguished from older engagement history, so that the current piece of work remains understandable.
21. As a practice staff member, I want the complete call and SMS history for the same practice/location and phone number visible when I open a task, so that I do not reconstruct context across pages.
22. As a practice staff member, I want phone number, optional name, and AI handoff context displayed with their source, so that I know what information was actually supplied.
23. As a practice staff member, I want calls, voicemails, and texts captured even when only a phone number is known, so that missing profile context does not erase work.
24. As a practice staff member, I want phone-number history treated as engagement context rather than verified patient identity, so that shared numbers do not silently merge people.
25. As a practice staff member, I want the AI receptionist's task tool to create a task immediately, so that clearly identified follow-up does not need re-entry.
26. As a practice staff member, I want AI-created tasks labeled with their creator and source, so that I understand where the task came from.
27. As a practice staff member, I want to expand the relevant transcript evidence behind an AI-created task, so that I can verify its interpretation when needed.
28. As a patient, I want the AI receptionist to complete my booking when it can, so that no unnecessary human task is created.
29. As a patient, I want the AI receptionist to transfer me to a human when it cannot complete my request, so that I can continue without starting over.
30. As a staff member, I want to toggle Available or Unavailable in the Call Center, so that only prepared staff receive live transfers.
31. As a staff member, I want a transfer offer to include the available contact and AI handoff context, so that I understand the need before answering.
32. As a staff member, I want exactly one staff member to win an offered call, so that two people never answer the same patient.
33. As a patient, I want the transfer to fall back after 20 seconds if nobody answers, so that I am not left ringing indefinitely.
34. As a practice staff member, I want a voicemail to create an Open task with recording and transcription, so that the message becomes actionable work.
35. As a practice staff member, I want an unanswered call without voicemail to create a Return missed call task, so that it does not disappear into a call log.
36. As a practice staff member, I want abandoned or spam calls without meaningful caller context to remain in the call log rather than creating tasks, so that the queue stays useful.
37. As a staff member, I want to answer, mute, use the keypad, hold, transfer, and end a human call from the engagement workspace, so that calling does not require a separate product.
38. As a staff member, I want the active human call to remain mounted while I inspect SMS and call history, so that navigating the workspace cannot accidentally destroy the call.
39. As a staff member, I want connected inbound calls recorded automatically for an enabled Practice, so that the resulting work has durable conversation evidence.
40. As a staff member, I want connected outbound calls recorded automatically for an enabled Practice, so that follow-up conversations are captured.
41. As a staff member, I want an outbound call started from a task linked automatically to the task with its contact snapshot, so that I perform no filing work.
42. As a staff member, I want to place a standalone outbound call from the Call Center, so that I can contact a patient before a task exists.
43. As a staff member, I want a standalone outbound call to create a call record but not an automatic task, so that completed conversations do not pollute the queue.
44. As a staff member, I want to record `Resolved on call` or create a prefilled follow-up task after a human call, so that every answered call receives an explicit disposition.
45. As a staff member, I want every inbound patient text captured in its exact Location-scoped conversation without automatically creating work, so that communication is durable without adding queue noise.
46. As a staff member, I want to create at most one follow-up Task explicitly from a Message, so that accountability is a human decision with a durable source.
47. As a staff member, I want to send SMS/MMS from a conversation or linked Task and see provider-backed delivery state, so that patient communication is visible to the team.
48. As a staff member, I want AI to draft a patient response while requiring my confirmation before send, so that writing is faster without silently sending consequential content.
49. As an administrator, I want to provision staff emails and assign authorized locations, so that access matches operational responsibility.
50. As an administrator, I want to see all tasks across the practice's locations, so that I can supervise the complete workload.
51. As a staff member, I want to see only data for authorized practices and locations, so that patient information does not cross tenant boundaries.
52. As an operator, I want failed provider actions and unavailable recordings to be visible and retryable, so that failure cannot masquerade as success.
53. As an operator, I want duplicate AI requests and provider webhooks to produce one durable result, so that retries do not duplicate work.
54. As an operator, I want task updates to appear promptly in other staff browsers, so that the shared queue remains coordinated.
55. As an operator, I want the portal to recover from a lost realtime connection by loading a fresh authoritative snapshot, so that stale sockets cannot lose state.
56. As an operator, I want searchable human call history and protected voicemail and connected-call playback, so that communication evidence remains accessible only to authorized staff.
57. As an operator, I want short-lived protected access to recordings rather than public URLs, so that sensitive audio is not exposed.
58. As a product owner, I want every critical journey exercised against production-like Telnyx, SMS, Better Auth, and database dependencies before launch, so that August 6 is a production release rather than a demo.
59. As a provisioned user, I want my verified Google identity to activate my assigned access automatically, so that no separate Acuity password or verification email is required.
60. As an administrator, I want every current and future location in my practice to be available automatically, so that adding a location does not silently exclude practice leadership.
61. As a Platform Operator, I want to discover every practice and location while working inside an explicit active scope, so that I can support customers without mixing their work.
62. As a Platform Operator, I want to change customer data directly under my real identity, so that operational work stays simple and every elevated action remains auditable.

## Implementation Decisions

### 1. Scope and system boundary

- The portal, product backend, database, task workspace, human Call Center, SMS workflow, recording archive, and operational infrastructure are greenfield.
- Human accounts, credentials, sessions, Practice configuration, Location configuration, and product data also begin fresh. No code, schema, account, password, session, or application-data migration is part of the release.
- The existing AI receptionist and its booking behavior remain external systems.
- The AI receptionist integrates through authenticated APIs. It receives no portal database credentials.
- The first release has no EHR dependency, canonical patient record, or medical-identity verification flow.
- The portal stores a task- or interaction-specific `ContactContext`: normalized phone number when available, optional display name, optional AI handoff details, and the source of each value.
- The portal owns tasks, assignments, communication activity, human call state, call disposition, contact snapshots, and audit history.

### 2. Product vocabulary

- The canonical domain glossary is [CONTEXT.md](../CONTEXT.md).
- **Practice:** one customer tenant and security boundary containing one or more locations.
- **Location:** one physical or operational office within a practice.
- **Abita office route:** one Abita Agent office key mapped to the operational Location that owns its calls and Tasks. Multiple Abita office routes may converge on one Location.
- **Membership:** one user's Admin or Staff role in one practice plus an `ALL` or `SELECTED` location scope.
- **Platform Operator:** an internal Acuity Health user with global visibility and direct audited write authority under their own identity.
- **Task:** the primary object and one accountable piece of patient work.
- **Interaction:** a call, voicemail, SMS message, or staff note that may exist with or without a task.
- **Contact context:** a snapshot of the phone number, optional name, and handoff details known for one task or interaction. It is not a global person or verified patient identity.
- **Engagement history:** calls and messages found by practice, location, and normalized phone number for display as context. Display does not attach those interactions to the current task.
- **Activity:** a chronological task entry such as an interaction, assignment, status change, or priority change.
- **Queue:** a query over tasks; it is not call-routing configuration or a second source of state.
- **Call:** the logical human-call session projected from provider legs and events.
- **Provider event:** signed evidence received from Telnyx or another external system.

### 3. Task rules

- Status is one of `OPEN`, `IN_PROGRESS`, or `COMPLETED`.
- Priority is stored as `P1`, `P2`, or `P3` and displayed as Emergency, Priority, or Routine.
- Priority controls default ordering; it does not represent a clinical diagnosis.
- Due time is optional. The release does not impose a due date or SLA on every task.
- Every task belongs to exactly one practice and one location.
- Every task has zero or one assigned staff member.
- An unassigned task remains visible in the shared queue.
- Assignment changes accountability, not visibility.
- Sending an SMS or starting a call from an unassigned task atomically assigns it to the acting staff member and moves it to `IN_PROGRESS`. Opening or reading the task does not claim it.
- If two staff members take a consequential action concurrently, the first committed action wins. The losing action is rejected before any provider side effect and shows the new owner.
- Every task mutation requires the last observed task version so stale edits cannot silently overwrite current state.
- Completion requires linked outcome evidence or a short explicit completion reason. It records the actor and timestamp and remains one click when sufficient evidence already exists.
- Completed tasks sort below active tasks and can be reopened.
- The default active order is priority and then oldest creation time.
- Staff can override the default sort without changing shared state.

### 4. Task creation rules

- A successfully resolved AI or human interaction creates no task.
- The AI task tool creates a task immediately only when the AI chooses asynchronous follow-up. It does not create a task for a live human transfer of the same need.
- A voicemail creates an Open task with recording and transcription.
- A meaningful unanswered call without voicemail creates an Open Return missed call task.
- An inbound SMS/MMS creates or appends to the exact Practice, Location,
  configured office sender, and normalized external-phone conversation. It
  never creates or reopens a Task automatically.
- Staff may explicitly create at most one follow-up Task from one Message.
  Repeating that action returns the same Task; completed Tasks remain completed
  unless a human reopens them.
- A human may create a task manually through the quick composer.
- Human inbound and outbound calls do not blindly generate tasks from every transcript.
- A live AI-to-human transfer creates a call and offer, not a task.
- After an answered human call, staff selects `Resolved on call` or `Create follow-up task`.
- `Create follow-up task` prefills the call, contact snapshot, AI handoff, and relevant engagement context; assigns the answering staff member; and starts `IN_PROGRESS`.
- A call remains durably `NEEDS_DISPOSITION` if the browser closes or staff leaves before choosing an outcome.
- AI-created tasks expose their creator and source. Detailed rationale remains expandable rather than permanently occupying the workspace.

### 5. Contact-context rules

- `ContactContext` is stored as a snapshot on each task and interaction, not as a shared global Contact record.
- A provider may supply a phone number. The AI transfer may additionally supply a display name and handoff details.
- Every context value records its source: provider, AI transfer, or staff entry.
- Phone-number history is scoped to one practice and location and is presented as context, not verified patient history.
- Opening a task may display prior engagement for the same normalized number, but it does not relink or mutate those prior records.
- Outbound calls and messages launched from a task copy the task's current contact snapshot.
- Canonical patient identity and EMR-specific adapters are deferred.

### 6. Experience model

The portal is a greenfield, desktop-first, sidebar-native operating workspace
inspired by the persistent work-canvas grammar of Codex and T3 Code. It is not
an analytics dashboard and does not reuse an existing portal implementation.

- A persistent left sidebar owns practice/location context, primary navigation, search, filters, the current section's dense work list, and the signed-in user's status.
- Selecting a task, call, recording, or setting changes the central canvas without replacing the application shell.
- In Tasks, the sidebar contains the prioritized queue and the central living engagement workspace contains contact context, the current need, task controls, a chronological communication timeline, and the persistent composer.
- The timeline shows inbound and outbound SMS, calls, voicemails, notes, recordings, transcripts, assignments, and status changes.
- Current-task interactions are clearly distinguished from older phone-number engagement history.
- Calls stay inline in the engagement workspace.
- Patient message and internal note are explicit composer modes.
- An active call uses the same workspace and shows the available contact snapshot, AI handoff, SMS history, and call history.
- Active call state and controls remain mounted while the user changes sidebar selection or opens context.
- Context opens in a right-side drawer and may pin only on sufficiently wide displays.
- No patient-profile drawer or EMR-derived medical profile ships in the first release.

Primary navigation:

1. **Tasks** — default shared operating workspace.
2. **Call Center** — availability, ringing CallLegs, active human calls, and standalone dialer.
3. **Voicemail** — protected playback of provider-owned voicemail recordings.
4. **Settings** — practice, location, staff, routing credentials, and integrations.

Fixed task views:

- All active
- Mine
- Unassigned
- Emergency
- Completed

Visual system:

- Tailwind CSS 4 with shadcn/ui's Mira style, Zinc color base, and Base UI accessible primitives; do not use Radix UI.
- Lucide React provides icons. CVA owns named component variants, and `tailwind-merge` composes classes through one helper.
- DM Sans is the interface typeface. JetBrains Mono with SF Mono fallback is reserved for technical metadata; ordinary times and call timers use tabular numerals.
- Light mode is the initial default. A near-black dark mode has full feature parity, and the user's choice persists.
- CSS variables own light/dark theme and operational semantic colors. Raw utility colors do not encode task, call, delivery, or failure state.
- The base corner radius is approximately 10 pixels. Use thin borders, shallow shadows, restrained motion, and compact desktop controls with larger coarse-pointer targets.
- Translucency and subtle grain are limited to application chrome, overlays, and the persistent call surface. Task rows, patient communication, and consequential controls remain solid and high contrast.
- The interface uses panels, hairline separation, and a communication thread rather than a grid of generic dashboard cards.
- Add shadcn source components only when the current vertical slice uses them; do not preinstall or prebuild a generalized component library.

### 7. Call Center behavior

- Staff explicitly toggle Available before receiving transfers.
- Only staff authorized for the call's practice and location are eligible.
- Platform Operator visibility alone never grants calling access. An operator
  receives Staff calling controls only where the same identity has an explicit
  active Staff Membership.
- An AI-to-human transfer answers the caller, starts one 20-second ringback window, and asks Telnyx to dial one independent CallLeg for every available, authorized staff member for the Location.
- The transfer creates no task.
- The first eligible provider-confirmed Staff answer moves directly to `BRIDGE_PENDING` and commits one explicit Bridge command in the same PostgreSQL transaction.
- Later Staff answers are ended; the browser never elects the winner.
- If no Staff CallLeg bridges during the full ring window, HumanCalling starts the Location greeting and voicemail sequence.
- A voicemail creates a task; a meaningful missed call without voicemail creates a callback task.
- The bridged Staff member's workspace is populated with the available phone number, optional name, AI handoff details, SMS history, and call history.
- When an answered call ends, its durable disposition becomes `NEEDS_DISPOSITION`.
- `Resolved on call` closes the disposition without creating a task.
- `Create follow-up task` creates one prefilled task linked to the call, assigned to the winner, and initially `IN_PROGRESS`.
- Human call state is not considered connected solely because a browser clicked Answer.
- Telnyx provider events confirm ringing, answered legs, bridging, recording, and termination.
- A Practice-level policy may record every portal-controlled connected inbound and outbound human call automatically. Recording starts on the explicit Telnyx Bridge command with dual channels, both tracks, and MP3 output; it has no browser control.
- Abita Eye Group has connected-call recording enabled by default with a 30-day content-retention period. Other Practices remain disabled unless explicitly provisioned with a retention period.
- AI-only receptionist audio is outside the portal recording archive.
- Browser calling uses one TelnyxRTC client per tab. The Telnyx SDK owns its signaling WebSocket and WebRTC media.

### 8. SMS behavior

- Telnyx sends signed inbound and delivery webhooks to the Go backend.
- One conversation is keyed by Practice, Location, configured office sender,
  and normalized external phone. The same external phone at two Locations is
  never one thread.
- Signed inbound SMS/MMS is durably receipted before asynchronous projection.
  It appends to the exact conversation and creates no Task automatically.
- Staff send SMS/MMS from the Message workspace or an `OPEN` linked Task through
  one authenticated durable command. The configured office sender is
  server-owned and cannot be supplied by the browser.
- The visible delivery states are `Sending`, `Sent`, `Delivered`, `Failed`, and
  `Status unknown`. Only provider evidence advances acceptance or delivery.
- Interrupted or ambiguous provider writes become `Status unknown` and are not
  blindly retried. Reconciliation is read-only when provider identity exists;
  `Send again` is a new immutable attempt and requires duplicate-risk
  acknowledgment for an unknown outcome.
- One JPEG, PNG, GIF, WebP, or PDF attachment up to 600 KB is copied into
  protected application storage. Provider URLs are short-lived and signed.
- `STOP` immediately blocks later outbound commands and fails commands that
  have not begun their provider write. `START` removes the block. A write
  already in progress remains evidence-driven.
- Opening a conversation marks it read only for the current user. An `OPEN`
  linked Task shows the same unread state; completed Tasks never show unread.
- AI may draft a message, but staff confirms before sending in this release.

### 9. Service architecture

Use a small modular monolith in one repository. Build one Go binary and one
immutable backend image, then run that image in isolated runtime roles:

```text
Next.js web application
    ├── React + TypeScript
    ├── Tailwind CSS 4 + shadcn/ui Mira/Zinc + Base UI
    ├── DM Sans + Lucide React + CVA + tailwind-merge
    ├── TelnyxRTC browser client
    └── Generated OpenAPI client

Go product runtime — one codebase, binary, and image
    ├── Access
    ├── Work
    ├── HumanCalling
    ├── Messaging
	└── runtime adapters
        ├── portal-api
        ├── provider-ingress
        ├── realtime
        ├── worker
        └── migrate

PostgreSQL
    └── Sole durable product source of truth
```

- `Access` owns human and service principals, Access Grants, Memberships, roles, location scope, and authorization decisions.
- `Work` owns task creation, assignment, priority, status, completion, reopening, task activity, and queue projections.
- `HumanCalling` owns availability, Call and CallLeg state, provider-confirmed answer arbitration, explicit Bridge commands, disposition, canonical voicemail lifecycle, connected-call recording metadata, protected playback, access audit, retention, and provider content deletion. Telnyx decides voice outcomes and owns audio bytes; PostgreSQL records requested commands, observed facts, durable recording identity, and non-content deletion evidence.
- `Messaging` owns Location-scoped conversations, inbound correlation, durable
  send intent, delivery evidence, attachment state, per-user unread state, and
  explicit send-again attempts. It asks `Work` to create a Task only after an
  authorized human explicitly chooses a source Message.
- `ContactContext` is a small value object used by tasks and interactions, not an independent identity service.
- The modules expose behavior-oriented interfaces. HTTP handlers, SQL, Telnyx, Better Auth/JWKS, object storage, SSE, and durable jobs remain replaceable adapters around them.
- The browser calls `portal-api` directly. Next.js does not proxy ordinary product commands.
- The Go runtime uses standard `net/http` with a small router, `pgx`, explicit SQL, and generated query bindings.
- A narrow OpenAPI document is the browser/backend contract and generates the TypeScript API client. CI fails when the generated client is stale or a change breaks the supported contract.
- Use forward-only SQL migrations.
- Do not split the backend into microservices.
- Runtime-role isolation is an operational deployment choice, not a domain split. The roles share modules and schema and never call one another over a private domain API.
- Keep one backend codebase, one backend image, and one database. Do not create role-specific repositories, schemas, or duplicated business logic.

Runtime roles:

| Role | Deployment | Responsibility | Isolation reason |
|---|---|---|---|
| `portal-api` | Cloud Run service | Authenticated commands and queries, including latency-sensitive call control | Webhook bursts, streams, and jobs cannot consume command capacity |
| `provider-ingress` | Cloud Run service | Telnyx voice, messaging, recording, and transcription webhook receipt | Provider acknowledgment remains short and independently scalable |
| `realtime` | Cloud Run service | Authorized SSE update hints | Long-lived streams cannot occupy command request slots |
| `worker` | Cloud Run worker pool | Provider-event projection, job/outbox execution, retry, and reconciliation | Durable work continues independently of request traffic |
| `migrate` | Cloud Run job | One reviewed forward-compatible product and auth schema migration per release | Migrations never run during ordinary instance startup |

### 10. Transport decisions

```text
Browser ── HTTPS commands ─────────────> portal-api
realtime ── SSE state hints ───────────> Browser
Browser ── TelnyxRTC WSS signaling ────> Telnyx
Browser ══ WebRTC human-call audio ═══> Telnyx
Telnyx ── signed HTTPS webhooks ───────> provider-ingress
Existing AI agent ── HTTPS task tool ──> portal-api
portal-api ── committed commands ──────> Telnyx
worker ── retries/reconciliation ──────> Telnyx
all Go roles ── bounded connections ──> PostgreSQL
```

- HTTP carries staff commands and AI tool requests.
- Command responses return committed state, stable IDs, and the new row version immediately. The UI renders requested or connecting state without waiting for a provider webhook.
- SSE carries one-way task and message update hints. Calling state uses one single-flight authoritative poll, every two seconds while visible and approximately ten seconds while hidden.
- SSE payloads contain stable IDs and monotonically increasing versions, not authoritative state.
- SSE streams have a bounded lifetime, heartbeats, and jittered reconnect. Initial connection and every reconnect reload the current authorized snapshot from `portal-api`.
- PostgreSQL `LISTEN/NOTIFY` may wake `realtime` instances through one dedicated direct connection per instance; committed rows remain the only durable state.
- Telnyx's SDK-managed WebSocket is provider signaling and is not an application state socket.
- The portal does not add a general application WebSocket.

### 11. Durable processing and failure behavior

- Every externally retryable command accepts an idempotency key.
- AI task creation stores an idempotency receipt scoped to the integration and practice.
- Human and service mutations record distinct actor types and stable actor IDs. The AI uses its own scoped service identity and never impersonates a Better Auth user.
- Telnyx webhook signatures are verified over the raw request.
- `provider-ingress` inserts each provider event ID once behind a unique constraint and creates or confirms its processing job in the same short transaction.
- `provider-ingress` returns `2xx` only after durable receipt commits. If PostgreSQL is unavailable, it returns a retryable `5xx` instead of acknowledging data it did not store.
- Webhook receipt does not perform normalized projection, download media, or call another provider. `worker` applies the provider fact asynchronously and idempotently.
- Background work uses a narrow PostgreSQL job/outbox table claimed with `FOR UPDATE SKIP LOCKED`. Do not add Kafka, Redis, or a generalized orchestration platform.
- Shared rows are locked in one deterministic order. Serialization failures and deadlocks retry the complete bounded transaction, not only its final statement.
- Commands that contact a provider commit ownership, intended state, and a durable provider-command record first. No provider request runs while a PostgreSQL transaction remains open.
- `portal-api` issues latency-sensitive provider commands immediately after commit using a stable command ID. `worker` retries or reconciles when the process dies or the provider response is uncertain.
- Provider command IDs remain stable across retry attempts and are persisted beyond the provider's duplicate-suppression window.
- User-visible state distinguishes requested, provider-confirmed, failed, and recoverable operations.
- Failures never silently advance a task or call to success.
- Commands that both claim work and contact a provider commit ownership first and reject stale competitors before issuing the provider request.
- Calls awaiting a human outcome remain durably visible as `NEEDS_DISPOSITION`; a browser disconnect cannot resolve or discard them.

### 12. Core data model

- **Practice**
- **Location**
- **User**
- **PlatformOperator** for the small set of internal Acuity users with global visibility
- **Membership** with Admin or Staff role and `ALL` or `SELECTED` location scope
- **MembershipLocation** entries for each explicitly selected location
- **AccessGrant** with email, practice, role, location scope, revocation, and claim state
- **ServiceIdentity** with minimum practice/location scope
- **Task** with tenant, location, contact-context snapshot, source, title, status, priority, optional assignee, version, optional due time, and completion metadata
- **Activity** with task, actor, type, timestamp, and normalized display payload
- **Interaction** with channel, direction, provider identity, contact-context snapshot, optional task, and lifecycle timestamps
- **Call** with logical identity, direction, terminal outcome, disposition, and human-segment timing
- **CallLeg** with role, provider identity, Staff lease snapshot, monotonic lifecycle timestamps, bridge evidence, and termination evidence
- **Voicemail** with provider recording identity, timing, availability, protected playback, and recovery Task
- **CallRecording** with connected Call identity, provider recording identity, timing, availability, and protected playback
- **Message** with provider message identity, direction, delivery state, contact-context snapshot, and optional task
- **ProviderEvent** with provider, unique event identity, receipt state, processing state, and normalized linkage
- **ProviderCommand** with stable command identity, target, action, requested state, attempt state, and provider-event reconciliation
- **IdempotencyReceipt**
- **AuditEvent**
- **Job** for named durable background effects

The database stores current relational state plus append-only activity and audit history. This is not full event sourcing.

### 13. API boundaries

The first API surface should remain narrow:

- Task list, task detail, manual creation, edit, claim, assign, status, priority, complete, reopen, and note commands
- Phone-number engagement-history query scoped to practice and location
- AI integration task-creation command
- Call Center readiness/state, outbound initiation, exact media confirmation, privileged call-control, and post-call disposition commands
- SMS send command
- Recording and transcript lookup with short-lived protected access
- Telnyx voice and messaging webhook receivers
- SSE update stream

The AI task-creation command requires:

- service authentication
- practice and location scope
- idempotency key
- stable source interaction or call identity
- title and source
- priority
- optional phone number, display name, and AI handoff context with source metadata
- optional supporting summary and transcript excerpt

The AI task tool is for asynchronous follow-up only. A live human transfer must not call it for the same need.

### 14. Authentication and authorization

- Human access is Google-only and preauthorized by email. Public sign-up is disabled.
- Google proves identity but does not grant access: Better Auth User creation fails closed unless `Access` confirms an unrevoked Access Grant or Platform Operator record for the same verified email. The first authorized Access discovery atomically claims a pending Access Grant and creates its exact Membership.
- Password authentication, verification email, recovery email, MFA, magic-link authentication, and customer-managed SSO are out of scope for the first release.
- Better Auth in Next.js owns Google sign-in and browser-session lifecycle.
- The browser obtains a short-lived Better Auth JWT for direct `portal-api` calls.
- The Go `Access` module verifies signature, issuer, audience, and expiration locally against cached Better Auth JWKS. It does not call Next.js on every request or read Better Auth session tables as an authorization mechanism.
- Better Auth proves who the human is. PostgreSQL membership data and the Go `Access` module are the sole authority for what that human may do.
- Practice and location IDs supplied by a client are requested context, never proof of access. `Access` resolves and enforces the allowed scope for every command, query, SSE stream, and evidence grant.
- Admin always has `ALL` location scope for the practice. Staff may have `ALL` or `SELECTED` scope.
- `ALL` includes every current and future location in the practice. `SELECTED` includes only explicit membership-location grants.
- Platform Operators are not duplicated as Admin memberships in every practice. They can discover and read every current and future practice/location but still select an explicit active practice and location.
- A Platform Operator mutates customer data directly under their real identity. Every operator mutation records the real Platform Operator in the same transaction and never impersonates a customer user.
- Each Access Grant is email-bound and revocable and specifies Practice, role, and Location Scope before first sign-in.
- Practice, Location, Abita Office Route, Platform Operator, and initial Access Grant records are created through an auditable provisioning path that accepts business facts, not human credentials. Provisioning may map several Abita office keys to one operational Location, but each office key has exactly one Location owner within its Practice. Established topology reconciliation is idempotent by provisioning key.
- Integration credentials are separate service identities with the minimum required practice/location scope. Mutations record `actor_type=service` and the stable service actor ID.
- Do not duplicate practice/location authorization in Better Auth organization permissions.
- Better Auth may use the same PostgreSQL instance, but its tables remain private to Better Auth and the Go runtime never mutates them.
- Telnyx JWTs are generated server-side; Telnyx API keys never reach the browser.

### 15. Infrastructure

- Default the web, all Go runtime roles, Cloud SQL, recording storage, and dependent regional resources to `us-east1` (South Carolina) for current Florida users. Geography is only the starting assumption; measured Florida-to-`us-east1` latency remains a live acceptance gate.
- Use Cloud SQL PostgreSQL 16 Enterprise edition, single-zone, at 2 vCPU / 8 GiB with 50 GiB SSD initially. Enable storage auto-increase, automated backups in `us-east1`, seven days of point-in-time recovery, deletion protection, and a rehearsed restore procedure. Do not use Enterprise Plus or data cache.
- Accept the single-zone tradeoff explicitly: a database or zone outage does not automatically fail over. Telnyx retries and durable receipt/command recovery protect correctness, but portal and call control may remain unavailable until database recovery or restore.
- Keep one web, `portal-api`, `provider-ingress`, and `realtime` instance warm, and one worker fixed. The web is the public website and authentication entrypoint.
- Pin every request role and worker initially to 1 vCPU / 512 MiB. Request roles use request-based billing; the worker and migration job use instance-based billing.
- Give every runtime role its own explicit Cloud Run concurrency, minimum-instance, maximum-instance, and `pgxpool` limits.
- Give every runtime role a distinct Google service identity and least-privilege database role. Only `migrate` receives DDL authority.
- `migrate` applies version-controlled product SQL and reviewed Better Auth schema SQL. Runtime Go modules never read or mutate Better Auth-owned tables.
- Before sizing, record the 12-month capacity envelope: peak concurrent staff, active calls, open SSE streams, command rate, webhook burst rate, daily messages, PostgreSQL data volume, and evidence-retention volume.
- Calculate the PostgreSQL connection reservation before deployment:

  ```text
  sum(each service's maximum instances × its pool maximum)
    + fixed worker connections
    + Next.js and Better Auth connections
    + dedicated LISTEN connections
    + migration, autoscaler-overshoot, and operator headroom
  ```

- The checked pilot reservation is exactly 36 usable client connections: 20 across configured request-service maxima, 4 for one old plus one new worker revision with two connections each, 1 migration connection, 8 for one extra instance of every request role, and 3 operator/recovery connections. Cloud Run may temporarily exceed a configured maximum during rapid spikes or maintenance, so live overshoot and saturation remain release gates rather than a claimed hard platform ceiling. Retain the existing maximum service burst bounds and recalculate the reservation after every pool or instance change.
- Normal maximum application pools must leave explicit headroom for overlapping revisions, recovery reconnects, migrations, and operator access.
- Use small direct `pgxpool` pools initially. Do not add managed transaction pooling until measurements justify it; `LISTEN/NOTIFY` retains a dedicated direct connection.
- Every role uses bounded acquisition, exponential backoff with jitter, and only safe idempotent retries from the beginning of the operation. A zonal database outage is visible until recovery; no runtime may convert it into false success.
- Store secrets in Secret Manager.
- Generate internal application credentials through approved tooling or deployment automation. Do not commit them, paste them into product configuration, or ask an operator to invent them manually.
- Keep recording audio in Telnyx. Store canonical lifecycle, authorization metadata, durable provider recording identity, timestamps, and audit evidence in PostgreSQL; stream audio through short-lived, location-authorized backend access.
- Require an approved recording retention period in each enabled practice's production configuration. Deny playback at expiry, delete content through the provider with durable retry state, and retain canonical non-content audit metadata.
- Use one public product domain with path routing to the web and API services.
- Publish immutable frontend and backend images by digest. Deploy backend roles from the same tested backend digest.
- Deploy request-role revisions with no traffic and smoke-test startup, run `migrate` once, exercise the tagged revisions against the expanded schema, deploy the compatible worker and realtime revisions, then shift traffic gradually.
- The CallLeg replacement uses one guarded maintenance cutover. Admission closes, old revisions and in-flight work drain, a snapshot and parity checks complete, the destructive migration runs once, and only the new runtime reopens admission. There is no mixed-schema rollback after reopen.
- Every runtime handles termination by stopping new work, releasing job leases, closing pools, and ending streams. Correctness never depends on shutdown cleanup completing.
- Rollback switches staff and provider routing to the old portal, freezes new-portal writes, and preserves all new database state for reconciliation. It never destructively reverses the database.
- Confirm executed BAAs and service eligibility/configuration before protected health information enters production.

### 16. Observability

- Emit structured logs without patient names, phone numbers, transcript content, message bodies, or recording URLs.
- Trace commands through database transaction, provider command, provider event, and visible state transition.
- Record metrics for task creation, provider receipt and processing delay, webhook acknowledgment, duplicate suppression, job/outbox depth, pool acquisition and saturation, SSE connections and reconnects, Staff answer races, answer-to-bridge time, voicemail creation, missed-call creation, SMS delivery, and voicemail readiness.
- Partition operational metrics by runtime role and revision so a noisy role or unsafe rollout is visible.
- Alert on failed webhook verification, slow or failed durable receipt, growing unprocessed-event backlog, failed durable jobs, database saturation, pool wait, elevated call-bridge failure, and cross-tenant authorization denial anomalies.

## Testing Decisions

### Highest testing seam

The primary test seam is the complete observable product journey:

> External provider/AI event or staff action → authenticated Go command → PostgreSQL transition → visible portal state.

This is the highest useful seam because it proves the product promise while allowing Telnyx and Better Auth boundaries to be simulated deterministically.

### Test strategy

- Test external behavior and durable invariants, not controller, repository, hook, or SQL implementation details.
- Use real PostgreSQL integration tests for concurrency, unique constraints, authorization scope, idempotency, and lifecycle transitions.
- Exercise `portal-api`, `provider-ingress`, and `realtime` through their public interfaces and `worker` through durable jobs.
- Exercise the portal with Playwright against the real runtime roles and provider simulators.
- Maintain signed Telnyx webhook fixtures for voice, recording, transcription, and messaging events.
- Run controlled live Telnyx acceptance tests before release for the paths that simulation cannot prove: stable REFER normalization, WebRTC readiness, independent fan-out, answer, explicit Bridge event shape, media, voicemail recording, and SMS delivery.
- Run every production journey in two browser sessions to verify shared visibility and realtime recovery.

### Required invariant tests

1. Two simultaneous provider-confirmed Staff answers produce exactly one provisional bridge winner and one durable Bridge command.
2. Replaying an AI task request produces one task.
3. Replaying, reordering, or concurrently delivering provider events does not corrupt call or message state.
4. A browser click cannot mark a call connected before provider evidence.
5. A completed task remains queryable and can be reopened.
6. Assignment does not remove the task from another authorized staff member's All active view.
7. An unauthorized practice or location cannot read, mutate, stream, or fetch recording access for another tenant.
8. Phone-number history is displayed as context and cannot silently relink older interactions to the current task.
9. SSE disconnect and reconnect yields the authoritative current snapshot without losing updates.
10. A failed post-commit process is eventually retried from durable PostgreSQL state.
11. An unanswered 20-second transfer produces the correct voicemail or callback task.
12. An answered AI transfer creates no task until staff chooses `Create follow-up task`.
13. An undisposed answered call survives browser loss and remains `NEEDS_DISPOSITION`.
14. `Resolved on call` creates no task; `Create follow-up task` creates one prefilled, assigned, `IN_PROGRESS` task.
15. A standalone completed outbound call creates a call record but no task.
16. A task-originated outbound call attaches to the task and copies its contact snapshot.
17. Inbound SMS/MMS appends only to the exact Location/sender/phone
    conversation, creates no Task automatically, and supports one explicit
    Message-derived follow-up Task.
18. Two concurrent actions on an unassigned task produce one owner and at most one provider side effect.
19. Completion records linked outcome evidence or an explicit completion reason.
20. Logs and error responses remain free of protected content.
21. Killing `portal-api` after database commit but before or after a Telnyx request converges through the durable provider command without duplicate effect.
22. At least ten repeated production-shaped PostgreSQL runs send 25 simultaneous correctly signed webhooks through the real handler under the 1.5-second deadline; every response is `204`, acknowledgement p99 is below one second, duplicates converge, one worker advances receipt and command lanes, 10 concurrent Staff commands complete, and portal/realtime database paths remain responsive under ingress pressure.
23. The 36-connection reservation covers configured service bounds, one-old/one-new worker overlap, one extra instance of each request role, migration, and operator recovery. A failed reduction records pool wait and transaction latency, then retains the smallest passing capacity.
24. Cloud SQL outage and restore produce visible failure and controlled recovery without false success or lost acknowledged work.
25. SSE timeout, instance death, and revision rollout reconnect to an authoritative snapshot without losing state.
26. Killing a worker mid-job releases or expires its lease and safely resumes without double-applying the effect.
27. Adding a location immediately grants access to Admin and `ALL`-scope Staff memberships but not `SELECTED`-scope Staff memberships.
28. A Platform Operator can read every practice but cannot mutate customer data without an active Support Session for that practice; mutations retain the real actor, reason, and audit trail.
29. Public sign-up is rejected, and a provisioned verified Google email activates only its assigned Membership without an Acuity password or verification email.
30. Sidebar navigation, context opening, and realtime reconnect do not unmount or lose an active call or the selected engagement workspace.

### Performance targets

- Staff commands acknowledge committed state within 300 ms at p95, excluding provider network time.
- A committed task or call-state change becomes visible in another active browser within 2 seconds at p95.
- Initial task workspace load completes within 1.5 seconds at p95 under the target production dataset.
- Provider-confirmed Staff answer to provider-confirmed bridge completes within 3 seconds at p95 in the controlled acceptance environment.
- Signed provider webhooks commit durable receipt and return `2xx` within 2 seconds at p99 under the target burst load when PostgreSQL is healthy.
- The 20-second ring-window fallback is accurate within one second.
- Duplicate tasks and duplicate bridged winners remain zero under replay and concurrency tests.
- Pool acquisition time and total open connections remain inside the tested per-role budget during peak load and overlapping deployment.

## Production Release Bar

The August 6 release does not ship until all conditions are proven:

1. Staff can sign in and see only authorized practice/location data.
2. Tasks can be created manually, by the asynchronous AI task tool, an explicit
   Message follow-up action, voicemail, unanswered calls, and the post-call
   follow-up action.
3. Staff can prioritize, assign, claim, edit, move between Open and In progress, complete, reopen, filter, sort, and search tasks.
4. Completed tasks move to the bottom without disappearing.
5. Opening a task shows its linked activity and the complete practice/location phone-number engagement history without merging identities.
6. Available staff can receive an AI transfer and exactly one person wins the call.
7. An answered transfer creates no task until staff records `Resolved on call` or `Create follow-up task`; undisposed calls cannot disappear.
8. Unanswered transfers fall back after 20 seconds and create the correct voicemail or missed-call task.
9. Portal-controlled connected human inbound and outbound calls record successfully under the configured retention policy.
10. Outbound calls started from a task attach to that task and preserve its contact snapshot.
11. Staff can receive and send SMS/MMS from a Location-scoped conversation or
    linked `OPEN` Task, see provider-backed delivery state, and explicitly
    create follow-up work from a Message.
12. Provider events and AI task requests are authenticated, idempotent, and cannot create duplicates when retried.
13. Concurrent task actions cannot overwrite ownership or issue duplicate patient contact.
14. Cross-practice/location data access is denied and ordinary logs contain no protected content.
15. Scoped staff Access Grants, short-lived evidence access, deployment, backups, rollback, monitoring, and the complete production journey are tested before launch.
16. API commands, provider ingress, realtime streams, durable workers, and migrations run in their specified isolated runtime roles from one tested backend image.
17. The maximum connection budget holds under peak traffic, webhook bursts, worker backlog, SSE load, and an overlapping rollout.
18. Cloud SQL outage/restore, runtime termination, webhook retry, worker recovery, SSE reconnect, and traffic rollback are rehearsed without data loss or false success.
19. The approved capacity envelope and burst factor are exercised successfully with measurable database, pool, runtime, and provider headroom.
20. Provisioned Google access, dynamic location scope, direct audited Platform Operator writes, and the persistent sidebar workspace pass their authorization and browser acceptance journeys.

## Rollout Plan

### Two-person operating model

- Each vertical slice has one directly responsible owner and one reviewer.
- Both people implement across schema, backend, frontend, tests, and deployment for the slice they own.
- Avoid frontend/backend handoffs.
- Merge only demoable, green slices.
- Run the critical journey at the end of every day.
- Stop adding scope after Monday, August 3.
- Wednesday, August 5 is a release-candidate day, not a feature day.

### Daily plan

| Date | Outcome required by end of day | Owner A focus | Owner B focus | Verification gate |
|---|---|---|---|---|
| Thu Jul 23 | Product contract, architecture, release bar, and tickets approved | Spec and system invariants | Rendered experience inventory and acceptance journeys | No unresolved scope decision blocks implementation |
| Fri Jul 24 | Deployed walking skeleton | One Go image, runtime modes, PostgreSQL, migration job, Better Auth/Access, connection budget | Next.js shell, generated client, deployment, first Playwright journey | Sign in → authorized `portal-api` → empty workspace; roles use bounded pools |
| Sat Jul 25 | Live provider spine proven end to end, even with rough UI | `HumanCalling`, isolated `provider-ingress`, durable `worker`, Telnyx voice adapter, recording/transcript receipt | TelnyxRTC, simultaneous offer, one-winner UI, active-call workspace, disposition | AI transfer → offer → one winner → bridge → durable webhook receipt → projection → disposition → optional task |
| Sun Jul 26 | Manual task loop works end to end | `Work` model, commands, optimistic concurrency, audit, queries | Queue, quick create, assignment/status/completion/reopen UI | Create → assign → In progress → evidence/reason → Complete → bottom → Reopen |
| Mon Jul 27 | Living engagement workspace and realtime coordination work | Task activity, phone-history query, isolated `realtime`, SSE/versioning | SMS-first workspace, unified timeline, reconnect/refetch behavior | Two authorized browsers see current task; SSE restart reconstructs the authoritative snapshot |
| Tue Jul 28 | AI-created asynchronous tasks work | Scoped service identity, idempotent AI command | AI source and handoff presentation | Replay the same AI request; exactly one unassigned Open task appears |
| Wed Jul 29 | Location-scoped SMS/MMS conversations work | `Messaging`, signed webhooks, durable send/reconciliation, delivery and attachment state | Message rail, mixed timeline, composer, explicit Message-derived Task | Exact Location/sender/phone thread; inbound creates no Task; explicit Task link; no blind retry |
| Thu Jul 30 | No-answer and call-disposition recovery work | 20-second fallback, voicemail, missed call, durable `NEEDS_DISPOSITION` | Recovery UI, post-call outcomes, prefilled follow-up task | Timeout falls back; browser loss preserves disposition; both outcome paths pass |
| Fri Jul 31 | Outbound calling and call controls work | Task-originated and standalone outbound commands and linkage | Dialer, mute, keypad, hold, transfer, end, persistent active-call workspace | Task call links to task; standalone resolved call creates no task |
| Sat Aug 1 | Human call evidence works | `HumanCalling`, protected grants, retention/deletion jobs | Call history, voicemail and connected-call playback, failure states | Human-call facts and recording evidence appear, expired provider content is deleted, and unauthorized access fails |
| Sun Aug 2 | Administration and complete access boundaries work | Access Grants, Memberships, service scope, authorization audit | Staff/location settings and access-denied states | Verified Google email activates exact scope; cross-location reads/writes/streams fail |
| Mon Aug 3 | Feature complete; scope closes | Failure recovery, durable jobs, audit, tenant authorization | Empty/error/reconnect states, accessibility, full journey cleanup | All release-bar journeys pass in simulation |
| Tue Aug 4 | Production hardening complete | Load/concurrency, connection ceiling, Cloud SQL outage/restore, backups/PITR, rollback, alerts, PHI-log audit | Cross-browser Playwright, SSE/runtime termination, performance, operator runbook | Peak load, role isolation, restore, replay, backup, rollback, and observability gates pass |
| Wed Aug 5 | Release candidate frozen and rehearsed | Production deployment rehearsal and provider configuration audit | Full staff rehearsal and UI blocker fixes only | Live Telnyx, SMS, recording, transcription, and AI-tool acceptance pass twice |
| Thu Aug 6 | Full production release | Deploy, monitor backend/provider/database | Launch smoke, operator support, UI monitoring | All gates green; enable production routing; rollback immediately on release blocker |

### August 6 release sequence

1. Confirm database backup, deployable previous revision, provider failover routing, and on-call ownership.
2. Deploy the release candidate with production routing disabled.
3. Run sign-in, manual task, AI task, SMS, live transfer, fallback, outbound call, recording, transcript, and tenant-isolation smoke tests.
4. Enable production task-tool and messaging webhooks.
5. Enable human transfer routing.
6. Monitor call bridging, task creation, webhook backlog, SMS delivery, recording readiness, error rates, and database capacity continuously.
7. If a release-bar journey fails, switch staff and provider routing to the old portal, freeze new-portal writes, and preserve all new data for reconciliation.
8. Do not perform schema contractions, cleanup migrations, or unrelated feature releases on launch day.

## Out of Scope

- Autonomous AI completion of queued tasks
- Automatic transcript-to-task creation for every human call
- Custom statuses or priority labels
- Required due dates or a configurable SLA engine
- Custom roles and permission builder
- Custom saved views or filter builder
- Advanced analytics or manager dashboards
- Practice-facing call-routing and offer-timeout configuration
- AI-only receptionist recording archive in the portal
- Canonical patient identity, patient-profile merging, EMR lookup, or medical-chart ingestion
- Automatic identity merging or historical relinking by phone number
- New EHR or scheduling integrations
- Native mobile applications
- Full event sourcing
- Microservices
- Kafka, Redis-owned product state, or a generalized workflow engine
- General application WebSockets
- Multi-region active-active infrastructure

## Further Notes

- The frontend is greenfield. The approved reference defines only the persistent sidebar and living-workspace grammar; no existing portal code, component, stylesheet, account, or session is reused.
- The product name in code and interfaces should use Acuity Portal; provider adapters should not leak Telnyx terminology into the task domain.
- Emergency, Priority, and Routine are operational queue labels, not clinical assessments.
- The first release's name and AI context are operational hints, not verified medical identity. The UI describes longitudinal results as phone-number engagement history.
- The August 6 date is achievable only if the scope fence remains closed after ticket approval and both people work in vertical slices with daily integration.
- The first implementation slice must prove the live provider spine by July 25 before isolated component-library, database-framework, or speculative provider-abstraction work.
- One modular monolith means one domain implementation, backend image, and PostgreSQL authority. Runtime-role isolation prevents unlike workloads from sharing a failure and scaling domain; it does not create microservices.
- TelnyxRTC owns its browser signaling WebSocket while WebRTC carries media; product commands remain HTTP and live product updates use SSE. See [Telnyx WebRTC signaling](https://developers.telnyx.com/development/webrtc/js-sdk/explanation/webrtc-signaling).
- Telnyx webhooks are verified, acknowledged quickly, deduplicated, and interpreted as provider evidence. See [Telnyx Voice API webhooks](https://developers.telnyx.com/docs/voice/programmable-voice/voice-api-webhooks).
- The concurrency, connection-budget, failure-isolation, and rollout rationale is recorded in [Backend concurrency and resilience review](research/backend-concurrency-resilience-review.md).
