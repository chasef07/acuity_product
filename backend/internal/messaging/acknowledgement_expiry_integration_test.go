package messaging_test

import (
	"context"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/messaging"
)

func TestAutomaticAcknowledgementExpiresWithoutChangingPatientTask(t *testing.T) {
	for _, configuration := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing_sender", true: "worker_delayed"}[configuration], func(t *testing.T) {
			fixture := newAutomaticAcknowledgementTestFixture(t, configuration)
			ctx := context.Background()
			if !configuration {
				for minute := 0; minute < 5; minute++ {
					*fixture.clock = fixture.now.Add(time.Duration(minute) * time.Minute)
					if processed, err := fixture.module.QueueNextTaskAcknowledgement(ctx); err != nil || !processed {
						t.Fatalf("configuration retry at minute %d = %t, %v", minute, processed, err)
					}
					if processed, err := fixture.module.QueueNextTaskAcknowledgement(ctx); err != nil || processed {
						t.Fatalf("retry before due time = %t, %v", processed, err)
					}
				}
			}
			*fixture.clock = fixture.now.Add(5 * time.Minute)
			if processed, err := fixture.module.QueueNextTaskAcknowledgement(ctx); err != nil || !processed {
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
			if err := fixture.module.Provision(ctx, []messaging.LocationProvision{{
				PracticeKey: "automatic-acknowledgement-scenarios", LocationKey: "main",
				Sender: "+17275550100", MessagingProfileID: "automatic-acknowledgement-profile",
			}}); err != nil {
				t.Fatal(err)
			}
			*fixture.clock = fixture.now.Add(24 * time.Hour)
			if processed, err := fixture.module.QueueNextTaskAcknowledgement(ctx); err != nil || processed {
				t.Fatalf("expired acknowledgement replay = %t, %v", processed, err)
			}
			if processed, err := fixture.module.ProcessNextCommand(ctx); err != nil || processed || len(fixture.provider.commands) != 0 {
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
			// Queue near the deadline: the five minutes start at the original
			// acknowledgement intent, not when a worker finally creates its Message.
			*fixture.clock = fixture.now.Add(4 * time.Minute)
			if processed, err := fixture.module.QueueNextTaskAcknowledgement(ctx); err != nil || !processed {
				t.Fatalf("queue acknowledgement = %t, %v", processed, err)
			}
			*fixture.clock = fixture.now.Add(elapsed)
			if processed, err := fixture.module.ProcessNextCommand(ctx); err != nil || !processed {
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
			if processed, err := fixture.module.ProcessNextCommand(ctx); err != nil || processed {
				t.Fatalf("replay terminal command = %t, %v", processed, err)
			}
		})
	}
}

func TestAcknowledgementRetryNeverExtendsItsOriginalDeadline(t *testing.T) {
	fixture := newAutomaticAcknowledgementTestFixture(t, false)
	*fixture.clock = fixture.now.Add(4*time.Minute + 30*time.Second)
	if processed, err := fixture.module.QueueNextTaskAcknowledgement(context.Background()); err != nil || !processed {
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
	if processed, err := fixture.module.ProcessNextCommand(ctx); err != nil || !processed || len(fixture.provider.commands) != 1 {
		t.Fatalf("Staff message after acknowledgement deadline = %t, %v, %d provider calls", processed, err, len(fixture.provider.commands))
	}
	if fixture.provider.commands[0].MessageID != message.ID {
		t.Fatal("provider received the wrong Message")
	}
}
