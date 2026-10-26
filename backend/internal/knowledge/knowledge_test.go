package knowledge

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/google/uuid"
)

func TestValidateImport(t *testing.T) {
	cases := []struct {
		name     string
		sections []Section
		valid    bool
	}{
		{"empty", nil, false}, {"blank title", []Section{{ID: "hours", Text: "Office hours are not supplied."}}, false},
		{"duplicate", []Section{{ID: "hours", Title: "Hours", Text: "Five"}, {ID: "hours", Title: "Hours", Text: "Six"}}, false},
		{"explicit absence", []Section{{ID: "hours", Title: "Hours", Text: "Saturday hours are not supplied."}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := ImportCommand{ID: uuid.NewString(), PracticeID: uuid.NewString(), OfficeKey: "office", Sections: tc.sections, Provenance: "approved synthetic", Reason: "test", ActorSubject: "operator"}
			if err := ValidateImport(cmd); (err == nil) != tc.valid {
				t.Fatalf("validation=%v want valid %v", err, tc.valid)
			}
		})
	}
}
func TestCorpusAtomicReplacementAndScopedRead(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	ctx := context.Background()
	a := access.New(db.As(testdb.Portal), time.Now)
	_, err := access.New(pool, time.Now).Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "test", Practices: []access.PracticeProvision{{Key: "knowledge-test", Name: "Synthetic practice", Locations: []access.LocationProvision{{Key: "one", Name: "Synthetic location", AbitaOfficeKeys: []string{"office-a", "office-b"}}}, AccessGrants: []access.AccessGrantProvision{{Key: "staff", Email: "staff@example.com", Role: access.RoleStaff, LocationScope: access.LocationScopeAll}}}}})
	if err != nil {
		t.Fatal(err)
	}
	authorization := testaccess.Activate(t, a, access.Identity{Subject: "staff", Email: "staff@example.com", EmailVerified: true})
	m, err := New(db.As(testdb.Portal), a)
	if err != nil {
		t.Fatal(err)
	}
	cmd := ImportCommand{ID: uuid.NewString(), PracticeID: authorization.Practice.ID, OfficeKey: "office-a", Sections: []Section{{ID: "hours", Title: "Office hours", Text: "The synthetic office closes at six. Saturday hours are not supplied."}}, Provenance: "approved synthetic fixture", Reason: "initial import", ActorSubject: "operator@example.com"}
	rev, err := m.ReplaceCorpus(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	service := access.ServiceIdentity{Subject: "agent", PracticeID: cmd.PracticeID, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityReadKnowledge}}
	got, err := m.Read(ctx, service, "office-a")
	if err != nil || got.RevisionID != rev.ID || !reflect.DeepEqual(got.Entries, cmd.Sections) {
		t.Fatalf("read=%+v err=%v", got, err)
	}
	if _, err := m.Read(ctx, access.ServiceIdentity{Subject: "agent", PracticeID: uuid.NewString(), LocationScope: access.LocationScopeAll, Capabilities: service.Capabilities}, "office-a"); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("cross Practice=%v", err)
	}
	if _, err := m.Read(ctx, service, "office-b"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("same location different route must not share corpus: %v", err)
	}
	if _, err := m.Read(ctx, access.ServiceIdentity{Subject: "staff", PracticeID: cmd.PracticeID, LocationScope: access.LocationScopeAll}, "office-a"); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("missing capability=%v", err)
	}
	if replayed, err := m.ReplaceCorpus(ctx, cmd); err != nil || replayed.ID != rev.ID || replayed.Version != rev.Version {
		t.Fatalf("replay=%+v %v", replayed, err)
	}
	next := cmd
	next.ID = uuid.NewString()
	next.ExpectedRevisionID = rev.ID
	next.Sections = []Section{{ID: "hours", Title: "Office hours", Text: "The synthetic office closes at five."}}
	newRev, err := m.ReplaceCorpus(ctx, next)
	if err != nil || newRev.Version != rev.Version+1 {
		t.Fatalf("replacement=%+v %v", newRev, err)
	}
	if _, err := m.ReplaceCorpus(ctx, cmd); !errors.Is(err, ErrConflict) {
		t.Fatalf("superseded replay=%v", err)
	}
	stale := next
	stale.ID = uuid.NewString()
	if _, err := m.ReplaceCorpus(ctx, stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale pointer=%v", err)
	}
	got, err = m.Read(ctx, service, "office-a")
	if err != nil || got.RevisionID != newRev.ID || got.Entries[0].Text != next.Sections[0].Text {
		t.Fatalf("replacement read=%+v %v", got, err)
	}
	var revisions, passages, embedded int
	if err := pool.QueryRow(ctx, `SELECT count(*), count(embedding_model) + count(embedding_dimensions) FROM knowledge_revisions`).Scan(&revisions, &embedded); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*), $1::int + count(embedding) FROM knowledge_passages`, embedded).Scan(&passages, &embedded); err != nil {
		t.Fatal(err)
	}
	if revisions != 2 || passages != 2 || embedded != 0 {
		t.Fatalf("partial/replayed revisions persisted: %d %d, embedding values %d", revisions, passages, embedded)
	}
	candidates := []ImportCommand{next, next}
	for i := range candidates {
		candidates[i].ID = uuid.NewString()
		candidates[i].ExpectedRevisionID = newRev.ID
	}
	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Exec(ctx, `SELECT 1 FROM knowledge_corpora WHERE practice_id=$1 AND office_key=$2 FOR UPDATE`, next.PracticeID, next.OfficeKey); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := range candidates {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _, results[i] = m.ReplaceCorpus(ctx, candidates[i]) }(i)
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		var waiting int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%knowledge_corpora%FOR UPDATE%'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("publishers waiting on the corpus lock=%d", waiting)
		}
	}
	if err := holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	wins, conflicts := 0, 0
	for _, err := range results {
		if err == nil {
			wins++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("concurrent imports wins=%d conflicts=%d", wins, conflicts)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM knowledge_revisions`).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if revisions != 3 {
		t.Fatalf("concurrent imports persisted %d revisions", revisions)
	}

	provisioning := access.Provisioning{Environment: "test", RequestedBy: "test", Practices: []access.PracticeProvision{{Key: "knowledge-test", Name: "Synthetic practice", Locations: []access.LocationProvision{{Key: "one", Name: "Synthetic location", AbitaOfficeKeys: []string{"office-a", "office-b"}}}}}}
	if _, err := access.New(pool, time.Now).Provision(ctx, provisioning); err != nil {
		t.Fatalf("reconcile existing routes after import: %v", err)
	}
	provisioning.Practices[0].Locations[0].AbitaOfficeKeys = []string{"office-b"}
	if _, err := access.New(pool, time.Now).Provision(ctx, provisioning); err != nil {
		t.Fatalf("retire imported route: %v", err)
	}
	if _, err := m.Read(ctx, service, "office-a"); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("retired route read: %v", err)
	}
}
