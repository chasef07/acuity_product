# Practice booking analytics

Practice Admins open **Analytics** from the existing Acuity Portal workspace.
The page is **Confirmed bookings**, with Bookings, Conversion, and Duration
views. Platform Operators can also use this scoped customer view; their
existing technical evidence remains under **AI diagnostics** and its original
operator-only API.

`POST /v1/analytics/bookings/query` is owned by AIInteraction. Access resolves
the authenticated actor's current Practice and Location authorization inside
the transaction. Staff and Admins from other Practices are denied. Responses
contain aggregates only, without phone numbers, patient identifiers,
appointment identifiers, transcript content, or provider payloads.

## Metric definitions

- **Confirmed bookings:** distinct appointment identifiers backed by a Product
  `BOOKING` outcome and a successful `booked` result. The earliest matching
  call in the selected cohort owns a repeated identifier's patient group and
  day. A reschedule, action label, or unconfirmed result is not a booking.
- **Booking conversion:** booked calls divided by completed calls with a
  `get_availability` execution that has a successful tool output, excluding
  calls whose completed Product outcome is `RESCHEDULE` or `CANCELLATION`. A
  call counts once regardless of repeat executions. A completed search with no
  openings remains in the denominator; failed and incomplete executions do not.
- **Booking-attempt duration:** call start to call end, across calls with a
  confirmed booking or completed availability search and valid timing. This is
  the only duration metric; appointment confirmation does not stop the clock. Missing or out-of-order timestamps
  are excluded from duration samples, not represented as zero.
- **p50:** linear interpolation over all valid booking-attempt durations in the
  selected cohort. Period percentiles and rates are calculated from pooled
  observations, never averaged from daily percentiles or percentages.

The conversion headline names the overall rate and its exact counts, for
example “4 of 6 calls booked after a completed availability search.” The
underlying unit is one call, not unique people across repeat calls. The
Conversion table shows
booked calls, completed availability searches, and their conversion rate; it uses
converted calls rather than distinct appointment counts and omits duration
columns. This replaces the September 2 presentation rule that hid the
denominator in the table.

## Evidence and reporting boundaries

All metrics use the same per-call patient category. Successful, non-superseded
patient creation/new results establish new; verification or switching establishes
existing. A successful switch discards earlier patient outcomes. Legacy closeouts
without `domainOutcomes` use outcome-specific `toolExecutions.outputClass` values;
a successful tool transport alone does not verify a patient.

Without a conclusive identity result, a successful patient-not-found result assumes
new. Otherwise a saved phone lookup with one or multiple matches assumes existing.
Explicit no-match or failed lookup status follows the current assume-new fallback.
These reporting assumptions do not establish verified identity.

When both native phone-lookup status and historical backfill are absent, retain a
previous existing-patient category as `legacy_existing`. This is a compatibility
rule for incomplete historical telemetry, not a verified patient result. The
previous projection classified a completed search without an earlier `add_patient`
as existing; outside completed searches it recognized a matching established or
post-op booking receipt. Stronger patient or phone evidence overrides this fallback.
Calls without either stronger evidence or a previous existing category assume new.
Migration 0064 corrects already-stored assumptions and keeps source corrections in
sync. Historical phone matches can still be supplied through the explicit backfill.

Conversion has New, Existing, and Total rows. Their denominators add up to the
overall denominator, and the overall conversion rate is weighted by those counts.
The deprecated Unknown API group remains empty with the current classification.

Bookings, Conversion, and Duration share the same chart treatment: monotone
total, new-patient, and existing-patient lines with subtle area fills. The total
line is dashed. Days without a measurement plot at zero with connected lines and
fills. Tooltips distinguish no activity from unavailable durations; summary
metrics retain their original null values. Tooltips show each series' numerator
and denominator where
relevant. Days without completed searches have no conversion rate; a day with
completed searches and no bookings has a real zero.

Bookings and Duration retain their new/existing breakdown and all-call
coverage note. When classification is incomplete, their All patients trend
includes every call. Daily total percentiles are pooled from source durations.

Availability use is read from native function-call records or historical
structured tool-execution records. A native execution is complete when a
matching function-call output records `is_error: false`; historical structured
evidence uses `status: success`. Product does not parse caller-facing result
text. Missing tool history is not a negative availability observation.
Incomplete history is shown beside the conversion metric and in daily hover
details.

Reports include the previous 7, 30, or 90 complete calendar days, grouped by
call start. The reporting timezone is an explicit validated IANA request field
used for calendar grouping. Until Practice has a reporting-timezone setting,
the client uses the viewer's timezone; it does not silently claim a configured Practice
timezone. Calendar boundaries use timezone-aware dates, including DST.

A database trigger maintains four reporting facts when source evidence changes.
The retired `booking_search_precise` column remains for append-only migration
compatibility. Current terminal projections set it false, and Product does not
read it. The deprecated API compatibility field remains zero during a rolling
deployment and is hidden by the current frontend.
A resumable migration backfills existing completed calls in batches of 500.
The bounded report reads a covering index without parsing transcript JSON and rejects
windows exceeding 50,000 completed calls. Both admin analytics endpoints share one non-queuing permit per portal instance,
a two-second request budget, a 1.5-second statement timeout, and a 100ms lock
timeout. Queries use one worker and 4MB sort memory. The browser does not poll
or raise database-pool limits. Errors are
visible with Retry. Obsolete client requests are aborted and their results
cannot populate another query's state.

The real workspace calls the API and never substitutes preview data. The
standalone design preview and its duplicate aggregation implementation were
removed; browser journeys use synthetic records in a disposable database.

The duration view uses one chart with total, new, and existing patient p50
lines. The Breakdown table also shows P50; P90 is retained only in the API for
compatibility. Staff task duration uses the same monotone line and subtle area treatment. The Staff table initially sorts by
inbound time descending, with missing durations last. The page ends at the
Total row.
