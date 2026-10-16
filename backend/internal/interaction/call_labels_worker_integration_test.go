package interaction

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/jackc/pgx/v5/pgxpool"
)

type classificationFixture struct {
	pool                   *pgxpool.Pool
	practice, location, id string
	now                    time.Time
}

func newClassificationFixture(t *testing.T, closeout string) classificationFixture {
	t.Helper()
	f := classificationFixture{pool: testdb.Open(t), now: time.Now().UTC().Add(time.Second)}
	ctx := context.Background()
	if err := f.pool.QueryRow(ctx, `INSERT INTO access_practices(provisioning_key,name) VALUES('label-worker','Synthetic Practice') RETURNING id::text`).Scan(&f.practice); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `INSERT INTO access_locations(practice_id,provisioning_key,name) VALUES($1,'main','Main') RETURNING id::text`, f.practice).Scan(&f.location); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(ctx, `INSERT INTO ai_interactions(service_subject,practice_id,location_id,source_call_id,phone,office_phone,started_at,ended_at,status,lifecycle_stage,transcript,closeout_payload)
 VALUES('agent',$1,$2,'synthetic-source','+15555550101','+15555550102',$3,$4,'COMPLETED',3,'{"items":[{"type":"message","role":"user","content":["What time do you open?"]},{"type":"message","role":"assistant","content":["We open at nine."]}]}',$5) RETURNING id::text`, f.practice, f.location, f.now.Add(-time.Minute), f.now, []byte(closeout)).Scan(&f.id); err != nil {
		t.Fatal(err)
	}
	return f
}
func (f classificationFixture) worker(server *httptest.Server) *ClassificationWorker {
	classifier := NewCallLabelClassifier("synthetic-key")
	classifier.endpoint = server.URL
	worker := NewClassificationWorker(f.pool, classifier, humancalling.New(f.pool, nil, nil, humancalling.Config{}, func() time.Time { return f.now }), work.New(f.pool, nil, nil))
	worker.now = func() time.Time { return f.now }
	return worker
}
func labelTestServer(t *testing.T, intent, outcome string, inspect func(*http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if inspect != nil {
			inspect(r)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{"intent": choiceResponse(intent, intentCriteria), "outcome": choiceResponse(outcome, outcomeCriteria)}})
	}))
}
func (f classificationFixture) labels(t *testing.T) (string, string, string) {
	t.Helper()
	var intent, outcome, status string
	if err := f.pool.QueryRow(context.Background(), `SELECT COALESCE(intent,''),COALESCE(outcome,''),status FROM ai_interaction_classifications WHERE interaction_id=$1`, f.id).Scan(&intent, &outcome, &status); err != nil {
		t.Fatal(err)
	}
	return intent, outcome, status
}

func TestClassificationWorkerQueuesShortCallsAndSavesChoices(t *testing.T) {
	f := newClassificationFixture(t, `{"domainOutcomes":[]}`)
	server := labelTestServer(t, "office_information", "resolved", nil)
	defer server.Close()
	worker := f.worker(server)
	if done, err := worker.ProcessNextClassification(context.Background()); !done || err != nil {
		t.Fatalf("process=%v %v", done, err)
	}
	var version int64
	if err := f.pool.QueryRow(context.Background(), `SELECT workspace_version FROM access_practices WHERE id=$1`, f.practice).Scan(&version); err != nil || version != 2 {
		t.Fatalf("classification did not publish workspace change: %d %v", version, err)
	}
	if i, o, s := f.labels(t); i != "office_information" || o != "resolved" || s != "complete" {
		t.Fatalf("labels=%s %s %s", i, o, s)
	}
	if done, err := worker.ProcessNextClassification(context.Background()); done || err != nil {
		t.Fatalf("duplicate inference=%v %v", done, err)
	}
	if _, err := f.pool.Exec(context.Background(), `UPDATE ai_interactions SET closeout_payload='{"domainOutcomes":[],"changed":true}' WHERE id=$1`, f.id); err != nil {
		t.Fatal(err)
	}
	if i, o, s := f.labels(t); i != "" || o != "" || s != "pending" {
		t.Fatalf("stale labels=%s %s %s", i, o, s)
	}
}

func TestClassificationWorkerWaitsForTerminalScopedTransfer(t *testing.T) {
	f := newClassificationFixture(t, `{"transferStatus":"accepted"}`)
	ctx := context.Background()
	server := labelTestServer(t, "speak_to_staff", "transferred", func(r *http.Request) {
		var request struct{ State classificationEvidence }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if !request.State.StaffConnected || !strings.Contains(string(request.State.Handoffs), "bridgedAt") {
			t.Errorf("missing confirmed bridge: %+v", request.State)
		}
	})
	defer server.Close()
	worker := f.worker(server)
	run := func() (bool, error) {
		if _, err := f.pool.Exec(ctx, `UPDATE ai_interaction_classifications SET next_attempt_at=$2 WHERE interaction_id=$1`, f.id, f.now); err != nil {
			t.Fatal(err)
		}
		return worker.ProcessNextClassification(ctx)
	}
	if done, err := run(); !done || err != nil {
		t.Fatalf("missing handoff must wait=%v %v", done, err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO human_calling_handoffs(service_subject,practice_id,location_id,source_call_id,idempotency_key,input_fingerprint,expires_at)
 VALUES('other-agent',$1,$2,'synthetic-source','foreign-handoff',decode(repeat('00',32),'hex'),$3)`, f.practice, f.location, f.now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if done, err := run(); !done || err != nil {
		t.Fatalf("foreign handoff contaminated scope=%v %v", done, err)
	}
	var handoff, call string
	if err := f.pool.QueryRow(ctx, `INSERT INTO human_calling_handoffs(service_subject,practice_id,location_id,source_call_id,idempotency_key,input_fingerprint,expires_at)
 VALUES('agent',$1,$2,'synthetic-source','label-handoff',decode(repeat('00',32),'hex'),$3) RETURNING id::text`, f.practice, f.location, f.now.Add(time.Minute)).Scan(&handoff); err != nil {
		t.Fatal(err)
	}
	if done, err := run(); !done || err != nil {
		t.Fatalf("unconsumed handoff must wait=%v %v", done, err)
	}
	if err := f.pool.QueryRow(ctx, `INSERT INTO human_calling_calls(source_handoff_id,practice_id,location_id) VALUES($1,$2,$3) RETURNING id::text`, handoff, f.practice, f.location).Scan(&call); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `INSERT INTO human_calling_call_legs(call_id,role,sequence,staff_subject,state,answered_at,bridge_pending_at,bridged_at)
 VALUES($1,'STAFF',1,'synthetic-staff','BRIDGED',$2,$2,$2)`, call, f.now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if done, err := run(); !done || err != nil {
		t.Fatalf("active provider call must wait=%v %v", done, err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE human_calling_call_legs SET state='ENDED',ended_at=$2 WHERE call_id=$1`, call, f.now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE human_calling_calls SET ended_at=$2,terminal_outcome='RESOLVED' WHERE id=$1`, call, f.now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE ai_interaction_classifications SET next_attempt_at=$2 WHERE interaction_id=$1`, f.id, f.now); err != nil {
		t.Fatal(err)
	}
	if done, err := run(); !done || err != nil {
		t.Fatalf("terminal transfer=%v %v", done, err)
	}
	if i, o, s := f.labels(t); i != "speak_to_staff" || o != "transferred" || s != "complete" {
		t.Fatalf("labels=%s %s %s", i, o, s)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE human_calling_call_legs SET bridged_at=$2 WHERE call_id=$1`, call, f.now); err != nil {
		t.Fatal(err)
	}
	if _, o, s := f.labels(t); o != "" || s != "pending" {
		t.Fatalf("late facts retained outcome=%s %s", o, s)
	}
}

func TestClassificationWorkerIncludesOnlySavedServiceTasks(t *testing.T) {
	f := newClassificationFixture(t, `{}`)
	ctx := context.Background()
	for _, subject := range []string{"agent", "other-agent"} {
		if _, err := f.pool.Exec(ctx, `INSERT INTO work_tasks(practice_id,location_id,phone,title,state,origin,category,created_by_kind,created_by_subject,created_at,updated_at,source_call_id,source_message,ai_idempotency_key,ai_input_fingerprint)
 VALUES($1,$2,'+15555550101',$3,'OPEN','ABITA_AI','appointments','SERVICE',$3,$4,$4,'synthetic-source',$5,$3,decode(repeat('00',32),'hex'))`, f.practice, f.location, subject, f.now, "Saved next step for "+subject); err != nil {
			t.Fatal(err)
		}
	}
	server := labelTestServer(t, "existing_patient_booking", "resolved", func(r *http.Request) {
		var request struct{ State classificationEvidence }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if !strings.Contains(string(request.State.StaffTasks), "Saved next step for agent") || strings.Contains(string(request.State.StaffTasks), "other-agent") {
			t.Errorf("task source scope=%s", request.State.StaffTasks)
		}
	})
	defer server.Close()
	if done, err := f.worker(server).ProcessNextClassification(ctx); !done || err != nil {
		t.Fatalf("saved task classification=%v %v", done, err)
	}
}

func TestClassificationWorkerDoesNotHoldTransactionOrPublishStaleResponse(t *testing.T) {
	f := newClassificationFixture(t, `{}`)
	server := labelTestServer(t, "office_information", "resolved", func(r *http.Request) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if _, err := f.pool.Exec(ctx, `UPDATE ai_interactions SET transcript='{"items":[{"role":"user","content":["Updated conversation"]}]}' WHERE id=$1`, f.id); err != nil {
			t.Errorf("source blocked during inference: %v", err)
		}
	})
	defer server.Close()
	if done, err := f.worker(server).ProcessNextClassification(context.Background()); !done || err != nil {
		t.Fatalf("process=%v %v", done, err)
	}
	if i, o, s := f.labels(t); i != "" || o != "" || s != "pending" {
		t.Fatalf("stale response published=%s %s %s", i, o, s)
	}
}

func TestClassificationWorkerPersistsRecoverableFailuresAndSkipsMissingConversation(t *testing.T) {
	f := newClassificationFixture(t, `{}`)
	ctx := context.Background()
	worker := NewClassificationWorker(f.pool, NewCallLabelClassifier(""), humancalling.New(f.pool, nil, nil, humancalling.Config{}, func() time.Time { return f.now }), work.New(f.pool, nil, nil))
	worker.now = func() time.Time { return f.now }
	if done, err := worker.ProcessNextClassification(ctx); !done || err == nil {
		t.Fatalf("missing key=%v %v", done, err)
	}
	var attempts int
	var code string
	if err := f.pool.QueryRow(ctx, `SELECT attempts,error_code FROM ai_interaction_classifications WHERE interaction_id=$1`, f.id).Scan(&attempts, &code); err != nil || attempts != 1 || code != "openrouter_key_missing" {
		t.Fatalf("failure=%d %s %v", attempts, code, err)
	}
	if done, err := worker.ProcessNextClassification(ctx); done || err != nil {
		t.Fatalf("retry must back off=%v %v", done, err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE ai_interactions SET transcript='{"items":[]}' WHERE id=$1`, f.id); err != nil {
		t.Fatal(err)
	}
	if done, err := worker.ProcessNextClassification(ctx); !done || err != nil {
		t.Fatalf("missing conversation=%v %v", done, err)
	}
	if i, o, s := f.labels(t); i != "" || o != "" || s != "skipped" {
		t.Fatalf("missing evidence invented labels=%s %s %s", i, o, s)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE ai_interaction_classifications SET status='processing',attempts=3,claim_token=gen_random_uuid(),lease_expires_at=$2 WHERE interaction_id=$1`, f.id, f.now.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if done, err := worker.ProcessNextClassification(ctx); done || err != nil {
		t.Fatalf("exhausted recovery=%v %v", done, err)
	}
	if _, _, s := f.labels(t); s != "failed" {
		t.Fatalf("exhausted status=%s", s)
	}
}

func TestClassificationWorkerRetainsPartialLabelAcrossTransportFailure(t *testing.T) {
	f := newClassificationFixture(t, `{}`)
	ctx := context.Background()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) > 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"answers": map[string]any{
			"intent": choiceResponse("office_information", intentCriteria), "outcome": map[string]any{"type": "choice", "choice": "unknown"},
		}})
	}))
	defer server.Close()
	worker := f.worker(server)
	if done, err := worker.ProcessNextClassification(ctx); !done || err == nil {
		t.Fatalf("partial response=%v %v", done, err)
	}
	if i, o, s := f.labels(t); i != "office_information" || o != "" || s != "failed" {
		t.Fatalf("partial labels=%s %s %s", i, o, s)
	}
	worker.now = func() time.Time { return f.now.Add(time.Minute) }
	if done, err := worker.ProcessNextClassification(ctx); !done || err == nil {
		t.Fatalf("transport retry=%v %v", done, err)
	}
	if i, o, s := f.labels(t); i != "office_information" || o != "" || s != "failed" {
		t.Fatalf("transport erased labels=%s %s %s", i, o, s)
	}
	var count int
	var preserved string
	if err := f.pool.QueryRow(ctx, `SELECT jsonb_array_length(raw_judgments),raw_judgments#>>'{0,answers,intent,choice}' FROM ai_interaction_classifications WHERE interaction_id=$1`, f.id).Scan(&count, &preserved); err != nil || count != 1 || preserved != "office_information" {
		t.Fatalf("raw judgments lost=%d %s %v", count, preserved, err)
	}
}
