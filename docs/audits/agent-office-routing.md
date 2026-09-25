# Agent office routing audit — 2026-09-25

## Scope and evidence

Read-only source audit of fetched `origin/main`: Product `558e904`, S2S
`af44fad` (includes PR #82), Agent `0d98f80`, middleware `912f419`.
This is a source/configuration inventory, not proof of deployed routing or delivery.
No other repository needed an implementation change for the supported paths below.

Sources:

- S2S: `src/abita_s2s/offices.py`, `runtime/reporting.py`, `staff_tasks.py`,
  `handoff.py`, `config.py`.
- Agent: `src/customers/abita/profile.ts`, `runtime/session-startup.ts`,
  `runtime/portal-auth.ts`, `runtime/middleware-routing.ts`,
  `tools/create-staff-task.ts`, `tools/handoff.ts`.
- Middleware: `internal/domain/office.go` (`prodOffices`, `devOffices`).
- Product: `config/production-provisioning.json`, Access service authorization,
  Interaction receipts, Work Task creation, HumanCalling handoff/provisioning.

## Complete inbound inventory

Numbers below are office routing configuration, not patient data. Both agents
support every production row. Only Agent supports the three demo rows; S2S
rejects demo/unknown inbound numbers. All production rows map to the
`abita-eye-group` Practice; all demo rows map to `acuity-demo`.

| Inbound number | Product office key → Location | Middleware clinical route | Product staff VoiceNumber |
| --- | --- | --- | --- |
| +17275919997 | spring-hill → spring-hill | spring_hill | +17275919997 |
| +18135484830 | spring-hill → spring-hill | spring_hill through canonical +17275919997 | +17275919997 |
| +13523202007 | crystal-river → crystal-river | crystal_river | +13523202007 |
| +19542872010 | hollywood → hollywood | hollywood | +19542872010 |
| +17864657475 | sweetwater → sweetwater | sweetwater | +17864654836 |
| +17864654845 | sweetwater → sweetwater | sweetwater | +17864654836 |
| +17866134310 | sweetwater → sweetwater | sweetwater | +17864654836 |
| +17864657479 | sweetwater-optical → sweetwater-optical | sweetwater | none |
| +17864654836 | sweetwater → sweetwater | sweetwater | +17864654836 |
| +17864654882 | sweetwater → sweetwater | sweetwater | +17864654836 |
| +13055095333 | north-miami-beach-optical → north-miami-beach-optical | north_miami_beach_optical | +13055095333 |
| +14843989071 | rheumatology-demo → demo-484 | sandbox spring_hill | +14843989071 |
| +18027878312 | ophthalmology-demo → ophthalmology-demo | sandbox spring_hill | +18027878312 |
| +13207388132 | new-tampa-demo → new-tampa-demo | sandbox spring_hill | +13207388132 |

Product additionally retains office-key aliases `dev` → `demo-484` and
`mental-health-demo` → `new-tampa-demo`, inside the demo Practice. These are not
additional inbound numbers. Middleware additionally registers `+16182265883`
as a Crystal River placeholder, explicitly marked TODO in its source. Neither
agent accepts that number and Product does not provision it. It is not an
end-to-end supported route; enabling or deleting it requires separate scope.

## Valid differences and authorization

Sweetwater Optical is an operational Location for calls/Tasks, while its
clinical configuration remains Sweetwater. Both agents use Product's optical
key for 7479. S2S Tasks preserve canonical middleware `officePhone` 7475 and
`inboundOfficePhone` for aliases. Agent also resolves its Task/handoff office
from the original trunk. Product Task and handoff authorization already use
service identity plus the Practice-scoped office route, independently of those
phone fields.

Spring Hill's 813 alias need not appear in middleware's phone registry: agents
resolve it to the canonical clinical office before middleware requests. Demo
numbers deliberately share sandbox clinical configuration, while their Product
history stays in the demo Practice. Agent selects separate demo credentials
and sandbox origin/token; named nonproduction deployments disable Product
interaction reporting and sandbox calls block staff Tasks. S2S uses the
production Practice credential and has no demo office registry.

Crystal River has staff Tasks disabled in S2S and transfers directly to its
configured external phone; this is distinct from Product handoff availability.
Agent's demo handoff policy also allows its configured phone fallback. The
presence of a Product route does not establish staff availability or a connected
transfer.

## Defect and smallest fix

Previously every receipt required an enabled staff VoiceNumber. An optional
office key then had to resolve to that same Location. With checked-in production
provisioning, that rejects five of six Sweetwater inbound numbers (including
Optical) and Spring Hill's 813 alias. Provisioning removes superseded staff
voice numbers, so manual alias inserts would not be durable.

When `officeKey` is supplied, ingestion now uses the same authenticated,
Practice-scoped office authorization as Tasks and handoffs. The required
`officePhone` remains normalized, validated, immutable call evidence. A staff
number belonging to another Location does not override the supplied authorized
office route. Product trusts the authenticated agent's attribution; the number
is not a second tenant or Location grant.

When `officeKey` is omitted, existing enabled/unambiguous voice-number
resolution is unchanged. Unknown offices never fall back to a known phone.
Missing capability, missing identity, unsupported scope and cross-Practice
routes remain denied. Existing source-call immutability and receipt projection
checks remain in place. No migration, alias registry, or telephony configuration
change is required.

## Verification and operational follow-up

The synthetic PostgreSQL regression failed before the fix with
`START: AI Interaction access denied` for inbound aliases and Locations without
staff voice numbers; the phone-only control passed. After the fix, the same
regression projects START, OUTCOME_CHECKPOINT and CLOSEOUT to one Interaction,
retains the dialed number, checks three durable receipts, and verifies Task and
handoff Location attribution. It also verifies denied ingestion creates no
receipt. Existing HTTP coverage retains phone-only compatibility and rejects an
unknown office even with a known voice number.

A read-only live Cloud SQL inventory attempt was blocked by expired gcloud
credentials (`Reauthentication failed`). Live provisioning, deployed application
behavior, carrier/LiveKit routing, and actual receipt delivery remain unverified.
The reported historical LiveKit failure has no captured HTTP rejection details;
this code defect is not proof of that call's exact failure cause.

After separately authorized release/deployment, verify live Practice office
routes and service identities, then use synthetic calls on the aliases to check
HTTP acceptance and durable Interaction/receipt Location and phone evidence.
Check Task and handoff attribution separately from staff answer/media proof.
Do not bulk replay historical receipts or infer successful delivery from this
source audit. This PR does not merge or deploy itself.

Local checks completed:

- `TEST_DATABASE_URL=postgres://127.0.0.1:55432/acuity_ingestion_test?sslmode=disable go test -p 1 ./backend/... ./deploy -count=1` — passed with a disposable PostgreSQL 16 database (local Go 1.27.1).
- `TEST_DATABASE_URL=postgres://127.0.0.1:55432/acuity_ingestion_test?sslmode=disable GOTOOLCHAIN=go1.26.7 go test ./backend/internal/interaction -run TestReceiptOfficeAuthorizationIndependentOfStaffVoice -count=1` — passed on CI's Go version.
- `go generate ./backend/internal/api` and `corepack pnpm@10.34.5 --dir web api:generate` — regenerated the documented contract.
- `corepack pnpm@10.34.5 --dir web typecheck` — passed.
- Source inventory assertions — passed for all 14 supported inbound numbers,
  Product Practice/Location mappings, and S2S canonical middleware numbers.

The full browser journey was not run locally: there is no browser behavior
change. CI status belongs to the PR and is not implied by these local checks.
