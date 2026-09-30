package work_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/chasef07/acuity_product/backend/internal/work"
)

func TestTaskClassificationCoversEveryOriginAndUrgency(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	folders := map[work.TaskOrigin]string{
		work.TaskOriginHumanCallFollowUp:    "follow_up",
		work.TaskOriginAbitaAI:              "follow_up",
		work.TaskOriginStaffMessageFollowUp: "follow_up",
		work.TaskOriginAppointmentReview:    "appointments",
		work.TaskOriginInboundMessageReview: "texts",
		work.TaskOriginMissedCall:           "calls",
		work.TaskOriginVoicemail:            "calls",
	}
	var definition string
	if err := pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint
 WHERE conrelid='work_tasks'::regclass AND conname='work_tasks_origin_check'`).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	stored := regexp.MustCompile(`'([A-Z_]+)'`).FindAllStringSubmatch(definition, -1)
	if len(stored) != len(folders) {
		t.Fatalf("stored origins %q do not match classified origins %v", definition, folders)
	}
	for _, match := range stored {
		origin := work.TaskOrigin(match[1])
		folder, ok := folders[origin]
		if !ok {
			t.Fatalf("stored origin %q has no folder", origin)
		}
		var calls, texts, appointments, review, followUp bool
		if err := pool.QueryRow(ctx, `SELECT `+work.TaskIsCallRecoverySQL+`,`+work.TaskIsTextReviewSQL+`,
 `+work.TaskIsAppointmentReviewSQL+`,`+work.TaskIsCommunicationReviewSQL+`,`+work.TaskIsFollowUpSQL+`
 FROM (SELECT $1::text AS origin) task`, origin).Scan(&calls, &texts, &appointments, &review, &followUp); err != nil {
			t.Fatal(err)
		}
		if calls != (folder == "calls") || texts != (folder == "texts") ||
			appointments != (folder == "appointments") || followUp != (folder == "follow_up") ||
			review == followUp {
			t.Fatalf("origin %q classified as calls=%v texts=%v appointments=%v review=%v followUp=%v, want %s",
				origin, calls, texts, appointments, review, followUp, folder)
		}
	}
	for want, urgency := range []work.TaskUrgency{
		work.TaskUrgencyHighPriority, work.TaskUrgencyNormal, work.TaskUrgencyNonUrgent,
	} {
		var rank int
		if err := pool.QueryRow(ctx, `SELECT `+work.TaskUrgencyRankSQL+` FROM (SELECT $1::text AS urgency) task`, urgency).Scan(&rank); err != nil {
			t.Fatal(err)
		}
		if rank != want || urgency.Rank() != want {
			t.Fatalf("urgency %q ranks SQL=%d cursor=%d, want %d", urgency, rank, urgency.Rank(), want)
		}
	}
}
