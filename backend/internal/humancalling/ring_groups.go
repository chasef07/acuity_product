package humancalling

import (
	"context"
	"fmt"
	"net/mail"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

type LocationRingGroupProvision struct {
	PracticeKey  string
	LocationKey  string
	MemberEmails []string
}

// Omitted groups preserve existing routing. An explicit group must contain at
// least one account; unavailable members never widen the group to other Staff.
func (m *Module) ProvisionLocationRingGroupsInTx(
	ctx context.Context,
	tx pgx.Tx,
	provisions []LocationRingGroupProvision,
	requestedBy string,
) error {
	if tx == nil || strings.TrimSpace(requestedBy) == "" {
		return ErrInvalidInput
	}
	for _, provision := range provisions {
		if len(provision.MemberEmails) == 0 {
			return ErrInvalidInput
		}
		emails := make([]string, 0, len(provision.MemberEmails))
		for _, value := range provision.MemberEmails {
			email := strings.ToLower(strings.TrimSpace(value))
			parsed, err := mail.ParseAddress(email)
			if err != nil || parsed.Address != email {
				return ErrInvalidInput
			}
			emails = append(emails, email)
		}
		slices.Sort(emails)
		emails = slices.Compact(emails)
		var practiceID, locationID string
		if err := tx.QueryRow(ctx, `
			SELECT practice.id::text, location.id::text
			FROM access_practices practice
			JOIN access_locations location ON location.practice_id = practice.id
			WHERE practice.provisioning_key = $1 AND location.provisioning_key = $2
			FOR UPDATE OF location
		`, provision.PracticeKey, provision.LocationKey).Scan(&practiceID, &locationID); err != nil {
			return fmt.Errorf("resolve ring group Location: %w", err)
		}
		result, err := tx.Exec(ctx, `
			INSERT INTO human_calling_location_ring_groups (practice_id, location_id, member_emails)
			VALUES ($1, $2, $3)
			ON CONFLICT (practice_id, location_id) DO UPDATE SET
				member_emails = EXCLUDED.member_emails, updated_at = now()
			WHERE human_calling_location_ring_groups.member_emails IS DISTINCT FROM EXCLUDED.member_emails
		`, practiceID, locationID, emails)
		if err != nil {
			return fmt.Errorf("provision Location ring group: %w", err)
		}
		if result.RowsAffected() == 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO access_audit_events (actor_type, actor_subject, practice_id, action, details)
			VALUES ('PROVISIONER', $1, $2, 'calling.ring_group_configured',
				jsonb_build_object('locationId', $3::text, 'memberCount', $4::int))
		`, requestedBy, practiceID, locationID, len(emails)); err != nil {
			return fmt.Errorf("audit Location ring group: %w", err)
		}
	}
	return nil
}
