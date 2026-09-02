# Database execution and Calling pressure audit

Reviewed on 2026-09-02. This follows the availability incident in
[the original investigation](2026-09-02-conversation-calling-availability.md).
The immediate validator/UI repair was merged separately in PR #255. This audit and
its prevention changes belong to the follow-up PR.

## Coverage and limits

The source inventory is reproducible from the repository root:

```sh
go run ./scripts/audit-database-access.go > /tmp/database-operations.csv
```

The companion [operation index](2026-09-02-database-operations.csv) records each
production Go database call site, containing function, receiver, operation,
source location, literal SQL verbs/tables/CTEs, and unresolved expressions.
The inventory excludes generated handlers, tests, and test adapters. Forwarding
calls, transactions, and statements are counted separately; these are not
unique executed queries. It is a review index, not automatic reachability or
query-plan proof. `Prepare`, batch/copy adapters, direct LISTEN, health probes,
migrations, and provisioning are included. A second source search found no
additional raw driver or `database/sql` query path outside those boundaries.

All unresolved expressions were followed to fixed query builders/constants,
the generic executor's callers, embedded migration files, or the fixed LISTEN
channel. User data remains bound parameters. The five business-module family
review below covers every direct SQL/transaction-owning function in those
modules. HumanCalling, worker, and infrastructure review are recorded separately
below. Better Auth's generated SQL is reviewed as a pinned dependency boundary;
it is not miscounted as handwritten Go SQL.

Static coverage does not establish that every production query is fast. The
measured production cause was the Calling validator's historical JSON expansion.
Other findings are identified as source risks or locally reproduced failures.
A source audit and synthetic call tests cannot certify zero dropped live calls.


### Source inventory counts

| Boundary | Statements | Transaction operations | Other DB operations |
| --- | ---: | ---: | ---: |
| access | 48 | 25 | 0 |
| acuity | 0 | 3 | 1 |
| httpapi | 0 | 0 | 1 |
| humancalling | 338 | 222 | 0 |
| interaction | 21 | 25 | 0 |
| messaging | 65 | 87 | 0 |
| migrations | 8 | 7 | 0 |
| postgres | 7 | 7 | 5 |
| realtime | 1 | 0 | 1 |
| work | 32 | 20 | 0 |
| workspace | 12 | 16 | 0 |

Total: **952 source call sites**, including 36 expressions requiring manual resolution. The worker dependency Ping callback is an additional reviewed forwarding path; permit-gate Acquire calls are excluded because they do not access PostgreSQL.

## Execution ownership

| Entry | Database owner and lifetime | External work / recovery |
| --- | --- | --- |
| Browser Calling control | Portal route classification, `postgres.Executor`, HumanCalling transaction | Commit durable Call/CallLeg intent before provider execution; provider receipts establish outcome |
| Browser Calling sync | Same executor, non-control reservation | Current authorized state/detail; active media remains independently owned by the browser SDK |
| History, Tasks, Messaging, analytics | Background admission plus the same executor | Bounded admission; errors remain visible/retryable; no stale authorization fallback |
| Provider webhook | Separate ingress runtime and one-connection pool | Verify and durably record before acknowledgement; worker projects committed receipt |
| Worker | Separate two-connection pool, bounded receipt/command/maintenance lanes | Claims commit before normal provider I/O; reconciliation preserves uncertain outcomes |
| Workspace stream | Separate realtime pool plus one dedicated direct LISTEN connection | Initial authorization/revalidation release pooled connections; SSE retains only a hint subscription |
| Login / token | Better Auth through its own `pg` pool in the web runtime | Browser caches a signed token, shares a pending refresh, and backs off transient failures |
| Migration / provisioning | Separate migration job and one-connection pool | Ordered embedded migrations, reviewed grants, atomic provisioning; not customer request work |

The existing production reservation remains 36 usable application connections.
No additional workers, database connections, runtime, or environment switch is
introduced by this PR. See the [runtime contract](../../deploy/production-runtime-contract.md)
for arithmetic and availability tradeoffs.

## Invariants enforced by this change

1. **History cannot consume all Portal capacity.** Two background operations
   and three total non-control operations may use a four-connection Portal
   pool. Calling sync can use the third slot; commands can use the fourth.
   Handler-lifetime and connection-lifetime gates use one small policy. The
   database permit lasts through rows/transaction release, including nested
   acquisition. Background overload fails promptly with retryable `503` and
   `Retry-After: 1` instead of filling Cloud Run's request slots with waiters.
2. **Dependency failure is not access revocation.** Only an actual authorization
   denial maps to `403`. Pool, statement, and lock failures retain their cause
   and return a recoverable dependency error. This applies at domain boundaries,
   including authorization inside caller-owned transactions.
3. **A status-read failure does not speed up healthy readiness writes.**
   Readiness owns its retry counter. A readiness outage gets one quick retry,
   then backoff up to its existing staggered normal cadence. Active media and
   live controls keep their existing ownership. Idle nonowner tabs poll every
   10–10.5 seconds when visible and 15–15.5 seconds when hidden; focus and
   explicit takeover refresh immediately. Active/occupied runtimes and pending
   media keep their existing cadence. Hiding an idle nonowner starts no extra
   request, and hidden access revalidation remains bounded by 15.5 seconds.
4. **Auth cannot leave token consumers waiting indefinitely.** PostgreSQL
   cancels slow auth statements after five seconds; the browser bounds token
   acquisition and preserves a still-valid token on transient refresh failure.
5. **Attachment reads release database resources before storage I/O.**
   Authorization and metadata commit before retrieving bytes. Recovery and
   cleanup must preserve durable ownership and must not delete bytes merely
   because a successful commit acknowledgement was lost.
6. **Recovery work is bounded per tick.** Interrupted Messaging commands
   transition one oldest stale write per pass to UNKNOWN. They are reconciled,
   not automatically resent.
7. **Query timing starts before execution.** Both `QueryRow` adapters include
   the underlying driver's query time, allowing timeout telemetry to explain
   where the connection was occupied.

Admission protects pool/request capacity within each Portal instance. It does
not reserve database CPU or bypass row locks, and control bursts can still
compete with one another. Retryable background overload is deliberate pressure
control, not a promise of error-free requests under arbitrary load.

## Infrastructure and dependency trace

- `backend/cmd/acuity/main.go` constructs one executor per runtime; only Portal
  uses the reserved-capacity executor. Production domain modules receive that
  executor, not the raw pool. Raw pool uses are health probes, worker/runtime
  construction, and the separately executed migration/provisioning path.
- `backend/internal/postgres/executor.go` owns acquisition, statement and
  operation deadlines and releases its owned connection at the end of rows or
  transaction use. Wrapped nested transactions retain the parent lifetime.
  Generic `Prepare`, batch, and copy methods have no production domain caller
  in this source snapshot. No domain `Tx.Conn()` escape was found.
- `backend/internal/realtime/realtime.go` uses one intentional direct LISTEN
  connection with bounded jittered reconnect. Authorization/revalidation use
  the separate pool under `AccessTimeout`. The SSE stream holds no pooled
  connection; hints are capacity-one/coalesced, listener generations force
  reconstruction after reconnect, and committed rows remain authoritative.
- `backend/internal/migrations/migrations.go` executes trusted embedded SQL in
  sorted forward-only order. Each normal migration commits its schema change
  and journal entry together. Nontransactional migration statements have
  explicit completion probes and retry semantics. Runtime grants are reapplied
  in their own transaction. The 44 migration files and `database-grants.sql`
  are the schema/privilege source, not runtime caller-supplied SQL.
- Database triggers were checked separately from Go: the AI Task insert trigger
  adds one durable acknowledgement row; Messaging's legacy creator trigger
  fills a field on the same row; Message content and Task source triggers reject
  invalid changes. These are bounded per-row effects, not hidden external jobs.
- Atomic one-time provisioning also writes/syncs its operator output file while
  its transaction is open. This is a deliberate bootstrap-only path, separate
  from the Portal and worker request pools; it is not claimed to be free of I/O.
- Better Auth 1.6.25 selects its Kysely PostgreSQL adapter, which executes
  parameterized operations through the provided `pg` 8.23.0 pool. The reviewed
  families are user/account creation and identity lookup, session create/read/
  update/delete, verification consume/create/delete, and JWT signing-key reads/
  rotation. Reviewed grants restrict this role to the five `auth` tables.
  Better Auth's transaction helpers and Kysely's acquire/release boundary own
  those generated queries. Sign-up eligibility calls the Portal with a two-second
  deadline. This audit does not rewrite vendor SQL or claim a live auth incident
  was observed.
- `backend/cmd/receipt-audit/main.go` is a read-only operator command with one
  connection and a 15-second process context, not an HTTP or scheduled worker
  path. It does not replay receipts.

## Before and after proof

Local regressions use disposable PostgreSQL 16 databases and synthetic data.

| Scenario | Before | After |
| --- | --- | --- |
| Two real blocked history/analytics queries plus an eight-request burst | Readiness returned 503 when reads occupied the pool | Current bridged Call read, readiness commit, and two durable hangup commands progressed within milliseconds |
| Access lookup encounters database capacity/deadline failure | Some paths reported 403 / access denied | Retryable dependency failure; actual revoked access remains denied |
| Status/detail read unavailable while readiness succeeds | Warning could select the 500 ms heartbeat retry cadence | Healthy readiness retains its staggered 3.5–4 second cadence |
| Idle nonowner browser tabs | About 15 visible or 7 hidden state polls/minute | 5–6 visible or 3–4 hidden polls/minute, with immediate focus/takeover refresh |
| One auth connection executes a seven-second statement | Statement ran for 7.02 seconds | Server cancelled at 5.03 seconds; next query used the released connection |
| Token server stalls during refresh | Callers waited for the server's delayed response | Concurrent callers returned the unexpired token after the five-second request deadline |
| Attachment Get stalls with a one-connection pool | Unrelated Message read could not acquire the pool | Message read succeeds while byte retrieval is still blocked |
| Backlog of interrupted Messaging writes | One recovery transaction updated the entire backlog | One oldest write per pass; repeated passes converge as UNKNOWN without resending |
| QueryRow times out during driver execution | Timer started after the driver's work | A 40 ms database timeout records approximately 41 ms |

The call-control regression proves durable database commands, not actual Telnyx
hangup completion or audible media. Live production observation and provider
receipt convergence remain distinct evidence layers.

## Remaining structural work and release observation

The full family audit below identifies the remaining query-growth and storage
lifecycle risks. Each needs a production-shaped regression before changing its
ordering, ownership, or schema. Pool admission contains interference; it does
not make unbounded analytics scans cheap. The highest-value remaining design
work is a durable analytics projection, not larger timeouts or more polling.

Track new-revision latency, request volume, HTTP failures, pool occupancy and
acquisition timeouts, SQL CPU, command/receipt backlog age, and provider outcome
convergence independently. A green readiness endpoint is not an availability
or no-dropped-calls guarantee. Production uses a single-zone database without
automatic failover; that accepted infrastructure tradeoff is unchanged.

The existing alert policies reference a disabled delivery channel. No alert
setting is changed here; notification delivery is a separate operational choice.
Do not bulk replay uncertain provider commands or receipts.

## Business module family audit

The detailed source references below describe base commit `931e2b4` before the
follow-up changes. The generated operation index supplies current source lines.
Rows marked fixed distinguish completed prevention work from remaining risks.

### Findings from the baseline review

| Priority | Finding and exact source | Consequence / next proof |
| --- | --- | --- |
| P1, read fixed | Attachment upload begins a transaction at `messaging/attachments.go:97`, takes authorization/idempotency locks, and calls `AttachmentStore.Put` at `:204` before commit at `:314`. Attachment read calls `Get` at `:360` before commit at `:364`. | Object I/O holds a Portal pool connection and authorization locks. Reproduce with a blocked store and a one-connection disposable pool; split reservation/authorization, I/O, and guarded finalization without losing idempotency or revocation semantics. |
| P1 | Expiration deletes up to 50 attachment rows at `messaging/attachments.go:666`, then performs every object deletion at `:700–703` before commit `:705`. The production adapter ignores context and uses filesystem Write/Sync/ReadFile/Remove (`messaging/attachment_store.go:37–110`) over the documented Cloud Storage mount (`deploy/production-runtime-contract.md:168–175`). | A stalled mount can hold the worker connection and locks for the entire batch. Merely moving deletion after commit loses durable retry ownership; retain a tombstone/claim until deletion is acknowledged. Bounded claim size and restart convergence need tests. |
| P1, fixed | Inbound attachment copy deletes successfully copied bytes on finalization/commit errors before its deferred rollback (`messaging/attachments.go:530–577`), including unconditional cleanup after ambiguous COMMIT failure at `:575–577`. | A commit can have succeeded despite a lost acknowledgement; deleting the object can leave a stored attachment pointing at missing bytes. Preserve bytes until durable finalization is checked; test ambiguous commit and retry convergence. Error-path I/O also prolongs the open result transaction. |
| P2, fixed | Messaging interrupted-command recovery updates **all** stale `WRITING` commands at `messaging/messaging.go:1451–1465`, materializes them, and performs one message update plus workspace-version update per row before commit `:1485–1505`. No batch limit/SKIP LOCKED claim. | A backlog increases transaction duration and locks/notifications without a bound. Match the bounded HumanCalling maintenance pattern; prove one tick has bounded SQL work and repeated ticks converge without resending an uncertain provider write. |
| P2 | Operator analytics summary fetches every transcript and closeout in a 24-hour/7-day/30-day range (`interaction/analytics.go:226–238`, ranges `:467–474`) and parses each while the transaction remains open (`:244–257`, commit `:204`). Every page also reruns this summary. | Page limit 100 only bounds the call page, not summary work, JSON bytes, or Go processing time. Use a small durable metrics projection/aggregate owned by Interaction or another measured bounded design. Keep raw transcript evidence on the separate detail surface. Benchmark long payloads across a realistic 30-day corpus before claiming improvement. |
| P2 | Message-thread listing materializes every scoped candidate and computes message/call/task history aggregates (`messaging/messaging.go:1994–2080`) before applying cursor/LIMIT at `:2081–2091`. | The 50-row response does not bound the work needed to produce it. Cross-channel activity needs a durable, indexed latest-activity projection or equivalent measured query; simply moving LIMIT ahead of scoring changes ordering and is invalid. |
| P2 | Reconciliation selects `UNKNOWN` or `RECONCILING` commands (`messaging/messaging.go:1532–1538`) but the only scheduling index covers `PENDING` or `RECONCILING` (`migrations/sql/0011_messaging.sql:219–221`). Expiration also has no `OUTBOUND/PENDING/expires_at` index in the migration catalog. | The existing partial command index cannot cover the entire UNKNOWN predicate. Validate plans at historical cardinality and add narrow queue indexes only when the access path is demonstrated. No production plan proof yet. |
| P2 | Task list repeats complete scoped folder counts after each page (`workspace/tasks.go:104–112`, `:536–575`); search uses substring expressions and leading-wildcard phone matching (`:241–247`, `:549–553`) with no matching expression/trigram index found. | Cursor paging bounds returned rows but not counts/search scans. Separate page reads from counts when the UI does not need fresh counts; preserve exact counts where required and benchmark search before selecting an indexing strategy. |
| P3 | `DiscoverActor` runs binding/activation on each discovery (`access/access.go:868–878`); non-operators miss the fast operator lookup and acquire subject and email advisory locks (`:1482`, `:1538–1553`) even when already ordinary members. | Avoid assuming a pure read. Same-user overlapping polls can serialize during discovery. Any fast path must preserve concurrent provisioning, first identity binding, grant activation, revocation, and dynamic ALL scope. Current incident snapshots did not show this as the blocking owner. |
| P3 | Phone engagement lookup builds full exact-phone history across four sources twice (`workspace/workspace.go:125–192`, `:210–234`); Calling timeline combines phone identity with `COALESCE(handoff.phone, call.destination_phone)` and UUID-to-text scope comparisons (`workspace/timeline.go:368–382`). | Return cardinality is small, but joined/derived predicates can bypass useful index keys. Confirm plans for callers with substantial history; preserve exact-phone and practice/location authorization. These are growth risks, not measured faults. |

### Query-family coverage

### Workspace — 13 functions / 12 direct SQL call sites

- `QueryTasks`, `queryTaskFolderCounts`, `ReadTask`, `loadTaskInteractions`
  (`tasks.go:20,526,134,497`): one authorization transaction; list uses keyset
  cursors and `limit+1`, max 50. Priority/time/recent and open/completed branches
  are explicit. Folder counts scan all scoped matches. Detail loads all related
  interactions for one Task; relation count can grow without a response bound.
  Existing queue and task-interaction indexes are in migrations `0006`, `0007`,
  `0018`; dynamic recent ordering and casted scope/cursor expressions need plans.
- `QueryPhoneTimeline`, `QueryTimeline`, `queryTimelineMessages`,
  `queryTimelineCalls`, `queryPhoneInteractions`, `queryPhoneTaskActivities`,
  `queryConversationTasks`, `loadThread`
  (`timeline.go:27,94,220,385,432,512,550,582`): one transaction with current
  authorization; three/four source reads each receive `limit+1`, then a bounded
  in-memory merge. Cursor/time ordering and keyed cross-source IDs are explicit.
  Message and AI phone history indexes exist (`0011:104`, `0026:68`); Calling's
  joined phone expression and Task Activity's cross-task ordering merit plans.
- `QueryEngagements` (`workspace.go:102`): one exact normalized phone, all current
  authorized Locations, no external effect. Summary/location projection scans
  history as described above.

### Work — 23 functions / 32 direct SQL call sites

- Creation/linking: `EnsureCallFollowUp`, `EnsureMessageFollowUp`,
  `EnsureRecoveryTask`, `CreateAITask`, `insertTask`, `appendActivity`
  (`work.go:282,339,459,1219,1769,1742`). External callers pass their existing
  transaction except `CreateAITask`; exact source/idempotency/need indexes and
  `ON CONFLICT` prevent duplicate durable Tasks. Recovery serializes by
  practice+phone and locks the selected Task. Input fingerprinting occurs before
  `CreateAITask` begins its transaction. No provider I/O found.
- Recovery: `ResolveRecoveryTasks`, `lockRecoveryPhone`,
  `completeRecoveryTasksFromCheckpoint`, `ProcessNextRecoveryReconciliation`
  (`:683,743,757,842`). Normal completion is bounded by one practice/phone and
  its open recovery needs, with set-based activity insertion; reconciliation
  claims one key with `FOR UPDATE SKIP LOCKED LIMIT 1`. It reads latest committed
  call/booking evidence and advances one durable checkpoint. Historical per-phone
  evidence and related interactions can grow; do not replace receipt evidence
  with transcript inference or weaken exact-need identity.
- Acknowledgement: `ClaimNextTaskAcknowledgement`, `DeferTaskAcknowledgement`,
  `MarkTaskAcknowledgementQueued`, `MarkTaskAcknowledgementNotNeeded`
  (`:975,1042,1071,1101`) plus lock-only helpers: one indexed due acknowledgement
  with SKIP LOCKED and one-row guarded updates; Messaging owns external delivery.
- Mutations/readback: `ApplyCallTaskDisposition`, `RenameTask`, `CompleteTask`,
  `ReopenTask`, `ReadTask`, `loadTaskInteractions`, `loadTask`, `lockTask`,
  `loadTaskAcknowledgement` (`:1155,1376,1457,1545,1632,1668,1918,1969,2021`).
  One Task and atomic activity/version/audit; current Membership locks are
  intentional. Detail relation arrays are not paginated. `Work.ReadTask` is used
  by HumanCalling outbound eligibility (`humancalling/outbound.go:69`), so it is
  not dead merely because HTTP Task reads use Workspace.

### AI Interaction — 15 functions / 21 direct SQL call sites

- `read`/`Read`/`ReadEvidence`, `ReadOperatorAnalytics`
  (`interaction.go:192`; `analytics.go:402`): one ID and authorization transaction.
  The common interaction SELECT loads transcript/closeout even for routine
  detail; HTTP response projection can omit those bytes without avoiding the DB
  read. Operator detail performs expensive normalization after commit, a useful
  pattern to preserve. Require Admin/Operator for raw evidence.
- `QueryOutcomes`, `ReviewOutcome` (`interaction.go:239,510`): keyset page max 50,
  optional full attention counts controlled by `SkipCounts`, one-row review
  mutation and hint. No transcript selected for the outcome list. Attention's
  unreviewed user/time index exists in migration `0034`; excluded open-task
  existence and UUID scope casts need plan coverage.
- `QueryAnalytics`, `queryAnalyticsSummary`, `queryAnalyticsCalls`
  (`analytics.go:149,218,320`): raw evidence summary growth noted above; call page
  max 100 and time/id keyset; range max 30 days. One transaction includes summary
  and page normalization, so time range is not a cardinality/CPU bound.
- `acceptReceipt`, `ProcessNextReceipt`, `projectReceipt`, `lockBySourceCall`,
  `save`, `syncOutcomeAttention`, `quarantineReceipt`, `quarantinePendingReceipt`
  (`receipt.go:151,53,269,403,462,481`; `interaction.go:1013,1101`). Durable insert
  before projection; receipt row and practice/source advisory lock serialize
  one Interaction. Worker selects one pending supported receipt, then rechecks
  under lock; no SKIP LOCKED claim on its initial lookup, so parallel callers
  could perform duplicate waiting work, but durable projection is guarded.
  Evidence merge/JSON comparison occurs within the projection transaction.
  Attention fan-out is membership-scoped; recovery calls reuse the same tx.
  No external provider I/O found.

### Messaging — 31 functions / 65 direct SQL call sites

- Configuration: `Provision`, `ProvisionInTx` (`messaging.go:255,276`) loop only
  supplied configuration in the owner's transaction; no provider network call.
- Send/ack/retry: `Send`, `findOrCreateMessageThread`, `insertOutboundMessage`,
  `insertMessageProviderCommand`, `QueueNextTaskAcknowledgement`,
  `readMessageForRetry`, `loadSendAgainReplay`
  (`:345,761,809,847,878,1154,1209`). Durable one-message intent, exact sender
  configuration, Membership and original-message/attachment/task locks,
  idempotency constraints, max 1,600 text characters and one attachment. Explicit
  send-again preserves unknown-outcome acknowledgement. Automatic ack claim is
  one due task and observes expiry; no send provider call within these txs.
- Worker commands: `ProcessNextCommand`, `ReconcileNextCommand`,
  `RecoverInterruptedCommands` (`:1258,1514,1442`). Send and reconciliation claim
  one row with SKIP LOCKED, commit, call provider, then use a separate result tx.
  Unknown outcomes are not resent. Recovery is the unbounded exception above.
- Ingress/projection: `ReceiveWebhook`, `ProcessNextReceipt`,
  `projectOutboundReceipt`, `projectInboundReceipt`, `finishReceipt`
  (`:1666,1799,2494,2627,2887`). Signature/normalization precedes durable receipt;
  worker claims one event with 30-second reclaim handling and commits before
  parsing/projection. Projection locks a message/thread, inserts bounded message
  evidence and fans unreads to current scoped users. STOP atomically blocks its
  thread and fails all pending commands for that thread: fan-out is intentional
  and must not be weakened by arbitrary truncation.
- Reads/mutations: `ReadMessage`, `QueryThreads`, `MarkRead`,
  `CreateFollowUpTask`, `loadMessageByIdempotency`, `loadMessage`, `loadThread`
  (`:1904,1939,2191,2257,3003,3111,3215`). One ID or keyset 50-thread page; current
  authorization and one durable Task source link. QueryThreads performs history
  aggregation before its final bound, as noted above.
- Attachments: `uploadAttachment`, `OpenAttachment`, `OpenProviderAttachment`,
  `ProcessNextAttachment`, `RetryAttachment`, `ExpirePendingAttachments`,
  `loadAttachment` (`attachments.go:72,321,396,456,584,657,743`). Provider media
  read performs its DB check before object Get. Normal inbound copy correctly
  commits its one-row claim before download/Put (`:521–530`), then finalizes;
  upload/read/expiration and failure cleanup are the transaction-I/O exceptions.

### Access — 24 functions / 48 direct SQL call sites

- Provisioning and binding: `Provision`, `ProvisionInTx`,
  `accessGrantMatchesProvisioning`, `InspectSignUpEligibility`,
  `activateProvisionedEmail`, `bindPlatformOperator`,
  `lockPlatformOperatorIdentity`, `lockPlatformOperatorEmail`
  (`access.go:240,259,460,509,1042,1463,1533,1546`). Provisioning owns a global
  provisioning advisory lock and loops supplied practices/locations/grants;
  activation holds pending grant row locks and inserts memberships/scopes/audit.
  Subject/email locks protect binding races; do not remove them casually.
- Authorization/discovery: `ResolveActor`, `LockServiceAuthorization`,
  `LockServiceVoiceAuthorization`, `LockOperationalActor`, `DiscoverActor`,
  `resolvePlatformOperator`, `loadOperatorAuthorization`, `loadLocations`,
  `loadMembershipAuthorization` (`:553,684,731,787,858,1424,1559,1595,1726`).
  Mutation/read authorization helpers reuse the caller tx and acquire Membership
  SHARE locks to fence revocation. Discovery starts/commits its own tx and returns
  all current permitted Locations (or all practices for a Platform Operator).
  Administrative topology is unpaginated by design; no user-history JSON or
  provider I/O. Common non-operator discovery locking noted above.
- Administration/audit: `RevokeAccessGrant`, `RevokeMembership`, `AuditTrail`,
  `AddLocation`, `auditRevocation`, `AuditOperatorMutation`, `RecordWorkspaceChange`
  (`:1141,1208,1274,1334,1401,1622,1679`). Scoped row locks and atomic audits;
  workspace change updates one Practice version row then queues `pg_notify`
  (`:1688–1706`). That shared row is an intentional cross-module serialization
  point, so callers must keep transactions short. AuditTrail is full-history and
  has no paging, but current production reachability was not found.


## HumanCalling and worker family audit

### Query families

| Source family | Statement / transaction sites | Ownership, bounds, and review conclusion |
|---|---:|---|
| `humancalling/state.go` | 6 / 0 | Live Calling projection/validator. Staff-owned active Call and latest disposition candidate, active transfers, lease, and exactly the latest visible scoped Caller-backed voicemail. The historical voicemail serialization cause was already fixed in main by PR255. Current class is CallingSync; it can use the third slot of the four-connection pool while two heavy background requests run. |
| `humancalling/softphone.go` | 9 / 8 | Lease/readiness mutation serializes actor, sorted active Calls, recipient transfers, and own lease; commits before loading resulting current capacity. No provider network calls in the transaction. Access failure was collapsed into denial; now propagated. |
| `humancalling/calls.go` | 20 / 14 | Exact Call projection, page-bounded history (default25/max100, `QueryCallHistory:226`), exact-leg hangup and disposition. Hangup locks Call then matching legs and enqueues durable commands; provider work occurs later. `ExpireDispositions:659` claims at most100 Calls using SKIP LOCKED; bounded but can still perform100 Task/projection mutations in one transaction. `ReadOperatorTimeline:797` has an exact Call filter but no row limit across all timeline/command/receipt history: residual growth risk, not an established incident cause. Access/state query errors no longer become denial/conflict; bridge-evidence lookup at748 no longer discards errors. |
| `humancalling/outbound.go` | 39 / 30 | Idempotent outbound creation uses per-actor/idempotency advisory lock, exact Task/scope/lease/occupancy checks, Call+Leg creation and durable command enqueue. Media-ready confirms exact Call, Staff session, signed token and observed answer. Provider projection validates identifiers before state transition. Query errors in readiness/credential/Call/leg lookup no longer become ineligible/conflict. |
| `humancalling/staff_transfer.go` | 18 / 16 | Exact Call/source owner/session authorization; eligible recipient selection; idempotent transfer insert; response/expiry transition. `ExpireStaffTransfers:458` claims one Call with SKIP LOCKED then rechecks/locks exact transfer. Query errors now retained separately from actual wrong state/version/session/denial. |
| `humancalling/staff_transfer_projection.go` | 32 / 9 | Provider-fact-to-transfer projection under exact Call/transfer/CallLeg ownership. Existing conflicts represent evidence mismatch or invalid transition after successful DB reads. No provider I/O held inside DB transaction. |
| `humancalling/handoff.go` | 6 / 4 | Idempotent service-authenticated Handoff admission and Call/receipt enqueue, scope checked at Access boundary. Existing Access mapping already preserved dependency errors. Handoff is classified CallingControl because it starts live staff handoff. |
| `humancalling/callleg_projection.go` | 48 / 29 | Exact ProviderReceipt/Call/CallLeg fact matching and authoritative transition; prepares durable bridge/recording/termination work with Call→Leg ownership. Dynamic SQL variants are finite fixed forms (resolved below). Projection uses observed provider identifiers and duplicate guards. No provider network operation held by the SQL mutation transaction. |
| `humancalling/outgoing_callleg_control.go` | 36 / 10 | Single command claim with due state, dependencies, per-Call lane, SKIP LOCKED; claim committed before callback execution. Callback reads one command, Scan releases connection, invokes provider at220, then opens result transaction. Active ownership prevents duplicate effects; effect ambiguity remains explicit. |
| `humancalling/outgoing_callleg_control_maintenance.go` | 26 / 31 | Reclaim interrupted ownership or one stale/never-started leg per tick; old-state claims use LIMIT1, SKIP LOCKED and exact locked owner recheck. Reconciliation claim commits before provider ObserveCall at424. Structural query candidates/indexed pending paths already have production-shaped regression. No extra worker or connection change warranted. |
| `humancalling/outgoing_callleg_control_transitions.go` | 17 / 0 | Exact CallLeg/command state transition helpers inside caller-owned transactions; no independent pooled acquisition and no provider I/O. These AST statement sites are transaction forwarding, not additional connection lifetimes. |
| `humancalling/credentials.go` | 24 / 16 | Credential intent reconciliation, one ambiguous credential observation per claim, and media JWT authorization. Observation lookup runs after claim commit (`FindCredentialByName:217`). Media authorization commits before provider JWT creation. `ReconcileCredentials:15` processes set-based eligible users and all pending/disabling intents in a transaction: cardinality-growth exposure at scheduled frequency, no measured incident attribution. Lease/access errors now retained as retryable dependency errors. |
| `humancalling/connected_recording.go` | 17 / 25 | Recording receipts, one ambiguous recording claim (`LIMIT1:258`), one retention claim (`LIMIT1:473`), playback metadata authorization. ResolveRecording at281 and DeleteRecording at494 happen after claim commit; later transaction records result. Direct fact recording resolution at57 happens before mutation transaction. |
| `humancalling/voicemail.go` | 18 / 17 | Exact Caller voicemail lifecycle and recording outcome. Optional provider ResolveRecording at329 precedes mutation transaction. Playback authorization validates exact row and scope, appends audit and commits before OpenRecording at747/755; streaming does not hold a DB connection. Metadata/access lookup timeouts now retained as dependency errors. |
| `humancalling/webhook.go` | 17 / 10 | Verified incoming receipt/idempotency commit, bounded claiming and explicit quarantine/requeue authority. Requeue authorization dependency errors now preserved. Provider receipt ingress role still uses its own existing executor policy. |
| `humancalling/receipt_audit.go` | 2 / 3 | Repeatable-read, read-only administrative receipt audit (`AuditProviderReceipts:56`). Aggregates all receipt states and FAILED/QUARANTINED subsets with an allowlisted error vocabulary; no time or row cap. Result groups are small but underlying scan work grows with receipt history. No provider effects or recovery mutation; not a high-frequency portal route. |
| `humancalling/metrics.go` | 3 / 0 | Scheduled queue/terminal occupancy/command counts (`ReportReceiptQueue:57`), no content/PHI, no open transactions while emitting observations. Aggregate work can grow with table cardinality; no measured dominant query. |

### All twelve unresolved HumanCalling SQL expressions reconciled

The lines are current source lines at review; the CSV is the earlier baseline. None constructs SQL from a user-provided SQL fragment.

| Site | Resolution |
|---|---|
| `callleg_projection.go:100`, `recordingPolicyQuery` | Fixed Call→Practice SELECT for exact Call ID; conditional suffix is only the literal `FOR SHARE OF call, practice` when reserving recording. |
| `callleg_projection.go:1382`, `query` | Two fixed fact-match queries: exact provider control+leg IDs or exact signed client-state Call+CallLeg IDs; locks Call and CallLeg. |
| `outgoing_callleg_control.go:93`, `nextCallLegCommandQuery` | Constant at23: two candidates (Call-owned and global credential command), each LIMIT1 SKIP LOCKED and dependencies checked, final one-row oldest eligible pick. |
| `outgoing_callleg_control_maintenance.go:45`, `ownerPredicate` | Fixed ownership predicate selected by command kind (Call-owned vs global credential actions), then exact command ID; no external SQL. |
| `outgoing_callleg_control_maintenance.go:231`, `interruptedCallLegCommandQuery` | Constant210: oldest interrupted SENDING command older30s, Call-owned, LIMIT1 SKIP LOCKED. |
| `outgoing_callleg_control_maintenance.go:284`, `terminalNeverStartedCallLegQuery` | Constant86: terminal Call with never-started pending leg and no active command; older60s; LIMIT1 Call+Leg lock. |
| `outgoing_callleg_control_maintenance.go:369`, `staleCallLegCandidateQuery` | Constant108: one stale leg plus one latest relevant command via LATERAL LIMIT1; Call+Leg SKIP LOCKED. Commit occurs before external observation. |
| `staff_transfer.go:313`, `staffTransferSelect` | Constant605: fixed transfer projection with Location and one membership-email value per participant; appends exact transfer ID after commit. |
| `staff_transfer.go:650`, `staffTransferSelect` | Same fixed projection plus exact ID. |
| `staff_transfer.go:658`, `staffTransferSelect` | Same fixed projection plus exact ID and `FOR UPDATE OF transfer`. |
| `state.go:263`, `staffTransferSelect` | Same projection restricted to current Staff participant, active REQUESTED/ACCEPTED transfers and authorized scope. Bounded by active offers, not historical transfers. |
| `state.go:343`, `query` | Fixed Staff Call template; callers247/253 supply one of exactly two literals: owned BRIDGED nonterminal Call or latest owned ENDED undisposed Call. Scope/Staff filters and updated_at/id ordering, LIMIT1. |

### Worker runtime and existing controls

`worker/runner.go:136` starts seven independent lanes: Calling receipts, provider commands, AI receipts, Work recovery, outbound message commands, Messaging receipts, and maintenance. Dependency, credential and metric ticks run within the maintenance lane. Provider commands have one claim coordinator (`:222`) feeding a configured bounded executor channel (`:201`); production has ten provider-effect executors, which do not each poll independently. Receipt/provider claim batches are8; recovery/Messaging batches1. Idle work backs off to2s; consecutive failure backoff is250ms–10s; Calling receipts retain the250ms pickup interval. The worker keeps its existing two DB connections; portal admission does not alter worker/ingress/realtime executors.

`worker/runner.go:550–551` is a **callback-shaped database operation not directly represented as a CallExpr in the AST inventory**: `runner.dependency.Ping` is passed into `runWork`, invoked with HealthTimeout. Runtime injects the worker's pgx pool. Count it as a reviewed forwarding path, not an unexamined query or a missing SQL statement. Provider/receipt/Messaging lane interfaces likewise dispatch to owning module methods enumerated above or by the other audit owner.

No urgent-command priority exists across all eligible Calls; command picks are due/created order with per-Call dependencies and lock avoidance. Existing proof demonstrates progress across independent Calls; it does not prove end/answer priority under an arbitrarily large eligible-command backlog. The smallest follow-up would be a bounded mixed-command backlog latency test before changing ordering. No observed production evidence justifies adding worker executors or connections.

### Before/after proof and remaining limits

- `httpapi/call_control_capacity_integration_test.go`: real Pg4, two actual blocked history/analytics queries, eight simulated HTTP slots; six overflow reads fail fast503. Real Calling-state and readiness proceed, and hangup commits two PENDING HANGUP_LEG commands and two ENDING CallLegs. Measured local state14ms/readiness4ms/hangup6ms. This proves durable enqueue, not a provider hangup outcome.
- `postgres/portal_capacity_integration_test.go`: permits follow real row/transaction connection lifetime, nested acquisitions cannot bypass lower-priority caps, cancellation releases, and pool1/2 rejection is explicit for Portal only. Default executors retain behavior.
- Same file real `pg_sleep` statement-timeout test verifies QueryRow measurement includes underlying DB wait before Scan for both pooled and transaction QueryRow (approximately41ms, formerly near-zero).
- `httpapi/calling_access_dependency_integration_test.go`: Access table locks formerly produced four403s; lease table lock produced media-token403/hangup409. After preservation all six are retryable503; unlock recovers200; genuine missing/denied remains403 and missing credential/wrong session409.
- Existing `humancalling/reconciliation_capacity_integration_test.go:20` seeds2450legs/5565commands and exercises answer/bridge/hangup progression within500ms under the worker's two-connection budget. Tests214/284 prove one interrupted claim per tick and CallLeg-before-command lock order.
- Existing `humancalling/calllegs_integration_test.go:3447` verifies independent Call progress;3554 concurrent Staff dialing. `worker/runner_test.go:11,41,82,113,606` cover one idle poll, pickup interval, exact batch yielding, slow provider independent lanes and Calling-receipt polling.

Admission reserves access capacity within one Portal process. It does not guarantee latency under a slow query already using the reserved Calling slot, all-control saturation, conflicting Call locks, provider slowness, or CPU saturation across services. Protected Calling sync/control still receive visible retryable overload responses if their own budget is exhausted. Broader query tuning requires representative measurements; structural growth candidates above are not claims of a second production root cause.

## Bounded follow-up designs

- **Outbound attachment lifecycle:** upload still holds its authorization/
  idempotency transaction across Put. Its failure cleanup also cannot reliably
  distinguish a lost commit acknowledgement from a rollback. This is separate
  from the inbound-copy cleanup fixed here. A safe split needs an authorized
  provisional reservation, storage I/O outside SQL, reauthorization and fenced
  finalization, and a durable cleanup owner. Reusing PROCESSING without a fence
  allows a late Put after expiration. Tests must cover duplicate upload, revoked
  access during Put, crash before/after Put and COMMIT, and expired reservations.
- **Expired attachment cleanup:** replace the current up-to-50-row transaction
  containing object Delete with one expired reservation deletion plus an atomic
  cleanup-outbox key; Delete outside SQL; acknowledge the outbox only afterward.
  Prove idempotent deletion and restart convergence. A bare commit-before-Delete
  loses durable retry ownership and is not an acceptable simplification.
- **Analytics:** persist a compact close-time metrics projection owned by
  Interaction and aggregate that data; keep transcript/evidence detail separate.
  Benchmark all three ranges with large payloads and all authorization scopes.
  Current admission contains pool interference but does not reduce scan bytes.
- **Other history growth:** measure thread scoring, Task counts/search, long
  per-Call operator history and credential reconciliation at realistic
  cardinalities before choosing indexes or changing projections. Preserve
  exact cross-channel ordering and complete authorized counts.
- **Command urgency:** test an old eligible backlog containing credential,
  recording, bridge and hangup work. Change ordering only with proof that call
  latency improves while per-Call dependencies, fairness and uncertainty survive.

These are explicit remaining boundaries, not changes silently included in this
PR. No schema rewrite, production replay, automatic merge or additional alert
configuration is part of this follow-up.
