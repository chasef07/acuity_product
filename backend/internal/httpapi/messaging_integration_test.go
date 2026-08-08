package httpapi_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/google/uuid"
)

func TestGeneratedHTTPMessagingJourneyUsesProviderEvidenceAndExplicitTasks(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 29, 14, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	provisioned, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "slice-5-http-test",
		Practices: []access.PracticeProvision{{
			Key:       "message-http-practice",
			Name:      "Message HTTP Practice",
			Locations: []access.LocationProvision{{Key: "message-http-office", Name: "Message HTTP Office"}},
			Invitations: []access.InvitationProvision{{
				Key:           "message-http-staff",
				Email:         "staff@message-http.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
				ExpiresAt:     now.Add(time.Hour),
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision Messaging HTTP fixture: %v", err)
	}
	identity := access.Identity{
		Subject:       "message-http-staff",
		Email:         "staff@message-http.test",
		EmailVerified: true,
	}
	authorization, err := accessModule.AcceptInvitation(
		context.Background(),
		identity,
		provisioned.Invitations[0].Token,
	)
	if err != nil {
		t.Fatalf("accept Messaging HTTP invitation: %v", err)
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("create Messaging HTTP webhook key: %v", err)
	}
	workModule := work.New(pool, accessModule, func() time.Time { return now })
	messageProvider := &httpMessageProvider{}
	messageModule := messaging.New(
		pool,
		accessModule,
		workModule,
		messageProvider,
		messaging.Config{
			WebhookPublicKeys:  []ed25519.PublicKey{publicKey},
			WebhookTolerance:   time.Minute,
			AttachmentStore:    messaging.NewMemoryAttachmentStore(),
			MediaPublicBaseURL: "https://ingress.example/v1/provider/messaging-media",
			MediaSigningKey:    bytes.Repeat([]byte{9}, 32),
		},
		func() time.Time { return now },
	)
	if err := messageModule.Provision(
		context.Background(),
		[]messaging.LocationProvision{{
			PracticeKey:        "message-http-practice",
			LocationKey:        "message-http-office",
			Sender:             "+17275550100",
			MessagingProfileID: "profile-http",
		}},
	); err != nil {
		t.Fatalf("provision Messaging HTTP sender: %v", err)
	}
	serviceAuthenticator, err := access.NewServiceAuthenticator(
		"message-http-service-token",
		access.ServiceIdentity{
			Subject:       "message-http-service",
			PracticeID:    authorization.Practice.ID,
			LocationScope: access.LocationScopeAll,
			Capabilities:  []access.ServiceCapability{access.ServiceCapabilityHumanHandoff},
		},
	)
	if err != nil {
		t.Fatalf("create Messaging HTTP service authenticator: %v", err)
	}
	callingModule := humancalling.New(
		pool,
		accessModule,
		httpCallingProvider{},
		humancalling.Config{},
		nil,
	)
	portalHandler, err := httpapi.NewPortal(
		httpapi.Config{
			AllowedOrigin:  "http://localhost:3000",
			AcquireTimeout: time.Second,
		},
		pool,
		httpapi.PortalDependencies{
			Access:               accessModule,
			Authenticator:        staticAuthenticator{"message-token": identity},
			Calling:              callingModule,
			Messaging:            messageModule,
			Work:                 workModule,
			ServiceAuthenticator: serviceAuthenticator,
		},
	)
	if err != nil {
		t.Fatalf("create Messaging HTTP portal: %v", err)
	}
	portal := httptest.NewServer(portalHandler)
	defer portal.Close()

	sendBody, _ := json.Marshal(api.SendMessageRequest{
		PracticeId:     parsedUUID(t, authorization.Practice.ID),
		LocationId:     parsedUUID(t, authorization.Locations[0].ID),
		Destination:    stringPointer("(727) 555-0199"),
		Body:           "Your records are ready.",
		IdempotencyKey: "http-message-send-1",
	})
	sent := request(
		t,
		portal.Client(),
		http.MethodPost,
		portal.URL+"/v1/messages",
		"message-token",
		sendBody,
	)
	if sent.StatusCode != http.StatusCreated {
		t.Fatalf("send Message status = %d, body = %s", sent.StatusCode, readBody(t, sent))
	}
	var receipt api.MessageReceipt
	decode(t, sent, &receipt)
	if receipt.Message.Delivery != api.Sending ||
		receipt.Message.Sender != "+17275550100" ||
		receipt.Message.Destination != "+17275550199" {
		t.Fatalf("durable HTTP Message = %#v", receipt)
	}
	if len(messageProvider.commands) != 0 {
		t.Fatalf("browser request contacted provider: %#v", messageProvider.commands)
	}
	if processed, err := messageModule.ProcessNextCommand(context.Background()); err != nil || !processed {
		t.Fatalf("process HTTP Message command = %t, %v", processed, err)
	}

	ingressHandler, err := httpapi.NewProviderIngressWithMessaging(
		httpapi.Config{AcquireTimeout: time.Second},
		pool,
		humancalling.New(
			pool,
			nil,
			nil,
			humancalling.Config{
				WebhookPublicKeys: []ed25519.PublicKey{publicKey},
				WebhookTolerance:  time.Minute,
			},
			nil,
		),
		messageModule,
	)
	if err != nil {
		t.Fatalf("create Messaging provider ingress: %v", err)
	}
	ingress := httptest.NewServer(ingressHandler)
	defer ingress.Close()
	now = now.Add(time.Minute)
	delivery := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"message.finalized","id":"http-delivery-event","occurred_at":"%s","payload":{"id":"http-provider-message-1","from":"+17275550100","to":"+17275550199","delivery_status":"delivered"}}}`,
		now.Format(time.RFC3339),
	))
	deliverSignedWebhook(
		t,
		ingress.Client(),
		ingress.URL+"/v1/provider/telnyx/messaging-webhooks/"+
			messageProvider.commands[0].CallbackToken,
		delivery,
		now,
		privateKey,
	)
	if processed, err := messageModule.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("process HTTP delivery receipt = %t, %v", processed, err)
	}

	now = now.Add(time.Minute)
	inbound := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"message.received","id":"http-inbound-event","occurred_at":"%s","payload":{"id":"http-provider-inbound-1","from":"+17275550199","to":"+17275550100","delivery_status":"delivered","text":"Thank you."}}}`,
		now.Format(time.RFC3339),
	))
	deliverSignedWebhook(
		t,
		ingress.Client(),
		ingress.URL+"/v1/provider/telnyx/messaging-webhooks",
		inbound,
		now,
		privateKey,
	)
	if processed, err := messageModule.ProcessNextReceipt(context.Background()); err != nil || !processed {
		t.Fatalf("process HTTP inbound receipt = %t, %v", processed, err)
	}

	queryBody, _ := json.Marshal(api.MessageThreadQueryRequest{
		PracticeId: parsedUUID(t, authorization.Practice.ID),
		LocationId: parsedUUIDPointer(t, authorization.Locations[0].ID),
	})
	threadsResponse := request(
		t,
		portal.Client(),
		http.MethodPost,
		portal.URL+"/v1/message-threads/query",
		"message-token",
		queryBody,
	)
	if threadsResponse.StatusCode != http.StatusOK {
		t.Fatalf("query Threads status = %d, body = %s", threadsResponse.StatusCode, readBody(t, threadsResponse))
	}
	var threads api.MessageThreadPage
	decode(t, threadsResponse, &threads)
	if len(threads.Items) != 1 ||
		!threads.Items[0].Unread ||
		threads.Items[0].Preview != "Thank you." {
		t.Fatalf("HTTP Message Threads = %#v", threads)
	}
	timelineResponse := request(
		t,
		portal.Client(),
		http.MethodGet,
		portal.URL+"/v1/message-threads/"+threads.Items[0].Id.String()+"/timeline",
		"message-token",
		nil,
	)
	if timelineResponse.StatusCode != http.StatusOK {
		t.Fatalf("timeline status = %d, body = %s", timelineResponse.StatusCode, readBody(t, timelineResponse))
	}
	var timeline api.ConversationTimelinePage
	decode(t, timelineResponse, &timeline)
	if len(timeline.Items) != 2 ||
		timeline.Items[0].Message == nil ||
		timeline.Items[0].Message.Delivery != api.Delivered ||
		timeline.Items[1].Message == nil ||
		timeline.Items[1].Message.Direction != api.MessageDirectionINBOUND {
		t.Fatalf("HTTP conversation timeline = %#v", timeline)
	}
	taskResponse := request(
		t,
		portal.Client(),
		http.MethodPost,
		portal.URL+"/v1/messages/"+timeline.Items[1].Id.String()+"/follow-up-task",
		"message-token",
		[]byte(`{}`),
	)
	if taskResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create Message Task status = %d, body = %s", taskResponse.StatusCode, readBody(t, taskResponse))
	}
	var task api.Task
	decode(t, taskResponse, &task)
	if task.Origin != api.STAFFMESSAGEFOLLOWUP ||
		task.MessageId == nil ||
		task.MessageThreadId == nil ||
		task.State != api.TaskStateOPEN {
		t.Fatalf("HTTP Message Task = %#v", task)
	}
	var engagementCallID string
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO human_calling_calls (
			practice_id,
			location_id,
			provider_termination,
			terminal_outcome,
			disposition_outcome,
			ended_at,
			direction,
			entry_point,
			destination_phone,
			outbound_caller_id,
			initiating_subject,
			outbound_idempotency_key,
			outbound_input_fingerprint,
			created_at,
			updated_at
		)
		VALUES (
			$1, $2, 'NO_ANSWER', 'RESOLVED', 'NO_FOLLOW_UP', $3,
			'OUTBOUND', 'STANDALONE', $4, '+17275550100', $5,
			'history-call', $6, $3, $3
		)
		RETURNING id::text
	`, authorization.Practice.ID, authorization.Locations[0].ID, now,
		"+17275550199", identity.Subject, make([]byte, 32),
	).Scan(&engagementCallID); err != nil {
		t.Fatalf("create Engagement History Call: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_call_legs (
			call_id, role, sequence, staff_subject, staff_session_id, state,
			provider_call_control_id, provider_call_leg_id,
			answered_at, bridge_pending_at, bridged_at, ending_at, ended_at,
			created_at, updated_at
		) VALUES ($1, 'STAFF', 1, $2, 'history-session', 'ENDED',
			'history-staff-control', 'history-staff-leg', $3, $3, $3, $3, $3, $3, $3)
	`, engagementCallID, identity.Subject, now); err != nil {
		t.Fatalf("create Engagement History Staff CallLeg: %v", err)
	}
	engagementResponse := request(
		t,
		portal.Client(),
		http.MethodGet,
		portal.URL+"/v1/calling/calls/"+engagementCallID+"/engagement-history",
		"message-token",
		nil,
	)
	if engagementResponse.StatusCode != http.StatusOK {
		t.Fatalf(
			"Engagement History status = %d, body = %s",
			engagementResponse.StatusCode,
			readBody(t, engagementResponse),
		)
	}
	var engagement api.ConversationTimelinePage
	decode(t, engagementResponse, &engagement)
	types := map[api.ConversationTimelineItemType]bool{}
	for _, item := range engagement.Items {
		types[item.Type] = true
		switch item.Type {
		case api.ConversationTimelineItemTypeMESSAGE:
			if item.Message == nil ||
				item.Message.Thread.LocationName != authorization.Locations[0].Name {
				t.Fatalf("Location-labeled Message history = %#v", item)
			}
		case api.ConversationTimelineItemTypeCALL:
			if item.Call == nil ||
				item.Call.LocationName != authorization.Locations[0].Name {
				t.Fatalf("Location-labeled Call history = %#v", item)
			}
		case api.ConversationTimelineItemTypeTASK:
			if item.Task == nil ||
				item.Task.LocationName != authorization.Locations[0].Name {
				t.Fatalf("Location-labeled Task history = %#v", item)
			}
		}
	}
	if !types[api.ConversationTimelineItemTypeMESSAGE] ||
		!types[api.ConversationTimelineItemTypeCALL] ||
		!types[api.ConversationTimelineItemTypeTASK] {
		t.Fatalf("combined Engagement History = %#v", engagement)
	}
	engagementQueryBody, _ := json.Marshal(api.EngagementQueryRequest{
		PracticeId: parsedUUID(t, authorization.Practice.ID),
		Phone:      "(727) 555-0199",
	})
	if _, err := messageModule.QueryEngagements(
		context.Background(),
		messaging.QueryEngagementsCommand{
			Identity:   identity,
			PracticeID: authorization.Practice.ID,
			Phone:      "(727) 555-0199",
		},
	); err != nil {
		t.Fatalf("query Engagements module: %v", err)
	}
	engagementQueryResponse := request(
		t,
		portal.Client(),
		http.MethodPost,
		portal.URL+"/v1/engagements/query",
		"message-token",
		engagementQueryBody,
	)
	if engagementQueryResponse.StatusCode != http.StatusOK {
		t.Fatalf(
			"query Engagements status = %d, body = %s",
			engagementQueryResponse.StatusCode,
			readBody(t, engagementQueryResponse),
		)
	}
	var engagementPage api.EngagementPage
	decode(t, engagementQueryResponse, &engagementPage)
	if len(engagementPage.Items) != 1 ||
		engagementPage.Items[0].Phone != "+17275550199" ||
		engagementPage.Items[0].OpenTaskCount != 1 ||
		len(engagementPage.Items[0].Locations) != 1 {
		t.Fatalf("phone-led Engagement result = %#v", engagementPage)
	}
	for _, endpoint := range []string{
		portal.URL + "/v1/engagements/+17275550199/timeline?practiceId=" +
			url.QueryEscape(authorization.Practice.ID),
		portal.URL + "/v1/tasks/" + task.Id.String() + "/engagement-history",
	} {
		response := request(
			t,
			portal.Client(),
			http.MethodGet,
			endpoint,
			"message-token",
			nil,
		)
		if response.StatusCode != http.StatusOK {
			t.Fatalf(
				"phone-led timeline %s status = %d, body = %s",
				endpoint,
				response.StatusCode,
				readBody(t, response),
			)
		}
		var page api.ConversationTimelinePage
		decode(t, response, &page)
		if len(page.Items) != len(engagement.Items) {
			t.Fatalf("phone-led timeline %s = %#v", endpoint, page)
		}
	}
	pagedIDs := map[string]bool{}
	cursor := ""
	for pageNumber := 0; pageNumber < 10; pageNumber++ {
		endpoint := portal.URL + "/v1/calling/calls/" + engagementCallID +
			"/engagement-history?limit=2"
		if cursor != "" {
			endpoint += "&cursor=" + url.QueryEscape(cursor)
		}
		pageResponse := request(
			t,
			portal.Client(),
			http.MethodGet,
			endpoint,
			"message-token",
			nil,
		)
		if pageResponse.StatusCode != http.StatusOK {
			t.Fatalf(
				"paged Engagement History cursor %q status = %d, body = %s",
				cursor,
				pageResponse.StatusCode,
				readBody(t, pageResponse),
			)
		}
		var page api.ConversationTimelinePage
		decode(t, pageResponse, &page)
		for index, item := range page.Items {
			key := string(item.Type) + ":" + item.Id.String()
			if pagedIDs[key] {
				t.Fatalf("duplicate paged Engagement History item %s", key)
			}
			pagedIDs[key] = true
			if index > 0 &&
				page.Items[index-1].OccurredAt.After(item.OccurredAt) {
				t.Fatalf("Engagement History page is not chronological: %#v", page)
			}
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(pagedIDs) != len(engagement.Items) {
		t.Fatalf(
			"paged Engagement History items = %d, want %d",
			len(pagedIDs),
			len(engagement.Items),
		)
	}

	png := append(
		[]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'},
		bytes.Repeat([]byte{0}, 64)...,
	)
	uploadBody, _ := json.Marshal(api.UploadMessageAttachmentRequest{
		PracticeId:    parsedUUID(t, authorization.Practice.ID),
		LocationId:    parsedUUID(t, authorization.Locations[0].ID),
		FileName:      "photo.png",
		ContentType:   api.UploadMessageAttachmentRequestContentTypeImagepng,
		ContentBase64: base64.StdEncoding.EncodeToString(png),
	})
	uploadedResponse := request(
		t,
		portal.Client(),
		http.MethodPost,
		portal.URL+"/v1/attachments",
		"message-token",
		uploadBody,
	)
	if uploadedResponse.StatusCode != http.StatusCreated {
		t.Fatalf(
			"upload attachment status = %d, body = %s",
			uploadedResponse.StatusCode,
			readBody(t, uploadedResponse),
		)
	}
	var uploaded api.MessageAttachment
	decode(t, uploadedResponse, &uploaded)
	attachmentID := uploaded.Id
	attachmentSendBody, _ := json.Marshal(api.SendMessageRequest{
		PracticeId:     parsedUUID(t, authorization.Practice.ID),
		LocationId:     parsedUUID(t, authorization.Locations[0].ID),
		ThreadId:       &threads.Items[0].Id,
		Body:           "",
		AttachmentId:   &attachmentID,
		IdempotencyKey: "http-message-attachment-1",
	})
	attachmentSendResponse := request(
		t,
		portal.Client(),
		http.MethodPost,
		portal.URL+"/v1/messages",
		"message-token",
		attachmentSendBody,
	)
	if attachmentSendResponse.StatusCode != http.StatusCreated {
		t.Fatalf(
			"send attachment status = %d, body = %s",
			attachmentSendResponse.StatusCode,
			readBody(t, attachmentSendResponse),
		)
	}
	var attachmentReceipt api.MessageReceipt
	decode(t, attachmentSendResponse, &attachmentReceipt)
	if attachmentReceipt.Message.Attachment == nil ||
		attachmentReceipt.Message.Attachment.Id != attachmentID ||
		attachmentReceipt.Message.Body != "" {
		t.Fatalf("HTTP media-only Message = %#v", attachmentReceipt)
	}
	downloaded := request(
		t,
		portal.Client(),
		http.MethodGet,
		portal.URL+"/v1/attachments/"+attachmentID.String(),
		"message-token",
		nil,
	)
	if downloaded.StatusCode != http.StatusOK ||
		downloaded.Header.Get("Content-Type") != "image/png" ||
		!bytes.Equal([]byte(readBody(t, downloaded)), png) {
		t.Fatalf("authorized attachment response = %d, %#v", downloaded.StatusCode, downloaded.Header)
	}
}

type httpMessageProvider struct {
	commands []messaging.ProviderCommand
}

func (provider *httpMessageProvider) Send(
	_ context.Context,
	command messaging.ProviderCommand,
) (messaging.ProviderResult, error) {
	provider.commands = append(provider.commands, command)
	return messaging.ProviderResult{MessageID: "http-provider-message-1"}, nil
}

func (*httpMessageProvider) Reconcile(
	_ context.Context,
	_ string,
) (messaging.ProviderResult, error) {
	return messaging.ProviderResult{}, nil
}

func deliverSignedWebhook(
	t *testing.T,
	client *http.Client,
	target string,
	body []byte,
	now time.Time,
	privateKey ed25519.PrivateKey,
) {
	t.Helper()
	timestamp := fmt.Sprintf("%d", now.Unix())
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), body...),
	))
	request, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("telnyx-timestamp", timestamp)
	request.Header.Set("telnyx-signature-ed25519", signature)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("webhook status = %d, body = %s", response.StatusCode, readBody(t, response))
	}
}

func parsedUUID(t *testing.T, value string) uuid.UUID {
	t.Helper()
	parsed, err := uuid.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func parsedUUIDPointer(t *testing.T, value string) *uuid.UUID {
	parsed := parsedUUID(t, value)
	return &parsed
}

func stringPointer(value string) *string {
	return &value
}
