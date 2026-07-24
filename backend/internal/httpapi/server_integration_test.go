package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/api"
	"github.com/chasef07/acuity_product/backend/internal/authn"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
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
	handler, err := httpapi.New(httpapi.Config{
		Role:           "portal-api",
		AllowedOrigin:  "http://localhost:3000",
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
	handler, err := httpapi.New(httpapi.Config{
		Role:           "portal-api",
		AllowedOrigin:  "http://localhost:3000",
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

	handler, err := httpapi.New(httpapi.Config{
		Role:           "provider-ingress",
		AcquireTimeout: 75 * time.Millisecond,
	}, pool, nil, nil)
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

type staticAuthenticator map[string]access.Identity

func (adapter staticAuthenticator) Authenticate(_ context.Context, token string) (access.Identity, error) {
	identity, ok := adapter[token]
	if !ok {
		return access.Identity{}, authn.ErrInvalidCredential
	}
	return identity, nil
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
