package interaction

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
)

type ManualTags struct {
	Available []string
	Selected  []string
}

type ManualTagChange struct {
	Name    string
	Applied bool
}

func normalizeManualTag(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" || utf8.RuneCountInString(name) > 60 || !utf8.ValidString(name) {
		return "", ErrInvalidInput
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", ErrInvalidInput
		}
	}
	return name, nil
}

func availableManualTags(ctx context.Context, tx pgx.Tx, practiceID string) ([]string, error) {
	var names []string
	err := tx.QueryRow(ctx, `SELECT ARRAY(SELECT name FROM ai_manual_tags WHERE practice_id=$1 ORDER BY key)`, practiceID).Scan(&names)
	return names, err
}

// OperatorManualTags keeps human labels independent of provider closeout and AI analysis.
// A single-label mutation never overwrites another reviewer's labels.
func (m *Module) OperatorManualTags(ctx context.Context, identity access.Identity, interactionID string, change *ManualTagChange) (ManualTags, error) {
	if m.database == nil || m.access == nil || !validUUID(interactionID) {
		return ManualTags{}, ErrInvalidInput
	}
	var name string
	var err error
	if change != nil {
		name, err = normalizeManualTag(change.Name)
		if err != nil {
			return ManualTags{}, err
		}
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ManualTags{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var practiceID, locationID string
	err = tx.QueryRow(ctx, `SELECT practice_id::text, location_id::text FROM ai_interactions WHERE id=$1`, interactionID).Scan(&practiceID, &locationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ManualTags{}, ErrDenied
	}
	if err != nil {
		return ManualTags{}, err
	}
	var authorization access.Authorization
	if change == nil {
		authorization, err = m.access.LockReadAuthorization(ctx, tx, identity, practiceID, locationID)
	} else {
		authorization, err = m.access.LockMutationAuthorization(ctx, tx, identity, practiceID, locationID)
	}
	if err != nil || !authorization.PlatformOperator {
		return ManualTags{}, ErrDenied
	}
	if change != nil {
		var changed bool
		if change.Applied {
			_, err = tx.Exec(ctx, `INSERT INTO ai_manual_tags (practice_id,name,key) VALUES ($1,$2,lower($2)) ON CONFLICT DO NOTHING`, practiceID, name)
			if err != nil {
				return ManualTags{}, err
			}
			result, err := tx.Exec(ctx, `INSERT INTO ai_interaction_manual_tags (interaction_id,practice_id,tag_key,created_by) VALUES ($1,$2,lower($3),$4) ON CONFLICT DO NOTHING`, interactionID, practiceID, name, identity.Subject)
			if err != nil {
				return ManualTags{}, err
			}
			changed = result.RowsAffected() > 0
		} else {
			result, err := tx.Exec(ctx, `DELETE FROM ai_interaction_manual_tags WHERE interaction_id=$1 AND practice_id=$2 AND tag_key=lower($3)`, interactionID, practiceID, name)
			if err != nil {
				return ManualTags{}, err
			}
			changed = result.RowsAffected() > 0
		}
		if changed {
			action := "ai_interaction.tag_removed"
			if change.Applied {
				action = "ai_interaction.tag_added"
			}
			if err := m.access.AuditOperatorMutation(ctx, tx, authorization, access.OperatorMutationAudit{Action: action, ResourceType: "ai_interaction", ResourceID: interactionID, ResourceVersion: 1, OccurredAt: m.now()}); err != nil {
				return ManualTags{}, err
			}
		}
	}
	available, err := availableManualTags(ctx, tx, practiceID)
	if err != nil {
		return ManualTags{}, err
	}
	var selected []string
	err = tx.QueryRow(ctx, `SELECT ARRAY(SELECT tag.name FROM ai_manual_tags tag JOIN ai_interaction_manual_tags applied ON applied.practice_id=tag.practice_id AND applied.tag_key=tag.key WHERE applied.interaction_id=$1 ORDER BY tag.key)`, interactionID).Scan(&selected)
	if err != nil {
		return ManualTags{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ManualTags{}, err
	}
	return ManualTags{Available: available, Selected: selected}, nil
}
