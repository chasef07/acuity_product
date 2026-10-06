package workspace

import (
	"context"
	"errors"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	productpostgres "github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/jackc/pgx/v5"
)

var (
	ErrDenied       = errors.New("workspace read access denied")
	ErrInvalidInput = errors.New("invalid workspace read input")
)

type Module struct {
	database productpostgres.Database
	access   *access.Module
}

func New(database productpostgres.Database, accessModule *access.Module) *Module {
	return &Module{database: database, access: accessModule}
}

type QueryPhoneTimelineCommand struct {
	Ungrouped  bool
	Identity   access.Identity
	PracticeID string
	Phone      string
	Cursor     string
	Limit      int
}

type TimelineItem struct {
	Entries             []TimelineItem
	Type                string
	ID                  string
	OccurredAt          time.Time
	TaskActivityDetails map[string]any
	TaskActivity        string
	Message             messaging.Message
	Call                humancalling.CallHistoryItem
	AIInteraction       interaction.OutcomeItem
	Task                work.Task
}

type TimelinePage struct {
	Items      []TimelineItem
	NextCursor string
}

type QueryTasksCommand struct {
	Kind           string
	Responsibility string
	Category       work.TaskCategory
	Grouped        bool
	IncludeCounts  *bool
	Identity       access.Identity
	PracticeID     string
	LocationID     string
	Search         string
	State          work.TaskState
	Ordering       work.TaskOrdering
	Cursor         string
	Limit          int
}

func (m *Module) authorizedLocationIDs(
	ctx context.Context,
	tx pgx.Tx,
	identity access.Identity,
	practiceID string,
	locationID string,
) ([]string, error) {
	authorization, err := m.access.LockReadAuthorization(
		ctx, tx, identity, practiceID, locationID,
	)
	if err != nil {
		return nil, ErrDenied
	}
	if locationID != "" {
		return []string{locationID}, nil
	}
	locationIDs := make([]string, 0, len(authorization.Locations))
	for _, location := range authorization.Locations {
		locationIDs = append(locationIDs, location.ID)
	}
	if len(locationIDs) == 0 {
		return nil, ErrDenied
	}
	return locationIDs, nil
}
