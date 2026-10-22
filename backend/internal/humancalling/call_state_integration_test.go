package humancalling_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

type seededLeg struct {
	role    string
	state   string
	bridged bool
	age     time.Duration
}

type seededCommand struct {
	action string
	state  string
}

type seededCall struct {
	direction   string
	terminal    string
	disposition string
	legs        []seededLeg
	commands    []seededCommand
}

func staffLeg(state string) seededLeg { return seededLeg{role: "STAFF", state: state} }

func olderStaffLeg(state string) seededLeg {
	return seededLeg{role: "STAFF", state: state, age: time.Minute}
}

func destinationLeg(state string) seededLeg { return seededLeg{role: "DESTINATION", state: state} }

func bridgedStaffLeg() seededLeg { return seededLeg{role: "STAFF", state: "ENDED", bridged: true} }

func seedCall(
	t *testing.T,
	owner *pgxpool.Pool,
	authorization access.Authorization,
	now time.Time,
	call seededCall,
) string {
	t.Helper()
	ctx := context.Background()
	direction := call.direction
	if direction == "" {
		direction = "INBOUND"
	}
	var callID string
	if err := owner.QueryRow(ctx, `
		INSERT INTO human_calling_calls (
			practice_id, location_id, direction, entry_point,
			terminal_outcome, ended_at, disposition_outcome, disposition_at,
			disposition_deadline, created_at, updated_at
		) VALUES (
			$1, $2, $3, 'STANDALONE',
			NULLIF($4, ''), CASE WHEN $4 <> '' THEN $6::timestamptz END,
			NULLIF($5, ''), CASE WHEN $5 <> '' THEN $6::timestamptz END,
			CASE WHEN $4 = 'ENDED' AND $5 = '' THEN $6::timestamptz + interval '5 minutes' END,
			$6, $6
		) RETURNING id::text
	`, authorization.Practice.ID, authorization.Locations[0].ID, direction,
		call.terminal, call.disposition, now).Scan(&callID); err != nil {
		t.Fatalf("seed call: %v", err)
	}
	for index, leg := range call.legs {
		answered := leg.bridged || leg.state == "ANSWERED" || leg.state == "BRIDGE_PENDING" || leg.state == "BRIDGED"
		bridgePending := leg.bridged || leg.state == "BRIDGE_PENDING" || leg.state == "BRIDGED"
		bridged := leg.bridged || leg.state == "BRIDGED"
		ended := leg.state == "ENDED" || leg.state == "FAILED"
		var staffSubject *string
		if leg.role == "STAFF" {
			subject := fmt.Sprintf("%s-staff-%d", callID, index+1)
			staffSubject = &subject
		}
		if _, err := owner.Exec(ctx, `
			INSERT INTO human_calling_call_legs (
				call_id, role, sequence, staff_subject, state,
				answered_at, bridge_pending_at, bridged_at, ended_at,
				created_at, updated_at
			) VALUES (
				$1, $2, $3, $11, $4,
				CASE WHEN $5 THEN $9::timestamptz END,
				CASE WHEN $6 THEN $9::timestamptz END,
				CASE WHEN $7 THEN $9::timestamptz END,
				CASE WHEN $8 THEN $9::timestamptz END,
				$9, $9::timestamptz - $10::interval
			)
		`, callID, leg.role, index+1, leg.state, answered, bridgePending, bridged,
			ended, now, leg.age.String(), staffSubject); err != nil {
			t.Fatalf("seed %s %s leg: %v", leg.role, leg.state, err)
		}
	}
	for _, command := range call.commands {
		if _, err := owner.Exec(ctx, `
			INSERT INTO human_calling_provider_commands (
				call_id, action, target_id, state, created_at, updated_at
			) VALUES ($1, $2, 'synthetic-' || $2, $3, $4, $4)
		`, callID, command.action, command.state, now); err != nil {
			t.Fatalf("seed %s command: %v", command.action, err)
		}
	}
	return callID
}

func TestCallStateClassifiesEveryDistinguishableInput(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 21, 15, 0, 0, 0, time.UTC)
	accessModule := access.New(db.As(testdb.Portal), func() time.Time { return now })
	authorization, identities := provisionConcurrentStaff(t, db.Owner, accessModule, now, "call-state", 1)
	calling := humancalling.New(db.As(testdb.Portal), accessModule, nil,
		humancalling.Config{}, func() time.Time { return now },
	)
	cases := []struct {
		name string
		call seededCall
		want humancalling.CallState
	}{
		{"inbound without legs", seededCall{}, humancalling.CallRinging},
		{"outbound without legs", seededCall{direction: "OUTBOUND"}, humancalling.CallPreparing},
		{"staff PENDING", seededCall{legs: []seededLeg{staffLeg("PENDING")}}, humancalling.CallRinging},
		{"staff DIALING", seededCall{legs: []seededLeg{staffLeg("DIALING")}}, humancalling.CallRinging},
		{"staff RINGING", seededCall{legs: []seededLeg{staffLeg("RINGING")}}, humancalling.CallRinging},
		{"staff ANSWERED", seededCall{legs: []seededLeg{staffLeg("ANSWERED")}}, humancalling.CallConnecting},
		{"staff BRIDGE_PENDING", seededCall{legs: []seededLeg{staffLeg("BRIDGE_PENDING")}}, humancalling.CallConnecting},
		{"staff BRIDGED", seededCall{legs: []seededLeg{staffLeg("BRIDGED")}}, humancalling.CallConnected},
		{"staff ENDING", seededCall{legs: []seededLeg{staffLeg("ENDING")}}, humancalling.CallRinging},
		{"staff ENDED", seededCall{legs: []seededLeg{staffLeg("ENDED")}}, humancalling.CallRinging},
		{"staff FAILED", seededCall{legs: []seededLeg{staffLeg("FAILED")}}, humancalling.CallRinging},
		{"outbound staff DIALING", seededCall{direction: "OUTBOUND", legs: []seededLeg{staffLeg("DIALING")}}, humancalling.CallRinging},
		{"caller leg only", seededCall{legs: []seededLeg{{role: "CALLER", state: "ANSWERED"}}}, humancalling.CallRinging},
		{"ENDING outranks newer ANSWERED", seededCall{legs: []seededLeg{
			olderStaffLeg("ENDING"), staffLeg("ANSWERED"),
		}}, humancalling.CallRinging},
		{"newer ANSWERED outranks ENDED", seededCall{legs: []seededLeg{
			olderStaffLeg("ENDED"), staffLeg("ANSWERED"),
		}}, humancalling.CallConnecting},
		{"newer FAILED outranks ANSWERED", seededCall{legs: []seededLeg{
			olderStaffLeg("ANSWERED"), staffLeg("FAILED"),
		}}, humancalling.CallRinging},
		{"BRIDGED outranks newer RINGING", seededCall{legs: []seededLeg{
			olderStaffLeg("BRIDGED"), staffLeg("RINGING"),
		}}, humancalling.CallConnected},
		{"BRIDGE_PENDING outranks newer DIALING", seededCall{legs: []seededLeg{
			olderStaffLeg("BRIDGE_PENDING"), staffLeg("DIALING"),
		}}, humancalling.CallConnecting},
		{"destination BRIDGED", seededCall{direction: "OUTBOUND", legs: []seededLeg{
			destinationLeg("BRIDGED"),
		}}, humancalling.CallConnected},
		{"destination BRIDGED beside ANSWERED staff", seededCall{direction: "OUTBOUND", legs: []seededLeg{
			staffLeg("ANSWERED"), destinationLeg("BRIDGED"),
		}}, humancalling.CallConnected},
		{"latest destination decides", seededCall{direction: "OUTBOUND", legs: []seededLeg{
			destinationLeg("BRIDGED"), destinationLeg("RINGING"),
		}}, humancalling.CallPreparing},
		{"BRIDGED staff beside RINGING destination", seededCall{direction: "OUTBOUND", legs: []seededLeg{
			staffLeg("BRIDGED"), destinationLeg("RINGING"),
		}}, humancalling.CallConnected},
		{"voicemail greeting", seededCall{commands: []seededCommand{
			{"SPEAK_VOICEMAIL", "SENT"},
		}}, humancalling.CallVoicemailGreeting},
		{"voicemail recording", seededCall{commands: []seededCommand{
			{"SPEAK_VOICEMAIL", "RECONCILED"}, {"START_VOICEMAIL_RECORDING", "PENDING"},
		}}, humancalling.CallVoicemailRecording},
		{"ambiguous greeting after failed recording", seededCall{commands: []seededCommand{
			{"START_VOICEMAIL_RECORDING", "FAILED"}, {"SPEAK_VOICEMAIL", "AMBIGUOUS"},
		}}, humancalling.CallVoicemailGreeting},
		{"sending recording", seededCall{commands: []seededCommand{
			{"START_VOICEMAIL_RECORDING", "SENDING"},
		}}, humancalling.CallVoicemailRecording},
		{"failed greeting", seededCall{commands: []seededCommand{
			{"SPEAK_VOICEMAIL", "FAILED"},
		}}, humancalling.CallRinging},
		{"greeting outranks BRIDGED staff", seededCall{
			legs:     []seededLeg{staffLeg("BRIDGED")},
			commands: []seededCommand{{"SPEAK_VOICEMAIL", "PENDING"}},
		}, humancalling.CallVoicemailGreeting},
		{"other provider commands", seededCall{commands: []seededCommand{
			{"HANGUP_LEG", "SENT"},
		}}, humancalling.CallRinging},
		{"terminal VOICEMAIL outranks recording", seededCall{terminal: "VOICEMAIL", commands: []seededCommand{
			{"START_VOICEMAIL_RECORDING", "SENT"},
		}}, humancalling.CallVoicemail},
		{"terminal UNANSWERED outranks greeting", seededCall{terminal: "UNANSWERED", commands: []seededCommand{
			{"SPEAK_VOICEMAIL", "SENT"},
		}}, humancalling.CallUnanswered},
		{"ENDED", seededCall{terminal: "ENDED"}, humancalling.CallUnanswered},
		{"ENDED connected", seededCall{terminal: "ENDED", legs: []seededLeg{bridgedStaffLeg()}}, humancalling.CallNeedsDisposition},
		{"ENDED connected by destination", seededCall{direction: "OUTBOUND", terminal: "ENDED", legs: []seededLeg{
			{role: "DESTINATION", state: "ENDED", bridged: true},
		}}, humancalling.CallNeedsDisposition},
		{"UNANSWERED", seededCall{terminal: "UNANSWERED"}, humancalling.CallUnanswered},
		{"UNANSWERED connected", seededCall{terminal: "UNANSWERED", legs: []seededLeg{bridgedStaffLeg()}}, humancalling.CallUnanswered},
		{"VOICEMAIL", seededCall{terminal: "VOICEMAIL"}, humancalling.CallVoicemail},
		{"VOICEMAIL connected", seededCall{terminal: "VOICEMAIL", legs: []seededLeg{bridgedStaffLeg()}}, humancalling.CallVoicemail},
		{"MISSED", seededCall{terminal: "MISSED"}, humancalling.CallMissed},
		{"MISSED connected", seededCall{terminal: "MISSED", legs: []seededLeg{bridgedStaffLeg()}}, humancalling.CallMissed},
		{"RESOLVED", seededCall{terminal: "RESOLVED"}, humancalling.CallResolved},
		{"RESOLVED connected", seededCall{terminal: "RESOLVED", legs: []seededLeg{bridgedStaffLeg()}}, humancalling.CallResolved},
		{"FOLLOW_UP_REQUIRED", seededCall{terminal: "FOLLOW_UP_REQUIRED"}, humancalling.CallFollowUpRequired},
		{"FOLLOW_UP_REQUIRED connected", seededCall{terminal: "FOLLOW_UP_REQUIRED", legs: []seededLeg{bridgedStaffLeg()}}, humancalling.CallFollowUpRequired},
		{"ROUTING_FAILED", seededCall{terminal: "ROUTING_FAILED"}, humancalling.CallUnanswered},
		{"ROUTING_FAILED connected", seededCall{terminal: "ROUTING_FAILED", legs: []seededLeg{bridgedStaffLeg()}}, humancalling.CallUnanswered},
		{"ABANDONED", seededCall{terminal: "ABANDONED"}, humancalling.CallMissed},
		{"ABANDONED connected", seededCall{terminal: "ABANDONED", legs: []seededLeg{bridgedStaffLeg()}}, humancalling.CallMissed},
		{"disposition RESOLVED", seededCall{terminal: "ENDED", disposition: "RESOLVED", legs: []seededLeg{bridgedStaffLeg()}}, humancalling.CallResolved},
		{"disposition FOLLOW_UP_REQUIRED", seededCall{terminal: "ENDED", disposition: "FOLLOW_UP_REQUIRED", legs: []seededLeg{bridgedStaffLeg()}}, humancalling.CallFollowUpRequired},
		{"disposition COMPLETE_TASK", seededCall{terminal: "ENDED", disposition: "COMPLETE_TASK", legs: []seededLeg{bridgedStaffLeg()}}, humancalling.CallResolved},
		{"disposition KEEP_OPEN", seededCall{terminal: "ENDED", disposition: "KEEP_OPEN", legs: []seededLeg{bridgedStaffLeg()}}, humancalling.CallResolved},
		{"disposition CREATE_TASK", seededCall{terminal: "ENDED", disposition: "CREATE_TASK", legs: []seededLeg{bridgedStaffLeg()}}, humancalling.CallFollowUpRequired},
		{"disposition NO_FOLLOW_UP", seededCall{terminal: "ENDED", disposition: "NO_FOLLOW_UP"}, humancalling.CallResolved},
		{"disposition outranks terminal FOLLOW_UP_REQUIRED", seededCall{terminal: "FOLLOW_UP_REQUIRED", disposition: "RESOLVED"}, humancalling.CallResolved},
		{"disposition outranks terminal MISSED", seededCall{terminal: "MISSED", disposition: "CREATE_TASK"}, humancalling.CallFollowUpRequired},
		{"disposition outranks live call", seededCall{disposition: "KEEP_OPEN", legs: []seededLeg{staffLeg("BRIDGED")}}, humancalling.CallResolved},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			callID := seedCall(t, db.Owner, authorization, now, testCase.call)
			call, err := calling.ReadCall(ctx, identities[0], callID)
			if err != nil {
				t.Fatal(err)
			}
			if call.State != testCase.want {
				t.Fatalf("Call state = %s, want %s", call.State, testCase.want)
			}
		})
	}
}
