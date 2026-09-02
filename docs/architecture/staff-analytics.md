# Staff analytics

Practice Admins open Analytics → Staff beside Bookings, Conversion, and Duration.
The existing office and 7/30/90-day filters apply. Workspace owns the authorized
cross-domain read projection at `POST /v1/analytics/staff/query`; Access,
HumanCalling, and Work remain the owners of their durable state.

## Metrics

- **Inbound/outbound calls:** completed Calls with a provider-confirmed bridge
  on that person's Staff CallLeg, grouped by the Call's direction. Count each
  Call once per person even when several of their legs connected. Unanswered
  attempts and ringing time are excluded. A staff-to-staff transfer credits
  both people for the Call each handled.
- **Inbound/outbound phone time:** sum each person's connected leg time from
  `bridged_at` through `ended_at`. Missing or reversed timing remains unavailable
  for that direction in the UI, rather than presenting a partial sum as complete.
  Missing inbound timing does not hide valid outbound time, or vice versa. Call activity is
  selected by bridge time, within complete reporting days.
- **Tasks completed per account:** currently completed Tasks whose completion
  falls in the reporting period, credited to `completed_by_subject` only when
  `completed_by_kind=HUMAN`. Assignment does not determine credit.
- **Time to task completion:** median elapsed time from Task creation to its
  current completion, grouped in the chart by completion day. Automated
  completions contribute to the overall Task outcome, not a staff account.
- **Within 48 hours:** Tasks created in the selected period whose 48 elapsed
  hours have passed, with a current completed outcome at or before their
  deadline, divided by all such Tasks. Incomplete overdue Tasks remain in the
  denominator; younger Tasks wait until their deadline, even if completed early.
  Null means no eligible Tasks. Nights and weekends count.

Viewing, assignment, and reopening do not reset the creation clock. Reopened
Tasks are unfinished and receive no completion credit until completed again;
this is current durable outcome reporting, not a count of historical clicks.

## Accounts and boundaries

All Practice Memberships remain visible, including inactive accounts and
zero-activity accounts, plus unclaimed active Access Grants. The office filter
changes activity, not the Practice account directory. Unknown historical actors
are grouped as Other accounts; auth subjects are not returned. The response
contains staff account emails and aggregates, never patient contact details,
transcripts, Task titles, or provider payloads.

Admin/Operator authorization and current Location access are resolved in the
query transaction. Staff and cross-Practice access are denied. Queries reject
more than 5,000 accounts or 50,000 call legs/Tasks rather than returning a partial
report. Both reports share one non-queuing analytics permit per portal instance, a
two-second request deadline, 1.5-second statements, and 100ms lock waits.
SQL parallel workers are disabled for these requests; pool limits remain
unchanged. Excess analytics requests return 429 with Retry-After; normal
portal requests do not use this permit. The browser
aborts and fences obsolete requests on office, period, or analytics-tab changes.

Staff headers sort locally by name, calls, phone time, or completed Tasks.
Numeric columns start descending and toggle direction; unavailable time stays
last, and the Total row stays below all accounts. Sorting issues no API request.
