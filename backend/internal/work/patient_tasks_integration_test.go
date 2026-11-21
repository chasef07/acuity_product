package work_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
)

func TestCompletePatientTasksClosesExactlyTheReviewedTasksOrNone(t *testing.T) {
	db := testdb.Open(t)
	now := time.Now().UTC()
	a := access.New(db.As(testdb.Portal), func() time.Time { return now })
	auth, identity := provisionStaff(t, db.Owner, a, now)
	m := work.New(db.As(testdb.Portal), a, func() time.Time { return now })
	ctx := context.Background()
	create := func(key, phone string, category work.TaskCategory) work.Task {
		t.Helper()
		task, _, err := m.CreateAITask(ctx, work.CreateAITaskCommand{Service: access.ServiceIdentity{Subject: "patient-agent", PracticeID: auth.Practice.ID, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityCreateTask}}, OfficeKey: "spring-hill", OfficePhone: "+12025550100", SourceCallID: key, IdempotencyKey: key, Phone: phone, CallerName: "Synthetic Patient", Summary: "Request " + key, Message: "Synthetic request " + key, Category: category, Urgency: work.TaskUrgencyNormal})
		if err != nil {
			t.Fatal(err)
		}
		return task
	}
	const patient = "+12025550123"
	optical := create("optical", patient, work.TaskCategoryOptical)
	insurance := create("insurance", patient, work.TaskCategoryInsurance)
	otherPatient := create("other", "+12025550124", work.TaskCategoryOptical)
	reviewed := []work.ReviewedTask{{ID: optical.ID, ExpectedVersion: optical.Version}, {ID: insurance.ID, ExpectedVersion: insurance.Version}}

	if _, err := m.CompletePatientTasks(ctx, work.CompletePatientTasksCommand{Identity: identity, Phone: patient, Reason: work.CloseReasonDone,
		Tasks: append(reviewed, work.ReviewedTask{ID: otherPatient.ID, ExpectedVersion: otherPatient.Version})}); !errors.Is(err, work.ErrDenied) {
		t.Fatalf("closing another patient's Task = %v, want denied", err)
	}
	if _, err := m.CompletePatientTasks(ctx, work.CompletePatientTasksCommand{Identity: identity, Phone: patient, Reason: work.CloseReasonDone,
		Tasks: []work.ReviewedTask{{ID: optical.ID, ExpectedVersion: optical.Version + 1}, reviewed[1]}}); !errors.Is(err, work.ErrConflict) {
		t.Fatalf("stale version = %v, want conflict", err)
	}
	for _, task := range []work.Task{optical, insurance, otherPatient} {
		if current, err := m.ReadTask(ctx, identity, task.ID); err != nil || current.State != work.TaskOpen {
			t.Fatalf("a refused close changed %s: %+v %v", task.ID, current, err)
		}
	}

	closed, err := m.CompletePatientTasks(ctx, work.CompletePatientTasksCommand{Identity: identity, Phone: patient, Reason: work.CloseReasonNotNeeded, Tasks: reviewed})
	if err != nil || closed != 2 {
		t.Fatalf("close all = %d, %v", closed, err)
	}
	for _, task := range []work.Task{optical, insurance} {
		current, err := m.ReadTask(ctx, identity, task.ID)
		if err != nil || current.State != work.TaskCompleted || current.CloseReason != work.CloseReasonNotNeeded || current.CompletedBy.Subject != identity.Subject {
			t.Fatalf("closed Task = %+v %v", current, err)
		}
	}
	if current, _ := m.ReadTask(ctx, identity, otherPatient.ID); current.State != work.TaskOpen {
		t.Fatalf("another patient's Task closed")
	}
}
