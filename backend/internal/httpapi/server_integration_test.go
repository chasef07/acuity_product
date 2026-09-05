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
	"sync"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/authn"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/chasef07/acuity_product/backend/internal/observability"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestGeneratedHTTPSInterfaceLoadsOnlyTheAuthorizedEmptyWorkspace(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	_, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment:       "test",
		RequestedBy:       "access-http-test",
		PlatformOperators: []string{"founder@acuity.test"},
		Practices: []access.PracticeProvision{{
			Key:  "abita-eye-group",
			Name: "Abita Eye Group",
			Locations: []access.LocationProvision{
				{Key: "fixture-location-1", Name: "Fixture Location 1"},
				{Key: "fixture-location-2", Name: "Fixture Location 2"},
			},
			AccessGrants: []access.AccessGrantProvision{{
				Key:                  "selected-staff",
				Email:                "selected@abita.test",
				Role:                 access.RoleStaff,
				LocationScope:        access.LocationScopeSelected,
				SelectedLocationKeys: []string{"fixture-location-1"},
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

	eligibleBody, _ := json.Marshal(api.SignUpEligibilityRequest{
		Email: "selected@abita.test",
	})
	eligible := request(t, server.Client(), http.MethodPost,
		server.URL+"/v1/access/sign-up-eligibility",
		"", eligibleBody,
	)
	if eligible.StatusCode != http.StatusOK {
		t.Fatalf("eligible Google sign-up status = %d, body = %s", eligible.StatusCode, readBody(t, eligible))
	}
	_ = eligible.Body.Close()
	unknownBody, _ := json.Marshal(api.SignUpEligibilityRequest{
		Email: "somebody-else@abita.test",
	})
	unknown := request(t, server.Client(), http.MethodPost,
		server.URL+"/v1/access/sign-up-eligibility",
		"", unknownBody,
	)
	if unknown.StatusCode != http.StatusForbidden {
		t.Fatalf("unknown Google sign-up status = %d, body = %s", unknown.StatusCode, readBody(t, unknown))
	}
	_ = unknown.Body.Close()

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
		accessDiscovery.Practices[0].Membership == nil ||
		len(accessDiscovery.Practices[0].Locations) != 1 {
		t.Fatalf("discovery = %#v", accessDiscovery)
	}

	workspaceURL := server.URL + "/v1/workspace?" + url.Values{
		"practiceId": {accessDiscovery.Practices[0].Id.String()},
		"locationId": {accessDiscovery.Practices[0].Locations[0].Id.String()},
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
		"practiceId": {accessDiscovery.Practices[0].Id.String()},
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
	var metrics synchronizedBuffer
	observer := observability.NewLogger(
		observability.RuntimePortalAPI,
		"voicemail-playback-test",
		slog.New(slog.NewJSONHandler(&metrics, nil)),
	)
	accessModule := access.New(pool, func() time.Time { return now })
	_, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "voicemail-http-test",
		Practices: []access.PracticeProvision{{
			Key:  "voicemail-http-practice",
			Name: "Voicemail HTTP Practice",
			Locations: []access.LocationProvision{
				{Key: "voicemail-http-location", Name: "Voicemail HTTP Location"},
				{Key: "voicemail-hidden-location", Name: "Voicemail Hidden Location"},
			},
			AccessGrants: []access.AccessGrantProvision{
				{
					Key:           "voicemail-http-staff",
					Email:         "voicemail-http@synthetic.test",
					Role:          access.RoleStaff,
					LocationScope: access.LocationScopeAll,
				},
				{
					Key:                  "voicemail-hidden-staff",
					Email:                "voicemail-hidden@synthetic.test",
					Role:                 access.RoleStaff,
					LocationScope:        access.LocationScopeSelected,
					SelectedLocationKeys: []string{"voicemail-hidden-location"},
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
	authorization := testaccess.Activate(t, accessModule, identity)
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
	testaccess.Activate(t, accessModule, hiddenIdentity)
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
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_call_recordings (
			call_id, practice_id, location_id, audio_state,
			provider_recording_id, retention_days, recording_started_at,
			recording_ended_at, content_expires_at, duration_millis,
			created_at, updated_at
		) VALUES ($1, $2, $3, 'READY', 'call-http-recording', 90,
			$4, $5, $6, 12000, $4, $5)
	`, callID, authorization.Practice.ID, voicemailLocationID,
		now, now.Add(12*time.Second), now.Add(90*24*time.Hour)); err != nil {
		t.Fatalf("insert call recording HTTP evidence: %v", err)
	}

	audio := &httpVoicemailAudio{}
	calling := humancalling.New(
		pool,
		accessModule,
		httpCallingProvider{},
		humancalling.Config{
			PlaybackSigningKey:     []byte("abcdef0123456789abcdef0123456789"),
			RecordingAudioProvider: audio,
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
	deniedCapabilityResponse := request(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/v1/calling/calls/"+callID+"/voicemail-playback",
		"voicemail-hidden-token",
		nil,
	)
	_ = deniedCapabilityResponse.Body.Close()
	if deniedCapabilityResponse.StatusCode != http.StatusForbidden || audio.calls != 0 {
		t.Fatalf(
			"cross-Location capability = status:%d provider-calls:%d",
			deniedCapabilityResponse.StatusCode,
			audio.calls,
		)
	}
	deniedRequest, err := http.NewRequest(
		http.MethodGet,
		server.URL+"/v1/calling/voicemail-playback/"+url.PathEscape(capability.Token+"invalid"),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	deniedResponse, err := server.Client().Do(deniedRequest)
	if err != nil {
		t.Fatalf("request cross-Location voicemail playback: %v", err)
	}
	_ = deniedResponse.Body.Close()
	deniedAudio := audio.snapshot()
	if deniedResponse.StatusCode != http.StatusForbidden || deniedAudio.calls != 0 {
		t.Fatalf(
			"invalid capability playback = status:%d provider-calls:%d",
			deniedResponse.StatusCode,
			deniedAudio.calls,
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
	playbackAudio := audio.snapshot()
	if playbackResponse.StatusCode != http.StatusPartialContent ||
		playbackResponse.Header.Get("Accept-Ranges") != "bytes" ||
		playbackResponse.Header.Get("Content-Range") != "bytes 0-3/13" ||
		playbackResponse.Header.Get("Content-Length") != "4" ||
		playbackResponse.Header.Get("Content-Type") != "audio/mpeg" ||
		string(body) != "synt" ||
		playbackAudio.rangeHeader != "bytes=0-3" {
		t.Fatalf("voicemail playback response = status:%d headers:%v body:%q fixture:%#v",
			playbackResponse.StatusCode, playbackResponse.Header, body, playbackAudio)
	}
	recordingCapabilityResponse := request(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/v1/calling/calls/"+callID+"/recording-playback",
		"voicemail-http-token",
		nil,
	)
	if recordingCapabilityResponse.StatusCode != http.StatusOK {
		t.Fatalf("recording capability status = %d, body = %s",
			recordingCapabilityResponse.StatusCode, readBody(t, recordingCapabilityResponse))
	}
	var recordingCapability api.RecordingPlaybackCapability
	decode(t, recordingCapabilityResponse, &recordingCapability)
	contentExpiresAt := time.Now().Add(500 * time.Millisecond)
	if _, err := pool.Exec(context.Background(), `
		UPDATE human_calling_call_recordings
		SET content_expires_at = $2
		WHERE call_id = $1
	`, callID, contentExpiresAt); err != nil {
		t.Fatalf("set imminent recording expiry: %v", err)
	}
	providerContextDone := make(chan time.Time, 1)
	audio.update(func(audio *httpVoicemailAudio) {
		audio.streamUntilContextDone = true
		audio.contextDone = providerContextDone
	})
	recordingRequest, err := http.NewRequest(
		http.MethodGet,
		server.URL+"/v1/calling/recording-playback/"+url.PathEscape(recordingCapability.Token),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	recordingClient := *server.Client()
	recordingClient.Timeout = 2 * time.Second
	recordingResponse, requestErr := recordingClient.Do(recordingRequest)
	if recordingResponse != nil {
		_, _ = io.ReadAll(recordingResponse.Body)
		_ = recordingResponse.Body.Close()
	}
	if requestErr == nil && recordingResponse == nil {
		t.Fatal("recording playback returned neither a response nor an error")
	}
	select {
	case canceledAt := <-providerContextDone:
		if canceledAt.Before(contentExpiresAt.Add(-50*time.Millisecond)) ||
			canceledAt.After(contentExpiresAt.Add(300*time.Millisecond)) {
			t.Fatalf("recording provider context canceled at %v; content expired at %v",
				canceledAt, contentExpiresAt)
		}
	case <-time.After(2500 * time.Millisecond):
		t.Fatal("recording provider context was not canceled at content expiry")
	}
	audio.update(func(audio *httpVoicemailAudio) {
		audio.streamUntilContextDone = false
		audio.contextDone = nil
	})
	if _, err := pool.Exec(context.Background(), `
		UPDATE access_memberships SET revoked_at = $2
		WHERE user_subject = $1
	`, identity.Subject, now); err != nil {
		t.Fatalf("revoke voicemail playback membership: %v", err)
	}
	revokedRequest, err := http.NewRequest(
		http.MethodGet,
		server.URL+"/v1/calling/voicemail-playback/"+url.PathEscape(capability.Token),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	revokedResponse, err := server.Client().Do(revokedRequest)
	if err != nil {
		t.Fatalf("request revoked voicemail playback: %v", err)
	}
	_ = revokedResponse.Body.Close()
	if revokedResponse.StatusCode != http.StatusForbidden || audio.calls != 2 {
		t.Fatalf("revoked playback = status:%d provider-calls:%d",
			revokedResponse.StatusCode, audio.calls)
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE access_memberships SET revoked_at = NULL
		WHERE user_subject = $1
	`, identity.Subject); err != nil {
		t.Fatalf("restore voicemail playback membership: %v", err)
	}

	waitForLog(t, &metrics, `"outcome":"succeeded"`)
	metrics.Reset()
	malformedRange := "items 0-3/13"
	audio.update(func(audio *httpVoicemailAudio) {
		audio.contentRange = &malformedRange
	})
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
	waitForLog(t, &metrics, `"outcome":"invalid_response"`)
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
	audio.update(func(audio *httpVoicemailAudio) {
		audio.contentRange = nil
		audio.contentLength = "invalid"
		audio.failStream = true
	})
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
	waitForLog(t, &metrics, `"outcome":"unavailable"`)
	if (requestErr == nil && readErr == nil) ||
		!strings.Contains(metrics.String(), `"outcome":"unavailable"`) ||
		strings.Contains(metrics.String(), `"outcome":"succeeded"`) {
		t.Fatalf("failed voicemail stream = request-err:%v read-err:%v metrics:%s",
			requestErr, readErr, metrics.String())
	}
	audio.update(func(audio *httpVoicemailAudio) {
		audio.failStream = false
	})

	failures := []struct {
		name       string
		reason     humancalling.RecordingUnavailableReason
		retryAfter string
		status     int
		retryable  bool
	}{
		{name: "recording not found", reason: humancalling.RecordingNotFound, status: http.StatusNotFound},
		{name: "provider auth", reason: humancalling.RecordingProviderAuth, status: http.StatusServiceUnavailable},
		{name: "provider rate limited", reason: humancalling.RecordingRateLimited, retryAfter: "7", status: http.StatusServiceUnavailable, retryable: true},
		{name: "provider timeout", reason: humancalling.RecordingProviderTimeout, status: http.StatusGatewayTimeout, retryable: true},
		{name: "provider unavailable", reason: humancalling.RecordingProviderFailure, status: http.StatusServiceUnavailable, retryable: true},
		{name: "recording URL expired", reason: humancalling.RecordingURLExpired, status: http.StatusServiceUnavailable, retryable: true},
	}
	for _, failure := range failures {
		t.Run(failure.name, func(t *testing.T) {
			audio.update(func(audio *httpVoicemailAudio) {
				audio.err = &humancalling.RecordingUnavailableError{
					Reason:     failure.reason,
					RetryAfter: failure.retryAfter,
				}
			})
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
	_, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "work-http-test",
		Practices: []access.PracticeProvision{{
			Key:       "task-practice",
			Name:      "Task Practice",
			Locations: []access.LocationProvision{{Key: "task-office", Name: "Task Office"}},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "task-staff",
				Email:         "task-staff@synthetic.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
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
	authorization := testaccess.Activate(t, accessModule, identity)
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
	if page.Items[0].AutomaticAcknowledgement != nil {
		t.Fatalf("Task automatic acknowledgement = %#v", page.Items[0].AutomaticAcknowledgement)
	}
	if page.Counts.Tasks != 1 {
		t.Fatalf("Task query counts = %#v, want one Task", page.Counts)
	}

	countFreeBody, _ := json.Marshal(map[string]any{
		"practiceId":    authorization.Practice.ID,
		"search":        "(985) 555-0100",
		"includeCounts": false,
	})
	countFreeResponse := request(t, server.Client(), http.MethodPost,
		server.URL+"/v1/tasks/query", "task-token", countFreeBody)
	if countFreeResponse.StatusCode != http.StatusOK {
		t.Fatalf("count-free Task query status = %d", countFreeResponse.StatusCode)
	}
	var countFreePage api.TaskPage
	decode(t, countFreeResponse, &countFreePage)
	if countFreePage.Counts != nil || len(countFreePage.Items) != 1 || countFreePage.Items[0].Id.String() != task.ID {
		t.Fatalf("count-free Task query page = %#v", countFreePage)
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
	_, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "portal-pool-test",
		Practices: []access.PracticeProvision{{
			Key:       "pool-practice",
			Name:      "Pool Fixture Practice",
			Locations: []access.LocationProvision{{Key: "fixture-1", Name: "Fixture 1"}},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "pool-member",
				Email:         "member@pool.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
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
	testaccess.Activate(t, accessModule, identity)
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
	}, pool, calling, messaging.New(pool, nil, nil, nil, messaging.Config{}, nil))
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
			WebhookPublicKeys: [][]byte{publicKey, nextPublicKey},
		},
		func() time.Time { return now },
	)
	handler, err := httpapi.NewProviderIngress(httpapi.Config{
		AcquireTimeout: 500 * time.Millisecond,
		Observer:       observer,
	}, pool, calling, messaging.New(pool, nil, nil, nil, messaging.Config{}, nil))
	if err != nil {
		t.Fatalf("new provider-ingress HTTP adapter: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	raw := []byte(fmt.Sprintf(
		`{"data":{"record_type":"event","event_type":"call.initiated","id":"http-webhook-event","occurred_at":"%s","payload":{"call_control_id":"caller-control","call_leg_id":"caller-leg","call_session_id":"caller-session","to":"sip:opaque@synthetic.sip.telnyx.com"}}}`,
		now.Format(time.RFC3339Nano),
	))
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
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
	_, err := accessModule.Provision(
		context.Background(),
		access.Provisioning{
			Environment: "test",
			RequestedBy: "ai-task-http-test",
			Practices: []access.PracticeProvision{{
				Key:  "calling-practice",
				Name: "Calling Practice",
				Locations: []access.LocationProvision{{
					Key:             "calling-location",
					Name:            "Calling Location",
					AbitaOfficeKeys: []string{"spring-hill"},
				}},
				AccessGrants: []access.AccessGrantProvision{{
					Key:           "calling-staff",
					Email:         "staff@calling.test",
					Role:          access.RoleStaff,
					LocationScope: access.LocationScopeAll,
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
	testaccess.Activate(t, accessModule, staffIdentity)
	var practiceID string
	if err := pool.QueryRow(context.Background(), `
		SELECT id::text
		FROM access_practices
		WHERE provisioning_key = 'calling-practice'
	`).Scan(&practiceID); err != nil {
		t.Fatalf("load staff Task HTTP Practice: %v", err)
	}
	serviceAuthenticator, err := access.NewServiceAuthenticator(
		access.ServiceCredential{
			Token: "demo-token",
			Identity: access.ServiceIdentity{
				Subject:       "acuity-demo",
				PracticeID:    practiceID,
				LocationScope: access.LocationScopeAll,
				Capabilities: []access.ServiceCapability{
					access.ServiceCapabilityCreateTask,
					access.ServiceCapabilityHumanHandoff,
				},
			},
		},
		access.ServiceCredential{
			Token: "production-token",
			Identity: access.ServiceIdentity{
				Subject:       "abita-eye-group",
				PracticeID:    practiceID,
				LocationScope: access.LocationScopeAll,
				Capabilities: []access.ServiceCapability{
					access.ServiceCapabilityCreateTask,
					access.ServiceCapabilityHumanHandoff,
					access.ServiceCapabilityIngestAIInteraction,
				},
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
			Messaging:            messaging.New(pool, accessModule, work.New(pool, accessModule, nil), nil, messaging.Config{}, nil),
			Work:                 work.New(pool, accessModule, func() time.Time { return now }),
			Workspace:            workspace.New(pool, accessModule),
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
		"demo-token",
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
		"demo-token",
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

	created := request(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/v1/tasks",
		"production-token",
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
		"production-token",
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
		projected.CreatedBy.Subject != "abita-eye-group" ||
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
		"production-token",
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
	_, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "human-calling-http-test",
		Practices: []access.PracticeProvision{{
			Key:  "calling-practice",
			Name: "Calling Practice",
			Locations: []access.LocationProvision{{
				Key:             "calling-location",
				Name:            "Calling Location",
				AbitaOfficeKeys: []string{"calling-office"},
			}},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "calling-staff",
				Email:         "staff@calling.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
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
	authorization := testaccess.Activate(t, accessModule, identity)
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
		access.ServiceCredential{
			Token: "production-token",
			Identity: access.ServiceIdentity{
				Subject:       "abita-eye-group",
				PracticeID:    authorization.Practice.ID,
				LocationScope: access.LocationScopeAll,
				Capabilities: []access.ServiceCapability{
					access.ServiceCapabilityHumanHandoff,
				},
			},
		},
		access.ServiceCredential{
			Token: "demo-token",
			Identity: access.ServiceIdentity{
				Subject:       "acuity-demo",
				PracticeID:    authorization.Practice.ID,
				LocationScope: access.LocationScopeAll,
				Capabilities: []access.ServiceCapability{
					access.ServiceCapabilityCreateTask,
					access.ServiceCapabilityHumanHandoff,
					access.ServiceCapabilityIngestAIInteraction,
				},
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
			Messaging:            messaging.New(pool, accessModule, work.New(pool, accessModule, nil), nil, messaging.Config{}, nil),
			Work:                 work.New(pool, accessModule, nil),
			Workspace:            workspace.New(pool, accessModule),
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
		"officeKey":      "calling-office",
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
	demoHandoffBody, _ := json.Marshal(map[string]any{
		"practiceId":     authorization.Practice.ID,
		"officeKey":      "calling-office",
		"sourceCallId":   "demo-http-source-call",
		"idempotencyKey": "demo-http-idempotency",
		"contact": map[string]any{
			"phone": "+15555550101",
		},
	})
	demoHandoff := request(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/v1/handoffs",
		"demo-token",
		demoHandoffBody,
	)
	if demoHandoff.StatusCode != http.StatusCreated {
		t.Fatalf(
			"demo service handoff status = %d, body = %s",
			demoHandoff.StatusCode,
			readBody(t, demoHandoff),
		)
	}
	_ = demoHandoff.Body.Close()
	created := request(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/v1/handoffs",
		"production-token",
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
	legacyBody, _ := json.Marshal(map[string]any{
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
	legacy := request(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/v1/handoffs",
		"production-token",
		legacyBody,
	)
	if legacy.StatusCode != http.StatusCreated {
		t.Fatalf("legacy handoff status = %d, body = %s", legacy.StatusCode, readBody(t, legacy))
	}
	var legacyHandoff struct {
		ID string `json:"id"`
	}
	decode(t, legacy, &legacyHandoff)
	if legacyHandoff.ID != handoff.ID {
		t.Fatalf("cross-format replay handoff = %q, want %q", legacyHandoff.ID, handoff.ID)
	}
	bothRoutesBody, _ := json.Marshal(map[string]any{
		"practiceId":     authorization.Practice.ID,
		"officeKey":      "calling-office",
		"locationId":     authorization.Locations[0].ID,
		"sourceCallId":   "ambiguous-http-source-call",
		"idempotencyKey": "ambiguous-http-idempotency",
		"contact": map[string]any{
			"phone": "+15555550102",
		},
	})
	bothRoutes := request(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/v1/handoffs",
		"production-token",
		bothRoutesBody,
	)
	if bothRoutes.StatusCode != http.StatusBadRequest {
		t.Fatalf("ambiguous handoff route status = %d, body = %s", bothRoutes.StatusCode, readBody(t, bothRoutes))
	}
	_ = bothRoutes.Body.Close()
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
	for {
		processed, err := calling.ProcessNextCommand(context.Background())
		if err != nil {
			t.Fatalf("process HTTP ring-window command: %v", err)
		}
		if !processed {
			break
		}
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
			CallID    string    `json:"callId"`
			CallLegID string    `json:"callLegId"`
			Phone     string    `json:"phone"`
			Deadline  time.Time `json:"deadline"`
		} `json:"ringing"`
	}
	etag := stateResponse.Header.Get("ETag")
	decode(t, stateResponse, &state)
	if len(state.Ringing) != 1 || state.Ringing[0].CallID == "" ||
		state.Ringing[0].CallLegID == "" ||
		state.Ringing[0].Phone != "+15555550100" ||
		!state.Ringing[0].Deadline.Equal(now.Add(20*time.Second)) || etag == "" {
		t.Fatalf("HTTP Calling state = %#v, ETag = %q", state, etag)
	}
	outboundBody, _ := json.Marshal(map[string]any{
		"sessionId":      "http-browser",
		"idempotencyKey": "http-outbound-while-ringing",
		"practiceId":     authorization.Practice.ID,
		"locationId":     authorization.Locations[0].ID,
		"destination":    "+15555550102",
	})
	outbound := request(
		t,
		server.Client(),
		http.MethodPost,
		server.URL+"/v1/calling/outbound-calls",
		"calling-token",
		outboundBody,
	)
	var outboundEnvelope api.ErrorEnvelope
	decode(t, outbound, &outboundEnvelope)
	if outbound.StatusCode != http.StatusConflict ||
		outboundEnvelope.Error.Code != "CALL_OCCUPIED" {
		t.Fatalf(
			"outbound during inbound offer = status:%d body:%#v",
			outbound.StatusCode,
			outboundEnvelope,
		)
	}
}

type callingHangupHTTPFixture struct {
	pool          *pgxpool.Pool
	calling       *humancalling.Module
	identity      access.Identity
	authorization access.Authorization
	server        *httptest.Server
	token         string
	sessionID     string
}

func newCallingHangupHTTPFixture(
	t *testing.T,
	prefix string,
	clock func() time.Time,
	config humancalling.Config,
) callingHangupHTTPFixture {
	t.Helper()
	pool := testdb.Open(t)
	accessModule := access.New(pool, clock)
	email := prefix + "@synthetic.test"
	if _, err := accessModule.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: prefix + "-http-test",
		Practices: []access.PracticeProvision{{
			Key:  prefix + "-practice",
			Name: prefix + " Practice",
			Locations: []access.LocationProvision{{
				Key:  prefix + "-location",
				Name: prefix + " Location",
			}},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           prefix + "-staff",
				Email:         email,
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}},
		}},
	}); err != nil {
		t.Fatalf("provision %s fixture: %v", prefix, err)
	}
	identity := access.Identity{
		Subject:       prefix + "-subject",
		Email:         email,
		EmailVerified: true,
	}
	authorization := testaccess.Activate(t, accessModule, identity)
	calling := humancalling.New(
		pool,
		accessModule,
		httpCallingProvider{},
		config,
		clock,
	)
	token := prefix + "-token"
	handler, err := newPortalHandlerWithCalling(
		t,
		httpapi.Config{
			AllowedOrigins: []string{"http://localhost:3000"},
			AcquireTimeout: 500 * time.Millisecond,
		},
		pool,
		accessModule,
		staticAuthenticator{token: identity},
		calling,
	)
	if err != nil {
		t.Fatalf("new %s HTTP adapter: %v", prefix, err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return callingHangupHTTPFixture{
		pool:          pool,
		calling:       calling,
		identity:      identity,
		authorization: authorization,
		server:        server,
		token:         token,
		sessionID:     prefix + "-browser",
	}
}

func (fixture callingHangupHTTPFixture) postHangup(
	t *testing.T,
	callID string,
	sessionID string,
) *http.Response {
	t.Helper()
	body, _ := json.Marshal(api.CallingControlRequest{SessionId: sessionID})
	return request(
		t,
		fixture.server.Client(),
		http.MethodPost,
		fixture.server.URL+"/v1/calling/calls/"+callID+"/hangup",
		fixture.token,
		body,
	)
}

func (fixture callingHangupHTTPFixture) hangupCommandCount(t *testing.T, callID string) int {
	t.Helper()
	var count int
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT count(*) FROM human_calling_provider_commands
		WHERE call_id = $1 AND action = 'HANGUP_LEG'
	`, callID).Scan(&count); err != nil {
		t.Fatalf("count Hangup commands: %v", err)
	}
	return count
}

func TestCallingHangupEndsOwnedOutboundBeforeProviderControl(t *testing.T) {
	now := time.Date(2026, time.August, 26, 14, 45, 0, 0, time.UTC)
	fixture := newCallingHangupHTTPFixture(
		t, "end-preparing-outbound-http", func() time.Time { return now },
		humancalling.Config{},
	)
	if lease, err := fixture.calling.AcquireSoftphone(
		context.Background(), fixture.identity, fixture.sessionID, false,
	); err != nil || !lease.Owner {
		t.Fatalf("acquire preparing outbound softphone: state=%#v err=%v", lease, err)
	}
	callID, callerLegID, staffLegID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	tx, err := fixture.pool.Begin(context.Background())
	if err != nil {
		t.Fatalf("begin preparing outbound HTTP fixture: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(context.Background(), `
		INSERT INTO human_calling_calls (
			id, practice_id, location_id, direction, entry_point,
			destination_phone, outbound_caller_id, initiating_subject,
			outbound_idempotency_key, outbound_input_fingerprint,
			version, created_at, updated_at
		) VALUES (
			$1, $2, $3, 'OUTBOUND', 'STANDALONE', '+15555550123',
			'+14843336938', $4, 'end-preparing-outbound-http', $5, 1, $6, $6
		)
	`, callID, fixture.authorization.Practice.ID,
		fixture.authorization.Locations[0].ID, fixture.identity.Subject,
		make([]byte, 32), now); err != nil {
		t.Fatalf("insert preparing outbound HTTP Call: %v", err)
	}
	if _, err := tx.Exec(context.Background(), `
		INSERT INTO human_calling_call_legs (
			id, call_id, role, sequence, state, created_at, updated_at
		) VALUES ($1, $2, 'CALLER', 1, 'PENDING', $3, $3)
	`, callerLegID, callID, now); err != nil {
		t.Fatalf("insert preparing outbound caller CallLeg: %v", err)
	}
	if _, err := tx.Exec(context.Background(), `
		INSERT INTO human_calling_call_legs (
			id, call_id, role, sequence, staff_subject, staff_session_id,
			state, created_at, updated_at
		) VALUES ($1, $2, 'STAFF', 1, $3, $4, 'PENDING', $5, $5)
	`, staffLegID, callID, fixture.identity.Subject, fixture.sessionID, now); err != nil {
		t.Fatalf("insert preparing outbound Staff CallLeg: %v", err)
	}
	if _, err := tx.Exec(context.Background(), `
		INSERT INTO human_calling_provider_commands (
			call_id, call_leg_id, user_subject, action, payload, next_attempt_at,
			created_at, updated_at
		) VALUES ($1, $2, $3, 'DIAL_OUTBOUND_STAFF', '{}'::jsonb, $4, $4, $4)
	`, callID, staffLegID, fixture.identity.Subject, now); err != nil {
		t.Fatalf("insert preparing outbound Dial command: %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("commit preparing outbound HTTP fixture: %v", err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		response := fixture.postHangup(t, callID, fixture.sessionID)
		if response.StatusCode != http.StatusAccepted {
			t.Fatalf("preparing outbound End attempt %d status = %d, body = %s",
				attempt, response.StatusCode, readBody(t, response))
		}
		var current api.CallingCall
		decode(t, response, &current)
		if current.State != api.CallingCallStateUNANSWERED {
			t.Fatalf("preparing outbound End attempt %d Call state = %s",
				attempt, current.State)
		}
	}
	var terminal string
	var canceledDials, requestedEvents int
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT call.terminal_outcome,
			(SELECT count(*) FROM human_calling_provider_commands command
				WHERE command.call_id = call.id AND command.action = 'DIAL_OUTBOUND_STAFF'
					AND command.state = 'FAILED'
					AND command.last_error_code = 'STAFF_ENDED_BEFORE_PROVIDER_START'),
			(SELECT count(*) FROM human_calling_timeline timeline
				WHERE timeline.call_id = call.id AND timeline.kind = 'call.hangup.requested')
		FROM human_calling_calls call WHERE call.id = $1
	`, callID).Scan(&terminal, &canceledDials, &requestedEvents); err != nil {
		t.Fatalf("read preparing outbound HTTP End: %v", err)
	}
	if terminal != "UNANSWERED" || canceledDials != 1 || requestedEvents != 1 {
		t.Fatalf("preparing outbound HTTP End = terminal:%s canceled:%d events:%d",
			terminal, canceledDials, requestedEvents)
	}
}

func TestCallingHangupReturnsTerminalCallWhenProviderWinsRace(t *testing.T) {
	now := time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)
	currentTime := now
	fixture := newCallingHangupHTTPFixture(
		t,
		"provider-first-hangup",
		func() time.Time { return currentTime },
		humancalling.Config{
			HandoffSIPDomain: "synthetic.sip.telnyx.com",
			HandoffTokenKey:  []byte("0123456789abcdef0123456789abcdef"),
			CallControlID:    "provider-first-hangup-connection",
		},
	)
	_, err := fixture.calling.CreateHandoff(context.Background(), humancalling.CreateHandoffCommand{
		Service: humancalling.ServiceIdentity{
			Subject:    "provider-first-hangup-service",
			PracticeID: fixture.authorization.Practice.ID,
		},
		LocationID:     fixture.authorization.Locations[0].ID,
		SourceCallID:   "provider-first-hangup-source",
		IdempotencyKey: "provider-first-hangup",
		Contact:        humancalling.ContactContext{Phone: "+15555550100"},
	})
	if err != nil {
		t.Fatalf("create provider-first Hangup handoff: %v", err)
	}
	if err := fixture.calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:       "provider-first-hangup-caller-initiated",
		Type:          humancalling.FactCallInitiated,
		OccurredAt:    now,
		ConnectionID:  "provider-first-hangup-connection",
		CallControlID: "provider-first-hangup-caller-control",
		CallLegID:     "provider-first-hangup-caller-leg",
		CallSessionID: "provider-first-hangup-session",
		From:          "+15555550100",
		To:            "+14843989071",
	}); err != nil {
		t.Fatalf("admit provider-first Hangup Call: %v", err)
	}
	var callID, callerLegID string
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT call.id::text, caller.id::text
		FROM human_calling_calls call
		JOIN human_calling_call_legs caller
			ON caller.call_id = call.id AND caller.role = 'CALLER'
		WHERE caller.provider_call_leg_id = 'provider-first-hangup-caller-leg'
	`).Scan(&callID, &callerLegID); err != nil {
		t.Fatalf("read provider-first Hangup Call: %v", err)
	}
	connectedAt := now.Add(-time.Second)
	if _, err := fixture.pool.Exec(context.Background(), `
		UPDATE human_calling_call_legs
		SET state = 'BRIDGED', answered_at = $2, bridge_pending_at = $2,
			bridged_at = $2, updated_at = $2
		WHERE id = $1
	`, callerLegID, connectedAt); err != nil {
		t.Fatalf("connect provider-first Hangup caller: %v", err)
	}
	if _, err := fixture.pool.Exec(context.Background(), `
		INSERT INTO human_calling_call_legs (
			call_id, role, sequence, staff_subject, staff_session_id, state,
			provider_call_control_id, provider_call_leg_id, provider_call_session_id,
			answered_at, bridge_pending_at, bridged_at, created_at, updated_at
		)
		VALUES ($1, 'STAFF', 1, $2, 'provider-first-hangup-browser', 'BRIDGED',
			'provider-first-hangup-staff-control', 'provider-first-hangup-staff-leg',
			'provider-first-hangup-staff-session', $3, $3, $3, $3, $3)
	`, callID, fixture.identity.Subject, connectedAt); err != nil {
		t.Fatalf("connect provider-first Hangup Staff CallLeg: %v", err)
	}
	if lease, err := fixture.calling.AcquireSoftphone(
		context.Background(), fixture.identity, fixture.sessionID, false,
	); err != nil || !lease.Owner {
		t.Fatalf("acquire provider-first Hangup softphone: state=%#v err=%v", lease, err)
	}

	providerEndedAt := now
	if err := fixture.calling.ApplyProviderFact(context.Background(), humancalling.ProviderFact{
		EventID:           "provider-first-hangup-staff-ended",
		Type:              humancalling.FactCallHangup,
		OccurredAt:        providerEndedAt,
		CallControlID:     "provider-first-hangup-staff-control",
		CallLegID:         "provider-first-hangup-staff-leg",
		CallSessionID:     "provider-first-hangup-staff-session",
		HangupCause:       "NORMAL_CLEARING",
		TerminationSource: "STAFF",
	}); err != nil {
		t.Fatalf("project provider-first Staff Hangup: %v", err)
	}
	currentTime = providerEndedAt.Add(5 * time.Millisecond)
	var terminalOutcome, staffState string
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT call.terminal_outcome, staff.state
		FROM human_calling_calls call
		JOIN human_calling_call_legs staff
			ON staff.call_id = call.id AND staff.role = 'STAFF'
		WHERE call.id = $1
	`, callID).Scan(&terminalOutcome, &staffState); err != nil {
		t.Fatalf("read provider-first terminal state: %v", err)
	}
	if terminalOutcome != "ENDED" || staffState != "ENDED" {
		t.Fatalf(
			"provider-first terminal state = Call:%s Staff:%s",
			terminalOutcome,
			staffState,
		)
	}
	hangupCommandsBefore := fixture.hangupCommandCount(t, callID)
	response := fixture.postHangup(t, callID, fixture.sessionID)
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf(
			"provider-first Hangup status = %d, body = %s",
			response.StatusCode,
			readBody(t, response),
		)
	}
	var current api.CallingCall
	decode(t, response, &current)
	if current.State != api.CallingCallStateNEEDSDISPOSITION {
		t.Fatalf("provider-first Hangup Call state = %s", current.State)
	}
	hangupCommandsAfter := fixture.hangupCommandCount(t, callID)
	if hangupCommandsBefore != 1 || hangupCommandsAfter != hangupCommandsBefore {
		t.Fatalf(
			"provider-first Hangup commands = before:%d after:%d",
			hangupCommandsBefore,
			hangupCommandsAfter,
		)
	}
}

func TestCallingHangupReplaysCommittedCommandButRejectsMissingIntent(t *testing.T) {
	now := time.Date(2026, time.August, 14, 13, 0, 0, 0, time.UTC)
	fixture := newCallingHangupHTTPFixture(
		t,
		"replayed-hangup",
		func() time.Time { return now },
		humancalling.Config{},
	)
	handoffID := uuid.NewString()
	callID := uuid.NewString()
	if _, err := fixture.pool.Exec(context.Background(), `
		INSERT INTO human_calling_handoffs (
			id, service_subject, practice_id, location_id, source_call_id,
			idempotency_key, input_fingerprint, phone, expires_at, consumed_at,
			created_at
		)
		VALUES ($1, 'replayed-hangup-service', $2, $3,
			'replayed-hangup-source', 'replayed-hangup-key', $4,
			'+15555550101', $5, $6, $6)
	`, handoffID, fixture.authorization.Practice.ID, fixture.authorization.Locations[0].ID,
		[]byte("replayed-hangup"), now.Add(time.Minute), now); err != nil {
		t.Fatalf("insert replayed Hangup handoff: %v", err)
	}
	if _, err := fixture.pool.Exec(context.Background(), `
		INSERT INTO human_calling_calls (
			id, source_handoff_id, practice_id, location_id, caller_phone,
			created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, '+15555550101', $5, $5)
	`, callID, handoffID, fixture.authorization.Practice.ID,
		fixture.authorization.Locations[0].ID, now); err != nil {
		t.Fatalf("insert replayed Hangup Call: %v", err)
	}
	connectedAt := now.Add(-time.Second)
	if _, err := fixture.pool.Exec(context.Background(), `
		INSERT INTO human_calling_call_legs (
			call_id, role, sequence, staff_subject, staff_session_id, state,
			provider_call_control_id, provider_call_leg_id, provider_call_session_id,
			answered_at, bridge_pending_at, bridged_at, created_at, updated_at
		)
		VALUES
			($1, 'CALLER', 1, NULL, NULL, 'BRIDGED',
				'replayed-hangup-caller-control', 'replayed-hangup-caller-leg',
				'replayed-hangup-session', $3, $3, $3, $2, $3),
			($1, 'STAFF', 1, $4, 'replayed-hangup-browser', 'BRIDGED',
				'replayed-hangup-staff-control', 'replayed-hangup-staff-leg',
				'replayed-hangup-session', $3, $3, $3, $2, $3)
	`, callID, now, connectedAt, fixture.identity.Subject); err != nil {
		t.Fatalf("insert replayed Hangup CallLegs: %v", err)
	}
	if lease, err := fixture.calling.AcquireSoftphone(
		context.Background(), fixture.identity, fixture.sessionID, false,
	); err != nil || !lease.Owner {
		t.Fatalf("acquire replayed Hangup softphone: state=%#v err=%v", lease, err)
	}
	mismatch := fixture.postHangup(t, callID, "another-browser")
	var envelope api.ErrorEnvelope
	decode(t, mismatch, &envelope)
	if mismatch.StatusCode != http.StatusConflict || envelope.Error.Code != "CALL_CONFLICT" {
		t.Fatalf(
			"active session mismatch = status:%d body:%#v",
			mismatch.StatusCode,
			envelope,
		)
	}
	first := fixture.postHangup(t, callID, fixture.sessionID)
	if first.StatusCode != http.StatusAccepted {
		t.Fatalf("first Hangup status = %d, body = %s", first.StatusCode, readBody(t, first))
	}
	_ = first.Body.Close()
	commandsAfterFirst := fixture.hangupCommandCount(t, callID)
	if commandsAfterFirst != 2 {
		t.Fatalf("first Hangup commands = %d", commandsAfterFirst)
	}
	if _, err := fixture.pool.Exec(context.Background(), `
		UPDATE human_calling_provider_commands
		SET state = 'FAILED', last_error_code = 'PROVIDER_REJECTED', updated_at = $2
		WHERE call_id = $1 AND action = 'HANGUP_LEG'
	`, callID, now.Add(time.Second)); err != nil {
		t.Fatalf("fail committed Hangup commands: %v", err)
	}
	second := fixture.postHangup(t, callID, fixture.sessionID)
	if second.StatusCode != http.StatusAccepted {
		t.Fatalf("replayed Hangup status = %d, body = %s", second.StatusCode, readBody(t, second))
	}
	_ = second.Body.Close()
	var commandsAfterReplay, failedCommands, endingLegs, requestedEvents int
	if err := fixture.pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM human_calling_provider_commands
				WHERE call_id = $1 AND action = 'HANGUP_LEG'),
			(SELECT count(*) FROM human_calling_provider_commands
				WHERE call_id = $1 AND action = 'HANGUP_LEG' AND state = 'FAILED'),
			(SELECT count(*) FROM human_calling_call_legs
				WHERE call_id = $1 AND state = 'ENDING'),
			(SELECT count(*) FROM human_calling_timeline
				WHERE call_id = $1 AND kind = 'call.hangup.requested')
	`, callID).Scan(
		&commandsAfterReplay,
		&failedCommands,
		&endingLegs,
		&requestedEvents,
	); err != nil {
		t.Fatalf("read replayed Hangup state: %v", err)
	}
	if commandsAfterReplay != commandsAfterFirst || failedCommands != 2 ||
		endingLegs != 2 || requestedEvents != 1 {
		t.Fatalf(
			"replayed Hangup = commands:%d->%d failed:%d ending:%d events:%d",
			commandsAfterFirst,
			commandsAfterReplay,
			failedCommands,
			endingLegs,
			requestedEvents,
		)
	}
	if _, err := fixture.pool.Exec(context.Background(), `
		DELETE FROM human_calling_provider_commands
		WHERE call_id = $1 AND action = 'HANGUP_LEG'
	`, callID); err != nil {
		t.Fatalf("remove committed Hangup command evidence: %v", err)
	}
	missing := fixture.postHangup(t, callID, fixture.sessionID)
	var missingEnvelope api.ErrorEnvelope
	decode(t, missing, &missingEnvelope)
	if missing.StatusCode != http.StatusConflict ||
		missingEnvelope.Error.Code != "CALL_CONFLICT" {
		t.Fatalf(
			"missing Hangup intent = status:%d body:%#v",
			missing.StatusCode,
			missingEnvelope,
		)
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
	mu                     sync.Mutex
	calls                  int
	rangeHeader            string
	contentLength          string
	contentRange           *string
	failStream             bool
	streamUntilContextDone bool
	contextDone            chan time.Time
	err                    error
}

type httpVoicemailAudioSnapshot struct {
	calls       int
	rangeHeader string
}

func (audio *httpVoicemailAudio) update(update func(*httpVoicemailAudio)) {
	audio.mu.Lock()
	defer audio.mu.Unlock()
	update(audio)
}

func (audio *httpVoicemailAudio) snapshot() httpVoicemailAudioSnapshot {
	audio.mu.Lock()
	defer audio.mu.Unlock()
	return httpVoicemailAudioSnapshot{
		calls:       audio.calls,
		rangeHeader: audio.rangeHeader,
	}
}

func (audio *httpVoicemailAudio) OpenRecording(
	ctx context.Context,
	_ string,
	rangeHeader string,
) (humancalling.PlaybackContent, error) {
	audio.mu.Lock()
	audio.calls++
	audio.rangeHeader = rangeHeader
	providerErr := audio.err
	contentRange := "bytes 0-3/13"
	if audio.contentRange != nil {
		contentRange = *audio.contentRange
	}
	contentLength := audio.contentLength
	if contentLength == "" {
		contentLength = "4"
	}
	failStream := audio.failStream
	streamUntilContextDone := audio.streamUntilContextDone
	contextDone := audio.contextDone
	audio.mu.Unlock()
	if providerErr != nil {
		return humancalling.PlaybackContent{}, providerErr
	}
	var body io.ReadCloser = io.NopCloser(strings.NewReader("synt"))
	if failStream {
		body = &failingVoicemailBody{}
	}
	if streamUntilContextDone {
		return humancalling.PlaybackContent{
			StatusCode:  http.StatusOK,
			ContentType: "audio/mpeg",
			Body: &contextBoundPlaybackBody{
				ctx:  ctx,
				done: contextDone,
			},
		}, nil
	}
	return humancalling.PlaybackContent{
		StatusCode:    http.StatusPartialContent,
		ContentType:   "audio/mpeg",
		ContentLength: contentLength,
		ContentRange:  contentRange,
		Body:          body,
	}, nil
}

type contextBoundPlaybackBody struct {
	ctx  context.Context
	done chan time.Time
	sent bool
}

func (body *contextBoundPlaybackBody) Read(target []byte) (int, error) {
	if !body.sent {
		body.sent = true
		payload := bytes.Repeat([]byte("x"), 32*1024)
		return copy(target, payload), nil
	}
	<-body.ctx.Done()
	select {
	case body.done <- time.Now():
	default:
	}
	return 0, body.ctx.Err()
}

func (*contextBoundPlaybackBody) Close() error { return nil }

type synchronizedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (buffer *synchronizedBuffer) Write(payload []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.Buffer.Write(payload)
}

func (buffer *synchronizedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.Buffer.String()
}

func (buffer *synchronizedBuffer) Reset() {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	buffer.Buffer.Reset()
}

func waitForLog(t *testing.T, buffer *synchronizedBuffer, marker string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !strings.Contains(buffer.String(), marker) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for log marker %q: %s", marker, buffer.String())
		}
		time.Sleep(time.Millisecond)
	}
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
		access.ServiceCredential{
			Token: "unused-service-token",
			Identity: access.ServiceIdentity{
				Subject:       "unused-service",
				PracticeID:    "00000000-0000-0000-0000-000000000001",
				LocationScope: access.LocationScopeAll,
				Capabilities: []access.ServiceCapability{
					access.ServiceCapabilityHumanHandoff,
					access.ServiceCapabilityCreateTask,
				},
			},
		},
		access.ServiceCredential{
			Token: "unused-secondary-service-token",
			Identity: access.ServiceIdentity{
				Subject:       "unused-secondary-service",
				PracticeID:    "00000000-0000-0000-0000-000000000002",
				LocationScope: access.LocationScopeAll,
				Capabilities:  []access.ServiceCapability{access.ServiceCapabilityIngestAIInteraction},
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
		Messaging:            messaging.New(pool, accessModule, work.New(pool, accessModule, nil), nil, messaging.Config{}, nil),
		Work:                 work.New(pool, accessModule, nil),
		Workspace:            workspace.New(pool, accessModule),
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
