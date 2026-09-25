package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
)

func TestReceiptOfficeAuthorizationIndependentOfStaffVoice(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	am := access.New(pool, nil)
	_, err := am.Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "synthetic", Practices: []access.PracticeProvision{
		{Key: "clinical", Name: "Synthetic Practice", Locations: []access.LocationProvision{
			{Key: "medical", Name: "Medical", AbitaOfficeKeys: []string{"medical"}},
			{Key: "optical", Name: "Optical", AbitaOfficeKeys: []string{"optical"}},
		}},
		{Key: "demo", Name: "Synthetic Demo", Locations: []access.LocationProvision{
			{Key: "demo", Name: "Demo", AbitaOfficeKeys: []string{"demo", "legacy-demo"}},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	locations := map[string]string{}
	practices := map[string]string{}
	for _, key := range []string{"medical", "optical", "demo"} {
		var practice, location string
		if err := pool.QueryRow(ctx, `SELECT practice_id::text,id::text FROM access_locations WHERE provisioning_key=$1`, key).Scan(&practice, &location); err != nil {
			t.Fatal(err)
		}
		locations[key], practices[key] = location, practice
	}
	calling := humancalling.New(pool, am, nil, humancalling.Config{HandoffSIPDomain: "synthetic.example"}, nil)
	if err := calling.ProvisionLocationVoices(ctx, []humancalling.LocationVoiceProvision{{PracticeKey: "clinical", LocationKey: "medical", Number: "+15555550100", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	service := access.ServiceIdentity{Subject: "synthetic-agent", PracticeID: practices["medical"], LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityIngestAIInteraction, access.ServiceCapabilityCreateTask, access.ServiceCapabilityHumanHandoff}}
	module := New(pool, am, func() time.Time { return now })
	wm := work.New(pool, am, nil)
	for _, scenario := range []struct{ name, key, phone, location string }{
		{"inbound-alias", "medical", "+15555550101", "medical"},              // Unprovisioned inbound alias.
		{"no-staff-voice", "optical", "+15555550102", "optical"},             // No staff voice number at all.
		{"office-route-owns-location", "optical", "+15555550100", "optical"}, // Office route owns authorization; phone is evidence.
		{"demo-tenant", "demo", "+15555550103", "demo"},
		{"office-key-alias", "legacy-demo", "+15555550104", "demo"},
		{"phone-only", "", "+15555550100", "medical"}, // Existing phone-only caller.
	} {
		t.Run(scenario.name, func(t *testing.T) {
			actor := service
			actor.PracticeID = practices[scenario.location]
			source := "synthetic-" + scenario.name
			cmd := IngestCommand{Service: actor, Kind: MessageStart, OfficeKey: scenario.key, OfficePhone: scenario.phone, SourceCallID: source, CallerPhone: "+15555550199", StartedAt: now, Status: CallInProgress}
			var id string
			for _, kind := range []MessageKind{MessageStart, MessageOutcomeCheckpoint, MessageCloseout} {
				cmd.Kind = kind
				if kind == MessageOutcomeCheckpoint {
					cmd.Appointment = &AppointmentEvidence{Action: AppointmentBooked, OccurredAt: now, NewAppointmentID: "synthetic-appointment", BookingResult: json.RawMessage(`{"status":"booked","appointmentId":"synthetic-appointment"}`)}
				}
				if kind == MessageCloseout {
					end := now.Add(time.Minute)
					cmd.EndedAt = &end
					cmd.Status = CallCompleted
					cmd.CloseoutPayload = json.RawMessage(`{}`)
				}
				result, _, err := module.Ingest(ctx, cmd)
				if err != nil {
					t.Fatalf("%s: %v", kind, err)
				}
				if result.LocationID != locations[scenario.location] || result.PracticeID != actor.PracticeID || result.OfficePhone != scenario.phone || (id != "" && id != result.ID) {
					t.Fatalf("wrong attribution: %+v", result)
				}
				id = result.ID
			}
			var count int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_interaction_receipts WHERE interaction_id=$1 AND state='PROJECTED'`, id).Scan(&count); err != nil || count != 3 {
				t.Fatalf("durable lifecycle receipts: %d %v", count, err)
			}
			if scenario.key == "" {
				return
			}
			task, _, err := wm.CreateAITask(ctx, work.CreateAITaskCommand{Service: actor, OfficeKey: scenario.key, OfficePhone: "+15555550100", InboundOfficePhone: scenario.phone, SourceCallID: source, IdempotencyKey: source, Phone: cmd.CallerPhone, Summary: "Synthetic follow-up", Message: "Synthetic caller requests follow-up", Category: work.TaskCategoryOther, Urgency: work.TaskUrgencyNormal})
			if err != nil || task.LocationID != locations[scenario.location] {
				t.Fatalf("task attribution: %+v %v", task, err)
			}
			handoff, err := calling.CreateHandoff(ctx, humancalling.CreateHandoffCommand{Service: actor, OfficeKey: scenario.key, SourceCallID: source, IdempotencyKey: source, Contact: humancalling.ContactContext{Phone: cmd.CallerPhone}})
			if err != nil {
				t.Fatal(err)
			}
			var location string
			if err := pool.QueryRow(ctx, `SELECT location_id::text FROM human_calling_handoffs WHERE id=$1`, handoff.ID).Scan(&location); err != nil || location != locations[scenario.location] {
				t.Fatalf("handoff attribution: %s %v", location, err)
			}
		})
	}
	for _, scenario := range []struct {
		name, key, phone string
		actor            access.ServiceIdentity
	}{
		{"unknown-office", "unknown", "+15555550100", service},
		{"cross-tenant", "demo", "+15555550100", service},
		{"demo-to-production", "medical", "+15555550100", access.ServiceIdentity{Subject: "synthetic-demo", PracticeID: practices["demo"], LocationScope: access.LocationScopeAll, Capabilities: service.Capabilities}},
		{"missing-subject", "medical", "+15555550100", access.ServiceIdentity{PracticeID: service.PracticeID, LocationScope: access.LocationScopeAll, Capabilities: service.Capabilities}},
		{"unknown-phone-only", "", "+15555550101", service},
		{"missing-capability", "medical", "+15555550100", access.ServiceIdentity{Subject: service.Subject, PracticeID: service.PracticeID, LocationScope: access.LocationScopeAll}},
		{"selected-scope", "medical", "+15555550100", access.ServiceIdentity{Subject: service.Subject, PracticeID: service.PracticeID, LocationScope: access.LocationScopeSelected, Capabilities: service.Capabilities}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			_, _, err := module.Ingest(ctx, IngestCommand{Service: scenario.actor, Kind: MessageStart, OfficeKey: scenario.key, OfficePhone: scenario.phone, SourceCallID: scenario.name, CallerPhone: "+15555550199", StartedAt: now, Status: CallInProgress})
			if !errors.Is(err, ErrDenied) {
				t.Fatalf("expected denial: %v", err)
			}
			if scenario.key != "" {
				_, _, err = wm.CreateAITask(ctx, work.CreateAITaskCommand{Service: scenario.actor, OfficeKey: scenario.key, OfficePhone: scenario.phone, SourceCallID: scenario.name, IdempotencyKey: scenario.name, Phone: "+15555550199", Summary: "Synthetic follow-up", Message: "Synthetic follow-up request", Category: work.TaskCategoryOther, Urgency: work.TaskUrgencyNormal})
				if !errors.Is(err, work.ErrDenied) {
					t.Fatalf("expected Task denial: %v", err)
				}
				_, err = calling.CreateHandoff(ctx, humancalling.CreateHandoffCommand{Service: scenario.actor, OfficeKey: scenario.key, SourceCallID: scenario.name, IdempotencyKey: scenario.name, Contact: humancalling.ContactContext{Phone: "+15555550199"}})
				if !errors.Is(err, humancalling.ErrDenied) {
					t.Fatalf("expected handoff denial: %v", err)
				}
			}
			var count int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM ai_interaction_receipts WHERE source_call_id=$1`, scenario.name).Scan(&count); err != nil || count != 0 {
				t.Fatalf("rejected receipt persisted: %d %v", count, err)
			}
		})
	}
}
