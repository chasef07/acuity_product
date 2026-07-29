\set ON_ERROR_STOP on

REVOKE ALL ON SCHEMA public FROM PUBLIC;
REVOKE ALL ON SCHEMA auth FROM PUBLIC;

GRANT USAGE ON SCHEMA public
TO acuity_portal, acuity_provider, acuity_realtime, acuity_worker;
GRANT USAGE ON SCHEMA auth TO acuity_auth;

-- Remove the former broad authority before applying the query-owned grants.
REVOKE ALL ON ALL TABLES IN SCHEMA public
FROM acuity_portal, acuity_provider, acuity_realtime, acuity_worker;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA public
FROM acuity_portal, acuity_provider, acuity_realtime, acuity_worker;
REVOKE ALL ON ALL TABLES IN SCHEMA auth FROM acuity_auth;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA auth FROM acuity_auth;

-- portal-api: Access, HumanCalling command/read paths, and Work.
GRANT SELECT ON TABLE
    public.access_abita_office_locations,
    public.access_audit_events,
    public.access_invitation_locations,
    public.access_invitations,
    public.access_locations,
    public.access_membership_locations,
    public.access_memberships,
    public.access_operational_users,
    public.access_platform_operators,
    public.access_practices,
    public.access_support_sessions,
    public.human_calling_calls,
    public.human_calling_connection_attempts,
    public.human_calling_credentials,
    public.human_calling_handoffs,
    public.human_calling_provider_commands,
    public.human_calling_recordings,
    public.human_calling_softphone_leases,
    public.human_calling_timeline,
    public.work_task_activities,
    public.work_tasks
TO acuity_portal;

GRANT SELECT (
    event_id,
    event_type,
    occurred_at,
    received_at,
    call_id,
    state,
    projection_attempts,
    projection_error_code,
    duplicate_count
)
ON TABLE public.human_calling_provider_receipts
TO acuity_portal;

GRANT UPDATE (
    state,
    projection_attempts,
    projection_error_code,
    processing_started_at,
    last_attempt_at,
    next_attempt_at,
    projected_at,
    quarantined_at
)
ON TABLE public.human_calling_provider_receipts
TO acuity_portal;

GRANT INSERT ON TABLE
    public.access_audit_events,
    public.access_locations,
    public.access_membership_locations,
    public.access_memberships,
    public.access_support_sessions,
    public.human_calling_connection_attempts,
    public.human_calling_credentials,
    public.human_calling_handoffs,
    public.human_calling_provider_commands,
    public.human_calling_softphone_leases,
    public.human_calling_timeline,
    public.work_task_activities,
    public.work_tasks
TO acuity_portal;

GRANT UPDATE ON TABLE
    public.access_invitations,
    public.access_locations,
    public.access_memberships,
    public.access_practices,
    public.access_support_sessions,
    public.human_calling_calls,
    public.human_calling_connection_attempts,
    public.human_calling_credentials,
    public.human_calling_handoffs,
    public.human_calling_provider_commands,
    public.human_calling_softphone_leases,
    public.work_tasks
TO acuity_portal;

GRANT UPDATE (user_subject)
ON TABLE public.access_platform_operators
TO acuity_portal;

-- provider-ingress: the signed receipt transaction and nothing else.
GRANT INSERT (
    event_id,
    event_type,
    occurred_at,
    received_at,
    signature_timestamp,
    raw_body,
    next_attempt_at
)
ON TABLE public.human_calling_provider_receipts
TO acuity_provider;

GRANT SELECT (
    event_id,
    event_type,
    raw_body,
    state,
    duplicate_count
)
ON TABLE public.human_calling_provider_receipts
TO acuity_provider;

GRANT UPDATE (duplicate_count)
ON TABLE public.human_calling_provider_receipts
TO acuity_provider;

-- realtime: authorization reads plus first-login Platform Operator binding.
GRANT SELECT ON TABLE
    public.access_locations,
    public.access_membership_locations,
    public.access_memberships,
    public.access_platform_operators,
    public.access_practices,
    public.access_support_sessions
TO acuity_realtime;

GRANT UPDATE (user_subject)
ON TABLE public.access_platform_operators
TO acuity_realtime;

-- worker: durable receipt projection, command execution, and reconciliation.
GRANT SELECT ON TABLE
    public.access_operational_users,
    public.access_practices,
    public.human_calling_calls,
    public.human_calling_connection_attempts,
    public.human_calling_credentials,
    public.human_calling_handoffs,
    public.human_calling_provider_commands,
    public.human_calling_provider_receipts,
    public.human_calling_recordings,
    public.human_calling_softphone_leases
TO acuity_worker;

GRANT INSERT ON TABLE
    public.human_calling_calls,
    public.human_calling_credentials,
    public.human_calling_projected_facts,
    public.human_calling_provider_commands,
    public.human_calling_recordings,
    public.human_calling_timeline
TO acuity_worker;

GRANT UPDATE ON TABLE
    public.access_practices,
    public.human_calling_calls,
    public.human_calling_connection_attempts,
    public.human_calling_credentials,
    public.human_calling_handoffs,
    public.human_calling_provider_commands,
    public.human_calling_provider_receipts,
    public.human_calling_recordings,
    public.human_calling_softphone_leases
TO acuity_worker;

-- Better Auth owns only its schema.
GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE
    auth."user",
    auth."session",
    auth."account",
    auth."verification",
    auth."jwks"
TO acuity_auth;

-- New relations are denied by default. Every migration must update this
-- contract with the owning runtime's exact authority.
ALTER DEFAULT PRIVILEGES FOR ROLE acuity_migrate IN SCHEMA public
REVOKE ALL ON TABLES
FROM acuity_portal, acuity_provider, acuity_realtime, acuity_worker;
ALTER DEFAULT PRIVILEGES FOR ROLE acuity_migrate IN SCHEMA public
REVOKE ALL ON SEQUENCES
FROM acuity_portal, acuity_provider, acuity_realtime, acuity_worker;
ALTER DEFAULT PRIVILEGES FOR ROLE acuity_migrate IN SCHEMA auth
REVOKE ALL ON TABLES
FROM acuity_auth;
ALTER DEFAULT PRIVILEGES FOR ROLE acuity_migrate IN SCHEMA auth
REVOKE ALL ON SEQUENCES
FROM acuity_auth;
