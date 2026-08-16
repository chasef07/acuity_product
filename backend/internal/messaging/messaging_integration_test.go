package messaging_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
	"github.com/google/uuid"
)

func TestCreateFollowUpTaskKeepsDistinctMessageThreadsSeparate(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 16, 11, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "message-thread-task-test",
		Practices: []access.PracticeProvision{{
			Key:  "message-thread-task-practice",
			Name: "Message Thread Task Practice",
			Locations: []access.LocationProvision{{
				Key: "main", Name: "Main",
			}},
			AccessGrants: []access.AccessGrantProvision{{
				Key: "staff", Email: "staff@message-thread-task.test",
				Role: access.RoleStaff, LocationScope: access.LocationScopeAll,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision Message Thread Task fixture: %v", err)
	}
	identity := access.Identity{
		Subject: "message-thread-task-staff", Email: "staff@message-thread-task.test",
		EmailVerified: true,
	}
	authorization := testaccess.Activate(t, accessModule, identity)
	workModule := work.New(pool, accessModule, func() time.Time { return now })
	module := messaging.New(
		pool, accessModule, workModule, nil, messaging.Config{}, func() time.Time { return now },
	)

	messageIDs := make([]string, 0, 2)
	for index, officePhone := range []string{"+17275550100", "+17275550101"} {
		threadID := uuid.NewString()
		messageID := uuid.NewString()
		if _, err := pool.Exec(context.Background(), `
			INSERT INTO messaging_threads (
				id, practice_id, location_id, office_phone, external_phone,
				created_at, updated_at
			) VALUES ($1, $2, $3, $4, '+17275550199', $5, $5)
		`, threadID, authorization.Practice.ID, authorization.Locations[0].ID,
			officePhone, now.Add(time.Duration(index)*time.Minute)); err != nil {
			t.Fatalf("insert Message Thread %d: %v", index, err)
		}
		if _, err := pool.Exec(context.Background(), `
			INSERT INTO messaging_messages (
				id, thread_id, practice_id, location_id, direction, body,
				sender, destination, delivery_state, created_by_subject,
				created_at, updated_at
			) VALUES ($1, $2, $3, $4, 'OUTBOUND', 'Please follow up',
				$5, '+17275550199', 'SENT', $6, $7, $7)
		`, messageID, threadID, authorization.Practice.ID,
			authorization.Locations[0].ID, officePhone, identity.Subject,
			now.Add(time.Duration(index)*time.Minute)); err != nil {
			t.Fatalf("insert Message %d: %v", index, err)
		}
		messageIDs = append(messageIDs, messageID)
	}

	created := make([]work.Task, 0, len(messageIDs))
	for _, messageID := range messageIDs {
		task, status, err := module.CreateFollowUpTask(
			context.Background(),
			messaging.CreateFollowUpTaskCommand{Identity: identity, MessageID: messageID},
		)
		if err != nil || status != work.TaskCreated {
			t.Fatalf("create Message follow-up for %q = %#v, %q, %v", messageID, task, status, err)
		}
		created = append(created, task)
	}
	if created[0].ID == created[1].ID ||
		created[0].MessageThreadID == created[1].MessageThreadID {
		t.Fatalf("distinct Message Threads shared follow-up Tasks: %#v", created)
	}
}

func TestSendCommitsOneLocationScopedMessageBeforeProviderContact(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "slice-5-send-test",
		Practices: []access.PracticeProvision{{
			Key:  "message-practice",
			Name: "Message Practice",
			Locations: []access.LocationProvision{{
				Key:             "message-office",
				Name:            "Message Office",
				AbitaOfficeKeys: []string{"message-office"},
			}, {
				Key:  "message-office-two",
				Name: "Message Office Two",
			}},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "message-staff",
				Email:         "staff@message.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}, {
				Key:           "message-staff-two",
				Email:         "staff-two@message.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision Access fixture: %v", err)
	}
	identity := access.Identity{
		Subject:       "message-staff-subject",
		Email:         "staff@message.test",
		EmailVerified: true,
	}
	authorization := testaccess.Activate(t, accessModule, identity)
	secondIdentity := access.Identity{
		Subject:       "message-staff-two-subject",
		Email:         "staff-two@message.test",
		EmailVerified: true,
	}
	testaccess.Activate(t, accessModule, secondIdentity)
	provider := &providerFixture{}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("create webhook key: %v", err)
	}
	workModule := work.New(pool, accessModule, func() time.Time { return now })
	module := messaging.New(
		pool,
		accessModule,
		workModule,
		provider,
		messaging.Config{
			WebhookPublicKeys: []ed25519.PublicKey{publicKey},
			WebhookTolerance:  time.Minute,
		},
		func() time.Time { return now },
	)
	reads := workspace.New(pool, accessModule)
	if err := module.Provision(context.Background(), []messaging.LocationProvision{{
		PracticeKey:        "message-practice",
		LocationKey:        "message-office",
		Sender:             "+17275550100",
		MessagingProfileID: "profile-message-office",
	}, {
		PracticeKey:        "message-practice",
		LocationKey:        "message-office-two",
		Sender:             "+17275550101",
		MessagingProfileID: "profile-message-office-two",
	}}); err != nil {
		t.Fatalf("provision Messaging fixture: %v", err)
	}
	if err := module.RecoverInterruptedCommands(context.Background()); err != nil {
		t.Fatalf("recover empty interrupted-command set: %v", err)
	}
	var workspaceVersionBefore int
	if err := pool.QueryRow(
		context.Background(),
		`SELECT workspace_version FROM access_practices WHERE id = $1`,
		authorization.Practice.ID,
	).Scan(&workspaceVersionBefore); err != nil {
		t.Fatalf("read workspace version before send: %v", err)
	}
	if _, _, err := module.Send(context.Background(), messaging.SendCommand{
		Identity:       identity,
		PracticeID:     authorization.Practice.ID,
		LocationID:     authorization.Locations[0].ID,
		Destination:    "wrong +1 727 555 0199",
		Body:           "This must fail closed.",
		IdempotencyKey: "message-invalid-destination",
	}); !errors.Is(err, messaging.ErrInvalidInput) {
		t.Fatalf("malformed destination error = %v", err)
	}
	var rejectedMessages, rejectedCommands, workspaceVersionAfterReject int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM messaging_messages),
			(SELECT count(*) FROM messaging_provider_commands),
			(SELECT workspace_version FROM access_practices WHERE id = $1)
	`, authorization.Practice.ID).Scan(
		&rejectedMessages,
		&rejectedCommands,
		&workspaceVersionAfterReject,
	); err != nil {
		t.Fatalf("inspect rejected destination: %v", err)
	}
	if rejectedMessages != 0 ||
		rejectedCommands != 0 ||
		workspaceVersionAfterReject != workspaceVersionBefore {
		t.Fatalf(
			"rejected destination changed state: messages=%d commands=%d version=%d",
			rejectedMessages,
			rejectedCommands,
			workspaceVersionAfterReject,
		)
	}

	first, firstStatus, err := module.Send(context.Background(), messaging.SendCommand{
		Identity:       identity,
		PracticeID:     authorization.Practice.ID,
		LocationID:     authorization.Locations[0].ID,
		Destination:    "(727) 555-0199",
		Body:           "Your records are ready.",
		IdempotencyKey: "message-send-1",
	})
	if err != nil {
		t.Fatalf("send Message: %v", err)
	}
	replayed, replayStatus, err := module.Send(context.Background(), messaging.SendCommand{
		Identity:       identity,
		PracticeID:     authorization.Practice.ID,
		LocationID:     authorization.Locations[0].ID,
		Destination:    "+17275550199",
		Body:           "Your records are ready.",
		IdempotencyKey: "message-send-1",
	})
	if err != nil {
		t.Fatalf("replay Message: %v", err)
	}

	if firstStatus != messaging.MessageCreated ||
		replayStatus != messaging.MessageDuplicate ||
		replayed.ID != first.ID {
		t.Fatalf(
			"send receipts = (%q, %q, %q), want created, duplicate, %q",
			firstStatus,
			replayStatus,
			replayed.ID,
			first.ID,
		)
	}
	if first.Direction != messaging.DirectionOutbound ||
		first.Delivery != messaging.DeliverySending ||
		first.Sender != "+17275550100" ||
		first.Destination != "+17275550199" ||
		first.Thread.ExternalPhone != "+17275550199" ||
		first.Thread.LocationID != authorization.Locations[0].ID {
		t.Fatalf("committed Message = %#v", first)
	}
	if len(provider.commands) != 0 {
		t.Fatalf("provider contacted during browser transaction: %#v", provider.commands)
	}

	var threads, messages, commands, workspaceVersion int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM messaging_threads),
			(SELECT count(*) FROM messaging_messages),
			(SELECT count(*) FROM messaging_provider_commands),
			(SELECT workspace_version FROM access_practices WHERE id = $1)
	`, authorization.Practice.ID).Scan(
		&threads,
		&messages,
		&commands,
		&workspaceVersion,
	); err != nil {
		t.Fatalf("inspect durable send: %v", err)
	}
	if threads != 1 ||
		messages != 1 ||
		commands != 1 ||
		workspaceVersion != workspaceVersionBefore+1 {
		t.Fatalf(
			"durable send = %d Threads, %d Messages, %d commands, version %d",
			threads,
			messages,
			commands,
			workspaceVersion,
		)
	}
	processed, err := module.ProcessNextCommand(context.Background())
	if err != nil || !processed {
		t.Fatalf("process Message provider command = %t, %v", processed, err)
	}
	if len(provider.commands) != 1 {
		t.Fatalf("provider command count = %d, want 1", len(provider.commands))
	}
	providerCommand := provider.commands[0]
	if providerCommand.MessageID != first.ID ||
		providerCommand.Sender != "+17275550100" ||
		providerCommand.Destination != "+17275550199" ||
		providerCommand.Body != "Your records are ready." ||
		providerCommand.MessagingProfileID != "profile-message-office" ||
		providerCommand.CallbackToken == "" {
		t.Fatalf("provider command = %#v", providerCommand)
	}
	sent, err := module.ReadMessage(context.Background(), identity, first.ID)
	if err != nil {
		t.Fatalf("read accepted Message: %v", err)
	}
	if sent.Delivery != messaging.DeliverySent ||
		sent.ProviderMessageID != "provider-message-1" ||
		sent.Version != 2 {
		t.Fatalf("provider-accepted Message = %#v", sent)
	}

	rawReceipt := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"message.finalized","id":"message-event-delivered","occurred_at":"%s","payload":{"id":"provider-message-1","from":"+17275550100","to":"+17275550199","delivery_status":"delivered"}},"meta":{"attempt":1}}`,
		now.Format(time.RFC3339),
	))
	timestamp := fmt.Sprintf("%d", now.Unix())
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), rawReceipt...),
	))
	receipt, err := module.ReceiveWebhook(
		context.Background(),
		providerCommand.CallbackToken,
		rawReceipt,
		timestamp,
		signature,
	)
	if err != nil {
		t.Fatalf("receive delivery webhook: %v", err)
	}
	duplicate, err := module.ReceiveWebhook(
		context.Background(),
		providerCommand.CallbackToken,
		rawReceipt,
		timestamp,
		signature,
	)
	if err != nil {
		t.Fatalf("receive duplicate delivery webhook: %v", err)
	}
	if receipt.Duplicate || !duplicate.Duplicate {
		t.Fatalf("delivery receipts = %#v, %#v", receipt, duplicate)
	}
	retryTimestamp := fmt.Sprintf("%d", now.Add(time.Second).Unix())
	retriedRawReceipt := []byte(fmt.Sprintf(
		`{"meta":{"attempt":2},"data":{"payload":{"delivery_status":"delivered","to":"+17275550199","from":"+17275550100","id":"provider-message-1"},"occurred_at":"%s","id":"message-event-delivered","event_type":"message.finalized","record_type":"event"}}`,
		now.Format(time.RFC3339),
	))
	retriedSignature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(retryTimestamp+"|"), retriedRawReceipt...),
	))
	retriedReceipt, err := module.ReceiveWebhook(
		context.Background(),
		providerCommand.CallbackToken,
		retriedRawReceipt,
		retryTimestamp,
		retriedSignature,
	)
	if err != nil || !retriedReceipt.Duplicate {
		t.Fatalf("receive newly signed provider retry = %#v, %v", retriedReceipt, err)
	}
	conflictingRawReceipt := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"message.finalized","id":"message-event-delivered","occurred_at":"%s","payload":{"id":"provider-message-1","from":"+17275550100","to":"+17275550199","delivery_status":"failed"}}}`,
		now.Format(time.RFC3339),
	))
	conflictingSignature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), conflictingRawReceipt...),
	))
	if _, err := module.ReceiveWebhook(
		context.Background(),
		providerCommand.CallbackToken,
		conflictingRawReceipt,
		timestamp,
		conflictingSignature,
	); !errors.Is(err, messaging.ErrConflict) {
		t.Fatalf("conflicting duplicate provider event error = %v", err)
	}
	beforeProjection, err := module.ReadMessage(
		context.Background(),
		identity,
		first.ID,
	)
	if err != nil || beforeProjection.Delivery != messaging.DeliverySent {
		t.Fatalf(
			"Message changed before receipt projection = %#v, %v",
			beforeProjection,
			err,
		)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE messaging_provider_receipts
		SET
			state = 'PROCESSING',
			processing_started_at = $2::timestamptz - interval '31 seconds'
		WHERE event_id = $1
	`, receipt.EventID, now); err != nil {
		t.Fatalf("simulate interrupted receipt projection: %v", err)
	}
	processed, err = module.ProcessNextReceipt(context.Background())
	if err != nil || !processed {
		t.Fatalf("process delivery receipt = %t, %v", processed, err)
	}
	delivered, err := module.ReadMessage(context.Background(), identity, first.ID)
	if err != nil {
		t.Fatalf("read delivered Message: %v", err)
	}
	if delivered.Delivery != messaging.DeliveryDelivered ||
		delivered.Version != 3 {
		t.Fatalf("delivered Message = %#v", delivered)
	}
	transientRawReceipt := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"message.finalized","id":"message-event-transient-projection","occurred_at":"%s","payload":{"id":"provider-message-1","from":"+17275550100","to":"+17275550199","delivery_status":"delivered"}}}`,
		now.Format(time.RFC3339),
	))
	transientSignature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), transientRawReceipt...),
	))
	transientReceipt, err := module.ReceiveWebhook(
		context.Background(),
		providerCommand.CallbackToken,
		transientRawReceipt,
		timestamp,
		transientSignature,
	)
	if err != nil {
		t.Fatalf("receive transient projection fixture: %v", err)
	}
	blockingTx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin transient projection blocker: %v", err)
	}
	if _, err := blockingTx.Exec(
		context.Background(),
		`LOCK TABLE messaging_provider_commands IN ACCESS EXCLUSIVE MODE`,
	); err != nil {
		t.Fatalf("lock provider commands for transient projection: %v", err)
	}
	projectionContext, cancelProjection := context.WithTimeout(
		context.Background(),
		100*time.Millisecond,
	)
	processed, err = module.ProcessNextReceipt(projectionContext)
	cancelProjection()
	if !processed || err == nil {
		t.Fatalf("transient receipt projection = %t, %v", processed, err)
	}
	var transientState string
	if err := pool.QueryRow(context.Background(), `
		SELECT state
		FROM messaging_provider_receipts
		WHERE event_id = $1
	`, transientReceipt.EventID).Scan(&transientState); err != nil {
		t.Fatalf("read transient receipt state: %v", err)
	}
	if transientState != "PROCESSING" {
		t.Fatalf("transient receipt state = %q, want PROCESSING", transientState)
	}
	if err := blockingTx.Rollback(context.Background()); err != nil {
		t.Fatalf("release transient projection blocker: %v", err)
	}
	now = now.Add(31 * time.Second)
	if processed, err := module.ProcessNextReceipt(context.Background()); err != nil ||
		!processed {
		t.Fatalf("reclaim transient receipt = %t, %v", processed, err)
	}

	now = now.Add(30 * time.Second)
	unknown, _, err := module.Send(context.Background(), messaging.SendCommand{
		Identity:       identity,
		PracticeID:     authorization.Practice.ID,
		LocationID:     authorization.Locations[0].ID,
		Destination:    "+17275550199",
		Body:           "This send has an uncertain provider response.",
		IdempotencyKey: "message-send-unknown",
	})
	if err != nil {
		t.Fatalf("commit unknown-outcome Message: %v", err)
	}
	provider.sendError = messaging.ErrAmbiguous
	provider.sendResult = messaging.ProviderResult{
		MessageID: "provider-message-unknown",
	}
	processed, err = module.ProcessNextCommand(context.Background())
	if err != nil || !processed {
		t.Fatalf("process ambiguous Message command = %t, %v", processed, err)
	}
	unknown, err = module.ReadMessage(context.Background(), identity, unknown.ID)
	if err != nil {
		t.Fatalf("read unknown Message: %v", err)
	}
	if unknown.Delivery != messaging.DeliveryUnknown {
		t.Fatalf("ambiguous Message = %#v", unknown)
	}
	processed, err = module.ProcessNextCommand(context.Background())
	if err != nil || processed {
		t.Fatalf("ambiguous Message was selected for blind retry = %t, %v", processed, err)
	}
	if len(provider.commands) != 2 {
		t.Fatalf("provider command count after ambiguity = %d, want 2", len(provider.commands))
	}
	if _, _, err := module.SendAgain(
		context.Background(),
		messaging.SendAgainCommand{
			Identity:       identity,
			MessageID:      unknown.ID,
			IdempotencyKey: "message-unknown-new-attempt-without-warning",
		},
	); !errors.Is(err, messaging.ErrConflict) {
		t.Fatalf("unknown new attempt without warning error = %v", err)
	}
	provider.reconcileResult = messaging.ProviderResult{
		MessageID: "provider-message-unknown",
		State:     messaging.DeliveryDelivered,
	}
	reconciled, err := module.ReconcileNextCommand(context.Background())
	if err != nil || !reconciled {
		t.Fatalf("reconcile unknown Message = %t, %v", reconciled, err)
	}
	unknown, err = module.ReadMessage(context.Background(), identity, unknown.ID)
	if err != nil || unknown.Delivery != messaging.DeliveryDelivered {
		t.Fatalf("provider-reconciled Message = %#v, %v", unknown, err)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE messaging_provider_commands
		SET
			state = 'UNKNOWN',
			next_attempt_at = $2,
			reconcile_until = $2::timestamptz + interval '1 hour'
		WHERE message_id = $1
	`, unknown.ID, now); err != nil {
		t.Fatalf("prepare contradictory reconciliation: %v", err)
	}
	provider.reconcileResult = messaging.ProviderResult{
		MessageID: "provider-message-unknown",
		State:     messaging.DeliveryFailed,
	}
	if reconciled, err := module.ReconcileNextCommand(context.Background()); err != nil ||
		!reconciled {
		t.Fatalf("reconcile contradictory Message = %t, %v", reconciled, err)
	}
	var contradictoryState, contradictoryCode string
	var contradictoryNext time.Time
	if err := pool.QueryRow(context.Background(), `
		SELECT state, last_error_code, next_attempt_at
		FROM messaging_provider_commands
		WHERE message_id = $1
	`, unknown.ID).Scan(
		&contradictoryState,
		&contradictoryCode,
		&contradictoryNext,
	); err != nil {
		t.Fatalf("read contradictory reconciliation: %v", err)
	}
	if contradictoryState != "UNKNOWN" ||
		contradictoryCode != "CONTRADICTORY_TERMINAL_EVIDENCE" ||
		!contradictoryNext.After(now) {
		t.Fatalf(
			"contradictory reconciliation = %q/%q at %s",
			contradictoryState,
			contradictoryCode,
			contradictoryNext,
		)
	}

	now = now.Add(time.Minute)
	rawInbound := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"message.received","id":"message-event-inbound","occurred_at":"%s","payload":{"id":"provider-inbound-1","from":"+17275550199","to":"+17275550100","delivery_status":"delivered","text":"Thanks, I will pick them up."}}}`,
		now.Format(time.RFC3339),
	))
	timestamp = fmt.Sprintf("%d", now.Unix())
	signature = base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), rawInbound...),
	))
	if _, err := module.ReceiveWebhook(
		context.Background(),
		"",
		rawInbound,
		timestamp,
		signature,
	); err != nil {
		t.Fatalf("receive inbound Message: %v", err)
	}
	processed, err = module.ProcessNextReceipt(context.Background())
	if err != nil || !processed {
		t.Fatalf("process inbound Message = %t, %v", processed, err)
	}
	firstThreads, err := module.QueryThreads(
		context.Background(),
		messaging.QueryThreadsCommand{
			Identity:   identity,
			PracticeID: authorization.Practice.ID,
			LocationID: authorization.Locations[0].ID,
			Search:     "(727) 555-0199",
		},
	)
	if err != nil {
		t.Fatalf("query first User Threads: %v", err)
	}
	secondThreads, err := module.QueryThreads(
		context.Background(),
		messaging.QueryThreadsCommand{
			Identity:   secondIdentity,
			PracticeID: authorization.Practice.ID,
			LocationID: authorization.Locations[0].ID,
		},
	)
	if err != nil {
		t.Fatalf("query second User Threads: %v", err)
	}
	if len(firstThreads.Items) != 1 ||
		len(secondThreads.Items) != 1 ||
		!firstThreads.Items[0].Unread ||
		!secondThreads.Items[0].Unread ||
		firstThreads.Items[0].Preview != "Thanks, I will pick them up." ||
		firstThreads.Items[0].LatestDirection != messaging.DirectionInbound {
		t.Fatalf(
			"inbound Thread projections = %#v, %#v",
			firstThreads,
			secondThreads,
		)
	}
	timeline, err := reads.QueryTimeline(
		context.Background(),
		workspace.QueryTimelineCommand{
			Identity: identity,
			ThreadID: first.Thread.ID,
		},
	)
	if err != nil {
		t.Fatalf("query Message timeline: %v", err)
	}
	if len(timeline.Items) != 3 ||
		timeline.Items[0].Message.ID != first.ID ||
		timeline.Items[2].Message.ProviderMessageID != "provider-inbound-1" ||
		timeline.Items[2].Message.Direction != messaging.DirectionInbound {
		t.Fatalf("Message timeline = %#v", timeline)
	}
	newerPage, err := reads.QueryTimeline(
		context.Background(),
		workspace.QueryTimelineCommand{
			Identity: identity,
			ThreadID: first.Thread.ID,
			Limit:    2,
		},
	)
	if err != nil || len(newerPage.Items) != 2 || newerPage.NextCursor == "" {
		t.Fatalf("newer timeline page = %#v, %v", newerPage, err)
	}
	olderPage, err := reads.QueryTimeline(
		context.Background(),
		workspace.QueryTimelineCommand{
			Identity: identity,
			ThreadID: first.Thread.ID,
			Cursor:   newerPage.NextCursor,
			Limit:    2,
		},
	)
	if err != nil ||
		len(olderPage.Items) != 1 ||
		olderPage.Items[0].ID == newerPage.Items[0].ID ||
		olderPage.Items[0].ID == newerPage.Items[1].ID {
		t.Fatalf("older timeline page = %#v, %v", olderPage, err)
	}
	if err := module.MarkRead(context.Background(), messaging.MarkReadCommand{
		Identity: identity,
		ThreadID: first.Thread.ID,
	}); err != nil {
		t.Fatalf("mark Thread read: %v", err)
	}
	firstThreads, err = module.QueryThreads(
		context.Background(),
		messaging.QueryThreadsCommand{
			Identity:   identity,
			PracticeID: authorization.Practice.ID,
			LocationID: authorization.Locations[0].ID,
		},
	)
	if err != nil {
		t.Fatalf("query read Thread: %v", err)
	}
	secondThreads, err = module.QueryThreads(
		context.Background(),
		messaging.QueryThreadsCommand{
			Identity:   secondIdentity,
			PracticeID: authorization.Practice.ID,
			LocationID: authorization.Locations[0].ID,
		},
	)
	if err != nil {
		t.Fatalf("query second unread Thread: %v", err)
	}
	if firstThreads.Items[0].Unread || !secondThreads.Items[0].Unread {
		t.Fatalf(
			"per-User unread state = first %t, second %t",
			firstThreads.Items[0].Unread,
			secondThreads.Items[0].Unread,
		)
	}
	var taskCount int
	if err := pool.QueryRow(
		context.Background(),
		`SELECT count(*) FROM work_tasks`,
	).Scan(&taskCount); err != nil {
		t.Fatalf("count Tasks after inbound Message: %v", err)
	}
	if taskCount != 0 {
		t.Fatalf("inbound Message created %d automatic Tasks", taskCount)
	}

	now = now.Add(time.Minute)
	queued, _, err := module.Send(context.Background(), messaging.SendCommand{
		Identity:       identity,
		PracticeID:     authorization.Practice.ID,
		LocationID:     authorization.Locations[0].ID,
		ThreadID:       first.Thread.ID,
		Body:           "This queued Message must yield to provider opt-out evidence.",
		IdempotencyKey: "message-before-stop",
	})
	if err != nil {
		t.Fatalf("queue Message before STOP: %v", err)
	}
	rawStop := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"message.received","id":"message-event-stop","occurred_at":"%s","payload":{"id":"provider-inbound-stop","from":{"phone_number":"+17275550199"},"to":[{"phone_number":"+17275550100","status":"delivered"}],"text":"STOP","record_type":"message","direction":"inbound"}}}`,
		now.Format(time.RFC3339),
	))
	timestamp = fmt.Sprintf("%d", now.Unix())
	signature = base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), rawStop...),
	))
	if _, err := module.ReceiveWebhook(
		context.Background(),
		"",
		rawStop,
		timestamp,
		signature,
	); err != nil {
		t.Fatalf("receive STOP: %v", err)
	}
	if processed, err := module.ProcessNextReceipt(context.Background()); err != nil ||
		!processed {
		t.Fatalf("process STOP = %t, %v", processed, err)
	}
	queued, err = module.ReadMessage(context.Background(), identity, queued.ID)
	if err != nil ||
		queued.Delivery != messaging.DeliveryFailed ||
		queued.SafeFailureCode != "OUTBOUND_BLOCKED" {
		t.Fatalf("queued Message after STOP = %#v, %v", queued, err)
	}
	if _, _, err := module.Send(context.Background(), messaging.SendCommand{
		Identity:       identity,
		PracticeID:     authorization.Practice.ID,
		LocationID:     authorization.Locations[0].ID,
		ThreadID:       first.Thread.ID,
		Body:           "This must stay blocked.",
		IdempotencyKey: "message-after-stop",
	}); !errors.Is(err, messaging.ErrBlocked) {
		t.Fatalf("send after STOP error = %v, want blocked", err)
	}
	now = now.Add(time.Minute)
	rawStart := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"message.received","id":"message-event-start","occurred_at":"%s","payload":{"id":"provider-inbound-start","from":{"phone_number":"+17275550199"},"to":[{"phone_number":"+17275550100","status":"delivered"}],"text":"START"}}}`,
		now.Format(time.RFC3339),
	))
	timestamp = fmt.Sprintf("%d", now.Unix())
	signature = base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), rawStart...),
	))
	if _, err := module.ReceiveWebhook(
		context.Background(),
		"",
		rawStart,
		timestamp,
		signature,
	); err != nil {
		t.Fatalf("receive START: %v", err)
	}
	if processed, err := module.ProcessNextReceipt(context.Background()); err != nil ||
		!processed {
		t.Fatalf("process START = %t, %v", processed, err)
	}
	now = now.Add(2 * time.Minute)
	newerStopAt := now
	rawNewerStop := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"message.received","id":"message-event-newer-stop","occurred_at":"%s","payload":{"id":"provider-inbound-newer-stop","from":"+17275550199","to":"+17275550100","text":"STOP"}}}`,
		newerStopAt.Format(time.RFC3339),
	))
	timestamp = fmt.Sprintf("%d", now.Unix())
	signature = base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), rawNewerStop...),
	))
	if _, err := module.ReceiveWebhook(
		context.Background(),
		"",
		rawNewerStop,
		timestamp,
		signature,
	); err != nil {
		t.Fatalf("receive newer STOP: %v", err)
	}
	if processed, err := module.ProcessNextReceipt(context.Background()); err != nil ||
		!processed {
		t.Fatalf("process newer STOP = %t, %v", processed, err)
	}
	now = now.Add(time.Minute)
	rawOlderStart := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"message.received","id":"message-event-older-start","occurred_at":"%s","payload":{"id":"provider-inbound-older-start","from":"+17275550199","to":"+17275550100","text":"START"}}}`,
		newerStopAt.Add(-time.Minute).Format(time.RFC3339),
	))
	timestamp = fmt.Sprintf("%d", now.Unix())
	signature = base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), rawOlderStart...),
	))
	if _, err := module.ReceiveWebhook(
		context.Background(),
		"",
		rawOlderStart,
		timestamp,
		signature,
	); err != nil {
		t.Fatalf("receive reordered older START: %v", err)
	}
	if processed, err := module.ProcessNextReceipt(context.Background()); err != nil ||
		!processed {
		t.Fatalf("process reordered older START = %t, %v", processed, err)
	}
	reorderedThreads, err := module.QueryThreads(
		context.Background(),
		messaging.QueryThreadsCommand{
			Identity:   identity,
			PracticeID: authorization.Practice.ID,
			LocationID: authorization.Locations[0].ID,
		},
	)
	if err != nil || len(reorderedThreads.Items) != 1 ||
		reorderedThreads.Items[0].Preview != "STOP" {
		t.Fatalf("reordered latest Message projection = %#v, %v", reorderedThreads, err)
	}
	rawEqualStart := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"message.received","id":"zzzz-message-event-equal-start","occurred_at":"%s","payload":{"id":"provider-inbound-equal-start","from":"+17275550199","to":"+17275550100","text":"START"}}}`,
		newerStopAt.Format(time.RFC3339),
	))
	signature = base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), rawEqualStart...),
	))
	if _, err := module.ReceiveWebhook(
		context.Background(),
		"",
		rawEqualStart,
		timestamp,
		signature,
	); err != nil {
		t.Fatalf("receive equal-time START: %v", err)
	}
	if processed, err := module.ProcessNextReceipt(context.Background()); err != nil ||
		!processed {
		t.Fatalf("process equal-time START = %t, %v", processed, err)
	}
	if _, _, err := module.Send(context.Background(), messaging.SendCommand{
		Identity:       identity,
		PracticeID:     authorization.Practice.ID,
		LocationID:     authorization.Locations[0].ID,
		ThreadID:       first.Thread.ID,
		Body:           "Reordered START must not undo the newer STOP.",
		IdempotencyKey: "message-after-reordered-start",
	}); !errors.Is(err, messaging.ErrBlocked) {
		t.Fatalf("send after reordered START error = %v, want blocked", err)
	}
	rawNewestStart := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"message.received","id":"message-event-newest-start","occurred_at":"%s","payload":{"id":"provider-inbound-newest-start","from":"+17275550199","to":"+17275550100","text":"START"}}}`,
		now.Format(time.RFC3339),
	))
	signature = base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), rawNewestStart...),
	))
	if _, err := module.ReceiveWebhook(
		context.Background(),
		"",
		rawNewestStart,
		timestamp,
		signature,
	); err != nil {
		t.Fatalf("receive newest START: %v", err)
	}
	if processed, err := module.ProcessNextReceipt(context.Background()); err != nil ||
		!processed {
		t.Fatalf("process newest START = %t, %v", processed, err)
	}
	newAttempt, newAttemptStatus, err := module.SendAgain(
		context.Background(),
		messaging.SendAgainCommand{
			Identity:       identity,
			MessageID:      queued.ID,
			IdempotencyKey: "message-failed-new-attempt",
		},
	)
	if err != nil ||
		newAttemptStatus != messaging.MessageCreated ||
		newAttempt.ID == queued.ID ||
		newAttempt.RetryOfMessageID != queued.ID ||
		newAttempt.Body != queued.Body {
		t.Fatalf(
			"failed Message new attempt = %#v, %q, %v",
			newAttempt,
			newAttemptStatus,
			err,
		)
	}

	followUp, status, err := module.CreateFollowUpTask(
		context.Background(),
		messaging.CreateFollowUpTaskCommand{
			Identity:  identity,
			MessageID: timeline.Items[2].Message.ID,
		},
	)
	if err != nil {
		t.Fatalf("create Message follow-up Task: %v", err)
	}
	replayedFollowUp, replayedStatus, err := module.CreateFollowUpTask(
		context.Background(),
		messaging.CreateFollowUpTaskCommand{
			Identity:  identity,
			MessageID: timeline.Items[2].Message.ID,
		},
	)
	if err != nil {
		t.Fatalf("replay Message follow-up Task: %v", err)
	}
	if status != work.TaskCreated ||
		replayedStatus != work.TaskDuplicate ||
		replayedFollowUp.ID != followUp.ID {
		t.Fatalf(
			"Message Task receipts = (%q, %q, %q), want created, duplicate, %q",
			status,
			replayedStatus,
			replayedFollowUp.ID,
			followUp.ID,
		)
	}
	if followUp.Origin != work.TaskOriginStaffMessageFollowUp ||
		followUp.State != work.TaskOpen ||
		followUp.Title != "Follow up on text" ||
		followUp.MessageID != timeline.Items[2].Message.ID ||
		followUp.MessageThreadID != first.Thread.ID ||
		followUp.LocationID != authorization.Locations[0].ID ||
		followUp.Phone != "+17275550199" {
		t.Fatalf("Message follow-up Task = %#v", followUp)
	}
	taskProjection, err := reads.ReadTask(context.Background(), identity, followUp.ID)
	if err != nil || !taskProjection.Unread ||
		taskProjection.ConversationThreadID != first.Thread.ID {
		t.Fatalf("OPEN Task unread projection = %#v, %v", taskProjection, err)
	}
	timelineWithTask, err := reads.QueryTimeline(
		context.Background(),
		workspace.QueryTimelineCommand{
			Identity: identity,
			ThreadID: first.Thread.ID,
		},
	)
	if err != nil {
		t.Fatalf("query timeline with Task: %v", err)
	}
	taskCards := 0
	messageLinked := false
	for _, item := range timelineWithTask.Items {
		if item.Type == "TASK" {
			taskCards++
			if item.Task.ID != followUp.ID || item.Task.State != work.TaskOpen {
				t.Fatalf("conversation Task card = %#v", item)
			}
		}
		if item.Type == "MESSAGE" &&
			item.Message.ID == timeline.Items[2].Message.ID {
			messageLinked = item.Message.TaskID == followUp.ID
		}
	}
	if taskCards != 1 {
		t.Fatalf("conversation Task card count = %d", taskCards)
	}
	if !messageLinked {
		t.Fatal("source Message was not linked to its follow-up Task")
	}
	var secondLocationID string
	if err := pool.QueryRow(context.Background(), `
		SELECT id::text
		FROM access_locations
		WHERE practice_id = $1 AND provisioning_key = 'message-office-two'
	`, authorization.Practice.ID).Scan(&secondLocationID); err != nil {
		t.Fatalf("read second Location: %v", err)
	}
	for index, locationID := range []string{
		authorization.Locations[0].ID,
		secondLocationID,
	} {
		callAt := now.Add(time.Duration(index+1) * time.Second)
		if _, err := pool.Exec(context.Background(), `
			WITH handoff AS (
				INSERT INTO human_calling_handoffs (
					service_subject,
					practice_id,
					location_id,
					source_call_id,
					idempotency_key,
					input_fingerprint,
					phone,
					phone_source,
					expires_at,
					created_at
				)
				VALUES (
					'slice-5-timeline',
					$1,
					$2,
					$3,
					$3,
					$4,
					'+17275550199',
					'fixture',
					$5::timestamptz + interval '1 hour',
					$5
				)
				RETURNING id
			)
			INSERT INTO human_calling_calls (
				source_handoff_id,
				practice_id,
				location_id,
				caller_phone,
				terminal_outcome,
				ended_at,
				created_at,
				updated_at
			)
			SELECT
				id,
				$1,
				$2,
				'+17275550199',
				'RESOLVED',
				$5::timestamptz + interval '10 seconds',
				$5,
				$5
			FROM handoff
		`, authorization.Practice.ID, locationID,
			fmt.Sprintf("slice-5-timeline-call-%d", index),
			bytes.Repeat([]byte{byte(index + 1)}, 32),
			callAt,
		); err != nil {
			t.Fatalf("seed exact-phone Call %d: %v", index, err)
		}
		if _, err := pool.Exec(context.Background(), `
			INSERT INTO human_calling_call_legs (
				call_id, role, sequence, state, provider_call_control_id,
				provider_call_leg_id, provider_call_session_id, ending_at, ended_at,
				created_at, updated_at
			)
			SELECT id, 'CALLER', 1, 'ENDED', $1 || '-control', $1 || '-leg',
				$1 || '-session', $2::timestamptz + interval '10 seconds',
				$2::timestamptz + interval '10 seconds', $2,
				$2::timestamptz + interval '10 seconds'
			FROM human_calling_calls
			WHERE source_handoff_id = (
				SELECT id FROM human_calling_handoffs WHERE source_call_id = $1
			)
		`, fmt.Sprintf("slice-5-timeline-call-%d", index), callAt); err != nil {
			t.Fatalf("seed exact-phone Caller CallLeg %d: %v", index, err)
		}
		if _, err := pool.Exec(context.Background(), `
			INSERT INTO human_calling_call_legs (
				call_id, role, sequence, staff_subject, state,
				provider_call_control_id, provider_call_leg_id,
				provider_call_session_id, ending_at, ended_at, created_at, updated_at
			)
			SELECT id, 'STAFF', 1, $3, 'ENDED', $1 || '-staff-control',
				$1 || '-staff-leg', $1 || '-staff-session',
				$2::timestamptz + interval '9 seconds',
				$2::timestamptz + interval '9 seconds', $2,
				$2::timestamptz + interval '9 seconds'
			FROM human_calling_calls
			WHERE source_handoff_id = (
				SELECT id FROM human_calling_handoffs WHERE source_call_id = $1
			)
		`, fmt.Sprintf("slice-5-timeline-call-%d", index), callAt,
			identity.Subject); err != nil {
			t.Fatalf("seed unbridged Staff CallLeg %d: %v", index, err)
		}
	}
	timelineWithCall, err := reads.QueryTimeline(
		context.Background(),
		workspace.QueryTimelineCommand{
			Identity: identity,
			ThreadID: first.Thread.ID,
		},
	)
	if err != nil {
		t.Fatalf("query timeline with Call: %v", err)
	}
	callCards := 0
	for _, item := range timelineWithCall.Items {
		if item.Type == "CALL" {
			callCards++
			if item.Call.LocationID != authorization.Locations[0].ID ||
				item.Call.Direction != "INBOUND" ||
				item.Call.Outcome != humancalling.CallUnanswered ||
				item.Call.AnsweredByEmail != "" {
				t.Fatalf("conversation Call card = %#v", item)
			}
		}
	}
	if callCards != 1 {
		t.Fatalf("exact-location Call card count = %d", callCards)
	}

	now = now.Add(time.Minute)
	linked, _, err := module.Send(context.Background(), messaging.SendCommand{
		Identity:       identity,
		PracticeID:     authorization.Practice.ID,
		LocationID:     authorization.Locations[0].ID,
		ThreadID:       first.Thread.ID,
		TaskID:         followUp.ID,
		Body:           "I have set the records aside for you.",
		IdempotencyKey: "message-task-send-1",
	})
	if err != nil {
		t.Fatalf("send from OPEN Task: %v", err)
	}
	if linked.TaskID != followUp.ID ||
		linked.Thread.ID != first.Thread.ID ||
		linked.Destination != "+17275550199" {
		t.Fatalf("Task-linked Message = %#v", linked)
	}
	completed, err := workModule.CompleteTask(
		context.Background(),
		work.CompleteTaskCommand{
			Identity:        identity,
			TaskID:          followUp.ID,
			ExpectedVersion: followUp.Version,
		},
	)
	if err != nil {
		t.Fatalf("complete Message Task: %v", err)
	}
	completedProjection, err := reads.ReadTask(context.Background(), identity, completed.ID)
	if err != nil || completedProjection.Unread {
		t.Fatalf("COMPLETED Task unread projection = %#v, %v", completedProjection, err)
	}
	timelineWithTask, err = reads.QueryTimeline(
		context.Background(),
		workspace.QueryTimelineCommand{
			Identity: identity,
			ThreadID: first.Thread.ID,
		},
	)
	if err != nil {
		t.Fatalf("query timeline with completed Task: %v", err)
	}
	taskCards = 0
	for _, item := range timelineWithTask.Items {
		if item.Type == "TASK" {
			taskCards++
			if item.Task.ID != completed.ID ||
				item.Task.State != work.TaskCompleted {
				t.Fatalf("completed conversation Task card = %#v", item)
			}
		}
	}
	if taskCards != 1 {
		t.Fatalf("completed conversation Task card count = %d", taskCards)
	}
	if _, _, err := module.Send(context.Background(), messaging.SendCommand{
		Identity:       identity,
		PracticeID:     authorization.Practice.ID,
		LocationID:     authorization.Locations[0].ID,
		ThreadID:       first.Thread.ID,
		TaskID:         followUp.ID,
		Body:           "This must not send from a completed Task.",
		IdempotencyKey: "message-task-send-completed",
	}); !errors.Is(err, messaging.ErrConflict) {
		t.Fatalf("completed Task send error = %v, want conflict", err)
	}
	reopened, err := workModule.ReopenTask(
		context.Background(),
		work.ReopenTaskCommand{
			Identity:        identity,
			TaskID:          followUp.ID,
			ExpectedVersion: completed.Version,
		},
	)
	if err != nil || reopened.State != work.TaskOpen {
		t.Fatalf("reopen Message Task = %#v, %v", reopened, err)
	}
	if _, _, err := module.Send(context.Background(), messaging.SendCommand{
		Identity:       identity,
		PracticeID:     authorization.Practice.ID,
		LocationID:     authorization.Locations[0].ID,
		ThreadID:       first.Thread.ID,
		TaskID:         followUp.ID,
		Body:           "The reopened Task can continue explicitly.",
		IdempotencyKey: "message-task-send-reopened",
	}); err != nil {
		t.Fatalf("send from reopened Task: %v", err)
	}

	secondLocationMessage, _, err := module.Send(
		context.Background(),
		messaging.SendCommand{
			Identity:       identity,
			PracticeID:     authorization.Practice.ID,
			LocationID:     secondLocationID,
			Destination:    "+17275550199",
			Body:           "This is the other office's conversation.",
			IdempotencyKey: "message-location-two",
		},
	)
	if err != nil {
		t.Fatalf("send from second Location: %v", err)
	}
	if secondLocationMessage.Thread.ID == first.Thread.ID ||
		secondLocationMessage.Thread.OfficePhone != "+17275550101" {
		t.Fatalf("second Location Message = %#v", secondLocationMessage)
	}
	phoneTimeline, err := reads.QueryPhoneTimeline(
		context.Background(),
		workspace.QueryPhoneTimelineCommand{
			Identity: identity, PracticeID: authorization.Practice.ID,
			Phone: "+17275550199",
		},
	)
	if err != nil {
		t.Fatalf("query cross-Location phone timeline: %v", err)
	}
	taskActivities := map[string]bool{}
	locations := map[string]bool{}
	for _, item := range phoneTimeline.Items {
		switch item.Type {
		case "MESSAGE":
			locations[item.Message.Thread.LocationName] = true
		case "CALL":
			locations[item.Call.LocationName] = true
		case "TASK":
			locations[item.Task.LocationName] = true
			taskActivities[item.TaskActivity] = true
		}
	}
	for _, activity := range []string{"TASK_CREATED", "TASK_COMPLETED", "TASK_REOPENED"} {
		if !taskActivities[activity] {
			t.Fatalf("phone timeline omitted %s: %#v", activity, taskActivities)
		}
	}
	if !locations[authorization.Locations[0].Name] || !locations["Message Office Two"] {
		t.Fatalf("phone timeline Location provenance = %#v", locations)
	}
	secondLocationThreads, err := module.QueryThreads(
		context.Background(),
		messaging.QueryThreadsCommand{
			Identity:   identity,
			PracticeID: authorization.Practice.ID,
			LocationID: secondLocationID,
			Search:     "+17275550199",
		},
	)
	if err != nil ||
		len(secondLocationThreads.Items) != 1 ||
		secondLocationThreads.Items[0].ID != secondLocationMessage.Thread.ID {
		t.Fatalf(
			"second Location Thread isolation = %#v, %v",
			secondLocationThreads,
			err,
		)
	}
	allLocationThreads, err := module.QueryThreads(
		context.Background(),
		messaging.QueryThreadsCommand{
			Identity:   identity,
			PracticeID: authorization.Practice.ID,
			Search:     "+17275550199",
		},
	)
	if err != nil || len(allLocationThreads.Items) != 2 {
		t.Fatalf(
			"all-Location Thread scope = %#v, %v",
			allLocationThreads,
			err,
		)
	}
	allLocationIDs := map[string]bool{}
	for _, thread := range allLocationThreads.Items {
		allLocationIDs[thread.LocationID] = true
	}
	if !allLocationIDs[authorization.Locations[0].ID] ||
		!allLocationIDs[secondLocationID] {
		t.Fatalf(
			"all-Location Thread provenance = %#v",
			allLocationIDs,
		)
	}

	aiTask, status, err := workModule.CreateAITask(
		context.Background(),
		work.CreateAITaskCommand{
			Service: access.ServiceIdentity{
				Subject:       "message-ai-service",
				PracticeID:    authorization.Practice.ID,
				LocationScope: access.LocationScopeAll,
				Capabilities: []access.ServiceCapability{
					access.ServiceCapabilityCreateTask,
				},
			},
			OfficeKey:      "message-office",
			OfficePhone:    "+17275550100",
			SourceCallID:   "message-ai-task-source",
			IdempotencyKey: "message-ai-task",
			Phone:          "+17275550155",
			CallerName:     "Task caller",
			Summary:        "Send the requested information.",
			Message:        "The caller asked the office to text an update.",
			Category:       work.TaskCategoryDocumentation,
			Urgency:        work.TaskUrgencyNormal,
		},
	)
	if err != nil || status != work.TaskCreated {
		t.Fatalf("create unthreaded AI Task = %#v, %q, %v", aiTask, status, err)
	}
	taskMessage, _, err := module.Send(
		context.Background(),
		messaging.SendCommand{
			Identity:       identity,
			PracticeID:     aiTask.PracticeID,
			LocationID:     aiTask.LocationID,
			Destination:    aiTask.Phone,
			TaskID:         aiTask.ID,
			Body:           "Here is the update you requested.",
			IdempotencyKey: "message-ai-task-first-send",
		},
	)
	if err != nil {
		t.Fatalf("send first Message from unthreaded Task: %v", err)
	}
	if taskMessage.TaskID != aiTask.ID ||
		taskMessage.Thread.ExternalPhone != aiTask.Phone ||
		taskMessage.Thread.LocationID != aiTask.LocationID {
		t.Fatalf("first unthreaded Task Message = %#v", taskMessage)
	}

	completedAITask, err := workModule.CompleteTask(
		context.Background(),
		work.CompleteTaskCommand{
			Identity:        identity,
			TaskID:          aiTask.ID,
			ExpectedVersion: aiTask.Version,
		},
	)
	if err != nil {
		t.Fatalf("complete Task with conversation: %v", err)
	}
	completedPage, err := reads.ReadTask(context.Background(), identity, completedAITask.ID)
	if err != nil ||
		completedPage.ConversationThreadID != taskMessage.Thread.ID ||
		completedPage.Unread {
		t.Fatalf(
			"completed Task conversation projection = %#v, %v",
			completedPage,
			err,
		)
	}
}

func TestAttachmentLifecycleKeepsBytesPrivateAndMessageMembershipImmutable(
	t *testing.T,
) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 29, 15, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "slice-5-attachment-test",
		Practices: []access.PracticeProvision{{
			Key:  "attachment-practice",
			Name: "Attachment Practice",
			Locations: []access.LocationProvision{{
				Key:  "attachment-office",
				Name: "Attachment Office",
			}},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "attachment-user",
				Email:         "attachment@message.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision attachment fixture: %v", err)
	}
	identity := access.Identity{
		Subject:       "attachment-user-subject",
		Email:         "attachment@message.test",
		EmailVerified: true,
	}
	authorization := testaccess.Activate(t, accessModule, identity)
	var mediaAvailable bool
	pdf := append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("safe-pdf"), 16)...)
	mediaServer := httptest.NewTLSServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		_ *http.Request,
	) {
		if !mediaAvailable {
			response.WriteHeader(http.StatusGone)
			return
		}
		response.Header().Set("Content-Type", "application/pdf")
		_, _ = response.Write(pdf)
	}))
	defer mediaServer.Close()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("create attachment webhook key: %v", err)
	}
	memoryStore := messaging.NewMemoryAttachmentStore()
	store := &deleteFailingAttachmentStore{
		AttachmentObjectStore: memoryStore,
	}
	provider := &providerFixture{}
	module := messaging.New(
		pool,
		accessModule,
		work.New(pool, accessModule, func() time.Time { return now }),
		provider,
		messaging.Config{
			WebhookPublicKeys:  []ed25519.PublicKey{publicKey},
			WebhookTolerance:   time.Minute,
			AttachmentStore:    store,
			MediaPublicBaseURL: "https://ingress.example/v1/provider/messaging-media",
			MediaSigningKey:    bytes.Repeat([]byte{7}, 32),
			MediaURLTTL:        5 * time.Minute,
			HTTPClient:         mediaServer.Client(),
		},
		func() time.Time { return now },
	)
	reads := workspace.New(pool, accessModule)
	if err := module.Provision(context.Background(), []messaging.LocationProvision{{
		PracticeKey:        "attachment-practice",
		LocationKey:        "attachment-office",
		Sender:             "+17275550110",
		MessagingProfileID: "attachment-profile",
	}}); err != nil {
		t.Fatalf("provision attachment sender: %v", err)
	}
	png := append(
		[]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'},
		bytes.Repeat([]byte{0}, 64)...,
	)
	if _, err := module.UploadAttachment(
		context.Background(),
		messaging.UploadAttachmentCommand{
			Identity:     identity,
			PracticeID:   authorization.Practice.ID,
			LocationID:   authorization.Locations[0].ID,
			FileName:     "not-really.pdf",
			DeclaredType: "application/pdf",
			Content:      png,
		},
	); !errors.Is(err, messaging.ErrInvalidInput) {
		t.Fatalf("mismatched attachment error = %v", err)
	}
	pending, err := module.UploadAttachment(
		context.Background(),
		messaging.UploadAttachmentCommand{
			Identity:     identity,
			PracticeID:   authorization.Practice.ID,
			LocationID:   authorization.Locations[0].ID,
			FileName:     "photo.png",
			DeclaredType: "image/png",
			Content:      png,
		},
	)
	if err != nil {
		t.Fatalf("upload private attachment: %v", err)
	}
	if _, _, err := module.Send(context.Background(), messaging.SendCommand{
		Identity:       identity,
		PracticeID:     authorization.Practice.ID,
		LocationID:     authorization.Locations[0].ID,
		Destination:    "wrong +1 727 555 0119",
		AttachmentID:   pending.ID,
		IdempotencyKey: "attachment-invalid-destination",
	}); !errors.Is(err, messaging.ErrInvalidInput) {
		t.Fatalf("attachment malformed destination error = %v", err)
	}
	var pendingState messaging.AttachmentState
	var pendingMessageID *string
	var rejectedMessageCount, rejectedCommandCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			attachment.state,
			attachment.message_id::text,
			(SELECT count(*) FROM messaging_messages),
			(SELECT count(*) FROM messaging_provider_commands)
		FROM messaging_attachments attachment
		WHERE attachment.id = $1
	`, pending.ID).Scan(
		&pendingState,
		&pendingMessageID,
		&rejectedMessageCount,
		&rejectedCommandCount,
	); err != nil {
		t.Fatalf("inspect attachment after rejected destination: %v", err)
	}
	if pendingState != messaging.AttachmentPending ||
		pendingMessageID != nil ||
		rejectedMessageCount != 0 ||
		rejectedCommandCount != 0 {
		t.Fatalf(
			"rejected attachment send = state %q message %v rows %d/%d",
			pendingState,
			pendingMessageID,
			rejectedMessageCount,
			rejectedCommandCount,
		)
	}
	outbound, _, err := module.Send(context.Background(), messaging.SendCommand{
		Identity:       identity,
		PracticeID:     authorization.Practice.ID,
		LocationID:     authorization.Locations[0].ID,
		Destination:    "+17275550119",
		AttachmentID:   pending.ID,
		IdempotencyKey: "attachment-message-1",
	})
	if err != nil {
		t.Fatalf("send media-only Message: %v", err)
	}
	if outbound.Body != "" ||
		outbound.Attachment == nil ||
		outbound.Attachment.ID != pending.ID ||
		outbound.Attachment.State != messaging.AttachmentStored {
		t.Fatalf("media-only Message = %#v", outbound)
	}
	replayed, status, err := module.Send(context.Background(), messaging.SendCommand{
		Identity:       identity,
		PracticeID:     authorization.Practice.ID,
		LocationID:     authorization.Locations[0].ID,
		Destination:    "+17275550119",
		AttachmentID:   pending.ID,
		IdempotencyKey: "attachment-message-1",
	})
	if err != nil ||
		status != messaging.MessageDuplicate ||
		replayed.ID != outbound.ID {
		t.Fatalf("attachment Message replay = %#v, %q, %v", replayed, status, err)
	}
	if _, _, err := module.Send(context.Background(), messaging.SendCommand{
		Identity:       identity,
		PracticeID:     authorization.Practice.ID,
		LocationID:     authorization.Locations[0].ID,
		Destination:    "+17275550119",
		AttachmentID:   pending.ID,
		IdempotencyKey: "attachment-message-reuse",
	}); !errors.Is(err, messaging.ErrConflict) {
		t.Fatalf("reused attachment error = %v", err)
	}
	if processed, err := module.ProcessNextCommand(context.Background()); err != nil ||
		!processed {
		t.Fatalf("send outbound MMS = %t, %v", processed, err)
	}
	if len(provider.commands) != 1 || provider.commands[0].MediaURL == "" {
		t.Fatalf("outbound MMS command = %#v", provider.commands)
	}
	signedURL, err := url.Parse(provider.commands[0].MediaURL)
	if err != nil {
		t.Fatalf("parse provider media URL: %v", err)
	}
	providerContent, err := module.OpenProviderAttachment(
		context.Background(),
		pending.ID,
		signedURL.Query().Get("expires"),
		signedURL.Query().Get("signature"),
	)
	if err != nil || !bytes.Equal(providerContent.Content, png) {
		t.Fatalf("open short-lived provider media = %#v, %v", providerContent, err)
	}
	authorizedContent, err := module.OpenAttachment(
		context.Background(),
		identity,
		pending.ID,
	)
	if err != nil || !bytes.Equal(authorizedContent.Content, png) {
		t.Fatalf("open authorized attachment = %#v, %v", authorizedContent, err)
	}

	now = now.Add(10 * time.Second)
	rawFailed := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"message.finalized","id":"attachment-outbound-failed","occurred_at":"%s","payload":{"id":"provider-message-1","from":"+17275550110","to":"+17275550119","delivery_status":"failed"}}}`,
		now.Format(time.RFC3339),
	))
	timestamp := fmt.Sprintf("%d", now.Unix())
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), rawFailed...),
	))
	if _, err := module.ReceiveWebhook(
		context.Background(),
		provider.commands[0].CallbackToken,
		rawFailed,
		timestamp,
		signature,
	); err != nil {
		t.Fatalf("receive outbound MMS failure: %v", err)
	}
	if processed, err := module.ProcessNextReceipt(context.Background()); err != nil ||
		!processed {
		t.Fatalf("project outbound MMS failure = %t, %v", processed, err)
	}
	type sendAgainResult struct {
		message messaging.Message
		status  messaging.MessageCreateStatus
		err     error
	}
	startConcurrentRetry := make(chan struct{})
	retryResults := make(chan sendAgainResult, 2)
	for range 2 {
		go func() {
			<-startConcurrentRetry
			message, status, err := module.SendAgain(
				context.Background(),
				messaging.SendAgainCommand{
					Identity:       identity,
					MessageID:      outbound.ID,
					IdempotencyKey: "attachment-message-new-attempt",
				},
			)
			retryResults <- sendAgainResult{
				message: message,
				status:  status,
				err:     err,
			}
		}()
	}
	close(startConcurrentRetry)
	firstRetry := <-retryResults
	secondRetry := <-retryResults
	if firstRetry.err != nil || secondRetry.err != nil ||
		firstRetry.message.ID != secondRetry.message.ID {
		t.Fatalf(
			"concurrent MMS retries = (%q, %q, %v) and (%q, %q, %v)",
			firstRetry.message.ID,
			firstRetry.status,
			firstRetry.err,
			secondRetry.message.ID,
			secondRetry.status,
			secondRetry.err,
		)
	}
	statuses := map[messaging.MessageCreateStatus]int{
		firstRetry.status:  1,
		secondRetry.status: 1,
	}
	if firstRetry.status == secondRetry.status {
		statuses[firstRetry.status] = 2
	}
	if statuses[messaging.MessageCreated] != 1 ||
		statuses[messaging.MessageDuplicate] != 1 {
		t.Fatalf("concurrent MMS retry statuses = %#v", statuses)
	}
	newAttempt := firstRetry.message
	if newAttempt.RetryOfMessageID != outbound.ID ||
		newAttempt.Attachment == nil ||
		newAttempt.Attachment.ID == pending.ID {
		t.Fatalf("outbound MMS new attempt = %#v", newAttempt)
	}
	var retryAttachmentCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM messaging_attachments
		WHERE message_id = $1
	`, newAttempt.ID).Scan(&retryAttachmentCount); err != nil {
		t.Fatalf("count concurrent MMS retry attachments: %v", err)
	}
	if retryAttachmentCount != 1 {
		t.Fatalf("concurrent MMS retry attachment count = %d, want 1", retryAttachmentCount)
	}
	newAttemptContent, err := module.OpenAttachment(
		context.Background(),
		identity,
		newAttempt.Attachment.ID,
	)
	if err != nil || !bytes.Equal(newAttemptContent.Content, png) {
		t.Fatalf("open retried outbound MMS = %#v, %v", newAttemptContent, err)
	}
	provider.sendResult = messaging.ProviderResult{MessageID: "provider-message-2"}
	if processed, err := module.ProcessNextCommand(context.Background()); err != nil ||
		!processed {
		t.Fatalf("send outbound MMS new attempt = %t, %v", processed, err)
	}
	if len(provider.commands) != 2 || provider.commands[1].MediaURL == "" {
		t.Fatalf("outbound MMS new-attempt command = %#v", provider.commands)
	}

	now = now.Add(time.Minute)
	rawInbound := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"message.received","id":"attachment-inbound-event","occurred_at":"%s","payload":{"id":"attachment-inbound-provider-id","from":{"phone_number":"+17275550119"},"to":[{"phone_number":"+17275550110"}],"text":"Please review.","media":[{"url":%q,"content_type":"application/pdf"}]}}}`,
		now.Format(time.RFC3339),
		mediaServer.URL,
	))
	timestamp = fmt.Sprintf("%d", now.Unix())
	signature = base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), rawInbound...),
	))
	if _, err := module.ReceiveWebhook(
		context.Background(),
		"",
		rawInbound,
		timestamp,
		signature,
	); err != nil {
		t.Fatalf("receive inbound MMS: %v", err)
	}
	if processed, err := module.ProcessNextReceipt(context.Background()); err != nil ||
		!processed {
		t.Fatalf("project inbound MMS = %t, %v", processed, err)
	}
	timeline, err := reads.QueryTimeline(
		context.Background(),
		workspace.QueryTimelineCommand{
			Identity: identity,
			ThreadID: outbound.Thread.ID,
		},
	)
	if err != nil {
		t.Fatalf("read inbound MMS timeline: %v", err)
	}
	inbound := timeline.Items[len(timeline.Items)-1].Message
	if inbound.Attachment == nil ||
		inbound.Attachment.State != messaging.AttachmentProcessing {
		t.Fatalf("processing inbound attachment = %#v", inbound)
	}
	attachmentID := inbound.Attachment.ID
	if processed, err := module.ProcessNextAttachment(context.Background()); err != nil ||
		!processed {
		t.Fatalf("fail expired inbound media copy = %t, %v", processed, err)
	}
	timeline, err = reads.QueryTimeline(
		context.Background(),
		workspace.QueryTimelineCommand{
			Identity: identity,
			ThreadID: outbound.Thread.ID,
		},
	)
	if err != nil ||
		timeline.Items[len(timeline.Items)-1].Message.Attachment.State !=
			messaging.AttachmentUnavailable {
		t.Fatalf("unavailable inbound attachment = %#v, %v", timeline, err)
	}
	retrying, err := module.RetryAttachment(
		context.Background(),
		messaging.RetryAttachmentCommand{
			Identity:     identity,
			AttachmentID: attachmentID,
		},
	)
	if err != nil || retrying.State != messaging.AttachmentProcessing {
		t.Fatalf("retry inbound attachment = %#v, %v", retrying, err)
	}
	mediaAvailable = true
	if processed, err := module.ProcessNextAttachment(context.Background()); err != nil ||
		!processed {
		t.Fatalf("copy inbound media = %t, %v", processed, err)
	}
	inboundContent, err := module.OpenAttachment(
		context.Background(),
		identity,
		attachmentID,
	)
	if err != nil ||
		inboundContent.Attachment.ID != attachmentID ||
		inboundContent.Attachment.State != messaging.AttachmentStored ||
		!bytes.Equal(inboundContent.Content, pdf) {
		t.Fatalf("stored inbound attachment = %#v, %v", inboundContent, err)
	}

	expiring, err := module.UploadAttachment(
		context.Background(),
		messaging.UploadAttachmentCommand{
			Identity:     identity,
			PracticeID:   authorization.Practice.ID,
			LocationID:   authorization.Locations[0].ID,
			FileName:     "expired.png",
			DeclaredType: "image/png",
			Content:      png,
		},
	)
	if err != nil {
		t.Fatalf("upload attachment for expiration: %v", err)
	}
	now = now.Add(16 * time.Minute)
	store.deleteError = errors.New("transient protected-object deletion failure")
	if err := module.ExpirePendingAttachments(
		context.Background(),
	); err == nil {
		t.Fatal("attachment expiration ignored object deletion failure")
	}
	var expiredRowExists bool
	if err := pool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1
			FROM messaging_attachments
			WHERE id = $1
		)
	`, expiring.ID).Scan(&expiredRowExists); err != nil {
		t.Fatalf("inspect failed attachment expiration: %v", err)
	}
	if !expiredRowExists {
		t.Fatal("failed object deletion removed its retryable attachment row")
	}
	if _, err := memoryStore.Get(
		context.Background(),
		"attachments/"+expiring.ID,
	); err != nil {
		t.Fatalf("failed expiration lost protected object: %v", err)
	}

	store.deleteError = nil
	if err := module.ExpirePendingAttachments(context.Background()); err != nil {
		t.Fatalf("retry attachment expiration: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1
			FROM messaging_attachments
			WHERE id = $1
		)
	`, expiring.ID).Scan(&expiredRowExists); err != nil {
		t.Fatalf("inspect retried attachment expiration: %v", err)
	}
	if expiredRowExists {
		t.Fatal("successful object deletion retained expired attachment row")
	}
	if _, err := memoryStore.Get(
		context.Background(),
		"attachments/"+expiring.ID,
	); !errors.Is(err, messaging.ErrObjectNotFound) {
		t.Fatalf("expired protected object error = %v, want not found", err)
	}
}

func TestPlatformOperatorMessagingWritesDirectlyAndAuditsAtomically(
	t *testing.T,
) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 29, 14, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	operator := access.Identity{
		Subject:       "messaging-operator-subject",
		Email:         "messaging-operator@acuity.test",
		EmailVerified: true,
	}
	if _, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment:       "test",
		RequestedBy:       "operator-messaging-test",
		PlatformOperators: []string{operator.Email},
		Practices: []access.PracticeProvision{{
			Key:       "support-message-a",
			Name:      "Support Message A",
			Locations: []access.LocationProvision{{Key: "office-a", Name: "Office A"}},
		}, {
			Key:       "support-message-b",
			Name:      "Support Message B",
			Locations: []access.LocationProvision{{Key: "office-b", Name: "Office B"}},
		}},
	}); err != nil {
		t.Fatalf("provision operator Messaging fixture: %v", err)
	}
	discovery, err := accessModule.DiscoverActor(context.Background(), operator)
	if err != nil || !discovery.PlatformOperator {
		t.Fatalf("discover Messaging Platform Operator = %#v, %v", discovery, err)
	}
	practices := make(map[string]access.PracticeAccess, len(discovery.Practices))
	for _, practice := range discovery.Practices {
		practices[practice.Practice.Name] = practice
	}
	practiceA := practices["Support Message A"]
	practiceB := practices["Support Message B"]
	if practiceA.Practice.ID == "" || practiceB.Practice.ID == "" {
		t.Fatalf("operator Messaging practices = %#v", discovery.Practices)
	}
	module := messaging.New(
		pool,
		accessModule,
		work.New(pool, accessModule, func() time.Time { return now }),
		&providerFixture{},
		messaging.Config{},
		func() time.Time { return now },
	)
	if err := module.Provision(context.Background(), []messaging.LocationProvision{{
		PracticeKey:        "support-message-a",
		LocationKey:        "office-a",
		Sender:             "+17275550120",
		MessagingProfileID: "support-profile-a",
	}, {
		PracticeKey:        "support-message-b",
		LocationKey:        "office-b",
		Sender:             "+17275550121",
		MessagingProfileID: "support-profile-b",
	}}); err != nil {
		t.Fatalf("provision operator Messaging senders: %v", err)
	}
	send := func(key string) error {
		_, _, err := module.Send(context.Background(), messaging.SendCommand{
			Identity:       operator,
			PracticeID:     practiceA.Practice.ID,
			LocationID:     practiceA.Locations[0].ID,
			Destination:    "+17275550199",
			Body:           "Operator office message.",
			IdempotencyKey: key,
		})
		return err
	}
	if _, err := pool.Exec(context.Background(), `
		ALTER TABLE access_audit_events
		ADD CONSTRAINT reject_message_sent_audit
		CHECK (action <> 'message.sent')
	`); err != nil {
		t.Fatalf("install Message audit failure fixture: %v", err)
	}
	if err := send("operator-message-audit-failure"); err == nil {
		t.Fatal("Message succeeded while its required operator audit failed")
	}
	var rolledBackThreads, rolledBackMessages, rolledBackCommands, messageAudits int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM messaging_threads),
			(SELECT count(*) FROM messaging_messages),
			(SELECT count(*) FROM messaging_provider_commands),
			(
				SELECT count(*)
				FROM access_audit_events
				WHERE action = 'message.sent'
			)
	`).Scan(
		&rolledBackThreads,
		&rolledBackMessages,
		&rolledBackCommands,
		&messageAudits,
	); err != nil {
		t.Fatalf("inspect Message audit rollback: %v", err)
	}
	if rolledBackThreads != 0 ||
		rolledBackMessages != 0 ||
		rolledBackCommands != 0 ||
		messageAudits != 0 {
		t.Fatalf(
			"failed Message audit was not atomic: %d/%d/%d rows, %d audits",
			rolledBackThreads,
			rolledBackMessages,
			rolledBackCommands,
			messageAudits,
		)
	}
	if _, err := pool.Exec(context.Background(), `
		ALTER TABLE access_audit_events
		DROP CONSTRAINT reject_message_sent_audit
	`); err != nil {
		t.Fatalf("remove Message audit failure fixture: %v", err)
	}
	if err := send("operator-message-success"); err != nil {
		t.Fatalf("send operator Message: %v", err)
	}
	var auditSubject string
	if err := pool.QueryRow(context.Background(), `
		SELECT actor_subject
		FROM access_audit_events
		WHERE action = 'message.sent'
	`).Scan(&auditSubject); err != nil {
		t.Fatalf("read operator Message audit: %v", err)
	}
	if auditSubject != operator.Subject {
		t.Fatalf("operator Message audit subject = %q", auditSubject)
	}
	if _, status, err := module.Send(
		context.Background(),
		messaging.SendCommand{
			Identity:       operator,
			PracticeID:     practiceB.Practice.ID,
			LocationID:     practiceB.Locations[0].ID,
			Destination:    "+17275550199",
			Body:           "Same actor and key in a different Practice.",
			IdempotencyKey: "operator-message-success",
		},
	); err != nil || status != messaging.MessageCreated {
		t.Fatalf(
			"Practice-scoped Message idempotency = %q, %v",
			status,
			err,
		)
	}
}

type providerFixture struct {
	commands        []messaging.ProviderCommand
	sendResult      messaging.ProviderResult
	sendError       error
	reconcileResult messaging.ProviderResult
	reconcileError  error
}

func (fixture *providerFixture) Send(
	_ context.Context,
	command messaging.ProviderCommand,
) (messaging.ProviderResult, error) {
	fixture.commands = append(fixture.commands, command)
	if fixture.sendError != nil {
		err := fixture.sendError
		fixture.sendError = nil
		result := fixture.sendResult
		fixture.sendResult = messaging.ProviderResult{}
		return result, err
	}
	if fixture.sendResult.MessageID != "" {
		result := fixture.sendResult
		fixture.sendResult = messaging.ProviderResult{}
		return result, nil
	}
	return messaging.ProviderResult{
		MessageID: "provider-message-1",
	}, nil
}

func (fixture *providerFixture) Reconcile(
	_ context.Context,
	providerMessageID string,
) (messaging.ProviderResult, error) {
	if fixture.reconcileError != nil {
		return messaging.ProviderResult{}, fixture.reconcileError
	}
	if fixture.reconcileResult.MessageID != providerMessageID {
		return messaging.ProviderResult{}, messaging.ErrAmbiguous
	}
	return fixture.reconcileResult, nil
}

type deleteFailingAttachmentStore struct {
	messaging.AttachmentObjectStore
	deleteError error
}

func (store *deleteFailingAttachmentStore) Delete(
	ctx context.Context,
	key string,
) error {
	if store.deleteError != nil {
		return store.deleteError
	}
	return store.AttachmentObjectStore.Delete(ctx, key)
}
