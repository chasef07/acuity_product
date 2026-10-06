package knowledge

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/google/uuid"
)

func TestReadLocationReturnsTheCurrentRevisionForAnAuthorizedLocation(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	a := access.New(pool, time.Now)
	if _, err := a.Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "test", Practices: []access.PracticeProvision{{
		Key:  "knowledge-read-test",
		Name: "Synthetic practice",
		Locations: []access.LocationProvision{
			{Key: "alpha", Name: "Alpha", AbitaOfficeKeys: []string{"office-alpha"}},
			{Key: "beta", Name: "Beta", AbitaOfficeKeys: []string{"office-beta"}},
			{Key: "unrouted", Name: "Unrouted"},
			{Key: "combined", Name: "Combined", AbitaOfficeKeys: []string{"office-c", "office-d"}},
		},
		AccessGrants: []access.AccessGrantProvision{
			{Key: "admin", Email: "admin@example.com", Role: access.RoleAdmin, LocationScope: access.LocationScopeAll},
			{Key: "staff", Email: "staff@example.com", Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"alpha"}},
		},
	}}}); err != nil {
		t.Fatal(err)
	}
	admin := access.Identity{Subject: "admin", Email: "admin@example.com", EmailVerified: true}
	staff := access.Identity{Subject: "staff", Email: "staff@example.com", EmailVerified: true}
	authorization := testaccess.Activate(t, a, admin)
	testaccess.Activate(t, a, staff)
	locations := map[string]string{}
	for _, location := range authorization.Locations {
		locations[location.Name] = location.ID
	}
	practiceID := authorization.Practice.ID
	m, err := New(pool, a, &testEmbedder{}, Config{})
	if err != nil {
		t.Fatal(err)
	}

	empty, err := m.ReadLocation(ctx, staff, practiceID, locations["Alpha"])
	if err != nil || empty.Revision != nil || empty.Sections == nil || len(empty.Sections) != 0 || empty.LocationID != locations["Alpha"] {
		t.Fatalf("no corpus = %+v, %v", empty, err)
	}

	sections := []Section{
		{ID: "parking", Title: "Parking", Text: "Synthetic garage.\nLevel two."},
		{ID: "hours", Title: "Office hours", Text: "The synthetic office closes at six."},
		{ID: "billing", Title: "Billing", Text: "Synthetic billing questions go to the front desk."},
	}
	firstCommand := ImportCommand{ID: uuid.NewString(), PracticeID: practiceID, OfficeKey: "office-alpha", Sections: sections, Provenance: "approved synthetic fixture", Reason: "initial import", ActorSubject: "operator@example.com"}
	first, err := m.ReplaceCorpus(ctx, firstCommand)
	if err != nil {
		t.Fatal(err)
	}
	betaSections := []Section{{ID: "hours", Title: "Beta hours", Text: "Beta closes at five."}, {ID: "fax", Title: "Beta fax", Text: "Beta has no fax."}}
	beta, err := m.ReplaceCorpus(ctx, ImportCommand{ID: uuid.NewString(), PracticeID: practiceID, OfficeKey: "office-beta", Sections: betaSections, Provenance: "approved synthetic fixture", Reason: "initial import", ActorSubject: "operator@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE knowledge_passages SET position = NULL WHERE revision_id = $1`, beta.ID); err != nil {
		t.Fatal(err)
	}
	got, err := m.ReadLocation(ctx, staff, practiceID, locations["Alpha"])
	if err != nil || got.Revision == nil || got.Revision.ID != first.ID || !got.Revision.CreatedAt.Equal(first.CreatedAt) || !reflect.DeepEqual(got.Sections, sections) {
		t.Fatalf("first revision = %+v, %v", got, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE knowledge_passages SET position = NULL WHERE revision_id = $1`, first.ID); err != nil {
		t.Fatal(err)
	}
	replayed, err := m.ReplaceCorpus(ctx, firstCommand)
	if err != nil || replayed.ID != first.ID {
		t.Fatalf("replay = %+v, %v", replayed, err)
	}
	got, err = m.ReadLocation(ctx, staff, practiceID, locations["Alpha"])
	if err != nil || got.Revision == nil || got.Revision.ID != first.ID || !reflect.DeepEqual(got.Sections, sections) {
		t.Fatalf("replayed revision without positions = %+v, %v", got, err)
	}

	next := []Section{{ID: "hours", Title: "Office hours", Text: "The synthetic office closes at five."}}
	second, err := m.ReplaceCorpus(ctx, ImportCommand{ID: uuid.NewString(), PracticeID: practiceID, OfficeKey: "office-alpha", ExpectedRevisionID: first.ID, Sections: next, Provenance: "approved synthetic fixture", Reason: "update", ActorSubject: "operator@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	got, err = m.ReadLocation(ctx, staff, practiceID, locations["Alpha"])
	if err != nil || got.Revision == nil || got.Revision.ID != second.ID || !reflect.DeepEqual(got.Sections, next) {
		t.Fatalf("current revision = %+v, %v", got, err)
	}

	for name, scenario := range map[string]struct {
		identity   access.Identity
		practiceID string
		locationID string
		want       error
	}{
		"outside scope":    {staff, practiceID, locations["Beta"], access.ErrDenied},
		"other Practice":   {admin, uuid.NewString(), locations["Alpha"], access.ErrDenied},
		"unknown Location": {admin, practiceID, uuid.NewString(), access.ErrDenied},
		"no route":         {admin, practiceID, locations["Unrouted"], access.ErrNoOfficeRoute},
		"several routes":   {admin, practiceID, locations["Combined"], access.ErrNoOfficeRoute},
		"invalid Location": {admin, practiceID, "not-a-uuid", ErrInvalidInput},
	} {
		if result, err := m.ReadLocation(ctx, scenario.identity, scenario.practiceID, scenario.locationID); !errors.Is(err, scenario.want) {
			t.Errorf("%s = %+v, %v; want %v", name, result, err, scenario.want)
		}
	}
	legacy, err := m.ReadLocation(ctx, admin, practiceID, locations["Beta"])
	if err != nil || legacy.Revision == nil || legacy.Revision.ID != beta.ID || !reflect.DeepEqual(legacy.Sections, []Section{betaSections[1], betaSections[0]}) {
		t.Fatalf("legacy Beta corpus without positions = %+v, %v", legacy, err)
	}
}
