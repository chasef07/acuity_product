package interaction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type MessageKind string

const (
	MessageStart             MessageKind = "START"
	MessageSummary           MessageKind = "SUMMARY"
	MessageCloseout          MessageKind = "CLOSEOUT"
	MessageOutcomeCheckpoint MessageKind = "OUTCOME_CHECKPOINT"
)

type CallStatus string

const (
	CallInProgress CallStatus = "IN_PROGRESS"
	CallCompleted  CallStatus = "COMPLETED"
	CallEscalated  CallStatus = "ESCALATED"
	CallFailed     CallStatus = "FAILED"
)

type AppointmentAction string

const (
	AppointmentBooked      AppointmentAction = "BOOKED"
	AppointmentCancelled   AppointmentAction = "CANCELLED"
	AppointmentRescheduled AppointmentAction = "RESCHEDULED"
)

type AppointmentOutcome string

const (
	OutcomeBooking       AppointmentOutcome = "BOOKING"
	OutcomeCancellation  AppointmentOutcome = "CANCELLATION"
	OutcomeReschedule    AppointmentOutcome = "RESCHEDULE"
	OutcomePartial       AppointmentOutcome = "PARTIAL"
	OutcomeIndeterminate AppointmentOutcome = "INDETERMINATE"
)

type UpsertStatus string

const (
	StatusCreated UpsertStatus = "created"
	StatusUpdated UpsertStatus = "updated"
)

var (
	ErrDenied       = errors.New("AI Interaction access denied")
	ErrInvalidInput = errors.New("invalid AI Interaction input")
	ErrConflict     = errors.New("AI Interaction source conflict")
	canonicalPhone  = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)
)

type AppointmentEvidence struct {
	Action             AppointmentAction
	OccurredAt         time.Time
	ExternalPatientID  string
	OldAppointmentID   string
	NewAppointmentID   string
	BookingResult      json.RawMessage
	CancellationResult json.RawMessage
}

type IngestCommand struct {
	Service         access.ServiceIdentity
	Kind            MessageKind
	OfficeKey       string
	SourceCallID    string
	CallerPhone     string
	OfficePhone     string
	StartedAt       time.Time
	EndedAt         *time.Time
	Status          CallStatus
	Summary         string
	Transcript      json.RawMessage
	Appointment     *AppointmentEvidence
	SummaryPayload  json.RawMessage
	CloseoutPayload json.RawMessage
}

type Interaction struct {
	ID                    string
	ServiceSubject        string
	PracticeID            string
	LocationID            string
	LocationName          string
	SourceCallID          string
	Phone                 string
	OfficePhone           string
	ExternalPatientID     string
	StartedAt             time.Time
	EndedAt               *time.Time
	Status                CallStatus
	Summary               string
	Transcript            json.RawMessage
	AppointmentOutcome    AppointmentOutcome
	AppointmentOccurredAt *time.Time
	OldAppointmentID      string
	NewAppointmentID      string
	BookingResult         json.RawMessage
	CancellationResult    json.RawMessage
	OutcomeCompleteness   int
	SummaryPayload        json.RawMessage
	CloseoutPayload       json.RawMessage
	Completeness          int
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type QueryDailyOutcomesCommand struct {
	Identity   access.Identity
	PracticeID string
	LocationID string
	Date       time.Time
}

type OutcomeItem struct {
	ID                    string
	LocationID            string
	LocationName          string
	SourceCallID          string
	Phone                 string
	ExternalPatientID     string
	StartedAt             time.Time
	EndedAt               *time.Time
	Status                CallStatus
	Summary               string
	AppointmentOutcome    AppointmentOutcome
	AppointmentOccurredAt *time.Time
	OldAppointmentID      string
	NewAppointmentID      string
}

type OutcomeCounts struct {
	Bookings      int
	Cancellations int
	Reschedules   int
	Partial       int
	Indeterminate int
}

type DailyOutcomes struct {
	Date   time.Time
	Counts OutcomeCounts
	Items  []OutcomeItem
}

func (m *Module) Read(
	ctx context.Context,
	identity access.Identity,
	interactionID string,
) (Interaction, error) {
	if m.pool == nil || m.access == nil {
		return Interaction{}, ErrInvalidInput
	}
	if _, err := uuid.Parse(strings.TrimSpace(interactionID)); err != nil {
		return Interaction{}, ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Interaction{}, fmt.Errorf("begin AI Interaction read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	stored, err := scanInteraction(tx.QueryRow(ctx, interactionSelect+`
		WHERE interaction.id = $1
	`, interactionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Interaction{}, ErrDenied
	}
	if err != nil {
		return Interaction{}, fmt.Errorf("read AI Interaction: %w", err)
	}
	if _, err := m.access.LockReadAuthorization(
		ctx,
		tx,
		identity,
		stored.PracticeID,
		stored.LocationID,
	); err != nil {
		return Interaction{}, ErrDenied
	}
	if err := tx.Commit(ctx); err != nil {
		return Interaction{}, fmt.Errorf("commit AI Interaction read: %w", err)
	}
	return stored, nil
}

func (m *Module) QueryDailyOutcomes(
	ctx context.Context,
	command QueryDailyOutcomesCommand,
) (DailyOutcomes, error) {
	command.PracticeID = strings.TrimSpace(command.PracticeID)
	command.LocationID = strings.TrimSpace(command.LocationID)
	date := command.Date.UTC()
	if m.pool == nil || m.access == nil || command.PracticeID == "" ||
		date.IsZero() || date.Hour() != 0 || date.Minute() != 0 ||
		date.Second() != 0 || date.Nanosecond() != 0 {
		return DailyOutcomes{}, ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return DailyOutcomes{}, fmt.Errorf("begin AI outcome query: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorization, err := m.access.LockReadAuthorization(
		ctx,
		tx,
		command.Identity,
		command.PracticeID,
		command.LocationID,
	)
	if err != nil {
		return DailyOutcomes{}, ErrDenied
	}
	locationIDs := make([]string, 0, len(authorization.Locations))
	if command.LocationID != "" {
		locationIDs = append(locationIDs, command.LocationID)
	} else {
		for _, location := range authorization.Locations {
			locationIDs = append(locationIDs, location.ID)
		}
	}
	if len(locationIDs) == 0 {
		return DailyOutcomes{}, ErrDenied
	}
	rows, err := tx.Query(ctx, `
		SELECT
			interaction.id::text,
			interaction.location_id::text,
			location.name,
			interaction.source_call_id,
			interaction.phone,
			COALESCE(interaction.external_patient_id, ''),
			interaction.started_at,
			interaction.ended_at,
			interaction.status,
			COALESCE(interaction.summary, ''),
			interaction.appointment_outcome,
			interaction.appointment_occurred_at,
			COALESCE(interaction.old_appointment_id, ''),
			COALESCE(interaction.new_appointment_id, '')
		FROM ai_interactions interaction
		JOIN access_locations location
			ON location.practice_id = interaction.practice_id
			AND location.id = interaction.location_id
		WHERE interaction.practice_id = $1
			AND interaction.location_id::text = ANY($2::text[])
			AND interaction.started_at >= $3
			AND interaction.started_at < $4
			AND interaction.status <> 'IN_PROGRESS'
		ORDER BY interaction.started_at, interaction.id
	`, command.PracticeID, locationIDs, date, date.AddDate(0, 0, 1))
	if err != nil {
		return DailyOutcomes{}, fmt.Errorf("query daily AI outcomes: %w", err)
	}
	defer rows.Close()
	page := DailyOutcomes{Date: date, Items: []OutcomeItem{}}
	for rows.Next() {
		var item OutcomeItem
		if err := rows.Scan(
			&item.ID,
			&item.LocationID,
			&item.LocationName,
			&item.SourceCallID,
			&item.Phone,
			&item.ExternalPatientID,
			&item.StartedAt,
			&item.EndedAt,
			&item.Status,
			&item.Summary,
			&item.AppointmentOutcome,
			&item.AppointmentOccurredAt,
			&item.OldAppointmentID,
			&item.NewAppointmentID,
		); err != nil {
			return DailyOutcomes{}, fmt.Errorf("scan daily AI outcome: %w", err)
		}
		countOutcome(&page.Counts, item.AppointmentOutcome)
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return DailyOutcomes{}, fmt.Errorf("iterate daily AI outcomes: %w", err)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return DailyOutcomes{}, fmt.Errorf("commit daily AI outcome query: %w", err)
	}
	return page, nil
}

func countOutcome(counts *OutcomeCounts, outcome AppointmentOutcome) {
	switch outcome {
	case OutcomeBooking:
		counts.Bookings++
	case OutcomeCancellation:
		counts.Cancellations++
	case OutcomeReschedule:
		counts.Reschedules++
	case OutcomePartial:
		counts.Partial++
	default:
		counts.Indeterminate++
	}
}

type Module struct {
	pool   *pgxpool.Pool
	access *access.Module
	now    func() time.Time
}

func New(
	pool *pgxpool.Pool,
	accessModule *access.Module,
	now func() time.Time,
) *Module {
	if now == nil {
		now = time.Now
	}
	return &Module{pool: pool, access: accessModule, now: now}
}

func (m *Module) Ingest(
	ctx context.Context,
	command IngestCommand,
) (Interaction, UpsertStatus, error) {
	normalizeCommand(&command)
	stage := messageCompleteness(command.Kind)
	if m.pool == nil || m.access == nil || stage == 0 || !validCommand(command) {
		return Interaction{}, "", ErrInvalidInput
	}

	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Interaction{}, "", fmt.Errorf("begin AI Interaction ingestion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var authorization access.ServiceAuthorization
	if command.OfficeKey != "" {
		authorization, err = m.access.LockServiceAuthorization(
			ctx,
			tx,
			command.Service,
			command.OfficeKey,
			access.ServiceCapabilityIngestAIInteraction,
		)
	} else {
		authorization, err = m.access.LockServiceVoiceAuthorization(
			ctx,
			tx,
			command.Service,
			command.OfficePhone,
			access.ServiceCapabilityIngestAIInteraction,
		)
	}
	if err != nil {
		return Interaction{}, "", ErrDenied
	}

	current, found, err := lockBySourceCall(
		ctx,
		tx,
		authorization.PracticeID,
		command.SourceCallID,
	)
	if err != nil {
		return Interaction{}, "", err
	}
	now := m.now().UTC()
	status := StatusUpdated
	if !found {
		current = Interaction{
			ID:                 uuid.NewString(),
			ServiceSubject:     command.Service.Subject,
			PracticeID:         authorization.PracticeID,
			LocationID:         authorization.LocationID,
			SourceCallID:       command.SourceCallID,
			Phone:              command.CallerPhone,
			OfficePhone:        command.OfficePhone,
			StartedAt:          command.StartedAt,
			AppointmentOutcome: OutcomeIndeterminate,
			CreatedAt:          now,
			UpdatedAt:          now,
		}
		status = StatusCreated
	} else if current.ServiceSubject != command.Service.Subject ||
		current.LocationID != authorization.LocationID ||
		current.Phone != command.CallerPhone ||
		current.OfficePhone != command.OfficePhone ||
		!current.StartedAt.Equal(command.StartedAt) {
		return Interaction{}, "", ErrConflict
	}

	applyMessage(&current, command, stage, now)
	if err := save(ctx, tx, current, found); err != nil {
		return Interaction{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return Interaction{}, "", fmt.Errorf("commit AI Interaction ingestion: %w", err)
	}
	return current, status, nil
}

func normalizeCommand(command *IngestCommand) {
	command.OfficeKey = strings.TrimSpace(command.OfficeKey)
	command.SourceCallID = strings.TrimSpace(command.SourceCallID)
	command.CallerPhone = strings.TrimSpace(command.CallerPhone)
	command.OfficePhone = strings.TrimSpace(command.OfficePhone)
	command.Summary = strings.TrimSpace(command.Summary)
	command.StartedAt = command.StartedAt.UTC()
	if command.EndedAt != nil {
		endedAt := command.EndedAt.UTC()
		command.EndedAt = &endedAt
	}
	if command.Appointment != nil {
		command.Appointment.ExternalPatientID = strings.TrimSpace(
			command.Appointment.ExternalPatientID,
		)
		command.Appointment.OldAppointmentID = strings.TrimSpace(
			command.Appointment.OldAppointmentID,
		)
		command.Appointment.NewAppointmentID = strings.TrimSpace(
			command.Appointment.NewAppointmentID,
		)
		command.Appointment.OccurredAt = command.Appointment.OccurredAt.UTC()
	}
}

func validCommand(command IngestCommand) bool {
	if !textLengthBetween(command.OfficeKey, 0, 100) ||
		!textLengthBetween(command.SourceCallID, 1, 255) ||
		!canonicalPhone.MatchString(command.CallerPhone) ||
		!canonicalPhone.MatchString(command.OfficePhone) ||
		command.StartedAt.IsZero() ||
		!validStatus(command.Status) ||
		(command.EndedAt != nil && command.EndedAt.Before(command.StartedAt)) ||
		!textLengthBetween(command.Summary, 0, 10000) ||
		!validOptionalJSON(command.Transcript) ||
		!validOptionalJSON(command.SummaryPayload) ||
		!validOptionalJSON(command.CloseoutPayload) {
		return false
	}
	switch command.Kind {
	case MessageStart:
		return command.Status == CallInProgress && command.EndedAt == nil
	case MessageSummary:
		return command.EndedAt != nil && command.Status != CallInProgress
	case MessageCloseout:
		return command.EndedAt != nil &&
			command.Status != CallInProgress &&
			len(command.CloseoutPayload) > 0
	case MessageOutcomeCheckpoint:
		return validAppointmentEvidence(command.Appointment)
	default:
		return false
	}
}

func validAppointmentEvidence(evidence *AppointmentEvidence) bool {
	if evidence == nil || evidence.OccurredAt.IsZero() ||
		!textLengthBetween(evidence.ExternalPatientID, 0, 255) ||
		!textLengthBetween(evidence.OldAppointmentID, 0, 255) ||
		!textLengthBetween(evidence.NewAppointmentID, 0, 255) ||
		!validOptionalJSON(evidence.BookingResult) ||
		!validOptionalJSON(evidence.CancellationResult) {
		return false
	}
	switch evidence.Action {
	case AppointmentBooked:
		return len(evidence.BookingResult) > 0
	case AppointmentCancelled:
		return len(evidence.CancellationResult) > 0
	case AppointmentRescheduled:
		return len(evidence.BookingResult) > 0 &&
			len(evidence.CancellationResult) > 0
	default:
		return false
	}
}

func validStatus(status CallStatus) bool {
	switch status {
	case CallInProgress, CallCompleted, CallEscalated, CallFailed:
		return true
	default:
		return false
	}
}

func validOptionalJSON(value json.RawMessage) bool {
	return len(value) == 0 || (json.Valid(value) && !bytes.Equal(value, []byte("null")))
}

func textLengthBetween(value string, minimum int, maximum int) bool {
	length := utf8.RuneCountInString(value)
	return length >= minimum && length <= maximum
}

func messageCompleteness(kind MessageKind) int {
	switch kind {
	case MessageStart, MessageOutcomeCheckpoint:
		return 1
	case MessageSummary:
		return 2
	case MessageCloseout:
		return 3
	default:
		return 0
	}
}

func applyMessage(
	interaction *Interaction,
	command IngestCommand,
	stage int,
	now time.Time,
) {
	if stage > interaction.Completeness {
		interaction.Status = command.Status
		interaction.EndedAt = command.EndedAt
		interaction.Completeness = stage
	}
	if interaction.Summary == "" && command.Summary != "" {
		interaction.Summary = command.Summary
	}
	interaction.Transcript = mergeEvidenceJSON(
		interaction.Transcript,
		command.Transcript,
	)
	interaction.SummaryPayload = mergeEvidenceJSON(
		interaction.SummaryPayload,
		command.SummaryPayload,
	)
	interaction.CloseoutPayload = mergeEvidenceJSON(
		interaction.CloseoutPayload,
		command.CloseoutPayload,
	)
	if command.Appointment != nil {
		applyAppointmentEvidence(interaction, *command.Appointment)
	}
	interaction.UpdatedAt = now
}

func mergeEvidenceJSON(current json.RawMessage, candidate json.RawMessage) json.RawMessage {
	if len(candidate) == 0 {
		return current
	}
	if len(current) == 0 {
		return candidate
	}
	var currentValue, candidateValue any
	currentDecoder := json.NewDecoder(bytes.NewReader(current))
	currentDecoder.UseNumber()
	candidateDecoder := json.NewDecoder(bytes.NewReader(candidate))
	candidateDecoder.UseNumber()
	if currentDecoder.Decode(&currentValue) != nil ||
		candidateDecoder.Decode(&candidateValue) != nil {
		return current
	}
	merged, err := json.Marshal(mergeEvidenceValue(currentValue, candidateValue))
	if err != nil {
		return current
	}
	return merged
}

func mergeEvidenceValue(current any, candidate any) any {
	switch currentValue := current.(type) {
	case map[string]any:
		candidateValue, ok := candidate.(map[string]any)
		if !ok {
			return current
		}
		for key, value := range candidateValue {
			if existing, found := currentValue[key]; found {
				currentValue[key] = mergeEvidenceValue(existing, value)
			} else {
				currentValue[key] = value
			}
		}
		return currentValue
	case []any:
		candidateValue, ok := candidate.([]any)
		if !ok || len(currentValue) > 0 || len(candidateValue) == 0 {
			return current
		}
		return candidateValue
	default:
		return current
	}
}

func applyAppointmentEvidence(
	interaction *Interaction,
	evidence AppointmentEvidence,
) {
	outcome := deriveOutcome(evidence)
	completeness := outcomeCompleteness(evidence, outcome)
	if completeness < interaction.OutcomeCompleteness ||
		(completeness == interaction.OutcomeCompleteness &&
			interaction.AppointmentOccurredAt != nil &&
			evidence.OccurredAt.Before(*interaction.AppointmentOccurredAt)) {
		return
	}
	interaction.AppointmentOutcome = outcome
	interaction.AppointmentOccurredAt = &evidence.OccurredAt
	interaction.ExternalPatientID = evidence.ExternalPatientID
	interaction.OldAppointmentID = evidence.OldAppointmentID
	interaction.NewAppointmentID = evidence.NewAppointmentID
	interaction.BookingResult = evidence.BookingResult
	interaction.CancellationResult = evidence.CancellationResult
	interaction.OutcomeCompleteness = completeness
}

func deriveOutcome(evidence AppointmentEvidence) AppointmentOutcome {
	bookingStatus := resultStatus(evidence.BookingResult)
	cancellationStatus := resultStatus(evidence.CancellationResult)
	switch evidence.Action {
	case AppointmentBooked:
		if bookingStatus == "booked" {
			return OutcomeBooking
		}
		if bookingStatus == "partial" {
			return OutcomePartial
		}
	case AppointmentCancelled:
		if cancellationStatus == "cancelled" {
			return OutcomeCancellation
		}
	case AppointmentRescheduled:
		if bookingStatus == "booked" && cancellationStatus == "cancelled" {
			return OutcomeReschedule
		}
		if bookingStatus == "booked" || bookingStatus == "partial" ||
			cancellationStatus == "cancelled" {
			return OutcomePartial
		}
	}
	return OutcomeIndeterminate
}

func resultStatus(value json.RawMessage) string {
	var result struct {
		Status string `json:"status"`
	}
	if len(value) == 0 || json.Unmarshal(value, &result) != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(result.Status))
}

func outcomeCompleteness(
	evidence AppointmentEvidence,
	outcome AppointmentOutcome,
) int {
	score := 1
	if evidence.ExternalPatientID != "" {
		score++
	}
	if evidence.OldAppointmentID != "" {
		score++
	}
	if evidence.NewAppointmentID != "" {
		score++
	}
	if len(evidence.BookingResult) > 0 {
		score += 3
	}
	if len(evidence.CancellationResult) > 0 {
		score += 3
	}
	if outcome == OutcomePartial {
		score++
	} else if outcome != OutcomeIndeterminate {
		score += 2
	}
	return score
}

func lockBySourceCall(
	ctx context.Context,
	tx pgx.Tx,
	practiceID string,
	sourceCallID string,
) (Interaction, bool, error) {
	interaction, err := scanInteraction(tx.QueryRow(ctx, interactionSelect+`
		WHERE interaction.practice_id = $1
			AND interaction.source_call_id = $2
		FOR UPDATE
	`, practiceID, sourceCallID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Interaction{}, false, nil
	}
	if err != nil {
		return Interaction{}, false, fmt.Errorf("lock AI Interaction: %w", err)
	}
	return interaction, true, nil
}

const interactionSelect = `
	SELECT
		interaction.id::text,
		interaction.service_subject,
		interaction.practice_id::text,
		interaction.location_id::text,
		location.name,
		interaction.source_call_id,
		interaction.phone,
		interaction.office_phone,
		COALESCE(interaction.external_patient_id, ''),
		interaction.started_at,
		interaction.ended_at,
		interaction.status,
		COALESCE(interaction.summary, ''),
		interaction.transcript,
		interaction.appointment_outcome,
		interaction.appointment_occurred_at,
		COALESCE(interaction.old_appointment_id, ''),
		COALESCE(interaction.new_appointment_id, ''),
		interaction.booking_result,
		interaction.cancellation_result,
		interaction.outcome_completeness,
		interaction.summary_payload,
		interaction.closeout_payload,
		interaction.completeness,
		interaction.created_at,
		interaction.updated_at
	FROM ai_interactions interaction
	JOIN access_locations location
		ON location.practice_id = interaction.practice_id
		AND location.id = interaction.location_id`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanInteraction(row rowScanner) (Interaction, error) {
	var interaction Interaction
	err := row.Scan(
		&interaction.ID,
		&interaction.ServiceSubject,
		&interaction.PracticeID,
		&interaction.LocationID,
		&interaction.LocationName,
		&interaction.SourceCallID,
		&interaction.Phone,
		&interaction.OfficePhone,
		&interaction.ExternalPatientID,
		&interaction.StartedAt,
		&interaction.EndedAt,
		&interaction.Status,
		&interaction.Summary,
		&interaction.Transcript,
		&interaction.AppointmentOutcome,
		&interaction.AppointmentOccurredAt,
		&interaction.OldAppointmentID,
		&interaction.NewAppointmentID,
		&interaction.BookingResult,
		&interaction.CancellationResult,
		&interaction.OutcomeCompleteness,
		&interaction.SummaryPayload,
		&interaction.CloseoutPayload,
		&interaction.Completeness,
		&interaction.CreatedAt,
		&interaction.UpdatedAt,
	)
	return interaction, err
}

func save(
	ctx context.Context,
	tx pgx.Tx,
	interaction Interaction,
	exists bool,
) error {
	if !exists {
		_, err := tx.Exec(ctx, `
			INSERT INTO ai_interactions (
				id, service_subject, practice_id, location_id, source_call_id,
				phone, office_phone, external_patient_id, started_at, ended_at,
				status, summary, transcript, appointment_outcome,
				appointment_occurred_at, old_appointment_id, new_appointment_id,
				booking_result, cancellation_result, outcome_completeness,
				summary_payload, closeout_payload, completeness, created_at, updated_at
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
				$11, $12, $13, $14, $15, $16, $17, $18, $19, $20,
				$21, $22, $23, $24, $25
			)
		`, interactionValues(interaction)...)
		if err != nil {
			return fmt.Errorf("insert AI Interaction: %w", err)
		}
		return nil
	}
	_, err := tx.Exec(ctx, `
		UPDATE ai_interactions SET
			external_patient_id = $2,
			ended_at = $3,
			status = $4,
			summary = $5,
			transcript = $6,
			appointment_outcome = $7,
			appointment_occurred_at = $8,
			old_appointment_id = $9,
			new_appointment_id = $10,
			booking_result = $11,
			cancellation_result = $12,
			outcome_completeness = $13,
			summary_payload = $14,
			closeout_payload = $15,
			completeness = $16,
			updated_at = $17
		WHERE id = $1
	`,
		interaction.ID,
		nullIfEmpty(interaction.ExternalPatientID),
		interaction.EndedAt,
		interaction.Status,
		nullIfEmpty(interaction.Summary),
		nullIfEmptyJSON(interaction.Transcript),
		interaction.AppointmentOutcome,
		interaction.AppointmentOccurredAt,
		nullIfEmpty(interaction.OldAppointmentID),
		nullIfEmpty(interaction.NewAppointmentID),
		nullIfEmptyJSON(interaction.BookingResult),
		nullIfEmptyJSON(interaction.CancellationResult),
		interaction.OutcomeCompleteness,
		nullIfEmptyJSON(interaction.SummaryPayload),
		nullIfEmptyJSON(interaction.CloseoutPayload),
		interaction.Completeness,
		interaction.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("update AI Interaction: %w", err)
	}
	return nil
}

func interactionValues(interaction Interaction) []any {
	return []any{
		interaction.ID,
		interaction.ServiceSubject,
		interaction.PracticeID,
		interaction.LocationID,
		interaction.SourceCallID,
		interaction.Phone,
		interaction.OfficePhone,
		nullIfEmpty(interaction.ExternalPatientID),
		interaction.StartedAt,
		interaction.EndedAt,
		interaction.Status,
		nullIfEmpty(interaction.Summary),
		nullIfEmptyJSON(interaction.Transcript),
		interaction.AppointmentOutcome,
		interaction.AppointmentOccurredAt,
		nullIfEmpty(interaction.OldAppointmentID),
		nullIfEmpty(interaction.NewAppointmentID),
		nullIfEmptyJSON(interaction.BookingResult),
		nullIfEmptyJSON(interaction.CancellationResult),
		interaction.OutcomeCompleteness,
		nullIfEmptyJSON(interaction.SummaryPayload),
		nullIfEmptyJSON(interaction.CloseoutPayload),
		interaction.Completeness,
		interaction.CreatedAt,
		interaction.UpdatedAt,
	}
}

func nullIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullIfEmptyJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
