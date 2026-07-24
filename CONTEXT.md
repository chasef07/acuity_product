# Acuity Portal

Acuity Portal is the operating workspace where medical-practice teams turn
patient communications into accountable work while preserving the evidence
around that work.

## Organization and access

**Practice**:
A customer tenant and security boundary containing one or more Locations.
_Avoid_: Account, organization, clinic

**Location**:
A physical or operational office within one Practice.
_Avoid_: Site, branch

**User**:
A human identity that may hold Practice Memberships or the Acuity-wide Platform
Operator role.
_Avoid_: Login, account

**Membership**:
The relationship granting one User an Admin or Staff role in one Practice,
together with a Location Scope.
_Avoid_: Seat, permission set

**Location Scope**:
The set of Locations a Membership may access. `All` includes every current and
future Location in the Practice; `Selected` includes only explicitly granted
Locations.
_Avoid_: Location role, office permission

**Admin**:
A Practice role with access to every current and future Location in that
Practice.
_Avoid_: Platform admin, super admin

**Staff**:
A Practice role whose Location Scope may be `All` or `Selected`.
_Avoid_: Agent, member

**Platform Operator**:
An internal Acuity Health role with visibility across every current and future
Practice and Location.
_Avoid_: Super admin, Practice Admin

**Support Mode**:
A time-limited, Practice-scoped, audited state that permits a Platform Operator
to change customer data without impersonating a customer User.
_Avoid_: Impersonation, admin mode

**Invitation**:
An email-bound, expiring, revocable offer of a Practice role and Location Scope
that becomes a Membership only after acceptance.
_Avoid_: User creation, credential

## Work and communication

**Task**:
One accountable piece of patient work.
_Avoid_: Request, ticket, case

**Interaction**:
A call, voicemail, SMS message, or staff note that may exist with or without a
Task.
_Avoid_: Event, touch

**Contact Context**:
A task- or interaction-specific snapshot of the phone number, optional name,
handoff details, and provenance known at that time; it is not verified patient
identity.
_Avoid_: Patient profile, contact record

**Engagement History**:
Practice- and Location-scoped calls and messages found by normalized phone
number and displayed as context without being attached to the current Task.
_Avoid_: Patient history, medical record

**Activity**:
A chronological Task entry such as an Interaction, assignment, status change,
or priority change.
_Avoid_: Audit event

**Queue**:
A query over Tasks, never a second source of workflow state.
_Avoid_: Inbox state
