package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/knowledge"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/google/uuid"
)

type publicationEmbedder struct{}

func (publicationEmbedder) Embed(_ context.Context, texts []string, _ knowledge.TaskType) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for i := range vectors {
		vectors[i] = make([]float32, knowledge.Dimensions)
		vectors[i][0] = 1
	}
	return vectors, nil
}

func TestImportSetsUpAnOfficeOrUpdatesItsActiveRevision(t *testing.T) {
	pool := testdb.OpenAsOwner(t)
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
	source := Source{PracticeID: practice, OfficeKey: "synthetic-office", Entries: []Entry{{ID: "hours", Title: "Hours", Text: "Closes at five."}}}
	command, err := sourceCommand(source, strings.Repeat("a", 40), "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []string{"", "unknown@example.com"} {
		if _, err := preparePublication(ctx, pool, command, actor); err == nil {
			t.Fatal("import bypassed the operator check")
		}
	}
	unknown := command
	unknown.OfficeKey = "unknown-office"
	if _, err := preparePublication(ctx, pool, unknown, email); err == nil {
		t.Fatal("accepted unknown route")
	}
	prepared, err := preparePublication(ctx, pool, command, email)
	if err != nil || prepared.ActorSubject != "synthetic-operator" || prepared.ExpectedRevisionID != "" {
		t.Fatalf("first import: %+v, %v", prepared, err)
	}
	module, err := knowledge.New(pool, auth, publicationEmbedder{}, knowledge.Config{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := module.ReplaceCorpus(ctx, prepared)
	if err != nil {
		t.Fatal(err)
	}
	source.Entries[0].Text = "Closes at six."
	for _, tc := range []struct{ expected, reason, want string }{
		{first.ID, " ", "--reason"},
		{"", " ", "--reason"},
		{"not-a-revision", "Later hours", "--expected-revision"},
	} {
		if _, err := sourceCommand(source, strings.Repeat("a", 40), tc.expected, tc.reason); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
	setup, err := sourceCommand(source, strings.Repeat("a", 40), "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := preparePublication(ctx, pool, setup, email); err == nil || !strings.Contains(err.Error(), "--expected-revision "+first.ID) {
		t.Fatalf("setup over an active revision: %v", err)
	}
	wrong, err := sourceCommand(source, strings.Repeat("a", 40), uuid.NewString(), "Later hours")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := preparePublication(ctx, pool, wrong, email); err == nil {
		t.Fatal("accepted an update against a revision that is not active")
	}
	update, err := sourceCommand(source, strings.Repeat("a", 40), strings.ToUpper(first.ID), "Later hours")
	if err != nil {
		t.Fatal(err)
	}
	if update.ID == setup.ID || update.Reason != "Later hours" {
		t.Fatalf("update command: %+v", update)
	}
	prepared, err = preparePublication(ctx, pool, update, email)
	if err != nil {
		t.Fatal(err)
	}
	second, err := module.ReplaceCorpus(ctx, prepared)
	if err != nil || second.Version != 2 {
		t.Fatalf("update: %+v, %v", second, err)
	}
	replay, err := preparePublication(ctx, pool, update, email)
	if err != nil {
		t.Fatalf("replaying the same update: %v", err)
	}
	if again, err := module.ReplaceCorpus(ctx, replay); err != nil || again.ID != second.ID || again.Version != 2 {
		t.Fatalf("replayed update: %+v, %v", again, err)
	}
	staff := prepared
	staff.ID, staff.ExpectedRevisionID, staff.Provenance = uuid.NewString(), second.ID, "portal"
	staff.Sections = []knowledge.Section{{ID: "hours", Title: "Hours", Text: "Closes at seven."}}
	if _, err := module.ReplaceCorpus(ctx, staff); err != nil {
		t.Fatal(err)
	}
	source.Entries[0].Text = "Closes at eight."
	stale, err := sourceCommand(source, strings.Repeat("a", 40), second.ID, "Even later hours")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := preparePublication(ctx, pool, stale, email); err == nil {
		t.Fatal("an update overwrote a newer Portal publish")
	}
	oversized := Source{PracticeID: practice, OfficeKey: "synthetic-office"}
	for i := range 4 {
		oversized.Entries = append(oversized.Entries, Entry{ID: fmt.Sprintf("entry-%d", i), Title: "T", Text: strings.Repeat("x", 5000)})
	}
	var size *knowledge.SizeError
	if _, err := sourceCommand(oversized, strings.Repeat("a", 40), second.ID, "Too long"); !errors.As(err, &size) || size.Characters != 20004 {
		t.Fatalf("source over the prompt cap: %v", err)
	}
}

func TestExportRoundTripsARevision(t *testing.T) {
	pool := testdb.OpenAsOwner(t)
	ctx := context.Background()
	auth := access.New(pool, nil)
	_, err := auth.Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "test", Practices: []access.PracticeProvision{{Key: "synthetic", Name: "Synthetic practice", Locations: []access.LocationProvision{
		{Key: "seeded", Name: "Seeded", AbitaOfficeKeys: []string{"seeded"}},
		{Key: "copy-active", Name: "Active copy", AbitaOfficeKeys: []string{"copy-active"}},
		{Key: "copy-earlier", Name: "Earlier copy", AbitaOfficeKeys: []string{"copy-earlier"}},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	var practice string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM access_practices WHERE provisioning_key='synthetic'`).Scan(&practice); err != nil {
		t.Fatal(err)
	}
	module, err := knowledge.New(pool, auth, publicationEmbedder{}, knowledge.Config{})
	if err != nil {
		t.Fatal(err)
	}
	seed := func(expected string, sections []knowledge.Section) knowledge.Revision {
		t.Helper()
		revision, err := module.ReplaceCorpus(ctx, knowledge.ImportCommand{ID: uuid.NewString(), PracticeID: practice, OfficeKey: "seeded", ExpectedRevisionID: expected, Sections: sections, Provenance: "portal", Reason: "seed", ActorSubject: "synthetic"})
		if err != nil {
			t.Fatal(err)
		}
		return revision
	}
	earlier := seed("", []knowledge.Section{
		{ID: "parking", Title: "Parking", Text: "Use the visitor lot.\nLevel two: \"staff only\"."},
		{ID: "hours", Title: "Hours", Text: "Closes at five."},
	})
	active := seed(earlier.ID, []knowledge.Section{{ID: "hours", Title: "Hours", Text: "Closes at six."}})

	for _, tc := range []struct {
		target, revision string
		want             knowledge.Revision
	}{{"copy-active", "", active}, {"copy-earlier", earlier.ID, earlier}} {
		t.Run(tc.target, func(t *testing.T) {
			var out bytes.Buffer
			exported, err := exportSource(ctx, module, practice, "seeded", tc.revision, &out)
			if err != nil || exported != tc.want.ID {
				t.Fatalf("export: %s, %v", exported, err)
			}
			source, err := readSource(&out)
			if err != nil {
				t.Fatal(err)
			}
			if source.PracticeID != practice || source.OfficeKey != "seeded" {
				t.Fatalf("exported scope: %+v", source)
			}
			source.OfficeKey = tc.target
			command, err := sourceCommand(source, strings.Repeat("b", 40), "", "")
			if err != nil {
				t.Fatal(err)
			}
			command.ActorSubject = "synthetic"
			imported, err := module.ReplaceCorpus(ctx, command)
			if err != nil {
				t.Fatal(err)
			}
			if imported.ContentHash != tc.want.ContentHash {
				t.Fatalf("round trip hash %s, want %s", imported.ContentHash, tc.want.ContentHash)
			}
		})
	}
	var out bytes.Buffer
	if _, err := exportSource(ctx, module, practice, "copy-active", earlier.ID, &out); err == nil {
		t.Fatal("exported a revision from another office")
	}
}

func TestImportHoldsAbitaOfficesToTheEntryStandard(t *testing.T) {
	pool := testdb.OpenAsOwner(t)
	ctx := context.Background()
	auth := access.New(pool, nil)
	_, err := auth.Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "test", Practices: []access.PracticeProvision{{Key: "abita-eye-group", Name: "Synthetic Abita", Locations: []access.LocationProvision{{Key: "new", Name: "New office", AbitaOfficeKeys: []string{"new-office"}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	var practice string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM access_practices WHERE provisioning_key='abita-eye-group'`).Scan(&practice); err != nil {
		t.Fatal(err)
	}
	command, err := sourceCommand(Source{PracticeID: practice, OfficeKey: "new-office", Entries: []Entry{{ID: "hours", Title: "Hours", Text: "Closes at five."}}}, strings.Repeat("c", 40), "", "")
	if err != nil {
		t.Fatal(err)
	}
	command.ActorSubject = "synthetic-operator"
	module, err := knowledge.New(pool, auth, publicationEmbedder{}, knowledge.Config{})
	if err != nil {
		t.Fatal(err)
	}
	var entry *knowledge.EntryError
	if _, err := module.ReplaceCorpus(ctx, command); !errors.As(err, &entry) || entry.EntryID != "current-address" {
		t.Fatalf("non-standard Abita import: %v", err)
	}
}
