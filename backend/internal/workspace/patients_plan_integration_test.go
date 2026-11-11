package workspace

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestPatientWorkQueriesDoNotScanEveryTask(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	ctx := context.Background()
	const practiceID = "10000000-0000-0000-0000-000000000002"
	const otherPracticeID = "10000000-0000-0000-0000-000000000003"
	if _, err := pool.Exec(ctx, `INSERT INTO access_practices(id,provisioning_key,name) VALUES
 ($1,'patient-work-plan','Synthetic Patient Plan'),($2,'patient-work-plan-other','Synthetic Other Practice')`, practiceID, otherPracticeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO access_locations(id,practice_id,provisioning_key,name)
 SELECT md5(practice.id::text||i::text)::uuid,practice.id,'location-'||i,'Synthetic Location '||i
 FROM access_practices practice CROSS JOIN generate_series(0,5) i WHERE practice.id IN ($1,$2)`, practiceID, otherPracticeID); err != nil {
		t.Fatal(err)
	}
	const open = `(practice_id=$1 AND right(caller_phone,1) IN ('0','1'))`
	if _, err := pool.Exec(ctx, `WITH calls AS (
 INSERT INTO human_calling_calls(practice_id,location_id,direction,entry_point,caller_phone,terminal_outcome,ended_at,created_at,updated_at)
 SELECT practice.id,md5(practice.id::text||(i%6)::text)::uuid,'INBOUND','STANDALONE','+1555'||lpad((i%9000)::text,7,'0'),'RESOLVED',
 '2026-09-01'::timestamptz+i*interval '1 second','2026-09-01'::timestamptz+i*interval '1 second','2026-09-01'::timestamptz+i*interval '1 second'
 FROM access_practices practice CROSS JOIN generate_series(1,30000) i WHERE practice.id IN ($1,$2) RETURNING *
 ) INSERT INTO work_tasks(practice_id,location_id,call_id,phone,title,state,origin,urgency,created_by_kind,created_by_subject,created_by_email,
 completed_by_kind,completed_by_subject,completed_by_email,completed_at,close_reason,created_at,updated_at)
 SELECT practice_id,location_id,id,caller_phone,'Synthetic task '||md5(id::text),
 CASE WHEN `+open+` THEN 'OPEN' ELSE 'COMPLETED' END,
 'HUMAN_CALL_FOLLOW_UP','normal','HUMAN','synthetic-staff','staff@example.test',
 CASE WHEN `+open+` THEN NULL ELSE 'HUMAN' END,
 CASE WHEN `+open+` THEN NULL ELSE 'synthetic-staff' END,
 CASE WHEN `+open+` THEN NULL ELSE 'staff@example.test' END,
 CASE WHEN `+open+` THEN NULL ELSE created_at END,
 CASE WHEN `+open+` THEN NULL ELSE 'DONE' END,
 created_at,updated_at FROM calls`, practiceID, otherPracticeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ANALYZE work_tasks; ANALYZE access_locations`); err != nil {
		t.Fatal(err)
	}
	var locationIDs []string
	var openTasks, patients int
	if err := pool.QueryRow(ctx, `SELECT array_agg(id::text) FROM access_locations WHERE practice_id=$1`, practiceID).Scan(&locationIDs); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*), count(DISTINCT phone) FROM work_tasks WHERE practice_id=$1 AND state='OPEN'`, practiceID).Scan(&openTasks, &patients); err != nil {
		t.Fatal(err)
	}
	if openTasks < 3000 {
		t.Fatalf("fixture has %d open Tasks, want at least 3,000", openTasks)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, test := range []struct {
		name string
		sql  string
		args []any
		rows float64
	}{
		{
			"page",
			patientWorkPageSQL(PatientWorkMine, PatientWorkAll, taskSearch{}),
			[]any{practiceID, locationIDs, "", "", "synthetic@staff.test", false, false, time.Unix(0, 0), "", 51},
			51,
		},
		{
			"summary",
			patientWorkSummarySQL(PatientWorkMine, taskSearch{}),
			[]any{practiceID, locationIDs, "", "", "synthetic@staff.test"},
			1,
		},
	} {
		var raw []byte
		if err := tx.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+test.sql, test.args...).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var plans []map[string]any
		if err := json.Unmarshal(raw, &plans); err != nil {
			t.Fatal(err)
		}
		t.Logf("patient work %s over %d open Tasks for %d patients among 60,000 Tasks: %.3fms", test.name, openTasks, patients, plans[0]["Execution Time"])
		plan := plans[0]["Plan"].(map[string]any)
		var inspect func(map[string]any)
		inspect = func(node map[string]any) {
			if node["Node Type"] == "Seq Scan" && node["Relation Name"] == "work_tasks" {
				t.Errorf("patient work %s scans every Task sequentially: %v", test.name, node)
			}
			children, _ := node["Plans"].([]any)
			for _, child := range children {
				inspect(child.(map[string]any))
			}
		}
		inspect(plan)
		if plan["Actual Rows"] != test.rows {
			t.Errorf("patient work %s returned %v rows, want %v", test.name, plan["Actual Rows"], test.rows)
		}
	}
}
