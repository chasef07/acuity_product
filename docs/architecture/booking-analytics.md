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
- **Booked-call duration:** call start to call end, across calls with confirmed
  bookings and valid timing. This is the only duration metric; appointment
  confirmation does not stop the clock. Missing or out-of-order timestamps
  are excluded from duration samples, not represented as zero.
- **p50/p90:** linear interpolation over all valid call durations in the
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

For a completed availability search, patient status follows one explicit rule:
a preceding `add_patient` tool call means new; every other completed search
means existing. This makes the two patient rows exhaustive for conversion and
their denominators add up to the overall denominator.

When a call has no completed availability search, new versus existing uses the
explicit new/established appointment type on the confirmed booking receipt
matched to `new_appointment_id`, then explicit patient receipts. This preserves
useful booking and duration classification outside the conversion cohort.

Recognized receipt labels are New Adult/Pediatric Medical, New Adult/Pediatric
Vision, Crystal River New Patient, their Established equivalents (Medical
includes “Follow Up”), and Crystal River Established Patient. These labels
come from the middleware appointment catalog; numeric EHR type IDs alone are
not interpreted globally across Practices. **Post Op and Crystal River Post Op
count as existing patients**, per the September 2 product decision, even when
that call also created a chart. Unrecognized labels do not imply a patient category.

When no typed receipt is available, explicit successful `patient_new` or
`patient_created` establishes new; `patient_verified` establishes existing.
Superseded outcomes are ignored. Creation takes precedence over later
verification of that newly created patient. A patient switch leaves unbound
call-wide evidence ambiguous. Calls without completed searches therefore still
require explicit patient evidence for the Bookings and Duration breakdowns.

Conversion has New, Existing, and Total rows. It never shows Unclassified:
absence of a preceding `add_patient` call is the Product rule for existing
within this completed-search cohort. The overall conversion rate is therefore
the weighted result of the New and Existing rows.

Bookings, Conversion, and Duration share the same chart treatment: monotone
total, new-patient, and existing-patient lines with subtle area fills. The total
line is dashed. Isolated observations retain a dot so missing adjacent days do not hide data. Tooltips show each series' numerator and denominator where
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
lines. P90 remains in the Breakdown table. Staff task duration uses the same
monotone line and subtle area treatment. The Staff table initially sorts by
inbound time descending, with missing durations last. The page ends at the
Total row.
