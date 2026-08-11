CREATE TABLE ai_interaction_attention (
    interaction_id uuid NOT NULL REFERENCES ai_interactions(id) ON DELETE CASCADE,
    user_subject text NOT NULL,
    outcome_occurred_at timestamptz NOT NULL,
    reviewed_at timestamptz,
    created_at timestamptz NOT NULL,
    PRIMARY KEY (interaction_id, user_subject, outcome_occurred_at)
);

CREATE INDEX ai_interaction_attention_unreviewed_idx
    ON ai_interaction_attention (user_subject, outcome_occurred_at DESC, interaction_id)
    WHERE reviewed_at IS NULL;

INSERT INTO ai_interaction_attention (
    interaction_id,
    user_subject,
    outcome_occurred_at,
    created_at
)
SELECT
    interaction.id,
    operational_scope.user_subject,
    interaction.appointment_occurred_at,
    now()
FROM ai_interactions interaction
JOIN access_operational_scopes operational_scope
    ON operational_scope.practice_id = interaction.practice_id
WHERE interaction.status <> 'IN_PROGRESS'
    AND interaction.appointment_outcome IN (
        'BOOKING', 'CANCELLATION', 'RESCHEDULE'
    )
    AND interaction.appointment_occurred_at IS NOT NULL
    AND (
        operational_scope.location_scope = 'ALL'
        OR EXISTS (
            SELECT 1
            FROM access_membership_locations location_grant
            WHERE location_grant.membership_id = operational_scope.membership_id
                AND location_grant.practice_id = operational_scope.practice_id
                AND location_grant.location_id = interaction.location_id
        )
    )
ON CONFLICT (interaction_id, user_subject, outcome_occurred_at)
DO NOTHING;
