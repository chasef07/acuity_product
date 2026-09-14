package interaction

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestAppointmentReceiptCommitsReviewWithSourceFacts(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	var practice, location string
	if err := pool.QueryRow(ctx, `INSERT INTO access_practices(provisioning_key,name) VALUES('review-receipt','Synthetic') RETURNING id::text`).Scan(&practice); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO access_locations(practice_id,provisioning_key,name) VALUES($1,'main','Main') RETURNING id::text`, practice).Scan(&location); err != nil {
		t.Fatal(err)
	}
	payload := storedReceiptPayload{Kind: MessageCloseout, SourceCallID: "synthetic-review-call", CallerPhone: "+15555550123", OfficePhone: "+15555550100", StartedAt: now.Add(-time.Hour), EndedAt: &now, Status: CallCompleted, Transcript: json.RawMessage(`{"items":[]}`), CloseoutPayload: json.RawMessage(`{}`), Appointment: &AppointmentEvidence{Action: AppointmentBooked, OccurredAt: now, NewAppointmentID: "synthetic-appointment", BookingResult: json.RawMessage(`{"status":"booked","appointmentId":"synthetic-appointment","providerName":"Synthetic Provider","appointmentDate":"2026-10-01"}`)}}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint := sha256.Sum256(raw)
	if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_receipts(service_subject,practice_id,location_id,source_call_id,kind,payload_fingerprint,payload) VALUES('synthetic',$1,$2,'synthetic-review-call','CLOSEOUT',$3,$4)`, practice, location, fingerprint[:], raw); err != nil {
		t.Fatal(err)
	}
	module := New(pool, access.New(pool, nil), func() time.Time { return now })
	if processed, err := module.ProcessNextReceipt(ctx); err != nil || !processed {
		t.Fatalf("project receipt: %v %v", processed, err)
	}
	var body, source, state string
	var count int
	if err := pool.QueryRow(ctx, `SELECT t.source_message,t.source_call_id,r.state,(SELECT count(*) FROM work_tasks) FROM work_tasks t JOIN ai_interaction_receipts r ON r.source_call_id=t.source_call_id WHERE t.origin='APPOINTMENT_REVIEW'`).Scan(&body, &source, &state, &count); err != nil {
		t.Fatal(err)
	}
	if count != 1 || state != "PROJECTED" || source != "synthetic-review-call" || !strings.Contains(body, "Synthetic Provider") || !strings.Contains(body, "2026-10-01") {
		t.Fatalf("missing durable source facts: %d %s %s %q", count, state, source, body)
	}
	if processed, err := module.ProcessNextReceipt(ctx); err != nil || processed {
		t.Fatalf("projected receipt replayed: %v %v", processed, err)
	}
	// A successful cancellation with failed replacement booking is unfinished
	// work, not a successful reschedule that needs routine verification.
	payload.SourceCallID = "synthetic-partial-change"
	payload.Appointment = &AppointmentEvidence{Action: AppointmentRescheduled, OccurredAt: now, OldAppointmentID: "synthetic-old", BookingResult: json.RawMessage(`{"status":"error","reason":"unavailable"}`), CancellationResult: json.RawMessage(`{"status":"cancelled","appointmentId":"synthetic-old"}`)}
	raw, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint = sha256.Sum256(raw)
	if _, err := pool.Exec(ctx, `INSERT INTO ai_interaction_receipts(service_subject,practice_id,location_id,source_call_id,kind,payload_fingerprint,payload) VALUES('synthetic',$1,$2,$3,'CLOSEOUT',$4,$5)`, practice, location, payload.SourceCallID, fingerprint[:], raw); err != nil {
		t.Fatal(err)
	}
	if processed, err := module.ProcessNextReceipt(ctx); err != nil || !processed {
		t.Fatalf("project partial receipt: %v %v", processed, err)
	}
	var title, outcome string
	if err := pool.QueryRow(ctx, `SELECT t.title,t.source_message,i.appointment_outcome FROM work_tasks t JOIN ai_interactions i ON i.practice_id=t.practice_id AND i.source_call_id=t.source_call_id WHERE t.source_call_id=$1`, payload.SourceCallID).Scan(&title, &body, &outcome); err != nil {
		t.Fatal(err)
	}
	if title != "Review appointment change" || outcome != "PARTIAL" || !strings.Contains(body, "unfinished appointment change") || !strings.Contains(body, "remaining follow-up") || strings.Contains(body, "Verify insurance and provider") {
		t.Fatalf("partial outcome hidden: %s %s %q", title, outcome, body)
	}

}
