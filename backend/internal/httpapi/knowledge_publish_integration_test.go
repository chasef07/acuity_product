package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/httpapi"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/knowledge"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/chasef07/acuity_product/backend/internal/testaccess"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type switchableEmbedder struct {
	fail   atomic.Bool
	during atomic.Pointer[func()]
}

func (e *switchableEmbedder) Embed(ctx context.Context, texts []string, task knowledge.TaskType) ([][]float32, error) {
	if e.fail.Load() {
		return nil, errors.New("synthetic embedding outage")
	}
	if during := e.during.Swap(nil); during != nil {
		(*during)()
	}
	return constantEmbedder{}.Embed(ctx, texts, task)
}

var syntheticRequiredAbitaEntries = []string{
	"current-address", "phone", "fax", "paperwork-email", "directions", "other-offices",
	"hours", "holiday-closures", "after-hours",
	"providers", "provider-languages", "scope-of-services",
	"appointment-confirmation", "new-patient-visit", "what-to-bring", "referral-requirements",
	"billing", "payments", "self-pay-pricing",
	"medical-records-requests", "prescription-copies", "social-follow-up",
}

func syntheticAbitaSections() []knowledge.Section {
	sections := make([]knowledge.Section, len(syntheticRequiredAbitaEntries))
	for i, id := range syntheticRequiredAbitaEntries {
		sections[i] = knowledge.Section{ID: id, Title: "Synthetic " + id, Text: "Synthetic fact for " + id + "."}
	}
	return sections
}

func syntheticAbitaSectionsOfSize(t *testing.T, total int) []knowledge.Section {
	t.Helper()
	sections := syntheticAbitaSections()
	for i := 1; i <= 5; i++ {
		sections = append(sections, knowledge.Section{ID: fmt.Sprintf("extra-%d", i), Title: fmt.Sprintf("Synthetic extra %d", i), Text: "Synthetic."})
	}
	missing := total
	for _, section := range sections {
		missing -= utf8.RuneCountInString(section.Title) + utf8.RuneCountInString(section.Text)
	}
	for i := range sections {
		add := min(missing, 800-utf8.RuneCountInString(sections[i].Text))
		sections[i].Text += strings.Repeat("x", add)
		missing -= add
	}
	if missing != 0 {
		t.Fatalf("cannot build %d characters of synthetic knowledge", total)
	}
	return sections
}

type publishBody struct {
	PracticeID         string              `json:"practiceId"`
	LocationID         string              `json:"locationId"`
	RevisionID         string              `json:"revisionId"`
	ExpectedRevisionID string              `json:"expectedRevisionId,omitempty"`
	Reason             string              `json:"reason"`
	Sections           []knowledge.Section `json:"sections"`
}

func TestStaffPublishOfficeKnowledgeThroughPortalRuntimeRole(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	ctx := context.Background()
	seedAccess := access.New(db.As(testdb.Portal), nil)
	if _, err := access.New(pool, nil).Provision(ctx, access.Provisioning{
		Environment:       "test",
		RequestedBy:       "knowledge-publish-http-test",
		PlatformOperators: []string{"operator@acuity.test"},
		Practices: []access.PracticeProvision{
			{
				Key:  "abita-eye-group",
				Name: "Synthetic Abita",
				Locations: []access.LocationProvision{
					{Key: "hollywood", Name: "Hollywood", AbitaOfficeKeys: []string{"hollywood"}},
					{Key: "spring-hill", Name: "Spring Hill", AbitaOfficeKeys: []string{"spring-hill"}},
				},
				AccessGrants: []access.AccessGrantProvision{
					{Key: "hollywood-staff", Email: "staff@abita.test", Role: access.RoleStaff, LocationScope: access.LocationScopeSelected, SelectedLocationKeys: []string{"hollywood"}},
				},
			},
			{
				Key:  "synthetic-demo",
				Name: "Synthetic demo",
				Locations: []access.LocationProvision{
					{Key: "demo", Name: "Demo", AbitaOfficeKeys: []string{"demo-office"}},
				},
				AccessGrants: []access.AccessGrantProvision{
					{Key: "demo-admin", Email: "admin@demo.test", Role: access.RoleAdmin, LocationScope: access.LocationScopeAll},
				},
			},
		},
	}); err != nil {
		t.Fatalf("provision knowledge publish fixture: %v", err)
	}
	staff := access.Identity{Subject: "staff-subject", Email: "staff@abita.test", EmailVerified: true}
	demoAdmin := access.Identity{Subject: "demo-admin-subject", Email: "admin@demo.test", EmailVerified: true}
	operator := access.Identity{Subject: "operator-subject", Email: "operator@acuity.test", EmailVerified: true}
	staffAuthorization := testaccess.Activate(t, seedAccess, staff)
	demoAuthorization := testaccess.Activate(t, seedAccess, demoAdmin)
	if _, err := seedAccess.DiscoverActor(ctx, operator); err != nil {
		t.Fatalf("activate operator: %v", err)
	}
	abita := staffAuthorization.Practice.ID
	demo := demoAuthorization.Practice.ID
	locations := map[string]string{}
	for _, row := range []struct{ practice, key string }{{abita, "hollywood"}, {abita, "spring-hill"}, {demo, "demo"}} {
		var id string
		if err := pool.QueryRow(ctx, `SELECT id::text FROM access_locations WHERE practice_id=$1 AND provisioning_key=$2`, row.practice, row.key).Scan(&id); err != nil {
			t.Fatal(err)
		}
		locations[row.key] = id
	}

	embedder := &switchableEmbedder{}
	seedKnowledge, err := knowledge.New(db.As(testdb.Portal), seedAccess, embedder, knowledge.Config{})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := seedKnowledge.ReplaceCorpus(ctx, knowledge.ImportCommand{
		ID: uuid.NewString(), PracticeID: abita, OfficeKey: "hollywood",
		Sections:   append(syntheticAbitaSections(), knowledge.Section{ID: "sunglasses", Title: "Sunglasses", Text: "Synthetic sunglasses are sold here."}),
		Provenance: "approved synthetic fixture", Reason: "initial import", ActorSubject: "synthetic-operator",
	})
	if err != nil {
		t.Fatalf("import initial knowledge: %v", err)
	}

	runtimePool := db.As(testdb.Portal)

	accessModule := access.New(runtimePool, nil)
	knowledgeModule, err := knowledge.New(runtimePool, accessModule, embedder, knowledge.Config{SearchEmbeddingTimeout: 500 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	serviceAuthenticator, err := access.NewServiceAuthenticator(
		access.ServiceCredential{Token: "abita-agent-token", Identity: access.ServiceIdentity{Subject: "abita-agent", PracticeID: abita, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityReadKnowledge}}},
		access.ServiceCredential{Token: "demo-agent-token", Identity: access.ServiceIdentity{Subject: "demo-agent", PracticeID: demo, LocationScope: access.LocationScopeAll, Capabilities: []access.ServiceCapability{access.ServiceCapabilityReadKnowledge}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	workModule := work.New(runtimePool, accessModule, nil)
	handler, err := httpapi.NewPortal(httpapi.Config{AcquireTimeout: time.Second, RequestTimeout: 5 * time.Second}, runtimePool, httpapi.PortalDependencies{
		Access:               accessModule,
		Authenticator:        staticAuthenticator{"staff-token": staff, "demo-token": demoAdmin, "operator-token": operator},
		Calling:              humancalling.New(runtimePool, accessModule, httpCallingProvider{}, humancalling.Config{}, nil),
		Interactions:         interaction.New(runtimePool, accessModule, nil),
		Messaging:            messaging.New(runtimePool, accessModule, workModule, nil, messaging.Config{}, nil),
		Work:                 workModule,
		Workspace:            workspace.New(runtimePool, accessModule),
		Knowledge:            knowledgeModule,
		ServiceAuthenticator: serviceAuthenticator,
	})
	if err != nil {
		t.Fatalf("create portal: %v", err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	publish := func(token string, body publishBody) (int, string) {
		t.Helper()
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		response := request(t, server.Client(), http.MethodPost, server.URL+"/v1/knowledge/revisions", token, payload)
		defer response.Body.Close()
		return response.StatusCode, readBody(t, response)
	}
	active := func(practice, office string) string {
		t.Helper()
		var revision string
		if err := pool.QueryRow(ctx, `SELECT revision_id::text FROM knowledge_corpora WHERE practice_id=$1 AND office_key=$2`, practice, office).Scan(&revision); err != nil {
			t.Fatal(err)
		}
		return revision
	}
	search := func(office string) knowledge.SearchResult {
		t.Helper()
		payload := []byte(`{"query":"When does the office close?"}`)
		req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/agent/knowledge/search", strings.NewReader(string(payload)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer abita-agent-token")
		req.Header.Set("X-Office-Key", office)
		req.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		var result knowledge.SearchResult
		decode(t, response, &result)
		return result
	}
	hollywood := func(sections []knowledge.Section) publishBody {
		return publishBody{PracticeID: abita, LocationID: locations["hollywood"], RevisionID: uuid.NewString(), ExpectedRevisionID: initial.ID, Reason: "Correct closing time", Sections: sections}
	}

	edited := syntheticAbitaSections()
	edited[6].Text = "The synthetic office now closes at half past five."
	edited = append([]knowledge.Section{{ID: "parking", Title: "Parking", Text: "Synthetic free parking is behind the building."}}, edited...)
	first := hollywood(edited)

	t.Run("staff without access to the Location is denied", func(t *testing.T) {
		body := first
		body.LocationID = locations["spring-hill"]
		if status, response := publish("staff-token", body); status != http.StatusForbidden {
			t.Fatalf("status=%d body=%s", status, response)
		}
		if active(abita, "hollywood") != initial.ID {
			t.Fatal("denied publish changed the active revision")
		}
	})

	for _, tc := range []struct {
		name, entry string
		change      func([]knowledge.Section) []knowledge.Section
	}{
		{"required entry removed", "hours", func(s []knowledge.Section) []knowledge.Section {
			return slices.DeleteFunc(s, func(x knowledge.Section) bool { return x.ID == "hours" })
		}},
		{"entry too long", "billing", func(s []knowledge.Section) []knowledge.Section {
			s[16].Text = strings.Repeat("a", 801)
			return s
		}},
		{"heading in text", "payments", func(s []knowledge.Section) []knowledge.Section {
			s[17].Text = "Synthetic payments.\n## Cards"
			return s
		}},
		{"duplicate title", "fax", func(s []knowledge.Section) []knowledge.Section {
			s[2].Title = s[1].Title
			return s
		}},
	} {
		t.Run("Abita standard rejects "+tc.name, func(t *testing.T) {
			status, response := publish("staff-token", hollywood(tc.change(syntheticAbitaSections())))
			if status != http.StatusBadRequest || !strings.Contains(response, `\"`+tc.entry+`\"`) || !strings.Contains(response, `"entryId":"`+tc.entry+`"`) {
				t.Fatalf("status=%d body=%s", status, response)
			}
			if active(abita, "hollywood") != initial.ID {
				t.Fatal("rejected publish changed the active revision")
			}
		})
	}

	t.Run("Abita knowledge over 20,000 characters is rejected with its size and the limit, and at the limit it publishes", func(t *testing.T) {
		springHill := func(total int) publishBody {
			return publishBody{PracticeID: abita, LocationID: locations["spring-hill"], RevisionID: uuid.NewString(), Reason: "Synthetic size check", Sections: syntheticAbitaSectionsOfSize(t, total)}
		}
		status, response := publish("operator-token", springHill(20001))
		if status != http.StatusBadRequest || !strings.Contains(response, `"code":"KNOWLEDGE_TOO_LARGE"`) || !strings.Contains(response, "20001 characters") || !strings.Contains(response, "keep it to 20000") {
			t.Fatalf("status=%d body=%s", status, response)
		}
		var corpora int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM knowledge_corpora WHERE practice_id=$1 AND office_key='spring-hill'`, abita).Scan(&corpora); err != nil || corpora != 0 {
			t.Fatalf("rejected publish created a corpus: count=%d err=%v", corpora, err)
		}
		body := springHill(20000)
		if status, response := publish("operator-token", body); status != http.StatusOK {
			t.Fatalf("status=%d body=%s", status, response)
		}
		if active(abita, "spring-hill") != body.RevisionID {
			t.Fatal("publish at the limit did not switch the active revision")
		}
	})

	t.Run("missing revision ID is rejected", func(t *testing.T) {
		body := first
		body.RevisionID = uuid.Nil.String()
		if status, response := publish("staff-token", body); status != http.StatusBadRequest {
			t.Fatalf("status=%d body=%s", status, response)
		}
	})

	t.Run("embedding failure leaves the previous revision searchable", func(t *testing.T) {
		embedder.fail.Store(true)
		status, response := publish("staff-token", hollywood(edited))
		embedder.fail.Store(false)
		if status != http.StatusServiceUnavailable || !strings.Contains(response, "Nothing changed") {
			t.Fatalf("status=%d body=%s", status, response)
		}
		if active(abita, "hollywood") != initial.ID {
			t.Fatal("failed publish changed the active revision")
		}
		if result := search("hollywood"); result.RevisionID == nil || *result.RevisionID != initial.ID {
			t.Fatalf("search after failed publish=%+v", result)
		}
	})

	t.Run("access revoked while embedding blocks the publish", func(t *testing.T) {
		revoke := func() {
			if _, err := pool.Exec(ctx, `UPDATE access_memberships SET revoked_at=now() WHERE user_subject=$1`, staff.Subject); err != nil {
				t.Error(err)
			}
		}
		embedder.during.Store(&revoke)
		status, response := publish("staff-token", hollywood(edited))
		if _, err := pool.Exec(ctx, `UPDATE access_memberships SET revoked_at=NULL WHERE user_subject=$1`, staff.Subject); err != nil {
			t.Fatal(err)
		}
		if status != http.StatusForbidden {
			t.Fatalf("status=%d body=%s", status, response)
		}
		if active(abita, "hollywood") != initial.ID {
			t.Fatal("revoked member changed the active revision")
		}
	})

	t.Run("search embedding slower than its timeout fails the search", func(t *testing.T) {
		slowEmbedding := func() { time.Sleep(1500 * time.Millisecond) }
		embedder.during.Store(&slowEmbedding)
		if result := search("hollywood"); result.Outcome != "temporary_failure" {
			t.Fatalf("slow search=%+v", result)
		}
	})

	t.Run("staff publish slower than the search embedding timeout is live for the agent and attributed", func(t *testing.T) {
		slowEmbedding := func() { time.Sleep(1500 * time.Millisecond) }
		embedder.during.Store(&slowEmbedding)
		status, response := publish("staff-token", first)
		if status != http.StatusOK {
			t.Fatalf("status=%d body=%s", status, response)
		}
		var revision struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(response), &revision); err != nil || revision.ID != first.RevisionID {
			t.Fatalf("revision=%s err=%v", response, err)
		}
		if active(abita, "hollywood") != first.RevisionID {
			t.Fatal("publish did not switch the active revision")
		}
		result := search("hollywood")
		if result.RevisionID == nil || *result.RevisionID != first.RevisionID {
			t.Fatalf("agent search=%+v", result)
		}
		var createdBy, provenance, reason, previous string
		if err := pool.QueryRow(ctx, `SELECT created_by, provenance, reason, previous_revision_id::text FROM knowledge_revisions WHERE id=$1`, first.RevisionID).Scan(&createdBy, &provenance, &reason, &previous); err != nil {
			t.Fatal(err)
		}
		if createdBy != staff.Subject || provenance != "portal" || reason != first.Reason || previous != initial.ID {
			t.Fatalf("attribution=%s %s %s %s", createdBy, provenance, reason, previous)
		}
		rows, err := pool.Query(ctx, `SELECT section_id FROM knowledge_passages WHERE revision_id=$1 ORDER BY position`, first.RevisionID)
		if err != nil {
			t.Fatal(err)
		}
		order, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
		want := make([]string, len(edited))
		for i, s := range edited {
			want[i] = s.ID
		}
		if !slices.Equal(order, want) {
			t.Fatalf("order=%v want=%v", order, want)
		}
	})

	t.Run("repeating the same revision is idempotent", func(t *testing.T) {
		status, response := publish("staff-token", first)
		if status != http.StatusOK || !strings.Contains(response, first.RevisionID) {
			t.Fatalf("status=%d body=%s", status, response)
		}
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM knowledge_revisions WHERE practice_id=$1 AND office_key='hollywood'`, abita).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 2 {
			t.Fatalf("revisions=%d", count)
		}
	})

	t.Run("stale expected revision conflicts", func(t *testing.T) {
		status, response := publish("staff-token", hollywood(syntheticAbitaSections()))
		if status != http.StatusConflict {
			t.Fatalf("status=%d body=%s", status, response)
		}
		if active(abita, "hollywood") != first.RevisionID {
			t.Fatal("conflicting publish changed the active revision")
		}
	})

	t.Run("platform operator publishes under their own identity", func(t *testing.T) {
		body := hollywood(syntheticAbitaSections())
		body.ExpectedRevisionID = first.RevisionID
		if status, response := publish("operator-token", body); status != http.StatusOK {
			t.Fatalf("status=%d body=%s", status, response)
		}
		var createdBy string
		if err := pool.QueryRow(ctx, `SELECT created_by FROM knowledge_revisions WHERE id=$1`, body.RevisionID).Scan(&createdBy); err != nil {
			t.Fatal(err)
		}
		if createdBy != operator.Subject {
			t.Fatalf("created_by=%s", createdBy)
		}
	})

	t.Run("other practices are not held to the Abita standard but are held to the 20,000 character limit", func(t *testing.T) {
		body := publishBody{PracticeID: demo, LocationID: locations["demo"], RevisionID: uuid.NewString(), Reason: "First demo facts"}
		for i := range 4 {
			body.Sections = append(body.Sections, knowledge.Section{ID: fmt.Sprintf("topic-%d", i), Title: fmt.Sprintf("Topic %d", i), Text: strings.Repeat("Demo fact. ", 545)})
		}
		if status, response := publish("demo-token", body); status != http.StatusBadRequest || !strings.Contains(response, `"code":"KNOWLEDGE_TOO_LARGE"`) {
			t.Fatalf("status=%d body=%s", status, response)
		}
		body.Sections = body.Sections[:3]
		if status, response := publish("demo-token", body); status != http.StatusOK {
			t.Fatalf("status=%d body=%s", status, response)
		}
		if active(demo, "demo-office") != body.RevisionID {
			t.Fatal("demo publish did not switch the active revision")
		}
	})

	type historyRow struct {
		ID                 string     `json:"id"`
		Version            int        `json:"version"`
		CreatedAt          time.Time  `json:"createdAt"`
		Author             string     `json:"author"`
		Provenance         string     `json:"provenance"`
		Reason             string     `json:"reason"`
		PreviousRevisionID *string    `json:"previousRevisionId"`
		Active             bool       `json:"active"`
		LastAgentSearchAt  *time.Time `json:"lastAgentSearchAt"`
		AgentSearchCount   int        `json:"agentSearchCount"`
	}
	type revisionView struct {
		LocationID string `json:"locationId"`
		Revision   *struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
		} `json:"revision"`
		Sections []knowledge.Section `json:"sections"`
	}
	post := func(token, path string, body any) (int, string) {
		t.Helper()
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		response := request(t, server.Client(), http.MethodPost, server.URL+path, token, payload)
		defer response.Body.Close()
		return response.StatusCode, readBody(t, response)
	}
	history := func(t *testing.T) []historyRow {
		t.Helper()
		status, response := post("staff-token", "/v1/knowledge/revisions/query", map[string]string{"practiceId": abita, "locationId": locations["hollywood"]})
		if status != http.StatusOK {
			t.Fatalf("history status=%d body=%s", status, response)
		}
		var body struct {
			LocationID string       `json:"locationId"`
			Revisions  []historyRow `json:"revisions"`
		}
		if err := json.Unmarshal([]byte(response), &body); err != nil || body.LocationID != locations["hollywood"] {
			t.Fatalf("history=%s err=%v", response, err)
		}
		return body.Revisions
	}
	readRevision := func(t *testing.T, token, location, revision string) (int, revisionView) {
		t.Helper()
		status, response := post(token, "/v1/knowledge/revisions/read", map[string]string{"practiceId": abita, "locationId": location, "revisionId": revision})
		var view revisionView
		if status == http.StatusOK {
			if err := json.Unmarshal([]byte(response), &view); err != nil {
				t.Fatal(err)
			}
		}
		return status, view
	}

	t.Run("history lists every revision newest first with version, author, and agent use", func(t *testing.T) {
		latest := active(abita, "hollywood")
		rows := history(t)
		var ids, authors, reasons []string
		var versions []int
		for _, row := range rows {
			ids, versions, authors, reasons = append(ids, row.ID), append(versions, row.Version), append(authors, row.Author), append(reasons, row.Reason)
			if row.Active != (row.ID == latest) {
				t.Errorf("revision %s active=%v", row.ID, row.Active)
			}
			var count int
			var last *time.Time
			if err := pool.QueryRow(ctx, `SELECT count(*), max(created_at) FROM knowledge_retrieval_observations WHERE revision_id=$1`, row.ID).Scan(&count, &last); err != nil {
				t.Fatal(err)
			}
			if row.AgentSearchCount != count || (last == nil) != (row.LastAgentSearchAt == nil) || (last != nil && !last.Equal(*row.LastAgentSearchAt)) {
				t.Errorf("revision %s agent use=%d %v; want %d %v", row.ID, row.AgentSearchCount, row.LastAgentSearchAt, count, last)
			}
		}
		if !slices.Equal(ids, []string{latest, first.RevisionID, initial.ID}) || !slices.Equal(versions, []int{3, 2, 1}) {
			t.Fatalf("history ids=%v versions=%v", ids, versions)
		}
		if !slices.Equal(authors, []string{"operator@acuity.test", "staff@abita.test", "Unknown user"}) || !slices.Equal(reasons, []string{"Correct closing time", "Correct closing time", "initial import"}) {
			t.Fatalf("history authors=%v reasons=%v", authors, reasons)
		}
		if rows[0].Provenance != "portal" || rows[2].Provenance != "approved synthetic fixture" || rows[2].PreviousRevisionID != nil || *rows[1].PreviousRevisionID != initial.ID || *rows[0].PreviousRevisionID != first.RevisionID {
			t.Fatalf("history provenance and lineage=%+v", rows)
		}
		if rows[1].AgentSearchCount == 0 || rows[0].AgentSearchCount != 0 || rows[0].LastAgentSearchAt != nil {
			t.Fatalf("agent use not distinguished=%+v", rows)
		}
	})

	t.Run("history and revision reads are limited to the Location's office", func(t *testing.T) {
		if status, response := post("staff-token", "/v1/knowledge/revisions/query", map[string]string{"practiceId": abita, "locationId": locations["spring-hill"]}); status != http.StatusForbidden {
			t.Fatalf("other Location history status=%d body=%s", status, response)
		}
		if status, response := post("demo-token", "/v1/knowledge/revisions/query", map[string]string{"practiceId": abita, "locationId": locations["hollywood"]}); status != http.StatusForbidden {
			t.Fatalf("other Practice history status=%d body=%s", status, response)
		}
		if status, _ := readRevision(t, "staff-token", locations["spring-hill"], initial.ID); status != http.StatusForbidden {
			t.Fatalf("other Location revision read status=%d", status)
		}
		if status, _ := readRevision(t, "staff-token", locations["hollywood"], active(demo, "demo-office")); status != http.StatusBadRequest {
			t.Fatalf("other office revision read status=%d", status)
		}
	})

	t.Run("revision read returns that revision's entries in order", func(t *testing.T) {
		status, view := readRevision(t, "staff-token", locations["hollywood"], initial.ID)
		want := append(syntheticAbitaSections(), knowledge.Section{ID: "sunglasses", Title: "Sunglasses", Text: "Synthetic sunglasses are sold here."})
		if status != http.StatusOK || view.LocationID != locations["hollywood"] || view.Revision == nil || view.Revision.ID != initial.ID || view.Revision.Version != 1 || !slices.Equal(view.Sections, want) {
			t.Fatalf("status=%d view=%+v", status, view)
		}
	})

	t.Run("restoring an earlier revision publishes it as the next version", func(t *testing.T) {
		before := active(abita, "hollywood")
		_, old := readRevision(t, "staff-token", locations["hollywood"], initial.ID)
		restore := publishBody{PracticeID: abita, LocationID: locations["hollywood"], RevisionID: uuid.NewString(), ExpectedRevisionID: before, Reason: "Restore version 1", Sections: old.Sections}
		status, response := publish("staff-token", restore)
		var published struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
		}
		if status != http.StatusOK || json.Unmarshal([]byte(response), &published) != nil || published.ID != restore.RevisionID || published.Version != 4 {
			t.Fatalf("status=%d body=%s", status, response)
		}
		status, current := post("staff-token", "/v1/knowledge/query", map[string]string{"practiceId": abita, "locationId": locations["hollywood"]})
		var view revisionView
		if status != http.StatusOK || json.Unmarshal([]byte(current), &view) != nil || view.Revision == nil || view.Revision.ID != restore.RevisionID || view.Revision.Version != 4 || !slices.Equal(view.Sections, old.Sections) {
			t.Fatalf("current status=%d body=%s", status, current)
		}
		if result := search("hollywood"); result.RevisionID == nil || *result.RevisionID != restore.RevisionID {
			t.Fatalf("agent search after restore=%+v", result)
		}
		rows := history(t)
		if len(rows) != 4 || rows[0].ID != restore.RevisionID || rows[0].Version != 4 || !rows[0].Active || rows[0].Reason != "Restore version 1" || *rows[0].PreviousRevisionID != before || rows[0].AgentSearchCount != 1 || rows[3].ID != initial.ID {
			t.Fatalf("history after restore=%+v", rows)
		}
	})

	t.Run("portal role cannot rewrite or delete published knowledge", func(t *testing.T) {
		for _, statement := range []string{
			`UPDATE knowledge_revisions SET reason='rewritten'`,
			`UPDATE knowledge_passages SET text='rewritten'`,
			`DELETE FROM knowledge_passages`,
			`DELETE FROM knowledge_revisions`,
			`UPDATE knowledge_corpora SET office_key='other'`,
		} {
			if _, err := runtimePool.Exec(ctx, statement); err == nil {
				t.Fatalf("portal role may run %s", statement)
			}
		}
	})
}
