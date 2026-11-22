-- Delete the Acuity Demo practice and everything that belongs to it. The
-- provisioner only adds and updates, so taking the practice out of the
-- provisioning files left its Locations, office routes, voice numbers,
-- messaging config, calls, Tasks, texts and AI calls in place.
--
-- Work Tasks, calls, voicemails and texts reference each other in cycles, as do
-- knowledge and insurance revisions with their current pointers. Non-deferrable
-- foreign keys are checked at the end of a statement, so every delete runs as
-- one statement and the cycles need no intermediate updates (which the source
-- guards on work_tasks would refuse).
--
-- Stored text attachments get cleanup intents first, so the attachment cleanup
-- worker deletes their objects. A database with no demo practice is unchanged.
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '120s';

DO $migration$
DECLARE
    demo_practice_id uuid;
BEGIN
    SELECT id INTO demo_practice_id
    FROM access_practices
    WHERE provisioning_key = 'acuity-demo'
    FOR UPDATE;

    IF demo_practice_id IS NULL THEN
        RETURN;
    END IF;

    PERFORM 1 FROM access_locations WHERE practice_id = demo_practice_id FOR UPDATE;

    INSERT INTO messaging_attachment_cleanup (object_key, attachment_id, write_finished, cleanup_after)
    SELECT object_key, id, true, now()
    FROM messaging_attachments
    WHERE practice_id = demo_practice_id AND object_key IS NOT NULL
    ON CONFLICT (object_key) DO NOTHING;

    WITH
    demo_interactions AS (
        SELECT id FROM ai_interactions WHERE practice_id = demo_practice_id
    ),
    demo_calls AS (
        SELECT id FROM human_calling_calls WHERE practice_id = demo_practice_id
    ),
    demo_legs AS (
        SELECT id FROM human_calling_call_legs WHERE call_id IN (SELECT id FROM demo_calls)
    ),
    demo_tasks AS (
        SELECT id FROM work_tasks WHERE practice_id = demo_practice_id
    ),
    demo_messages AS (
        SELECT id FROM messaging_messages WHERE practice_id = demo_practice_id
    ),
    demo_receipts AS (
        SELECT event_id FROM human_calling_provider_receipts WHERE call_id IN (SELECT id FROM demo_calls)
    ),
    demo_revisions AS (
        SELECT id FROM knowledge_revisions WHERE practice_id = demo_practice_id
    ),
    d_classifications AS (DELETE FROM ai_interaction_classifications WHERE interaction_id IN (SELECT id FROM demo_interactions)),
    d_insurance_gaps AS (DELETE FROM ai_interaction_insurance_gap_reviews WHERE interaction_id IN (SELECT id FROM demo_interactions)),
    d_issues AS (DELETE FROM ai_interaction_issues WHERE interaction_id IN (SELECT id FROM demo_interactions)),
    d_knowledge_gaps AS (DELETE FROM ai_interaction_knowledge_gap_reviews WHERE interaction_id IN (SELECT id FROM demo_interactions)),
    d_ai_receipts AS (DELETE FROM ai_interaction_receipts WHERE practice_id = demo_practice_id OR interaction_id IN (SELECT id FROM demo_interactions)),
    d_scorecard_answers AS (DELETE FROM ai_interaction_scorecard_answers WHERE practice_id = demo_practice_id),
    d_interactions AS (DELETE FROM ai_interactions WHERE practice_id = demo_practice_id),
    d_rejected_legs AS (DELETE FROM human_calling_rejected_provider_legs WHERE initiated_event_id IN (SELECT event_id FROM demo_receipts)),
    d_call_receipts AS (DELETE FROM human_calling_provider_receipts WHERE event_id IN (SELECT event_id FROM demo_receipts)),
    d_staff_transfers AS (DELETE FROM human_calling_staff_transfers WHERE practice_id = demo_practice_id),
    d_call_commands AS (
        DELETE FROM human_calling_provider_commands
        WHERE call_id IN (SELECT id FROM demo_calls)
            OR call_leg_id IN (SELECT id FROM demo_legs)
            OR peer_call_leg_id IN (SELECT id FROM demo_legs)
    ),
    d_timeline AS (DELETE FROM human_calling_timeline WHERE practice_id = demo_practice_id),
    d_recordings AS (DELETE FROM human_calling_call_recordings WHERE practice_id = demo_practice_id),
    d_voicemails AS (DELETE FROM human_calling_voicemails WHERE practice_id = demo_practice_id),
    d_legs AS (DELETE FROM human_calling_call_legs WHERE id IN (SELECT id FROM demo_legs)),
    d_task_interactions AS (DELETE FROM work_task_interactions WHERE practice_id = demo_practice_id),
    d_reclassifications AS (DELETE FROM work_reclassification_changes WHERE task_id IN (SELECT id FROM demo_tasks)),
    d_acknowledgements AS (DELETE FROM work_task_acknowledgements WHERE task_id IN (SELECT id FROM demo_tasks) OR message_id IN (SELECT id FROM demo_messages)),
    d_activities AS (DELETE FROM work_task_activities WHERE task_id IN (SELECT id FROM demo_tasks)),
    d_text_replies AS (DELETE FROM work_text_replies WHERE task_id IN (SELECT id FROM demo_tasks) OR message_id IN (SELECT id FROM demo_messages)),
    d_calls AS (DELETE FROM human_calling_calls WHERE practice_id = demo_practice_id),
    d_handoffs AS (DELETE FROM human_calling_handoffs WHERE practice_id = demo_practice_id),
    d_tasks AS (DELETE FROM work_tasks WHERE practice_id = demo_practice_id),
    d_message_commands AS (DELETE FROM messaging_provider_commands WHERE practice_id = demo_practice_id),
    d_attachments AS (DELETE FROM messaging_attachments WHERE practice_id = demo_practice_id),
    d_messages AS (DELETE FROM messaging_messages WHERE practice_id = demo_practice_id),
    d_threads AS (DELETE FROM messaging_threads WHERE practice_id = demo_practice_id),
    d_blocked_senders AS (DELETE FROM messaging_blocked_senders WHERE practice_id = demo_practice_id),
    d_messaging_configurations AS (DELETE FROM messaging_location_configurations WHERE practice_id = demo_practice_id),
    d_ring_groups AS (DELETE FROM human_calling_location_ring_groups WHERE practice_id = demo_practice_id),
    d_voice_numbers AS (DELETE FROM human_calling_location_voice_numbers WHERE practice_id = demo_practice_id),
    d_voice_fallbacks AS (DELETE FROM human_calling_outbound_voice_fallbacks WHERE practice_id = demo_practice_id),
    d_reconciliation_queue AS (DELETE FROM work_recovery_reconciliation_queue WHERE practice_id = demo_practice_id),
    d_resolution_checkpoints AS (DELETE FROM work_recovery_resolution_checkpoints WHERE practice_id = demo_practice_id),
    d_responsibilities AS (DELETE FROM work_responsibilities WHERE practice_id = demo_practice_id),
    d_responsibility_locations AS (DELETE FROM work_responsibility_locations WHERE practice_id = demo_practice_id),
    d_retrieval_observations AS (DELETE FROM knowledge_retrieval_observations WHERE practice_id = demo_practice_id),
    d_passages AS (DELETE FROM knowledge_passages WHERE revision_id IN (SELECT id FROM demo_revisions)),
    d_knowledge_revisions AS (DELETE FROM knowledge_revisions WHERE practice_id = demo_practice_id),
    d_knowledge_corpora AS (DELETE FROM knowledge_corpora WHERE practice_id = demo_practice_id),
    d_insurance_revisions AS (DELETE FROM insurance_revisions WHERE practice_id = demo_practice_id),
    d_insurance_catalogs AS (DELETE FROM insurance_catalogs WHERE practice_id = demo_practice_id),
    d_membership_locations AS (DELETE FROM access_membership_locations WHERE practice_id = demo_practice_id),
    d_memberships AS (DELETE FROM access_memberships WHERE practice_id = demo_practice_id),
    d_grant_locations AS (DELETE FROM access_grant_locations WHERE practice_id = demo_practice_id),
    d_grants AS (DELETE FROM access_grants WHERE practice_id = demo_practice_id),
    d_office_routes AS (DELETE FROM access_abita_office_locations WHERE practice_id = demo_practice_id),
    d_audit_events AS (DELETE FROM access_audit_events WHERE practice_id = demo_practice_id),
    d_locations AS (DELETE FROM access_locations WHERE practice_id = demo_practice_id)
    DELETE FROM access_practices WHERE id = demo_practice_id;
END
$migration$;
