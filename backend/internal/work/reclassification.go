package work

import (
	"context"
	"fmt"
	"strings"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
)

type ReclassificationPlan struct {
	RestoreRunID string
	RunID        string
	PracticeID   string
	LocationIDs  []string
	Tasks        []ReclassificationEntry
}
type ReclassificationEntry struct {
	TaskID          string
	ExpectedVersion int64
	OldCategory     TaskCategory
	NewCategory     TaskCategory
	Title           string
	Message         string
	Reason          string
}
type ReclassificationResult struct {
	TaskID         string
	Status         string
	AppliedVersion int64
}

// PlanReclassification is read-only. Ambiguity is retained for human review.
func (m *Module) PlanReclassification(ctx context.Context, identity access.Identity, runID, practiceID string, locations []string) (ReclassificationPlan, error) {
	plan := ReclassificationPlan{RunID: runID, PracticeID: practiceID, LocationIDs: locations, Tasks: []ReclassificationEntry{}}
	if runID == "" || practiceID == "" || len(locations) == 0 {
		return plan, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return plan, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, location := range locations {
		auth, err := m.access.LockMutationAuthorization(ctx, tx, identity, practiceID, location)
		if err != nil || !auth.PlatformOperator {
			return plan, ErrDenied
		}
	}
	rows, err := tx.Query(ctx, `SELECT id::text,version,COALESCE(category,''),title,COALESCE(source_message,'') FROM work_tasks WHERE practice_id=$1 AND location_id=ANY($2::uuid[]) AND state='OPEN' ORDER BY created_at,id`, practiceID, locations)
	if err != nil {
		return plan, err
	}
	for rows.Next() {
		var entry ReclassificationEntry
		if err := rows.Scan(&entry.TaskID, &entry.ExpectedVersion, &entry.OldCategory, &entry.Title, &entry.Message); err != nil {
			rows.Close()
			return plan, err
		}
		entry.NewCategory, entry.Reason = reclassificationSuggestion(entry)
		plan.Tasks = append(plan.Tasks, entry)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return plan, err
	}
	if err := tx.Commit(ctx); err != nil {
		return plan, err
	}
	return plan, nil
}

// ApplyReclassification commits one version-checked Task per transaction. The
// durable run/Task receipt makes interrupted runs repeatable without duplicate Activity.
func (m *Module) ApplyReclassification(ctx context.Context, identity access.Identity, plan ReclassificationPlan) ([]ReclassificationResult, error) {
	if plan.RunID == "" || len(plan.RunID) > 200 || plan.PracticeID == "" || len(plan.LocationIDs) == 0 {
		return nil, ErrInvalidInput
	}
	results := make([]ReclassificationResult, 0, len(plan.Tasks))
	for _, entry := range plan.Tasks {
		result, err := m.applyReclassificationEntry(ctx, identity, plan, entry)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

func (m *Module) applyReclassificationEntry(ctx context.Context, identity access.Identity, plan ReclassificationPlan, entry ReclassificationEntry) (ReclassificationResult, error) {
	result := ReclassificationResult{TaskID: entry.TaskID}
	if entry.NewCategory == "" && plan.RestoreRunID == "" {
		result.Status = "needs_review"
		return result, nil
	}
	if (plan.RestoreRunID == "" && (!validTaskCategory(entry.NewCategory) || entry.NewCategory == TaskCategoryBilling)) || entry.ExpectedVersion < 1 || strings.TrimSpace(entry.Reason) == "" {
		return result, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	task, err := loadTask(ctx, tx, entry.TaskID)
	if err != nil {
		return result, err
	}
	inScope := false
	for _, id := range plan.LocationIDs {
		if task.LocationID == id {
			inScope = true
		}
	}
	if task.PracticeID != plan.PracticeID || !inScope {
		return result, ErrDenied
	}
	auth, err := m.authorizeMutation(ctx, tx, identity, task)
	if err != nil || !auth.PlatformOperator {
		return result, ErrDenied
	}
	task, err = lockTask(ctx, tx, task.ID)
	if err != nil {
		return result, err
	}
	err = tx.QueryRow(ctx, `SELECT applied_version FROM work_reclassification_changes WHERE run_id=$1 AND task_id=$2`, plan.RunID, task.ID).Scan(&result.AppliedVersion)
	if err == nil {
		result.Status = "already_applied"
		return result, nil
	}
	if err != pgx.ErrNoRows {
		return result, err
	}
	if task.State != TaskOpen || task.Version != entry.ExpectedVersion || task.Category != entry.OldCategory {
		result.Status = "skipped_changed"
		return result, nil
	}
	if task.Category == entry.NewCategory {
		result.Status = "unchanged"
		return result, nil
	}
	if plan.RestoreRunID != "" {
		var oldCategory, newCategory string
		var appliedVersion int64
		err := tx.QueryRow(ctx, `SELECT COALESCE(old_category,''),new_category,applied_version FROM work_reclassification_changes WHERE run_id=$1 AND task_id=$2`, plan.RestoreRunID, task.ID).Scan(&oldCategory, &newCategory, &appliedVersion)
		if err != nil || oldCategory != string(entry.NewCategory) || newCategory != string(entry.OldCategory) || appliedVersion != entry.ExpectedVersion {
			return result, ErrConflict
		}
	}
	task.Category = entry.NewCategory
	task.UpdatedAt = m.now()
	err = tx.QueryRow(ctx, `UPDATE work_tasks SET category=$2,version=version+1,updated_at=$3 WHERE id=$1 RETURNING version`, task.ID, nullIfEmpty(string(task.Category)), task.UpdatedAt).Scan(&task.Version)
	if err != nil {
		return result, err
	}
	if err := appendActivityDetails(ctx, tx, task, "CATEGORY_CHANGED", humanActorSnapshot(auth.Actor), map[string]any{"runId": plan.RunID, "oldCategory": entry.OldCategory, "newCategory": entry.NewCategory, "reason": entry.Reason}); err != nil {
		return result, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO work_reclassification_changes VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, plan.RunID, task.ID, nullIfEmpty(string(entry.OldCategory)), entry.NewCategory, entry.ExpectedVersion, task.Version, auth.Actor.Subject, task.UpdatedAt)
	if err != nil {
		return result, err
	}
	if err := m.auditOperatorMutation(ctx, tx, auth, task, "task.reclassified", task.UpdatedAt); err != nil {
		return result, err
	}
	if _, err := m.access.RecordWorkspaceChange(ctx, tx, task.PracticeID); err != nil {
		return result, err
	}
	if err := tx.Commit(ctx); err != nil {
		return result, err
	}
	result.Status = "applied"
	result.AppliedVersion = task.Version
	return result, nil
}

func (m *Module) RestorationPlan(ctx context.Context, identity access.Identity, runID, practiceID string, locations []string) (ReclassificationPlan, error) {
	plan, err := m.PlanReclassification(ctx, identity, "restore:"+runID, practiceID, locations)
	if err != nil {
		return plan, err
	}
	plan.Tasks = nil
	plan.RestoreRunID = runID
	rows, err := m.database.Query(ctx, `SELECT c.task_id::text,c.applied_version,c.new_category,COALESCE(c.old_category,'') FROM work_reclassification_changes c JOIN work_tasks t ON t.id=c.task_id WHERE c.run_id=$1 AND t.practice_id=$2 AND t.location_id=ANY($3::uuid[]) ORDER BY c.task_id`, runID, practiceID, locations)
	if err != nil {
		return plan, err
	}
	defer rows.Close()
	for rows.Next() {
		var e ReclassificationEntry
		if err := rows.Scan(&e.TaskID, &e.ExpectedVersion, &e.OldCategory, &e.NewCategory); err != nil {
			return plan, err
		}
		e.Reason = "Restore reviewed classification run " + runID
		plan.Tasks = append(plan.Tasks, e)
	}
	return plan, rows.Err()
}

func reclassificationSuggestion(entry ReclassificationEntry) (TaskCategory, string) {
	text := strings.ToLower(entry.Title + " " + entry.Message)
	has := func(phrases ...string) bool {
		for _, phrase := range phrases {
			if strings.Contains(text, phrase) {
				return true
			}
		}
		return false
	}
	copay := has("copay", "co-pay", "co pay", "copago")
	documentation := has("records release", "release of records", "medical records", "full records", "visit summary", "school note", "work note")
	scheduling := has("reschedule", "cancel appointment", "book an appointment", "appointment availability")
	referral := has("specialist referral", "send referral", "referral receipt", "imaging order")
	auth := has("prior auth", "authorization", "authorisation", "denial", "denied", "approval")
	switch {
	case (has("glasses", "contact lens", "frames") && has("refill", "medication prescription", "pharmacy")) || (auth && has("medication", "pharmacy", "drug") && has("procedure authorization", "surgery authorization", "visit authorization")):
		return "", "Distinct or ambiguous needs require individual staff review"
	case copay && (documentation || scheduling || referral || has("refill", "medication prescription")):
		return "", "Copay context overlaps another request; review the unresolved need"
	case copay:
		return TaskCategoryInsurance, "Copay or copayment question"
	case documentation:
		return TaskCategoryDocumentation, "Records or documentation request"
	case auth && has("medication", "pharmacy", "eye drops", "eyedrops", "drug", "refill"):
		return TaskCategoryMedication, "Medication authorization or denial follow-up"
	case auth && has("surgery", "procedure", "visit", "testing", "test ", "appointment"):
		return TaskCategoryInsurance, "Visit, procedure, surgery or test authorization"
	case auth:
		return "", "Authorization subject needs staff review"
	case has("glasses", "contact lens", "contacts prescription", "optical", "frames", "spectacle"):
		return TaskCategoryOptical, "Optical request context (not title alone)"
	case has("refill", "pharmacy", "medication prescription"):
		return TaskCategoryMedication, "Medication fulfillment"
	case scheduling:
		return TaskCategoryAppointments, "Appointment scheduling"
	case has("pre-op", "preop", "before surgery", "surgical preparation", "clearance coordination"):
		return TaskCategoryPreOp, "Surgical preparation or instructions"
	case has("post-op", "postop", "aftercare", "recovery after surgery", "after surgery"):
		return TaskCategoryPostOp, "Recovery or aftercare"
	case has("insurance acceptance", "insurance coverage", "insurance update", "referral requirement"):
		return TaskCategoryInsurance, "Insurance coverage or requirements"
	case referral:
		return TaskCategoryReferrals, "Referral or imaging-order coordination"
	case entry.OldCategory == TaskCategoryBilling:
		return TaskCategoryOther, "Remaining legacy Billing work"
	default:
		return "", fmt.Sprintf("Review actual request; retain %s until confirmed", entry.OldCategory)
	}
}
