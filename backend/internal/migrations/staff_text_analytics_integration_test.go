package migrations_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestStaffTextAnalyticsIndexUpgrade(t *testing.T) {
	pool := testdb.OpenThrough(t, "0064_preserve_historical_patient_classification.sql")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
 INSERT INTO access_practices(id,provisioning_key,name)
 VALUES('00000000-0000-0000-0000-000000000101','text-capacity','Synthetic Practice');
 INSERT INTO access_locations(id,practice_id,provisioning_key,name)
 VALUES('00000000-0000-0000-0000-000000000102','00000000-0000-0000-0000-000000000101','location','Synthetic Location');
 INSERT INTO messaging_threads(id,practice_id,location_id,office_phone,external_phone)
 VALUES('00000000-0000-0000-0000-000000000103','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000102','+15555550100','+15555550199');
 INSERT INTO messaging_messages(thread_id,practice_id,location_id,direction,body,sender,destination,delivery_state,created_by_kind,created_by_subject,created_at)
 SELECT '00000000-0000-0000-0000-000000000103','00000000-0000-0000-0000-000000000101',
 '00000000-0000-0000-0000-000000000102','OUTBOUND','Synthetic text','+15555550100','+15555550199',
 CASE WHEN i % 2 = 0 THEN 'SENT' ELSE 'DELIVERED' END,'HUMAN','synthetic-staff',
 CASE WHEN i <= 20000 THEN '2025-01-01'::timestamptz ELSE '2026-09-05'::timestamptz END
 FROM generate_series(1,20100) i;
 ANALYZE messaging_messages;
 `); err != nil {
		t.Fatal(err)
	}
	// Match the reporting query, including its UUID Location array and row limit.
	const query = `SELECT created_by_subject, count(*)
 FROM messaging_messages
 WHERE practice_id=$1::uuid AND location_id=ANY($2::uuid[])
  AND direction='OUTBOUND' AND created_by_kind='HUMAN'
  AND delivery_state IN ('SENT', 'DELIVERED')
  AND created_at >= $3 AND created_at < $4
 GROUP BY created_by_subject
 LIMIT 5001`
	args := []any{practiceID, []string{locationID},
		time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)}
	explain := func() string {
		t.Helper()
		rows, err := pool.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var lines []string
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, line)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return strings.Join(lines, "\n")
	}
	before := explain()
	t.Logf("Before migration, 20,100 retained texts:\n%s", before)
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	after := explain()
	t.Logf("After migration, same reporting query:\n%s", after)
	if !strings.Contains(after, "messaging_messages_staff_analytics_idx") || strings.Contains(after, "Seq Scan on messaging_messages") {
		t.Fatalf("reporting query did not use the scoped index:\n%s", after)
	}
	for _, condition := range []string{"practice_id =", "location_id = ANY", "created_at >=", "created_at <"} {
		if !strings.Contains(after, condition) {
			t.Errorf("query plan lacks reporting condition %q:\n%s", condition, after)
		}
	}
	var subject string
	var count int
	if err := pool.QueryRow(ctx, query, args...).Scan(&subject, &count); err != nil {
		t.Fatal(err)
	}
	if subject != "synthetic-staff" || count != 100 {
		t.Fatalf("reporting result = %q / %d, want synthetic-staff / 100", subject, count)
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}
