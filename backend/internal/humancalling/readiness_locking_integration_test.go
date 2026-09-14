package humancalling_test

import (
	"context"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

// Reproduce both production lock cycles: readiness locks the operator's
// Practices before waiting for a Call or lease, while call processing owns
// that row and advances the Practice workspace version before committing.
func TestOperatorReadinessDoesNotDeadlockWorkspaceChange(t *testing.T) {
	for _, lockedResource := range []string{"lease", "call"} {
		t.Run(lockedResource, func(t *testing.T) {
			pool := testdb.Open(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			now := time.Date(2026, time.September, 9, 18, 0, 0, 0, time.UTC)
			accessModule := access.New(pool, func() time.Time { return now })
			operator := access.Identity{
				Subject: "readiness-operator", Email: "readiness-operator@synthetic.test", EmailVerified: true,
			}
			if _, err := accessModule.Provision(ctx, access.Provisioning{
				Environment: "test", RequestedBy: "readiness-locking-test",
				PlatformOperators: []string{operator.Email},
				Practices: []access.PracticeProvision{{
					Key: "readiness-practice", Name: "Readiness Practice",
					Locations: []access.LocationProvision{{Key: "readiness-location", Name: "Readiness Location"}},
				}},
			}); err != nil {
				t.Fatal(err)
			}
			discovery, err := accessModule.DiscoverActor(ctx, operator)
			if err != nil {
				t.Fatal(err)
			}
			practice := discovery.Practices[0]
			calling := humancalling.New(pool, accessModule, &recordingProvider{}, humancalling.Config{}, func() time.Time { return now })
			readyConcurrentStaff(t, calling, []access.Identity{operator}, "readiness-browser")

			var callID string
			if lockedResource == "call" {
				if err := pool.QueryRow(ctx, `
					INSERT INTO human_calling_calls (practice_id, location_id, direction, entry_point)
					VALUES ($1, $2, 'INBOUND', 'STANDALONE') RETURNING id::text
				`, practice.ID, practice.Locations[0].ID).Scan(&callID); err != nil {
					t.Fatal(err)
				}
				if _, err := pool.Exec(ctx, `
					INSERT INTO human_calling_call_legs (call_id, role, sequence, staff_subject, staff_session_id, state)
					VALUES ($1, 'STAFF', 1, $2, 'readiness-browser-1', 'RINGING')
				`, callID, operator.Subject); err != nil {
					t.Fatal(err)
				}
			}

			workerTx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = workerTx.Rollback(context.Background()) }()
			if lockedResource == "call" {
				_, err = workerTx.Exec(ctx, `SELECT id FROM human_calling_calls WHERE id = $1 FOR UPDATE`, callID)
			} else {
				_, err = workerTx.Exec(ctx, `SELECT user_subject FROM human_calling_softphone_leases WHERE user_subject = $1 FOR UPDATE`, operator.Subject)
			}
			if err != nil {
				t.Fatal(err)
			}
			observer, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer observer.Release()
			readinessDone := make(chan error, 1)
			go func() {
				_, err := calling.SetReadiness(ctx, humancalling.ReadinessCommand{
					Identity: operator, SessionID: "readiness-browser-1", Registered: true,
					MicrophoneReady: true, AudioReady: true, SessionHealthy: true, Available: true,
				})
				readinessDone <- err
			}()
			waitForPostgresLockWaiter(t, observer, "transactionid", int32(workerTx.Conn().PgConn().PID()))
			version, workerErr := accessModule.RecordWorkspaceChange(ctx, workerTx, practice.ID)
			if workerErr == nil {
				workerErr = workerTx.Commit(ctx)
			} else {
				_ = workerTx.Rollback(ctx)
			}
			select {
			case readinessErr := <-readinessDone:
				if workerErr != nil || readinessErr != nil {
					t.Fatalf("concurrent workspace change/readiness failed: workspace=%v readiness=%v", workerErr, readinessErr)
				}
			case <-ctx.Done():
				t.Fatal("workspace change and readiness did not complete")
			}
			var persistedVersion int64
			if err := observer.QueryRow(ctx, `SELECT workspace_version FROM access_practices WHERE id = $1`, practice.ID).Scan(&persistedVersion); err != nil {
				t.Fatal(err)
			}
			if persistedVersion != version {
				t.Fatalf("workspace version = %d, want committed %d", persistedVersion, version)
			}
		})
	}
}
