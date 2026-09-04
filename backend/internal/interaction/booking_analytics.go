package interaction

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
	_ "time/tzdata"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
)

// Booking analytics returns only aggregates. Operator evidence is a separate API.
type QueryBookingAnalyticsCommand struct {
	Identity   access.Identity
	PracticeID string
	LocationID string
	Days       int
	TimeZone   string
}

type BookingMetrics struct {
	Calls               int      `json:"calls"`
	Bookings            int      `json:"bookings"`
	Searched            int      `json:"searched"`
	Converted           int      `json:"converted"`
	Conversion          *float64 `json:"conversion"`
	P50                 *float64 `json:"p50"`
	P90                 *float64 `json:"p90"`
	DurationSamples     int      `json:"durationSamples"`
	SearchEvidenceCalls int      `json:"searchEvidenceCalls"`
	PreciseSearchCalls  int      `json:"preciseSearchCalls"`
}

type BookingGroups struct {
	New      BookingMetrics `json:"new"`
	Existing BookingMetrics `json:"existing"`
	Unknown  BookingMetrics `json:"unknown"`
}

type BookingDay struct {
	Day   string         `json:"day"`
	Total BookingMetrics `json:"total"`
	BookingGroups
}

type BookingAnalytics struct {
	From     string         `json:"from"`
	Through  string         `json:"through"`
	TimeZone string         `json:"timeZone"`
	Total    BookingMetrics `json:"total"`
	Groups   BookingGroups  `json:"groups"`
	Daily    []BookingDay   `json:"daily"`
}

type bookingFact struct {
	started       time.Time
	ended         *time.Time
	booked        bool
	appointmentID string
	countBooking  bool
	searched      bool
	searchKnown   bool
	searchPrecise bool
	patientGroup  string
}

func (m *Module) QueryBookingAnalytics(ctx context.Context, command QueryBookingAnalyticsCommand) (BookingAnalytics, error) {
	zone, zoneErr := time.LoadLocation(command.TimeZone)
	if m.database == nil || m.access == nil || !validUUID(command.PracticeID) ||
		(command.LocationID != "" && !validUUID(command.LocationID)) ||
		(command.Days != 7 && command.Days != 30 && command.Days != 90) ||
		command.TimeZone == "" || command.TimeZone == "Local" || zoneErr != nil {
		return BookingAnalytics{}, ErrInvalidInput
	}
	now := m.now().In(zone)
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, zone)
	from := to.AddDate(0, 0, -command.Days)
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return BookingAnalytics{}, fmt.Errorf("begin booking analytics: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL statement_timeout = '1500ms'; SET LOCAL lock_timeout = '100ms'; SET LOCAL max_parallel_workers_per_gather = 0; SET LOCAL work_mem = '4MB'`); err != nil {
		return BookingAnalytics{}, err
	}
	authorization, err := m.access.LockReadAuthorization(ctx, tx, command.Identity, command.PracticeID, command.LocationID)
	if errors.Is(err, access.ErrDenied) {
		return BookingAnalytics{}, ErrDenied
	}
	if err != nil {
		return BookingAnalytics{}, err
	}
	if !authorization.PlatformOperator && authorization.Membership.Role != access.RoleAdmin {
		return BookingAnalytics{}, ErrDenied
	}
	locations := authorizedLocationIDs(authorization, command.LocationID)
	if len(locations) == 0 {
		return BookingAnalytics{}, ErrDenied
	}

	// Read only stored facts maintained alongside source evidence. Transcript JSON
	// is never parsed on this path. Fail visibly rather than truncating a report.
	rows, err := tx.Query(ctx, `
        SELECT started_at, ended_at, booking_confirmed, COALESCE(new_appointment_id, ''),
            booking_searched, booking_search_known,
            COALESCE(booking_search_precise, false), booking_patient_group
        FROM ai_interactions
        WHERE practice_id = $1::uuid AND location_id = ANY($2::uuid[])
            AND started_at >= $3 AND started_at < $4
            AND status <> 'IN_PROGRESS' AND lifecycle_stage = 3
        ORDER BY started_at, id
        LIMIT 50001
	`, command.PracticeID, locations, from, to)
	if err != nil {
		return BookingAnalytics{}, fmt.Errorf("query booking analytics: %w", err)
	}
	facts := make([]bookingFact, 0)
	for rows.Next() {
		var fact bookingFact
		if err := rows.Scan(&fact.started, &fact.ended, &fact.booked, &fact.appointmentID, &fact.searched, &fact.searchKnown, &fact.searchPrecise, &fact.patientGroup); err != nil {
			rows.Close()
			return BookingAnalytics{}, fmt.Errorf("read booking analytics: %w", err)
		}
		facts = append(facts, fact)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return BookingAnalytics{}, fmt.Errorf("read booking analytics: %w", err)
	}
	if len(facts) > 50000 {
		return BookingAnalytics{}, fmt.Errorf("booking analytics exceeds bounded reporting window")
	}
	if err := tx.Commit(ctx); err != nil {
		return BookingAnalytics{}, fmt.Errorf("commit booking analytics: %w", err)
	}
	return summarizeBookingFacts(facts, from, to), nil
}

type bookingAccumulator struct {
	metrics   BookingMetrics
	durations []float64
}

func (a *bookingAccumulator) add(f bookingFact) {
	a.metrics.Calls++
	if f.searchKnown || f.searched {
		a.metrics.SearchEvidenceCalls++
	}
	if f.searched {
		a.metrics.Searched++
		if f.searchPrecise {
			a.metrics.PreciseSearchCalls++
		}
		if f.booked {
			a.metrics.Converted++
		}
	}
	if !f.booked {
		return
	}
	if f.countBooking {
		a.metrics.Bookings++
	}
	end := f.ended
	if end == nil || end.Before(f.started) {
		return
	}
	a.durations = append(a.durations, end.Sub(f.started).Seconds())
}

func (a *bookingAccumulator) finish() BookingMetrics {
	result := a.metrics
	if result.Searched > 0 {
		value := float64(result.Converted) / float64(result.Searched) * 100
		result.Conversion = &value
	}
	sort.Float64s(a.durations)
	result.DurationSamples = len(a.durations)
	result.P50 = bookingPercentile(a.durations, .5)
	result.P90 = bookingPercentile(a.durations, .9)
	return result
}

func bookingPercentile(sorted []float64, fraction float64) *float64 {
	if len(sorted) == 0 {
		return nil
	}
	index := float64(len(sorted)-1) * fraction
	lower, upper := int(math.Floor(index)), int(math.Ceil(index))
	value := sorted[lower] + (sorted[upper]-sorted[lower])*(index-float64(lower))
	return &value
}

func summarizeBookingFacts(facts []bookingFact, from, to time.Time) BookingAnalytics {
	newGroups := func() map[string]*bookingAccumulator {
		return map[string]*bookingAccumulator{"new": {}, "existing": {}, "unknown": {}}
	}
	finishGroups := func(groups map[string]*bookingAccumulator) BookingGroups {
		return BookingGroups{New: groups["new"].finish(), Existing: groups["existing"].finish(), Unknown: groups["unknown"].finish()}
	}
	total := &bookingAccumulator{}
	groups := newGroups()
	daily := map[string]map[string]*bookingAccumulator{}
	dailyTotals := map[string]*bookingAccumulator{}
	days := []string{}
	for day := from; day.Before(to); day = day.AddDate(0, 0, 1) {
		key := day.Format(time.DateOnly)
		days = append(days, key)
		daily[key] = newGroups()
		dailyTotals[key] = &bookingAccumulator{}
	}
	seenBookings := map[string]bool{}
	for _, fact := range facts {
		day := fact.started.In(from.Location()).Format(time.DateOnly)
		if daily[day] == nil {
			continue
		}
		if fact.booked && fact.appointmentID != "" && !seenBookings[fact.appointmentID] {
			fact.countBooking = true
			seenBookings[fact.appointmentID] = true
		}
		total.add(fact)
		dailyTotals[day].add(fact)
		groups[fact.patientGroup].add(fact)
		daily[day][fact.patientGroup].add(fact)
	}
	result := BookingAnalytics{
		From: from.Format(time.DateOnly), Through: to.AddDate(0, 0, -1).Format(time.DateOnly),
		TimeZone: from.Location().String(),
		Total:    total.finish(), Groups: finishGroups(groups), Daily: []BookingDay{},
	}
	for _, day := range days {
		result.Daily = append(result.Daily, BookingDay{Day: day, Total: dailyTotals[day].finish(), BookingGroups: finishGroups(daily[day])})
	}
	return result
}
