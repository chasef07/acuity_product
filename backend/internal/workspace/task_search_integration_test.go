package workspace_test

import (
	"context"
	"sort"
	"testing"
	"time"
	"unicode"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
	"github.com/google/uuid"
)

const legacyTaskSearchSQL = `
	SELECT task.id::text
	FROM work_tasks task
	JOIN access_locations location
		ON location.practice_id = task.practice_id
		AND location.id = task.location_id
	WHERE task.practice_id = $1
		AND task.state = $2
		AND (
			$3 = ''
				OR strpos(lower(task.title), lower($3)) > 0
				OR strpos(lower(COALESCE(task.caller_name, '')), lower($3)) > 0
				OR strpos(lower(location.name), lower($3)) > 0
				OR strpos(lower(COALESCE(task.category, '')), lower($3)) > 0
				OR ($4 <> '' AND task.phone_digits LIKE '%' || $4 || '%')
		)`

func TestTaskSearchKeepsCaseInsensitiveSubstringSemantics(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	now := time.Date(2026, time.September, 10, 15, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	if _, err := accessModule.Provision(ctx, access.Provisioning{
		Environment: "test",
		RequestedBy: "workspace-task-search-test",
		Practices: []access.PracticeProvision{{
			Key:  "task-search-practice",
			Name: "Synthetic Search Practice",
			Locations: []access.LocationProvision{
				{Key: "task-search-north", Name: "North Synthetic Office", AbitaOfficeKeys: []string{"task-search-north"}},
				{Key: "task-search-south", Name: "South Synthetic Office", AbitaOfficeKeys: []string{"task-search-south"}},
			},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "task-search-staff",
				Email:         "staff@task-search.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}},
		}},
	}); err != nil {
		t.Fatalf("provision search fixture: %v", err)
	}
	identity := access.Identity{
		Subject:       "task-search-staff-subject",
		Email:         "staff@task-search.test",
		EmailVerified: true,
	}
	authorization := testaccess.Activate(t, accessModule, identity)
	practiceID := authorization.Practice.ID
	locations := map[string]string{}
	for _, location := range authorization.Locations {
		locations[location.Name] = location.ID
	}
	north, south := locations["North Synthetic Office"], locations["South Synthetic Office"]
	if north == "" || south == "" {
		t.Fatalf("fixture Locations = %+v", authorization.Locations)
	}

	insert := func(locationID, phone, title string, callerName *string, category, state string) string {
		t.Helper()
		id := uuid.NewString()
		var completedKind, completedSubject, completedEmail *string
		var completedAt *time.Time
		if state == "COMPLETED" {
			kind, subject, email := "HUMAN", identity.Subject, identity.Email
			completedKind, completedSubject, completedEmail, completedAt = &kind, &subject, &email, &now
		}
		if _, err := pool.Exec(ctx, `INSERT INTO work_tasks (
				id, practice_id, location_id, phone, title, caller_name, state,
				created_by_kind, created_by_subject, created_at, updated_at, origin,
				source_call_id, source_message, category, ai_idempotency_key, ai_input_fingerprint,
				completed_by_kind, completed_by_subject, completed_by_email, completed_at
			) VALUES (
				$1::uuid, $2, $3, $4, $5, $6, $7,
				'SERVICE', 'task-search-fixture', $8, $8, 'ABITA_AI',
				$1::text, 'Synthetic search evidence', $9, $1::text, decode(repeat('00', 32), 'hex'),
				$10, $11, $12, $13
			)`,
			id, practiceID, locationID, phone, title, callerName, state, now, category,
			completedKind, completedSubject, completedEmail, completedAt,
		); err != nil {
			t.Fatalf("insert search Task %q: %v", title, err)
		}
		now = now.Add(time.Minute)
		return id
	}
	name := func(value string) *string { return &value }

	refill := insert(north, "+15552223301", "Synthetic Refill request", nil, "medication", "OPEN")
	caller := insert(south, "+15552223302", "Synthetic callback", name("Jordan Sample"), "other", "OPEN")
	insurance := insert(south, "+15552223303", "Synthetic statement", nil, "insurance", "OPEN")
	phone := insert(south, "+15550107788", "Synthetic phone match", nil, "other", "OPEN")
	percent := insert(south, "+15552223304", "Copay 100% covered", nil, "other", "OPEN")
	percentDecoy := insert(south, "+15552223305", "Copay 1000 covered", nil, "other", "OPEN")
	underscore := insert(south, "+15552223306", "Form_A pending", nil, "other", "OPEN")
	underscoreDecoy := insert(south, "+15552223307", "FormBA pending", nil, "other", "OPEN")
	backslash := insert(south, "+15552223308", `Path C:\docs ready`, nil, "other", "OPEN")
	backslashDecoy := insert(south, "+15552223309", "Path C:docs ready", nil, "other", "OPEN")
	insert(south, "+15552223310", "Ärztin Rückruf", name("Zoë Ångström"), "other", "OPEN")
	completedRefill := insert(south, "+15552223311", "Completed REFILL request", nil, "medication", "COMPLETED")
	if _, err := pool.Exec(ctx, `
		WITH call AS (
			INSERT INTO human_calling_calls (practice_id, location_id, direction, entry_point, caller_phone, terminal_outcome, ended_at, created_at, updated_at)
			VALUES ($1, $2, 'INBOUND', 'STANDALONE', '+15552223312', 'RESOLVED', $3, $3, $3)
			RETURNING id
		)
		INSERT INTO work_tasks (practice_id, location_id, call_id, phone, title, state, origin, urgency, created_by_kind, created_by_subject, created_by_email, created_at, updated_at)
		SELECT $1, $2, call.id, '+15552223312', 'Synthetic uncategorized follow-up', 'OPEN', 'HUMAN_CALL_FOLLOW_UP', 'normal', 'HUMAN', 'task-search-staff-subject', 'staff@task-search.test', $3, $3
		FROM call
	`, practiceID, south, now); err != nil {
		t.Fatalf("insert uncategorized Task: %v", err)
	}

	reads := workspace.New(pool, accessModule)
	query := func(search string, state work.TaskState) ([]string, int) {
		t.Helper()
		page, err := reads.QueryTasks(ctx, workspace.QueryTasksCommand{
			Identity: identity, PracticeID: practiceID, Search: search, State: state,
			Ordering: work.TaskOrderingRecent,
		})
		if err != nil {
			t.Fatalf("search %q in %s: %v", search, state, err)
		}
		ids := make([]string, 0, len(page.Items))
		for _, item := range page.Items {
			ids = append(ids, item.ID)
		}
		sort.Strings(ids)
		return ids, page.Counts.Tasks
	}
	legacy := func(search string, state work.TaskState) []string {
		t.Helper()
		var digits []rune
		for _, character := range search {
			if unicode.IsDigit(character) {
				digits = append(digits, character)
			}
		}
		rows, err := pool.Query(ctx, legacyTaskSearchSQL, practiceID, string(state), search, string(digits))
		if err != nil {
			t.Fatalf("legacy search %q: %v", search, err)
		}
		defer rows.Close()
		ids := []string{}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				t.Fatal(err)
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		sort.Strings(ids)
		return ids
	}
	sorted := func(ids ...string) []string {
		sort.Strings(ids)
		return ids
	}

	for _, test := range []struct {
		search string
		state  work.TaskState
		want   []string
	}{
		{"REFILL", work.TaskOpen, sorted(refill)},
		{"refill", work.TaskCompleted, sorted(completedRefill)},
		{"jordan SAMPLE", work.TaskOpen, sorted(caller)},
		{"INSUR", work.TaskOpen, sorted(insurance)},
		{"north synthetic", work.TaskOpen, sorted(refill)},
		{"(555) 010-77", work.TaskOpen, sorted(phone)},
		{"0107788", work.TaskOpen, sorted(phone)},
		{"100%", work.TaskOpen, sorted(percent)},
		{"form_a", work.TaskOpen, sorted(underscore)},
		{`c:\docs`, work.TaskOpen, sorted(backslash)},
		{"%", work.TaskOpen, sorted(percent)},
		{"_", work.TaskOpen, sorted(underscore)},
		{`\`, work.TaskOpen, sorted(backslash)},
		{"%_\\", work.TaskOpen, []string{}},
		{"no synthetic match", work.TaskOpen, []string{}},
	} {
		got, total := query(test.search, test.state)
		if !equalIDs(got, test.want) || total != len(test.want) {
			t.Errorf("search %q in %s = %v (count %d), want %v", test.search, test.state, got, total, test.want)
		}
	}

	all, total := query("  ", work.TaskOpen)
	if len(all) != 12 || total != 12 {
		t.Fatalf("blank search returned %d Tasks (count %d), want every open Task", len(all), total)
	}
	for _, id := range []string{percentDecoy, underscoreDecoy, backslashDecoy} {
		if !containsID(all, id) {
			t.Fatalf("blank search omitted decoy Task %s", id)
		}
	}

	for _, search := range []string{
		"synthetic", "SYNTHETIC OFFICE", "office", "south", "uncategorized", "medication", "pre_op", "other",
		"5555", "+1 555 222", "3301", "1000", "100", "%", "_", `\`, "%%", "__", `\\`, "C:", ":", "a", "Ä",
		"ö", "copay 1", "pending", "fo", "x", "Rückruf", "ärztin", "ZOË", "ångström", "zoë ång",
	} {
		for _, state := range []work.TaskState{work.TaskOpen, work.TaskCompleted} {
			got, total := query(search, state)
			want := legacy(search, state)
			if !equalIDs(got, want) || total != len(want) {
				t.Errorf("search %q in %s = %v (count %d), legacy predicate = %v", search, state, got, total, want)
			}
		}
	}
}

func equalIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func containsID(ids []string, id string) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}
