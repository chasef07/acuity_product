# Audited backend backlog recovery

Use `backend/cmd/backlog-recovery` only for an explicitly authorized incident.
It defaults to a read-only candidate count. `--apply` handles one selected item
per transaction, verifies its committed state and audit, then advances to the
next item. It stops on the first conflict or failed verification. It sends no
SMS, call, or other provider command.

Use a separately authorized recovery database connection through a local Cloud
SQL Auth Proxy. The connection must support protected evidence reads and
operator audit writes; the ordinary portal and worker roles deliberately do
not each have both permissions. Do not widen their grants. Set the authenticated
operator's email in `RECOVERY_OPERATOR_EMAIL`; the tool requires that email to
already have a bound Platform Operator identity. Never print database URLs,
provider keys, or raw receipt bodies.

Every invocation requires an explicit exclusive creation cutoff and a limit
between 1 and 200. Start with one item. Do not bulk reset or requeue a quarantine.

```sh
go run ./backend/cmd/backlog-recovery \
  --group acknowledgements --before 2026-08-25T00:00:00Z --limit 1

# Apply only after the candidate's evidence and disposition have been reviewed.
go run ./backend/cmd/backlog-recovery \
  --group acknowledgements --before 2026-08-25T00:00:00Z --limit 1 --apply
```

## Dispositions

| Group | Required evidence | Result |
| --- | --- | --- |
| `acknowledgements` | Unsent `PENDING` acknowledgement, missing sender configuration, more than seven days old, within the incident cutoff | `NOT_NEEDED` with `HISTORICAL_ACKNOWLEDGEMENT_SUPPRESSED`; the patient Task is unchanged |
| `ai-clock` | Supported quarantined lifecycle receipt; same source, service, Location, caller and office; only `startedAt` differs | Apply normal lifecycle/outcome rules using the existing canonical start time; preserve original payload and fingerprint |
| `ai-legacy` | Quarantined `SUMMARY` and a terminal source Interaction established by a projected supported `CLOSEOUT` | `RETIRED`, linked to that Interaction, preserving original payload, fingerprint and error |
| `calling` | One quarantined terminal ringtone receipt on a terminal Call; exact provider leg/session plus bridge and hangup command evidence; no active legs, commands or receipts | Atomically apply the fact and receipt, preserving attempts and raw evidence; no Call outcome change |

Calling recovery requires `RECOVERY_HANDOFF_KEY` to be the existing configured
recovery-reference key. The tool obtains the reference through the authorized
operator timeline; it does not use a raw event ID as the recovery command.

Historical ringtone recovery accepts only `call_hangup` playback endings with
`cleanup` or `staff_hangup` state. A normal `outbound_media` callback remains
valid during ingestion, but its bridge evidence alone cannot authorize this
historical repair. The Call must have both a terminal outcome and an end time.
`SENT` commands are still awaiting provider confirmation and block recovery,
along with `PENDING`, `SENDING`, and `AMBIGUOUS` commands. Any pending, processing,
or other quarantined receipt on that Call also blocks the transaction. Let
unresolved provider work reconcile before retrying; do not mark it successful
just to pass this gate. The CLI rechecks committed state and the matching audit.

Legacy retirement requires forward migration
`0045_retired_legacy_interaction_receipts.sql`. The `schema` group can dry-run or
apply exactly through that migration, requiring the existing `0044` schema.
The new state is limited to `SUMMARY`, which every current runtime already
rejects as an ingestion message and excludes from worker selection. This keeps
the migration compatible with the prior runtime. It changes no receipt rows.

An audit records each disposition under the real Platform Operator identity:
`task_acknowledgement.retired`, `ai_interaction.source_clock_recovered`,
`ai_interaction.legacy_receipt_retired`, or `provider_receipt.recovered`.

Retirement is not successful projection or successful patient work. Missing
closeouts without recoverable evidence and calls with no recording remain
separate evidence gaps. Never invent their outcomes to make a count zero.

## Preventing repeated acknowledgement backlog

The worker retries missing or inactive sender configuration once per minute for
at most five minutes from the acknowledgement intent. It then records a durable
not-sent disposition and removes the retry time. The last configuration failure
remains visible; an acknowledgement delayed before its first attempt records
`ACKNOWLEDGEMENT_EXPIRED`. The Task remains open. A queued acknowledgement is
also checked against the original deadline before any provider write begins.
Restoring configuration later does not send expired acknowledgements.

To send future acknowledgements, provision an active sender and messaging profile
for the Location using the normal Messaging provisioning path, after confirming
the provider number is enabled for that profile. No sender is inferred from its
voice configuration. The deadline prevents stale sends and indefinite retries;
it does not establish messaging readiness or successful delivery.

This change adds no alerts or Agent behavior. Unexpected provider or AI evidence
conflicts continue to quarantine with the original evidence for explicit review.
Transient pending work can exist while workers process it; healthy convergence
means it drains, rather than being silently deleted or marked successful.
