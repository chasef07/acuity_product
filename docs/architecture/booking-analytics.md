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
- **Booking conversion:** completed calls with a recorded `get_availability`
  invocation and confirmed booking, divided by completed calls with a recorded
  invocation. A call counts once regardless of repeat searches. Failed and
  empty searches remain in the denominator. This replaces ACU-33's earlier
  eligible-slot denominator following the September 2 product discussion.
- **Booked-call duration:** call start to call end, across calls with confirmed
  bookings and valid timing. This is the only duration metric; appointment
  confirmation does not stop the clock. Missing or out-of-order timestamps
  are excluded from duration samples, not represented as zero.
- **p50/p90:** linear interpolation over all valid call durations in the
  selected cohort. Period percentiles and rates are calculated from pooled
  observations, never averaged from daily percentiles or percentages.

The conversion headline is explained as “80% of calls with a recorded
availability check booked.” The underlying unit is one call, not
unique people across repeat calls. Counts remain in the explanation and hover
details; there is no “Availability calls” headline or table column.

## Evidence and reporting boundaries

New versus existing uses successful structured `patient_new` and
`patient_verified` domain outcomes. A new-patient outcome takes precedence over
later verification. Patient switching is Unknown, and chart creation alone
does not establish either cohort. Missing historical classification remains
unclassified in stored evidence and remains included in totals. The UI has no
Unknown row. When classification is incomplete, a dashed All patients trend
includes every call, and a compact coverage note explains the new/existing
breakdown. Daily total percentiles are pooled from source durations.

Availability use is read from native function-call records or historical
structured tool-execution records. Missing tool history is not a negative
availability observation. The API returns coverage counts with the aggregates. Incomplete history is
shown beside the conversion metric and in daily hover details.

Reports include the previous 7, 30, or 90 complete calendar days, grouped by
call start. The reporting timezone is an explicit validated IANA request field
used for calendar grouping. Until Practice has a reporting-timezone setting,
the client uses the viewer's timezone; it does not silently claim a configured Practice
timezone. Calendar boundaries use timezone-aware dates, including DST.

A database trigger maintains four small booking facts when source evidence changes.
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
