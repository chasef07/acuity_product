package httpapi_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
)

type failingAttachmentReads struct {
	*messaging.MemoryAttachmentStore
	readError error
}

func (store *failingAttachmentReads) Get(ctx context.Context, key string) ([]byte, error) {
	if store.readError != nil {
		return nil, store.readError
	}
	return store.MemoryAttachmentStore.Get(ctx, key)
}

func TestMessagingRoutesReportDatabaseFailuresAsUnavailable(t *testing.T) {
	ctx := context.Background()
	db := testdb.Open(t)
	now := time.Date(2026, time.November, 5, 17, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	identity := access.Identity{
		Subject: "messaging-failure-staff", Email: "staff@messaging-failure.test", EmailVerified: true,
	}
	outsider := access.Identity{
		Subject: "messaging-failure-outsider", Email: "outsider@messaging-failure.test", EmailVerified: true,
	}
	if _, err := access.New(db.Owner, clock).Provision(ctx, access.Provisioning{
		Environment: "test",
		RequestedBy: "messaging-failure-test",
		Practices: []access.PracticeProvision{{
			Key:       "messaging-failure",
			Name:      "Messaging Failure",
			Locations: []access.LocationProvision{{Key: "main", Name: "Main"}},
			AccessGrants: []access.AccessGrantProvision{{
				Key: "staff", Email: identity.Email,
				Role: access.RoleStaff, LocationScope: access.LocationScopeAll,
			}},
		}},
	}); err != nil {
		t.Fatalf("provision fixture: %v", err)
	}
	portalAccess := access.New(db.As(testdb.Portal), clock)
	authorization := testaccess.Activate(t, portalAccess, identity)

	database := &accessLookupFaultDatabase{Database: db.As(testdb.Portal)}
	faultAccess := access.New(database, clock)
	workModule := work.New(database, faultAccess, clock)
	store := &failingAttachmentReads{MemoryAttachmentStore: messaging.NewMemoryAttachmentStore()}
	config := messaging.Config{
		AttachmentStore:    store,
		MediaPublicBaseURL: "https://ingress.example/v1/provider/messaging-media",
		MediaSigningKey:    bytes.Repeat([]byte{9}, 32),
	}
	provider := &httpMessageProvider{}
	if err := messaging.New(db.Owner, portalAccess, work.New(db.Owner, portalAccess, clock), provider, config, clock).
		Provision(ctx, []messaging.LocationProvision{{
			PracticeKey: "messaging-failure", LocationKey: "main",
			Sender: "+17275550100", MessagingProfileID: "profile-failure",
		}}); err != nil {
		t.Fatalf("provision sender: %v", err)
	}
	serviceAuthenticator, err := access.NewServiceAuthenticator(access.ServiceCredential{
		Token: "messaging-failure-service-token",
		Identity: access.ServiceIdentity{
			Subject:       "messaging-failure-service",
			PracticeID:    authorization.Practice.ID,
			LocationScope: access.LocationScopeAll,
			Capabilities:  []access.ServiceCapability{access.ServiceCapabilityHumanHandoff},
		},
	})
	if err != nil {
		t.Fatalf("create service authenticator: %v", err)
	}
	handler, err := httpapi.NewPortal(
		httpapi.Config{AcquireTimeout: time.Second},
		db.As(testdb.Portal),
		httpapi.PortalDependencies{
			Access:        portalAccess,
			Authenticator: staticAuthenticator{"staff-token": identity, "outsider-token": outsider},
			Calling: humancalling.New(db.As(testdb.Portal), portalAccess, httpCallingProvider{},
				humancalling.Config{}, clock),
			Interactions:         interaction.New(db.As(testdb.Portal), portalAccess, nil),
			Messaging:            messaging.New(database, faultAccess, workModule, provider, config, clock),
			Work:                 work.New(db.As(testdb.Portal), portalAccess, clock),
			Workspace:            workspace.New(db.As(testdb.Portal), portalAccess),
			ServiceAuthenticator: serviceAuthenticator,
		},
	)
	if err != nil {
		t.Fatalf("create portal: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	call := func(method, path, token string, body any) (int, string) {
		t.Helper()
		var payload []byte
		if body != nil {
			payload, _ = json.Marshal(body)
		}
		response := request(t, server.Client(), method, server.URL+path, token, payload)
		defer response.Body.Close()
		return response.StatusCode, readBody(t, response)
	}
	sendBody := func(key string) api.SendMessageRequest {
		destination := "+17275550199"
		return api.SendMessageRequest{
			PracticeId:     parsedUUID(t, authorization.Practice.ID),
			LocationId:     parsedUUID(t, authorization.Locations[0].ID),
			Destination:    &destination,
			Body:           "Your records are ready.",
			IdempotencyKey: key,
		}
	}
	status, body := call(http.MethodPost, "/v1/messages", "staff-token", sendBody("seed-send"))
	if status != http.StatusCreated {
		t.Fatalf("seed send = %d %s", status, body)
	}
	var sent api.MessageReceipt
	if err := json.Unmarshal([]byte(body), &sent); err != nil {
		t.Fatalf("decode seed send: %v", err)
	}
	png := append(
		[]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'},
		bytes.Repeat([]byte{0}, 64)...,
	)
	upload := map[string]any{
		"practiceId":    authorization.Practice.ID,
		"locationId":    authorization.Locations[0].ID,
		"fileName":      "note.png",
		"contentType":   "image/png",
		"contentBase64": base64.StdEncoding.EncodeToString(png),
	}
	status, body = call(http.MethodPost, "/v1/attachments", "staff-token", upload)
	if status != http.StatusCreated {
		t.Fatalf("seed attachment = %d %s", status, body)
	}
	var attachment api.MessageAttachment
	if err := json.Unmarshal([]byte(body), &attachment); err != nil {
		t.Fatalf("decode seed attachment: %v", err)
	}
	attachmentPath := "/v1/attachments/" + attachment.Id.String()
	sendAgain := map[string]any{"idempotencyKey": "seed-again", "duplicateRiskAcknowledged": true}
	followUp := map[string]any{"title": "Call the patient back"}

	expect := func(label string, wantStatus int, wantCode string, status int, body string) {
		t.Helper()
		if status != wantStatus || !strings.Contains(body, `"`+wantCode+`"`) {
			t.Errorf("%s = %d %s, want %d %s", label, status, body, wantStatus, wantCode)
		}
	}
	routes := []struct {
		label        string
		method, path string
		body         any
	}{
		{"send", http.MethodPost, "/v1/messages", sendBody("failure-send")},
		{"send again", http.MethodPost, "/v1/messages/" + sent.Message.Id.String() + "/send-again", sendAgain},
		{"follow-up Task", http.MethodPost, "/v1/messages/" + sent.Message.Id.String() + "/follow-up-task", followUp},
		{"attachment upload", http.MethodPost, "/v1/attachments", upload},
		{"attachment read", http.MethodGet, attachmentPath, nil},
	}
	database.failQueriesContaining("FROM access_platform_operators")
	for _, route := range routes {
		status, body := call(route.method, route.path, "staff-token", route.body)
		expect(route.label+" during an access lookup failure",
			http.StatusServiceUnavailable, "UNAVAILABLE", status, body)
	}
	database.failQueriesContaining("")
	for _, route := range routes {
		status, body := call(route.method, route.path, "outsider-token", route.body)
		expect(route.label+" by an outsider", http.StatusForbidden, "ACCESS_DENIED", status, body)
	}

	store.readError = errors.New("synthetic object store outage")
	status, body = call(http.MethodGet, attachmentPath, "staff-token", nil)
	expect("attachment read during a store outage",
		http.StatusServiceUnavailable, "UNAVAILABLE", status, body)
	store.readError = messaging.ErrObjectNotFound
	status, body = call(http.MethodGet, attachmentPath, "staff-token", nil)
	expect("attachment read with a missing object", http.StatusForbidden, "ACCESS_DENIED", status, body)
}
