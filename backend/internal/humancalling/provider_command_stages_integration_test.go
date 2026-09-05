package humancalling_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/observability"
	"github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/jackc/pgx/v5"
)

func TestProviderCommandStagesSurviveProviderAndPersistenceFailures(t *testing.T) {
	for _, scenario := range []struct {
		name               string
		providerError      error
		persistenceFailure bool
		wantState          string
	}{
		{name: "success", wantState: "SENT"},
		{name: "provider_failure", providerError: humancalling.ErrDefinitiveProviderFailure, wantState: "FAILED"},
		{name: "persistence_failure", persistenceFailure: true, wantState: "SENDING"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			now := time.Now()
			pool, _, _, _ := prepareInboundFanout(t, now, "command-stage", &recordingProvider{}, 1)
			// Arrange a ringback that has become eligible before its scheduled Dial.
			// Once claimed, ordinary Call serialization must keep the Dial blocked.
			if _, err := pool.Exec(context.Background(), `UPDATE human_calling_provider_commands SET next_attempt_at = $1 WHERE action = 'DIAL_STAFF'`, now.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			var metrics bytes.Buffer
			observer := observability.NewLogger(observability.RuntimeWorker, "worker-stage-test", slog.New(slog.NewJSONHandler(&metrics, nil)))
			database := &stageFailingDatabase{Database: pool}
			provider := stageProvider{execute: func(context.Context, humancalling.ProviderCommand) (humancalling.ProviderResult, error) {
				now = now.Add(2 * time.Second)
				database.failBegin = scenario.persistenceFailure
				return humancalling.ProviderResult{}, scenario.providerError
			}}
			calling := humancalling.New(database, access.New(database, func() time.Time { return now }), provider, humancalling.Config{Observer: observer}, func() time.Time { return now })
			effect, claimed, err := calling.ClaimNextCommand(context.Background())
			if err != nil || !claimed {
				t.Fatalf("claim=%t error=%v", claimed, err)
			}
			initial := commandStageEntries(t, metrics.String())
			if len(initial) != 2 || initial["claim"]["outcome"] != "succeeded" || initial["created_to_first_claim"]["outcome"] != "succeeded" {
				t.Fatalf("claim metrics must precede execution: %#v", initial)
			}
			// Make the Dial eligible while ringback is SENDING, proving that readiness
			// is not inferred from next_attempt_at alone.
			if _, err := pool.Exec(context.Background(), `UPDATE human_calling_provider_commands SET next_attempt_at = $1 WHERE action = 'DIAL_STAFF'`, now); err != nil {
				t.Fatal(err)
			}
			beforeIdle := metrics.Len()
			if _, ok, err := calling.ClaimNextCommand(context.Background()); err != nil || ok {
				t.Fatalf("Dial bypassed active ringback: claimed=%t err=%v", ok, err)
			}
			if metrics.Len() != beforeIdle {
				t.Fatal("empty/blocked claim emitted idle metrics")
			}
			now = now.Add(3 * time.Second)
			executeErr := effect(context.Background())
			if (executeErr != nil) != scenario.persistenceFailure {
				t.Fatalf("execute error=%v", executeErr)
			}
			stages := commandStageEntries(t, metrics.String())
			if stages["claim_to_dispatch"]["seconds"] != float64(3) || stages["provider"]["seconds"] != float64(2) {
				t.Fatalf("stage boundaries=%#v", stages)
			}
			wantProvider := "succeeded"
			if scenario.providerError != nil {
				wantProvider = "failed"
			}
			wantPersist := "succeeded"
			if scenario.persistenceFailure {
				wantPersist = "failed"
			}
			if stages["provider"]["outcome"] != wantProvider || stages["persist"]["outcome"] != wantPersist {
				t.Fatalf("failure stages=%#v", stages)
			}
			var state string
			if err := pool.QueryRow(context.Background(), `SELECT state FROM human_calling_provider_commands WHERE action='START_RING_WINDOW'`).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if state != scenario.wantState {
				t.Fatalf("durable ringback=%s want %s", state, scenario.wantState)
			}
			database.failBegin = false
			// Reclaim a scheduled attempt using the original identity. It must never
			// publish another first-claim sample or conflate retry age with initial wait.
			if _, err := pool.Exec(context.Background(), `UPDATE human_calling_provider_commands SET state='PENDING',next_attempt_at=$1 WHERE action='START_RING_WINDOW'`, now); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(context.Background(), `UPDATE human_calling_provider_commands SET next_attempt_at=$1 WHERE action='DIAL_STAFF'`, now.Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			metrics.Reset()
			if _, ok, err := calling.ClaimNextCommand(context.Background()); err != nil || !ok {
				t.Fatalf("reclaim=%t err=%v", ok, err)
			}
			if _, ok := commandStageEntries(t, metrics.String())["created_to_first_claim"]; ok {
				t.Fatal("retry reported first-claim age")
			}
		})
	}
}

type stageProvider struct {
	execute func(context.Context, humancalling.ProviderCommand) (humancalling.ProviderResult, error)
}

func (provider stageProvider) Execute(ctx context.Context, command humancalling.ProviderCommand) (humancalling.ProviderResult, error) {
	return provider.execute(ctx, command)
}

type stageFailingDatabase struct {
	postgres.Database
	failBegin bool
}

func (database *stageFailingDatabase) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	if database.failBegin {
		return nil, errors.New("synthetic database interruption")
	}
	return database.Database.BeginTx(ctx, options)
}
func commandStageEntries(t *testing.T, logs string) map[string]map[string]any {
	t.Helper()
	result := make(map[string]map[string]any)
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		if entry["metric"] != "acuity_call_center_provider_command_stage" {
			continue
		}
		stage, _ := entry["stage"].(string)
		result[stage] = entry
	}
	return result
}
