package work_test

import (
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/work"
)

func TestAppointmentReviewKeyUsesUTCMicroseconds(t *testing.T) {
	when := time.Date(2026, 9, 23, 5, 1, 2, 123456789, time.FixedZone("synthetic", -7*60*60))
	key := work.AppointmentReviewKey("interaction-example", when)
	if key != "interaction-example:2026-09-23T12:01:02.123456Z" {
		t.Fatalf("unexpected review key: %q", key)
	}
	task := work.Task{Origin: work.TaskOriginAppointmentReview, SourceReviewKey: key}
	if task.SourceInteractionID() != "interaction-example" {
		t.Fatalf("unexpected source interaction: %q", task.SourceInteractionID())
	}
}

func TestSourceInteractionIDOnlyComesFromAppointmentReviews(t *testing.T) {
	for _, test := range []struct {
		origin    work.TaskOrigin
		key, want string
	}{
		{work.TaskOriginAppointmentReview, "", ""},
		{work.TaskOriginAppointmentReview, ":2026-09-23T12:00:00Z", ""},
		{work.TaskOriginAppointmentReview, "legacy-source", "legacy-source"},
		{work.TaskOriginInboundMessageReview, "interaction-example:2026-09-23T12:00:00Z", ""},
	} {
		task := work.Task{Origin: test.origin, SourceReviewKey: test.key}
		if got := task.SourceInteractionID(); got != test.want {
			t.Errorf("origin=%s key=%q: got %q want %q", test.origin, test.key, got, test.want)
		}
	}
}
