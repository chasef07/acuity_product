package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"testing"
)

const dispositionClosurePhone = "+15555550100"

func (fixture *callingAccessFailureFixture) seedEndedInboundCall() string {
	fixture.t.Helper()
	ctx := context.Background()
	var callID string
	if err := fixture.pool.QueryRow(ctx, `
		INSERT INTO human_calling_calls (
			practice_id, location_id, direction, entry_point, caller_phone,
			terminal_outcome, ended_at, disposition_deadline, created_at, updated_at
		)
		VALUES ($1, $2, 'INBOUND', 'STANDALONE', $3, 'ENDED', $4, $4, $4, $4)
		RETURNING id::text
	`, fixture.authorization.Practice.ID, fixture.authorization.Locations[0].ID,
		dispositionClosurePhone, fixture.now()).Scan(&callID); err != nil {
		fixture.t.Fatalf("seed ended inbound Call: %v", err)
	}
	if _, err := fixture.pool.Exec(ctx, `
		INSERT INTO human_calling_call_legs (
			call_id, role, sequence, staff_subject, staff_session_id, state,
			answered_at, bridge_pending_at, bridged_at, ending_at, ended_at
		)
		VALUES
			($1, 'CALLER', 1, NULL, NULL, 'ENDED', $3, $3, $3, $3, $3),
			($1, 'STAFF', 2, $2, 'browser-a', 'ENDED', $3, $3, $3, $3, $3)
	`, callID, fixture.staff.Subject, fixture.now()); err != nil {
		fixture.t.Fatalf("seed ended CallLegs: %v", err)
	}
	return callID
}

func (fixture *callingAccessFailureFixture) seedOpenTask(phone, title string) string {
	fixture.t.Helper()
	var id string
	if err := fixture.pool.QueryRow(context.Background(), `
		INSERT INTO work_tasks (
			practice_id, location_id, phone, title, state, origin, urgency, category,
			created_by_kind, created_by_subject, created_at, updated_at,
			source_call_id, source_message, ai_idempotency_key, ai_input_fingerprint
		) VALUES (
			$1, $2, $3, $4::text, 'OPEN', 'ABITA_AI', 'normal', 'medication',
			'SERVICE', 'abita-synthetic', $5, $5, $4::text, 'Synthetic request.', $4::text,
			sha256(convert_to($4::text, 'UTF8'))
		)
		RETURNING id::text
	`, fixture.authorization.Practice.ID, fixture.authorization.Locations[0].ID,
		phone, title, fixture.now()).Scan(&id); err != nil {
		fixture.t.Fatalf("seed Task %q: %v", title, err)
	}
	return id
}

func (fixture *callingAccessFailureFixture) taskState(taskID string) string {
	fixture.t.Helper()
	var state string
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT state || ':' || COALESCE(close_reason, '') || ':' || COALESCE(closed_via_call_id::text, '')
		FROM work_tasks WHERE id = $1
	`, taskID).Scan(&state); err != nil {
		fixture.t.Fatalf("read Task: %v", err)
	}
	return state
}

func TestDispositionClosuresOverHTTP(t *testing.T) {
	fixture := newCallingAccessFailureFixture(t, httpCallingProvider{})
	if status, body := fixture.lease("staff-token", "browser-a"); status != http.StatusOK {
		t.Fatalf("acquire lease = %d %s", status, body)
	}
	refill := fixture.seedOpenTask(dispositionClosurePhone, "Refill request")
	insurance := fixture.seedOpenTask(dispositionClosurePhone, "Insurance question")
	otherPhone := fixture.seedOpenTask("+15555550177", "Other phone")
	dispose := func(callID string, closures any) (int, string) {
		body := map[string]any{"sessionId": "browser-a", "outcome": "RESOLVED"}
		if closures != nil {
			body["closures"] = closures
		}
		return fixture.call(http.MethodPost, "/v1/calling/calls/"+callID+"/disposition", "staff-token", body)
	}
	type closedTasks struct {
		ClosedTaskIDs        []string `json:"closedTaskIds"`
		AlreadyClosedTaskIDs []string `json:"alreadyClosedTaskIds"`
	}
	decode := func(label string, status int, body string) closedTasks {
		t.Helper()
		var result closedTasks
		if status != http.StatusOK {
			t.Fatalf("%s = %d %s", label, status, body)
		}
		if err := json.Unmarshal([]byte(body), &result); err != nil ||
			result.ClosedTaskIDs == nil || result.AlreadyClosedTaskIDs == nil {
			t.Fatalf("%s body = %s, %v", label, body, err)
		}
		return result
	}

	legacy := fixture.seedEndedInboundCall()
	status, body := dispose(legacy, nil)
	if result := decode("disposition without closures", status, body); len(result.ClosedTaskIDs) != 0 ||
		len(result.AlreadyClosedTaskIDs) != 0 {
		t.Fatalf("disposition without closures closed Tasks: %s", body)
	}
	if state := fixture.taskState(refill); state != "OPEN::" {
		t.Fatalf("disposition without closures left the Task %s", state)
	}

	callID := fixture.seedEndedInboundCall()
	for label, test := range map[string]struct {
		closure map[string]any
		status  int
		code    string
	}{
		"UNREACHABLE": {map[string]any{"taskId": refill, "reason": "UNREACHABLE"}, http.StatusBadRequest, "INVALID_REQUEST"},
		"EXPIRED":     {map[string]any{"taskId": refill, "reason": "EXPIRED"}, http.StatusBadRequest, "INVALID_REQUEST"},
		"other phone": {map[string]any{"taskId": otherPhone, "reason": "DONE"}, http.StatusBadRequest, "INVALID_REQUEST"},
	} {
		status, body := dispose(callID, []map[string]any{
			{"taskId": insurance, "reason": "DONE"}, test.closure,
		})
		fixture.expect(label+" closure", test.status, test.code, status, body)
		if state := fixture.taskState(insurance); state != "OPEN::" {
			t.Fatalf("%s closure wrote the valid Task: %s", label, state)
		}
	}

	closures := []map[string]any{
		{"taskId": refill, "reason": "DONE"},
		{"taskId": insurance, "reason": "NOT_NEEDED"},
	}
	for _, label := range []string{"disposition with closures", "replayed disposition"} {
		status, body = dispose(callID, closures)
		result := decode(label, status, body)
		if len(result.ClosedTaskIDs) != 2 || !slices.Contains(result.ClosedTaskIDs, refill) ||
			!slices.Contains(result.ClosedTaskIDs, insurance) || len(result.AlreadyClosedTaskIDs) != 0 {
			t.Fatalf("%s body = %s", label, body)
		}
		if state := fixture.taskState(refill); state != "COMPLETED:DONE:"+callID {
			t.Fatalf("%s left the first Task %s", label, state)
		}
		if state := fixture.taskState(insurance); state != "COMPLETED:NOT_NEEDED:"+callID {
			t.Fatalf("%s left the second Task %s", label, state)
		}
	}
	if state := fixture.taskState(otherPhone); state != "OPEN::" {
		t.Fatalf("closures reached another phone: %s", state)
	}
}
