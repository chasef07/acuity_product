package migrations_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/insurance"
	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

var updateInsuranceExport = flag.Bool("update-insurance-export", false, "rewrite insurance/testdata/export.json from the migrated v1 fixture")

const (
	exportPractice  = "00000000-0000-0000-0000-000000001601"
	exportRevision  = "00000000-0000-0000-0000-000000001602"
	edgePractice    = "00000000-0000-0000-0000-000000001603"
	edgeRevision    = "00000000-0000-0000-0000-000000001604"
	edgeRestored    = "00000000-0000-0000-0000-000000001605"
	v2ExportPath    = "../insurance/testdata/export.json"
	v1FixturePath   = "testdata/insurance-catalog-v1.json"
	selfPayOffered  = "You can be seen as self-pay."
	edgeEscapedText = "Bring <b>both</b> cards & \"ID\" \\ \u2028line\u2029 \ttab Señor \U0001F453"
)

const edgeCatalog = `{
 "carriers": [
  {"id": "car1", "name": "TIE CARRIER"},
  {"id": "car2", "name": "MAJORITY CARRIER"},
  {"id": "car3", "name": "UNUSED CARRIER"}
 ],
 "regions": [
  {"id": "r1", "plans": [
   {"id": "tie-b", "label": "Tie B", "family": "Tie", "coverage": "medical", "carrierCode": "TIEB", "carrierId": "car1", "selfPay": false, "aliases": null},
   {"id": "tie-a", "label": "Tie A", "family": "Tie", "coverage": "medical", "carrierCode": "TIEA", "carrierId": "car1", "selfPay": false, "aliases": []},
   {"id": "major-1", "label": "Major 1", "family": "Major", "coverage": "medical", "carrierCode": "MAJ01", "carrierId": "car2", "selfPay": false, "aliases": ["Major"]},
   {"id": "blank-code", "label": "Blank code", "family": "Major", "coverage": "medical", "carrierCode": "", "carrierId": "car2", "selfPay": false, "aliases": []}
  ], "tables": [
   {"id": "t1", "offices": ["office-1"], "doctors": ["Dr. a", "Dr. B", "Dr. Ä"], "rows": [
    {"planId": "tie-b", "doctors": {"Dr. Ä": "yes", "Dr. a": "pending", "Dr. B": "no"}, "requires": [{"kind": "prior_authorization", "channel": "availity"}, {"kind": "pcp_referral", "channel": ""}], "onlyOffices": null, "notice": "\u00a0You can be seen as self-pay.\n\u3000", "note": "Synthetic note"},
    {"planId": "tie-a", "doctors": {}, "requires": null, "onlyOffices": [], "notice": "You can be seen as self-pay. Ask about pricing.", "note": ""},
    {"planId": "blank-code", "doctors": {"Dr. a": "yes"}, "requires": [], "onlyOffices": ["office-1"], "notice": EDGE_NOTICE, "note": ""}
   ]}
  ]},
  {"id": "r2", "plans": [
   {"id": "major-2", "label": "Major 2", "family": "Major", "coverage": "routine_vision", "carrierCode": "MAJ01", "carrierId": "car2", "selfPay": false, "aliases": []},
   {"id": "minor", "label": "Minor", "family": "Major", "coverage": "routine_vision", "carrierCode": "MIN01", "carrierId": "car2", "selfPay": false, "aliases": []}
  ], "tables": []}
 ]
}`

func TestInsuranceCatalogV2MigrationConvertsEveryStoredRevision(t *testing.T) {
	ctx := context.Background()
	pool := testdb.OpenThrough(t, "0115_insurance_revisions.sql")
	v1, err := os.ReadFile(v1FixturePath)
	if err != nil {
		t.Fatal(err)
	}
	notice, _ := json.Marshal(edgeEscapedText)
	edge := strings.Replace(edgeCatalog, "EDGE_NOTICE", string(notice), 1)
	if _, err := pool.Exec(ctx, `
		INSERT INTO access_practices(id, provisioning_key, name)
		VALUES ('`+exportPractice+`', 'insurance-v2-export', 'Synthetic export'), ('`+edgePractice+`', 'insurance-v2-edge', 'Synthetic edge');
		INSERT INTO insurance_catalogs(practice_id) VALUES ('`+exportPractice+`'), ('`+edgePractice+`');
	`); err != nil {
		t.Fatal(err)
	}
	for _, revision := range []struct{ id, practice, previous, body string }{
		{exportRevision, exportPractice, "", string(v1)},
		{edgeRevision, edgePractice, "", edge},
		{edgeRestored, edgePractice, edgeRevision, edge},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO insurance_revisions(id, practice_id, catalog, content_hash, reason, created_by, provenance, previous_revision_id)
			VALUES ($1, $2, $3, $4, 'Synthetic v1 revision', 'operator-subject', 'import', NULLIF($5, '')::uuid)
		`, revision.id, revision.practice, revision.body, sha256Hex([]byte(revision.body)), revision.previous); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE insurance_catalogs SET revision_id = CASE practice_id WHEN $1::uuid THEN $2::uuid ELSE $3::uuid END`, exportPractice, exportRevision, edgeRestored); err != nil {
		t.Fatal(err)
	}

	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}

	migrated := map[string]insurance.Catalog{}
	for _, id := range []string{exportRevision, edgeRevision, edgeRestored} {
		var body, hash string
		if err := pool.QueryRow(ctx, `SELECT catalog::text, content_hash FROM insurance_revisions WHERE id = $1`, id).Scan(&body, &hash); err != nil {
			t.Fatal(err)
		}
		catalog, err := insurance.ParseCatalog([]byte(body))
		if err != nil {
			t.Fatalf("revision %s does not parse as a v2 catalog after the migration: %v", id, err)
		}
		encoded, _ := json.Marshal(catalog)
		if got := sha256Hex(encoded); got != hash {
			t.Fatalf("revision %s content_hash = %s, want the hash of Go's encoding %s", id, hash, got)
		}
		migrated[id] = catalog
	}

	exported, _ := json.Marshal(migrated[exportRevision])
	if *updateInsuranceExport {
		if err := os.WriteFile(v2ExportPath, append(exported, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	golden, err := os.ReadFile(v2ExportPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(bytes.TrimSuffix(golden, []byte("\n")), exported) {
		t.Fatalf("%s is not the migrated v1 fixture; rerun with -update-insurance-export", v2ExportPath)
	}

	export := migrated[exportRevision]
	for carrier, code := range map[string]string{"car308175": "HUM03", "car301672": "SELF", "car303033": "HUM02", "car280612": ""} {
		if got := carrierCode(t, export, carrier); got != code {
			t.Errorf("carrier %s code = %q, want %q", carrier, got, code)
		}
	}
	for _, want := range []struct{ region, table, plan, beforeBooking, notice string }{
		{"south_florida", "doctors", "cigna-hmo", "staff_verify", ""},
		{"south_florida", "doctors", "avmed-entrust-medical", "none", ""},
		{"south_florida", "doctors", "cigna-sure-fit-vision", "none", ""},
		{"south_florida", "doctors", "leon-medical-center-health-plan", "", "Leon Medical Center patients schedule through their Leon coordinator."},
	} {
		got := row(t, export, want.region, want.table, want.plan)
		if want.beforeBooking != "" && got.BeforeBooking != want.beforeBooking {
			t.Errorf("%s beforeBooking = %q, want %q", want.plan, got.BeforeBooking, want.beforeBooking)
		}
		if got.Notice != want.notice {
			t.Errorf("%s notice = %q, want %q", want.plan, got.Notice, want.notice)
		}
	}
	steps := map[string]int{}
	for _, region := range export.Regions {
		for _, table := range region.Tables {
			for _, entry := range table.Rows {
				steps[entry.BeforeBooking]++
				if strings.TrimSpace(entry.Notice) == selfPayOffered {
					t.Errorf("%s/%s keeps the hand-typed self-pay notice", table.ID, entry.PlanID)
				}
			}
		}
	}
	if steps["staff_verify"] != 7 || steps["pcp_referral"] != 8 || steps["prior_authorization"] != 7 {
		t.Errorf("before-booking steps = %v, want 7 staff_verify, 8 pcp_referral and 7 prior_authorization", steps)
	}

	for _, id := range []string{edgeRevision, edgeRestored} {
		catalog := migrated[id]
		for carrier, code := range map[string]string{"car1": "TIEA", "car2": "MAJ01", "car3": ""} {
			if got := carrierCode(t, catalog, carrier); got != code {
				t.Errorf("edge carrier %s code = %q, want %q", carrier, got, code)
			}
		}
		for plan, want := range map[string]insurance.TableRow{
			"tie-b":      {BeforeBooking: "prior_authorization", Notice: ""},
			"tie-a":      {BeforeBooking: "none", Notice: selfPayOffered + " Ask about pricing."},
			"blank-code": {BeforeBooking: "none", Notice: edgeEscapedText},
		} {
			got := row(t, catalog, "r1", "t1", plan)
			if got.BeforeBooking != want.BeforeBooking || got.Notice != want.Notice {
				t.Errorf("edge %s = %q / %q, want %q / %q", plan, got.BeforeBooking, got.Notice, want.BeforeBooking, want.Notice)
			}
		}
	}

	revisions, err := insurance.NewRevisions(pool, access.New(pool, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	seed, err := insurance.ParseCatalog(golden)
	if err != nil {
		t.Fatal(err)
	}
	replayed, published, err := revisions.Import(ctx, insurance.ImportCommand{PracticeID: exportPractice, RevisionID: "00000000-0000-0000-0000-000000001606", Reason: "Synthetic reimport", ActorSubject: "operator-subject", Catalog: seed})
	if err != nil || published || replayed.ID != exportRevision {
		t.Fatalf("reimporting the v2 export over its migrated revision: %+v published=%t err=%v", replayed, published, err)
	}
}

func sha256Hex(body []byte) string {
	digest := sha256.Sum256(body)
	return hex.EncodeToString(digest[:])
}

func carrierCode(t *testing.T, catalog insurance.Catalog, id string) string {
	t.Helper()
	for _, carrier := range catalog.Carriers {
		if carrier.ID == id {
			return carrier.Code
		}
	}
	t.Fatalf("no carrier %s", id)
	return ""
}

func row(t *testing.T, catalog insurance.Catalog, regionID, tableID, planID string) insurance.TableRow {
	t.Helper()
	for _, region := range catalog.Regions {
		for _, table := range region.Tables {
			for _, entry := range table.Rows {
				if region.ID == regionID && table.ID == tableID && entry.PlanID == planID {
					return entry
				}
			}
		}
	}
	t.Fatalf("no row %s/%s/%s", regionID, tableID, planID)
	return insurance.TableRow{}
}
