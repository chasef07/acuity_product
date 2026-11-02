package migrations_test

import (
	"context"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestInboundAttachmentSizeMigrationRequeuesUnavailableCopies(t *testing.T) {
	ctx := context.Background()
	pool := testdb.OpenThrough(t, "0117_insurance_gap_reviews.sql")
	if _, err := pool.Exec(ctx, `
 INSERT INTO access_practices(id,provisioning_key,name) VALUES('00000000-0000-0000-0000-000000000101','attachment-size','Synthetic');
 INSERT INTO access_locations(id,practice_id,provisioning_key,name) VALUES('00000000-0000-0000-0000-000000000102','00000000-0000-0000-0000-000000000101','main','Main');
 INSERT INTO messaging_threads(id,practice_id,location_id,office_phone,external_phone,created_at,updated_at) VALUES('00000000-0000-0000-0000-000000000103','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000102','+15555550100','+15555550123','2026-01-01','2026-01-01');
 INSERT INTO messaging_messages(id,thread_id,practice_id,location_id,direction,body,sender,destination,delivery_state,provider_message_id,created_at,updated_at)
 VALUES('00000000-0000-0000-0000-000000000104','00000000-0000-0000-0000-000000000103','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000102','INBOUND','Photo','+15555550123','+15555550100','DELIVERED','synthetic-mms','2026-01-01','2026-01-01');
 INSERT INTO messaging_attachments(id,practice_id,location_id,message_id,direction,state,file_name,content_type,provider_media_url,created_at,updated_at)
 VALUES('00000000-0000-0000-0000-000000000105','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000102','00000000-0000-0000-0000-000000000104','INBOUND','UNAVAILABLE','attachment.jpg','image/jpeg','https://media.example/photo','2026-01-01','2026-01-01');
 `); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := pool.QueryRow(ctx, `SELECT state FROM messaging_attachments WHERE id='00000000-0000-0000-0000-000000000105'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "PROCESSING" {
		t.Fatalf("unavailable inbound copy state = %s, want PROCESSING", state)
	}
	if _, err := pool.Exec(ctx, `UPDATE messaging_attachments SET byte_size=1000498 WHERE id='00000000-0000-0000-0000-000000000105'`); err != nil {
		t.Fatalf("inbound photo over the outbound limit rejected: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE messaging_attachments SET byte_size=5242881 WHERE id='00000000-0000-0000-0000-000000000105'`); err == nil {
		t.Fatal("inbound copy over 5 MiB accepted")
	}
}
