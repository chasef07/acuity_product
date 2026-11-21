package messaging_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/chasef07/acuity_product/backend/internal/work"
)

func TestAutomaticAcknowledgementExpiresWithoutChangingPatientTask(t *testing.T) {
	for _, configuration := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing_sender", true: "worker_delayed"}[configuration], func(t *testing.T) {
			fixture := newAutomaticAcknowledgementTestFixture(t, configuration)
			ctx := context.Background()
			if !configuration {
				for minute := 0; minute < 5; minute++ {
					*fixture.clock = fixture.now.Add(time.Duration(minute) * time.Minute)
					if processed, err := fixture.worker.QueueNextTaskAcknowledgement(ctx); err != nil || !processed {
						t.Fatalf("configuration retry at minute %d = %t, %v", minute, processed, err)
					}
					if processed, err := fixture.worker.QueueNextTaskAcknowledgement(ctx); err != nil || processed {
						t.Fatalf("retry before due time = %t, %v", processed, err)
					}
				}
			}
			*fixture.clock = fixture.now.Add(5 * time.Minute)
			if processed, err := fixture.worker.QueueNextTaskAcknowledgement(ctx); err != nil || !processed {
				t.Fatalf("expire acknowledgement = %t, %v", processed, err)
			}
			var state, failure, taskState string
			var completed, noRetry, noMessage bool
			if err := fixture.pool.QueryRow(ctx, `
				SELECT acknowledgement.state, COALESCE(acknowledgement.safe_failure_code, ''),
					acknowledgement.completed_at IS NOT NULL,
					acknowledgement.next_attempt_at IS NULL,
					acknowledgement.message_id IS NULL, task.state
				FROM work_task_acknowledgements acknowledgement
				JOIN work_tasks task ON task.id = acknowledgement.task_id
				WHERE task.id = $1
			`, fixture.task.ID).Scan(&state, &failure, &completed, &noRetry, &noMessage, &taskState); err != nil {
				t.Fatal(err)
			}
			wantFailure := "ACKNOWLEDGEMENT_EXPIRED"
			if !configuration {
				wantFailure = "SENDER_CONFIGURATION_UNAVAILABLE"
			}
			if state != "NOT_NEEDED" || failure != wantFailure || !completed || !noRetry || !noMessage || taskState != "OPEN" {
				t.Fatalf("expiry = state %s, reason %s, completed %v, no retry %v, no message %v, Task %s", state, failure, completed, noRetry, noMessage, taskState)
			}
			if err := messaging.New(fixture.pool, nil, nil, nil, messaging.Config{}, nil).Provision(ctx, []messaging.LocationProvision{{
				PracticeKey: "automatic-acknowledgement-scenarios", LocationKey: "main",
				Sender: "+17275550100", MessagingProfileID: "automatic-acknowledgement-profile",
			}}); err != nil {
				t.Fatal(err)
			}
			*fixture.clock = fixture.now.Add(24 * time.Hour)
			if processed, err := fixture.worker.QueueNextTaskAcknowledgement(ctx); err != nil || processed {
				t.Fatalf("expired acknowledgement replay = %t, %v", processed, err)
			}
			if processed, err := fixture.worker.ProcessNextCommand(ctx); err != nil || processed || len(fixture.provider.commands) != 0 {
				t.Fatalf("expired acknowledgement provider effect = %t, %v, %d commands", processed, err, len(fixture.provider.commands))
			}
		})
	}
}

func TestAutomaticAcknowledgementDeadlineAlsoAppliesToQueuedProviderCommand(t *testing.T) {
	for _, elapsed := range []time.Duration{5*time.Minute - time.Microsecond, 5 * time.Minute} {
		t.Run(elapsed.String(), func(t *testing.T) {
			fixture := newAutomaticAcknowledgementTestFixture(t, true)
			ctx := context.Background()
			*fixture.clock = fixture.now.Add(4 * time.Minute)
			if processed, err := fixture.worker.QueueNextTaskAcknowledgement(ctx); err != nil || !processed {
				t.Fatalf("queue acknowledgement = %t, %v", processed, err)
			}
			*fixture.clock = fixture.now.Add(elapsed)
			if processed, err := fixture.worker.ProcessNextCommand(ctx); err != nil || !processed {
				t.Fatalf("process queued acknowledgement = %t, %v", processed, err)
			}
			var delivery, failure, commandState string
			if err := fixture.pool.QueryRow(ctx, `
				SELECT message.delivery_state, COALESCE(message.safe_failure_code, ''), command.state
				FROM messaging_messages message
				JOIN messaging_provider_commands command ON command.message_id = message.id
				WHERE message.task_id = $1
			`, fixture.task.ID).Scan(&delivery, &failure, &commandState); err != nil {
				t.Fatal(err)
			}
			if elapsed < 5*time.Minute {
				if delivery != "SENT" || failure != "" || commandState != "SENT" || len(fixture.provider.commands) != 1 {
					t.Fatalf("timely acknowledgement = %s, %s, %s, %d provider calls", delivery, failure, commandState, len(fixture.provider.commands))
				}
			} else if delivery != "FAILED" || failure != "ACKNOWLEDGEMENT_EXPIRED" || commandState != "FAILED" || len(fixture.provider.commands) != 0 {
				t.Fatalf("expired acknowledgement = %s, %s, %s, %d provider calls", delivery, failure, commandState, len(fixture.provider.commands))
			}
			if processed, err := fixture.worker.ProcessNextCommand(ctx); err != nil || processed {
				t.Fatalf("replay terminal command = %t, %v", processed, err)
			}
		})
	}
}

func TestAcknowledgementRetryNeverExtendsItsOriginalDeadline(t *testing.T) {
	fixture := newAutomaticAcknowledgementTestFixture(t, false)
	*fixture.clock = fixture.now.Add(4*time.Minute + 30*time.Second)
	if processed, err := fixture.worker.QueueNextTaskAcknowledgement(context.Background()); err != nil || !processed {
		t.Fatalf("late configuration retry = %t, %v", processed, err)
	}
	var nextAttempt time.Time
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT next_attempt_at FROM work_task_acknowledgements WHERE task_id = $1
	`, fixture.task.ID).Scan(&nextAttempt); err != nil {
		t.Fatal(err)
	}
	if !nextAttempt.Equal(fixture.now.Add(5 * time.Minute)) {
		t.Fatalf("next attempt = %s; must be at the original five-minute deadline", nextAttempt)
	}
}

func TestAcknowledgementDeadlineDoesNotExpireStaffMessages(t *testing.T) {
	fixture := newAutomaticAcknowledgementTestFixture(t, true)
	ctx := context.Background()
	message, _, err := fixture.module.Send(ctx, messaging.SendCommand{
		Identity: fixture.identity, PracticeID: fixture.practiceID, LocationID: fixture.locationID,
		Destination: fixture.task.Phone, TaskID: fixture.task.ID,
		Body: "Staff is following up.", IdempotencyKey: "staff-message",
	})
	if err != nil {
		t.Fatal(err)
	}
	*fixture.clock = fixture.now.Add(6 * time.Minute)
	if processed, err := fixture.worker.ProcessNextCommand(ctx); err != nil || !processed || len(fixture.provider.commands) != 1 {
		t.Fatalf("Staff message after acknowledgement deadline = %t, %v, %d provider calls", processed, err, len(fixture.provider.commands))
	}
	if fixture.provider.commands[0].MessageID != message.ID {
		t.Fatal("provider received the wrong Message")
	}
}

func TestAutomaticAcknowledgementIsSentOncePerPatientPerDay(t *testing.T) {
	fixture := newAutomaticAcknowledgementTestFixture(t, true)
	ctx := context.Background()
	create := func(key string, category work.TaskCategory) work.Task {
		t.Helper()
		task, status, err := fixture.work.CreateAITask(ctx, work.CreateAITaskCommand{
			Service: access.ServiceIdentity{
				Subject:       "abita-automatic-acknowledgement",
				PracticeID:    fixture.practiceID,
				LocationScope: access.LocationScopeAll,
				Capabilities:  []access.ServiceCapability{access.ServiceCapabilityCreateTask},
			},
			OfficeKey:      "automatic-acknowledgement-scenarios-office",
			OfficePhone:    "+17275919997",
			SourceCallID:   "automatic-acknowledgement-" + key,
			IdempotencyKey: "automatic-acknowledgement-" + key,
			Phone:          "+17275550199",
			Summary:        "Caller needs " + key,
			Message:        "Caller asked about " + key + ".",
			Category:       category,
			Urgency:        work.TaskUrgencyNormal,
		})
		if err != nil || status != work.TaskCreated {
			t.Fatalf("create %s = %q, %v", key, status, err)
		}
		return task
	}
	second := create("medication", work.TaskCategoryMedication)
	third := create("optical", work.TaskCategoryOptical)
	for range 3 {
		if processed, err := fixture.worker.QueueNextTaskAcknowledgement(ctx); err != nil || !processed {
			t.Fatalf("queue acknowledgement = %t, %v", processed, err)
		}
	}
	states := map[string]string{}
	rows, err := fixture.pool.Query(ctx, `
		SELECT task_id::text, state || ':' || COALESCE(safe_failure_code, '')
		FROM work_task_acknowledgements
	`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, state string
		if err := rows.Scan(&id, &state); err != nil {
			t.Fatal(err)
		}
		states[id] = state
	}
	rows.Close()
	outcomes := map[string]int{}
	for _, task := range []work.Task{fixture.task, second, third} {
		outcomes[states[task.ID]]++
	}
	if outcomes["MESSAGE_QUEUED:"] != 1 || outcomes["NOT_NEEDED:RECENTLY_ACKNOWLEDGED"] != 2 {
		t.Fatalf("same-day acknowledgements = %v, want one text and two skipped", outcomes)
	}
	var texts int
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM messaging_messages WHERE direction = 'OUTBOUND'`).Scan(&texts); err != nil {
		t.Fatal(err)
	}
	if texts != 1 {
		t.Fatalf("automatic texts = %d, want 1", texts)
	}

	*fixture.clock = fixture.now.Add(25 * time.Hour)
	next := create("records", work.TaskCategoryDocumentation)
	if processed, err := fixture.worker.QueueNextTaskAcknowledgement(ctx); err != nil || !processed {
		t.Fatalf("queue next-day acknowledgement = %t, %v", processed, err)
	}
	var state string
	if err := fixture.pool.QueryRow(ctx, `SELECT state FROM work_task_acknowledgements WHERE task_id = $1`, next.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "MESSAGE_QUEUED" {
		t.Fatalf("next-day acknowledgement = %s, want a new text", state)
	}
}

func TestCourtesyReplyToAnAutomaticTextOpensNoReview(t *testing.T) {
	fixture := newAutomaticAcknowledgementTestFixture(t, true)
	ctx := context.Background()
	if processed, err := fixture.worker.QueueNextTaskAcknowledgement(ctx); err != nil || !processed {
		t.Fatalf("queue acknowledgement = %t, %v", processed, err)
	}
	reply := func(id, text string, at time.Time) int {
		t.Helper()
		raw := []byte(fmt.Sprintf(
			`{"data":{"record_type":"event","event_type":"message.received","id":"%s","occurred_at":"%s","payload":{"id":"provider-%s","from":{"phone_number":"%s"},"to":[{"phone_number":"+17275550100","status":"webhook_delivered"}],"text":%q}}}`,
			id, at.Format(time.RFC3339), id, fixture.task.Phone, text,
		))
		timestamp := fmt.Sprintf("%d", time.Now().Unix())
		signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
			fixture.privateKey,
			append([]byte(timestamp+"|"), raw...),
		))
		if _, err := fixture.ingress.ReceiveWebhook(ctx, "", raw, timestamp, signature); err != nil {
			t.Fatalf("receive %q: %v", text, err)
		}
		if processed, err := fixture.worker.ProcessNextReceipt(ctx); err != nil || !processed {
			t.Fatalf("project %q = %t, %v", text, processed, err)
		}
		var reviews int
		if err := fixture.pool.QueryRow(ctx, `
			SELECT count(*) FROM work_tasks WHERE origin = 'INBOUND_MESSAGE_REVIEW' AND state = 'OPEN'
		`).Scan(&reviews); err != nil {
			t.Fatal(err)
		}
		return reviews
	}
	for index, text := range []string{"Thanks!", "ok 👍", "Thank you so much.", "👍"} {
		if reviews := reply(fmt.Sprintf("courtesy-%d", index), text, fixture.now.Add(time.Duration(index+1)*time.Minute)); reviews != 0 {
			t.Fatalf("%q after the automatic text opened %d reviews, want none", text, reviews)
		}
	}
	if reviews := reply("question", "Thanks, can I also change my appointment?", fixture.now.Add(10*time.Minute)); reviews != 1 {
		t.Fatalf("a real reply opened %d reviews, want 1", reviews)
	}
}

func TestCourtesyReplyWithNoRecentAutomaticTextStillOpensAReview(t *testing.T) {
	fixture := newAutomaticAcknowledgementTestFixture(t, true)
	ctx := context.Background()
	if processed, err := fixture.worker.QueueNextTaskAcknowledgement(ctx); err != nil || !processed {
		t.Fatalf("queue acknowledgement = %t, %v", processed, err)
	}
	at := fixture.now.Add(25 * time.Hour)
	raw := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"message.received","id":"late-thanks","occurred_at":"%s","payload":{"id":"provider-late-thanks","from":{"phone_number":"%s"},"to":[{"phone_number":"+17275550100","status":"webhook_delivered"}],"text":"Thanks"}}}`,
		at.Format(time.RFC3339), fixture.task.Phone,
	))
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(fixture.privateKey, append([]byte(timestamp+"|"), raw...)))
	if _, err := fixture.ingress.ReceiveWebhook(ctx, "", raw, timestamp, signature); err != nil {
		t.Fatal(err)
	}
	if processed, err := fixture.worker.ProcessNextReceipt(ctx); err != nil || !processed {
		t.Fatalf("project late thanks = %t, %v", processed, err)
	}
	var reviews int
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM work_tasks WHERE origin = 'INBOUND_MESSAGE_REVIEW'`).Scan(&reviews); err != nil {
		t.Fatal(err)
	}
	if reviews != 1 {
		t.Fatalf("thanks a day after the automatic text opened %d reviews, want 1", reviews)
	}
}

func TestStaffTextRepliesCloseCourtesyRouteToTheSenderAndRespectBlocks(t *testing.T) {
	fixture := newAutomaticAcknowledgementTestFixture(t, true)
	ctx := context.Background()
	if processed, err := fixture.worker.QueueNextTaskAcknowledgement(ctx); err != nil || !processed {
		t.Fatalf("queue acknowledgement = %t, %v", processed, err)
	}
	reply := func(id, text, media string, at time.Time) int {
		t.Helper()
		raw := []byte(fmt.Sprintf(
			`{"data":{"record_type":"event","event_type":"message.received","id":"%s","occurred_at":"%s","payload":{"id":"provider-%s","from":{"phone_number":"%s"},"to":[{"phone_number":"+17275550100","status":"webhook_delivered"}],"text":%q%s}}}`,
			id, at.Format(time.RFC3339), id, fixture.task.Phone, text, media,
		))
		timestamp := fmt.Sprintf("%d", time.Now().Unix())
		signature := base64.StdEncoding.EncodeToString(ed25519.Sign(fixture.privateKey, append([]byte(timestamp+"|"), raw...)))
		if _, err := fixture.ingress.ReceiveWebhook(ctx, "", raw, timestamp, signature); err != nil {
			t.Fatalf("receive %q: %v", text, err)
		}
		if processed, err := fixture.worker.ProcessNextReceipt(ctx); err != nil || !processed {
			t.Fatalf("project %q = %t, %v", text, processed, err)
		}
		var reviews int
		if err := fixture.pool.QueryRow(ctx, `
			SELECT count(*) FROM work_tasks WHERE origin = 'INBOUND_MESSAGE_REVIEW' AND state = 'OPEN'
		`).Scan(&reviews); err != nil {
			t.Fatal(err)
		}
		return reviews
	}
	if reviews := reply("photo-thanks", "Thanks", `,"media":[{"url":"https://media.example.test/card.jpg","content_type":"image/jpeg"}]`, fixture.now.Add(time.Minute)); reviews != 1 {
		t.Fatalf("thanks with a photo opened %d reviews, want 1", reviews)
	}
	if _, err := fixture.pool.Exec(ctx, `UPDATE work_tasks SET state = 'COMPLETED', completed_at = $1, completed_by_kind = 'SERVICE', completed_by_subject = 'test', close_reason = 'DONE', version = version + 1 WHERE origin = 'INBOUND_MESSAGE_REVIEW'`, fixture.now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	*fixture.clock = fixture.now.Add(3 * time.Minute)
	if _, _, err := fixture.module.Send(ctx, messaging.SendCommand{
		Identity: fixture.identity, PracticeID: fixture.practiceID, LocationID: fixture.locationID,
		Destination: fixture.task.Phone, Body: "Can you come in at 3pm?", IdempotencyKey: "staff-question",
	}); err != nil {
		t.Fatal(err)
	}
	if reviews := reply("staff-ok", "Thank you 👍", "", fixture.now.Add(4*time.Minute)); reviews != 0 {
		t.Fatalf("thanks after a staff text opened %d reviews, want none", reviews)
	}
	if reviews := reply("staff-answer", "3pm works, see you then", "", fixture.now.Add(5*time.Minute)); reviews != 1 {
		t.Fatalf("a real answer to a staff text opened %d reviews, want 1", reviews)
	}
	var routedTo *string
	if err := fixture.pool.QueryRow(ctx, `SELECT routed_to_subject FROM work_tasks WHERE origin = 'INBOUND_MESSAGE_REVIEW' AND state = 'OPEN'`).Scan(&routedTo); err != nil {
		t.Fatal(err)
	}
	if routedTo == nil || *routedTo != fixture.identity.Subject {
		t.Fatalf("answer to a staff text routed to %v, want the staff member who texted (%s)", routedTo, fixture.identity.Subject)
	}

	if _, err := fixture.pool.Exec(ctx, `UPDATE work_tasks SET state = 'COMPLETED', completed_at = $1, completed_by_kind = 'SERVICE', completed_by_subject = 'test', close_reason = 'DONE', version = version + 1 WHERE origin = 'INBOUND_MESSAGE_REVIEW' AND state = 'OPEN'`, fixture.now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	*fixture.clock = fixture.now.Add(6 * time.Minute)
	if _, _, err := fixture.module.Send(ctx, messaging.SendCommand{
		Identity: fixture.identity, PracticeID: fixture.practiceID, LocationID: fixture.locationID,
		Destination: fixture.task.Phone, Body: "STOP", IdempotencyKey: "staff-stop",
	}); err != nil {
		t.Fatal(err)
	}
	if reviews := reply("opt-out-confirmation", "You are now opted out. No more messages will be sent.", "", fixture.now.Add(7*time.Minute)); reviews != 0 {
		t.Fatalf("the opt-out confirmation after the office texted STOP opened %d reviews, want none", reviews)
	}

	block := func(blocked bool) {
		t.Helper()
		got, err := fixture.module.SetSenderBlocked(ctx, messaging.BlockSenderCommand{
			Identity: fixture.identity, PracticeID: fixture.practiceID, LocationID: fixture.locationID,
			Phone: fixture.task.Phone, Blocked: blocked,
		})
		if err != nil || got != blocked {
			t.Fatalf("set blocked %t = %t, %v", blocked, got, err)
		}
		read, err := fixture.module.SenderBlocked(ctx, fixture.identity, fixture.practiceID, fixture.task.Phone)
		if err != nil || read != blocked {
			t.Fatalf("read blocked = %t, %v, want %t", read, err, blocked)
		}
	}
	if reviews := reply("before-block", "Congrats! You won a gift card", "", fixture.now.Add(25*time.Hour)); reviews != 1 {
		t.Fatalf("spam before blocking opened %d reviews, want 1", reviews)
	}
	*fixture.clock = fixture.now.Add(25*time.Hour + time.Minute)
	block(true)
	var stops int
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM messaging_messages WHERE direction = 'OUTBOUND' AND body = 'STOP' AND created_by_kind = 'HUMAN'`).Scan(&stops); err != nil || stops != 2 {
		t.Fatalf("outbound STOP texts = %d, %v, want the staff one and one for the block", stops, err)
	}
	block(true)
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM messaging_messages WHERE direction = 'OUTBOUND' AND body = 'STOP'`).Scan(&stops); err != nil || stops != 2 {
		t.Fatalf("blocking again sent another STOP: %d, %v", stops, err)
	}
	var reason string
	if err := fixture.pool.QueryRow(ctx, `SELECT close_reason FROM work_tasks WHERE origin = 'INBOUND_MESSAGE_REVIEW' ORDER BY created_at DESC LIMIT 1`).Scan(&reason); err != nil || reason != "NOT_NEEDED" {
		t.Fatalf("blocking closed the open review as %q, %v, want NOT_NEEDED", reason, err)
	}
	if reviews := reply("spam", "WIN A FREE CRUISE http://spam.example.test", "", fixture.now.Add(26*time.Hour)); reviews != 0 {
		t.Fatalf("a blocked sender opened %d reviews, want none", reviews)
	}
	var stored int
	if err := fixture.pool.QueryRow(ctx, `SELECT count(*) FROM messaging_messages WHERE direction = 'INBOUND' AND body LIKE 'WIN A FREE%'`).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("blocked text stored %d times, %v, want it kept as evidence", stored, err)
	}
	block(false)
	if reviews := reply("after-unblock", "Sorry, wrong number earlier", "", fixture.now.Add(50*time.Hour)); reviews != 1 {
		t.Fatalf("after unblocking a text opened %d reviews, want 1", reviews)
	}
}

func TestFailedAcknowledgementDoesNotBlockTheNextOne(t *testing.T) {
	fixture := newAutomaticAcknowledgementTestFixture(t, true)
	ctx := context.Background()
	if processed, err := fixture.worker.QueueNextTaskAcknowledgement(ctx); err != nil || !processed {
		t.Fatalf("queue acknowledgement = %t, %v", processed, err)
	}
	if _, err := fixture.pool.Exec(ctx, `UPDATE messaging_messages SET delivery_state = 'FAILED', safe_failure_code = 'PROVIDER_REJECTED' WHERE direction = 'OUTBOUND'`); err != nil {
		t.Fatal(err)
	}
	next, status, err := fixture.work.CreateAITask(ctx, work.CreateAITaskCommand{
		Service: access.ServiceIdentity{
			Subject: "abita-automatic-acknowledgement", PracticeID: fixture.practiceID,
			LocationScope: access.LocationScopeAll,
			Capabilities:  []access.ServiceCapability{access.ServiceCapabilityCreateTask},
		},
		OfficeKey: "automatic-acknowledgement-scenarios-office", OfficePhone: "+17275919997",
		SourceCallID: "after-failure", IdempotencyKey: "after-failure", Phone: fixture.task.Phone,
		Summary: "Caller needs records", Message: "Caller asked about records.",
		Category: work.TaskCategoryDocumentation, Urgency: work.TaskUrgencyNormal,
	})
	if err != nil || status != work.TaskCreated {
		t.Fatalf("create next Task = %q, %v", status, err)
	}
	if processed, err := fixture.worker.QueueNextTaskAcknowledgement(ctx); err != nil || !processed {
		t.Fatalf("queue next acknowledgement = %t, %v", processed, err)
	}
	var state string
	if err := fixture.pool.QueryRow(ctx, `SELECT state FROM work_task_acknowledgements WHERE task_id = $1`, next.ID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "MESSAGE_QUEUED" {
		t.Fatalf("acknowledgement after a failed one = %s, want a new text", state)
	}
}
