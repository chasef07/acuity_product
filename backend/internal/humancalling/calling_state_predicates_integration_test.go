package humancalling_test

import (
	"context"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestCallingStateETagIncludesNewVoicemailGreeting(t *testing.T) {
	db := testdb.Open(t)
	pool := db.Owner
	ctx := context.Background()
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(db.As(testdb.Portal), func() time.Time { return now })
	authorization, identities := provisionConcurrentStaff(t, db.Owner, accessModule, now, "voicemail-etag", 1)
	calling := humancalling.New(db.As(testdb.Portal), accessModule, nil, humancalling.Config{}, func() time.Time { return now })
	if _, err := calling.AcquireSoftphone(ctx, identities[0], "voicemail-etag-session", false); err != nil {
		t.Fatal(err)
	}
	before, _, err := calling.ReadCallingStateConditionally(ctx, identities[0], "")
	if err != nil {
		t.Fatal(err)
	}
	if before.ETag == "" || before.Voicemail != nil {
		t.Fatalf("initial state: %#v", before)
	}

	var callID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO human_calling_calls (practice_id, location_id, direction, entry_point, created_at, updated_at)
		VALUES ($1, $2, 'INBOUND', 'STANDALONE', $3, $3) RETURNING id::text
	`, authorization.Practice.ID, authorization.Locations[0].ID, now).Scan(&callID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_call_legs (call_id, role, sequence, state)
		VALUES ($1, 'CALLER', 1, 'ANSWERED')
	`, callID); err != nil {
		t.Fatal(err)
	}
	if _, unchanged, err := calling.ReadCallingStateConditionally(ctx, identities[0], before.ETag); err != nil || !unchanged {
		t.Fatalf("call without voicemail command: unchanged=%t err=%v", unchanged, err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_provider_commands (call_id, action, target_id, state)
		VALUES ($1, 'SPEAK_VOICEMAIL', 'synthetic-voicemail', 'PENDING')
	`, callID); err != nil {
		t.Fatal(err)
	}
	after, unchanged, err := calling.ReadCallingStateConditionally(ctx, identities[0], before.ETag)
	if err != nil || unchanged || after.ETag == before.ETag {
		t.Fatalf("new greeting: unchanged=%t etag=%q err=%v", unchanged, after.ETag, err)
	}
	if after.Voicemail == nil || after.Voicemail.CallID != callID || after.Voicemail.State != "VOICEMAIL_GREETING" {
		t.Fatalf("new greeting not included: %#v", after.Voicemail)
	}
	if _, unchanged, err := calling.ReadCallingStateConditionally(ctx, identities[0], after.ETag); err != nil || !unchanged {
		t.Fatalf("unchanged greeting: unchanged=%t err=%v", unchanged, err)
	}
}
