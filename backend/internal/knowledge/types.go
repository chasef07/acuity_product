package knowledge

import (
	"errors"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/postgres"
)

type Module struct {
	db     postgres.Database
	access *access.Module
}

var (
	ErrInvalidInput = errors.New("invalid knowledge input")
	ErrConflict     = errors.New("knowledge corpus changed; inspect current revision before retrying")
	ErrUnavailable  = errors.New("knowledge is temporarily unavailable")
)

func New(db postgres.Database, accessModule *access.Module) (*Module, error) {
	if db == nil || accessModule == nil {
		return nil, ErrInvalidInput
	}
	return &Module{db: db, access: accessModule}, nil
}

type Section struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

type ImportCommand struct {
	ID                 string    `json:"id"`
	PracticeID         string    `json:"practiceId"`
	OfficeKey          string    `json:"officeKey"`
	ExpectedRevisionID string    `json:"expectedRevisionId"`
	Sections           []Section `json:"sections"`
	Provenance         string    `json:"provenance"`
	Reason             string    `json:"reason"`
	ActorSubject       string    `json:"actorSubject"`
}
type Revision struct {
	ID          string    `json:"id"`
	ContentHash string    `json:"contentHash"`
	Version     int       `json:"version"`
	CreatedAt   time.Time `json:"createdAt"`
}
type OfficeKnowledge struct {
	RevisionID string    `json:"revisionId"`
	Entries    []Section `json:"entries"`
}
