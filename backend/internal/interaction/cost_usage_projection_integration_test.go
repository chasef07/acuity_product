package interaction

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestCostUsageProjectionPreservesPricingAndCorrections(t *testing.T) {
	ctx := context.Background()
	pool := testdb.OpenThrough(t, "0057_call_leg_reconciliation_schedule.sql")
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	var practiceID, locationID string
	if err := pool.QueryRow(ctx, `INSERT INTO access_practices(provisioning_key,name) VALUES('cost-projection','Synthetic Cost Projection') RETURNING id::text`).Scan(&practiceID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO access_locations(practice_id,provisioning_key,name) VALUES($1,'main','Main') RETURNING id::text`, practiceID).Scan(&locationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO access_platform_operators(email,user_subject) VALUES('operator@example.test','operator')`); err != nil {
		t.Fatal(err)
	}
	cases := []string{
		`null`, `{}`, `[]`,
		`[null,4,"Synthetic invalid entry",[],{}]`,
		`[{"type":"llm_usage","provider":"livekit","model":"google/gemma-4-31b-it","input_tokens":1000000,"input_cached_tokens":250000,"output_tokens":100000}, {"type":"stt_usage","provider":"livekit","model":"assemblyai/universal-3-5-pro","audio_duration":600}, {"type":"tts_usage","provider":"rime","model":"coda","characters_count":10000}]`,
		`[{"type":"llm_usage","provider":"google","model":"google/gemma_4_31b_it","inputTokens":1000000,"inputCachedTokens":250000,"outputTokens":100000}, {"type":"stt_usage","provider":"assemblyai","model":"universal-3.5-pro","audioDurationMs":600000}, {"type":"tts_usage","provider":"livekit","model":"rime/coda","charactersCount":10000}]`,
		`[{"type":"llm_usage","provider":"unknown","model":"FallbackAdapter","input_tokens":1000000}, {"type":"llm_usage","provider":"google","model":"google/gemma-4-31b-it"}]`,
		`[{"type":"llm_usage","provider":"different","model":"unpriced","input_tokens":4}, {"type":"tts_usage","provider":"rime","model":"different","characters_count":10}]`,
		`[{"type":"tts_usage","provider":"rime","model":"coda","characters_count":null,"charactersCount":10}, {"type":"stt_usage","provider":"assemblyai","model":"universal-3.5-pro","audio_duration":"Synthetic invalid quantity","audioDurationMs":600000}]`,
		`[{"type":"llm_usage","provider":"google","model":"google/gemma-4-31b-it","input_tokens":20,"input_cached_tokens":30}, {"type":"tts_usage","provider":"rime","model":"coda","characters_count":-1}]`,
		`[{"type":"tts_usage","provider":{"text":"Synthetic nested payload"},"model":"coda","characters_count":{"text":"Synthetic nested payload"},"text":"Synthetic extra content"}, {"type":"ignored","audio_duration":0}]`,
	}
	ids := make([]string, len(cases))
	for i, usage := range cases {
		transcript := `{"items":[{"content":"Synthetic conversation"}],"usage":` + usage + `}`
		if err := pool.QueryRow(ctx, `INSERT INTO ai_interactions(service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,transcript) VALUES('agent',$1,$2,$3,'+15555550101','+15555550102',$4,$5,'COMPLETED',3,$6) RETURNING id::text`, practiceID, locationID, fmt.Sprint(i), now.Add(-time.Hour), now.Add(-50*time.Minute), []byte(transcript)).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrations.ApplyThrough(ctx, pool, "0058_compact_ai_cost_usage.sql"); err != nil {
		t.Fatal(err)
	}
	module := New(pool, access.New(pool, func() time.Time { return now }), func() time.Time { return now })
	command := QueryCostAnalyticsCommand{Identity: access.Identity{Subject: "operator", Email: "operator@example.test", EmailVerified: true}, PracticeID: practiceID, Range: AnalyticsRange24Hours, TimeZone: "UTC"}
	if _, err := module.QueryCostAnalytics(ctx, command); err == nil || !strings.Contains(err.Error(), "backfill is incomplete") {
		t.Fatalf("incomplete projection must fail visibly: %v", err)
	}
	if err := migrations.ApplyThrough(ctx, pool, "0059_backfill_ai_cost_usage.sql"); err != nil {
		t.Fatal(err)
	}
	for i, usage := range cases {
		check := func(usage string) {
			t.Helper()
			var projected json.RawMessage
			if err := pool.QueryRow(ctx, `SELECT cost_usage_evidence FROM ai_interactions WHERE id=$1`, ids[i]).Scan(&projected); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(projected), "Synthetic") {
				t.Fatalf("projection retained non-usage payload: %s", projected)
			}
			original := newCostAnalytics(now.Add(-24*time.Hour), now, time.UTC)
			compact := newCostAnalytics(now.Add(-24*time.Hour), now, time.UTC)
			original.addCall(now.Add(-time.Hour), now.Add(-50*time.Minute), json.RawMessage(usage), time.UTC)
			compact.addCall(now.Add(-time.Hour), now.Add(-50*time.Minute), projected, time.UTC)
			original.finalize()
			compact.finalize()
			if !reflect.DeepEqual(original, compact) {
				t.Fatalf("case %d changed cost semantics\noriginal=%+v\ncompact=%+v", i, original, compact)
			}
		}
		check(usage)
		corrected := `[{"type":"tts_usage","provider":"rime","model":"coda","characters_count":250}]`
		for range 2 {
			if _, err := pool.Exec(ctx, `UPDATE ai_interactions SET transcript=jsonb_build_object('usage',$2::jsonb) WHERE id=$1`, ids[i], []byte(corrected)); err != nil {
				t.Fatal(err)
			}
			check(corrected)
		}
	}
	if _, err := module.QueryCostAnalytics(ctx, command); err != nil {
		t.Fatalf("completed projection unavailable: %v", err)
	}
}
