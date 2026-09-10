// Package knowledge owns imported office facts and their authoritative corpus pointer.
// This corpus contains reusable, non-patient knowledge and never creates Tasks.
package knowledge

import (
	"context"
	"errors"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/postgres"
)

const Model = "text-multilingual-embedding-002"
const Dimensions = 768

type TaskType string

const (
	RetrievalDocument TaskType = "RETRIEVAL_DOCUMENT"
	RetrievalQuery    TaskType = "RETRIEVAL_QUERY"
)

type Embedder interface {
	Embed(context.Context, []string, TaskType) ([][]float32, error)
}
type Config struct {
	MinSimilarity   float64
	MaxPassages     int
	ProviderTimeout time.Duration
}
type Module struct {
	db       postgres.Database
	access   *access.Module
	embedder Embedder
	config   Config
}

var (
	ErrInvalidInput = errors.New("invalid knowledge input")
	ErrConflict     = errors.New("knowledge corpus changed; inspect current revision before retrying")
	ErrUnavailable  = errors.New("knowledge is temporarily unavailable")
)

func New(db postgres.Database, accessModule *access.Module, embedder Embedder, config Config) (*Module, error) {
	if db == nil || accessModule == nil {
		return nil, ErrInvalidInput
	}
	if config.MinSimilarity == 0 {
		config.MinSimilarity = 0.52
	}
	if config.MaxPassages == 0 {
		config.MaxPassages = 4
	}
	if config.ProviderTimeout == 0 {
		config.ProviderTimeout = 8 * time.Second
	}
	if config.MaxPassages < 1 || config.MaxPassages > 8 || config.MinSimilarity < 0 || config.MinSimilarity > 1 || config.ProviderTimeout <= 0 {
		return nil, ErrInvalidInput
	}
	return &Module{db: db, access: accessModule, embedder: embedder, config: config}, nil
}

type Section struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Text  string `json:"text"`
}

// ImportCommand is only accepted by the operator CLI, never a browser or agent API.
// ExpectedRevisionID is empty only for the first import. ID is the idempotency key.
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
	Model       string    `json:"model"`
	Dimensions  int       `json:"dimensions"`
	CreatedAt   time.Time `json:"createdAt"`
}
type Passage struct {
	SectionID  string `json:"sectionId"`
	Title      string `json:"title"`
	Text       string `json:"text"`
	RevisionID string `json:"revisionId"`
}
type SearchResult struct {
	Outcome    string    `json:"outcome"`
	RevisionID *string   `json:"revisionId,omitempty"`
	Passages   []Passage `json:"passages"`
}
