package main

import (
	"bytes"
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

type publicationEmbedder struct{}

func (publicationEmbedder) Embed(_ context.Context, texts []string, _ knowledge.TaskType) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for i := range vectors {
		vectors[i] = make([]float32, knowledge.Dimensions)
		vectors[i][0] = 1
	}
	return vectors, nil
}

func TestImportPublishesOnlyOfficesWithoutKnowledge(t *testing.T) {
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
	command, err := sourceCommand(source, strings.Repeat("a", 40))
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
	if _, err := module.ReplaceCorpus(ctx, prepared); err != nil {
		t.Fatal(err)
	}
	command.Sections[0].Text = "Closes at six."
	if _, err := preparePublication(ctx, pool, command, email); err == nil || !strings.Contains(err.Error(), "Portal") {
		t.Fatalf("import over an active revision: %v", err)
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
			command, err := sourceCommand(source, strings.Repeat("b", 40))
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
	command, err := sourceCommand(Source{PracticeID: practice, OfficeKey: "new-office", Entries: []Entry{{ID: "hours", Title: "Hours", Text: "Closes at five."}}}, strings.Repeat("c", 40))
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
