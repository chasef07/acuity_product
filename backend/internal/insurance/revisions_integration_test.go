package insurance_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/insurance"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestQueryChecksEveryTableOfficeLocationAgainstOneAuthorization(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	south := []string{"hollywood", "sweetwater", "sweetwater-optical", "north-miami-beach-optical"}
	locations := []access.LocationProvision{{Key: "spring-hill", Name: "spring-hill", AbitaOfficeKeys: []string{"spring-hill"}}}
	for _, key := range south {
		locations = append(locations, access.LocationProvision{Key: key, Name: key, AbitaOfficeKeys: []string{key}})
	}
	if _, err := access.New(db.Owner, nil).Provision(ctx, access.Provisioning{
		Environment: "test", RequestedBy: "insurance-query-statements",
		Practices: []access.PracticeProvision{{
			Key: "abita", Name: "Synthetic Abita", Locations: locations,
			AccessGrants: []access.AccessGrantProvision{
				{Key: "south", Email: "south@abita.test", Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: south},
				{Key: "partial", Email: "partial@abita.test", Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: south[:3]},
			},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	statements := &statementCounter{}
	config := db.As(testdb.Portal).Config()
	config.ConnConfig.Tracer = statements
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	accessModule := access.New(pool, nil)
	southStaff := access.Identity{Subject: "south-subject", Email: "south@abita.test", EmailVerified: true}
	partialStaff := access.Identity{Subject: "partial-subject", Email: "partial@abita.test", EmailVerified: true}
	authorization := testaccess.Activate(t, accessModule, southStaff)
	testaccess.Activate(t, accessModule, partialStaff)
	var hollywood string
	for _, location := range authorization.Locations {
		if location.Name == "hollywood" {
			hollywood = location.ID
		}
	}
	middleware := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"valid":true}`)
	}))
	t.Cleanup(middleware.Close)
	client, err := insurance.NewMiddlewareClient(middleware.URL, "synthetic-middleware-secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	revisions, err := insurance.NewRevisions(pool, accessModule, client)
	if err != nil {
		t.Fatal(err)
	}
	export, err := os.ReadFile("testdata/export.json")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := insurance.ParseCatalog(export)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := revisions.Import(ctx, insurance.ImportCommand{
		PracticeID: authorization.Practice.ID, RevisionID: uuid.NewString(),
		Reason: "Seed", ActorSubject: "operator-subject", Catalog: catalog,
	}); err != nil {
		t.Fatal(err)
	}
	counts := []int64{}
	for _, tc := range []struct {
		identity access.Identity
		canEdit  bool
	}{{southStaff, true}, {partialStaff, false}} {
		statements.count.Store(0)
		view, err := revisions.Query(ctx, tc.identity, authorization.Practice.ID, hollywood)
		if err != nil {
			t.Fatal(err)
		}
		counts = append(counts, statements.count.Load())
		if view.OfficeID != "hollywood" || view.CanEdit != tc.canEdit {
			t.Fatalf("%s Query office=%s canEdit=%t, want hollywood and %t", tc.identity.Subject, view.OfficeID, view.CanEdit, tc.canEdit)
		}
	}
	if counts[0] != counts[1] {
		t.Fatalf("Query statements = %v, want the same count however many office Locations are checked", counts)
	}
}

type statementCounter struct {
	count atomic.Int64
}

func (counter *statementCounter) TraceQueryStart(
	ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData,
) context.Context {
	counter.count.Add(1)
	return ctx
}

func (*statementCounter) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}
