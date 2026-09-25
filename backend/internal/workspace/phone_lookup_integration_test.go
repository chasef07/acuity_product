package workspace

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestPhoneLookupPreservesResultsAndUsesIndexes(t *testing.T) {
	pool := testdb.OpenThrough(t, "0076_backfill_gpt_live_cost_usage.sql")
	ctx := context.Background()
	const practice = "10000000-0000-0000-0000-000000000001"
	const location = "20000000-0000-0000-0000-000000000001"
	const hidden = "20000000-0000-0000-0000-000000000002"
	const otherPractice = "10000000-0000-0000-0000-000000000002"
	const otherLocation = "20000000-0000-0000-0000-000000000003"
	const phone = "+15555550100"
	_, err := pool.Exec(ctx, `
 INSERT INTO access_practices(id,provisioning_key,name) VALUES
 ($1,'phone-lookup','Synthetic Phone Lookup'),($2,'other-phone-lookup','Other Synthetic Practice');
 `, practice, otherPractice)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO access_locations(id,practice_id,provisioning_key,name) VALUES
 ($2,$1,'visible','Visible'),($3,$1,'hidden','Hidden'),($5,$4,'other','Other')`, practice, location, hidden, otherPractice, otherLocation)
	if err != nil {
		t.Fatal(err)
	}
	// Enough unrelated history to expose a scan instead of a selective phone seek.
	_, err = pool.Exec(ctx, `INSERT INTO human_calling_handoffs
 (id,service_subject,practice_id,location_id,source_call_id,idempotency_key,input_fingerprint,phone,expires_at)
 SELECT md5('handoff-'||i)::uuid,'synthetic',$1,$2,i::text,i::text,'x'::bytea,
 '+1556'||lpad(i::text,7,'0'),now()+interval '1 hour' FROM generate_series(1,20000) i`, practice, location)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO human_calling_calls
 (id,practice_id,location_id,source_handoff_id,direction,entry_point,destination_phone,created_at,updated_at)
 SELECT md5('call-'||i)::uuid,$1,$2,CASE WHEN i<=20000 THEN md5('handoff-'||i)::uuid END,
 CASE WHEN i<=20000 THEN 'INBOUND' ELSE 'OUTBOUND' END,'STANDALONE',
 CASE WHEN i>20000 THEN '+1556'||lpad(i::text,7,'0') END,
 '2026-09-01'::timestamptz+i*interval '1 second','2026-09-01'::timestamptz+i*interval '1 second'
 FROM generate_series(1,40000) i`, practice, location)
	if err != nil {
		t.Fatal(err)
	}
	// Includes handoff precedence, NULL fallback, no handoff, no phone, and both scope boundaries.
	_, err = pool.Exec(ctx, `
 UPDATE human_calling_handoffs SET phone=CASE WHEN source_call_id IN ('3','7') THEN NULL ELSE $1 END
 WHERE source_call_id IN ('1','2','3','4','5','7');
 `, phone)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `UPDATE human_calling_calls SET destination_phone=$1
 WHERE id IN (md5('call-2')::uuid,md5('call-3')::uuid,md5('call-6')::uuid,md5('call-20001')::uuid)`, phone)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `UPDATE human_calling_calls SET location_id=$1 WHERE id=md5('call-4')::uuid`, hidden)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `UPDATE human_calling_calls SET practice_id=$1,location_id=$2 WHERE id=md5('call-5')::uuid`, otherPractice, otherLocation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ANALYZE human_calling_calls; ANALYZE human_calling_handoffs; ANALYZE access_locations`); err != nil {
		t.Fatal(err)
	}
	args := []any{practice, []string{location}, phone}
	const legacy = `SELECT call.id FROM human_calling_calls call
 LEFT JOIN human_calling_handoffs handoff ON handoff.id=call.source_handoff_id
 WHERE call.practice_id=$1 AND call.location_id=ANY($2::uuid[])
 AND COALESCE(handoff.phone, call.destination_phone) = $3`
	readIDs := func(query string) []string {
		t.Helper()
		rows, err := pool.Query(ctx, "SELECT id::text FROM ("+query+") matches ORDER BY id", args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var ids []string
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
		return ids
	}
	before := readIDs(legacy)
	if len(before) != 4 {
		t.Fatalf("legacy returned %d calls, want 4", len(before))
	}
	if got := readIDs(phoneCallIDsSQL); !reflect.DeepEqual(before, got) {
		t.Fatalf("phone precedence/scope changed: %v != %v", got, before)
	}
	type measurement struct {
		milliseconds float64
		blocks       float64
		plan         string
	}
	explain := func(query string, parameters []any) measurement {
		t.Helper()
		var raw []byte
		if err := pool.QueryRow(ctx, "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) "+query, parameters...).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var plans []struct {
			ExecutionTime float64 `json:"Execution Time"`
			Plan          map[string]any
		}
		if err := json.Unmarshal(raw, &plans); err != nil {
			t.Fatal(err)
		}
		hits, _ := plans[0].Plan["Shared Hit Blocks"].(float64)
		reads, _ := plans[0].Plan["Shared Read Blocks"].(float64)
		return measurement{plans[0].ExecutionTime, hits + reads, string(raw)}
	}
	oldPredicate := "COALESCE(handoff.phone, call.destination_phone) = $3"
	newPredicate := "call.id IN (" + phoneCallIDsSQL + ")"
	cases := []struct {
		name, sql string
		args      []any
	}{
		{"call history roots", phoneHistoryRootsSQL, append(append([]any{}, args...), nil, nil, 51)},
		{"timeline call projection", callProjectionSQL, append(append([]any{}, args...), nil, nil, 51, true, nil)},
	}
	baselines := make([]measurement, len(cases))
	for i, c := range cases {
		baselines[i] = explain(strings.ReplaceAll(c.sql, newPredicate, oldPredicate), c.args)
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	if got := readIDs(phoneCallIDsSQL); !reflect.DeepEqual(before, got) {
		t.Fatalf("indexed lookup changed results: %v", got)
	}
	indexed := explain(phoneCallIDsSQL, args)
	for _, index := range []string{"human_calling_handoffs_phone_lookup_idx", "human_calling_calls_destination_lookup_idx"} {
		if !strings.Contains(indexed.plan, index) {
			t.Fatalf("lookup did not use %s: %s", index, indexed.plan)
		}
	}
	for i, c := range cases {
		after := explain(c.sql, c.args)
		t.Logf("40,000 calls, %s: before %.3fms / %.0f buffers; after %.3fms / %.0f buffers", c.name, baselines[i].milliseconds, baselines[i].blocks, after.milliseconds, after.blocks)
		if after.blocks >= baselines[i].blocks {
			t.Fatalf("%s did not reduce buffer work: %s", c.name, after.plan)
		}
	}
	// Compare full projections, including pagination, rather than just matching IDs.
	for _, c := range cases {
		read := func(sql string) string {
			t.Helper()
			var value string
			if err := pool.QueryRow(ctx, "SELECT coalesce(json_agg(result)::text,'[]') FROM ("+sql+") result", c.args...).Scan(&value); err != nil {
				t.Fatal(err)
			}
			return value
		}
		if old, new := read(strings.ReplaceAll(c.sql, newPredicate, oldPredicate)), read(c.sql); old != new {
			t.Fatalf("%s projection changed", c.name)
		}
	}
}
