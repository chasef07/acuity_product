package knowledge

import (
	"context"
	"fmt"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/google/uuid"
)

const officeRevisions = `
	SELECT r.*, row_number() OVER (ORDER BY r.created_at, r.id) AS version
	FROM knowledge_revisions r
	WHERE r.practice_id = $1 AND r.office_key = $2`

type RevisionSummary struct {
	ID        string    `json:"id"`
	Version   int       `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
}

type LocationKnowledge struct {
	LocationID string           `json:"locationId"`
	Revision   *RevisionSummary `json:"revision,omitempty"`
	Sections   []Section        `json:"sections"`
}

type RevisionHistoryEntry struct {
	ID                 string     `json:"id"`
	Version            int        `json:"version"`
	CreatedAt          time.Time  `json:"createdAt"`
	Author             string     `json:"author"`
	Provenance         string     `json:"provenance"`
	Reason             string     `json:"reason"`
	PreviousRevisionID *string    `json:"previousRevisionId,omitempty"`
	Active             bool       `json:"active"`
	LastAgentSearchAt  *time.Time `json:"lastAgentSearchAt,omitempty"`
	AgentSearchCount   int        `json:"agentSearchCount"`
}

type LocationHistory struct {
	LocationID string                 `json:"locationId"`
	Revisions  []RevisionHistoryEntry `json:"revisions"`
}

func (m *Module) ReadLocation(ctx context.Context, identity access.Identity, practiceID, locationID string) (LocationKnowledge, error) {
	return m.readLocationRevision(ctx, identity, practiceID, locationID, "")
}

func (m *Module) ReadLocationRevision(ctx context.Context, identity access.Identity, practiceID, locationID, revisionID string) (LocationKnowledge, error) {
	if uuid.Validate(revisionID) != nil {
		return LocationKnowledge{}, ErrInvalidInput
	}
	result, err := m.readLocationRevision(ctx, identity, practiceID, locationID, revisionID)
	if err == nil && result.Revision == nil {
		return LocationKnowledge{}, fmt.Errorf("no revision %s for this Location's office: %w", revisionID, ErrInvalidInput)
	}
	return result, err
}

func (m *Module) readLocationRevision(ctx context.Context, identity access.Identity, practiceID, locationID, revisionID string) (LocationKnowledge, error) {
	officeKey, err := m.readLocationOfficeKey(ctx, identity, practiceID, locationID)
	if err != nil {
		return LocationKnowledge{}, err
	}
	rows, err := m.db.Query(ctx, `
		SELECT r.id::text, r.version, r.created_at, p.section_id, p.title, p.text
		FROM (`+officeRevisions+`) r
		JOIN knowledge_passages p ON p.revision_id = r.id
		WHERE r.id = COALESCE($3::uuid, (SELECT revision_id FROM knowledge_corpora WHERE practice_id = $1 AND office_key = $2))
		ORDER BY p.position NULLS LAST, p.section_id
	`, practiceID, officeKey, optionalID(revisionID))
	if err != nil {
		return LocationKnowledge{}, err
	}
	defer rows.Close()
	result := LocationKnowledge{LocationID: locationID, Sections: []Section{}}
	for rows.Next() {
		var revision RevisionSummary
		var section Section
		if err := rows.Scan(&revision.ID, &revision.Version, &revision.CreatedAt, &section.ID, &section.Title, &section.Text); err != nil {
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

func (m *Module) ReadLocationHistory(ctx context.Context, identity access.Identity, practiceID, locationID string) (LocationHistory, error) {
	officeKey, err := m.readLocationOfficeKey(ctx, identity, practiceID, locationID)
	if err != nil {
		return LocationHistory{}, err
	}
	rows, err := m.db.Query(ctx, `
		SELECT r.id::text, r.version, r.created_at,
			COALESCE(
				(SELECT email FROM access_memberships WHERE user_subject = r.created_by AND practice_id = r.practice_id),
				(SELECT email FROM access_platform_operators WHERE user_subject = r.created_by),
				'Unknown user'),
			r.provenance, r.reason, r.previous_revision_id::text,
			r.id IS NOT DISTINCT FROM (SELECT revision_id FROM knowledge_corpora WHERE practice_id = $1 AND office_key = $2),
			o.last_search, COALESCE(o.searches, 0)
		FROM (`+officeRevisions+`) r
		LEFT JOIN (
			SELECT revision_id, max(created_at) AS last_search, count(*) AS searches
			FROM knowledge_retrieval_observations
			WHERE practice_id = $1 AND office_key = $2
			GROUP BY revision_id
		) o ON o.revision_id = r.id
		ORDER BY r.version DESC
	`, practiceID, officeKey)
	if err != nil {
		return LocationHistory{}, err
	}
	defer rows.Close()
	result := LocationHistory{LocationID: locationID, Revisions: []RevisionHistoryEntry{}}
	for rows.Next() {
		var entry RevisionHistoryEntry
		if err := rows.Scan(&entry.ID, &entry.Version, &entry.CreatedAt, &entry.Author, &entry.Provenance, &entry.Reason, &entry.PreviousRevisionID, &entry.Active, &entry.LastAgentSearchAt, &entry.AgentSearchCount); err != nil {
			return LocationHistory{}, err
		}
		result.Revisions = append(result.Revisions, entry)
	}
	if err := rows.Err(); err != nil {
		return LocationHistory{}, err
	}
	return result, nil
}

func (m *Module) readLocationOfficeKey(ctx context.Context, identity access.Identity, practiceID, locationID string) (string, error) {
	if uuid.Validate(practiceID) != nil || uuid.Validate(locationID) != nil {
		return "", ErrInvalidInput
	}
	return m.access.ReadLocationAbitaOfficeKey(ctx, identity, practiceID, locationID)
}

func (m *Module) revisionVersion(ctx context.Context, practiceID, officeKey, revisionID string) (int, error) {
	var version int
	err := m.db.QueryRow(ctx, `SELECT version FROM (`+officeRevisions+`) r WHERE r.id = $3`, practiceID, officeKey, revisionID).Scan(&version)
	return version, err
}
