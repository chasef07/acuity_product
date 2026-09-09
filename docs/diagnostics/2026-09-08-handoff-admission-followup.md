# Post-release handoff admission and provider failures

Production cohort: September 8, 2026, 20:00–September 9, 00:10 UTC.
Inspection used forced read-only PostgreSQL sessions through Cloud SQL Auth Proxy,
bounded Cloud Logging queries, and authenticated Telnyx GET requests. Outputs
contain aggregate state and fixed error categories, with no patient data.

## Handoff admission: confirmed application defect

The 41 initial `HANDOFF_REJECTED` receipts split into two different cases:

- 33 came from the Product WebRTC credential connection. All 33 provider sessions
  matched existing Portal Staff CallLegs; 21 also matched Destination CallLegs.
  These staff-side events are not 33 failed patient transfers. They must not be
  admitted as new inbound patient Calls.
- Eight came from the Product Call Control application with `to` equal to the
  exact configured bare `acuity-handoff@<domain>` address. Seven matched seven
  distinct, valid Handoff reservations and source calls. None of those seven
  reservations was consumed. The eighth had no reservation valid at event time.

`resolveHandoffForRefer` required both addresses to be E.164 phone numbers.
`CreateHandoff` returns a SIP destination, and Telnyx retained that address in the
webhook, without the `sip:` prefix. The destination check rejected the seven
valid transfers before reservation correlation could run.

Issue #51 still describes an earlier token-routing design. Merged PR #58
explicitly superseded that handoff transport with the stable SIP destination and
one pending reservation correlated by caller phone. This repair follows the
current implementation and PR #58; it introduces no new correlation policy.

The fix accepts the configured SIP destination with or without its scheme,
alongside the existing E.164 destination representation. It still requires an
E.164 caller, the expected provider connection, and exactly one valid unconsumed
reservation. Other SIP users/domains and extra URI syntax remain rejected.

### Reproduction and verification

Two existing integration scenarios were corrected to use actual SIP shapes:

- Full inbound fan-out uses the destination returned by `CreateHandoff`.
- Signed receipt processing uses the observed bare SIP address and exercises a
  child receipt arriving before its initiation receipt.

Before the implementation change, these failed with `invalid handoff` and
`related child was not woken when parent attached the Call`. Both pass after the
change. Negative destination cases verify the reservation is not consumed by a
wrong domain/user, leading whitespace, URI parameters, or an empty address.
The existing suite retains E.164 destination, ambiguous-reservation and closed
admission coverage.

The seven expired historical attempts are not repaired by a deployment. Their
source interactions need operator follow-up. No receipt was replayed, no ended
call was recreated, and no production database state was changed.

## Outbound rejection: country policy, not a broken API credential

One `DIAL_OUTBOUND_DESTINATION` command failed at 20:52:13 UTC. Its stored
`TELNYX_AUTH_REJECTED` diagnosis collapsed all HTTP 401/403 responses.
Telnyx's call-event API returned provider code `10010`, with a specific
allowed-country rejection. Offline phone metadata identified the destination as
a valid-pattern U.S. Virgin Islands (`VI`) number in the shared `+1` numbering
plan. Being `+1` alone does not establish that the destination is in `US`.

The Call ended as `UNANSWERED`, its Destination CallLeg failed, and its Staff
CallLeg was hung up and reconciled. A later retry to the same destination ended
without creating a destination-dial command. There is no active command to
replay.

The adapter now classifies code `10010` as
`TELNYX_DESTINATION_COUNTRY_REJECTED`, retaining its definitive-failure behavior.
A synthetic HTTP 403/10010 regression failed with the old authentication label
and passes with the specific label. This change does not expand country access,
retry the rejected effect, or rewrite historical error evidence. Any policy
expansion requires a separate decision about the allowed destination countries.
Live readback confirmed the Product profile allows only `US`; permitting this
destination would add `VI`. The profile also retains a 0.20 maximum destination
rate and 10.00 daily spend limit. Country-policy expansion is outside this fix; no provider setting has been
changed.

## Six transport warnings: recovered, exact cause not retained

Six `TELNYX_TRANSPORT` reconciliation warnings occurred from 20:08:42 through
20:17:05 UTC. Three ended CallLegs retain that diagnostic. The complete current
worker candidate predicate returned zero due eligible candidates; retained
error text is not a backlog. A live read-only active-call request succeeded in
approximately 0.28 seconds and returned zero active calls. The follow-up warning
scan found no new warnings after 00:10 UTC.

The historical diagnostic does not distinguish a socket failure from a timeout,
so the initiating transport cause remains unproven. No retry or timeout policy
was changed. The existing provider-deadline/backoff integration regression passed.

## Validation and release boundary

Using Go 1.26.7 and a disposable local PostgreSQL 16 instance on port 55452:

```sh
GOTOOLCHAIN=go1.26.7 \
TEST_DATABASE_URL=postgres://postgres@127.0.0.1:55452/acuity_handoff_fix_test?sslmode=disable \
  go test -p 1 ./backend/... ./deploy -count=1

GOTOOLCHAIN=go1.26.7 \
E2E_DATABASE_URL=postgres://postgres@127.0.0.1:55452/acuity_handoff_fix_e2e?sslmode=disable \
  ./scripts/run-e2e.sh e2e/human-calling.spec.ts

GOTOOLCHAIN=go1.26.7 \
  go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./backend/...

git diff --check
```

The full backend/deploy suite passed. All three calling browser journeys passed
(1.1 minutes), including fan-out/bridge and voicemail/recovery. Vulnerability
checking found no vulnerabilities; whitespace validation passed.

These are local application and browser results, with live provider/database
read evidence for diagnosis. No deployment, production replay or provider
configuration update occurred. Deployed signed-webhook admission,
real transfer completion and historical patient follow-up remain unverified.

References: [merged PR #58](https://github.com/chasef07/acuity_product/pull/58),
[Telnyx call-event API](https://developers.telnyx.com/api-reference/debugging/list-call-events).
