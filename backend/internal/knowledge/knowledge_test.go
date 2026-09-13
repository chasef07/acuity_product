package knowledge

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/google/uuid"
)

type testEmbedder struct {
	fail       bool
	before     func()
	calls      int
	irrelevant bool
}

func (p *testEmbedder) Embed(_ context.Context, texts []string, _ TaskType) ([][]float32, error) {
	p.calls++
	if p.before != nil {
		p.before()
	}
	if p.fail {
		return nil, errors.New("provider failed with private diagnostics")
	}
	result := make([][]float32, len(texts))
	for i := range texts {
		result[i] = make([]float32, Dimensions)
		if p.irrelevant {
			result[i][1] = 1
		} else {
			result[i][0] = 1
		}
	}
	return result, nil
}
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
func TestCorpusAtomicReplacementAndScopedSearch(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	a := access.New(pool, time.Now)
	_, err := a.Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "test", Practices: []access.PracticeProvision{{Key: "knowledge-test", Name: "Synthetic practice", Locations: []access.LocationProvision{{Key: "one", Name: "Synthetic location", AbitaOfficeKeys: []string{"office-a", "office-b"}}}, AccessGrants: []access.AccessGrantProvision{{Key: "staff", Email: "staff@example.com", Role: access.RoleStaff, LocationScope: access.LocationScopeAll}}}}})
	if err != nil {
		t.Fatal(err)
	}
	authorization := testaccess.Activate(t, a, access.Identity{Subject: "staff", Email: "staff@example.com", EmailVerified: true})
	provider := &testEmbedder{}
	provider.before = func() {
		if got := pool.Stat().AcquiredConns(); got != 1 {
			t.Errorf("provider called with %d acquired connections; only test advisory lock expected", got)
		}
	}
	m, err := New(pool, a, provider, Config{MinSimilarity: 0.5})
	if err != nil {
		t.Fatal(err)
	}
	cmd := ImportCommand{ID: uuid.NewString(), PracticeID: authorization.Practice.ID, OfficeKey: "office-a", Sections: []Section{{ID: "hours", Title: "Office hours", Text: "The synthetic office closes at six. Saturday hours are not supplied."}}, Provenance: "approved synthetic fixture", Reason: "initial import", ActorSubject: "operator@example.com"}
	rev, err := m.ReplaceCorpus(ctx, cmd)
	if err != nil {
		t.Fatal(err)
	}
	service := access.ServiceIdentity{Subject: "agent", PracticeID: cmd.PracticeID, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityReadKnowledge}}
	got, err := m.Search(ctx, service, "office-a", "When does everyone head home?")
	if err != nil || got.Outcome != "found" || got.Passages[0].RevisionID != rev.ID {
		t.Fatalf("search=%+v err=%v", got, err)
	}
	provider.irrelevant = true
	irrelevant, err := m.Search(ctx, service, "office-a", "Who won the football game?")
	if err != nil || irrelevant.Outcome != "no_relevant_information" || len(irrelevant.Passages) != 0 {
		t.Fatalf("irrelevant result=%+v error=%v", irrelevant, err)
	}
	provider.irrelevant = false
	before := provider.calls
	if _, err := m.Search(ctx, access.ServiceIdentity{Subject: "agent", PracticeID: uuid.NewString(), LocationScope: access.LocationScopeAll, Capabilities: service.Capabilities}, "office-a", "hours"); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("cross Practice=%v", err)
	}
	if provider.calls != before {
		t.Fatal("unauthorized query reached embedding provider")
	}
	if _, err := m.Search(ctx, service, "office-b", "hours"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("same location different route must not share corpus: %v", err)
	}
	if _, err := m.Search(ctx, access.ServiceIdentity{Subject: "staff", PracticeID: cmd.PracticeID, LocationScope: access.LocationScopeAll}, "office-a", "hours"); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("missing capability=%v", err)
	}
	// Idempotent replay skips provider work and never republishes a superseded revision.
	before = provider.calls
	if _, err := m.ReplaceCorpus(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	if provider.calls != before {
		t.Fatal("replay re-embedded corpus")
	}
	next := cmd
	next.ID = uuid.NewString()
	next.ExpectedRevisionID = rev.ID
	next.Sections = []Section{{ID: "hours", Title: "Office hours", Text: "The synthetic office closes at five."}}
	provider.fail = true
	if _, err := m.ReplaceCorpus(ctx, next); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("provider failure=%v", err)
	}
	provider.fail = false
	got, err = m.Search(ctx, service, "office-a", "hours")
	if err != nil || *got.RevisionID != rev.ID {
		t.Fatalf("failed import changed pointer: %+v %v", got, err)
	}
	newRev, err := m.ReplaceCorpus(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.ReplaceCorpus(ctx, cmd); !errors.Is(err, ErrConflict) {
		t.Fatalf("superseded replay=%v", err)
	}
	stale := next
	stale.ID = uuid.NewString()
	if _, err := m.ReplaceCorpus(ctx, stale); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale pointer=%v", err)
	}
	got, err = m.Search(ctx, service, "office-a", "hours")
	if err != nil || *got.RevisionID != newRev.ID || got.Passages[0].Text != next.Sections[0].Text {
		t.Fatalf("replacement search=%+v %v", got, err)
	}
	var revisions, passages int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM knowledge_revisions`).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM knowledge_passages`).Scan(&passages); err != nil {
		t.Fatal(err)
	}
	if revisions != 2 || passages != 2 {
		t.Fatalf("partial/replayed revisions persisted: %d %d", revisions, passages)
	}
	// Two fully prepared imports compete for the same expected pointer. Exactly
	// one may commit; the other cannot leave an orphan revision or partial corpus.
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	m.embedder = embeddingFunc(func(ctx context.Context, texts []string, task TaskType) ([][]float32, error) {
		entered <- struct{}{}
		<-release
		return (&testEmbedder{}).Embed(ctx, texts, task)
	})
	candidates := []ImportCommand{next, next}
	for i := range candidates {
		candidates[i].ID = uuid.NewString()
		candidates[i].ExpectedRevisionID = newRev.ID
	}
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := range candidates {
		wg.Add(1)
		go func(i int) { defer wg.Done(); _, results[i] = m.ReplaceCorpus(ctx, candidates[i]) }(i)
	}
	<-entered
	<-entered
	close(release)
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
	// A provider that ignores cancellation must still never install late content.
	canceled, cancel := context.WithCancel(ctx)
	m.embedder = embeddingFunc(func(ctx context.Context, texts []string, task TaskType) ([][]float32, error) {
		cancel()
		return (&testEmbedder{}).Embed(ctx, texts, task)
	})
	var current string
	if err := pool.QueryRow(ctx, `SELECT revision_id::text FROM knowledge_corpora WHERE practice_id=$1 AND office_key=$2`, cmd.PracticeID, cmd.OfficeKey).Scan(&current); err != nil {
		t.Fatal(err)
	}
	late := next
	late.ID = uuid.NewString()
	late.ExpectedRevisionID = current
	if _, err := m.ReplaceCorpus(canceled, late); !errors.Is(err, context.Canceled) {
		t.Fatalf("late provider import=%v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM knowledge_revisions`).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if revisions != 3 {
		t.Fatalf("canceled/concurrent imports persisted %d revisions", revisions)
	}

	// Access provisioning replaces routing rows. It must remain operable after
	// import; removing a route denies search while retaining historical evidence.
	provisioning := access.Provisioning{Environment: "test", RequestedBy: "test", Practices: []access.PracticeProvision{{Key: "knowledge-test", Name: "Synthetic practice", Locations: []access.LocationProvision{{Key: "one", Name: "Synthetic location", AbitaOfficeKeys: []string{"office-a", "office-b"}}}}}}
	if _, err := a.Provision(ctx, provisioning); err != nil {
		t.Fatalf("reconcile existing routes after import: %v", err)
	}
	provisioning.Practices[0].Locations[0].AbitaOfficeKeys = []string{"office-b"}
	if _, err := a.Provision(ctx, provisioning); err != nil {
		t.Fatalf("retire imported route: %v", err)
	}
	if _, err := m.Search(ctx, service, "office-a", "hours"); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("retired route search: %v", err)
	}

}

type embeddingFunc func(context.Context, []string, TaskType) ([][]float32, error)

func (f embeddingFunc) Embed(ctx context.Context, texts []string, task TaskType) ([][]float32, error) {
	return f(ctx, texts, task)
}
