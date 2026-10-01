package architecture

import (
	"go/parser"
	"maps"
	"testing"
)

func TestScanSQLClassifiesTableUse(t *testing.T) {
	owners := map[string]string{}
	for _, table := range []string{"access_audit_events", "ai_interactions", "human_calling_calls", "messaging_messages", "messaging_threads", "work_tasks", "work_text_replies"} {
		owners[table] = "fixture"
	}
	for _, test := range []struct {
		sql  string
		want map[string]access
	}{
		{`SELECT id FROM work_tasks`, map[string]access{"work_tasks": read}},
		{`INSERT INTO access_audit_events (action) VALUES ('work_tasks')`, map[string]access{"access_audit_events": write}},
		{`UPDATE work_tasks task SET state = 'DONE' FROM human_calling_calls call WHERE call.id = task.call_id`, map[string]access{"work_tasks": write, "human_calling_calls": read}},
		{`DELETE FROM public.messaging_threads USING work_tasks`, map[string]access{"messaging_threads": write, "work_tasks": read}},
		{`INSERT INTO work_tasks (id) VALUES ($1) ON CONFLICT (id) DO UPDATE SET id = EXCLUDED.id`, map[string]access{"work_tasks": write}},
		{`SELECT reply.task_id FROM work_text_replies reply JOIN messaging_messages message ON message.id = reply.message_id FOR UPDATE OF reply`, map[string]access{"work_text_replies": lock, "messaging_messages": read}},
		{`SELECT 1 FROM messaging_threads AS thread, work_tasks task FOR SHARE OF thread, task`, map[string]access{"messaging_threads": lock, "work_tasks": lock}},
		{`SELECT id FROM messaging_threads WHERE id = $1 FOR NO KEY UPDATE`, map[string]access{"messaging_threads": lock}},
		{`SELECT 1 FROM work_tasks FOR UPDATE; SELECT 1 FROM ai_interactions`, map[string]access{"work_tasks": lock, "ai_interactions": read}},
		{`LOCK TABLE work_tasks`, map[string]access{"work_tasks": lock}},
		{`SELECT task.ai_interactions, work_task_id -- work_tasks
		  FROM "messaging_threads" /* human_calling_calls */`, map[string]access{"messaging_threads": read}},
		{`load work tasks: %w`, map[string]access{}},
	} {
		if got := scanSQL(test.sql, owners); !maps.Equal(got, test.want) {
			t.Errorf("scanSQL(%q) = %v, want %v", test.sql, got, test.want)
		}
	}
}

func TestStringConstantJoinsLiteralConcatenation(t *testing.T) {
	expr, err := parser.ParseExpr("`SELECT 1 ` + (\"FROM \" + \"work_tasks\")")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := stringConstant(expr); !ok || got != "SELECT 1 FROM work_tasks" {
		t.Fatalf("stringConstant = %q, %v", got, ok)
	}
	expr, err = parser.ParseExpr(`"SELECT 1 FROM " + table`)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := stringConstant(expr); ok {
		t.Fatal("stringConstant accepted a non-constant operand")
	}
}
