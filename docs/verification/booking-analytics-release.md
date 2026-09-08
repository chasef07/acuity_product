# Booking analytics release

The feature keeps conversion and durations per call. Phone numbers are not used
for deduplication. Bookings retain the existing distinct-appointment definition.

## Schema and historical data

Ship `0063_booking_patient_basis.sql` through the normal release migration job.
It classifies stored successful patient outcomes, including legacy
`toolExecutions.outputClass`, and adds a separate nullable field for historical
phone-match assumptions. It performs no external log queries. New agent closeouts
supply `phoneLookup.status`; release the companion agent change for future calls.

Historical middleware logs are external to the database. Keep their reviewed
export private rather than checking patient-linked identifiers or logs into a PR.
The one-time data migration is `scripts/backfill-booking-phone-lookups.sql`.
Supply a CSV with `interaction_id,status` and only the accepted single/multiple
phone matches (`verified` or `multiple_matches`). The current historical matching
rule uses exactly one nearby request within five seconds of call start. Missing,
ambiguous, and no-match results need no backfill because their default is new.
This is a reporting assumption, not identity verification.

At release, run against the intended Practice after schema migration:

```sh
psql "$DATABASE_URL" -X -v ON_ERROR_STOP=1 \
  -v practice_id="$PRACTICE_ID" -v apply=false \
  -f scripts/backfill-booking-phone-lookups.sql < "$PRIVATE_EVIDENCE_CSV"
```

The default dry run rolls back. Compare its aggregate changes with the reviewed
export. Only when execution is authorized, repeat with `-v apply=true`. The data
migration rejects missing/cross-Practice/incomplete calls, duplicate identifiers,
invalid statuses, and conflicting prior assumptions. It preserves native lookup
results, provider closeout payloads, booking/search facts, and durations. Rerunning
the same evidence is a no-op.

Verify local and production against the same captured window, office scope, and
timezone. Compare each patient row, total bookings, searched and converted calls,
and pooled P50. The local preview must use this same schema and backfill procedure;
enriching only the local closeout payload does not prove release parity.

No production migration or backfill has been executed during development.
