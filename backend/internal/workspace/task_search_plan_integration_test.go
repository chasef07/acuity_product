package workspace

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/jackc/pgx/v5"
)

func TestTaskSearchUsesTrigramIndexes(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	const practiceID = "10000000-0000-0000-0000-000000000001"
	if _, err := pool.Exec(ctx, `INSERT INTO access_practices(id,provisioning_key,name) VALUES($1,'task-search-plan','Synthetic Search Plan')`, practiceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO access_locations(id,practice_id,provisioning_key,name)
 SELECT md5(i::text)::uuid,$1,'location-'||i,'Synthetic Location '||i FROM generate_series(0,19) i`, practiceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `WITH calls AS (
 INSERT INTO human_calling_calls(practice_id,location_id,direction,entry_point,caller_phone,terminal_outcome,ended_at,created_at,updated_at)
 SELECT $1,md5((i%20)::text)::uuid,'INBOUND','STANDALONE','+1555'||lpad(i::text,7,'0'),'RESOLVED',
 '2026-09-01'::timestamptz+i*interval '1 second','2026-09-01'::timestamptz+i*interval '1 second','2026-09-01'::timestamptz+i*interval '1 second'
 FROM generate_series(1,20000) i RETURNING *
 ) INSERT INTO work_tasks(practice_id,location_id,call_id,phone,title,state,origin,urgency,created_by_kind,created_by_subject,created_by_email,created_at,updated_at)
 SELECT practice_id,location_id,id,caller_phone,
 CASE WHEN caller_phone LIKE '%00' THEN 'Synthetic needle request' ELSE 'Synthetic task '||md5(id::text) END,
 'OPEN','HUMAN_CALL_FOLLOW_UP','normal','HUMAN','synthetic-staff','staff@example.test',created_at,updated_at FROM calls`, practiceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ANALYZE work_tasks; ANALYZE access_locations`); err != nil {
		t.Fatal(err)
	}
	var locationIDs []string
	if err := pool.QueryRow(ctx, `SELECT array_agg(id::text) FROM access_locations WHERE practice_id=$1`, practiceID).Scan(&locationIDs); err != nil {
		t.Fatal(err)
	}

	for _, test := range []struct {
		search   string
		ordering work.TaskOrdering
		index    string
		rows     float64
	}{
		{"NEEDLE", work.TaskOrderingRecent, "work_tasks_title_search_idx", 51},
		{"needle request", work.TaskOrderingPriority, "work_tasks_title_search_idx", 51},
		{"+1 555 001 2345", work.TaskOrderingRecent, "work_tasks_phone_digits_search_idx", 1},
	} {
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
		if err != nil {
			t.Fatal(err)
		}
		search, err := resolveTaskSearch(ctx, tx, practiceID, locationIDs, test.search)
		if err != nil {
			t.Fatal(err)
		}
		var raw []byte
		if err := tx.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+taskQuerySQL(work.TaskOpen, test.ordering, false, search), search.arguments(
			practiceID, locationIDs, search.pattern, search.digits, false, time.Now(), "", 1, 51,
			"all", "synthetic@staff.test", "", "",
		)...).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		_ = tx.Rollback(ctx)
		var plans []map[string]any
		if err := json.Unmarshal(raw, &plans); err != nil {
			t.Fatal(err)
		}
		plan := plans[0]["Plan"].(map[string]any)
		indexes := map[string]bool{}
		var inspect func(map[string]any)
		inspect = func(node map[string]any) {
			if name, ok := node["Index Name"].(string); ok {
				indexes[name] = true
			}
			children, _ := node["Plans"].([]any)
			for _, child := range children {
				inspect(child.(map[string]any))
			}
		}
		inspect(plan)
		if !indexes[test.index] {
			t.Errorf("search %q did not use %s: indexes %v", test.search, test.index, indexes)
		}
		if plan["Actual Rows"] != test.rows {
			t.Errorf("search %q returned %v rows, want %v", test.search, plan["Actual Rows"], test.rows)
		}
		t.Logf("search %q over 20,000 Tasks: %.3fms using %v", test.search, plans[0]["Execution Time"], indexes)
	}
}
