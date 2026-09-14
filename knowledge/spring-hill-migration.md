# Spring Hill corpus migration

Source: the active Google Cloud database export, revision `2ceface2-6675-40a8-ac8c-86d22f57c610`.

The 15 source sections became 35 focused entries. Existing factual sentences, dates, prices, names, provider restrictions, age restrictions, and explicit missing-information statements are retained. The repeated cataract provider restriction is stored once. All offices remove the redundant `Status:` prefixes. The actual missing-information statements remain unchanged. Medical Drive closure is the only newly supplied fact, authorized by the user.

The seven other office files preserve their exported section IDs and titles unchanged. Their text preserves every factual sentence and restriction, removing only the leading status label and its following blank line. Rheumatology demo retains its existing scope entry (3,776 characters after removing its status prefix); it requires a separate reviewed content restructuring.

The after-hours entry title is clarified to `After-hours doctor contact` so ordinary office-hours questions are less likely to retrieve the doctor contact number. Its source text is unchanged.

## Source mapping

| Previous section | Published entry IDs |
| --- | --- |
| `after-hours` | `after-hours` |
| `billing` | `billing` |
| `contact-lenses` | `contact-lenses` |
| `payments` | `payments` |
| `repairs-and-warranty` | `repairs-and-warranty` |
| `self-pay-pricing` | `self-pay-pricing` |
| `social-follow-up` | `social-follow-up` |
| `what-to-bring` | `what-to-bring` |
| `appointment-expectations` | `appointment-confirmation`, `new-patient-visit` |
| `hours` | `hours`, `labor-day-2026` |
| `insurance-and-referrals` | `retinal-photo-coverage`, `referral-requirements` |
| `optical-and-glasses` | `frame-brands`, `glasses-turnaround`, `eyeglass-prescription-validity`, `outside-prescriptions`, `sunglasses`, `optician-hours`, `optical-walk-ins` |
| `providers` | `cataract-provider`, `dr-bach`, `dr-noel`, `dr-licht`, `additional-providers` |
| `location-and-contact` | `current-address`, `paperwork-email`, `fax`, `crystal-river-location`, `lutz-move-history` |
| `scope-of-services` | `cataract-provider`, `medical-services`, `routine-vision-and-children`, `retina-care` |
| `new-user-request` | `medical-drive-closure` |

## Evaluation

`evals/spring-hill.json` contains synthetic questions with expected entry IDs and response budgets. These are acceptance cases, not evidence that live retrieval has passed. Evaluate against the active published revision and record outcomes separately. Preserving the September 7, 2026 holiday statement preserves the live source; it must remain explicitly dated and must not be presented as a current closure.
