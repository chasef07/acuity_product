package insurance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrInvalidInput = errors.New("invalid insurance input")
	ErrUnavailable  = errors.New("insurance rules are unavailable")
	ErrConflict     = errors.New("the insurance list changed; reload it before publishing")
	ErrNoCatalog    = errors.New("no insurance list has been published for this Practice")
)

type Provenance string

const (
	ProvenanceImport  Provenance = "import"
	ProvenancePortal  Provenance = "portal"
	ProvenanceRestore Provenance = "restore"
)

type Revision struct {
	ID                 string     `json:"id"`
	CreatedAt          time.Time  `json:"createdAt"`
	CreatedBy          string     `json:"createdBy"`
	Reason             string     `json:"reason"`
	Provenance         Provenance `json:"provenance"`
	PreviousRevisionID *string    `json:"previousRevisionId,omitempty"`
}

type LocationCatalog struct {
	Revision Revision `json:"revision"`
	Catalog  Catalog  `json:"catalog"`
	OfficeID string   `json:"officeId"`
	CanEdit  bool     `json:"canEdit"`
}

type ActiveCatalog struct {
	RevisionID string  `json:"revisionId"`
	Catalog    Catalog `json:"catalog"`
}

type PublishCommand struct {
	Identity           access.Identity
	PracticeID         string
	LocationID         string
	RevisionID         string
	ExpectedRevisionID string
	Reason             string
	Catalog            Catalog
}

type ImportCommand struct {
	PracticeID   string
	RevisionID   string
	Reason       string
	ActorSubject string
	Catalog      Catalog
}

type Revisions struct {
	db         postgres.Database
	access     *access.Module
	middleware *MiddlewareClient
}

func NewRevisions(db postgres.Database, accessModule *access.Module, middleware *MiddlewareClient) (*Revisions, error) {
	if db == nil || accessModule == nil {
		return nil, ErrInvalidInput
	}
	return &Revisions{db: db, access: accessModule, middleware: middleware}, nil
}

type replacement struct {
	practiceID         string
	revisionID         string
	expectedRevisionID string
	reason             string
	actor              string
	provenance         Provenance
	catalog            Catalog
	body               []byte
	hash               string
}

type authorizer func(ctx context.Context, tx pgx.Tx, before Catalog) error

func (r *Revisions) Publish(ctx context.Context, cmd PublishCommand) (Revision, error) {
	if uuid.Validate(cmd.PracticeID) != nil || uuid.Validate(cmd.LocationID) != nil || uuid.Validate(cmd.ExpectedRevisionID) != nil || !validReason(cmd.Reason) {
		return Revision{}, ErrInvalidInput
	}
	if id, err := uuid.Parse(cmd.RevisionID); err != nil || id == uuid.Nil {
		return Revision{}, ErrInvalidInput
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Revision{}, err
	}
	_, err = r.access.LockMutationAuthorization(ctx, tx, cmd.Identity, cmd.PracticeID, cmd.LocationID)
	_ = tx.Rollback(ctx)
	if err != nil {
		return Revision{}, err
	}
	return r.replace(ctx, replacement{
		practiceID: cmd.PracticeID, revisionID: cmd.RevisionID, expectedRevisionID: cmd.ExpectedRevisionID,
		reason: cmd.Reason, actor: cmd.Identity.Subject, provenance: ProvenancePortal, catalog: cmd.Catalog,
	}, func(ctx context.Context, tx pgx.Tx, before Catalog) error {
		return r.authorizeOffices(ctx, tx, cmd.Identity, cmd.PracticeID, cmd.LocationID, TouchedOffices(before, cmd.Catalog))
	})
}

func (r *Revisions) Import(ctx context.Context, cmd ImportCommand) (Revision, bool, error) {
	if uuid.Validate(cmd.PracticeID) != nil || uuid.Validate(cmd.RevisionID) != nil || !validReason(cmd.Reason) || strings.TrimSpace(cmd.ActorSubject) == "" {
		return Revision{}, false, ErrInvalidInput
	}
	if _, err := r.access.ReadPracticeKey(ctx, cmd.PracticeID); err != nil {
		return Revision{}, false, err
	}
	active, err := readActive(ctx, r.db, cmd.PracticeID)
	switch {
	case err == nil && active.hash == contentHash(cmd.Catalog.encode()):
		revision, err := r.withAuthor(ctx, cmd.PracticeID, active.revision)
		return revision, false, err
	case err == nil:
		return Revision{}, false, fmt.Errorf("the Practice already has insurance list revision %s, so change it in the Portal: %w", active.revision.ID, ErrConflict)
	case !errors.Is(err, ErrNoCatalog):
		return Revision{}, false, err
	}
	revision, err := r.replace(ctx, replacement{
		practiceID: cmd.PracticeID, revisionID: cmd.RevisionID, reason: cmd.Reason,
		actor: cmd.ActorSubject, provenance: ProvenanceImport, catalog: cmd.Catalog,
	}, nil)
	return revision, err == nil, err
}

func (r *Revisions) replace(ctx context.Context, cmd replacement, authorize authorizer) (Revision, error) {
	cmd.body = cmd.catalog.encode()
	cmd.hash = contentHash(cmd.body)
	check := func(tx pgx.Tx) (*Revision, error) {
		replayed, before, err := inspect(ctx, tx, cmd)
		if err != nil {
			return nil, err
		}
		if authorize != nil {
			if err := authorize(ctx, tx, before); err != nil {
				return nil, err
			}
		}
		return replayed, nil
	}
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Revision{}, err
	}
	replayed, err := check(tx)
	_ = tx.Rollback(ctx)
	if err != nil {
		return Revision{}, err
	}
	if replayed != nil {
		return r.withAuthor(ctx, cmd.practiceID, *replayed)
	}
	if r.middleware == nil {
		return Revision{}, fmt.Errorf("%w: the middleware validator is not configured", ErrUnavailable)
	}
	if err := r.middleware.Validate(ctx, cmd.catalog); err != nil {
		return Revision{}, err
	}
	tx, err = r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Revision{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `INSERT INTO insurance_catalogs(practice_id) VALUES($1) ON CONFLICT DO NOTHING`, cmd.practiceID); err != nil {
		return Revision{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT 1 FROM insurance_catalogs WHERE practice_id=$1 FOR UPDATE`, cmd.practiceID); err != nil {
		return Revision{}, err
	}
	if replayed, err = check(tx); err != nil {
		return Revision{}, err
	}
	if replayed == nil {
		if _, err = tx.Exec(ctx, `INSERT INTO insurance_revisions(id,practice_id,catalog,content_hash,reason,created_by,provenance,previous_revision_id) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`,
			cmd.revisionID, cmd.practiceID, cmd.body, cmd.hash, cmd.reason, cmd.actor, cmd.provenance, optionalID(cmd.expectedRevisionID)); err != nil {
			return Revision{}, err
		}
		tag, err := tx.Exec(ctx, `UPDATE insurance_catalogs SET revision_id=$2 WHERE practice_id=$1 AND revision_id IS NOT DISTINCT FROM $3::uuid`, cmd.practiceID, cmd.revisionID, optionalID(cmd.expectedRevisionID))
		if err != nil {
			return Revision{}, err
		}
		if tag.RowsAffected() != 1 {
			return Revision{}, ErrConflict
		}
	}
	active, err := readActive(ctx, tx, cmd.practiceID)
	if err != nil {
		return Revision{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Revision{}, err
	}
	return r.withAuthor(ctx, cmd.practiceID, active.revision)
}

func inspect(ctx context.Context, tx pgx.Tx, cmd replacement) (*Revision, Catalog, error) {
	var active string
	err := tx.QueryRow(ctx, `SELECT COALESCE(revision_id::text, '') FROM insurance_catalogs WHERE practice_id=$1`, cmd.practiceID).Scan(&active)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, Catalog{}, err
	}
	var replayed *Revision
	var practice, hash string
	var revision Revision
	err = tx.QueryRow(ctx, `SELECT id::text,practice_id::text,content_hash,created_at,created_by,reason,provenance,previous_revision_id::text FROM insurance_revisions WHERE id=$1`, cmd.revisionID).
		Scan(&revision.ID, &practice, &hash, &revision.CreatedAt, &revision.CreatedBy, &revision.Reason, &revision.Provenance, &revision.PreviousRevisionID)
	switch {
	case err == nil:
		if active != cmd.revisionID || practice != cmd.practiceID || hash != cmd.hash || revision.CreatedBy != cmd.actor ||
			revision.Reason != cmd.reason || revision.Provenance != cmd.provenance || stringValue(revision.PreviousRevisionID) != cmd.expectedRevisionID {
			return nil, Catalog{}, ErrConflict
		}
		replayed = &revision
	case !errors.Is(err, pgx.ErrNoRows):
		return nil, Catalog{}, err
	case active != cmd.expectedRevisionID:
		return nil, Catalog{}, ErrConflict
	}
	before := Catalog{}
	if cmd.expectedRevisionID != "" {
		var body []byte
		if err := tx.QueryRow(ctx, `SELECT catalog::text FROM insurance_revisions WHERE practice_id=$1 AND id=$2`, cmd.practiceID, cmd.expectedRevisionID).Scan(&body); err != nil {
			return nil, Catalog{}, err
		}
		if before, err = ParseCatalog(body); err != nil {
			return nil, Catalog{}, err
		}
	}
	return replayed, before, nil
}

func (r *Revisions) authorizeOffices(ctx context.Context, tx pgx.Tx, identity access.Identity, practiceID, locationID string, offices []string) error {
	authorization, err := r.access.LockMutationAuthorization(ctx, tx, identity, practiceID, locationID)
	if err != nil || authorization.PlatformOperator || len(offices) == 0 {
		return err
	}
	routes, err := r.access.ReadAbitaOfficeRoutes(ctx, tx, practiceID)
	if err != nil {
		return err
	}
	locations := officeLocations(routes)
	checked := map[string]bool{locationID: true}
	for _, office := range offices {
		if len(locations[office]) == 0 {
			return fmt.Errorf("%w: office %q has no Location, so only a Platform Operator can change it", access.ErrDenied, office)
		}
		for _, location := range locations[office] {
			if checked[location] {
				continue
			}
			if _, err := r.access.LockMutationAuthorization(ctx, tx, identity, practiceID, location); err != nil {
				return err
			}
			checked[location] = true
		}
	}
	return nil
}

func (r *Revisions) Query(ctx context.Context, identity access.Identity, practiceID, locationID string) (LocationCatalog, error) {
	if uuid.Validate(practiceID) != nil || uuid.Validate(locationID) != nil {
		return LocationCatalog{}, ErrInvalidInput
	}
	officeKey, err := r.access.ReadLocationAbitaOfficeKey(ctx, identity, practiceID, locationID)
	if err != nil {
		return LocationCatalog{}, err
	}
	active, err := readActive(ctx, r.db, practiceID)
	if err != nil {
		return LocationCatalog{}, err
	}
	office := middlewareOffice(officeKey)
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return LocationCatalog{}, err
	}
	denied := r.authorizeOffices(ctx, tx, identity, practiceID, locationID, active.catalog.tableOffices(office))
	_ = tx.Rollback(ctx)
	if denied != nil && !errors.Is(denied, access.ErrDenied) {
		return LocationCatalog{}, denied
	}
	revision, err := r.withAuthor(ctx, practiceID, active.revision)
	if err != nil {
		return LocationCatalog{}, err
	}
	return LocationCatalog{Revision: revision, Catalog: active.catalog, OfficeID: office, CanEdit: denied == nil}, nil
}

func (r *Revisions) Read(ctx context.Context, identity access.ServiceIdentity) (ActiveCatalog, error) {
	if !identity.Allows(access.ServiceCapabilityReadInsurance) || uuid.Validate(identity.PracticeID) != nil {
		return ActiveCatalog{}, access.ErrDenied
	}
	active, err := readActive(ctx, r.db, identity.PracticeID)
	if err != nil {
		return ActiveCatalog{}, err
	}
	return ActiveCatalog{RevisionID: active.revision.ID, Catalog: active.catalog}, nil
}

type storedRevision struct {
	revision Revision
	hash     string
	catalog  Catalog
}

type querier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func readActive(ctx context.Context, db querier, practiceID string) (storedRevision, error) {
	var stored storedRevision
	var body []byte
	err := db.QueryRow(ctx, `
		SELECT r.id::text, r.created_at, r.created_by, r.reason, r.provenance, r.previous_revision_id::text, r.content_hash, r.catalog::text
		FROM insurance_catalogs c
		JOIN insurance_revisions r ON r.practice_id = c.practice_id AND r.id = c.revision_id
		WHERE c.practice_id = $1
	`, practiceID).Scan(&stored.revision.ID, &stored.revision.CreatedAt, &stored.revision.CreatedBy, &stored.revision.Reason,
		&stored.revision.Provenance, &stored.revision.PreviousRevisionID, &stored.hash, &body)
	if errors.Is(err, pgx.ErrNoRows) {
		return storedRevision{}, ErrNoCatalog
	}
	if err != nil {
		return storedRevision{}, err
	}
	if stored.catalog, err = ParseCatalog(body); err != nil {
		return storedRevision{}, fmt.Errorf("stored insurance revision %s: %w", stored.revision.ID, err)
	}
	return stored, nil
}

func (r *Revisions) withAuthor(ctx context.Context, practiceID string, revision Revision) (Revision, error) {
	emails, err := r.access.ReadSubjectEmails(ctx, practiceID, []string{revision.CreatedBy})
	if err != nil {
		return Revision{}, err
	}
	revision.CreatedBy = emails[revision.CreatedBy]
	if revision.CreatedBy == "" {
		revision.CreatedBy = "Unknown user"
	}
	return revision, nil
}

func validReason(reason string) bool {
	return strings.TrimSpace(reason) != "" && utf8.RuneCountInString(reason) <= 2000
}

func optionalID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
