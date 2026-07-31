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
    public.messaging_attachments,
    public.messaging_location_configurations,
    public.messaging_messages,
    public.messaging_thread_unreads,
    public.messaging_threads,
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
    public.messaging_attachments,
    public.messaging_messages,
    public.messaging_provider_commands,
    public.messaging_threads,
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

GRANT UPDATE (updated_at)
ON TABLE public.messaging_threads
TO acuity_portal;

GRANT UPDATE (task_id, version, updated_at)
ON TABLE public.messaging_messages
TO acuity_portal;

GRANT UPDATE (
    message_id,
    state,
    expires_at,
    copy_started_at,
    updated_at
)
ON TABLE public.messaging_attachments
TO acuity_portal;

GRANT DELETE ON TABLE public.messaging_thread_unreads
TO acuity_portal;

GRANT SELECT (
    message_id,
    practice_id,
    actor_subject,
    idempotency_key,
    input_fingerprint
)
ON TABLE public.messaging_provider_commands
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

GRANT SELECT ON TABLE public.messaging_attachments
TO acuity_provider;

GRANT INSERT (
    event_id,
    event_type,
    callback_token,
    occurred_at,
    received_at,
    signature_timestamp,
    raw_body
)
ON TABLE public.messaging_provider_receipts
TO acuity_provider;

GRANT SELECT (
    event_id,
    event_type,
    callback_token,
    occurred_at,
    signature_timestamp,
    raw_body,
    duplicate_count
)
ON TABLE public.messaging_provider_receipts
TO acuity_provider;

GRANT UPDATE (duplicate_count)
ON TABLE public.messaging_provider_receipts
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
    public.access_membership_locations,
    public.access_memberships,
    public.access_operational_users,
    public.access_practices,
    public.human_calling_calls,
    public.human_calling_connection_attempts,
    public.human_calling_credentials,
    public.human_calling_handoffs,
    public.human_calling_provider_commands,
    public.human_calling_provider_receipts,
    public.human_calling_recordings,
    public.human_calling_softphone_leases,
    public.messaging_attachments,
    public.messaging_location_configurations,
    public.messaging_messages,
    public.messaging_provider_commands,
    public.messaging_provider_receipts,
    public.messaging_thread_unreads,
    public.messaging_threads
TO acuity_worker;

GRANT INSERT ON TABLE
    public.human_calling_calls,
    public.human_calling_credentials,
    public.human_calling_projected_facts,
    public.human_calling_provider_commands,
    public.human_calling_recordings,
    public.human_calling_timeline,
    public.messaging_attachments,
    public.messaging_messages,
    public.messaging_thread_unreads,
    public.messaging_threads
TO acuity_worker;

GRANT SELECT (event_id)
ON TABLE public.human_calling_projected_facts
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

GRANT UPDATE (outbound_blocked, opt_out_evidence_at, opt_out_evidence_event_id, updated_at)
ON TABLE public.messaging_threads
TO acuity_worker;

GRANT UPDATE (
    delivery_state,
    safe_failure_code,
    provider_message_id,
    version,
    updated_at
)
ON TABLE public.messaging_messages
TO acuity_worker;

GRANT UPDATE (
    state,
    content_type,
    byte_size,
    object_key,
    copy_started_at,
    updated_at
)
ON TABLE public.messaging_attachments
TO acuity_worker;

GRANT UPDATE (unread_since, latest_message_id)
ON TABLE public.messaging_thread_unreads
TO acuity_worker;

GRANT UPDATE (
    state,
    provider_message_id,
    write_started_at,
    completed_at,
    next_attempt_at,
    reconcile_until,
    last_error_code,
    updated_at
)
ON TABLE public.messaging_provider_commands
TO acuity_worker;

GRANT UPDATE (
    state,
    processing_started_at,
    projected_at,
    projection_error_code
)
ON TABLE public.messaging_provider_receipts
TO acuity_worker;

GRANT DELETE ON TABLE public.messaging_attachments
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
