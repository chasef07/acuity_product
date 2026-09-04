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
- **Booking conversion:** confirmed bookings divided by completed calls with a
  recorded `get_availability` invocation. A call counts once regardless of
  repeat invocations. Empty, blocked, failed, reschedule, and cancellation
  invocations remain in the denominator by product definition.
- **Booked-call duration:** call start to call end, across calls with confirmed
  bookings and valid timing. This is the only duration metric; appointment
  confirmation does not stop the clock. Missing or out-of-order timestamps
  are excluded from duration samples, not represented as zero.
- **p50/p90:** linear interpolation over all valid call durations in the
  selected cohort. Period percentiles and rates are calculated from pooled
  observations, never averaged from daily percentiles or percentages.

The conversion headline names the overall rate and its exact counts, for
example “496 of 1,024 calls booked after an availability-check attempt.” Booked and
did-not-book counts partition the same denominator. The underlying unit is
one call, not unique people across repeat calls. The Conversion table shows
booked calls, calls with availability attempts, and their conversion rate; it uses
converted calls rather than distinct appointment counts and omits duration
columns. This replaces the September 2 presentation rule that hid the
denominator in the table.

## Evidence and reporting boundaries

New versus existing first uses the explicit new/established appointment type
on the confirmed booking receipt matched to `new_appointment_id`. This is the
appointment actually booked, so it takes precedence over call-wide patient
switches, unrelated registration attempts, or missing domain outcomes.

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
call-wide evidence ambiguous. Calls without bookings therefore still require
explicit patient evidence for the conversion breakdown.

Missing classification remains included in totals. Conversion shows an
Unclassified row whenever such calls have availability attempts, so the displayed
rows add up to the headline denominator. Its coverage note uses the same
availability-check cohort, not all completed calls. Since a booking receipt
can supply patient status, the note explains that incomplete new/existing
rates can be higher than overall conversion. Absence of a new-patient tool
call does not establish existing-patient status.

Conversion uses one solid overall daily line, with straight segments and
visible observations. Tooltips show each day's booked-call numerator and
availability-attempt denominator. Days without attempts have no rate; a day
with attempts and no bookings has a real zero. New/existing rates remain in the
table rather than competing with the overall trend.

Bookings and Duration retain their new/existing breakdown and all-call
coverage note. When classification is incomplete, their All patients trend
includes every call. Daily total percentiles are pooled from source durations.

Availability use is read from native function-call records or historical
structured tool-execution records. Missing tool history is not a negative
availability observation. Incomplete history is shown beside the conversion
metric and in daily hover details.

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

The duration view uses one chart with new and existing patient p50 lines.
Sparse conversion and duration observations remain visible as small dots.
P90 remains in the Breakdown table. The page ends at the Total row.
