package workspace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
)

func TestTaskLocationFilterUsesScopedIndex(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	const practiceID = "10000000-0000-0000-0000-000000000001"
	const locationID = "c4ca4238-a0b9-2382-0dcc-509a6f75849b"
	_, err := pool.Exec(ctx, `
 INSERT INTO access_practices(id,provisioning_key,name) VALUES($1,'task-capacity','Synthetic Task Capacity');
 `, practiceID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO access_locations(id,practice_id,provisioning_key,name)
 SELECT md5(i::text)::uuid,$1,'location-'||i,'Synthetic Location '||i FROM generate_series(0,19) i`, practiceID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `WITH calls AS (
 INSERT INTO human_calling_calls(practice_id,location_id,direction,entry_point,caller_phone,terminal_outcome,ended_at,created_at,updated_at)
 SELECT $1,md5((i%20)::text)::uuid,'INBOUND','STANDALONE','+15555550100','RESOLVED',
 '2026-09-01'::timestamptz+i*interval '1 second','2026-09-01'::timestamptz+i*interval '1 second','2026-09-01'::timestamptz+i*interval '1 second'
 FROM generate_series(1,20000) i RETURNING *
 ) INSERT INTO work_tasks(practice_id,location_id,call_id,phone,title,state,origin,urgency,created_by_kind,created_by_subject,created_by_email,created_at,updated_at)
 SELECT practice_id,location_id,id,'+15555550100','Synthetic task '||id,'OPEN','HUMAN_CALL_FOLLOW_UP','normal','HUMAN','synthetic-staff','staff@example.test',created_at,updated_at FROM calls`, practiceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ANALYZE work_tasks; ANALYZE access_locations`); err != nil {
		t.Fatal(err)
	}
	query := taskQuerySQL(work.TaskOpen, work.TaskOrderingRecent)
	args := []any{practiceID, []string{locationID}, "", "", false, time.Now(), "", 1, 51, "synthetic-staff", "work"}
	explain := func(sql string) (float64, map[string]any) {
		t.Helper()
		var raw []byte
		if err := pool.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+sql, args...).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var plans []map[string]any
		if err := json.Unmarshal(raw, &plans); err != nil {
			t.Fatal(err)
		}
		return plans[0]["Execution Time"].(float64), plans[0]["Plan"].(map[string]any)
	}
	before, _ := explain(strings.Replace(query, "task.location_id = ANY($2::uuid[])", "task.location_id::text = ANY($2::text[])", 1))
	after, plan := explain(query)
	var scoped bool
	var inspect func(map[string]any)
	inspect = func(node map[string]any) {
		if condition, ok := node["Index Cond"].(string); ok && strings.Contains(condition, "location_id") {
			scoped = true
		}
		children, _ := node["Plans"].([]any)
		for _, child := range children {
			inspect(child.(map[string]any))
		}
	}
	inspect(plan)
	if !scoped {
		t.Fatalf("Task query failed to constrain an index by Location: %+v", plan)
	}
	if plan["Actual Rows"] != float64(51) {
		t.Fatalf("Task query returned %v rows, want51", plan["Actual Rows"])
	}
	t.Logf("20,000 Tasks across20Locations, actual queue query: text predicate %.3fms; UUID predicate %.3fms", before, after)
}
