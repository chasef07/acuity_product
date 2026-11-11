package workspace_test

import (
	"context"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
)

func TestPhoneHistoryKeepsCallAndFollowUpTogetherBeforePagination(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(db.As(testdb.Portal), func() time.Time { return now })
	_, err := access.New(pool, func() time.Time { return now }).Provision(ctx, access.Provisioning{
		Environment: "test", RequestedBy: "call-history-test",
		Practices: []access.PracticeProvision{{Key: "history", Name: "History", Locations: []access.LocationProvision{{Key: "office", Name: "Office", AbitaOfficeKeys: []string{"history-office"}}, {Key: "hidden", Name: "Hidden office"}}, AccessGrants: []access.AccessGrantProvision{{Key: "staff", Email: "staff@history.test", Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"office"}}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := access.Identity{Subject: "history-staff", Email: "staff@history.test", EmailVerified: true}
	authorization := testaccess.Activate(t, accessModule, identity)
	locationID := authorization.Locations[0].ID
	phone := "+12025550123"
	callID := insertRecoveryCall(t, pool, authorization, locationID, phone, now)
	var sourceCallID string
	if err := pool.QueryRow(ctx, `SELECT handoff.source_call_id FROM human_calling_calls call JOIN human_calling_handoffs handoff ON handoff.id = call.source_handoff_id WHERE call.id = $1`, callID).Scan(&sourceCallID); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO ai_interactions (service_subject, practice_id, location_id, source_call_id, phone, office_phone, started_at, ended_at, status, appointment_outcome, lifecycle_stage) VALUES ('history-ai', $1, $2, $3, $4, '+12025550100', $5, $6, 'ESCALATED', 'BOOKING', 3)`, authorization.Practice.ID, locationID, sourceCallID, phone, now.Add(-time.Minute), now)
	if err != nil {
		t.Fatal(err)
	}
	writes := work.New(db.As(testdb.Portal), accessModule, func() time.Time { return now.Add(time.Minute) })
	task, _, err := writes.CreateAITask(ctx, work.CreateAITaskCommand{
		Service:   access.ServiceIdentity{Subject: "history-ai", PracticeID: authorization.Practice.ID, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityCreateTask}},
		OfficeKey: "history-office", OfficePhone: "+12025550100", SourceCallID: sourceCallID, IdempotencyKey: "history-task", Phone: phone, Summary: "Confirm instructions", Message: "Caller needs instructions after booking", Category: work.TaskCategoryDocumentation, Urgency: work.TaskUrgencyNormal,
	})
	if err != nil {
		t.Fatal(err)
	}
	reads := workspace.New(db.As(testdb.Portal), accessModule)
	page, err := reads.QueryPhoneTimeline(ctx, workspace.QueryPhoneTimelineCommand{Identity: identity, PracticeID: authorization.Practice.ID, Phone: phone, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Type != "CALL_HISTORY" || page.NextCursor != "" {
		t.Fatalf("one linked call should occupy one complete page item, got types/count/cursor: %#v", page)
	}
	if len(page.Items[0].Entries) != 3 {
		t.Fatalf("call history should retain AI, transfer, and Task evidence, got %d", len(page.Items[0].Entries))
	}
	originalID := page.Items[0].ID
	var booking, transfer, followUp bool
	for _, entry := range page.Items[0].Entries {
		booking = booking || entry.AIInteraction.AppointmentOutcome == "BOOKING"
		transfer = transfer || entry.Call.ID == callID
		followUp = followUp || entry.Task.ID == task.ID
	}
	if !booking || !transfer || !followUp {
		t.Fatal("group omitted a call outcome or linked Task")
	}

	olderTime := now.Add(-time.Hour)
	olderCallID := insertRecoveryCall(t, pool, authorization, locationID, phone, olderTime)
	first, err := reads.QueryPhoneTimeline(ctx, workspace.QueryPhoneTimelineCommand{Identity: identity, PracticeID: authorization.Practice.ID, Phone: phone, Limit: 1})
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != originalID || first.NextCursor == "" {
		t.Fatalf("first whole-call page: %v, %v", first, err)
	}
	older, err := reads.QueryPhoneTimeline(ctx, workspace.QueryPhoneTimelineCommand{Identity: identity, PracticeID: authorization.Practice.ID, Phone: phone, Limit: 1, Cursor: first.NextCursor})
	if err != nil || len(older.Items) != 1 || older.NextCursor != "" || older.Items[0].Entries[0].Call.ID != olderCallID {
		t.Fatalf("older whole-call page: %v, %v", older, err)
	}
	olderID := older.Items[0].ID

	var olderSource string
	if err := pool.QueryRow(ctx, `SELECT handoff.source_call_id FROM human_calling_calls call JOIN human_calling_handoffs handoff ON handoff.id = call.source_handoff_id WHERE call.id = $1`, olderCallID).Scan(&olderSource); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO ai_interactions (service_subject, practice_id, location_id, source_call_id, phone, office_phone, started_at, ended_at, status, lifecycle_stage) VALUES ('history-ai', $1, $2, $3, $4, '+12025550100', $5, $6, 'ESCALATED', 3)`, authorization.Practice.ID, locationID, olderSource, phone, olderTime.Add(-time.Minute), olderTime); err != nil {
		t.Fatal(err)
	}
	older, err = reads.QueryPhoneTimeline(ctx, workspace.QueryPhoneTimelineCommand{Identity: identity, PracticeID: authorization.Practice.ID, Phone: phone, Limit: 1, Cursor: first.NextCursor})
	if err != nil || older.Items[0].ID != olderID || len(older.Items[0].Entries) != 2 {
		t.Fatalf("late closeout changed identity or lost evidence: %v, %v", older, err)
	}

	completed, err := writes.CompleteTask(ctx, work.CompleteTaskCommand{Reason: work.CloseReasonDone, Identity: identity, TaskID: task.ID, ExpectedVersion: task.Version})
	if err != nil {
		t.Fatal(err)
	}
	all, err := reads.QueryPhoneTimeline(ctx, workspace.QueryPhoneTimelineCommand{Identity: identity, PracticeID: authorization.Practice.ID, Phone: phone})
	if err != nil || len(all.Items) != 3 {
		t.Fatalf("completion history count: %v, %v", all, err)
	}
	if all.Items[2].TaskActivity != "TASK_COMPLETED" || all.Items[2].Task.ID != completed.ID {
		t.Fatal("completion was swallowed into the old call")
	}
	if all.Items[1].ID != originalID {
		t.Fatal("Task completion changed call identity")
	}

	var hiddenLocationID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM access_locations WHERE practice_id = $1 AND name = 'Hidden office'`, authorization.Practice.ID).Scan(&hiddenLocationID); err != nil {
		t.Fatal(err)
	}
	insertRecoveryCall(t, pool, authorization, hiddenLocationID, phone, now.Add(time.Hour))
	scoped, err := reads.QueryPhoneTimeline(ctx, workspace.QueryPhoneTimelineCommand{Identity: identity, PracticeID: authorization.Practice.ID, Phone: phone})
	if err != nil || len(scoped.Items) != 3 {
		t.Fatalf("unauthorized Location changed history: %v, %v", scoped, err)
	}

	recoveryPhone := "+12025550124"
	recoveryCall := insertRecoveryCall(t, pool, authorization, locationID, recoveryPhone, now)
	voicemailCall := insertRecoveryCall(t, pool, authorization, locationID, recoveryPhone, now.Add(time.Minute))
	var recoveryTaskID string
	for index, id := range []string{recoveryCall, voicemailCall} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		outcome := work.RecoveryOutcomeMissedCall
		if index == 1 {
			outcome = work.RecoveryOutcomeVoicemail
		}
		recovery, err := writes.EnsureRecoveryTask(ctx, tx, work.EnsureRecoveryTaskCommand{
			CallID: id, PracticeID: authorization.Practice.ID, LocationID: locationID,
			Phone: recoveryPhone, CallerName: "Synthetic caller", Outcome: outcome,
			OccurredAt: now.Add(time.Duration(index) * time.Minute),
		})
		if err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		if index == 1 && recovery.ID != recoveryTaskID {
			t.Fatal("fixture did not attach both calls to one recovery Task")
		}
		recoveryTaskID = recovery.ID
	}
	recoveryHistory, err := reads.QueryPhoneTimeline(ctx, workspace.QueryPhoneTimelineCommand{Identity: identity, PracticeID: authorization.Practice.ID, Phone: recoveryPhone})
	if err != nil || len(recoveryHistory.Items) != 2 {
		t.Fatalf("recovery history added bookkeeping rows: %v, %v", recoveryHistory, err)
	}
	for _, history := range recoveryHistory.Items {
		linked := false
		for _, entry := range history.Entries {
			linked = linked || entry.Task.ID == recoveryTaskID
		}
		if !linked {
			t.Fatal("recovery Task link missing from a related call")
		}
	}

}

func TestPhoneHistoryNamesCallbackTaskVoicemailLengthAndTextSender(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(db.As(testdb.Portal), func() time.Time { return now })
	_, err := access.New(pool, func() time.Time { return now }).Provision(ctx, access.Provisioning{
		Environment: "test", RequestedBy: "call-line-test",
		Practices: []access.PracticeProvision{
			{Key: "lines", Name: "Lines", Locations: []access.LocationProvision{{Key: "office", Name: "Office"}, {Key: "hidden", Name: "Hidden office"}}, AccessGrants: []access.AccessGrantProvision{
				{Key: "staff", Email: "staff@lines.test", Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"office"}},
				{Key: "hidden-staff", Email: "hidden@lines.test", Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"hidden"}},
				{Key: "leaver", Email: "leaver@lines.test", Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"office"}},
			}},
			{Key: "elsewhere", Name: "Elsewhere", Locations: []access.LocationProvision{{Key: "office", Name: "Elsewhere office"}}, AccessGrants: []access.AccessGrantProvision{
				{Key: "staff", Email: "staff@elsewhere.test", Role: access.RoleStaff, LocationScope: access.LocationScopeAll},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	leaver := access.Identity{Subject: "lines-leaver", Email: "leaver@lines.test", EmailVerified: true}
	testaccess.Activate(t, accessModule, leaver)
	if _, err := pool.Exec(ctx, `UPDATE access_memberships SET revoked_at = $2 WHERE user_subject = $1`, leaver.Subject, now); err != nil {
		t.Fatalf("revoke membership: %v", err)
	}
	outsider := access.Identity{Subject: "elsewhere-staff", Email: "staff@elsewhere.test", EmailVerified: true}
	elsewhere := testaccess.Activate(t, accessModule, outsider)
	operator := access.Identity{Subject: "lines-operator", Email: "operator@platform.test", EmailVerified: true}
	if _, err := pool.Exec(ctx, `INSERT INTO access_platform_operators(email,user_subject) VALUES($1,$2)`, operator.Email, operator.Subject); err != nil {
		t.Fatalf("insert platform operator: %v", err)
	}
	identity := access.Identity{Subject: "lines-staff", Email: "staff@lines.test", EmailVerified: true}
	authorization := testaccess.Activate(t, accessModule, identity)
	hiddenIdentity := access.Identity{Subject: "lines-hidden-staff", Email: "hidden@lines.test", EmailVerified: true}
	hiddenAuthorization := testaccess.Activate(t, accessModule, hiddenIdentity)
	locationID := authorization.Locations[0].ID
	hiddenLocationID := hiddenAuthorization.Locations[0].ID
	phone := "+12025550125"
	writes := work.New(db.As(testdb.Portal), accessModule, func() time.Time { return now })

	recoveryTask := func(owner access.Authorization, location string, outcome work.RecoveryOutcome, at time.Time) (string, work.Task) {
		t.Helper()
		callID := insertRecoveryCall(t, pool, owner, location, phone, at)
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		task, err := writes.EnsureRecoveryTask(ctx, tx, work.EnsureRecoveryTaskCommand{
			CallID: callID, PracticeID: owner.Practice.ID, LocationID: location,
			Phone: phone, CallerName: "Synthetic caller", Outcome: outcome, OccurredAt: at,
		})
		if err != nil {
			_ = tx.Rollback(ctx)
			t.Fatal(err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		return callID, task
	}
	placeCallback := func(key string, entryPoint string, taskID any, subject string, at time.Time) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO human_calling_calls (
				practice_id, location_id, provider_termination, terminal_outcome, ended_at,
				direction, entry_point, task_id, destination_phone, outbound_caller_id,
				initiating_subject, outbound_idempotency_key, outbound_input_fingerprint,
				created_at, updated_at
			) VALUES ($1, $2, 'NO_ANSWER', 'UNANSWERED', $3, 'OUTBOUND', $10, $4, $5,
				'+12025550100', $6, $7, $8, $9, $3)
			RETURNING id::text
		`, authorization.Practice.ID, locationID, at.Add(30*time.Second), taskID, phone,
			subject, key, make([]byte, 32), at, entryPoint).Scan(&id); err != nil {
			t.Fatalf("place callback %s: %v", key, err)
		}
		return id
	}
	sendText := func(location string, kind string, subject string, at time.Time) string {
		t.Helper()
		var threadID, id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO messaging_threads(practice_id,location_id,office_phone,external_phone)
			VALUES($1,$2,'+12025550100',$3)
			ON CONFLICT(practice_id,location_id,office_phone,external_phone)
			DO UPDATE SET updated_at=messaging_threads.updated_at RETURNING id::text
		`, authorization.Practice.ID, location, phone).Scan(&threadID); err != nil {
			t.Fatal(err)
		}
		if err := pool.QueryRow(ctx, `
			INSERT INTO messaging_messages(thread_id,practice_id,location_id,direction,body,sender,destination,delivery_state,created_by_kind,created_by_subject,created_at)
			VALUES($1,$2,$3,'OUTBOUND','Synthetic text','+12025550100',$4,'DELIVERED',$5,$6,$7)
			RETURNING id::text
		`, threadID, authorization.Practice.ID, location, phone, kind, subject, at).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}

	leaveVoicemail := func(callID string, taskID string, millis int, at time.Time) {
		t.Helper()
		if _, err := pool.Exec(ctx, `
			INSERT INTO human_calling_voicemails (
				call_id, practice_id, location_id, task_id, outcome, audio_state,
				provider_recording_id, recording_started_at, recording_ended_at,
				duration_millis, created_at, updated_at
			) VALUES ($1, $2, $3, $4, 'VOICEMAIL', 'READY', $5, $6, $7, $8, $6, $7)
		`, callID, authorization.Practice.ID, locationID, taskID, "recording-"+callID, at, at.Add(time.Duration(millis)*time.Millisecond), millis); err != nil {
			t.Fatalf("insert voicemail evidence: %v", err)
		}
	}

	voicemailCallID, voicemailTask := recoveryTask(authorization, locationID, work.RecoveryOutcomeVoicemail, now)
	leaveVoicemail(voicemailCallID, voicemailTask.ID, 22400, now)
	roundedUpCallID := insertRecoveryCall(t, pool, authorization, locationID, phone, now.Add(10*time.Second))
	leaveVoicemail(roundedUpCallID, voicemailTask.ID, 22999, now.Add(10*time.Second))
	briefCallID := insertRecoveryCall(t, pool, authorization, locationID, phone, now.Add(20*time.Second))
	leaveVoicemail(briefCallID, voicemailTask.ID, 999, now.Add(20*time.Second))
	if _, err := pool.Exec(ctx, `UPDATE human_calling_calls SET surfaced_task_id = $2 WHERE id = $1`, briefCallID, voicemailTask.ID); err != nil {
		t.Fatalf("surface recovery Task on inbound call: %v", err)
	}
	_, hiddenTask := recoveryTask(authorization, hiddenLocationID, work.RecoveryOutcomeMissedCall, now.Add(time.Minute))
	_, foreignTask := recoveryTask(elsewhere, elsewhere.Locations[0].ID, work.RecoveryOutcomeMissedCall, now.Add(time.Minute))
	callbackID := placeCallback("callback-from-task", "TASK", voicemailTask.ID, identity.Subject, now.Add(2*time.Minute))
	crossLocationCallbackID := placeCallback("callback-from-hidden-task", "TASK", hiddenTask.ID, identity.Subject, now.Add(3*time.Minute))
	crossPracticeCallbackID := placeCallback("callback-from-foreign-task", "TASK", foreignTask.ID, identity.Subject, now.Add(3*time.Minute+time.Second))
	callers := map[string]string{
		placeCallback("callback-by-operator", "STANDALONE", nil, operator.Subject, now.Add(3*time.Minute+10*time.Second)): operator.Email,
		placeCallback("callback-by-leaver", "STANDALONE", nil, leaver.Subject, now.Add(3*time.Minute+20*time.Second)):     leaver.Email,
		placeCallback("callback-by-outsider", "STANDALONE", nil, outsider.Subject, now.Add(3*time.Minute+30*time.Second)): "",
	}
	texts := map[string]string{
		sendText(locationID, "HUMAN", identity.Subject, now.Add(4*time.Minute)):                identity.Email,
		sendText(locationID, "SERVICE", identity.Subject, now.Add(5*time.Minute)):              "",
		sendText(locationID, "HUMAN", operator.Subject, now.Add(5*time.Minute+10*time.Second)): operator.Email,
		sendText(locationID, "HUMAN", leaver.Subject, now.Add(5*time.Minute+20*time.Second)):   leaver.Email,
		sendText(locationID, "HUMAN", outsider.Subject, now.Add(5*time.Minute+30*time.Second)): "",
		sendText(locationID, "HUMAN", "lines-unknown", now.Add(5*time.Minute+40*time.Second)):  "",
	}
	sendText(hiddenLocationID, "HUMAN", hiddenIdentity.Subject, now.Add(6*time.Minute))

	reads := workspace.New(db.As(testdb.Portal), accessModule)
	page, err := reads.QueryPhoneTimeline(ctx, workspace.QueryPhoneTimelineCommand{Identity: identity, PracticeID: authorization.Practice.ID, Phone: phone})
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]workspace.TimelineItem{}
	senders := map[string]string{}
	for _, item := range page.Items {
		for _, entry := range append([]workspace.TimelineItem{item}, item.Entries...) {
			if entry.Type == "CALL" {
				calls[entry.Call.ID] = entry
			}
			if entry.Task.ID == hiddenTask.ID || entry.Task.ID == foreignTask.ID {
				t.Fatal("history linked a Task the reader cannot access")
			}
		}
		if item.Type == "MESSAGE" {
			senders[item.Message.ID] = item.Message.CreatedBy.Email
		}
	}
	for callID, want := range map[string]int64{voicemailCallID: 22, roundedUpCallID: 23, briefCallID: 1} {
		if got := calls[callID].Call; got.VoicemailDurationSeconds != want || got.TaskID != "" || got.TaskTitle != "" {
			t.Fatalf("voicemail call length/Task = %d/%q/%q, want %d seconds and no placed-from Task", got.VoicemailDurationSeconds, got.TaskID, got.TaskTitle, want)
		}
	}
	if got := calls[callbackID].Call; got.TaskID != voicemailTask.ID || got.TaskTitle != voicemailTask.Title || got.TaskTitle == "" || got.VoicemailDurationSeconds != 0 {
		t.Fatalf("callback Task/voicemail = %q/%q/%d, want %q/%q/0", got.TaskID, got.TaskTitle, got.VoicemailDurationSeconds, voicemailTask.ID, voicemailTask.Title)
	}
	for name, callID := range map[string]string{"an inaccessible Location": crossLocationCallbackID, "another Practice": crossPracticeCallbackID} {
		if got, found := calls[callID]; !found || got.Call.TaskID != "" || got.Call.TaskTitle != "" {
			t.Fatalf("callback from a Task in %s = found %v, Task %q/%q; want the call without its Task", name, found, got.Call.TaskID, got.Call.TaskTitle)
		}
	}
	for callID, want := range callers {
		if got, found := calls[callID]; !found || got.Call.PlacedByEmail != want {
			t.Fatalf("callback caller = found %v, %q; want %q", found, got.Call.PlacedByEmail, want)
		}
	}
	if len(senders) != len(texts) {
		t.Fatalf("history returned %d Messages, want only the %d in the accessible Location", len(senders), len(texts))
	}
	for messageID, want := range texts {
		if senders[messageID] != want {
			t.Fatalf("text sender = %q, want %q", senders[messageID], want)
		}
	}
}

func TestPhoneHistoryMarksAnAICallResolvedOnlyFromACompleteResolvedLabel(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	ctx := context.Background()
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(db.As(testdb.Portal), func() time.Time { return now })
	_, err := access.New(pool, func() time.Time { return now }).Provision(ctx, access.Provisioning{
		Environment: "test", RequestedBy: "resolved-label-test",
		Practices: []access.PracticeProvision{{Key: "labels", Name: "Labels", Locations: []access.LocationProvision{{Key: "office", Name: "Office"}}, AccessGrants: []access.AccessGrantProvision{
			{Key: "staff", Email: "staff@labels.test", Role: access.RoleStaff, LocationScope: access.LocationScopeAll},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := access.Identity{Subject: "labels-staff", Email: "staff@labels.test", EmailVerified: true}
	authorization := testaccess.Activate(t, accessModule, identity)
	phone := "+12025550126"
	want := map[string]bool{}
	for index, label := range []struct {
		name, outcome, status string
		resolved              bool
	}{
		{"complete and resolved", "resolved", "complete", true},
		{"complete and abandoned", "abandoned", "complete", false},
		{"complete and transferred", "transferred", "complete", false},
		{"resolved by a label that failed", "resolved", "failed", false},
		{"still pending", "", "pending", false},
		{"never labelled", "", "", false},
	} {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO ai_interactions (service_subject, practice_id, location_id, source_call_id, phone, office_phone, started_at, ended_at, status, lifecycle_stage)
			VALUES ('labels-ai', $1, $2, $3, $4, '+12025550100', $5, $6, 'COMPLETED', 3)
			RETURNING id::text
		`, authorization.Practice.ID, authorization.Locations[0].ID, label.name, phone, now.Add(time.Duration(index)*time.Minute), now.Add(time.Duration(index)*time.Minute+time.Second)).Scan(&id); err != nil {
			t.Fatalf("insert AI Interaction %s: %v", label.name, err)
		}
		if label.status != "" {
			if _, err := pool.Exec(ctx, `
				INSERT INTO ai_interaction_classifications(interaction_id,intent,outcome,status)
				VALUES($1,'office_information',NULLIF($2,''),$3)
			`, id, label.outcome, label.status); err != nil {
				t.Fatalf("label AI Interaction %s: %v", label.name, err)
			}
		}
		want[label.name] = label.resolved
	}
	page, err := workspace.New(db.As(testdb.Portal), accessModule).QueryPhoneTimeline(ctx, workspace.QueryPhoneTimelineCommand{Identity: identity, PracticeID: authorization.Practice.ID, Phone: phone})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, item := range page.Items {
		for _, entry := range append([]workspace.TimelineItem{item}, item.Entries...) {
			if entry.Type == "AI_INTERACTION" {
				got[entry.AIInteraction.SourceCallID] = entry.AIInteraction.Resolved
			}
		}
	}
	if len(got) != len(want) {
		t.Fatalf("history returned %d AI Interactions, want %d", len(got), len(want))
	}
	for name, resolved := range want {
		if got[name] != resolved {
			t.Errorf("%s: resolved = %v, want %v", name, got[name], resolved)
		}
	}
}
