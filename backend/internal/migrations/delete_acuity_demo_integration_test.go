package migrations_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

var practiceScopedTables = []string{
	"access_locations", "access_abita_office_locations", "access_audit_events",
	"human_calling_location_voice_numbers", "messaging_location_configurations",
	"human_calling_calls", "human_calling_timeline", "human_calling_voicemails",
	"work_tasks", "messaging_threads", "messaging_messages", "messaging_attachments",
	"messaging_blocked_senders", "ai_interactions", "knowledge_corpora", "knowledge_revisions",
	"insurance_catalogs", "insurance_revisions",
}

var callScopedTables = []string{
	"human_calling_call_legs", "human_calling_provider_commands", "human_calling_provider_receipts",
}

func seedPractice(key string, n int, phoneSuffix string) string {
	id := func(suffix string) string { return fmt.Sprintf("'00000000-0000-0000-0127-%08d%s'", n, suffix) }
	replacer := strings.NewReplacer(
		"{P}", id("0001"), "{L}", id("0002"), "{C}", id("0003"), "{G}", id("0004"),
		"{TA}", id("0005"), "{TB}", id("0006"), "{TH}", id("0007"), "{M}", id("0008"),
		"{AT}", id("0009"), "{AI}", id("0010"), "{KR}", id("0011"), "{IR}", id("0012"),
		"{KEY}", key, "{N}", fmt.Sprint(n), "{PH}", phoneSuffix,
	)
	return replacer.Replace(`
INSERT INTO access_practices(id, provisioning_key, name) VALUES ({P}, '{KEY}', 'Synthetic {KEY}');
INSERT INTO access_locations(id, practice_id, provisioning_key, name) VALUES ({L}, {P}, '{KEY}-main', 'Main');
INSERT INTO access_abita_office_locations(practice_id, office_key, location_id) VALUES ({P}, '{KEY}-office', {L});
INSERT INTO access_audit_events(actor_type, actor_subject, practice_id, action) VALUES ('PROVISIONER', 'test', {P}, 'access.practice_provisioned');
INSERT INTO human_calling_location_voice_numbers(practice_id, location_id, phone) VALUES ({P}, {L}, '+1727555{PH}');
INSERT INTO messaging_location_configurations(practice_id, location_id, sender, messaging_profile_id) VALUES ({P}, {L}, '+1727555{PH}', 'profile-{N}');
WITH task AS (
	INSERT INTO work_tasks(id, practice_id, location_id, phone, title, state, origin, urgency, call_id, created_by_kind, created_by_subject, created_by_email, created_at, updated_at)
	VALUES ({TA}, {P}, {L}, '+1813555{PH}', 'Call back', 'OPEN', 'HUMAN_CALL_FOLLOW_UP', 'normal', {C}, 'HUMAN', 'staff', 'staff@example.test', now(), now())
)
INSERT INTO human_calling_calls(id, practice_id, location_id, direction, entry_point, caller_phone, task_id)
VALUES ({C}, {P}, {L}, 'INBOUND', 'STANDALONE', '+1813555{PH}', {TA});
INSERT INTO human_calling_call_legs(id, call_id, role, sequence, state) VALUES ({G}, {C}, 'CALLER', 1, 'PENDING');
INSERT INTO human_calling_provider_commands(call_id, call_leg_id, action) VALUES ({C}, {G}, 'ANSWER_CALLER');
INSERT INTO human_calling_provider_receipts(event_id, call_id, event_type, signature_timestamp, raw_body) VALUES ('event-{N}', {C}, 'call.initiated', 1, '\x00');
INSERT INTO human_calling_timeline(call_id, practice_id, kind, occurred_at) VALUES ({C}, {P}, 'call.created', now());
INSERT INTO human_calling_voicemails(call_id, practice_id, location_id, task_id, outcome, audio_state, created_at, updated_at) VALUES ({C}, {P}, {L}, {TA}, 'MISSED_CALL', 'UNAVAILABLE', now(), now());
INSERT INTO work_task_activities(task_id, task_version, kind, actor_kind, actor_subject, actor_email, occurred_at) VALUES ({TA}, 1, 'TASK_CREATED', 'HUMAN', 'staff', 'staff@example.test', now());
INSERT INTO messaging_threads(id, practice_id, location_id, office_phone, external_phone) VALUES ({TH}, {P}, {L}, '+1727555{PH}', '+1813555{PH}');
WITH message AS (
	INSERT INTO messaging_messages(id, thread_id, practice_id, location_id, direction, sender, destination, delivery_state, body, task_id)
	VALUES ({M}, {TH}, {P}, {L}, 'INBOUND', '+1813555{PH}', '+1727555{PH}', 'DELIVERED', 'Hello', {TB})
)
INSERT INTO work_tasks(id, practice_id, location_id, phone, title, state, origin, urgency, source_message_id, message_thread_id, created_by_kind, created_by_subject, created_by_email, created_at, updated_at)
VALUES ({TB}, {P}, {L}, '+1813555{PH}', 'Text back', 'OPEN', 'STAFF_MESSAGE_FOLLOW_UP', 'normal', {M}, {TH}, 'HUMAN', 'staff', 'staff@example.test', now(), now());
INSERT INTO work_text_replies(message_id, task_id, task_version, actor_subject, actor_email) VALUES ({M}, {TB}, 1, 'staff', 'staff@example.test');
INSERT INTO messaging_attachments(id, practice_id, location_id, direction, state, file_name, content_type, byte_size, object_key, actor_subject)
VALUES ({AT}, {P}, {L}, 'OUTBOUND', 'STORED', 'scan.png', 'image/png', 10, 'attachments/{KEY}.png', 'staff');
INSERT INTO messaging_blocked_senders(practice_id, phone, blocked_by_email, blocked_at) VALUES ({P}, '+1813555{PH}', 'staff@example.test', now());
INSERT INTO ai_interactions(id, service_subject, practice_id, location_id, source_call_id, phone, office_phone, started_at, ended_at, status, lifecycle_stage, transcript, closeout_payload)
VALUES ({AI}, 'agent', {P}, {L}, 'source-{N}', '+1813555{PH}', '+1727555{PH}', now() - interval '1 hour', now(), 'COMPLETED', 3, '{}', '{}');
INSERT INTO ai_interaction_issues(interaction_id, reported_by, note) VALUES ({AI}, 'staff', 'Wrong hours');
WITH corpus AS (
	INSERT INTO knowledge_corpora(practice_id, office_key, revision_id) VALUES ({P}, '{KEY}-office', {KR})
)
INSERT INTO knowledge_revisions(id, practice_id, office_key, content_hash, provenance, reason, created_by)
VALUES ({KR}, {P}, '{KEY}-office', 'hash', 'import', 'seed', 'test');
INSERT INTO knowledge_passages(revision_id, section_id, title, text) VALUES ({KR}, 'hours', 'Hours', 'Open 9 to 5');
WITH catalog AS (
	INSERT INTO insurance_catalogs(practice_id, revision_id) VALUES ({P}, {IR})
)
INSERT INTO insurance_revisions(id, practice_id, catalog, content_hash, reason, created_by, provenance)
VALUES ({IR}, {P}, '{}', repeat('a', 64), 'seed', 'test', 'import');
`)
}

func TestDeleteAcuityDemoPracticeRemovesOnlyTheDemo(t *testing.T) {
	pool := testdb.OpenThrough(t, "0125_text_routing_and_blocked_senders.sql")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, seedPractice("acuity-demo", 1, "0101")+seedPractice("abita-eye-group", 2, "0202")); err != nil {
		t.Fatalf("seed practices: %v", err)
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}

	count := func(table, practiceID string) int {
		t.Helper()
		var rows int
		column := "practice_id"
		if table == "access_practices" {
			column = "id"
		}
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE `+column+` = $1`, practiceID).Scan(&rows); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return rows
	}
	callCount := func(table, callID string) int {
		t.Helper()
		var rows int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE call_id = $1`, callID).Scan(&rows); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		return rows
	}
	const demo, abita = "00000000-0000-0000-0127-000000010001", "00000000-0000-0000-0127-000000020001"
	const demoCall, abitaCall = "00000000-0000-0000-0127-000000010003", "00000000-0000-0000-0127-000000020003"
	for _, table := range append([]string{"access_practices"}, practiceScopedTables...) {
		if rows := count(table, demo); rows != 0 {
			t.Errorf("%s kept %d demo rows", table, rows)
		}
		if rows := count(table, abita); rows == 0 {
			t.Errorf("%s lost the Abita rows", table)
		}
	}
	for _, table := range callScopedTables {
		if rows := callCount(table, demoCall); rows != 0 {
			t.Errorf("%s kept %d demo rows", table, rows)
		}
		if rows := callCount(table, abitaCall); rows != 1 {
			t.Errorf("%s has %d Abita rows, want 1", table, rows)
		}
	}
	var survivors int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM work_task_activities) + (SELECT count(*) FROM work_text_replies)
			+ (SELECT count(*) FROM ai_interaction_issues) + (SELECT count(*) FROM knowledge_passages)
	`).Scan(&survivors); err != nil || survivors != 4 {
		t.Fatalf("child rows = %d, want the 4 Abita rows: %v", survivors, err)
	}
	var cleanupKeys []string
	rows, err := pool.Query(ctx, `SELECT object_key FROM messaging_attachment_cleanup ORDER BY object_key`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			t.Fatal(err)
		}
		cleanupKeys = append(cleanupKeys, key)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(cleanupKeys, ",") != "attachments/acuity-demo.png" {
		t.Fatalf("attachment cleanup intents = %v, want only the demo object", cleanupKeys)
	}
}

func TestDeleteAcuityDemoPracticeIgnoresDatabasesWithoutIt(t *testing.T) {
	pool := testdb.OpenThrough(t, "0125_text_routing_and_blocked_senders.sql")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, seedPractice("abita-eye-group", 2, "0202")); err != nil {
		t.Fatalf("seed practice: %v", err)
	}
	if err := migrations.Apply(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var practices int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM access_practices`).Scan(&practices); err != nil || practices != 1 {
		t.Fatalf("practices = %d: %v", practices, err)
	}
}
