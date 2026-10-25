package knowledge

import (
	"context"
	"fmt"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/google/uuid"
)

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
	revision, sections, err := readRevision(ctx, m.db, practiceID, officeKey, revisionID)
	if err != nil {
		return LocationKnowledge{}, err
	}
	return LocationKnowledge{LocationID: locationID, Revision: revision, Sections: sections}, nil
}

func (m *Module) ReadLocationHistory(ctx context.Context, identity access.Identity, practiceID, locationID string) (LocationHistory, error) {
	officeKey, err := m.readLocationOfficeKey(ctx, identity, practiceID, locationID)
	if err != nil {
		return LocationHistory{}, err
	}
	rows, err := m.db.Query(ctx, `
		SELECT r.id::text, r.version, r.created_at, r.created_by, r.provenance, r.reason, r.previous_revision_id::text,
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
	subjects := []string{}
	for rows.Next() {
		var entry RevisionHistoryEntry
		var subject string
		if err := rows.Scan(&entry.ID, &entry.Version, &entry.CreatedAt, &subject, &entry.Provenance, &entry.Reason, &entry.PreviousRevisionID, &entry.Active, &entry.LastAgentSearchAt, &entry.AgentSearchCount); err != nil {
			return LocationHistory{}, err
		}
		result.Revisions = append(result.Revisions, entry)
		subjects = append(subjects, subject)
	}
	if err := rows.Err(); err != nil {
		return LocationHistory{}, err
	}
	emails, err := m.access.ReadSubjectEmails(ctx, practiceID, subjects)
	if err != nil {
		return LocationHistory{}, err
	}
	for i, subject := range subjects {
		result.Revisions[i].Author = emails[subject]
		if result.Revisions[i].Author == "" {
			result.Revisions[i].Author = "Unknown user"
		}
	}
	return result, nil
}

func (m *Module) readLocationOfficeKey(ctx context.Context, identity access.Identity, practiceID, locationID string) (string, error) {
	if uuid.Validate(practiceID) != nil || uuid.Validate(locationID) != nil {
		return "", ErrInvalidInput
	}
	return m.access.ReadLocationAbitaOfficeKey(ctx, identity, practiceID, locationID)
}
