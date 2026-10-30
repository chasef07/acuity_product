package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestPortalPublishesInsuranceRevisionsButNeverRewritesThem(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	var practice string
	if err := db.Owner.QueryRow(ctx, `INSERT INTO access_practices(provisioning_key,name) VALUES('insurance-grants','Synthetic Practice') RETURNING id::text`).Scan(&practice); err != nil {
		t.Fatal(err)
	}
	portal := db.As(testdb.Portal)
	first, second := uuid.NewString(), uuid.NewString()
	hash := strings.Repeat("a", 64)
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO insurance_catalogs(practice_id) VALUES($1)`, []any{practice}},
		{`INSERT INTO insurance_revisions(id,practice_id,catalog,content_hash,reason,created_by,provenance) VALUES($1,$2,'{"carriers":[],"regions":[]}',$3,'seed','synthetic-operator','import')`, []any{first, practice, hash}},
		{`UPDATE insurance_catalogs SET revision_id=$2 WHERE practice_id=$1`, []any{practice, first}},
		{`INSERT INTO insurance_revisions(id,practice_id,catalog,content_hash,reason,created_by,provenance,previous_revision_id) VALUES($1,$2,'{"carriers":[],"regions":[]}',$3,'edit','synthetic-staff','portal',$4)`, []any{second, practice, hash, first}},
		{`UPDATE insurance_catalogs SET revision_id=$2 WHERE practice_id=$1`, []any{practice, second}},
	} {
		if _, err := portal.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatalf("portal %s: %v", statement.sql, err)
		}
	}
	for _, statement := range []string{
		`UPDATE insurance_revisions SET reason='rewritten' WHERE id='` + first + `'`,
		`UPDATE insurance_revisions SET catalog='{"carriers":[],"regions":[]}' WHERE id='` + first + `'`,
		`DELETE FROM insurance_revisions WHERE id='` + first + `'`,
		`DELETE FROM insurance_catalogs WHERE practice_id='` + practice + `'`,
		`UPDATE insurance_catalogs SET practice_id=practice_id WHERE practice_id='` + practice + `'`,
	} {
		_, err := portal.Exec(ctx, statement)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Errorf("portal %s: err = %v, want permission denied", statement, err)
		}
	}
	var reason, active string
	if err := db.Owner.QueryRow(ctx, `SELECT r.reason, c.revision_id::text FROM insurance_revisions r JOIN insurance_catalogs c ON c.practice_id=r.practice_id WHERE r.id=$1`, first).Scan(&reason, &active); err != nil || reason != "seed" || active != second {
		t.Fatalf("revision after denied writes: reason=%q active=%q err=%v", reason, active, err)
	}
	other := uuid.NewString()
	if _, err := portal.Exec(ctx, `INSERT INTO insurance_revisions(id,practice_id,catalog,content_hash,reason,created_by,provenance) VALUES($1,$2,'{}',$3,'bad','synthetic-staff','manual')`, other, practice, hash); err == nil {
		t.Fatal("accepted a provenance outside import, portal, restore")
	}
}
