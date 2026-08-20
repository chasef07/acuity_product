package migrations_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestWorkerCanQueueAutomaticTaskAcknowledgement(t *testing.T) {
	pool := testdb.Open(t)
	createDatabaseRoles(t, pool)
	applyDatabaseGrants(t, pool)

	ctx := context.Background()
	now := time.Date(2026, time.August, 20, 14, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, err := accessModule.Provision(ctx, access.Provisioning{
		Environment: "test",
		RequestedBy: "automatic-acknowledgement-worker-grant",
		Practices: []access.PracticeProvision{{
			Key:  "automatic-acknowledgement-worker-grant",
			Name: "Automatic Acknowledgement Worker Grant",
			Locations: []access.LocationProvision{{
				Key:             "main",
				Name:            "Main",
				AbitaOfficeKeys: []string{"automatic-acknowledgement-worker-office"},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision worker acknowledgement scope: %v", err)
	}
	var practiceID string
	if err := pool.QueryRow(ctx, `
		SELECT id::text FROM access_practices
		WHERE provisioning_key = 'automatic-acknowledgement-worker-grant'
	`).Scan(&practiceID); err != nil {
		t.Fatalf("read worker acknowledgement Practice: %v", err)
	}
	workModule := work.New(pool, accessModule, func() time.Time { return now })
	task, status, err := workModule.CreateAITask(ctx, work.CreateAITaskCommand{
		Service: access.ServiceIdentity{
			Subject:       "abita-worker-grant",
			PracticeID:    practiceID,
			LocationScope: access.LocationScopeAll,
			Capabilities:  []access.ServiceCapability{access.ServiceCapabilityCreateTask},
		},
		OfficeKey:      "automatic-acknowledgement-worker-office",
		OfficePhone:    "+17275550100",
		SourceCallID:   "automatic-acknowledgement-worker-call",
		IdempotencyKey: "automatic-acknowledgement-worker-task",
		Phone:          "+17275550199",
		Summary:        "Caller needs office follow-up",
		Message:        "Caller asked the office to return the call.",
		Category:       work.TaskCategoryOther,
		Urgency:        work.TaskUrgencyNormal,
	})
	if err != nil || status != work.TaskCreated {
		t.Fatalf("create worker acknowledgement Task = %#v, %q, %v", task, status, err)
	}
	ownerMessaging := messaging.New(pool, accessModule, workModule, nil, messaging.Config{}, func() time.Time { return now })
	if err := ownerMessaging.Provision(ctx, []messaging.LocationProvision{{
		PracticeKey:        "automatic-acknowledgement-worker-grant",
		LocationKey:        "main",
		Sender:             "+17275550100",
		MessagingProfileID: "automatic-acknowledgement-worker-profile",
	}}); err != nil {
		t.Fatalf("provision worker acknowledgement sender: %v", err)
	}

	config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("parse worker database config: %v", err)
	}
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, err := connection.Exec(ctx, "SET ROLE acuity_worker")
		return err
	}
	workerPool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("open worker pool: %v", err)
	}
	t.Cleanup(workerPool.Close)
	workerAccess := access.New(workerPool, func() time.Time { return now })
	workerModule := messaging.New(
		workerPool,
		workerAccess,
		work.New(workerPool, workerAccess, func() time.Time { return now }),
		nil,
		messaging.Config{},
		func() time.Time { return now },
	)

	queued, err := workerModule.QueueNextTaskAcknowledgement(ctx)
	if err != nil || !queued {
		t.Fatalf("queue automatic Task acknowledgement as acuity_worker = %t, %v", queued, err)
	}
}
