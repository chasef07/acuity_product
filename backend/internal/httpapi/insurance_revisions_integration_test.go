package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/insurance"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
	"github.com/google/uuid"
)

type fakeValidator struct {
	mu      sync.Mutex
	status  int
	body    string
	calls   int
	catalog insurance.Catalog
}

func (v *fakeValidator) respond(status int, body string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.status, v.body = status, body
}

func (v *fakeValidator) validated() (int, insurance.Catalog) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.calls, v.catalog
}

func (v *fakeValidator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer synthetic-middleware-secret" || r.Method != http.MethodPost || r.URL.Path != "/api/insurance/catalog/validate" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	body, _ := io.ReadAll(r.Body)
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls++
	v.catalog, _ = insurance.ParseCatalog(body)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(v.status)
	_, _ = io.WriteString(w, v.body)
}

type insurancePublication struct {
	PracticeID         string            `json:"practiceId"`
	LocationID         string            `json:"locationId"`
	RevisionID         string            `json:"revisionId"`
	ExpectedRevisionID string            `json:"expectedRevisionId"`
	Reason             string            `json:"reason"`
	Provenance         string            `json:"provenance,omitempty"`
	Catalog            insurance.Catalog `json:"catalog"`
}

type insuranceRevisionView struct {
	ID                 string    `json:"id"`
	CreatedAt          time.Time `json:"createdAt"`
	CreatedBy          string    `json:"createdBy"`
	Reason             string    `json:"reason"`
	Provenance         string    `json:"provenance"`
	PreviousRevisionID *string   `json:"previousRevisionId"`
}

func TestInsuranceListPublishesThroughTheMiddlewareValidatorToTheAgentRead(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	if _, err := access.New(db.Owner, nil).Provision(ctx, access.Provisioning{
		Environment:       "test",
		RequestedBy:       "insurance-revisions-http-test",
		PlatformOperators: []string{"operator@acuity.test"},
		Practices: []access.PracticeProvision{
			{
				Key:  "abita-eye-group",
				Name: "Synthetic Abita",
				Locations: []access.LocationProvision{
					{Key: "hollywood", Name: "Hollywood", AbitaOfficeKeys: []string{"hollywood"}},
					{Key: "sweetwater", Name: "Sweetwater", AbitaOfficeKeys: []string{"sweetwater"}},
					{Key: "sweetwater-optical", Name: "Sweetwater Optical", AbitaOfficeKeys: []string{"sweetwater-optical"}},
					{Key: "north-miami-beach-optical", Name: "North Miami Beach Optical", AbitaOfficeKeys: []string{"north-miami-beach-optical"}},
					{Key: "spring-hill", Name: "Spring Hill", AbitaOfficeKeys: []string{"spring-hill"}},
					{Key: "crystal-river", Name: "Crystal River", AbitaOfficeKeys: []string{"crystal-river"}},
				},
				AccessGrants: []access.AccessGrantProvision{
					{Key: "admin", Email: "admin@abita.test", Role: access.RoleAdmin, LocationScope: access.LocationScopeAll},
					{Key: "spring-hill", Email: "springhill@abita.test", Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"spring-hill"}},
					{Key: "north", Email: "north@abita.test", Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"spring-hill", "crystal-river"}},
					{Key: "south-but-optical", Email: "south@abita.test", Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"hollywood", "sweetwater", "north-miami-beach-optical"}},
					{Key: "south-but-sweetwater", Email: "south-optical@abita.test", Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"hollywood", "sweetwater-optical", "north-miami-beach-optical"}},
					{Key: "south", Email: "south-all@abita.test", Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"hollywood", "sweetwater", "sweetwater-optical", "north-miami-beach-optical"}},
				},
			},
			{
				Key:       "synthetic-demo",
				Name:      "Synthetic demo",
				Locations: []access.LocationProvision{{Key: "demo", Name: "Demo", AbitaOfficeKeys: []string{"demo-office"}}},
				AccessGrants: []access.AccessGrantProvision{
					{Key: "demo-admin", Email: "admin@demo.test", Role: access.RoleAdmin, LocationScope: access.LocationScopeAll},
				},
			},
		},
	}); err != nil {
		t.Fatalf("provision insurance fixture: %v", err)
	}
	runtime := db.As(testdb.Portal)
	accessModule := access.New(runtime, nil)
	identities := map[string]access.Identity{}
	for token, email := range map[string]string{"admin-token": "admin@abita.test", "spring-hill-token": "springhill@abita.test", "north-token": "north@abita.test", "south-token": "south@abita.test", "south-optical-token": "south-optical@abita.test", "south-all-token": "south-all@abita.test", "demo-token": "admin@demo.test", "operator-token": "operator@acuity.test"} {
		identities[token] = access.Identity{Subject: strings.TrimSuffix(token, "-token") + "-subject", Email: email, EmailVerified: true}
	}
	authorization := testaccess.Activate(t, accessModule, identities["admin-token"])
	for _, token := range []string{"spring-hill-token", "north-token", "south-token", "south-optical-token", "south-all-token", "demo-token"} {
		testaccess.Activate(t, accessModule, identities[token])
	}
	if _, err := accessModule.DiscoverActor(ctx, identities["operator-token"]); err != nil {
		t.Fatalf("activate operator: %v", err)
	}
	practice := authorization.Practice.ID
	locations := map[string]string{}
	for _, location := range authorization.Locations {
		locations[location.Name] = location.ID
	}

	validator := &fakeValidator{status: http.StatusOK, body: `{"valid":true}`}
	middleware := httptest.NewServer(validator)
	defer middleware.Close()
	client, err := insurance.NewMiddlewareClient(middleware.URL, "synthetic-middleware-secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	revisions, err := insurance.NewRevisions(runtime, accessModule, client)
	if err != nil {
		t.Fatal(err)
	}
	serviceAuthenticator, err := access.NewServiceAuthenticator(
		access.ServiceCredential{Token: "s2s-token", Identity: access.ServiceIdentity{Subject: "abita-eye-group", PracticeID: practice, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityCreateTask, access.ServiceCapabilityHumanHandoff, access.ServiceCapabilityIngestAIInteraction, access.ServiceCapabilityReadKnowledge}}},
		access.ServiceCredential{Token: "middleware-token", Identity: access.ServiceIdentity{Subject: "abita-eye-group-middleware", PracticeID: practice, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityReadInsurance}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	workModule := work.New(runtime, accessModule, nil)
	handler, err := httpapi.NewPortal(httpapi.Config{AcquireTimeout: time.Second, RequestTimeout: 10 * time.Second}, runtime, httpapi.PortalDependencies{
		Access:               accessModule,
		Authenticator:        staticAuthenticator(identities),
		Calling:              humancalling.New(runtime, accessModule, httpCallingProvider{}, humancalling.Config{}, nil),
		Interactions:         interaction.New(runtime, accessModule, nil),
		Messaging:            messaging.New(runtime, accessModule, workModule, nil, messaging.Config{}, nil),
		Work:                 workModule,
		Workspace:            workspace.New(runtime, accessModule),
		InsuranceRevisions:   revisions,
		ServiceAuthenticator: serviceAuthenticator,
	})
	if err != nil {
		t.Fatalf("create portal: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	send := func(path, token string, payload any) (int, string) {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		response := request(t, server.Client(), http.MethodPost, server.URL+path, token, body)
		defer response.Body.Close()
		return response.StatusCode, readBody(t, response)
	}
	agentRead := func(token string) (int, string, json.RawMessage) {
		t.Helper()
		status, body := send("/v1/agent/insurance/read", token, map[string]string{})
		var result struct {
			RevisionID string          `json:"revisionId"`
			Catalog    json.RawMessage `json:"catalog"`
		}
		if status == http.StatusOK {
			if err := json.Unmarshal([]byte(body), &result); err != nil {
				t.Fatal(err)
			}
		}
		return status, result.RevisionID, result.Catalog
	}
	active := func() (string, int) {
		t.Helper()
		var revision string
		var count int
		if err := db.Owner.QueryRow(ctx, `SELECT COALESCE((SELECT revision_id::text FROM insurance_catalogs WHERE practice_id=$1), ''), (SELECT count(*) FROM insurance_revisions WHERE practice_id=$1)`, practice).Scan(&revision, &count); err != nil {
			t.Fatal(err)
		}
		return revision, count
	}

	if status, _, _ := agentRead("middleware-token"); status != http.StatusServiceUnavailable {
		t.Fatalf("agent read before any list: status=%d", status)
	}

	export, err := os.ReadFile("../insurance/testdata/export.json")
	if err != nil {
		t.Fatal(err)
	}
	seedCatalog, err := insurance.ParseCatalog(export)
	if err != nil {
		t.Fatal(err)
	}
	seed, published, err := revisions.Import(ctx, insurance.ImportCommand{PracticeID: practice, RevisionID: uuid.NewString(), Reason: "Seed from the middleware export", ActorSubject: "operator-subject", Catalog: seedCatalog})
	if err != nil || !published || seed.Provenance != insurance.ProvenanceImport || seed.CreatedBy != "operator@acuity.test" || seed.PreviousRevisionID != nil {
		t.Fatalf("seed: %+v published=%t err=%v", seed, published, err)
	}

	edited := func(change func(*insurance.Catalog)) insurance.Catalog {
		t.Helper()
		catalog, err := insurance.ParseCatalog(export)
		if err != nil {
			t.Fatal(err)
		}
		change(&catalog)
		return catalog
	}
	region := func(c *insurance.Catalog, id string) *insurance.Region {
		for i := range c.Regions {
			if c.Regions[i].ID == id {
				return &c.Regions[i]
			}
		}
		t.Fatalf("no region %s", id)
		return nil
	}
	table := func(c *insurance.Catalog, regionID, id string) *insurance.OfficeTable {
		r := region(c, regionID)
		for i := range r.Tables {
			if r.Tables[i].ID == id {
				return &r.Tables[i]
			}
		}
		t.Fatalf("no table %s", id)
		return nil
	}
	springHillRow := edited(func(c *insurance.Catalog) {
		table(c, "spring_hill_crystal_river", "spring_hill").Rows[0].Notice = "Synthetic notice for Spring Hill"
	})
	sharedNorthPlan := edited(func(c *insurance.Catalog) {
		region(c, "spring_hill_crystal_river").Plans[0].Label = "Synthetic renamed plan"
	})
	publication := func(location string, expected string, catalog insurance.Catalog) insurancePublication {
		return insurancePublication{PracticeID: practice, LocationID: locations[location], RevisionID: uuid.NewString(), ExpectedRevisionID: expected, Reason: "Synthetic correction", Catalog: catalog}
	}
	unchanged := func(t *testing.T, wantActive string, wantCount int) {
		t.Helper()
		if got, count := active(); got != wantActive || count != wantCount {
			t.Fatalf("active=%s count=%d, want %s and %d", got, count, wantActive, wantCount)
		}
	}

	t.Run("the agent reads the seeded export byte for byte", func(t *testing.T) {
		status, revisionID, catalog := agentRead("middleware-token")
		if status != http.StatusOK || revisionID != seed.ID || !bytes.Equal(catalog, bytes.TrimSuffix(export, []byte("\n"))) {
			t.Fatalf("agent read: status=%d revision=%s (seed %s) bytes=%d want %d", status, revisionID, seed.ID, len(catalog), len(export)-1)
		}
	})

	t.Run("the agent read needs the middleware credential", func(t *testing.T) {
		for _, tc := range []struct {
			token  string
			status int
		}{{"s2s-token", http.StatusForbidden}, {"", http.StatusUnauthorized}, {"wrong-token", http.StatusUnauthorized}, {"admin-token", http.StatusUnauthorized}} {
			if status, _, _ := agentRead(tc.token); status != tc.status {
				t.Errorf("token %q: status=%d, want %d", tc.token, status, tc.status)
			}
		}
	})

	t.Run("staff read the list with their Location's office, every office's Location names, and whether they can edit its table", func(t *testing.T) {
		officeNames := map[string]string{
			"hollywood":                 "Hollywood",
			"sweetwater":                "Sweetwater / Sweetwater Optical",
			"north_miami_beach_optical": "North Miami Beach Optical",
			"spring_hill":               "Spring Hill",
			"crystal_river":             "Crystal River",
		}
		for _, tc := range []struct {
			token, location, office string
			canEdit                 bool
		}{
			{"spring-hill-token", "Spring Hill", "spring_hill", true},
			{"north-token", "Crystal River", "crystal_river", true},
			{"south-token", "Hollywood", "hollywood", false},
			{"admin-token", "Sweetwater Optical", "sweetwater", true},
			{"operator-token", "North Miami Beach Optical", "north_miami_beach_optical", true},
		} {
			status, body := send("/v1/insurance/revisions/query", tc.token, map[string]string{"practiceId": practice, "locationId": locations[tc.location]})
			var view struct {
				Revision    insuranceRevisionView `json:"revision"`
				Catalog     insurance.Catalog     `json:"catalog"`
				OfficeID    string                `json:"officeId"`
				OfficeNames map[string]string     `json:"officeNames"`
				CanEdit     bool                  `json:"canEdit"`
			}
			if status != http.StatusOK || json.Unmarshal([]byte(body), &view) != nil {
				t.Fatalf("%s at %s: status=%d body=%.300s", tc.token, tc.location, status, body)
			}
			if view.Revision.ID != seed.ID || view.Revision.Provenance != "import" || view.Revision.CreatedBy != "operator@acuity.test" || view.OfficeID != tc.office || view.CanEdit != tc.canEdit || !reflect.DeepEqual(view.Catalog, seedCatalog) {
				t.Fatalf("%s at %s: revision=%+v office=%s canEdit=%t", tc.token, tc.location, view.Revision, view.OfficeID, view.CanEdit)
			}
			if !reflect.DeepEqual(view.OfficeNames, officeNames) {
				t.Fatalf("%s at %s: office names=%v", tc.token, tc.location, view.OfficeNames)
			}
		}
		if status, _ := send("/v1/insurance/revisions/query", "spring-hill-token", map[string]string{"practiceId": practice, "locationId": locations["Crystal River"]}); status != http.StatusForbidden {
			t.Fatalf("query outside the member's Locations: status=%d", status)
		}
	})

	t.Run("a Spring Hill member cannot change a plan row Crystal River shares", func(t *testing.T) {
		before, _ := validator.validated()
		status, body := send("/v1/insurance/revisions", "spring-hill-token", publication("Spring Hill", seed.ID, sharedNorthPlan))
		if status != http.StatusForbidden || !strings.Contains(body, "ACCESS_DENIED") {
			t.Fatalf("status=%d body=%s", status, body)
		}
		if after, _ := validator.validated(); after != before {
			t.Fatal("a denied publish reached the middleware validator")
		}
		unchanged(t, seed.ID, 1)
	})

	t.Run("South Florida staff without Sweetwater Optical cannot change the shared doctors table", func(t *testing.T) {
		catalog := edited(func(c *insurance.Catalog) { table(c, "south_florida", "doctors").Rows[0].Notice = "Synthetic notice" })
		if status, body := send("/v1/insurance/revisions", "south-token", publication("Hollywood", seed.ID, catalog)); status != http.StatusForbidden {
			t.Fatalf("status=%d body=%s", status, body)
		}
		unchanged(t, seed.ID, 1)
	})

	t.Run("a member of another Practice is denied", func(t *testing.T) {
		body := publication("Spring Hill", seed.ID, springHillRow)
		if status, _ := send("/v1/insurance/revisions", "demo-token", body); status != http.StatusForbidden {
			t.Fatalf("status=%d", status)
		}
		if status, _ := send("/v1/insurance/revisions", "", body); status != http.StatusUnauthorized {
			t.Fatalf("unauthenticated status=%d", status)
		}
		unchanged(t, seed.ID, 1)
	})

	t.Run("the middleware's rejection comes back verbatim and nothing changes", func(t *testing.T) {
		validator.respond(http.StatusBadRequest, `{"valid":false,"errors":[{"region":"spring_hill_crystal_river","table":"spring_hill","planId":"aetna-better-health","rule":"unknown_doctor_column","message":"Synthetic doctor is not a column."},{"region":"","table":"","planId":"","rule":"unknown_carrier","message":"Synthetic carrier is unknown."}]}`)
		defer validator.respond(http.StatusOK, `{"valid":true}`)
		status, body := send("/v1/insurance/revisions", "spring-hill-token", publication("Spring Hill", seed.ID, springHillRow))
		var envelope struct {
			Error struct {
				Code   string                   `json:"code"`
				Errors []insurance.CatalogError `json:"errors"`
			} `json:"error"`
		}
		if status != http.StatusBadRequest || json.Unmarshal([]byte(body), &envelope) != nil || envelope.Error.Code != "INSURANCE_INVALID" {
			t.Fatalf("status=%d body=%s", status, body)
		}
		want := []insurance.CatalogError{
			{Region: "spring_hill_crystal_river", Table: "spring_hill", PlanID: "aetna-better-health", Rule: "unknown_doctor_column", Message: "Synthetic doctor is not a column."},
			{Rule: "unknown_carrier", Message: "Synthetic carrier is unknown."},
		}
		if !reflect.DeepEqual(envelope.Error.Errors, want) {
			t.Fatalf("errors=%+v", envelope.Error.Errors)
		}
		if _, sent := validator.validated(); !reflect.DeepEqual(sent, springHillRow) {
			t.Fatal("the validator did not receive the published catalog")
		}
		unchanged(t, seed.ID, 1)
	})

	t.Run("with the middleware failing or down, publish is unavailable and nothing changes", func(t *testing.T) {
		validator.respond(http.StatusInternalServerError, `{}`)
		status, body := send("/v1/insurance/revisions", "spring-hill-token", publication("Spring Hill", seed.ID, springHillRow))
		validator.respond(http.StatusOK, `{"valid":true}`)
		if status != http.StatusServiceUnavailable || !strings.Contains(body, `"retryable":true`) {
			t.Fatalf("failing middleware: status=%d body=%s", status, body)
		}
		down, err := insurance.NewMiddlewareClient("http://127.0.0.1:1", "synthetic-middleware-secret", nil)
		if err != nil {
			t.Fatal(err)
		}
		unreachable, err := insurance.NewRevisions(runtime, accessModule, down)
		if err != nil {
			t.Fatal(err)
		}
		downHandler, err := httpapi.NewPortal(httpapi.Config{AcquireTimeout: time.Second, RequestTimeout: 10 * time.Second}, runtime, httpapi.PortalDependencies{
			Access: accessModule, Authenticator: staticAuthenticator(identities),
			Calling:      humancalling.New(runtime, accessModule, httpCallingProvider{}, humancalling.Config{}, nil),
			Interactions: interaction.New(runtime, accessModule, nil), Messaging: messaging.New(runtime, accessModule, workModule, nil, messaging.Config{}, nil),
			Work: workModule, Workspace: workspace.New(runtime, accessModule), InsuranceRevisions: unreachable, ServiceAuthenticator: serviceAuthenticator,
		})
		if err != nil {
			t.Fatal(err)
		}
		downServer := httptest.NewServer(downHandler)
		defer downServer.Close()
		payload, _ := json.Marshal(publication("Spring Hill", seed.ID, springHillRow))
		response := request(t, downServer.Client(), http.MethodPost, downServer.URL+"/v1/insurance/revisions", "spring-hill-token", payload)
		defer response.Body.Close()
		if response.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("unreachable middleware: status=%d body=%s", response.StatusCode, readBody(t, response))
		}
		unchanged(t, seed.ID, 1)
	})

	first := publication("Spring Hill", seed.ID, springHillRow)
	t.Run("a member with access to every touched Location publishes and the agent reads it", func(t *testing.T) {
		status, body := send("/v1/insurance/revisions", "spring-hill-token", first)
		var revision insuranceRevisionView
		if status != http.StatusOK || json.Unmarshal([]byte(body), &revision) != nil {
			t.Fatalf("status=%d body=%s", status, body)
		}
		if revision.ID != first.RevisionID || revision.CreatedBy != "springhill@abita.test" || revision.Provenance != "portal" || revision.Reason != first.Reason || revision.PreviousRevisionID == nil || *revision.PreviousRevisionID != seed.ID {
			t.Fatalf("revision=%+v", revision)
		}
		unchanged(t, first.RevisionID, 2)
		var createdBy, provenance, reason string
		if err := db.Owner.QueryRow(ctx, `SELECT created_by, provenance, reason FROM insurance_revisions WHERE id=$1`, first.RevisionID).Scan(&createdBy, &provenance, &reason); err != nil {
			t.Fatal(err)
		}
		if createdBy != "spring-hill-subject" || provenance != "portal" || reason != first.Reason {
			t.Fatalf("stored attribution=%s %s %s", createdBy, provenance, reason)
		}
		status, revisionID, catalog := agentRead("middleware-token")
		want, _ := json.Marshal(springHillRow)
		if status != http.StatusOK || revisionID != first.RevisionID || !bytes.Equal(catalog, want) {
			t.Fatalf("agent read after publish: status=%d revision=%s", status, revisionID)
		}
	})

	t.Run("repeating a revision ID returns the same revision without a duplicate", func(t *testing.T) {
		before, _ := validator.validated()
		status, body := send("/v1/insurance/revisions", "spring-hill-token", first)
		if status != http.StatusOK || !strings.Contains(body, `"id":"`+first.RevisionID+`"`) {
			t.Fatalf("status=%d body=%s", status, body)
		}
		if after, _ := validator.validated(); after != before {
			t.Fatal("a replay was validated again")
		}
		unchanged(t, first.RevisionID, 2)
		reused := first
		reused.Reason = "A different reason"
		if status, _ := send("/v1/insurance/revisions", "spring-hill-token", reused); status != http.StatusConflict {
			t.Fatalf("revision ID reused for a different publish: status=%d", status)
		}
	})

	t.Run("a stale expected revision conflicts", func(t *testing.T) {
		status, body := send("/v1/insurance/revisions", "north-token", publication("Crystal River", seed.ID, sharedNorthPlan))
		if status != http.StatusConflict || !strings.Contains(body, "INSURANCE_CHANGED") {
			t.Fatalf("status=%d body=%s", status, body)
		}
		unchanged(t, first.RevisionID, 2)
	})

	t.Run("a member with both northern Locations changes their shared plan", func(t *testing.T) {
		catalog := edited(func(c *insurance.Catalog) {
			table(c, "spring_hill_crystal_river", "spring_hill").Rows[0].Notice = "Synthetic notice for Spring Hill"
			region(c, "spring_hill_crystal_river").Plans[0].Label = "Synthetic renamed plan"
		})
		body := publication("Crystal River", first.RevisionID, catalog)
		if status, response := send("/v1/insurance/revisions", "north-token", body); status != http.StatusOK {
			t.Fatalf("status=%d body=%s", status, response)
		}
		unchanged(t, body.RevisionID, 3)
		first = body
	})

	t.Run("an office with no Location needs a Platform Operator", func(t *testing.T) {
		current, err := revisions.Read(ctx, access.ServiceIdentity{Subject: "abita-eye-group-middleware", PracticeID: practice, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityReadInsurance}})
		if err != nil {
			t.Fatal(err)
		}
		catalog := current.Catalog
		north := &catalog.Regions[1]
		north.Tables = append(append([]insurance.OfficeTable(nil), north.Tables...), insurance.OfficeTable{ID: "inverness", Offices: []string{"inverness"}, Doctors: []string{}, Rows: []insurance.TableRow{}})
		if status, _ := send("/v1/insurance/revisions", "admin-token", publication("Spring Hill", first.RevisionID, catalog)); status != http.StatusForbidden {
			t.Fatalf("practice admin: status=%d", status)
		}
		operator := publication("Spring Hill", first.RevisionID, catalog)
		if status, body := send("/v1/insurance/revisions", "operator-token", operator); status != http.StatusOK || !strings.Contains(body, `"createdBy":"operator@acuity.test"`) {
			t.Fatalf("operator: status=%d body=%s", status, body)
		}
		unchanged(t, operator.RevisionID, 4)
	})

	t.Run("invalid publications are rejected before anything runs", func(t *testing.T) {
		current, _ := active()
		for name, change := range map[string]func(*insurancePublication){
			"empty reason":   func(p *insurancePublication) { p.Reason = "  " },
			"long reason":    func(p *insurancePublication) { p.Reason = strings.Repeat("r", 2001) },
			"nil revision":   func(p *insurancePublication) { p.RevisionID = uuid.Nil.String() },
			"no expectation": func(p *insurancePublication) { p.ExpectedRevisionID = "" },
		} {
			body := publication("Spring Hill", current, springHillRow)
			change(&body)
			if status, response := send("/v1/insurance/revisions", "spring-hill-token", body); status != http.StatusBadRequest {
				t.Errorf("%s: status=%d body=%s", name, status, response)
			}
		}
		raw := `{"practiceId":"` + practice + `","locationId":"` + locations["Spring Hill"] + `","revisionId":"` + uuid.NewString() + `","expectedRevisionId":"` + current + `","reason":"x","catalog":{"carriers":[],"regions":[],"offices":[]}}`
		response := request(t, server.Client(), http.MethodPost, server.URL+"/v1/insurance/revisions", "spring-hill-token", []byte(raw))
		defer response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("unknown catalog field: status=%d", response.StatusCode)
		}
		unchanged(t, current, 4)
	})

	t.Run("the seed import refuses to overwrite Portal edits", func(t *testing.T) {
		current, count := active()
		if _, published, err := revisions.Import(ctx, insurance.ImportCommand{PracticeID: practice, RevisionID: uuid.NewString(), Reason: "Seed again", ActorSubject: "operator-subject", Catalog: seedCatalog}); err == nil || published {
			t.Fatalf("import over Portal edits: published=%t err=%v", published, err)
		}
		unchanged(t, current, count)
	})

	history := func(t *testing.T, token, location string) []insuranceRevisionView {
		t.Helper()
		status, body := send("/v1/insurance/revisions/history", token, map[string]string{"practiceId": practice, "locationId": locations[location]})
		var view struct {
			Revisions []insuranceRevisionView `json:"revisions"`
		}
		if status != http.StatusOK || json.Unmarshal([]byte(body), &view) != nil {
			t.Fatalf("history for %s at %s: status=%d body=%.300s", token, location, status, body)
		}
		return view.Revisions
	}
	readRevision := func(t *testing.T, token, location, revisionID string) (int, insuranceRevisionView, insurance.Catalog) {
		t.Helper()
		status, body := send("/v1/insurance/revisions/read", token, map[string]string{"practiceId": practice, "locationId": locations[location], "revisionId": revisionID})
		var view struct {
			Revision insuranceRevisionView `json:"revision"`
			Catalog  insurance.Catalog     `json:"catalog"`
		}
		if status == http.StatusOK && json.Unmarshal([]byte(body), &view) != nil {
			t.Fatalf("read %s: body=%.300s", revisionID, body)
		}
		return status, view.Revision, view.Catalog
	}
	type storedRow struct{ id, hash, reason, provenance, createdBy, previous string }
	storedRevisions := func(t *testing.T) []storedRow {
		t.Helper()
		rows, err := db.Owner.Query(ctx, `SELECT id::text, content_hash, reason, provenance, created_by, COALESCE(previous_revision_id::text, '') FROM insurance_revisions WHERE practice_id=$1 ORDER BY created_at, id`, practice)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		stored := []storedRow{}
		for rows.Next() {
			var row storedRow
			if err := rows.Scan(&row.id, &row.hash, &row.reason, &row.provenance, &row.createdBy, &row.previous); err != nil {
				t.Fatal(err)
			}
			stored = append(stored, row)
		}
		return stored
	}

	t.Run("history lists every revision newest first with author, time, reason and provenance", func(t *testing.T) {
		current, count := active()
		revisions := history(t, "spring-hill-token", "Spring Hill")
		if len(revisions) != count || revisions[0].ID != current {
			t.Fatalf("history has %d revisions starting at %+v, want %d starting at %s", len(revisions), revisions[0], count, current)
		}
		for i, revision := range revisions[:len(revisions)-1] {
			older := revisions[i+1]
			if revision.PreviousRevisionID == nil || *revision.PreviousRevisionID != older.ID || !revision.CreatedAt.After(older.CreatedAt) {
				t.Fatalf("revision %d %+v does not follow %+v", i, revision, older)
			}
		}
		authors, provenances := []string{}, []string{}
		for _, revision := range revisions {
			authors = append(authors, revision.CreatedBy)
			provenances = append(provenances, revision.Provenance)
		}
		if want := []string{"operator@acuity.test", "north@abita.test", "springhill@abita.test", "operator@acuity.test"}; !reflect.DeepEqual(authors, want) {
			t.Fatalf("authors=%v, want %v", authors, want)
		}
		if want := []string{"portal", "portal", "portal", "import"}; !reflect.DeepEqual(provenances, want) {
			t.Fatalf("provenances=%v, want %v", provenances, want)
		}
		if oldest := revisions[len(revisions)-1]; oldest.ID != seed.ID || oldest.Reason != "Seed from the middleware export" || oldest.PreviousRevisionID != nil || revisions[1].Reason != "Synthetic correction" {
			t.Fatalf("oldest=%+v second=%+v", oldest, revisions[1])
		}
		if got := history(t, "north-token", "Crystal River"); !reflect.DeepEqual(got, revisions) {
			t.Fatal("history differs between two Locations of the Practice")
		}
	})

	t.Run("a revision reads back its catalog and an unknown revision is not found", func(t *testing.T) {
		status, revision, catalog := readRevision(t, "spring-hill-token", "Spring Hill", seed.ID)
		if status != http.StatusOK || revision.ID != seed.ID || revision.Provenance != "import" || revision.CreatedBy != "operator@acuity.test" || !reflect.DeepEqual(catalog, seedCatalog) {
			t.Fatalf("read seed: status=%d revision=%+v", status, revision)
		}
		if status, _, _ := readRevision(t, "spring-hill-token", "Spring Hill", uuid.NewString()); status != http.StatusNotFound {
			t.Fatalf("unknown revision: status=%d", status)
		}
		if status, _, _ := readRevision(t, "spring-hill-token", "Spring Hill", "not-a-uuid"); status != http.StatusBadRequest {
			t.Fatalf("malformed revision ID: status=%d", status)
		}
	})

	t.Run("members without access to the Location get 403 from history and read", func(t *testing.T) {
		for _, tc := range []struct{ token, location string }{{"spring-hill-token", "Crystal River"}, {"demo-token", "Spring Hill"}} {
			if status, _ := send("/v1/insurance/revisions/history", tc.token, map[string]string{"practiceId": practice, "locationId": locations[tc.location]}); status != http.StatusForbidden {
				t.Errorf("history for %s at %s: status=%d", tc.token, tc.location, status)
			}
			if status, _, _ := readRevision(t, tc.token, tc.location, seed.ID); status != http.StatusForbidden {
				t.Errorf("read for %s at %s: status=%d", tc.token, tc.location, status)
			}
		}
		if status, _ := send("/v1/insurance/revisions/history", "", map[string]string{"practiceId": practice, "locationId": locations["Spring Hill"]}); status != http.StatusUnauthorized {
			t.Errorf("unauthenticated history: status=%d", status)
		}
	})

	restore := func(location, expected string) insurancePublication {
		body := publication(location, expected, seedCatalog)
		body.Provenance = "restore"
		body.Reason = "Restore " + seed.ID[:8]
		return body
	}

	t.Run("clients cannot record an import or an unknown provenance", func(t *testing.T) {
		current, count := active()
		for _, provenance := range []string{"import", "git"} {
			body := restore("Spring Hill", current)
			body.Provenance = provenance
			if status, response := send("/v1/insurance/revisions", "operator-token", body); status != http.StatusBadRequest {
				t.Errorf("provenance %s: status=%d body=%s", provenance, status, response)
			}
		}
		unchanged(t, current, count)
	})

	t.Run("a restore touching an office the member cannot change is denied and nothing changes", func(t *testing.T) {
		current, count := active()
		before := storedRevisions(t)
		if status, body := send("/v1/insurance/revisions", "admin-token", restore("Spring Hill", current)); status != http.StatusForbidden || !strings.Contains(body, "ACCESS_DENIED") {
			t.Fatalf("status=%d body=%s", status, body)
		}
		unchanged(t, current, count)
		if !reflect.DeepEqual(storedRevisions(t), before) {
			t.Fatal("a denied restore changed stored revisions")
		}
	})

	t.Run("a restore the middleware rejects returns its errors and changes nothing", func(t *testing.T) {
		current, count := active()
		before := storedRevisions(t)
		validator.respond(http.StatusBadRequest, `{"valid":false,"errors":[{"region":"spring_hill_crystal_river","table":"spring_hill","planId":"","rule":"unknown_doctor_column","message":"Synthetic doctor column no longer exists."}]}`)
		status, body := send("/v1/insurance/revisions", "operator-token", restore("Spring Hill", current))
		validator.respond(http.StatusOK, `{"valid":true}`)
		if status != http.StatusBadRequest || !strings.Contains(body, "INSURANCE_INVALID") || !strings.Contains(body, "Synthetic doctor column no longer exists.") {
			t.Fatalf("status=%d body=%s", status, body)
		}
		if _, sent := validator.validated(); !reflect.DeepEqual(sent, seedCatalog) {
			t.Fatal("the restore was not validated with the restored catalog")
		}
		unchanged(t, current, count)
		if !reflect.DeepEqual(storedRevisions(t), before) {
			t.Fatal("a rejected restore changed stored revisions")
		}
	})

	t.Run("restoring a revision publishes its catalog as a new restore revision and leaves history unchanged", func(t *testing.T) {
		current, count := active()
		before := storedRevisions(t)
		body := restore("Spring Hill", current)
		status, response := send("/v1/insurance/revisions", "operator-token", body)
		var revision insuranceRevisionView
		if status != http.StatusOK || json.Unmarshal([]byte(response), &revision) != nil {
			t.Fatalf("status=%d body=%s", status, response)
		}
		if revision.ID != body.RevisionID || revision.Provenance != "restore" || revision.Reason != "Restore "+seed.ID[:8] || revision.CreatedBy != "operator@acuity.test" || revision.PreviousRevisionID == nil || *revision.PreviousRevisionID != current {
			t.Fatalf("restore revision=%+v", revision)
		}
		unchanged(t, body.RevisionID, count+1)
		after := storedRevisions(t)
		if !reflect.DeepEqual(after[:len(before)], before) {
			t.Fatal("restoring rewrote an earlier revision")
		}
		if restored := after[len(after)-1]; restored.id != body.RevisionID || restored.hash != before[0].hash {
			t.Fatalf("restored row=%+v, want the seed's content hash %s", restored, before[0].hash)
		}
		if _, read, catalog := readRevision(t, "spring-hill-token", "Spring Hill", body.RevisionID); read.Provenance != "restore" || !reflect.DeepEqual(catalog, seedCatalog) {
			t.Fatalf("read restored revision=%+v", read)
		}
		status, revisionID, catalog := agentRead("middleware-token")
		if status != http.StatusOK || revisionID != body.RevisionID || !bytes.Equal(catalog, bytes.TrimSuffix(export, []byte("\n"))) {
			t.Fatalf("agent read after restore: status=%d revision=%s", status, revisionID)
		}
		if newest := history(t, "spring-hill-token", "Spring Hill")[0]; newest.ID != body.RevisionID || newest.Provenance != "restore" {
			t.Fatalf("newest history entry=%+v", newest)
		}
	})

	t.Run("both Sweetwater Locations guard the one Sweetwater office", func(t *testing.T) {
		current, count := active()
		for _, tc := range []struct {
			token, location string
			canEdit         bool
			status          int
		}{
			{"south-token", "Sweetwater", false, http.StatusForbidden},
			{"south-optical-token", "Sweetwater Optical", false, http.StatusForbidden},
			{"south-all-token", "Sweetwater", true, http.StatusOK},
			{"south-all-token", "Sweetwater Optical", true, http.StatusOK},
		} {
			status, body := send("/v1/insurance/revisions/query", tc.token, map[string]string{"practiceId": practice, "locationId": locations[tc.location]})
			var view struct {
				Catalog  insurance.Catalog `json:"catalog"`
				OfficeID string            `json:"officeId"`
				CanEdit  bool              `json:"canEdit"`
			}
			if status != http.StatusOK || json.Unmarshal([]byte(body), &view) != nil || view.OfficeID != "sweetwater" || view.CanEdit != tc.canEdit {
				t.Fatalf("%s queries %s: status=%d office=%s canEdit=%t, want canEdit %t", tc.token, tc.location, status, view.OfficeID, view.CanEdit, tc.canEdit)
			}
			catalog := view.Catalog
			row := &table(&catalog, "south_florida", "doctors").Rows[5]
			if !slices.Contains(row.OnlyOffices, "sweetwater") {
				t.Fatalf("row %s is not a Sweetwater row", row.PlanID)
			}
			row.Notice = "Synthetic Sweetwater notice from " + tc.token + " at " + tc.location
			publish := publication(tc.location, current, catalog)
			status, body = send("/v1/insurance/revisions", tc.token, publish)
			if status != tc.status {
				t.Fatalf("%s publishes at %s: status=%d body=%s, want %d", tc.token, tc.location, status, body, tc.status)
			}
			if status == http.StatusOK {
				current, count = publish.RevisionID, count+1
			}
			unchanged(t, current, count)
		}
	})

	t.Run("a Spring Hill member removes a plan only Spring Hill lists but cannot rename one Crystal River shares", func(t *testing.T) {
		current, count := active()
		status, body := send("/v1/insurance/revisions/query", "spring-hill-token", map[string]string{"practiceId": practice, "locationId": locations["Spring Hill"]})
		var view struct {
			Catalog insurance.Catalog `json:"catalog"`
		}
		if status != http.StatusOK || json.Unmarshal([]byte(body), &view) != nil {
			t.Fatalf("query: status=%d body=%.300s", status, body)
		}
		removed := view.Catalog
		north := region(&removed, "spring_hill_crystal_river")
		north.Plans = slices.DeleteFunc(slices.Clone(north.Plans), func(p insurance.RegionPlan) bool { return p.ID == "simply-health" })
		springHill := table(&removed, "spring_hill_crystal_river", "spring_hill")
		springHill.Rows = slices.DeleteFunc(slices.Clone(springHill.Rows), func(r insurance.TableRow) bool { return r.PlanID == "simply-health" })
		if slices.ContainsFunc(table(&removed, "spring_hill_crystal_river", "crystal_river").Rows, func(r insurance.TableRow) bool { return r.PlanID == "simply-health" }) {
			t.Fatal("simply-health is not a Spring Hill-only plan")
		}
		removal := publication("Spring Hill", current, removed)
		if status, body := send("/v1/insurance/revisions", "spring-hill-token", removal); status != http.StatusOK {
			t.Fatalf("removing a Spring Hill-only plan: status=%d body=%s", status, body)
		}
		unchanged(t, removal.RevisionID, count+1)

		renamed := removed
		renamed.Regions = slices.Clone(removed.Regions)
		north = region(&renamed, "spring_hill_crystal_river")
		north.Plans = slices.Clone(north.Plans)
		north.Plans[0].Label = "Synthetic renamed shared plan"
		status, body = send("/v1/insurance/revisions", "spring-hill-token", publication("Spring Hill", removal.RevisionID, renamed))
		if status != http.StatusForbidden || !strings.Contains(body, "ACCESS_DENIED") {
			t.Fatalf("renaming a plan Crystal River shares: status=%d body=%s", status, body)
		}
		unchanged(t, removal.RevisionID, count+1)
	})
}
