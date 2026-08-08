package httpapi_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/authn"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/observability"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestGeneratedHTTPSInterfaceLoadsOnlyTheAuthorizedEmptyWorkspace(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	provisioned, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment:       "test",
		RequestedBy:       "slice-1-http-test",
		PlatformOperators: []string{"founder@acuity.test"},
		Practices: []access.PracticeProvision{{
			Key:  "abita-eye-group",
			Name: "Abita Eye Group",
			Locations: []access.LocationProvision{
				{Key: "fixture-location-1", Name: "Fixture Location 1"},
				{Key: "fixture-location-2", Name: "Fixture Location 2"},
			},
			Invitations: []access.InvitationProvision{{
				Key:                  "selected-staff",
				Email:                "selected@abita.test",
				Role:                 access.RoleStaff,
				LocationScope:        access.LocationScopeSelected,
				SelectedLocationKeys: []string{"fixture-location-1"},
				ExpiresAt:            now.Add(24 * time.Hour),
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision HTTP fixture: %v", err)
	}
	identity := access.Identity{
		Subject:       "selected-subject",
		Email:         "selected@abita.test",
		EmailVerified: true,
	}
	handler, err := newPortalHandler(t, httpapi.Config{
		AllowedOrigins: []string{"http://localhost:3000"},
		AcquireTimeout: 500 * time.Millisecond,
	}, pool, accessModule, staticAuthenticator{
		"selected-token": identity,
	})
	if err != nil {
		t.Fatalf("new HTTP adapter: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	invitationBody, _ := json.Marshal(api.InvitationCredentialRequest{
		Token: provisioned.Invitations[0].Token,
	})
	previewResponse := request(t, server.Client(), http.MethodPost,
		server.URL+"/v1/invitations/inspect",
		"", invitationBody,
	)
	if previewResponse.StatusCode != http.StatusOK {
		t.Fatalf("invitation preview status = %d, body = %s", previewResponse.StatusCode, readBody(t, previewResponse))
	}
	_ = previewResponse.Body.Close()

	ineligibleBody, _ := json.Marshal(api.SignUpEligibilityRequest{
		Email: "somebody-else@abita.test",
		InvitationToken: func() *string {
			token := provisioned.Invitations[0].Token
			return &token
		}(),
	})
	ineligible := request(t, server.Client(), http.MethodPost,
		server.URL+"/v1/access/sign-up-eligibility",
		"", ineligibleBody,
	)
	if ineligible.StatusCode != http.StatusForbidden {
		t.Fatalf("ineligible sign-up status = %d, body = %s", ineligible.StatusCode, readBody(t, ineligible))
	}
	_ = ineligible.Body.Close()

	accepted := request(t, server.Client(), http.MethodPost,
		server.URL+"/v1/invitations/accept",
		"selected-token", invitationBody,
	)
	if accepted.StatusCode != http.StatusOK {
		t.Fatalf("accept status = %d, body = %s", accepted.StatusCode, readBody(t, accepted))
	}
	var authorization api.Authorization
	decode(t, accepted, &authorization)
	if authorization.Membership == nil || len(authorization.Locations) != 1 {
		t.Fatalf("accepted authorization = %#v", authorization)
	}

	discovered := request(t, server.Client(), http.MethodGet,
		server.URL+"/v1/access",
		"selected-token", nil,
	)
	if discovered.StatusCode != http.StatusOK {
		t.Fatalf("discovery status = %d, body = %s", discovered.StatusCode, readBody(t, discovered))
	}
	var accessDiscovery api.AccessDiscovery
	decode(t, discovered, &accessDiscovery)
	if len(accessDiscovery.Practices) != 1 ||
		len(accessDiscovery.Practices[0].Locations) != 1 {
		t.Fatalf("discovery = %#v", accessDiscovery)
	}

	workspaceURL := server.URL + "/v1/workspace?" + url.Values{
		"practiceId": {authorization.Practice.Id.String()},
		"locationId": {authorization.Locations[0].Id.String()},
	}.Encode()
	workspace := request(t, server.Client(), http.MethodGet, workspaceURL, "selected-token", nil)
	if workspace.StatusCode != http.StatusOK {
		t.Fatalf("workspace status = %d, body = %s", workspace.StatusCode, readBody(t, workspace))
	}
	var snapshot api.WorkspaceSnapshot
	decode(t, workspace, &snapshot)
	if snapshot.State != api.EMPTY ||
		snapshot.SchemaVersion != api.N20260724 ||
		snapshot.Practice.Name != "Abita Eye Group" ||
		snapshot.Location.Name != "Fixture Location 1" {
		t.Fatalf("workspace snapshot = %#v", snapshot)
	}

	operatorDiscovery, err := accessModule.DiscoverActor(context.Background(), access.Identity{
		Subject:       "founder-subject",
		Email:         "founder@acuity.test",
		EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("discover test operator: %v", err)
	}
	unauthorizedLocationID := operatorDiscovery.Practices[0].Locations[1].ID
	crossLocationURL := server.URL + "/v1/workspace?" + url.Values{
		"practiceId": {authorization.Practice.Id.String()},
		"locationId": {unauthorizedLocationID},
	}.Encode()
	crossLocation := request(t, server.Client(), http.MethodGet, crossLocationURL, "selected-token", nil)
	if crossLocation.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-Location status = %d, body = %s", crossLocation.StatusCode, readBody(t, crossLocation))
	}
	denialBody := readBody(t, crossLocation)
	if strings.Contains(denialBody, "Fixture Location 2") {
		t.Fatalf("cross-Location denial leaked protected data: %s", denialBody)
	}
	_ = crossLocation.Body.Close()

	missingCredential := request(t, server.Client(), http.MethodGet,
		server.URL+"/v1/access",
		"", nil,
	)
	if missingCredential.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing credential status = %d, body = %s", missingCredential.StatusCode, readBody(t, missingCredential))
	}
	_ = missingCredential.Body.Close()
}

func TestVoicemailPlaybackStreamsProviderRangeResponse(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	var metrics bytes.Buffer
	observer := observability.NewLogger(
		observability.RuntimePortalAPI,
		"voicemail-playback-test",
		slog.New(slog.NewJSONHandler(&metrics, nil)),
	)
	accessModule := access.New(pool, func() time.Time { return now })
	provisioned, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "voicemail-http-test",
		Practices: []access.PracticeProvision{{
			Key:  "voicemail-http-practice",
			Name: "Voicemail HTTP Practice",
			Locations: []access.LocationProvision{
				{Key: "voicemail-http-location", Name: "Voicemail HTTP Location"},
				{Key: "voicemail-hidden-location", Name: "Voicemail Hidden Location"},
			},
			Invitations: []access.InvitationProvision{
				{
					Key:           "voicemail-http-staff",
					Email:         "voicemail-http@synthetic.test",
					Role:          access.RoleStaff,
					LocationScope: access.LocationScopeAll,
					ExpiresAt:     now.Add(time.Hour),
				},
				{
					Key:                  "voicemail-hidden-staff",
					Email:                "voicemail-hidden@synthetic.test",
					Role:                 access.RoleStaff,
					LocationScope:        access.LocationScopeSelected,
					SelectedLocationKeys: []string{"voicemail-hidden-location"},
					ExpiresAt:            now.Add(time.Hour),
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("provision voicemail HTTP fixture: %v", err)
	}
	identity := access.Identity{
		Subject:       "voicemail-http-subject",
		Email:         "voicemail-http@synthetic.test",
		EmailVerified: true,
	}
	authorization, err := accessModule.AcceptInvitation(
		context.Background(),
		identity,
		provisioned.Invitations[0].Token,
	)
	if err != nil {
		t.Fatalf("accept voicemail HTTP invitation: %v", err)
	}
	voicemailLocationID := ""
	for _, location := range authorization.Locations {
		if location.Name == "Voicemail HTTP Location" {
			voicemailLocationID = location.ID
			break
		}
	}
	if voicemailLocationID == "" {
		t.Fatal("voicemail HTTP Location is missing from authorization")
	}
	hiddenIdentity := access.Identity{
		Subject:       "voicemail-hidden-subject",
		Email:         "voicemail-hidden@synthetic.test",
		EmailVerified: true,
	}
	if _, err := accessModule.AcceptInvitation(
		context.Background(),
		hiddenIdentity,
		provisioned.Invitations[1].Token,
	); err != nil {
		t.Fatalf("accept hidden Location invitation: %v", err)
	}
	handoffID := uuid.NewString()
	callID := uuid.NewString()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_handoffs (
			id, service_subject, practice_id, location_id, source_call_id,
			idempotency_key, input_fingerprint, phone,
			expires_at, consumed_at, created_at
		)
		VALUES ($1, 'voicemail-http-service', $2, $3, 'voicemail-http-source',
			'voicemail-http-key', $4, '+15555550100', $5, $6, $6)
	`, handoffID, authorization.Practice.ID, voicemailLocationID,
		[]byte(callID), now.Add(time.Minute), now,
	); err != nil {
		t.Fatalf("insert voicemail HTTP handoff: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_calls (
			id, source_handoff_id, practice_id, location_id, caller_phone,
			terminal_outcome, ended_at, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, '+15555550100', 'VOICEMAIL', $5, $6, $5)
	`, callID, handoffID, authorization.Practice.ID,
		voicemailLocationID, now.Add(12*time.Second), now,
	); err != nil {
		t.Fatalf("insert voicemail HTTP Call: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_call_legs (
			call_id, role, sequence, state, provider_call_control_id,
			provider_call_leg_id, provider_call_session_id, ending_at, ended_at,
			created_at, updated_at
		) VALUES ($1, 'CALLER', 1, 'ENDED', 'voicemail-http-control',
			'voicemail-http-leg', 'voicemail-http-session', $2, $2, $3, $2)
	`, callID, now.Add(12*time.Second), now); err != nil {
		t.Fatalf("insert voicemail HTTP Caller CallLeg: %v", err)
	}
	tx, err := pool.BeginTx(context.Background(), pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin voicemail HTTP Task: %v", err)
	}
	task, err := work.New(pool, accessModule, func() time.Time { return now }).EnsureRecoveryTask(
		context.Background(),
		tx,
		work.EnsureRecoveryTaskCommand{
			CallID:     callID,
			PracticeID: authorization.Practice.ID,
			LocationID: voicemailLocationID,
			Phone:      "+15555550100",
			Outcome:    work.RecoveryOutcomeVoicemail,
			OccurredAt: now.Add(12 * time.Second),
		},
	)
	if err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("create voicemail HTTP Task: %v", err)
	}
	if _, err := tx.Exec(context.Background(), `
		INSERT INTO human_calling_voicemails (
			call_id, practice_id, location_id, task_id, outcome, audio_state,
			provider_recording_id, recording_started_at, recording_ended_at,
			duration_millis, created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, 'VOICEMAIL', 'READY',
			'voicemail-http-recording', $5, $6, 12000, $5, $6)
	`, callID, authorization.Practice.ID, voicemailLocationID,
		task.ID, now, now.Add(12*time.Second),
	); err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("insert voicemail HTTP evidence: %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit voicemail HTTP fixture: %v", err)
	}

	audio := &httpVoicemailAudio{}
	calling := humancalling.New(
		pool,
		accessModule,
		httpCallingProvider{},
		humancalling.Config{
			PlaybackSigningKey:     []byte("abcdef0123456789abcdef0123456789"),
			VoicemailAudioProvider: audio,
			Observer:               observer,
		},
		func() time.Time { return now },
	)
	handler, err := newPortalHandlerWithCalling(
		t,
		httpapi.Config{AllowedOrigins: []string{"http://localhost:3000"}, AcquireTimeout: time.Second},
		pool,
		accessModule,
		staticAuthenticator{
			"voicemail-http-token":   identity,
			"voicemail-hidden-token": hiddenIdentity,
		},
		calling,
	)
	if err != nil {
		t.Fatalf("new voicemail HTTP adapter: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	capabilityResponse := request(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/v1/calling/calls/"+callID+"/voicemail-playback",
		"voicemail-http-token",
		nil,
	)
	if capabilityResponse.StatusCode != http.StatusOK {
		t.Fatalf("voicemail capability status = %d, body = %s", capabilityResponse.StatusCode, readBody(t, capabilityResponse))
	}
	var capability api.VoicemailPlaybackCapability
	decode(t, capabilityResponse, &capability)
	deniedRequest, err := http.NewRequest(
		http.MethodGet,
		server.URL+"/v1/calling/voicemail-playback/"+url.PathEscape(capability.Token),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	deniedRequest.Header.Set("Authorization", "Bearer voicemail-hidden-token")
	deniedResponse, err := server.Client().Do(deniedRequest)
	if err != nil {
		t.Fatalf("request cross-Location voicemail playback: %v", err)
	}
	_ = deniedResponse.Body.Close()
	if deniedResponse.StatusCode != http.StatusForbidden || audio.calls != 0 {
		t.Fatalf(
			"cross-Location playback = status:%d provider-calls:%d",
			deniedResponse.StatusCode,
			audio.calls,
		)
	}
	playbackRequest, err := http.NewRequest(
		http.MethodGet,
		server.URL+"/v1/calling/voicemail-playback/"+url.PathEscape(capability.Token),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	playbackRequest.Header.Set("Authorization", "Bearer voicemail-http-token")
	playbackRequest.Header.Set("Range", "bytes=0-3")
	playbackResponse, err := server.Client().Do(playbackRequest)
	if err != nil {
		t.Fatalf("request voicemail playback: %v", err)
	}
	defer playbackResponse.Body.Close()
	body, err := io.ReadAll(playbackResponse.Body)
	if err != nil {
		t.Fatalf("read voicemail playback response: %v", err)
	}
	if playbackResponse.StatusCode != http.StatusPartialContent ||
		playbackResponse.Header.Get("Accept-Ranges") != "bytes" ||
		playbackResponse.Header.Get("Content-Range") != "bytes 0-3/13" ||
		playbackResponse.Header.Get("Content-Length") != "4" ||
		playbackResponse.Header.Get("Content-Type") != "audio/mpeg" ||
		string(body) != "synt" ||
		audio.rangeHeader != "bytes=0-3" {
		t.Fatalf("voicemail playback response = status:%d headers:%v body:%q fixture:%#v",
			playbackResponse.StatusCode, playbackResponse.Header, body, audio)
	}

	metrics.Reset()
	malformedRange := "items 0-3/13"
	audio.contentRange = &malformedRange
	malformedRequest, err := http.NewRequest(
		http.MethodGet,
		server.URL+"/v1/calling/voicemail-playback/"+url.PathEscape(capability.Token),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	malformedRequest.Header.Set("Authorization", "Bearer voicemail-http-token")
	malformedRequest.Header.Set("Range", "bytes=0-3")
	malformedResponse, err := server.Client().Do(malformedRequest)
	if err != nil {
		t.Fatalf("request malformed partial voicemail: %v", err)
	}
	var malformedEnvelope api.ErrorEnvelope
	decode(t, malformedResponse, &malformedEnvelope)
	if malformedResponse.StatusCode != http.StatusServiceUnavailable ||
		malformedResponse.Header.Get("Content-Range") != "" ||
		malformedEnvelope.Error.Code != "VOICEMAIL_UNAVAILABLE" ||
		!strings.Contains(metrics.String(), `"outcome":"invalid_response"`) ||
		strings.Contains(metrics.String(), `"outcome":"succeeded"`) {
		t.Fatalf("malformed partial response = status:%d headers:%v body:%#v metrics:%s",
			malformedResponse.StatusCode, malformedResponse.Header,
			malformedEnvelope, metrics.String())
	}

	metrics.Reset()
	audio.contentRange = nil
	audio.contentLength = "invalid"
	audio.failStream = true
	failedStreamRequest, err := http.NewRequest(
		http.MethodGet,
		server.URL+"/v1/calling/voicemail-playback/"+url.PathEscape(capability.Token),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	failedStreamRequest.Header.Set("Authorization", "Bearer voicemail-http-token")
	failedStreamRequest.Header.Set("Range", "bytes=0-3")
	failedStreamResponse, requestErr := server.Client().Do(failedStreamRequest)
	var readErr error
	if failedStreamResponse != nil {
		_, readErr = io.ReadAll(failedStreamResponse.Body)
		_ = failedStreamResponse.Body.Close()
	}
	if (requestErr == nil && readErr == nil) ||
		!strings.Contains(metrics.String(), `"outcome":"unavailable"`) ||
		strings.Contains(metrics.String(), `"outcome":"succeeded"`) {
		t.Fatalf("failed voicemail stream = request-err:%v read-err:%v metrics:%s",
			requestErr, readErr, metrics.String())
	}
	audio.failStream = false

	failures := []struct {
		name       string
		reason     humancalling.VoicemailUnavailableReason
		retryAfter string
		status     int
		retryable  bool
	}{
		{name: "recording not found", reason: humancalling.VoicemailRecordingNotFound, status: http.StatusNotFound},
		{name: "provider auth", reason: humancalling.VoicemailProviderAuth, status: http.StatusServiceUnavailable},
		{name: "provider rate limited", reason: humancalling.VoicemailProviderRateLimited, retryAfter: "7", status: http.StatusServiceUnavailable, retryable: true},
		{name: "provider timeout", reason: humancalling.VoicemailProviderTimeout, status: http.StatusGatewayTimeout, retryable: true},
		{name: "provider unavailable", reason: humancalling.VoicemailProviderUnavailable, status: http.StatusServiceUnavailable, retryable: true},
		{name: "recording URL expired", reason: humancalling.VoicemailRecordingURLExpired, status: http.StatusServiceUnavailable, retryable: true},
	}
	for _, failure := range failures {
		t.Run(failure.name, func(t *testing.T) {
			audio.err = &humancalling.VoicemailUnavailableError{
				Reason:     failure.reason,
				RetryAfter: failure.retryAfter,
			}
			request, err := http.NewRequest(
				http.MethodGet,
				server.URL+"/v1/calling/voicemail-playback/"+url.PathEscape(capability.Token),
				nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer voicemail-http-token")
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatalf("request unavailable voicemail: %v", err)
			}
			defer response.Body.Close()
			var envelope api.ErrorEnvelope
			decode(t, response, &envelope)
			if response.StatusCode != failure.status ||
				envelope.Error.Code != "VOICEMAIL_UNAVAILABLE" ||
				envelope.Error.Retryable != failure.retryable ||
				response.Header.Get("Retry-After") != failure.retryAfter {
				t.Fatalf("unavailable voicemail response = status:%d headers:%v body:%#v",
					response.StatusCode, response.Header, envelope)
			}
		})
	}
}

func TestGeneratedHTTPTaskInterfacePreservesTheSharedLifecycle(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 28, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	provisioned, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "slice-3-http-test",
		Practices: []access.PracticeProvision{{
			Key:       "task-practice",
			Name:      "Task Practice",
			Locations: []access.LocationProvision{{Key: "task-office", Name: "Task Office"}},
			Invitations: []access.InvitationProvision{{
				Key:           "task-staff",
				Email:         "task-staff@synthetic.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
				ExpiresAt:     now.Add(time.Hour),
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision Task HTTP fixture: %v", err)
	}
	identity := access.Identity{
		Subject:       "task-staff-subject",
		Email:         "task-staff@synthetic.test",
		EmailVerified: true,
	}
	authorization, err := accessModule.AcceptInvitation(
		context.Background(),
		identity,
		provisioned.Invitations[0].Token,
	)
	if err != nil {
		t.Fatalf("accept Task HTTP invitation: %v", err)
	}
	handoffID := uuid.NewString()
	callID := uuid.NewString()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_handoffs (
			id,
			service_subject,
			practice_id,
			location_id,
			source_call_id,
			idempotency_key,
			input_fingerprint,
			phone,
			phone_source,
			display_name,
			name_source,
			transfer_reason,
			reason_source,
			expires_at,
			consumed_at,
			created_at
		)
		VALUES ($1, 'abita-task-http', $2, $3, 'task-http-source',
			'task-http-idempotency', $4, '+19855550100', 'Abita',
			'HTTP Caller', 'Abita', 'Verify referral', 'Abita AI', $5, $6, $6)
	`, handoffID, authorization.Practice.ID, authorization.Locations[0].ID,
		[]byte(callID), now.Add(time.Minute), now,
	); err != nil {
		t.Fatalf("insert Task HTTP handoff: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_calls (
			id,
			source_handoff_id,
			practice_id,
			location_id,
			disposition_actor_subject,
			disposition_at,
			disposition_outcome,
			terminal_outcome,
			caller_phone,
			ended_at,
			created_at,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, 'FOLLOW_UP_REQUIRED',
			'FOLLOW_UP_REQUIRED', '+19855550100', $7, $6, $7)
	`, callID, handoffID, authorization.Practice.ID,
		authorization.Locations[0].ID, identity.Subject,
		now.Add(10*time.Second), now.Add(70*time.Second),
	); err != nil {
		t.Fatalf("insert Task HTTP Call: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_call_legs (
			call_id, role, sequence, staff_subject, staff_session_id, state,
			provider_call_control_id, provider_call_leg_id, provider_call_session_id,
			answered_at, bridge_pending_at, bridged_at, ending_at, ended_at,
			created_at, updated_at
		) VALUES
			($1, 'CALLER', 1, NULL, NULL, 'ENDED', 'task-http-control',
				'task-http-leg', 'task-http-session', $2, $2, $2, $3, $3, $2, $3),
			($1, 'STAFF', 1, $4, 'task-http-browser', 'ENDED',
				'task-http-staff-control', 'task-http-staff-leg', 'task-http-session',
				$2, $2, $2, $3, $3, $2, $3)
	`, callID, now.Add(10*time.Second), now.Add(70*time.Second), identity.Subject); err != nil {
		t.Fatalf("insert Task HTTP CallLegs: %v", err)
	}
	workModule := work.New(pool, accessModule, func() time.Time { return now })
	tx, err := pool.BeginTx(context.Background(), pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin Task HTTP fixture transaction: %v", err)
	}
	task, err := workModule.EnsureCallFollowUp(
		context.Background(),
		tx,
		work.EnsureCallFollowUpCommand{
			CallID:     callID,
			PracticeID: authorization.Practice.ID,
			LocationID: authorization.Locations[0].ID,
			Phone:      "+19855550100",
			Reason:     "Verify referral",
			Creator:    authorization.Actor,
		},
	)
	if err != nil {
		_ = tx.Rollback(context.Background())
		t.Fatalf("create Task HTTP fixture: %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit Task HTTP fixture: %v", err)
	}

	handler, err := newPortalHandler(
		t,
		httpapi.Config{
			AllowedOrigins: []string{"http://localhost:3000"},
			AcquireTimeout: 500 * time.Millisecond,
		},
		pool,
		accessModule,
		staticAuthenticator{"task-token": identity},
	)
	if err != nil {
		t.Fatalf("new Task HTTP adapter: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	queryBody, _ := json.Marshal(map[string]any{
		"practiceId": authorization.Practice.ID,
		"search":     "(985) 555-0100",
	})
	queryResponse := request(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/v1/tasks/query",
		"task-token",
		queryBody,
	)
	if queryResponse.StatusCode != http.StatusOK {
		t.Fatalf("Task query status = %d, body = %s", queryResponse.StatusCode, readBody(t, queryResponse))
	}
	var page api.TaskPage
	decode(t, queryResponse, &page)
	if len(page.Items) != 1 || page.Items[0].Id.String() != task.ID {
		t.Fatalf("Task query page = %#v", page)
	}

	renameBody, _ := json.Marshal(map[string]any{
		"expectedVersion": 1,
		"title":           "Confirm referral receipt",
	})
	renamedResponse := request(
		t,
		server.Client(),
		http.MethodPut,
		server.URL+"/v1/tasks/"+task.ID+"/title",
		"task-token",
		renameBody,
	)
	if renamedResponse.StatusCode != http.StatusOK {
		t.Fatalf("Task rename status = %d, body = %s", renamedResponse.StatusCode, readBody(t, renamedResponse))
	}
	var renamed api.Task
	decode(t, renamedResponse, &renamed)
	if renamed.Title != "Confirm referral receipt" || renamed.Version != 2 {
		t.Fatalf("renamed HTTP Task = %#v", renamed)
	}
	staleResponse := request(
		t,
		server.Client(),
		http.MethodPut,
		server.URL+"/v1/tasks/"+task.ID+"/title",
		"task-token",
		renameBody,
	)
	if staleResponse.StatusCode != http.StatusConflict {
		t.Fatalf("stale Task rename status = %d, body = %s", staleResponse.StatusCode, readBody(t, staleResponse))
	}
	_ = staleResponse.Body.Close()

	completeBody, _ := json.Marshal(map[string]any{"expectedVersion": 2})
	completedResponse := request(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/v1/tasks/"+task.ID+"/complete",
		"task-token",
		completeBody,
	)
	if completedResponse.StatusCode != http.StatusOK {
		t.Fatalf("Task completion status = %d, body = %s", completedResponse.StatusCode, readBody(t, completedResponse))
	}
	var completed api.Task
	decode(t, completedResponse, &completed)
	if completed.State != api.TaskStateCOMPLETED || completed.CompletedBy == nil ||
		completed.CompletedBy.Kind != api.TaskActorKindHUMAN ||
		completed.CompletedBy.Email == nil ||
		string(*completed.CompletedBy.Email) != identity.Email {
		t.Fatalf("completed HTTP Task = %#v", completed)
	}

	reopenBody, _ := json.Marshal(map[string]any{"expectedVersion": completed.Version})
	reopenedResponse := request(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/v1/tasks/"+task.ID+"/reopen",
		"task-token",
		reopenBody,
	)
	if reopenedResponse.StatusCode != http.StatusOK {
		t.Fatalf("Task reopen status = %d, body = %s", reopenedResponse.StatusCode, readBody(t, reopenedResponse))
	}
	var reopened api.Task
	decode(t, reopenedResponse, &reopened)
	if reopened.State != api.TaskStateOPEN || reopened.CompletedBy != nil ||
		reopened.Title != renamed.Title {
		t.Fatalf("reopened HTTP Task = %#v", reopened)
	}

	historyResponse := request(
		t,
		server.Client(),
		http.MethodGet,
		server.URL+"/v1/tasks/"+task.ID+"/history",
		"task-token",
		nil,
	)
	if historyResponse.StatusCode != http.StatusOK {
		t.Fatalf("Task history status = %d, body = %s", historyResponse.StatusCode, readBody(t, historyResponse))
	}
	var history api.CallHistoryPage
	decode(t, historyResponse, &history)
	if len(history.Items) != 1 ||
		history.Items[0].Id.String() != callID ||
		!history.Items[0].Originating {
		t.Fatalf("Task Call history = %#v", history)
	}
}

func TestPortalAPIBoundsPoolAcquisitionAndReturnsRetryableUnavailable(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	provisioned, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "slice-1-pool-test",
		Practices: []access.PracticeProvision{{
			Key:       "pool-practice",
			Name:      "Pool Fixture Practice",
			Locations: []access.LocationProvision{{Key: "fixture-1", Name: "Fixture 1"}},
			Invitations: []access.InvitationProvision{{
				Key:           "pool-member",
				Email:         "member@pool.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
				ExpiresAt:     now.Add(time.Hour),
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision pool fixture: %v", err)
	}
	identity := access.Identity{
		Subject:       "pool-member-subject",
		Email:         "member@pool.test",
		EmailVerified: true,
	}
	if _, err := accessModule.AcceptInvitation(
		context.Background(),
		identity,
		provisioned.Invitations[0].Token,
	); err != nil {
		t.Fatalf("accept pool fixture: %v", err)
	}
	handler, err := newPortalHandler(t, httpapi.Config{
		AllowedOrigins: []string{"http://localhost:3000"},
		AcquireTimeout: 75 * time.Millisecond,
	}, pool, accessModule, staticAuthenticator{"member-token": identity})
	if err != nil {
		t.Fatalf("new bounded HTTP adapter: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	held := make([]*pgxpool.Conn, 0, pool.Config().MaxConns)
	for pool.Stat().AcquiredConns() < pool.Config().MaxConns {
		connection, err := pool.Acquire(context.Background())
		if err != nil {
			t.Fatalf("saturate test pool: %v", err)
		}
		held = append(held, connection)
	}
	defer func() {
		for _, connection := range held {
			connection.Release()
		}
	}()

	started := time.Now()
	response := request(
		t,
		server.Client(),
		http.MethodGet,
		server.URL+"/v1/access",
		"member-token",
		nil,
	)
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("bounded request took %s", elapsed)
	}
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("saturated pool status = %d, body = %s", response.StatusCode, readBody(t, response))
	}
	var envelope api.ErrorEnvelope
	decode(t, response, &envelope)
	if envelope.Error.Code != "UNAVAILABLE" || !envelope.Error.Retryable {
		t.Fatalf("saturated pool error = %#v", envelope)
	}
}

func TestReadinessReportsRetryableUnavailableWhenPostgresCannotConnect(t *testing.T) {
	databaseURL := strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Host = "127.0.0.1:1"
	pool, err := pgxpool.New(context.Background(), parsed.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	calling := humancalling.New(
		pool,
		nil,
		nil,
		humancalling.Config{},
		nil,
	)
	handler, err := httpapi.NewProviderIngress(httpapi.Config{
		AcquireTimeout: 75 * time.Millisecond,
	}, pool, calling)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	response := request(
		t,
		server.Client(),
		http.MethodGet,
		server.URL+"/health/ready",
		"",
		nil,
	)
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("unavailable readiness status = %d", response.StatusCode)
	}
	var envelope api.ErrorEnvelope
	decode(t, response, &envelope)
	if envelope.Error.Code != "UNAVAILABLE" || !envelope.Error.Retryable {
		t.Fatalf("unavailable readiness error = %#v", envelope)
	}
}

func TestProviderIngressVerifiesAndCommitsTheExactSignedBody(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	var metrics bytes.Buffer
	observer := observability.NewLogger(
		observability.RuntimeProviderIngress,
		"provider-ingress-test",
		slog.New(slog.NewJSONHandler(&metrics, nil)),
	)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate webhook key: %v", err)
	}
	nextPublicKey, nextPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate next webhook key: %v", err)
	}
	calling := humancalling.New(
		pool,
		nil,
		nil,
		humancalling.Config{
			WebhookPublicKeys: []ed25519.PublicKey{publicKey, nextPublicKey},
			WebhookTolerance:  5 * time.Minute,
		},
		func() time.Time { return now },
	)
	handler, err := httpapi.NewProviderIngress(httpapi.Config{
		AcquireTimeout: 500 * time.Millisecond,
		Observer:       observer,
	}, pool, calling)
	if err != nil {
		t.Fatalf("new provider-ingress HTTP adapter: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	raw := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"call.initiated","id":"http-webhook-event","occurred_at":"%s","payload":{"call_control_id":"caller-control","call_leg_id":"caller-leg","call_session_id":"caller-session","to":"sip:opaque@synthetic.sip.telnyx.com"}}}`,
		now.Format(time.RFC3339Nano),
	))
	timestamp := strconv.FormatInt(now.Unix(), 10)
	signature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		privateKey,
		append([]byte(timestamp+"|"), raw...),
	))
	nextSignature := base64.StdEncoding.EncodeToString(ed25519.Sign(
		nextPrivateKey,
		append([]byte(timestamp+"|"), raw...),
	))
	send := func(signature string) *http.Response {
		t.Helper()
		request, err := http.NewRequest(
			http.MethodPost,
			server.URL+"/v1/provider/telnyx/webhooks",
			bytes.NewReader(raw),
		)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("telnyx-timestamp", timestamp)
		request.Header.Set("telnyx-signature-ed25519", signature)
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	for attempt, acceptedSignature := range []string{signature, signature, nextSignature} {
		response := send(acceptedSignature)
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf(
				"signed webhook attempt %d status = %d, body = %s",
				attempt+1,
				response.StatusCode,
				readBody(t, response),
			)
		}
		_ = response.Body.Close()
	}
	invalid := send(base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize)))
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf(
			"invalid webhook status = %d, body = %s",
			invalid.StatusCode,
			readBody(t, invalid),
		)
	}
	_ = invalid.Body.Close()

	for _, outcome := range []string{"accepted", "duplicate", "invalid"} {
		if !strings.Contains(
			metrics.String(),
			`"outcome":"`+outcome+`"`,
		) {
			t.Fatalf("%s webhook metric omitted: %s", outcome, metrics.String())
		}
	}
}

func TestStaffTaskHTTPInterfaceAcceptsCurrentAbitaToolContract(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	provisioned, err := accessModule.Provision(
		context.Background(),
		access.Provisioning{
			Environment: "test",
			RequestedBy: "slice-4-http-test",
			Practices: []access.PracticeProvision{{
				Key:  "calling-practice",
				Name: "Calling Practice",
				Locations: []access.LocationProvision{{
					Key:             "calling-location",
					Name:            "Calling Location",
					AbitaOfficeKeys: []string{"spring-hill"},
				}},
				Invitations: []access.InvitationProvision{{
					Key:           "calling-staff",
					Email:         "staff@calling.test",
					Role:          access.RoleStaff,
					LocationScope: access.LocationScopeAll,
					ExpiresAt:     now.Add(time.Hour),
				}},
			}},
		},
	)
	if err != nil {
		t.Fatalf("provision staff Task HTTP fixture: %v", err)
	}
	staffIdentity := access.Identity{
		Subject:       "calling-staff-subject",
		Email:         "staff@calling.test",
		EmailVerified: true,
	}
	if _, err := accessModule.AcceptInvitation(
		context.Background(),
		staffIdentity,
		provisioned.Invitations[0].Token,
	); err != nil {
		t.Fatalf("accept staff Task HTTP fixture: %v", err)
	}
	var practiceID string
	if err := pool.QueryRow(context.Background(), `
		SELECT id::text
		FROM access_practices
		WHERE provisioning_key = 'calling-practice'
	`).Scan(&practiceID); err != nil {
		t.Fatalf("load staff Task HTTP Practice: %v", err)
	}
	serviceAuthenticator, err := access.NewServiceAuthenticator(
		"abita-token",
		access.ServiceIdentity{
			Subject:       "abita-synthetic",
			PracticeID:    practiceID,
			LocationScope: access.LocationScopeAll,
			Capabilities: []access.ServiceCapability{
				access.ServiceCapabilityCreateTask,
			},
		},
	)
	if err != nil {
		t.Fatalf("new staff Task service authenticator: %v", err)
	}
	handler, err := httpapi.NewPortal(
		httpapi.Config{
			AllowedOrigins: []string{"http://localhost:3000"},
			AcquireTimeout: 500 * time.Millisecond,
		},
		pool,
		httpapi.PortalDependencies{
			Access:        accessModule,
			Authenticator: staticAuthenticator{"staff-token": staffIdentity},
			Calling: humancalling.New(
				pool,
				accessModule,
				httpCallingProvider{},
				humancalling.Config{},
				nil,
			),
			Interactions:         interaction.New(pool, accessModule, func() time.Time { return now }),
			Work:                 work.New(pool, accessModule, func() time.Time { return now }),
			ServiceAuthenticator: serviceAuthenticator,
		},
	)
	if err != nil {
		t.Fatalf("new staff Task HTTP adapter: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	payload := map[string]any{
		"callId":         "room/+@opaque",
		"callerPhone":    "+17275551212",
		"category":       "documentation",
		"idempotencyKey": "staff_task_3f94a1",
		"message":        "Caller asked the office to send records to a specialist.",
		"officeKey":      "spring-hill",
		"officePhone":    "+17275919997",
		"patient": map[string]any{
			"dob":  "01/01/1980",
			"id":   "patient-1",
			"name": "Jane Doe",
		},
		"source":  "agent",
		"summary": "Caller needs records sent.",
		"urgency": "normal",
	}
	body, _ := json.Marshal(payload)
	unauthenticated := request(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/v1/tasks",
		"wrong-token",
		body,
	)
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		t.Fatalf(
			"unauthenticated staff Task status = %d",
			unauthenticated.StatusCode,
		)
	}
	_ = unauthenticated.Body.Close()

	missingOfficePhone := make(map[string]any, len(payload))
	for key, value := range payload {
		missingOfficePhone[key] = value
	}
	delete(missingOfficePhone, "officePhone")
	invalidBody, _ := json.Marshal(missingOfficePhone)
	invalid := request(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/v1/tasks",
		"abita-token",
		invalidBody,
	)
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf(
			"invalid staff Task status = %d, body = %s",
			invalid.StatusCode,
			readBody(t, invalid),
		)
	}
	_ = invalid.Body.Close()

	withTranscript := make(map[string]any, len(payload)+1)
	for key, value := range payload {
		withTranscript[key] = value
	}
	withTranscript["transcript"] = "protected content"
	protectedBody, _ := json.Marshal(withTranscript)
	protected := request(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/v1/tasks",
		"abita-token",
		protectedBody,
	)
	if protected.StatusCode != http.StatusBadRequest {
		t.Fatalf(
			"protected staff Task field status = %d, body = %s",
			protected.StatusCode,
			readBody(t, protected),
		)
	}
	_ = protected.Body.Close()

	handoffBody, _ := json.Marshal(map[string]any{
		"practiceId":     practiceID,
		"locationId":     uuid.NewString(),
		"sourceCallId":   "capability-check",
		"idempotencyKey": "capability-check",
		"contact": map[string]any{
			"phone": "+17275551212",
		},
	})
	handoff := request(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/v1/handoffs",
		"abita-token",
		handoffBody,
	)
	if handoff.StatusCode != http.StatusForbidden {
		t.Fatalf(
			"Task-only service handoff status = %d, body = %s",
			handoff.StatusCode,
			readBody(t, handoff),
		)
	}
	_ = handoff.Body.Close()

	created := request(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/v1/tasks",
		"abita-token",
		body,
	)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf(
			"create staff Task status = %d, body = %s",
			created.StatusCode,
			readBody(t, created),
		)
	}
	var receipt struct {
		Status   string `json:"status"`
		TaskID   string `json:"taskId"`
		Category string `json:"category"`
		Urgency  string `json:"urgency"`
	}
	decode(t, created, &receipt)
	if receipt.Status != "created" ||
		receipt.TaskID == "" ||
		receipt.Category != "documentation" ||
		receipt.Urgency != "normal" {
		t.Fatalf("created staff Task receipt = %#v", receipt)
	}

	duplicate := request(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/v1/tasks",
		"abita-token",
		body,
	)
	if duplicate.StatusCode != http.StatusOK {
		t.Fatalf(
			"duplicate staff Task status = %d, body = %s",
			duplicate.StatusCode,
			readBody(t, duplicate),
		)
	}
	var duplicateReceipt struct {
		Status string `json:"status"`
		TaskID string `json:"taskId"`
	}
	decode(t, duplicate, &duplicateReceipt)
	if duplicateReceipt.Status != "duplicate" ||
		duplicateReceipt.TaskID != receipt.TaskID {
		t.Fatalf("duplicate staff Task receipt = %#v", duplicateReceipt)
	}

	queryBody, _ := json.Marshal(map[string]any{
		"practiceId": practiceID,
		"ordering":   "priority",
	})
	query := request(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/v1/tasks/query",
		"staff-token",
		queryBody,
	)
	if query.StatusCode != http.StatusOK {
		t.Fatalf(
			"query staff Task status = %d, body = %s",
			query.StatusCode,
			readBody(t, query),
		)
	}
	var page api.TaskPage
	decode(t, query, &page)
	if len(page.Items) != 1 {
		t.Fatalf("queried staff Tasks = %#v", page.Items)
	}
	projected := page.Items[0]
	if projected.Id.String() != receipt.TaskID ||
		projected.Origin != api.ABITAAI ||
		projected.Urgency != api.Normal ||
		projected.Category == nil ||
		*projected.Category != api.Documentation ||
		projected.CallerName == nil ||
		*projected.CallerName != "Jane Doe" ||
		projected.SourceCallId == nil ||
		*projected.SourceCallId != "room/+@opaque" ||
		projected.SourceMessage == nil ||
		*projected.SourceMessage != payload["message"] ||
		projected.CallId != nil ||
		projected.CreatedBy.Kind != api.TaskActorKindSERVICE ||
		projected.CreatedBy.Email != nil {
		t.Fatalf("projected staff Task = %#v", projected)
	}

	payload["message"] = "Changed request content."
	changedBody, _ := json.Marshal(payload)
	conflict := request(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/v1/tasks",
		"abita-token",
		changedBody,
	)
	if conflict.StatusCode != http.StatusConflict {
		t.Fatalf(
			"conflicting staff Task status = %d, body = %s",
			conflict.StatusCode,
			readBody(t, conflict),
		)
	}
	_ = conflict.Body.Close()

	var stored string
	if err := pool.QueryRow(context.Background(), `
		SELECT to_jsonb(task)::text
		FROM work_tasks task
		WHERE id = $1
	`, receipt.TaskID).Scan(&stored); err != nil {
		t.Fatalf("read stored staff Task: %v", err)
	}
	if strings.Contains(stored, "patient-1") ||
		strings.Contains(stored, "01/01/1980") {
		t.Fatalf("stored staff Task retained excluded patient data: %s", stored)
	}
}

func TestCallingHTTPInterfacePreservesServiceAndCurrentUserAuthority(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	var metrics bytes.Buffer
	observer := observability.NewLogger(
		observability.RuntimePortalAPI,
		"portal-api-test",
		slog.New(slog.NewJSONHandler(&metrics, nil)),
	)
	accessModule := access.New(pool, func() time.Time { return now })
	provisioned, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "slice-2-http-test",
		Practices: []access.PracticeProvision{{
			Key:       "calling-practice",
			Name:      "Calling Practice",
			Locations: []access.LocationProvision{{Key: "calling-location", Name: "Calling Location"}},
			Invitations: []access.InvitationProvision{{
				Key:           "calling-staff",
				Email:         "staff@calling.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
				ExpiresAt:     now.Add(time.Hour),
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision calling HTTP fixture: %v", err)
	}
	identity := access.Identity{
		Subject:       "calling-staff-subject",
		Email:         "staff@calling.test",
		EmailVerified: true,
	}
	authorization, err := accessModule.AcceptInvitation(
		context.Background(),
		identity,
		provisioned.Invitations[0].Token,
	)
	if err != nil {
		t.Fatalf("accept calling HTTP fixture: %v", err)
	}
	calling := humancalling.New(
		pool,
		accessModule,
		httpCallingProvider{},
		humancalling.Config{
			HandoffSIPDomain:   "synthetic.sip.telnyx.com",
			RingWindowDuration: 20 * time.Second,
			HandoffTokenKey:    []byte("0123456789abcdef0123456789abcdef"),
			CallControlID:      "http-call-control-connection",
		},
		func() time.Time { return now },
	)
	if err := calling.ReconcileCredentials(context.Background()); err != nil {
		t.Fatalf("reconcile HTTP calling credentials: %v", err)
	}
	if processed, err := calling.ProcessNextCommand(context.Background()); err != nil || !processed {
		t.Fatalf("create HTTP calling credential: processed=%t err=%v", processed, err)
	}
	serviceAuthenticator, err := access.NewServiceAuthenticator(
		"abita-token",
		access.ServiceIdentity{
			Subject:       "abita-synthetic",
			PracticeID:    authorization.Practice.ID,
			LocationScope: access.LocationScopeAll,
			Capabilities: []access.ServiceCapability{
				access.ServiceCapabilityHumanHandoff,
			},
		},
	)
	if err != nil {
		t.Fatalf("new service authenticator: %v", err)
	}
	handler, err := httpapi.NewPortal(
		httpapi.Config{
			AllowedOrigins: []string{"http://localhost:3000"},
			AcquireTimeout: 500 * time.Millisecond,
			Observer:       observer,
		},
		pool,
		httpapi.PortalDependencies{
			Access:               accessModule,
			Authenticator:        staticAuthenticator{"calling-token": identity},
			Calling:              calling,
			Interactions:         interaction.New(pool, accessModule, nil),
			Work:                 work.New(pool, accessModule, nil),
			ServiceAuthenticator: serviceAuthenticator,
		},
	)
	if err != nil {
		t.Fatalf("new calling HTTP adapter: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	handoffBody, _ := json.Marshal(map[string]any{
		"practiceId":     authorization.Practice.ID,
		"locationId":     authorization.Locations[0].ID,
		"sourceCallId":   "http-source-call",
		"idempotencyKey": "http-idempotency",
		"contact": map[string]any{
			"phone":          "+15555550100",
			"displayName":    "HTTP Caller",
			"nameSource":     "Abita",
			"transferReason": "HTTP interface proof",
			"reasonSource":   "Abita AI",
		},
	})
	unauthenticated := request(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/v1/handoffs",
		"wrong-service-token",
		handoffBody,
	)
	if unauthenticated.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated handoff status = %d", unauthenticated.StatusCode)
	}
	_ = unauthenticated.Body.Close()
	created := request(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/v1/handoffs",
		"abita-token",
		handoffBody,
	)
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("create handoff status = %d, body = %s", created.StatusCode, readBody(t, created))
	}
	var handoff struct {
		ID             string    `json:"id"`
		SIPDestination string    `json:"sipDestination"`
		ExpiresAt      time.Time `json:"expiresAt"`
	}
	decode(t, created, &handoff)
	if handoff.SIPDestination != "sip:acuity-handoff@synthetic.sip.telnyx.com" {
		t.Fatalf("handoff response = %#v", handoff)
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "http-inbound-event",
		Type:          humancalling.FactCallInitiated,
		OccurredAt:    now,
		ConnectionID:  "http-call-control-connection",
		CallControlID: "http-caller-control",
		CallLegID:     "http-caller-leg",
		CallSessionID: "http-call-session",
		From:          "+15555550100",
		To:            "+14843989071",
	}); err != nil {
		t.Fatalf("admit HTTP caller: %v", err)
	}

	leaseBody, _ := json.Marshal(map[string]any{
		"sessionId": "http-browser",
		"takeover":  false,
	})
	lease := request(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/v1/calling/softphone/lease",
		"calling-token",
		leaseBody,
	)
	if lease.StatusCode != http.StatusOK {
		t.Fatalf("lease status = %d, body = %s", lease.StatusCode, readBody(t, lease))
	}
	_ = lease.Body.Close()
	readinessBody, _ := json.Marshal(map[string]any{
		"sessionId":       "http-browser",
		"registered":      true,
		"microphoneReady": true,
		"audioReady":      true,
		"sessionHealthy":  true,
		"available":       true,
	})
	readiness := request(
		t,
		server.Client(),
		http.MethodPut,
		server.URL+"/v1/calling/readiness",
		"calling-token",
		readinessBody,
	)
	if readiness.StatusCode != http.StatusOK {
		t.Fatalf("readiness status = %d, body = %s", readiness.StatusCode, readBody(t, readiness))
	}
	_ = readiness.Body.Close()
	for {
		processed, err := calling.ProcessNextCommand(context.Background())
		if err != nil {
			t.Fatalf("process caller admission command: %v", err)
		}
		if !processed {
			break
		}
	}
	if err := calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "http-inbound-answered",
		Type:          humancalling.FactCallAnswered,
		OccurredAt:    now.Add(time.Second),
		CallControlID: "http-caller-control",
		CallLegID:     "http-caller-leg",
		CallSessionID: "http-call-session",
	}); err != nil {
		t.Fatalf("fan out HTTP caller: %v", err)
	}
	stateResponse := request(
		t,
		server.Client(),
		http.MethodGet,
		server.URL+"/v1/calling/state",
		"calling-token",
		nil,
	)
	if stateResponse.StatusCode != http.StatusOK {
		t.Fatalf("calling state status = %d, body = %s", stateResponse.StatusCode, readBody(t, stateResponse))
	}
	var state struct {
		Ringing []struct {
			CallID    string `json:"callId"`
			CallLegID string `json:"callLegId"`
		} `json:"ringing"`
	}
	etag := stateResponse.Header.Get("ETag")
	decode(t, stateResponse, &state)
	if len(state.Ringing) != 1 || state.Ringing[0].CallID == "" ||
		state.Ringing[0].CallLegID == "" || etag == "" {
		t.Fatalf("HTTP Calling state = %#v, ETag = %q", state, etag)
	}
}

func TestOperatorCanRequeueTimelineReceiptDirectly(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 29, 19, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	operator := access.Identity{
		Subject:       "http-recovery-operator",
		Email:         "http-recovery@acuity.test",
		EmailVerified: true,
	}
	if _, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment:       "test",
		RequestedBy:       "http-recovery-test",
		PlatformOperators: []string{operator.Email},
		Practices: []access.PracticeProvision{{
			Key:       "http-recovery-practice",
			Name:      "HTTP Recovery Practice",
			Locations: []access.LocationProvision{{Key: "office", Name: "Office"}},
		}},
	}); err != nil {
		t.Fatalf("provision HTTP recovery fixture: %v", err)
	}
	discovery, err := accessModule.DiscoverActor(context.Background(), operator)
	if err != nil || len(discovery.Practices) != 1 ||
		len(discovery.Practices[0].Locations) != 1 {
		t.Fatalf("discover HTTP recovery scope = %#v, err = %v", discovery, err)
	}
	practiceID := discovery.Practices[0].ID
	locationID := discovery.Practices[0].Locations[0].ID
	const callID = "00000000-0000-0000-0000-000000000701"
	const handoffID = "00000000-0000-0000-0000-000000000702"
	const eventID = "http-quarantined-receipt"
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_handoffs (
			id, service_subject, practice_id, location_id, source_call_id,
			idempotency_key, input_fingerprint, expires_at
		)
		VALUES ($1, 'http-recovery-service', $2, $3, 'source', 'idempotency',
			'\x01', $4)
	`, handoffID, practiceID, locationID, now.Add(time.Hour)); err != nil {
		t.Fatalf("seed HTTP recovery handoff: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_calls (
			id, source_handoff_id, practice_id, location_id, caller_phone
		)
		VALUES ($1, $2, $3, $4, '+15555550100')
	`, callID, handoffID, practiceID, locationID); err != nil {
		t.Fatalf("seed HTTP recovery Call: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_call_legs (
			call_id, role, sequence, state, provider_call_control_id,
			provider_call_leg_id, provider_call_session_id
		) VALUES ($1, 'CALLER', 1, 'RINGING', 'http-recovery-control',
			'http-recovery-leg', 'http-recovery-session')
	`, callID); err != nil {
		t.Fatalf("seed HTTP recovery Caller CallLeg: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_provider_receipts (
			event_id, call_id, event_type, occurred_at, received_at,
			signature_timestamp, raw_body, state, projection_attempts,
			last_attempt_at, next_attempt_at, projected_at, quarantined_at
		)
		VALUES ($1, $2, 'call.answered', $3, $3, 1, '{}'::bytea,
			'QUARANTINED', 10, $3, $3, $3, $3)
	`, eventID, callID, now); err != nil {
		t.Fatalf("seed HTTP recovery receipt: %v", err)
	}
	probe := humancalling.New(pool, accessModule, httpCallingProvider{}, humancalling.Config{}, func() time.Time { return now })
	if _, err := probe.ReadOperatorTimeline(context.Background(), operator, callID); err != nil {
		t.Fatalf("read direct operator timeline: %v", err)
	}

	handler, err := newPortalHandler(t, httpapi.Config{
		AllowedOrigins: []string{"http://localhost:3000"},
		AcquireTimeout: 500 * time.Millisecond,
	}, pool, accessModule, staticAuthenticator{"operator-token": operator})
	if err != nil {
		t.Fatalf("new HTTP recovery portal: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	timelineResponse := request(
		t,
		server.Client(),
		http.MethodGet,
		server.URL+"/v1/operator/calls/"+callID+"/timeline",
		"operator-token",
		nil,
	)
	if timelineResponse.StatusCode != http.StatusOK {
		t.Fatalf(
			"operator timeline status = %d, body = %s",
			timelineResponse.StatusCode,
			readBody(t, timelineResponse),
		)
	}
	var timeline api.OperatorCallingTimeline
	decode(t, timelineResponse, &timeline)
	recoveryReference := ""
	for _, entry := range timeline.Entries {
		if entry.RecoveryReference != nil {
			recoveryReference = *entry.RecoveryReference
		}
	}
	if recoveryReference == "" || strings.Contains(recoveryReference, eventID) {
		t.Fatalf("HTTP recovery reference = %q", recoveryReference)
	}

	body, _ := json.Marshal(api.ProviderReceiptRecoveryRequest{})
	requeued := request(
		t,
		server.Client(),
		http.MethodPost,
		fmt.Sprintf(
			"%s/v1/operator/practices/%s/provider-receipts/%s/requeue",
			server.URL,
			practiceID,
			recoveryReference,
		),
		"operator-token",
		body,
	)
	if requeued.StatusCode != http.StatusOK {
		t.Fatalf(
			"operator receipt requeue status = %d, body = %s",
			requeued.StatusCode,
			readBody(t, requeued),
		)
	}
	var result api.ProviderReceiptRecovery
	decode(t, requeued, &result)
	if result.ReceiptReference != recoveryReference ||
		result.State != api.ProviderReceiptRecoveryStatePENDING {
		t.Fatalf("operator receipt requeue = %#v", result)
	}
	var state humancalling.ReceiptState
	if err := pool.QueryRow(context.Background(), `
		SELECT state
		FROM human_calling_provider_receipts
		WHERE event_id = $1
	`, eventID).Scan(&state); err != nil {
		t.Fatalf("read HTTP recovery result: %v", err)
	}
	if state != humancalling.ReceiptPending {
		t.Fatalf("HTTP recovery receipt state = %q", state)
	}
}

type staticAuthenticator map[string]access.Identity

func (adapter staticAuthenticator) Authenticate(_ context.Context, token string) (access.Identity, error) {
	identity, ok := adapter[token]
	if !ok {
		return access.Identity{}, authn.ErrInvalidCredential
	}
	return identity, nil
}

type httpCallingProvider struct{}

func (httpCallingProvider) Execute(
	_ context.Context,
	command humancalling.ProviderCommand,
) (humancalling.ProviderResult, error) {
	if command.Action == humancalling.CommandCreateCredential {
		return humancalling.ProviderResult{
			CredentialID: "http-credential",
			SIPUsername:  "http-sip-user",
		}, nil
	}
	return humancalling.ProviderResult{}, nil
}

type httpVoicemailAudio struct {
	calls         int
	rangeHeader   string
	contentLength string
	contentRange  *string
	failStream    bool
	err           error
}

func (audio *httpVoicemailAudio) OpenVoicemailRecording(
	_ context.Context,
	_ string,
	rangeHeader string,
) (humancalling.PlaybackContent, error) {
	audio.calls++
	audio.rangeHeader = rangeHeader
	if audio.err != nil {
		return humancalling.PlaybackContent{}, audio.err
	}
	contentRange := "bytes 0-3/13"
	if audio.contentRange != nil {
		contentRange = *audio.contentRange
	}
	contentLength := audio.contentLength
	if contentLength == "" {
		contentLength = "4"
	}
	var body io.ReadCloser = io.NopCloser(strings.NewReader("synt"))
	if audio.failStream {
		body = &failingVoicemailBody{}
	}
	return humancalling.PlaybackContent{
		StatusCode:    http.StatusPartialContent,
		ContentType:   "audio/mpeg",
		ContentLength: contentLength,
		ContentRange:  contentRange,
		Body:          body,
	}, nil
}

type failingVoicemailBody struct {
	read bool
}

func (body *failingVoicemailBody) Read(target []byte) (int, error) {
	if !body.read {
		body.read = true
		return copy(target, "synt"), nil
	}
	return 0, errors.New("synthetic stream failure")
}

func (*failingVoicemailBody) Close() error { return nil }

func newPortalHandler(
	t *testing.T,
	config httpapi.Config,
	pool *pgxpool.Pool,
	accessModule *access.Module,
	authenticator httpapi.IdentityAuthenticator,
) (http.Handler, error) {
	return newPortalHandlerWithCalling(
		t,
		config,
		pool,
		accessModule,
		authenticator,
		humancalling.New(pool, accessModule, httpCallingProvider{}, humancalling.Config{}, nil),
	)
}

func newPortalHandlerWithCalling(
	t *testing.T,
	config httpapi.Config,
	pool *pgxpool.Pool,
	accessModule *access.Module,
	authenticator httpapi.IdentityAuthenticator,
	calling *humancalling.Module,
) (http.Handler, error) {
	t.Helper()
	serviceAuthenticator, err := access.NewServiceAuthenticator(
		"unused-service-token",
		access.ServiceIdentity{
			Subject:       "unused-service",
			PracticeID:    "00000000-0000-0000-0000-000000000001",
			LocationScope: access.LocationScopeAll,
			Capabilities: []access.ServiceCapability{
				access.ServiceCapabilityHumanHandoff,
				access.ServiceCapabilityCreateTask,
			},
		},
	)
	if err != nil {
		t.Fatalf("new test service authenticator: %v", err)
	}
	return httpapi.NewPortal(config, pool, httpapi.PortalDependencies{
		Access:               accessModule,
		Authenticator:        authenticator,
		Calling:              calling,
		Interactions:         interaction.New(pool, accessModule, nil),
		Work:                 work.New(pool, accessModule, nil),
		ServiceAuthenticator: serviceAuthenticator,
	})
}

func request(
	t *testing.T,
	client *http.Client,
	method string,
	target string,
	token string,
	body []byte,
) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, target, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func decode(t *testing.T, response *http.Response, target any) {
	t.Helper()
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatal(err)
	}
}

func readBody(t *testing.T, response *http.Response) string {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
