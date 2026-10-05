package knowledge

import (
	"context"
	"errors"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/google/uuid"
)

var ErrNoOffice = errors.New("Location has no single Abita Office Route")

type RevisionSummary struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
}

type LocationKnowledge struct {
	LocationID string           `json:"locationId"`
	Revision   *RevisionSummary `json:"revision,omitempty"`
	Sections   []Section        `json:"sections"`
}

func (m *Module) ReadLocation(ctx context.Context, identity access.Identity, practiceID, locationID string) (LocationKnowledge, error) {
	if uuid.Validate(practiceID) != nil || uuid.Validate(locationID) != nil {
		return LocationKnowledge{}, ErrInvalidInput
	}
	keys, err := m.access.ReadLocationAbitaOfficeKeys(ctx, identity, practiceID, locationID)
	if err != nil {
		return LocationKnowledge{}, err
	}
	if len(keys) != 1 {
		return LocationKnowledge{}, ErrNoOffice
	}
	rows, err := m.db.Query(ctx, `
		SELECT r.id::text, r.created_at, p.section_id, p.title, p.text
		FROM knowledge_corpora c
		JOIN knowledge_revisions r ON r.id = c.revision_id
		JOIN knowledge_passages p ON p.revision_id = r.id
		WHERE c.practice_id = $1 AND c.office_key = $2
		ORDER BY p.position NULLS LAST, p.section_id
	`, practiceID, keys[0])
	if err != nil {
		return LocationKnowledge{}, err
	}
	defer rows.Close()
	result := LocationKnowledge{LocationID: locationID, Sections: []Section{}}
	for rows.Next() {
		var revision RevisionSummary
		var section Section
		if err := rows.Scan(&revision.ID, &revision.CreatedAt, &section.ID, &section.Title, &section.Text); err != nil {
			return LocationKnowledge{}, err
		}
		result.Revision = &revision
		result.Sections = append(result.Sections, section)
	}
	if err := rows.Err(); err != nil {
		return LocationKnowledge{}, err
	}
	return result, nil
}
