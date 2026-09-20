package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/knowledge"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/google/uuid"
)

type tiedEmbeddings struct{}

func (tiedEmbeddings) Embed(_ context.Context, texts []string, _ knowledge.TaskType) ([][]float32, error) {
	vectors := make([][]float32, len(texts))
	for i := range vectors {
		vectors[i] = make([]float32, knowledge.Dimensions)
		vectors[i][0] = 1
	}
	return vectors, nil
}

// Replay non-patient questions found in call evidence through the real importer
// and retrieval SQL. Tied vectors isolate source granularity and lexical ranking;
// this does not claim to validate the embedding provider's semantic relevance.
func TestPublishedSourcesReturnFocusedEvidence(t *testing.T) {
	for _, fixture := range []struct{ office, caseID string }{
		{office: "north-miami-beach-optical"},
		{office: "sweetwater"},
		{office: "hollywood"},
		{office: "crystal-river"},
		{office: "ophthalmology-demo", caseID: "hours"},
	} {
		office := fixture.office
		t.Run(office, func(t *testing.T) {
			pool := testdb.Open(t)
			ctx := context.Background()
			a := access.New(pool, time.Now)
			_, err := a.Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "test", Practices: []access.PracticeProvision{{Key: "focused", Name: "Synthetic practice", Locations: []access.LocationProvision{{Key: "one", Name: "Synthetic location", AbitaOfficeKeys: []string{office}}}}}})
			if err != nil {
				t.Fatal(err)
			}
			var practice string
			if err := pool.QueryRow(ctx, `SELECT id::text FROM access_practices WHERE provisioning_key='focused'`).Scan(&practice); err != nil {
				t.Fatal(err)
			}
			file, err := os.Open(filepath.Join("../../..", "knowledge/offices", office+".yaml"))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			source, err := readSource(file)
			if err != nil {
				t.Fatal(err)
			}
			cmd, err := sourceCommand(source, "0000000000000000000000000000000000000000", "none")
			if err != nil {
				t.Fatal(err)
			}
			cmd.ID, cmd.PracticeID, cmd.ActorSubject = uuid.NewString(), practice, "test"
			module, err := knowledge.New(pool, a, tiedEmbeddings{}, knowledge.Config{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := module.ReplaceCorpus(ctx, cmd); err != nil {
				t.Fatal(err)
			}
			identity := access.ServiceIdentity{Subject: "agent", PracticeID: practice, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityReadKnowledge}}
			var cases []struct {
				ID                    string   `json:"id"`
				Query                 string   `json:"query"`
				ExpectedSectionIDs    []string `json:"expectedSectionIds"`
				MaxResponseCharacters int      `json:"maxResponseCharacters"`
			}
			caseFile, err := os.ReadFile(filepath.Join("../../..", "knowledge/evals", office+".json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(caseFile, &cases); err != nil {
				t.Fatal(err)
			}
			matched := 0
			for _, tc := range cases {
				if fixture.caseID != "" && fixture.caseID != tc.ID {
					continue
				}
				matched++
				t.Run(tc.Query, func(t *testing.T) {
					got, err := module.Search(ctx, identity, office, tc.Query)
					if err != nil {
						t.Fatal(err)
					}
					ids := []string{}
					characters := 0
					for _, passage := range got.Passages {
						ids = append(ids, passage.SectionID)
						characters += utf8.RuneCountInString(passage.Text) + utf8.RuneCountInString(passage.Title)
						if passage.SectionID == "paperwork-email" && !strings.Contains(passage.Text, "Use this address only for new-patient paperwork or requested documents.") {
							t.Fatal("email use restriction was lost")
						}
					}
					slices.Sort(ids)
					slices.Sort(tc.ExpectedSectionIDs)
					if got.Outcome != "found" || !slices.Equal(ids, tc.ExpectedSectionIDs) || characters > tc.MaxResponseCharacters {
						t.Fatalf("query %q must return only %v within %d characters; got %+v (%d characters)", tc.Query, tc.ExpectedSectionIDs, tc.MaxResponseCharacters, got, characters)
					}
				})
			}
			if matched == 0 {
				t.Fatalf("no retrieval cases matched %q", fixture.caseID)
			}

		})
	}
}
