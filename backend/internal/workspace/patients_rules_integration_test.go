package workspace_test

import (
	"slices"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
)

func (f *patientWorkFixture) hasOwner(reader access.Identity, phone string) bool {
	f.t.Helper()
	mine := slices.Contains(patientPhones(f.query(workspace.QueryPatientWorkCommand{Identity: reader})), phone)
	unowned := slices.Contains(patientPhones(f.query(workspace.QueryPatientWorkCommand{
		Identity: reader, Scope: workspace.PatientWorkEveryone, Filter: workspace.PatientWorkUnowned,
	})), phone)
	legacy, err := f.reads.QueryTasks(f.ctx, workspace.QueryTasksCommand{
		Identity: reader, PracticeID: f.practiceID, Responsibility: "mine", Ordering: work.TaskOrderingRecent,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	listed := slices.ContainsFunc(legacy.Items, func(task work.Task) bool { return task.Phone == phone })
	if mine != unowned || mine != listed {
		f.t.Fatalf("patient %s: in My Tasks %t, under No owner %t, in the Task query %t; want one answer", phone, mine, unowned, listed)
	}
	return !mine
}

func TestPatientWorkOwnerNeedsAnUnrevokedMembershipCoveringTheLocation(t *testing.T) {
	f := newPatientWorkFixture(t)
	reader := f.activate("reader")
	former := f.activate("former")
	f.activate("elsewhere")
	f.activate("local")
	cases := []struct {
		name, phone, office, owner, category string
		owned                                bool
	}{
		{"never signed in", "+17275550401", "north", "pending", "optical", false},
		{"signed in", "+17275550402", "north", "former", "medication", true},
		{"membership without the Location", "+17275550403", "north", "elsewhere", "referrals", false},
		{"membership with the Location", "+17275550404", "north", "local", "insurance", true},
		{"same owner at a Location outside the membership", "+17275550405", "south", "local", "insurance", false},
		{"no access at all", "+17275550406", "north", "ghost", "documentation", false},
	}
	for _, test := range cases {
		f.own(test.office, test.owner, test.category)
		f.open(patientTask{origin: work.TaskOriginAbitaAI, office: test.office, phone: test.phone, category: test.category})
	}
	check := func(stage string) {
		t.Helper()
		for _, test := range cases {
			if got := f.hasOwner(reader, test.phone); got != test.owned {
				t.Fatalf("%s: owner %q (%s) counts = %t, want %t", stage, test.owner, test.name, got, test.owned)
			}
		}
	}
	check("before any change")

	f.activate("pending")
	formerAuthorization, err := f.accessPort.ResolveActor(f.ctx, former, f.practiceID, "")
	if err != nil {
		t.Fatal(err)
	}
	testaccess.SeedRevokedMembership(t, f.db.Owner, formerAuthorization, f.at)
	cases[0].name, cases[0].owned = "signed in since", true
	cases[1].name, cases[1].owned = "membership revoked", false
	check("after the pending owner signs in and the other is revoked")
}

func TestPatientWorkTreatsARosteredPlatformOperatorAsNoOwner(t *testing.T) {
	f := newPatientWorkFixture(t)
	reader := f.activate("reader")
	operator := access.Identity{Subject: "patient-work-operator", Email: "operator@patient-work.test", EmailVerified: true}
	const phone = "+17275550451"
	f.own("north", "operator", "pre_op")
	f.open(patientTask{origin: work.TaskOriginAbitaAI, office: "north", phone: phone, category: "pre_op"})

	if f.hasOwner(reader, phone) {
		t.Fatal("a category listed only for a platform operator has an owner, want No owner for staff")
	}
	mine := f.query(workspace.QueryPatientWorkCommand{Identity: operator})
	if row := patientRow(t, mine, phone); !row.HasUnowned || mine.Counts.Unowned != 1 {
		t.Fatalf("operator's My Tasks = %+v %+v, want the Task listed as No owner", row, mine.Counts)
	}
}

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
		command := workspace.QueryPatientWorkCommand{Identity: reader, Scope: workspace.PatientWorkEveryone, Limit: limit}
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

	page := f.query(workspace.QueryPatientWorkCommand{Identity: reader, Scope: workspace.PatientWorkEveryone})
	if phones := patientPhones(page); !slices.Equal(phones, []string{texted, voicemail}) {
		t.Fatalf("order = %v, want the patient whose text is newest first", phones)
	}
	if row := patientRow(t, page, texted); !row.ActivityAt.Equal(newestText) {
		t.Fatalf("text row activity = %s, want the newest text at %s", row.ActivityAt, newestText)
	}
	if row := patientRow(t, page, voicemail); row.VoicemailDurationSeconds != 9 {
		t.Fatalf("voicemail row = %d seconds, want the 9 second voicemail, not the later missed call", row.VoicemailDurationSeconds)
	}
}

func TestPatientWorkSearchMatchesTheDisplayedNameWithinScope(t *testing.T) {
	f := newPatientWorkFixture(t)
	reader := f.activate("reader")
	owner := f.activate("owner")
	f.own("north", "owner", "insurance")
	const (
		textOnly   = "+17275550701"
		mixed      = "+17275550702"
		colleagues = "+17275550703"
	)
	f.open(patientTask{origin: work.TaskOriginInboundMessageReview, office: "north", phone: textOnly, threadName: "Marisol Vega"})
	f.open(patientTask{origin: work.TaskOriginAbitaAI, office: "north", phone: mixed, category: "insurance", title: "Crown estimate", callerName: "Tobias Lund"})
	f.open(patientTask{origin: work.TaskOriginInboundMessageReview, office: "north", phone: mixed})
	f.open(patientTask{origin: work.TaskOriginAbitaAI, office: "north", phone: colleagues, category: "insurance", title: "Crown estimate", callerName: "Petra Novak"})

	for _, test := range []struct {
		label    string
		identity access.Identity
		scope    workspace.PatientWorkScope
		search   string
		want     []string
	}{
		{"a name known only from the text thread", reader, workspace.PatientWorkMine, "marisol", []string{textOnly}},
		{"the same name under Everyone", reader, workspace.PatientWorkEveryone, "Marisol Vega", []string{textOnly}},
		{"a title only in a colleague's Task", reader, workspace.PatientWorkMine, "crown", nil},
		{"the same title for its owner", owner, workspace.PatientWorkMine, "crown", []string{mixed, colleagues}},
		{"the same title under Everyone", reader, workspace.PatientWorkEveryone, "crown", []string{mixed, colleagues}},
		{"the name the row shows", reader, workspace.PatientWorkMine, "tobias", []string{mixed}},
		{"a name on a patient with nothing in scope", reader, workspace.PatientWorkMine, "petra", nil},
		{"the phone of a patient with nothing in scope", reader, workspace.PatientWorkMine, "555-0703", nil},
	} {
		page := f.query(workspace.QueryPatientWorkCommand{Identity: test.identity, Scope: test.scope, Search: test.search})
		assertPatients(t, test.label, page, test.want...)
		if page.Counts.All != len(test.want) {
			t.Fatalf("%s: All count = %d, want %d", test.label, page.Counts.All, len(test.want))
		}
	}
	if row := patientRow(t, f.query(workspace.QueryPatientWorkCommand{Identity: reader, Search: "marisol"}), textOnly); row.DisplayName != "Marisol Vega" {
		t.Fatalf("text-only patient name = %q, want the thread's name", row.DisplayName)
	}
}
