package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
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
	for index, section := range sections {
		// Sections remain whole so restrictions cannot be separated from their facts.
		// The provider rejects any input exceeding its token limit; never truncate.
		if !sectionID.MatchString(section.ID) || seen[section.ID] || strings.TrimSpace(section.Title) == "" || utf8.RuneCountInString(section.Title) > 200 || strings.TrimSpace(section.Text) == "" || utf8.RuneCountInString(section.Text) > 6000 {
			return fmt.Errorf("section %d needs a unique safe ID, nonempty title (up to 200 characters), and complete text (up to 6000 characters): %w", index+1, ErrInvalidInput)
		}
		seen[section.ID] = true
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

// ValidateImport validates the non-patient source manifest without provider or DB work.
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
	if !sectionID.MatchString(cmd.OfficeKey) || strings.TrimSpace(cmd.Provenance) == "" || len(cmd.Provenance) > 4000 || strings.TrimSpace(cmd.Reason) == "" || len(cmd.Reason) > 2000 || strings.TrimSpace(cmd.ActorSubject) == "" || len(cmd.ActorSubject) > 320 {
		return ErrInvalidInput
	}
	if err := validateSections(cmd.Sections); err != nil {
		return err
	}
	return nil
}

// ReplaceCorpus prepares all vectors before opening a transaction. The final
// transaction inserts complete immutable evidence and changes exactly one pointer.
// Its caller is the operator CLI using an existing privileged database credential.
func (m *Module) ReplaceCorpus(ctx context.Context, cmd ImportCommand) (Revision, error) {
	if err := ValidateImport(cmd); err != nil {
		return Revision{}, err
	}
	hash := hashSections(cmd.Sections)
	// Read-only preflight rejects bad routes/stale input without a provider request.
	tx, err := m.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Revision{}, err
	}
	rev, replayed, err := m.inspectImport(ctx, tx, cmd, hash)
	_ = tx.Rollback(ctx)
	if err != nil || replayed {
		return rev, err
	}
	texts := make([]string, len(cmd.Sections))
	for i, s := range cmd.Sections {
		texts[i] = s.Title + "\n" + s.Text
	}
	vectors, err := m.embed(ctx, texts, RetrievalDocument)
	if err != nil {
		return Revision{}, fmt.Errorf("embedding preparation failed; check provider access and whole-section token limits (no content was replaced): %w", err)
	}
	tx, err = m.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Revision{}, err
	}
	defer tx.Rollback(ctx)
	// The route and corpus locks serialize concurrent import attempts for this scope.
	var route string
	if err = tx.QueryRow(ctx, `SELECT office_key FROM access_abita_office_locations WHERE practice_id=$1 AND office_key=$2 FOR SHARE`, cmd.PracticeID, cmd.OfficeKey).Scan(&route); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = access.ErrDenied
		}
		return Revision{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO knowledge_corpora(practice_id,office_key) VALUES($1,$2) ON CONFLICT DO NOTHING`, cmd.PracticeID, cmd.OfficeKey); err != nil {
		return Revision{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT 1 FROM knowledge_corpora WHERE practice_id=$1 AND office_key=$2 FOR UPDATE`, cmd.PracticeID, cmd.OfficeKey); err != nil {
		return Revision{}, err
	}
	rev, replayed, err = m.inspectImport(ctx, tx, cmd, hash)
	if err != nil {
		return Revision{}, err
	}
	if replayed {
		return rev, tx.Commit(ctx)
	}
	rev = Revision{ID: cmd.ID, ContentHash: hash, Model: Model, Dimensions: Dimensions}
	err = tx.QueryRow(ctx, `INSERT INTO knowledge_revisions(id,practice_id,office_key,content_hash,embedding_model,embedding_dimensions,provenance,reason,created_by,previous_revision_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING created_at`, cmd.ID, cmd.PracticeID, cmd.OfficeKey, hash, Model, Dimensions, cmd.Provenance, cmd.Reason, cmd.ActorSubject, optionalID(cmd.ExpectedRevisionID)).Scan(&rev.CreatedAt)
	if err != nil {
		return Revision{}, err
	}
	for i, s := range cmd.Sections {
		if _, err = tx.Exec(ctx, `INSERT INTO knowledge_passages(revision_id,section_id,title,text,embedding) VALUES($1,$2,$3,$4,$5::vector)`, cmd.ID, s.ID, s.Title, s.Text, vectors[i]); err != nil {
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
	if err = tx.Commit(ctx); err != nil {
		return Revision{}, err
	}
	return rev, nil
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
	err = tx.QueryRow(ctx, `SELECT id::text,content_hash,embedding_model,embedding_dimensions,created_at,practice_id::text,office_key,provenance,reason,created_by,previous_revision_id::text FROM knowledge_revisions WHERE id=$1`, cmd.ID).Scan(&rev.ID, &rev.ContentHash, &rev.Model, &rev.Dimensions, &rev.CreatedAt, &practice, &office, &provenance, &reason, &actor, &previous)
	if err == nil {
		prior := ""
		if previous != nil {
			prior = *previous
		}
		if current == nil || *current != cmd.ID || practice != cmd.PracticeID || office != cmd.OfficeKey || rev.ContentHash != hash || provenance != cmd.Provenance || reason != cmd.Reason || actor != cmd.ActorSubject || prior != cmd.ExpectedRevisionID {
			return Revision{}, false, ErrConflict
		}
		return rev, true, nil
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

func (m *Module) embed(ctx context.Context, texts []string, task TaskType) ([]string, error) {
	if m.embedder == nil {
		return nil, ErrUnavailable
	}
	providerCtx, cancel := context.WithTimeout(ctx, m.config.ProviderTimeout)
	defer cancel()
	vectors, err := m.embedder.Embed(providerCtx, texts, task)
	if providerCtx.Err() != nil {
		return nil, providerCtx.Err()
	}
	if err != nil {
		var classified *ProviderFailure
		if errors.As(err, &classified) {
			return nil, classified
		}
		return nil, ErrUnavailable
	}
	if len(vectors) != len(texts) {
		return nil, ErrUnavailable
	}
	result := make([]string, len(vectors))
	for i, v := range vectors {
		if len(v) != Dimensions {
			return nil, ErrUnavailable
		}
		parts := make([]string, len(v))
		norm := float64(0)
		for j, x := range v {
			if math.IsNaN(float64(x)) || math.IsInf(float64(x), 0) {
				return nil, ErrUnavailable
			}
			norm += float64(x) * float64(x)
			parts[j] = strconv.FormatFloat(float64(x), 'g', -1, 32)
		}
		if norm == 0 {
			return nil, ErrUnavailable
		}
		result[i] = "[" + strings.Join(parts, ",") + "]"
	}
	return result, nil
}

// Search authorizes before embedding and again when reading the current pointer.
// The embedding provider is never called while a DB connection is held. Only the
// current revision is queried; an unavailable corpus never falls back to files.
func (m *Module) Search(ctx context.Context, identity access.ServiceIdentity, officeKey, query string) (SearchResult, error) {
	started := time.Now()
	empty := SearchResult{Outcome: "temporary_failure", Passages: []Passage{}}
	if strings.TrimSpace(query) == "" || utf8.RuneCountInString(query) > 500 {
		return empty, ErrInvalidInput
	}
	tx, err := m.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return empty, err
	}
	_, err = m.access.LockServiceAuthorization(ctx, tx, identity, officeKey, access.ServiceCapabilityReadKnowledge)
	var initial *string
	if err == nil {
		err = tx.QueryRow(ctx, `SELECT revision_id::text FROM knowledge_corpora WHERE practice_id=$1 AND office_key=$2`, identity.PracticeID, officeKey).Scan(&initial)
	}
	_ = tx.Rollback(ctx)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrUnavailable
		}
		return empty, err
	}
	if initial == nil {
		return empty, ErrUnavailable
	}
	vectors, err := m.embed(ctx, []string{query}, RetrievalQuery)
	if err != nil {
		return empty, err
	}
	if ctx.Err() != nil {
		return empty, ctx.Err()
	}
	tx, err = m.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return empty, err
	}
	defer tx.Rollback(ctx)
	_, err = m.access.LockServiceAuthorization(ctx, tx, identity, officeKey, access.ServiceCapabilityReadKnowledge)
	if err != nil {
		return empty, err
	}
	var revision, model string
	var dimensions int
	// Pointer selection is the retrieval linearization point. Revision rows are
	// immutable, so subsequent passage reads stay coherent if an import commits.
	err = tx.QueryRow(ctx, `SELECT r.id::text,r.embedding_model,r.embedding_dimensions FROM knowledge_corpora c JOIN knowledge_revisions r ON r.id=c.revision_id WHERE c.practice_id=$1 AND c.office_key=$2 `, identity.PracticeID, officeKey).Scan(&revision, &model, &dimensions)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			err = ErrUnavailable
		}
		return empty, err
	}
	if model != Model || dimensions != Dimensions {
		return empty, ErrUnavailable
	}
	rows, err := tx.Query(ctx, `SELECT section_id,title,text FROM knowledge_passages WHERE revision_id=$1 AND 1-(embedding <=> $2::vector)>=$3 ORDER BY embedding <=> $2::vector,section_id LIMIT $4`, revision, vectors[0], m.config.MinSimilarity, m.config.MaxPassages)
	if err != nil {
		return empty, err
	}
	result := SearchResult{Outcome: "no_relevant_information", RevisionID: &revision, Passages: []Passage{}}
	ids := []string{}
	for rows.Next() {
		p := Passage{RevisionID: revision}
		if err = rows.Scan(&p.SectionID, &p.Title, &p.Text); err != nil {
			rows.Close()
			return empty, err
		}
		result.Passages = append(result.Passages, p)
		ids = append(ids, p.SectionID)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return empty, err
	}
	if len(result.Passages) > 0 {
		result.Outcome = "found"
	}
	if _, err = tx.Exec(ctx, `INSERT INTO knowledge_retrieval_observations(id,practice_id,office_key,revision_id,section_ids,outcome,elapsed_millis) VALUES($1,$2,$3,$4,$5,$6,$7)`, uuid.NewString(), identity.PracticeID, officeKey, revision, ids, result.Outcome, time.Since(started).Milliseconds()); err != nil {
		return empty, err
	}
	if err = tx.Commit(ctx); err != nil {
		return empty, err
	}
	return result, nil
}
