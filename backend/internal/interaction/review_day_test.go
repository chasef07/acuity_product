package interaction

import (
	"testing"
	"time"
)

func TestReviewDayRunsFromFourPMEasternToFourPMEastern(t *testing.T) {
	eastern, _ := time.LoadLocation("America/New_York")
	at := func(value string) time.Time {
		parsed, err := time.ParseInLocation("2006-01-02 15:04", value, eastern)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	for name, test := range map[string]struct {
		date     string
		in, out  []string
		duration time.Duration
	}{
		"ordinary day": {"2026-10-12", []string{"2026-10-11 16:00", "2026-10-12 15:59"}, []string{"2026-10-11 15:59", "2026-10-12 16:00"}, 24 * time.Hour},
		"DST ends":     {"2026-11-01", []string{"2026-10-31 16:00", "2026-11-01 15:59"}, []string{"2026-11-01 16:00"}, 25 * time.Hour},
		"DST starts":   {"2026-03-08", []string{"2026-03-07 16:00", "2026-03-08 15:59"}, []string{"2026-03-08 16:00"}, 23 * time.Hour},
	} {
		from, to, valid := reviewDay(test.date)
		if !valid || to.Sub(from) != test.duration {
			t.Errorf("%s: %v to %v", name, from, to)
		}
		for _, value := range test.in {
			if moment := at(value); moment.Before(from) || !moment.Before(to) {
				t.Errorf("%s: %s belongs to review day %s", name, value, test.date)
			}
		}
		for _, value := range test.out {
			if moment := at(value); !moment.Before(from) && moment.Before(to) {
				t.Errorf("%s: %s does not belong to review day %s", name, value, test.date)
			}
		}
	}
	if _, _, valid := reviewDay("2026-13-01"); valid {
		t.Error("an invalid date is not a review day")
	}
	if got := latestReviewDay(at("2026-10-12 15:59")).Format("2006-01-02"); got != "2026-10-11" {
		t.Errorf("at 3:59 PM the newest open review day is yesterday: %s", got)
	}
	if got := latestReviewDay(at("2026-10-12 16:00")).Format("2006-01-02"); got != "2026-10-12" {
		t.Errorf("at 4:00 PM today's review day opens: %s", got)
	}
}
