package httpapi_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCallingPollsKeepServingWhenOnePortalConnectionIsBusy(t *testing.T) {
	var oneConnection []int
	t.Run("one connection", func(t *testing.T) {
		oneConnection = runCallingPollBurst(t, 1)
	})
	if oneConnection[0] == http.StatusOK || oneConnection[1] == http.StatusOK {
		t.Fatalf("one-connection portal unexpectedly served busy burst: %v", oneConnection)
	}

	var twoConnections []int
	t.Run("two connections", func(t *testing.T) {
		twoConnections = runCallingPollBurst(t, 2)
	})
	if twoConnections[0] != http.StatusOK || twoConnections[1] != http.StatusOK {
		t.Fatalf("two-connection portal burst statuses = %v, want two 200s", twoConnections)
	}
}

func runCallingPollBurst(t *testing.T, poolMaximum int32) []int {
	t.Helper()
	ownerPool := testdb.Open(t)
	now := time.Date(2026, time.August, 11, 10, 0, 0, 0, time.UTC)
	ownerAccess := access.New(ownerPool, func() time.Time { return now })
	_, err := ownerAccess.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "portal-polling-capacity-test",
		Practices: []access.PracticeProvision{{
			Key:       "portal-polling",
			Name:      "Portal Polling",
			Locations: []access.LocationProvision{{Key: "main", Name: "Main"}},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "staff",
				Email:         "staff@portal-polling.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision portal polling fixture: %v", err)
	}
	identity := access.Identity{
		Subject:       "portal-polling-staff",
		Email:         "staff@portal-polling.test",
		EmailVerified: true,
	}
	testaccess.Activate(t, ownerAccess, identity)
	calling := humancalling.New(
		ownerPool,
		ownerAccess,
		httpCallingProvider{},
		humancalling.Config{CallControlID: "portal-polling-control"},
		func() time.Time { return now },
	)
	if err := calling.ReconcileCredentials(context.Background()); err != nil {
		t.Fatalf("reconcile portal polling credential: %v", err)
	}
	if processed, err := calling.ProcessNextCommand(context.Background()); err != nil || !processed {
		t.Fatalf("create portal polling credential: processed=%t err=%v", processed, err)
	}

	config, err := pgxpool.ParseConfig(os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("parse limited portal pool: %v", err)
	}
	config.MaxConns = poolMaximum
	config.MinConns = 0
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open limited portal pool: %v", err)
	}
	t.Cleanup(pool.Close)
	accessModule := access.New(pool, func() time.Time { return now })
	handler, err := newPortalHandler(
		t,
		httpapi.Config{AcquireTimeout: 150 * time.Millisecond},
		pool,
		accessModule,
		staticAuthenticator{"polling-token": identity},
	)
	if err != nil {
		t.Fatalf("create limited portal: %v", err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	held, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("hold portal connection: %v", err)
	}
	requests := []*http.Request{
		pollingRequest(t, http.MethodGet, server.URL+"/v1/calling/state", nil),
		pollingRequest(t, http.MethodPost, server.URL+"/v1/calling/softphone/lease",
			[]byte(`{"sessionId":"polling-session","takeover":false}`)),
	}
	statuses := make(chan int, len(requests))
	for _, request := range requests {
		go func(request *http.Request) {
			response, err := server.Client().Do(request)
			if err != nil {
				statuses <- 0
				return
			}
			_ = response.Body.Close()
			statuses <- response.StatusCode
		}(request)
	}
	result := []int{<-statuses, <-statuses}
	held.Release()
	sort.Ints(result)
	return result
}

func pollingRequest(t *testing.T, method string, target string, body []byte) *http.Request {
	t.Helper()
	request, err := http.NewRequest(method, target, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create polling request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer polling-token")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}
