package workspace_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
	"github.com/google/uuid"
)

type patientWorkFixture struct {
	t          *testing.T
	ctx        context.Context
	db         *testdb.DB
	accessPort *access.Module
	reads      *workspace.Module
	practiceID string
	locations  map[string]string
	at         time.Time
}

type patientTask struct {
	origin     work.TaskOrigin
	office     string
	phone      string
	title      string
	callerName string
	category   string
	urgency    string
	text       string
	threadName string
	voicemail  int
	tied       bool
}

func newPatientWorkFixture(t *testing.T) *patientWorkFixture {
	t.Helper()
	f := &patientWorkFixture{
		t:         t,
		ctx:       context.Background(),
		db:        testdb.Open(t),
		locations: map[string]string{},
		at:        time.Date(2026, time.November, 9, 15, 0, 0, 0, time.UTC),
	}
	clock := func() time.Time { return f.at }
	grant := func(key string, scope access.LocationScope, offices ...string) access.AccessGrantProvision {
		return access.AccessGrantProvision{
			Key: key, Email: key + "@patient-work.test", Role: access.RoleStaff,
			LocationScope: scope, SelectedLocationKeys: offices,
		}
	}
	if _, err := access.New(f.db.Owner, clock).Provision(f.ctx, access.Provisioning{
		Environment:       "test",
		RequestedBy:       "patient-work-test",
		PlatformOperators: []string{"operator@patient-work.test"},
		Practices: []access.PracticeProvision{{
			Key:  "patient-work",
			Name: "Synthetic Patient Work",
			Locations: []access.LocationProvision{
				{Key: "north", Name: "North Office"},
				{Key: "south", Name: "South Office"},
				{Key: "hidden", Name: "Hidden Office"},
			},
			AccessGrants: []access.AccessGrantProvision{
				grant("reader", access.LocationScopeSelected, "north", "south"),
				grant("owner", access.LocationScopeAll),
				grant("former", access.LocationScopeAll),
				grant("pending", access.LocationScopeAll),
				grant("local", access.LocationScopeSelected, "north"),
				grant("elsewhere", access.LocationScopeSelected, "hidden"),
			},
		}},
	}); err != nil {
		t.Fatalf("provision patient work fixture: %v", err)
	}
	f.accessPort = access.New(f.db.As(testdb.Portal), clock)
	f.reads = workspace.New(f.db.As(testdb.Portal), f.accessPort)
	rows, err := f.db.Owner.Query(f.ctx, `SELECT provisioning_key, id::text, practice_id::text FROM access_locations`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, id string
		if err := rows.Scan(&key, &id, &f.practiceID); err != nil {
			t.Fatal(err)
		}
		f.locations[key] = id
	}
	return f
}

func (f *patientWorkFixture) activate(key string) access.Identity {
	f.t.Helper()
	identity := access.Identity{
		Subject: "patient-work-" + key, Email: key + "@patient-work.test", EmailVerified: true,
	}
	testaccess.Activate(f.t, f.accessPort, identity)
	return identity
}

func (f *patientWorkFixture) own(office, key, category string) {
	f.t.Helper()
	if _, err := f.db.Owner.Exec(f.ctx,
		`INSERT INTO work_responsibility_locations VALUES($1,$2) ON CONFLICT DO NOTHING`,
		f.practiceID, f.locations[office],
	); err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.db.Owner.Exec(f.ctx,
		`INSERT INTO work_responsibilities VALUES($1,$2,$3,$4,'primary')`,
		f.practiceID, f.locations[office], key+"@patient-work.test", category,
	); err != nil {
		f.t.Fatal(err)
	}
}

func (f *patientWorkFixture) open(task patientTask) string {
	f.t.Helper()
	if !task.tied {
		f.at = f.at.Add(time.Minute)
	}
	id := uuid.NewString()
	locationID := f.locations[task.office]
	if task.title == "" {
		task.title = "Synthetic " + string(task.origin)
	}
	if task.urgency == "" {
		task.urgency = "normal"
	}
	if task.origin == work.TaskOriginAppointmentReview {
		task.category = "appointments"
	}
	var callID, messageID, threadID *string
	newCall := func() *string {
		var call string
		if err := f.db.Owner.QueryRow(f.ctx, `
			INSERT INTO human_calling_calls(practice_id,location_id,direction,entry_point,caller_phone,terminal_outcome,ended_at,created_at,updated_at)
			VALUES($1,$2,'INBOUND','STANDALONE',$3,'RESOLVED',$4,$4,$4) RETURNING id::text
		`, f.practiceID, locationID, task.phone, f.at).Scan(&call); err != nil {
			f.t.Fatalf("insert call: %v", err)
		}
		return &call
	}
	newMessage := func() (*string, *string) {
		var thread string
		if err := f.db.Owner.QueryRow(f.ctx, `
			INSERT INTO messaging_threads(practice_id,location_id,office_phone,external_phone,display_name,name_source)
			VALUES($1,$2,'+17275550100',$3,NULLIF($4,''),NULLIF($5,''))
			ON CONFLICT (practice_id,location_id,office_phone,external_phone) DO UPDATE SET updated_at=EXCLUDED.updated_at
			RETURNING id::text
		`, f.practiceID, locationID, task.phone, task.threadName, map[bool]string{true: "synthetic"}[task.threadName != ""]).Scan(&thread); err != nil {
			f.t.Fatalf("insert thread: %v", err)
		}
		message := uuid.NewString()
		text := task.text
		if text == "" {
			text = "Synthetic text"
		}
		if _, err := f.db.Owner.Exec(f.ctx, `
			INSERT INTO messaging_messages(id,thread_id,practice_id,location_id,direction,body,sender,destination,delivery_state,provider_message_id,created_at,updated_at)
			VALUES($1::uuid,$2,$3,$4,'INBOUND',$5,$6,'+17275550100','DELIVERED',$1::text,$7,$7)
		`, message, thread, f.practiceID, locationID, text, task.phone, f.at); err != nil {
			f.t.Fatalf("insert message: %v", err)
		}
		return &message, &thread
	}
	kind, subject := "SERVICE", "patient-work-service"
	var email, callerName, category, sourceCall, sourceMessage, idempotency, reviewKey, recovery *string
	var fingerprint []byte
	text := func(value string) *string { return &value }
	if task.callerName != "" {
		callerName = &task.callerName
	}
	if task.category != "" {
		category = &task.category
	}
	switch task.origin {
	case work.TaskOriginHumanCallFollowUp:
		kind, subject, email = "HUMAN", "patient-work-staff", text("staff@patient-work.test")
		callID = newCall()
	case work.TaskOriginStaffMessageFollowUp:
		kind, subject, email = "HUMAN", "patient-work-staff", text("staff@patient-work.test")
		messageID, threadID = newMessage()
	case work.TaskOriginAbitaAI:
		sourceCall, sourceMessage, idempotency = text("ai-"+id), text("Synthetic message"), text("ai-"+id)
		fingerprint = []byte(id[:32])
	case work.TaskOriginVoicemail:
		callID, recovery = newCall(), text("VOICEMAIL")
	case work.TaskOriginMissedCall:
		callID, recovery = newCall(), text("MISSED_CALL")
	case work.TaskOriginAppointmentReview:
		sourceCall, reviewKey = text("review-"+id), text("review-"+id)
	case work.TaskOriginInboundMessageReview:
		messageID, threadID = newMessage()
	default:
		f.t.Fatalf("unknown origin %q", task.origin)
	}
	if _, err := f.db.Owner.Exec(f.ctx, `
		INSERT INTO work_tasks(
			id,practice_id,location_id,call_id,phone,title,state,origin,urgency,
			created_by_kind,created_by_subject,created_by_email,caller_name,category,
			source_call_id,source_message,ai_idempotency_key,ai_input_fingerprint,
			source_message_id,message_thread_id,recovery_outcome,source_review_key,
			created_at,updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,'OPEN',$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$22)
	`, id, f.practiceID, locationID, callID, task.phone, task.title, task.origin, task.urgency,
		kind, subject, email, callerName, category, sourceCall, sourceMessage, idempotency, fingerprint,
		messageID, threadID, recovery, reviewKey, f.at,
	); err != nil {
		f.t.Fatalf("insert %s Task: %v", task.origin, err)
	}
	if task.voicemail > 0 {
		if _, err := f.db.Owner.Exec(f.ctx, `
			INSERT INTO human_calling_voicemails(
				call_id,practice_id,location_id,task_id,outcome,audio_state,provider_recording_id,
				recording_started_at,recording_ended_at,duration_millis,created_at,updated_at
			) VALUES ($1,$2,$3,$4,'VOICEMAIL','READY',$5,$6,$7,$8,$6,$7)
		`, callID, f.practiceID, locationID, id, "recording-"+id, f.at,
			f.at.Add(time.Duration(task.voicemail)*time.Millisecond), task.voicemail,
		); err != nil {
			f.t.Fatalf("insert voicemail: %v", err)
		}
	}
	return id
}

func (f *patientWorkFixture) query(command workspace.QueryPatientWorkCommand) workspace.PatientWorkPage {
	f.t.Helper()
	command.PracticeID = f.practiceID
	page, err := f.reads.QueryPatientWork(f.ctx, command)
	if err != nil {
		f.t.Fatalf("query patient work %+v: %v", command, err)
	}
	return page
}

func patientPhones(page workspace.PatientWorkPage) []string {
	phones := make([]string, 0, len(page.Rows))
	for _, row := range page.Rows {
		phones = append(phones, row.Phone)
	}
	return phones
}

func patientRow(t *testing.T, page workspace.PatientWorkPage, phone string) workspace.PatientWorkRow {
	t.Helper()
	for _, row := range page.Rows {
		if row.Phone == phone {
			return row
		}
	}
	t.Fatalf("patient %s is missing from %v", phone, patientPhones(page))
	return workspace.PatientWorkRow{}
}

func assertPatients(t *testing.T, label string, page workspace.PatientWorkPage, want ...string) {
	t.Helper()
	got := patientPhones(page)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("%s patients = %v, want %v", label, got, want)
	}
}

var everyTaskOrigin = []work.TaskOrigin{
	work.TaskOriginHumanCallFollowUp,
	work.TaskOriginAbitaAI,
	work.TaskOriginStaffMessageFollowUp,
	work.TaskOriginVoicemail,
	work.TaskOriginMissedCall,
	work.TaskOriginAppointmentReview,
	work.TaskOriginInboundMessageReview,
}

func TestPatientWorkListsEveryOpenTaskUnderExactlyOnePatient(t *testing.T) {
	f := newPatientWorkFixture(t)
	reader := f.activate("reader")
	const (
		everything = "+17275550101"
		voicemail  = "+17275550102"
		missed     = "+17275550103"
		texted     = "+17275550104"
		urgent     = "+17275550105"
		unseen     = "+17275550106"
		appointed  = "+17275550107"
		shared     = "+17275550108"
	)
	offices := []string{"north", "south"}
	tops := map[work.TaskOrigin]string{}
	for index, origin := range everyTaskOrigin {
		task := patientTask{origin: origin, office: offices[index%2], phone: everything, text: "Is the office open Friday?"}
		if origin == work.TaskOriginAbitaAI {
			task.category, task.callerName = "insurance", "Dolores Reyes"
		}
		if origin == work.TaskOriginVoicemail {
			task.voicemail = 21500
		}
		tops[origin] = f.open(task)
	}
	f.open(patientTask{origin: work.TaskOriginAbitaAI, office: "hidden", phone: everything, category: "other", callerName: "Hidden Alias"})
	urgentTask := f.open(patientTask{origin: work.TaskOriginAbitaAI, office: "north", phone: urgent, category: "medication", urgency: "high_priority", callerName: "Walter Stone"})
	f.open(patientTask{origin: work.TaskOriginVoicemail, office: "north", phone: voicemail, voicemail: 37600})
	f.open(patientTask{origin: work.TaskOriginMissedCall, office: "south", phone: missed})
	f.open(patientTask{origin: work.TaskOriginInboundMessageReview, office: "north", phone: texted, text: "Running ten minutes late"})
	f.open(patientTask{origin: work.TaskOriginAbitaAI, office: "hidden", phone: unseen, category: "other", callerName: "Hidden Person"})
	f.open(patientTask{origin: work.TaskOriginAppointmentReview, office: "south", phone: appointed, title: "Verify appointment"})
	f.open(patientTask{origin: work.TaskOriginAbitaAI, office: "north", phone: shared, category: "optical", callerName: "Hank Okafor"})
	f.open(patientTask{origin: work.TaskOriginAbitaAI, office: "south", phone: shared, category: "referrals", callerName: "Rita Okafor"})
	f.open(patientTask{origin: work.TaskOriginVoicemail, office: "north", phone: urgent, voicemail: 4000})

	everyone := f.query(workspace.QueryPatientWorkCommand{Identity: reader, Scope: workspace.PatientWorkEveryone})
	phones := patientPhones(everyone)
	if want := []string{urgent, shared, appointed, texted, missed, voicemail, everything}; !slices.Equal(phones, want) {
		t.Fatalf("Everyone order = %v, want high priority first then newest first %v", phones, want)
	}
	var readable, listed int
	if err := f.db.Owner.QueryRow(f.ctx,
		`SELECT count(*) FROM work_tasks WHERE state='OPEN' AND location_id = ANY($1::uuid[])`,
		[]string{f.locations["north"], f.locations["south"]},
	).Scan(&readable); err != nil {
		t.Fatal(err)
	}
	for _, row := range everyone.Rows {
		listed += row.OpenCount
	}
	if everyone.Totals.OpenTasks != readable || listed != readable || everyone.Totals.Patients != len(everyone.Rows) || everyone.Totals.PatientsOutsideScope != 0 {
		t.Fatalf("totals = %+v with %d listed, want %d open Tasks for %d patients and none outside Everyone", everyone.Totals, listed, readable, len(everyone.Rows))
	}
	if row := patientRow(t, everyone, everything); row.OpenCount != len(everyTaskOrigin) || row.DisplayName != "Dolores Reyes" || row.HighPriority {
		t.Fatalf("patient with every origin = %+v, want %d open, the one readable name, not high priority", row, len(everyTaskOrigin))
	}
	if row := patientRow(t, everyone, shared); row.DisplayName != "" || row.OpenCount != 2 {
		t.Fatalf("shared phone = %+v, want no name when two are known", row)
	}
	if row := patientRow(t, everyone, urgent); !row.HighPriority || row.Top.ID != urgentTask || row.Top.VoicemailDurationSeconds != 0 {
		t.Fatalf("high priority patient = %+v, want its high priority Task on top ahead of the newer voicemail", row)
	}

	for _, test := range []struct {
		filter workspace.PatientWorkFilter
		count  func(workspace.PatientWorkCounts) int
		phones []string
		top    work.TaskOrigin
	}{
		{workspace.PatientWorkAll, func(c workspace.PatientWorkCounts) int { return c.All }, phones, work.TaskOriginMissedCall},
		{workspace.PatientWorkFollowUp, func(c workspace.PatientWorkCounts) int { return c.FollowUp }, []string{everything, urgent, shared}, work.TaskOriginStaffMessageFollowUp},
		{workspace.PatientWorkCallBack, func(c workspace.PatientWorkCounts) int { return c.CallBack }, []string{everything, voicemail, missed, urgent}, work.TaskOriginMissedCall},
		{workspace.PatientWorkTexts, func(c workspace.PatientWorkCounts) int { return c.Texts }, []string{everything, texted}, work.TaskOriginInboundMessageReview},
		{workspace.PatientWorkAppointments, func(c workspace.PatientWorkCounts) int { return c.Appointments }, []string{everything, appointed}, work.TaskOriginAppointmentReview},
		{workspace.PatientWorkUnowned, func(c workspace.PatientWorkCounts) int { return c.Unowned }, []string{everything, urgent, shared}, work.TaskOriginStaffMessageFollowUp},
	} {
		page := f.query(workspace.QueryPatientWorkCommand{Identity: reader, Scope: workspace.PatientWorkEveryone, Filter: test.filter})
		assertPatients(t, string(test.filter), page, test.phones...)
		if got := test.count(page.Counts); got != len(test.phones) {
			t.Fatalf("%s count = %d, want %d patients", test.filter, got, len(test.phones))
		}
		if page.Counts != everyone.Counts || page.Totals != everyone.Totals {
			t.Fatalf("%s counts and totals = %+v %+v, want the same under every filter %+v %+v", test.filter, page.Counts, page.Totals, everyone.Counts, everyone.Totals)
		}
		if row := patientRow(t, page, everything); row.Top.ID != tops[test.top] || row.OpenCount != len(everyTaskOrigin) {
			t.Fatalf("%s top for the patient with every origin = %s %q, want the %s Task", test.filter, row.Top.Origin, row.Top.Title, test.top)
		}
	}

	tasks := f.query(workspace.QueryPatientWorkCommand{Identity: reader, Scope: workspace.PatientWorkEveryone, Filter: workspace.PatientWorkFollowUp})
	if phones := patientPhones(tasks); !slices.Equal(phones, []string{urgent, shared, everything}) {
		t.Fatalf("Tasks order = %v, want high priority first then the newest follow-up", phones)
	}
	if row := patientRow(t, tasks, urgent); !row.HighPriority || row.Top.ID != urgentTask {
		t.Fatalf("Tasks row for the high priority patient = %+v, want its high priority follow-up on top", row)
	}
	callBack := f.query(workspace.QueryPatientWorkCommand{Identity: reader, Scope: workspace.PatientWorkEveryone, Filter: workspace.PatientWorkCallBack})
	if row := patientRow(t, callBack, voicemail); row.Top.VoicemailDurationSeconds != 38 || row.Top.Origin != work.TaskOriginVoicemail {
		t.Fatalf("voicemail row = %+v, want a 38 second voicemail", row)
	}
	if row := patientRow(t, callBack, missed); row.Top.VoicemailDurationSeconds != 0 || row.Top.Origin != work.TaskOriginMissedCall {
		t.Fatalf("missed call row = %+v, want no voicemail length", row)
	}
	if row := patientRow(t, callBack, urgent); row.HighPriority || row.Top.VoicemailDurationSeconds != 4 {
		t.Fatalf("call back row for the high priority patient = %+v, want only its voicemail to count", row)
	}
	texts := f.query(workspace.QueryPatientWorkCommand{Identity: reader, Scope: workspace.PatientWorkEveryone, Filter: workspace.PatientWorkTexts})
	if row := patientRow(t, texts, texted); row.Top.Preview != "Running ten minutes late" {
		t.Fatalf("text row preview = %q, want the newest inbound text", row.Top.Preview)
	}

	south := f.query(workspace.QueryPatientWorkCommand{Identity: reader, Scope: workspace.PatientWorkEveryone, LocationID: f.locations["south"]})
	assertPatients(t, "south office", south, everything, missed, appointed, shared)
	if row := patientRow(t, south, shared); row.OpenCount != 1 || row.DisplayName != "Rita Okafor" {
		t.Fatalf("shared phone at one office = %+v, want only that office's Task and name", row)
	}
	if _, err := f.reads.QueryPatientWork(f.ctx, workspace.QueryPatientWorkCommand{
		Identity: reader, PracticeID: f.practiceID, LocationID: f.locations["hidden"],
	}); !errors.Is(err, workspace.ErrDenied) {
		t.Fatalf("hidden office error = %v, want denied", err)
	}
	stranger := access.Identity{Subject: "patient-work-stranger", Email: "stranger@patient-work.test", EmailVerified: true}
	if _, err := f.reads.QueryPatientWork(f.ctx, workspace.QueryPatientWorkCommand{
		Identity: stranger, PracticeID: f.practiceID,
	}); !errors.Is(err, workspace.ErrDenied) {
		t.Fatalf("stranger error = %v, want denied", err)
	}
	for _, command := range []workspace.QueryPatientWorkCommand{
		{Scope: "team"}, {Filter: "calls"}, {Limit: 51}, {Cursor: "not-a-cursor"},
	} {
		command.Identity, command.PracticeID = reader, f.practiceID
		if _, err := f.reads.QueryPatientWork(f.ctx, command); !errors.Is(err, workspace.ErrInvalidInput) {
			t.Fatalf("command %+v error = %v, want invalid input", command, err)
		}
	}
}

func TestPatientWorkMineShowsOwnedSharedAndUnownedWork(t *testing.T) {
	f := newPatientWorkFixture(t)
	reader := f.activate("reader")
	owner := f.activate("owner")
	former := f.activate("former")
	f.activate("elsewhere")
	f.activate("local")
	formerAuthorization, err := f.accessPort.ResolveActor(f.ctx, former, f.practiceID, "")
	if err != nil {
		t.Fatal(err)
	}
	testaccess.SeedRevokedMembership(t, f.db.Owner, formerAuthorization, f.at)
	f.own("north", "owner", "insurance")
	f.own("north", "former", "medication")
	f.own("north", "local", "optical")
	f.own("north", "elsewhere", "referrals")
	f.own("north", "owner", "appointments")
	const (
		owned       = "+17275550201"
		lapsed      = "+17275550202"
		colleagues  = "+17275550203"
		unlisted    = "+17275550204"
		followUp    = "+17275550205"
		voicemail   = "+17275550206"
		texted      = "+17275550207"
		appointed   = "+17275550208"
		mixed       = "+17275550209"
		uncovered   = "+17275550210"
		otherOffice = "+17275550211"
	)
	ai := func(phone, office, category string) string {
		return f.open(patientTask{origin: work.TaskOriginAbitaAI, office: office, phone: phone, category: category})
	}
	ai(owned, "north", "insurance")
	ai(lapsed, "north", "medication")
	ai(colleagues, "north", "optical")
	ai(unlisted, "north", "documentation")
	f.open(patientTask{origin: work.TaskOriginHumanCallFollowUp, office: "north", phone: followUp})
	f.open(patientTask{origin: work.TaskOriginVoicemail, office: "north", phone: voicemail, voicemail: 9000})
	f.open(patientTask{origin: work.TaskOriginInboundMessageReview, office: "north", phone: texted})
	f.open(patientTask{origin: work.TaskOriginAppointmentReview, office: "north", phone: appointed})
	ai(mixed, "north", "optical")
	mixedText := f.open(patientTask{origin: work.TaskOriginInboundMessageReview, office: "north", phone: mixed})
	ai(uncovered, "north", "referrals")
	ai(otherOffice, "south", "insurance")

	everyone := f.query(workspace.QueryPatientWorkCommand{Identity: reader, Scope: workspace.PatientWorkEveryone})
	if len(everyone.Rows) != 11 || everyone.Totals.Patients != 11 || everyone.Totals.OpenTasks != 12 || everyone.Totals.PatientsOutsideScope != 0 {
		t.Fatalf("Everyone = %v %+v, want all 11 patients and 12 open Tasks", patientPhones(everyone), everyone.Totals)
	}
	unowned := []string{lapsed, unlisted, followUp, uncovered, otherOffice}
	assertPatients(t, "No owner under Everyone", f.query(workspace.QueryPatientWorkCommand{
		Identity: reader, Scope: workspace.PatientWorkEveryone, Filter: workspace.PatientWorkUnowned,
	}), unowned...)
	for _, row := range everyone.Rows {
		if row.HasUnowned != slices.Contains(unowned, row.Phone) {
			t.Fatalf("patient %s hasUnowned = %t", row.Phone, row.HasUnowned)
		}
	}

	shared := []string{lapsed, unlisted, followUp, voicemail, texted, appointed, mixed, uncovered, otherOffice}
	mine := f.query(workspace.QueryPatientWorkCommand{Identity: reader})
	assertPatients(t, "My Tasks for a reader who owns nothing", mine, shared...)
	if mine.Totals != (workspace.PatientWorkTotals{OpenTasks: 9, Patients: 9, PatientsOutsideScope: 2}) {
		t.Fatalf("My Tasks totals = %+v, want the 9 open Tasks and 9 patients in scope with 2 patients outside", mine.Totals)
	}
	if mine.Counts != (workspace.PatientWorkCounts{All: 9, FollowUp: 5, CallBack: 1, Texts: 2, Appointments: 1, Unowned: 5}) {
		t.Fatalf("My Tasks counts = %+v", mine.Counts)
	}
	assertPatients(t, "Tasks under My Tasks", f.query(workspace.QueryPatientWorkCommand{
		Identity: reader, Filter: workspace.PatientWorkFollowUp,
	}), lapsed, unlisted, followUp, uncovered, otherOffice)
	everyoneTasks := f.query(workspace.QueryPatientWorkCommand{
		Identity: reader, Scope: workspace.PatientWorkEveryone, Filter: workspace.PatientWorkFollowUp,
	})
	assertPatients(t, "Tasks under Everyone", everyoneTasks, owned, lapsed, colleagues, unlisted, followUp, mixed, uncovered, otherOffice)
	if everyoneTasks.Counts.FollowUp != 8 {
		t.Fatalf("Tasks count under Everyone = %d, want 8 patients", everyoneTasks.Counts.FollowUp)
	}
	if row := patientRow(t, mine, mixed); row.Top.ID != mixedText || row.OpenCount != 2 {
		t.Fatalf("patient with a text and a colleague's Task = %+v, want the text on top and both counted", row)
	}

	ownerMine := f.query(workspace.QueryPatientWorkCommand{Identity: owner})
	assertPatients(t, "My Tasks for the insurance owner", ownerMine, append([]string{owned}, shared...)...)
	if ownerMine.Totals != (workspace.PatientWorkTotals{OpenTasks: 10, Patients: 10, PatientsOutsideScope: 1}) ||
		ownerMine.Counts.All != 10 || ownerMine.Counts.FollowUp != 6 || ownerMine.Counts.Unowned != 5 {
		t.Fatalf("owner totals and counts = %+v %+v, want 10 patients in scope, one outside, 6 with a follow-up and the same No owner count", ownerMine.Totals, ownerMine.Counts)
	}

	legacy, err := f.reads.QueryTasks(f.ctx, workspace.QueryTasksCommand{
		Identity: reader, PracticeID: f.practiceID, Responsibility: "mine", Kind: "follow_up", Ordering: work.TaskOrderingRecent,
	})
	if err != nil {
		t.Fatal(err)
	}
	legacyPhones := []string{}
	for _, task := range legacy.Items {
		legacyPhones = append(legacyPhones, task.Phone)
	}
	slices.Sort(legacyPhones)
	if want := []string{lapsed, unlisted, followUp, uncovered, otherOffice}; !slices.Equal(legacyPhones, want) {
		t.Fatalf("Task query mine = %v, want the same ownership rule %v", legacyPhones, want)
	}
}

func TestPatientWorkSearchesAndPagesWithoutGapsOrDuplicates(t *testing.T) {
	f := newPatientWorkFixture(t)
	reader := f.activate("reader")
	want := []string{}
	for index := range 7 {
		phone := fmt.Sprintf("+1727555030%d", index)
		task := patientTask{origin: work.TaskOriginAbitaAI, office: "north", phone: phone, category: "other", callerName: fmt.Sprintf("Patient %d", index)}
		if index == 2 || index == 5 {
			task.urgency = "high_priority"
		}
		if index == 4 {
			task.callerName = "Dolores Reyes"
		}
		f.open(task)
		want = append(want, phone)
	}
	f.open(patientTask{origin: work.TaskOriginInboundMessageReview, office: "south", phone: want[0]})
	want = []string{want[5], want[2], want[0], want[6], want[4], want[3], want[1]}

	command := workspace.QueryPatientWorkCommand{Identity: reader, Scope: workspace.PatientWorkEveryone, Limit: 3}
	got := []string{}
	pages := 0
	for {
		page := f.query(command)
		pages++
		got = append(got, patientPhones(page)...)
		if page.Counts.All != 7 || page.Totals.OpenTasks != 8 {
			t.Fatalf("page %d counts and totals = %+v %+v", pages, page.Counts, page.Totals)
		}
		if page.NextCursor == "" || len(got) > len(want) {
			break
		}
		command.Cursor = page.NextCursor
	}
	if pages != 3 || !reflect.DeepEqual(got, want) {
		t.Fatalf("paged patients = %v in %d pages, want %v in 3", got, pages, want)
	}
	if _, err := f.reads.QueryPatientWork(f.ctx, workspace.QueryPatientWorkCommand{
		Identity: reader, PracticeID: f.practiceID, Scope: workspace.PatientWorkEveryone,
		Filter: workspace.PatientWorkTexts, Cursor: command.Cursor,
	}); !errors.Is(err, workspace.ErrInvalidInput) {
		t.Fatalf("cursor reused under another filter = %v, want invalid input", err)
	}

	for _, search := range []string{"dolores", "(727) 555-0304"} {
		page := f.query(workspace.QueryPatientWorkCommand{Identity: reader, Scope: workspace.PatientWorkEveryone, Search: search})
		assertPatients(t, "search "+search, page, "+17275550304")
		if page.Counts.All != 1 || page.Totals.Patients != 7 || page.Totals.OpenTasks != 8 {
			t.Fatalf("search %q counts and totals = %+v %+v, want counts narrowed and totals whole", search, page.Counts, page.Totals)
		}
	}
	if page := f.query(workspace.QueryPatientWorkCommand{Identity: reader, Scope: workspace.PatientWorkEveryone, Search: "nobody by this name"}); len(page.Rows) != 0 || page.Counts.All != 0 {
		t.Fatalf("search without a match = %v", patientPhones(page))
	}
}
