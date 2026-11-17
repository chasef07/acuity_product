package workspace_test

import (
	"slices"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
)

func TestPatientWorkPagesAcrossPriorityAndTiedActivity(t *testing.T) {
	f := newPatientWorkFixture(t)
	reader := f.activate("reader")
	open := func(phone, urgency string, tied bool) {
		f.open(patientTask{origin: work.TaskOriginAbitaAI, office: "north", phone: phone, category: "other", urgency: urgency, tied: tied})
	}
	open("+17275550501", "high_priority", false)
	open("+17275550502", "high_priority", true)
	open("+17275550503", "normal", false)
	open("+17275550504", "normal", true)
	open("+17275550505", "normal", true)
	open("+17275550506", "high_priority", false)
	open("+17275550507", "normal", false)
	want := []string{
		"+17275550506", "+17275550502", "+17275550501",
		"+17275550507", "+17275550505", "+17275550504", "+17275550503",
	}
	for limit := 1; limit <= len(want); limit++ {
		command := workspace.QueryPatientWorkCommand{Identity: reader, Limit: limit}
		got := []string{}
		for {
			page := f.query(command)
			if len(page.Rows) > limit {
				t.Fatalf("limit %d returned %d rows", limit, len(page.Rows))
			}
			got = append(got, patientPhones(page)...)
			if page.NextCursor == "" || len(got) > len(want) {
				break
			}
			command.Cursor = page.NextCursor
		}
		if !slices.Equal(got, want) {
			t.Fatalf("pages of %d = %v, want every patient once in order %v", limit, got, want)
		}
	}
}

func TestPatientWorkRowReportsTheVoicemailLengthAndTheNewestTextTime(t *testing.T) {
	f := newPatientWorkFixture(t)
	reader := f.activate("reader")
	const (
		texted    = "+17275550601"
		voicemail = "+17275550602"
	)
	text := f.open(patientTask{origin: work.TaskOriginInboundMessageReview, office: "north", phone: texted})
	recovery := f.open(patientTask{origin: work.TaskOriginVoicemail, office: "north", phone: voicemail, voicemail: 9000})
	f.at = f.at.Add(time.Minute)
	if _, err := f.db.Owner.Exec(f.ctx, `
		WITH call AS (
			INSERT INTO human_calling_calls(practice_id,location_id,direction,entry_point,caller_phone,terminal_outcome,ended_at,created_at,updated_at)
			VALUES($1,$2,'INBOUND','STANDALONE',$3,'RESOLVED',$5,$5,$5) RETURNING id
		)
		INSERT INTO human_calling_voicemails(call_id,practice_id,location_id,task_id,outcome,audio_state,created_at,updated_at)
		SELECT call.id,$1,$2,$4,'MISSED_CALL','UNAVAILABLE',$5,$5 FROM call
	`, f.practiceID, f.locations["north"], voicemail, recovery, f.at); err != nil {
		t.Fatalf("insert later missed call: %v", err)
	}
	newestText := f.at.Add(time.Minute)
	if _, err := f.db.Owner.Exec(f.ctx, `UPDATE work_tasks SET updated_at=$2 WHERE id=$1`, text, newestText); err != nil {
		t.Fatalf("record a newer text: %v", err)
	}

	page := f.query(workspace.QueryPatientWorkCommand{Identity: reader})
	if phones := patientPhones(page); !slices.Equal(phones, []string{texted, voicemail}) {
		t.Fatalf("order = %v, want the patient whose text is newest first", phones)
	}
	if row := patientRow(t, page, texted); !row.ActivityAt.Equal(newestText) {
		t.Fatalf("text row activity = %s, want the newest text at %s", row.ActivityAt, newestText)
	}
	if row := patientRow(t, page, voicemail); row.Top.VoicemailDurationSeconds != 9 {
		t.Fatalf("voicemail row = %d seconds, want the 9 second voicemail, not the later missed call", row.Top.VoicemailDurationSeconds)
	}
	summary, err := f.reads.QueryPhoneSummary(f.ctx, workspace.QueryPhoneSummaryCommand{
		Identity: reader, PracticeID: f.practiceID, Phone: voicemail,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.OpenTasks) != 1 || summary.OpenTasks[0].VoicemailDurationSeconds != 9 {
		t.Fatalf("patient summary open Tasks = %+v, want the voicemail review with its 9 second length", summary.OpenTasks)
	}
}

func TestPatientWorkSearchMatchesTheDisplayedName(t *testing.T) {
	f := newPatientWorkFixture(t)
	reader := f.activate("reader")
	const (
		textOnly = "+17275550701"
		mixed    = "+17275550702"
		other    = "+17275550703"
	)
	f.open(patientTask{origin: work.TaskOriginInboundMessageReview, office: "north", phone: textOnly, threadName: "Marisol Vega"})
	f.open(patientTask{origin: work.TaskOriginAbitaAI, office: "north", phone: mixed, category: "insurance", title: "Crown estimate", callerName: "Tobias Lund"})
	f.open(patientTask{origin: work.TaskOriginInboundMessageReview, office: "north", phone: mixed})
	f.open(patientTask{origin: work.TaskOriginAbitaAI, office: "north", phone: other, category: "insurance", title: "Crown estimate", callerName: "Petra Novak"})

	for _, test := range []struct {
		label  string
		search string
		want   []string
	}{
		{"a name known only from the text thread", "marisol", []string{textOnly}},
		{"the full name from the text thread", "Marisol Vega", []string{textOnly}},
		{"a title on two patients", "crown", []string{mixed, other}},
		{"the name the row shows", "tobias", []string{mixed}},
		{"a caller name", "petra", []string{other}},
		{"a phone", "555-0703", []string{other}},
	} {
		page := f.query(workspace.QueryPatientWorkCommand{Identity: reader, Search: test.search})
		assertPatients(t, test.label, page, test.want...)
		if page.Counts.All != len(test.want) {
			t.Fatalf("%s: All count = %d, want %d", test.label, page.Counts.All, len(test.want))
		}
	}
	if row := patientRow(t, f.query(workspace.QueryPatientWorkCommand{Identity: reader, Search: "marisol"}), textOnly); row.DisplayName != "Marisol Vega" {
		t.Fatalf("text-only patient name = %q, want the thread's name", row.DisplayName)
	}
}
