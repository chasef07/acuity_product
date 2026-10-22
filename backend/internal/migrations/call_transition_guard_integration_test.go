package migrations_test

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var callLegStateOrder = []string{
	"PENDING", "DIALING", "RINGING", "ANSWERED", "BRIDGE_PENDING",
	"BRIDGED", "ENDING", "FAILED", "ENDED",
}

var callOutcomeMoves = map[string][]string{
	"":               {"ABANDONED", "ENDED", "MISSED", "ROUTING_FAILED", "UNANSWERED", "VOICEMAIL"},
	"ABANDONED":      {"ENDED"},
	"UNANSWERED":     {"ENDED"},
	"MISSED":         {"VOICEMAIL"},
	"ROUTING_FAILED": {"VOICEMAIL"},
	"ENDED":          {"RESOLVED", "FOLLOW_UP_REQUIRED"},
}

var callOutcomes = []string{
	"", "ABANDONED", "ENDED", "FOLLOW_UP_REQUIRED", "MISSED",
	"RESOLVED", "ROUTING_FAILED", "UNANSWERED", "VOICEMAIL",
}

var callDispositions = []string{
	"", "RESOLVED", "FOLLOW_UP_REQUIRED", "COMPLETE_TASK", "KEEP_OPEN",
	"CREATE_TASK", "NO_FOLLOW_UP",
}

func TestCallTransitionGuardWarnsOnEveryIllegalMoveWithoutBlockingIt(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	for constraint, guarded := range map[string][]string{
		"human_calling_call_legs_state_check":           callLegStateOrder,
		"human_calling_calls_terminal_outcome_check":    callOutcomes,
		"human_calling_calls_disposition_outcome_check": callDispositions,
	} {
		var definition string
		if err := db.Owner.QueryRow(ctx, `
			SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = $1
		`, constraint).Scan(&definition); err != nil {
			t.Fatalf("read %s: %v", constraint, err)
		}
		allowed := []string{}
		for _, match := range regexp.MustCompile(`'([A-Z_]+)'`).FindAllStringSubmatch(definition, -1) {
			allowed = append(allowed, match[1])
		}
		covered := slices.DeleteFunc(slices.Clone(guarded), func(value string) bool { return value == "" })
		slices.Sort(allowed)
		slices.Sort(covered)
		if !slices.Equal(allowed, covered) {
			t.Fatalf("%s allows %v but the transition rules cover %v", constraint, allowed, covered)
		}
	}
	var practiceID, locationID, callID string
	if err := db.Owner.QueryRow(ctx, `
		INSERT INTO access_practices (provisioning_key, name)
		VALUES ('transition-guard', 'Synthetic') RETURNING id::text
	`).Scan(&practiceID); err != nil {
		t.Fatal(err)
	}
	if err := db.Owner.QueryRow(ctx, `
		INSERT INTO access_locations (practice_id, provisioning_key, name)
		VALUES ($1, 'main', 'Main') RETURNING id::text
	`, practiceID).Scan(&locationID); err != nil {
		t.Fatal(err)
	}
	if err := db.Owner.QueryRow(ctx, `
		INSERT INTO human_calling_calls (practice_id, location_id, direction, entry_point)
		VALUES ($1, $2, 'INBOUND', 'STANDALONE') RETURNING id::text
	`, practiceID, locationID).Scan(&callID); err != nil {
		t.Fatal(err)
	}
	config := db.As(testdb.Worker).Config().ConnConfig
	var warnings []string
	config.OnNotice = func(_ *pgconn.PgConn, notice *pgconn.Notice) {
		if notice.Severity == "WARNING" {
			warnings = append(warnings, notice.Message)
		}
	}
	worker, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Close(context.Background()) })
	move := func(update string, id, to string, want string) {
		t.Helper()
		warnings = nil
		var stored string
		if err := worker.QueryRow(ctx, update, id, to).Scan(&stored); err != nil {
			t.Fatalf("%s -> %q: %v", id, to, err)
		}
		if stored != to {
			t.Fatalf("observe mode blocked %s -> %q, stored %q", id, to, stored)
		}
		var got string
		if len(warnings) > 0 {
			got = warnings[0]
		}
		if got != want || len(warnings) > 1 {
			t.Errorf("warnings = %q, want %q", warnings, want)
		}
	}

	sequence := 0
	for fromRank, from := range callLegStateOrder {
		for toRank, to := range callLegStateOrder {
			sequence++
			var legID string
			if err := db.Owner.QueryRow(ctx, `
				INSERT INTO human_calling_call_legs (call_id, role, sequence, state, ended_at)
				VALUES ($1, 'DESTINATION', $2, $3,
					CASE WHEN $3 IN ('ENDED', 'FAILED') THEN now() END)
				RETURNING id::text
			`, callID, sequence, from).Scan(&legID); err != nil {
				t.Fatal(err)
			}
			want := ""
			if toRank < fromRank {
				want = fmt.Sprintf("HC001 CallLeg %s cannot move from %s to %s", legID, from, to)
			}
			move(`
				UPDATE human_calling_call_legs
				SET state = $2, ended_at = CASE WHEN $2 IN ('ENDED', 'FAILED') THEN now() END
				WHERE id = $1 RETURNING state
			`, legID, to, want)
		}
	}

	shown := func(outcome string) string {
		if outcome == "" {
			return "NULL"
		}
		return outcome
	}
	for _, from := range callOutcomes {
		for _, to := range callOutcomes {
			var id string
			if err := db.Owner.QueryRow(ctx, `
				INSERT INTO human_calling_calls (
					practice_id, location_id, direction, entry_point,
					terminal_outcome, disposition_deadline
				) VALUES ($1, $2, 'INBOUND', 'STANDALONE', NULLIF($3, ''),
					CASE WHEN $3 = 'ENDED' THEN now() END)
				RETURNING id::text
			`, practiceID, locationID, from).Scan(&id); err != nil {
				t.Fatal(err)
			}
			want := ""
			if from != to && !slices.Contains(callOutcomeMoves[from], to) {
				want = fmt.Sprintf("HC002 Call %s cannot move terminal_outcome from %s to %s",
					id, shown(from), shown(to))
			}
			move(`
				UPDATE human_calling_calls
				SET terminal_outcome = NULLIF($2, ''),
					disposition_deadline = CASE WHEN $2 = 'ENDED' THEN now() END
				WHERE id = $1 RETURNING COALESCE(terminal_outcome, '')
			`, id, to, want)
		}
	}

	for _, from := range callDispositions {
		for _, to := range callDispositions {
			var id string
			if err := db.Owner.QueryRow(ctx, `
				INSERT INTO human_calling_calls (
					practice_id, location_id, direction, entry_point,
					terminal_outcome, disposition_outcome
				) VALUES ($1, $2, 'INBOUND', 'STANDALONE', 'RESOLVED', NULLIF($3, ''))
				RETURNING id::text
			`, practiceID, locationID, from).Scan(&id); err != nil {
				t.Fatal(err)
			}
			want := ""
			if from != "" && from != to {
				want = fmt.Sprintf("HC002 Call %s cannot move disposition_outcome from %s to %s",
					id, from, shown(to))
			}
			move(`
				UPDATE human_calling_calls SET disposition_outcome = NULLIF($2, '')
				WHERE id = $1 RETURNING COALESCE(disposition_outcome, '')
			`, id, to, want)
		}
	}
}
