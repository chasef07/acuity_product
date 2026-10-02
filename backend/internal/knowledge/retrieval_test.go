package knowledge

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/google/uuid"
)

func TestSelectPassagesPreservesRestrictionsAndBudget(t *testing.T) {
	restriction := strings.Repeat("Complete policy. ", 100) + "Never book this closed office."
	candidates := []Passage{{SectionID: "closure", Text: restriction}, {SectionID: "duplicate", Text: restriction}, {SectionID: "other", Text: strings.Repeat("Other policy. ", 150)}}
	got := selectPassages(candidates, 4)
	if len(got) != 1 || got[0].Text != restriction {
		t.Fatalf("must preserve strongest whole restriction: %+v", got)
	}
	legacy := strings.Repeat("x", 6000) + " Exception must remain."
	got = selectPassages([]Passage{{Text: legacy}, {Text: "Other"}}, 4)
	if len(got) != 1 || got[0].Text != legacy {
		t.Fatal("legacy evidence was truncated or exceeded with another entry")
	}
	got = selectPassages([]Passage{{Text: "Current address."}, {Text: "Closed former office."}, {Text: " Current   address. "}}, 4)
	if len(got) != 2 {
		t.Fatalf("multipart evidence or dedup lost: %+v", got)
	}
}

func TestHybridSearchNamesAndIrrelevantQueries(t *testing.T) {
	pool := testdb.Open(t)
	ctx := context.Background()
	a := access.New(pool, time.Now)
	_, err := a.Provision(ctx, access.Provisioning{Environment: "test", RequestedBy: "test", Practices: []access.PracticeProvision{{Key: "hybrid", Name: "Synthetic practice", Locations: []access.LocationProvision{{Key: "one", Name: "Synthetic location", AbitaOfficeKeys: []string{"office"}}}, AccessGrants: []access.AccessGrantProvision{{Key: "staff", Email: "staff@example.com", Role: access.RoleStaff, LocationScope: access.LocationScopeAll}}}}})
	if err != nil {
		t.Fatal(err)
	}
	authorization := testaccess.Activate(t, a, access.Identity{Subject: "staff", Email: "staff@example.com", EmailVerified: true})
	provider := &testEmbedder{}
	m, err := New(pool, a, provider, Config{})
	if err != nil {
		t.Fatal(err)
	}
	closure := "The former Synthetic Drive office is permanently closed. Do not book or move appointments there."
	cmd := ImportCommand{ID: uuid.NewString(), PracticeID: authorization.Practice.ID, OfficeKey: "office", Provenance: "synthetic", Reason: "test", ActorSubject: "operator", Sections: []Section{{ID: "a-hours", Title: "Office hours", Text: "The office opens at nine."}, {ID: "after-hours", Title: "After-hours doctor contact", Text: "After-hours doctor: 555-0100."}, {ID: "optician", Title: "Optician hours", Text: "The optician is available on Tuesdays."}, {ID: "z-closure", Title: "Synthetic Drive", Text: closure}}}
	if _, err = m.ReplaceCorpus(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	service := access.ServiceIdentity{Subject: "agent", PracticeID: cmd.PracticeID, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityReadKnowledge}}
	got, err := m.Search(ctx, service, "office", "Can I move my appointment to Synthetic Drive?")
	if err != nil || len(got.Passages) == 0 || got.Passages[0].SectionID != "z-closure" {
		t.Fatalf("lexical name must outrank tied vectors: %+v %v", got, err)
	}
	got, err = m.Search(ctx, service, "office", "What are your office hours?")
	if err != nil || len(got.Passages) != 1 || got.Passages[0].SectionID != "a-hours" {
		t.Fatalf("office hours must not return after-hours doctor: %+v %v", got, err)
	}
	got, err = m.Search(ctx, service, "office", "¿Está cerrada la oficina de Synthetic Drive y cuál es la dirección actual?")
	if err != nil || len(got.Passages) < 2 {
		t.Fatalf("mixed-language query must preserve semantic candidates, not assume name covers all needs: %+v %v", got, err)
	}
	provider.irrelevant = true
	got, err = m.Search(ctx, service, "office", "Can I move my appointment to Synthetic Drive?")
	if err != nil || len(got.Passages) != 1 || got.Passages[0].Text != closure {
		t.Fatalf("exact multiword title must rescue complete closure: %+v %v", got, err)
	}
	for _, query := range []string{"Who won the football game?", "Tell me about drive performance", "What are the hours of sunshine?"} {
		got, err = m.Search(ctx, service, "office", query)
		if err != nil || got.Outcome != "no_relevant_information" {
			t.Fatalf("shared word cannot rescue unrelated query %q: %+v %v", query, got, err)
		}
	}
}

func TestRelevanceSelectionCoversMultipartWithoutFiller(t *testing.T) {
	candidates := []searchCandidate{
		{Passage: Passage{SectionID: "optician"}, queryCoverage: 1, lexical: .8, matchedTerms: []string{"sherry", "optician"}, titleTerms: []string{"sherry", "optician"}},
		{Passage: Passage{SectionID: "glasses"}, queryCoverage: 1, lexical: .3, matchedTerms: []string{"avail"}},
		{Passage: Passage{SectionID: "walk-ins"}, queryCoverage: 1, lexical: .5, matchedTerms: []string{"need", "appoint", "adjust"}},
	}
	got := relevantPassages(candidates, 4)
	if len(got) != 2 || got[0].SectionID != "walk-ins" || got[1].SectionID != "optician" {
		t.Fatalf("must cover both needs without unrelated availability: %+v", got)
	}
	got = relevantPassages([]searchCandidate{
		{Passage: Passage{SectionID: "optician-hours"}, queryCoverage: 1, lexical: .02, matchedTerms: []string{"hour"}, titleTerms: []string{"hour"}},
		{Passage: Passage{SectionID: "office-hours"}, queryCoverage: 1, lexical: .05, matchedTerms: []string{"hour"}, titleTerms: []string{"hour"}},
	}, 4)
	if len(got) != 1 || got[0].SectionID != "office-hours" {
		t.Fatalf("generic hours needs strongest topic match, not vector-only first: %+v", got)
	}
	got = relevantPassages([]searchCandidate{{Passage: Passage{SectionID: "semantic-answer"}}, {Passage: Passage{SectionID: "weaker"}}}, 4)
	if len(got) != 2 || got[0].SectionID != "semantic-answer" {
		t.Fatalf("semantic-only query must preserve bounded evidence until relevance is calibrated: %+v", got)
	}
}

func TestRelevanceSelectionPrefersTopicsToIncidentalMentions(t *testing.T) {
	got := relevantPassages([]searchCandidate{
		{Passage: Passage{SectionID: "records-policy"}, queryCoverage: 1, lexical: .8, matchedTerms: []string{"address", "fax"}},
		{Passage: Passage{SectionID: "address"}, queryCoverage: 1, lexical: .4, matchedTerms: []string{"address"}, titleTerms: []string{"address"}},
		{Passage: Passage{SectionID: "fax"}, queryCoverage: 1, lexical: .3, matchedTerms: []string{"fax"}, titleTerms: []string{"fax"}},
	}, 4)
	if len(got) != 2 || got[0].SectionID != "address" || got[1].SectionID != "fax" {
		t.Fatalf("address and fax must not be replaced by a records policy mentioning both: %+v", got)
	}
}

func TestRelevanceSelectionIgnoresHyphenatedTitleCollisions(t *testing.T) {
	got := relevantPassages([]searchCandidate{
		{Passage: Passage{SectionID: "after-hours", Title: "After-hours doctor contact"}, queryCoverage: 1, lexical: .2, matchedTerms: []string{"hour"}, titleTerms: []string{"hour"}},
		{Passage: Passage{SectionID: "hours", Title: "Office hours"}, queryCoverage: 1, lexical: .1, matchedTerms: []string{"hour"}, titleTerms: []string{"hour"}},
	}, 4)
	if len(got) != 1 || got[0].SectionID != "hours" {
		t.Fatalf("hours query must return office hours, not an after-hours title fragment: %+v", got)
	}
	got = relevantPassages([]searchCandidate{
		{Passage: Passage{SectionID: "self-pay", Title: "Self-pay pricing"}, queryCoverage: 1, lexical: .2, matchedTerms: []string{"pay"}, titleTerms: []string{"pay"}},
		{Passage: Passage{SectionID: "billing", Title: "Billing questions"}, queryCoverage: 1, lexical: .1, matchedTerms: []string{"balanc"}},
	}, 4)
	if len(got) != 1 || got[0].SectionID != "self-pay" {
		t.Fatalf("hyphenated title without a standalone rival must still match: %+v", got)
	}
}

func TestRelevanceSelectionKeepsTopRankedTopicBehindBroadEntry(t *testing.T) {
	got := relevantPassages([]searchCandidate{
		{Passage: Passage{SectionID: "hours", Title: "Office hours"}, similarity: .7, queryCoverage: 1, lexical: .1, matchedTerms: []string{"lunch"}},
		{Passage: Passage{SectionID: "optical", Title: "Optical and glasses"}, similarity: .6, queryCoverage: 1, lexical: .3, matchedTerms: []string{"close", "offic"}},
	}, 4)
	if len(got) != 2 || got[0].SectionID != "optical" || got[1].SectionID != "hours" {
		t.Fatalf("closer top-ranked entry adding an uncovered term must not be dropped for a broader entry: %+v", got)
	}
	got = relevantPassages([]searchCandidate{
		{Passage: Passage{SectionID: "hours", Title: "Office hours"}, similarity: .6, queryCoverage: 1, lexical: .1, matchedTerms: []string{"lunch"}},
		{Passage: Passage{SectionID: "optical", Title: "Optical and glasses"}, similarity: .6, queryCoverage: 1, lexical: .3, matchedTerms: []string{"close", "offic"}},
	}, 4)
	if len(got) != 1 || got[0].SectionID != "optical" {
		t.Fatalf("top-ranked entry without a semantic advantage must not be added as filler: %+v", got)
	}
}

func TestRelevanceSelectionKeepsRestoredTopicWithinPassageLimit(t *testing.T) {
	candidates := []searchCandidate{{Passage: Passage{SectionID: "closest", Text: "Closest."}, similarity: .8, queryCoverage: 1, matchedTerms: []string{"lunch"}}}
	for _, id := range []string{"a", "b", "c", "d"} {
		candidates = append(candidates, searchCandidate{Passage: Passage{SectionID: id, Title: id + " topic", Text: id + "."}, similarity: .6, queryCoverage: 1, matchedTerms: []string{id}, titleTerms: []string{id}})
	}
	got := selectPassages(relevantPassages(candidates, 4), 4)
	ids := []string{}
	for _, p := range got {
		ids = append(ids, p.SectionID)
	}
	if len(got) != 4 || !slices.Contains(ids, "closest") {
		t.Fatalf("restored closest topic must survive the passage limit: %v", ids)
	}
}
