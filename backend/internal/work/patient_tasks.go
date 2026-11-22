package work

import (
	"context"
	"sort"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/contactcontext"
	"github.com/jackc/pgx/v5"
)

type ReviewedTask struct {
	ID              string
	ExpectedVersion int64
}

type CompletePatientTasksCommand struct {
	Identity access.Identity
	Phone    string
	Tasks    []ReviewedTask
	Reason   CloseReason
}

func (m *Module) CompletePatientTasks(ctx context.Context, command CompletePatientTasksCommand) (int, error) {
	phone, err := contactcontext.NormalizePhone(command.Phone)
	if err != nil || m.access == nil || len(command.Tasks) == 0 || len(command.Tasks) > 100 || !command.Reason.staffMayChoose() {
		return 0, ErrInvalidInput
	}
	expected := map[string]int64{}
	ids := make([]string, 0, len(command.Tasks))
	for _, reviewed := range command.Tasks {
		if reviewed.ID == "" || reviewed.ExpectedVersion < 1 || expected[reviewed.ID] != 0 {
			return 0, ErrInvalidInput
		}
		expected[reviewed.ID] = reviewed.ExpectedVersion
		ids = append(ids, reviewed.ID)
	}
	sort.Strings(ids)
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorizations := make(map[string]access.Authorization, len(ids))
	practiceID := ""
	for _, id := range ids {
		task, err := loadTask(ctx, tx, id, false)
		if err != nil {
			return 0, err
		}
		if practiceID == "" {
			practiceID = task.PracticeID
		}
		if task.PracticeID != practiceID || task.Phone != phone {
			return 0, ErrDenied
		}
		authorization, err := m.authorizeMutation(ctx, tx, command.Identity, task)
		if err != nil {
			return 0, err
		}
		authorizations[id] = authorization
	}
	if err := m.LockTasks(ctx, tx, ids); err != nil {
		return 0, err
	}
	for _, id := range ids {
		task, err := loadTask(ctx, tx, id, true)
		if err != nil {
			return 0, err
		}
		if task.State != TaskOpen || task.Version != expected[id] {
			return 0, ErrConflict
		}
		if _, err := m.completeLockedTask(ctx, tx, authorizations[id], task, command.Reason, ""); err != nil {
			return 0, err
		}
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return len(ids), nil
}
