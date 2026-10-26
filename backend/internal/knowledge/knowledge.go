package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var sectionID = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,99}$`)

func validateSections(sections []Section) error {
	if len(sections) == 0 || len(sections) > 100 {
		return fmt.Errorf("corpus must contain 1 to 100 whole sections: %w", ErrInvalidInput)
	}
	seen := map[string]bool{}
	characters := 0
	for index, section := range sections {
		characters += utf8.RuneCountInString(section.Title) + utf8.RuneCountInString(section.Text)
		var rule string
		switch {
		case !sectionID.MatchString(section.ID):
			rule = fmt.Sprintf("at position %d needs an ID of up to 100 letters, numbers, dots, dashes, or underscores", index+1)
		case seen[section.ID]:
			rule = "is used twice"
		case strings.TrimSpace(section.Title) == "" || utf8.RuneCountInString(section.Title) > 200:
			rule = "needs a title of 1 to 200 characters"
		case strings.TrimSpace(section.Text) == "" || utf8.RuneCountInString(section.Text) > 6000:
			rule = "needs text of 1 to 6000 characters"
		default:
			seen[section.ID] = true
			continue
		}
		return &EntryError{EntryID: section.ID, Rule: rule}
	}
	if characters > maxKnowledgeCharacters {
		return &SizeError{Characters: characters}
	}
	return nil
}
func hashSections(sections []Section) string {
	body, _ := json.Marshal(sections)
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}
func optionalID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

func ValidateImport(cmd ImportCommand) error {
	if _, err := uuid.Parse(cmd.ID); err != nil {
		return ErrInvalidInput
	}
	if _, err := uuid.Parse(cmd.PracticeID); err != nil {
		return ErrInvalidInput
	}
	if cmd.ExpectedRevisionID != "" {
		if _, err := uuid.Parse(cmd.ExpectedRevisionID); err != nil {
			return ErrInvalidInput
		}
	}
	if !sectionID.MatchString(cmd.OfficeKey) || strings.TrimSpace(cmd.Provenance) == "" || len(cmd.Provenance) > 4000 || strings.TrimSpace(cmd.Reason) == "" || utf8.RuneCountInString(cmd.Reason) > 2000 || strings.TrimSpace(cmd.ActorSubject) == "" || len(cmd.ActorSubject) > 320 {
		return ErrInvalidInput
	}
	if err := validateSections(cmd.Sections); err != nil {
		return err
	}
	return nil
}

func (m *Module) ReplaceCorpus(ctx context.Context, cmd ImportCommand) (Revision, error) {
	return m.replaceCorpus(ctx, cmd, nil)
}

func (m *Module) replaceCorpus(ctx context.Context, cmd ImportCommand, authorize func(context.Context, pgx.Tx) error) (Revision, error) {
	if err := ValidateImport(cmd); err != nil {
		return Revision{}, err
	}
	practiceKey, err := m.access.ReadPracticeKey(ctx, cmd.PracticeID)
	if err != nil {
		return Revision{}, err
	}
	if practiceKey == abitaPracticeKey {
		if err := validateAbitaStandard(cmd.Sections); err != nil {
			return Revision{}, err
		}
	}
	hash := hashSections(cmd.Sections)
	tx, err := m.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Revision{}, err
	}
	defer tx.Rollback(ctx)
	if authorize != nil {
		if err = authorize(ctx, tx); err != nil {
			return Revision{}, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO knowledge_corpora(practice_id,office_key) VALUES($1,$2) ON CONFLICT DO NOTHING`, cmd.PracticeID, cmd.OfficeKey); err != nil {
		return Revision{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT 1 FROM knowledge_corpora WHERE practice_id=$1 AND office_key=$2 FOR UPDATE`, cmd.PracticeID, cmd.OfficeKey); err != nil {
		return Revision{}, err
	}
	rev, replayed, err := m.inspectImport(ctx, tx, cmd, hash)
	if err != nil {
		return Revision{}, err
	}
	if replayed {
		return rev, tx.Commit(ctx)
	}
	rev = Revision{ID: cmd.ID, ContentHash: hash}
	err = tx.QueryRow(ctx, `INSERT INTO knowledge_revisions(id,practice_id,office_key,content_hash,provenance,reason,created_by,previous_revision_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING created_at`, cmd.ID, cmd.PracticeID, cmd.OfficeKey, hash, cmd.Provenance, cmd.Reason, cmd.ActorSubject, optionalID(cmd.ExpectedRevisionID)).Scan(&rev.CreatedAt)
	if err != nil {
		return Revision{}, err
	}
	for i, s := range cmd.Sections {
		if _, err = tx.Exec(ctx, `INSERT INTO knowledge_passages(revision_id,section_id,title,text,position) VALUES($1,$2,$3,$4,$5)`, cmd.ID, s.ID, s.Title, s.Text, i); err != nil {
			return Revision{}, err
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE knowledge_corpora SET revision_id=$3 WHERE practice_id=$1 AND office_key=$2 AND revision_id IS NOT DISTINCT FROM $4::uuid`, cmd.PracticeID, cmd.OfficeKey, cmd.ID, optionalID(cmd.ExpectedRevisionID))
	if err != nil {
		return Revision{}, err
	}
	if tag.RowsAffected() != 1 {
		return Revision{}, ErrConflict
	}
	if rev.Version, err = revisionVersion(ctx, tx, cmd.PracticeID, cmd.OfficeKey, cmd.ID); err != nil {
		return Revision{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Revision{}, err
	}
	return rev, nil
}

const officeRevisions = `
	SELECT r.*, row_number() OVER (ORDER BY r.created_at, r.id) AS version
	FROM knowledge_revisions r
	WHERE r.practice_id = $1 AND r.office_key = $2`

func revisionVersion(ctx context.Context, tx pgx.Tx, practiceID, officeKey, revisionID string) (int, error) {
	var version int
	err := tx.QueryRow(ctx, `SELECT version FROM (`+officeRevisions+`) r WHERE r.id = $3`, practiceID, officeKey, revisionID).Scan(&version)
	return version, err
}

func (m *Module) ReadRevision(ctx context.Context, practiceID, officeKey, revisionID string) (string, []Section, error) {
	if uuid.Validate(practiceID) != nil || (revisionID != "" && uuid.Validate(revisionID) != nil) {
		return "", nil, ErrInvalidInput
	}
	revision, sections, err := readRevision(ctx, m.db, practiceID, officeKey, revisionID)
	if err != nil {
		return "", nil, err
	}
	if revision == nil {
		return "", nil, fmt.Errorf("no such revision for office %q: %w", officeKey, ErrInvalidInput)
	}
	return revision.ID, sections, nil
}

type querier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func readRevision(ctx context.Context, db querier, practiceID, officeKey, revisionID string) (*RevisionSummary, []Section, error) {
	rows, err := db.Query(ctx, `
		SELECT r.id::text, r.version, r.created_at, p.section_id, p.title, p.text
		FROM (`+officeRevisions+`) r
		JOIN knowledge_passages p ON p.revision_id = r.id
		WHERE r.id = COALESCE($3::uuid, (SELECT revision_id FROM knowledge_corpora WHERE practice_id = $1 AND office_key = $2))
		ORDER BY p.position NULLS LAST, p.section_id
	`, practiceID, officeKey, optionalID(revisionID))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var revision *RevisionSummary
	sections := []Section{}
	for rows.Next() {
		var summary RevisionSummary
		var section Section
		if err := rows.Scan(&summary.ID, &summary.Version, &summary.CreatedAt, &section.ID, &section.Title, &section.Text); err != nil {
			return nil, nil, err
		}
		revision = &summary
		sections = append(sections, section)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return revision, sections, nil
}

func (m *Module) inspectImport(ctx context.Context, tx pgx.Tx, cmd ImportCommand, hash string) (Revision, bool, error) {
	var current *string
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM access_abita_office_locations WHERE practice_id=$1 AND office_key=$2), (SELECT revision_id::text FROM knowledge_corpora WHERE practice_id=$1 AND office_key=$2)`, cmd.PracticeID, cmd.OfficeKey).Scan(&exists, &current)
	if err != nil {
		return Revision{}, false, err
	}
	if !exists {
		return Revision{}, false, access.ErrDenied
	}
	var rev Revision
	var practice, office, provenance, reason, actor string
	var previous *string
	err = tx.QueryRow(ctx, `SELECT id::text,content_hash,created_at,practice_id::text,office_key,provenance,reason,created_by,previous_revision_id::text FROM knowledge_revisions WHERE id=$1`, cmd.ID).Scan(&rev.ID, &rev.ContentHash, &rev.CreatedAt, &practice, &office, &provenance, &reason, &actor, &previous)
	if err == nil {
		prior := ""
		if previous != nil {
			prior = *previous
		}
		if current == nil || *current != cmd.ID || practice != cmd.PracticeID || office != cmd.OfficeKey || rev.ContentHash != hash || provenance != cmd.Provenance || reason != cmd.Reason || actor != cmd.ActorSubject || prior != cmd.ExpectedRevisionID {
			return Revision{}, false, ErrConflict
		}
		rev.Version, err = revisionVersion(ctx, tx, cmd.PracticeID, cmd.OfficeKey, cmd.ID)
		return rev, err == nil, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Revision{}, false, err
	}
	currentID := ""
	if current != nil {
		currentID = *current
	}
	if currentID != cmd.ExpectedRevisionID {
		return Revision{}, false, ErrConflict
	}
	return Revision{}, false, nil
}

func (m *Module) Read(ctx context.Context, identity access.ServiceIdentity, officeKey string) (OfficeKnowledge, error) {
	started := time.Now()
	tx, err := m.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return OfficeKnowledge{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = m.access.LockServiceAuthorization(ctx, tx, identity, officeKey, access.ServiceCapabilityReadKnowledge); err != nil {
		return OfficeKnowledge{}, err
	}
	revision, entries, err := readRevision(ctx, tx, identity.PracticeID, officeKey, "")
	if err != nil {
		return OfficeKnowledge{}, err
	}
	if revision == nil {
		return OfficeKnowledge{}, ErrUnavailable
	}
	ids := make([]string, len(entries))
	for i, entry := range entries {
		ids[i] = entry.ID
	}
	if err = recordObservation(ctx, tx, identity.PracticeID, officeKey, revision.ID, ids, "found", started); err != nil {
		return OfficeKnowledge{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return OfficeKnowledge{}, err
	}
	return OfficeKnowledge{RevisionID: revision.ID, Entries: entries}, nil
}

func recordObservation(ctx context.Context, tx pgx.Tx, practiceID, officeKey, revisionID string, sectionIDs []string, outcome string, started time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO knowledge_retrieval_observations(id,practice_id,office_key,revision_id,section_ids,outcome,elapsed_millis) VALUES($1,$2,$3,$4,$5,$6,$7)`, uuid.NewString(), practiceID, officeKey, revisionID, sectionIDs, outcome, time.Since(started).Milliseconds())
	return err
}
