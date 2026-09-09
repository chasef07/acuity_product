package messaging_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/messaging"
	"github.com/google/uuid"
)

func TestRecentTextAttentionCountsPagesAndClearsOnlyEligibleUserScope(t *testing.T) {
	f := newAutomaticAcknowledgementTestFixture(t, false)
	ctx := context.Background()
	otherLocation := uuid.NewString()
	if _, err := f.pool.Exec(ctx, `INSERT INTO access_locations(id,practice_id,provisioning_key,name) VALUES($1,$2,'other','Other')`, otherLocation, f.practiceID); err != nil {
		t.Fatal(err)
	}
	add := func(phone, location string, at time.Time, unread bool) (string, string) {
		t.Helper()
		thread, message := uuid.NewString(), uuid.NewString()
		if _, err := f.pool.Exec(ctx, `INSERT INTO messaging_threads(id,practice_id,location_id,office_phone,external_phone) VALUES($1,$2,$3,'+15550000000',$4)`, thread, f.practiceID, location, phone); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(ctx, `INSERT INTO messaging_messages(id,thread_id,practice_id,location_id,direction,body,sender,destination,delivery_state,created_at,updated_at) VALUES($1,$2,$3,$4,'INBOUND','Synthetic inbound',$5,'+15550000000','DELIVERED',$6,$6)`, message, thread, f.practiceID, location, phone, at); err != nil {
			t.Fatal(err)
		}
		if unread {
			if _, err := f.pool.Exec(ctx, `INSERT INTO messaging_thread_unreads(thread_id,user_subject,unread_since,latest_message_id) VALUES($1,$2,$3,$4),($1,'other-user',$3,$4)`, thread, f.identity.Subject, at, message); err != nil {
				t.Fatal(err)
			}
		}
		return thread, message
	}
	// Read threads used to consume page one, leaving a misleading small badge.
	for i := 0; i < 36; i++ {
		add(fmt.Sprintf("+155501%05d", i), f.locationID, f.now, false)
	}
	for i := 0; i < 64; i++ {
		add(fmt.Sprintf("+155502%05d", i), f.locationID, f.now.Add(-time.Duration(i+1)*time.Minute), true)
	}
	add("+15550200000", otherLocation, f.now.Add(-2*time.Minute), true) // Same phone must not split across pages.
	oldThread, _ := add("+15550300000", f.locationID, f.now.Add(-7*24*time.Hour-time.Microsecond), true)
	_, coveredMessage := add("+15550300001", f.locationID, f.now, true)
	task, _, err := f.module.CreateFollowUpTask(ctx, messaging.CreateFollowUpTaskCommand{Identity: f.identity, MessageID: coveredMessage, Title: "Synthetic durable follow-up"})
	if err != nil {
		t.Fatal(err)
	}
	boundaryThread, _ := add("+15550300002", f.locationID, f.now.Add(-7*24*time.Hour), true)
	// A recent outgoing reply must not revive an old inbound message.
	if _, err := f.pool.Exec(ctx, `INSERT INTO messaging_messages(thread_id,practice_id,location_id,direction,body,sender,destination,delivery_state,created_by_subject,created_at,updated_at) VALUES($1,$2,$3,'OUTBOUND','Synthetic reply','+15550000000','+15550300000','SENT',$4,$5,$5)`, oldThread, f.practiceID, f.locationID, f.identity.Subject, f.now); err != nil {
		t.Fatal(err)
	}
	query := messaging.QueryThreadsCommand{Identity: f.identity, PracticeID: f.practiceID, RecentAttention: true, Limit: 10}
	phones := map[string]bool{}
	pages := 0
	for {
		page, err := f.module.QueryThreads(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		if page.Total == nil || *page.Total != 65 {
			t.Fatalf("total=%v, want 65", page.Total)
		}
		thisPage := map[string]bool{}
		for _, item := range page.Items {
			if phones[item.ExternalPhone] {
				t.Fatal("phone group split across pages")
			}
			thisPage[item.ExternalPhone] = true
			if item.ID == oldThread {
				t.Fatal("outbound activity revived an expired thread")
			}
		}
		for phone := range thisPage {
			phones[phone] = true
		}
		if pages == 0 && len(page.Items) != 11 {
			t.Fatalf("first grouped page has %d threads, want 11", len(page.Items))
		}
		pages++
		if page.NextCursor == "" {
			break
		}
		query.Cursor = page.NextCursor
	}
	if len(phones) != 65 || pages != 7 {
		t.Fatalf("phones=%d pages=%d", len(phones), pages)
	}
	denied := access.Identity{Subject: "outsider", Email: "outsider@example.test", EmailVerified: true}
	if err := f.module.MarkRecentRead(ctx, denied, f.practiceID, ""); !errors.Is(err, messaging.ErrDenied) {
		t.Fatalf("unauthorized bulk read=%v", err)
	}
	if err := f.module.MarkRecentRead(ctx, f.identity, f.practiceID, f.locationID); err != nil {
		t.Fatal(err)
	}
	query.Cursor = ""
	page, err := f.module.QueryThreads(ctx, query)
	if err != nil || page.Total == nil || *page.Total != 1 {
		t.Fatalf("remaining other Location total=%v err=%v", page.Total, err)
	}
	if err := f.module.MarkRecentRead(ctx, f.identity, f.practiceID, ""); err != nil {
		t.Fatal(err)
	}
	page, err = f.module.QueryThreads(ctx, query)
	if err != nil || page.Total == nil || *page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("bulk total=%v err=%v", page.Total, err)
	}
	var otherUnread, oldUnread, boundaryUnread int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FILTER(WHERE user_subject='other-user'),count(*) FILTER(WHERE user_subject=$1 AND thread_id=$2),count(*) FILTER(WHERE user_subject=$1 AND thread_id=$3) FROM messaging_thread_unreads`, f.identity.Subject, oldThread, boundaryThread).Scan(&otherUnread, &oldUnread, &boundaryUnread); err != nil {
		t.Fatal(err)
	}
	if otherUnread != 68 || oldUnread != 1 || boundaryUnread != 0 {
		t.Fatalf("other=%d old=%d boundary=%d", otherUnread, oldUnread, boundaryUnread)
	}
	var state string
	if err := f.pool.QueryRow(ctx, `SELECT state FROM work_tasks WHERE id=$1`, task.ID).Scan(&state); err != nil || state != "OPEN" {
		t.Fatalf("Task state=%s err=%v", state, err)
	}
	// A new patient text revives an expired conversation without restoring old rows.
	newMessage := uuid.NewString()
	if _, err := f.pool.Exec(ctx, `INSERT INTO messaging_messages(id,thread_id,practice_id,location_id,direction,body,sender,destination,delivery_state,created_at,updated_at) VALUES($1,$2,$3,$4,'INBOUND','New synthetic question','+15550300000','+15550000000','DELIVERED',$5,$5)`, newMessage, oldThread, f.practiceID, f.locationID, f.now); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE messaging_thread_unreads SET latest_message_id=$1 WHERE thread_id=$2`, newMessage, oldThread); err != nil {
		t.Fatal(err)
	}
	page, err = f.module.QueryThreads(ctx, query)
	if err != nil || page.Total == nil || *page.Total != 1 || page.Items[0].ID != oldThread {
		t.Fatalf("revived total=%v err=%v", page.Total, err)
	}
	*f.clock = f.now.Add(8 * 24 * time.Hour)
	page, err = f.module.QueryThreads(ctx, query)
	if err != nil || page.Total == nil || *page.Total != 0 {
		t.Fatalf("expired total=%v err=%v", page.Total, err)
	}
	history, err := f.module.QueryThreads(ctx, messaging.QueryThreadsCommand{Identity: f.identity, PracticeID: f.practiceID, Search: "+15550300000"})
	if err != nil || len(history.Items) != 1 {
		t.Fatalf("history lost: %v", err)
	}
}
