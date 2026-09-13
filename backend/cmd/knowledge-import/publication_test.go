package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/knowledge"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/google/uuid"
)

type publicationEmbedder struct{ calls int }

func (e *publicationEmbedder) Embed(_ context.Context, texts []string, _ knowledge.TaskType) ([][]float32, error) {
	e.calls++
	vectors := make([][]float32, len(texts))
	for i := range vectors {
		vectors[i] = make([]float32, knowledge.Dimensions)
		vectors[i][0] = 1
	}
	return vectors, nil
}

func TestAutomaticPublication(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	auth := access.New(pool, nil)
	const email = "operator@example.com"
	_, err := auth.Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "test", PlatformOperators: []string{email}, Practices: []access.PracticeProvision{{Key: "synthetic", Name: "Synthetic practice", Locations: []access.LocationProvision{{Key: "one", Name: "Synthetic location", AbitaOfficeKeys: []string{"synthetic-office"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	testaccess.Activate(t, auth, access.Identity{Subject: "synthetic-operator", Email: email, EmailVerified: true})
	var practice string
	if err := pool.QueryRow(ctx, `SELECT practice_id::text FROM access_abita_office_locations WHERE office_key='synthetic-office'`).Scan(&practice); err != nil {
		t.Fatal(err)
	}
	source := Source{PracticeID: practice, OfficeKey: "synthetic-office", Entries: []Entry{{ID: "hours", Title: "Hours", Text: "Closes at five."}, {ID: "parking", Title: "Parking", Text: "Use the visitor lot."}}}
	command, err := sourceCommand(source, strings.Repeat("a", 40), "")
	if err != nil {
		t.Fatal(err)
	}
	initial, receipt, err := preparePublication(ctx, pool, command, email, true)
	if err != nil || receipt != nil || initial.ExpectedRevisionID != "" {
		t.Fatalf("initial: %+v, %+v, %v", initial, receipt, err)
	}
	embedder := &publicationEmbedder{}
	module, err := knowledge.New(pool, auth, embedder, knowledge.Config{})
	if err != nil {
		t.Fatal(err)
	}
	revision, err := module.ReplaceCorpus(ctx, initial)
	if err != nil {
		t.Fatal(err)
	}
	// Entry order and a newer Git commit do not create another revision.
	command.Sections[0], command.Sections[1] = command.Sections[1], command.Sections[0]
	command.Provenance = "git:" + strings.Repeat("b", 40)
	_, receipt, err = preparePublication(ctx, pool, command, email, true)
	if err != nil || receipt == nil {
		t.Fatalf("unchanged: %+v, %v", receipt, err)
	}
	if !receipt.Unchanged || receipt.Applied || !receipt.ActiveRevisionVerified || receipt.Revision.ID != revision.ID || receipt.Provenance != initial.Provenance || receipt.SourceGitCommit != strings.Repeat("b", 40) {
		t.Fatalf("receipt: %+v", receipt)
	}
	if embedder.calls != 1 {
		t.Fatalf("unchanged source embedded again: %d", embedder.calls)
	}
	for _, actor := range []string{"", "unknown@example.com"} {
		if _, _, err := preparePublication(ctx, pool, command, actor, true); err == nil {
			t.Fatal("unchanged source bypassed operator check")
		}
	}
	unknown := command
	unknown.OfficeKey = "unknown-office"
	if _, _, err := preparePublication(ctx, pool, unknown, email, true); err == nil {
		t.Fatal("accepted unknown route")
	}
	// Explicit expectations remain authoritative, even when content matches.
	explicit, receipt, err := preparePublication(ctx, pool, command, email, false)
	if err != nil || receipt != nil || explicit.ExpectedRevisionID != command.ExpectedRevisionID || explicit.ID != command.ID {
		t.Fatalf("explicit: %+v, %+v, %v", explicit, receipt, err)
	}
	command.Sections = []knowledge.Section{{ID: "hours", Title: "Hours", Text: "Closes at six."}}
	changed, receipt, err := preparePublication(ctx, pool, command, email, true)
	if err != nil || receipt != nil || changed.ExpectedRevisionID != revision.ID || changed.ID == initial.ID {
		t.Fatalf("changed: %+v, %+v, %v", changed, receipt, err)
	}
	if _, err := module.ReplaceCorpus(ctx, changed); err != nil {
		t.Fatal(err)
	}
	// A prepared publication cannot overwrite an intervening publication.
	stale := changed
	stale.ID = uuid.NewString()
	if _, err := module.ReplaceCorpus(ctx, stale); !errors.Is(err, knowledge.ErrConflict) {
		t.Fatalf("concurrent publication: %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM knowledge_revisions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("unexpected revisions: %d", count)
	}
}
