package interaction

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chasef07/acuity_product/backend/internal/access"
	productpostgres "github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

type LifecycleStage int16

const (
	LifecycleStarted    LifecycleStage = 1
	LifecycleSummarized LifecycleStage = 2
	LifecycleClosed     LifecycleStage = 3
)

var (
	ErrDenied       = errors.New("AI Interaction access denied")
	ErrInvalidInput = errors.New("invalid AI Interaction input")
	ErrConflict     = errors.New("AI Interaction source conflict")
	canonicalPhone  = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)
)

type AppointmentEvidence struct {
	Action             AppointmentAction `json:"action"`
	OccurredAt         time.Time         `json:"occurredAt"`
	ExternalPatientID  string            `json:"externalPatientId,omitempty"`
	OldAppointmentID   string            `json:"oldAppointmentId,omitempty"`
	NewAppointmentID   string            `json:"newAppointmentId,omitempty"`
	BookingResult      json.RawMessage   `json:"bookingResult,omitempty"`
	CancellationResult json.RawMessage   `json:"cancellationResult,omitempty"`
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
	AppointmentAction     AppointmentAction
	AppointmentOutcome    AppointmentOutcome
	AppointmentOccurredAt *time.Time
	OldAppointmentID      string
	NewAppointmentID      string
	BookingResult         json.RawMessage
	CancellationResult    json.RawMessage
	SummaryPayload        json.RawMessage
	CloseoutPayload       json.RawMessage
	LifecycleStage        LifecycleStage
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type QueryOutcomesCommand struct {
	Identity          access.Identity
	PracticeID        string
	LocationID        string
	AppointmentAction AppointmentAction
	SkipCounts        bool
	Cursor            string
	Limit             int
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
	AppointmentAction     AppointmentAction
	AppointmentOutcome    AppointmentOutcome
	AppointmentOccurredAt *time.Time
	AttentionOccurredAt   time.Time
	OldAppointmentID      string
	NewAppointmentID      string
}

type OutcomePage struct {
	Items      []OutcomeItem
	NextCursor string
	Counts     *OutcomeCounts
}

type OutcomeCounts struct {
	Tasks         int
	Bookings      int
	Cancellations int
	Reschedules   int
}

func (m *Module) Read(
	ctx context.Context,
	identity access.Identity,
	interactionID string,
) (Interaction, error) {
	return m.read(ctx, identity, interactionID, false)
}

func (m *Module) ReadEvidence(
	ctx context.Context,
	identity access.Identity,
	interactionID string,
) (Interaction, error) {
	return m.read(ctx, identity, interactionID, true)
}

func (m *Module) read(
	ctx context.Context,
	identity access.Identity,
	interactionID string,
	requireAdmin bool,
) (Interaction, error) {
	if m.database == nil || m.access == nil {
		return Interaction{}, ErrInvalidInput
	}
	if _, err := uuid.Parse(strings.TrimSpace(interactionID)); err != nil {
		return Interaction{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
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
	authorization, err := m.access.LockReadAuthorization(
		ctx,
		tx,
		identity,
		stored.PracticeID,
		stored.LocationID,
	)
	if err != nil {
		return Interaction{}, ErrDenied
	}
	if requireAdmin &&
		!authorization.PlatformOperator &&
		authorization.Membership.Role != access.RoleAdmin {
		return Interaction{}, ErrDenied
	}
	if err := tx.Commit(ctx); err != nil {
		return Interaction{}, fmt.Errorf("commit AI Interaction read: %w", err)
	}
	return stored, nil
}

func (m *Module) QueryOutcomes(
	ctx context.Context,
	command QueryOutcomesCommand,
) (OutcomePage, error) {
	command.PracticeID = strings.TrimSpace(command.PracticeID)
	command.LocationID = strings.TrimSpace(command.LocationID)
	if m.database == nil || m.access == nil || command.PracticeID == "" {
		return OutcomePage{}, ErrInvalidInput
	}
	if command.AppointmentAction != "" &&
		command.AppointmentAction != AppointmentBooked &&
		command.AppointmentAction != AppointmentCancelled &&
		command.AppointmentAction != AppointmentRescheduled {
		return OutcomePage{}, ErrInvalidInput
	}
	limit := command.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 50 {
		return OutcomePage{}, ErrInvalidInput
	}
	cursor, err := decodeOutcomeCursor(command.Cursor)
	if err != nil {
		return OutcomePage{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return OutcomePage{}, fmt.Errorf("begin AI outcome query: %w", err)
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
		return OutcomePage{}, ErrDenied
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
		return OutcomePage{}, ErrDenied
	}
	page := OutcomePage{Items: []OutcomeItem{}}
	if !command.SkipCounts {
		counts := OutcomeCounts{}
		if err := tx.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE interaction.appointment_action IS NULL),
			count(*) FILTER (WHERE interaction.appointment_action = 'BOOKED'),
			count(*) FILTER (WHERE interaction.appointment_action = 'CANCELLED'),
			count(*) FILTER (WHERE interaction.appointment_action = 'RESCHEDULED')
		FROM ai_interactions interaction
		JOIN LATERAL (
			SELECT candidate.outcome_occurred_at
			FROM ai_interaction_attention candidate
			WHERE candidate.interaction_id = interaction.id
				AND candidate.user_subject = $3
				AND candidate.reviewed_at IS NULL
			ORDER BY candidate.outcome_occurred_at DESC
			LIMIT 1
		) attention ON true
		WHERE interaction.practice_id = $1
			AND interaction.location_id::text = ANY($2::text[])
			AND NOT EXISTS (
				SELECT 1
				FROM work_tasks task
				WHERE task.practice_id = interaction.practice_id
					AND task.source_call_id = interaction.source_call_id
					AND task.state = 'OPEN'
			)
			AND (
				interaction.appointment_action IN (
					'BOOKED',
					'CANCELLED',
					'RESCHEDULED'
				)
				OR interaction.status IN ('FAILED', 'ESCALATED')
				OR interaction.appointment_outcome = 'PARTIAL'
			)
		`, command.PracticeID, locationIDs, command.Identity.Subject).Scan(
			&counts.Tasks,
			&counts.Bookings,
			&counts.Cancellations,
			&counts.Reschedules,
		); err != nil {
			return OutcomePage{}, fmt.Errorf("count AI outcome attention: %w", err)
		}
		page.Counts = &counts
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
			COALESCE(interaction.appointment_action, ''),
			interaction.appointment_outcome,
			interaction.appointment_occurred_at,
			attention.outcome_occurred_at,
			COALESCE(interaction.old_appointment_id, ''),
			COALESCE(interaction.new_appointment_id, '')
		FROM ai_interactions interaction
		JOIN LATERAL (
			SELECT candidate.outcome_occurred_at
			FROM ai_interaction_attention candidate
			WHERE candidate.interaction_id = interaction.id
				AND candidate.user_subject = $3
				AND candidate.reviewed_at IS NULL
			ORDER BY candidate.outcome_occurred_at DESC
			LIMIT 1
		) attention ON true
		JOIN access_locations location
			ON location.practice_id = interaction.practice_id
			AND location.id = interaction.location_id
		WHERE interaction.practice_id = $1
			AND interaction.location_id::text = ANY($2::text[])
			AND NOT EXISTS (
				SELECT 1
				FROM work_tasks task
				WHERE task.practice_id = interaction.practice_id
					AND task.source_call_id = interaction.source_call_id
					AND task.state = 'OPEN'
			)
			AND (
				interaction.appointment_action IN (
					'BOOKED',
					'CANCELLED',
					'RESCHEDULED'
				)
				OR interaction.status IN ('FAILED', 'ESCALATED')
				OR interaction.appointment_outcome = 'PARTIAL'
			)
			AND ($4::text = '' OR interaction.appointment_action = $4)
			AND (
				NOT $5::boolean
				OR (attention.outcome_occurred_at, interaction.id) <
					($6::timestamptz, $7::uuid)
			)
		ORDER BY attention.outcome_occurred_at DESC, interaction.id DESC
		LIMIT $8
	`, command.PracticeID, locationIDs, command.Identity.Subject,
		command.AppointmentAction, cursor.Present, nullableOutcomeCursorTime(cursor),
		nullableOutcomeCursorID(cursor), limit+1)
	if err != nil {
		return OutcomePage{}, fmt.Errorf("query AI outcome attention: %w", err)
	}
	defer rows.Close()
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
			&item.AppointmentAction,
			&item.AppointmentOutcome,
			&item.AppointmentOccurredAt,
			&item.AttentionOccurredAt,
			&item.OldAppointmentID,
			&item.NewAppointmentID,
		); err != nil {
			return OutcomePage{}, fmt.Errorf("scan AI outcome attention: %w", err)
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return OutcomePage{}, fmt.Errorf("iterate AI outcome attention: %w", err)
	}
	rows.Close()
	nextCursor := ""
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		nextCursor, err = encodeOutcomeCursor(page.Items[len(page.Items)-1])
		if err != nil {
			return OutcomePage{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return OutcomePage{}, fmt.Errorf("commit AI outcome attention query: %w", err)
	}
	page.NextCursor = nextCursor
	return page, nil
}

type outcomeCursor struct {
	Present    bool      `json:"-"`
	OccurredAt time.Time `json:"occurredAt"`
	ID         string    `json:"id"`
}

func encodeOutcomeCursor(item OutcomeItem) (string, error) {
	occurredAt := item.AttentionOccurredAt
	if occurredAt.IsZero() && item.AppointmentOccurredAt != nil {
		occurredAt = *item.AppointmentOccurredAt
	}
	if occurredAt.IsZero() || uuid.Validate(item.ID) != nil {
		return "", fmt.Errorf("encode AI outcome cursor: invalid outcome")
	}
	encoded, err := json.Marshal(outcomeCursor{
		OccurredAt: occurredAt,
		ID:         item.ID,
	})
	if err != nil {
		return "", fmt.Errorf("encode AI outcome cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeOutcomeCursor(encoded string) (outcomeCursor, error) {
	if encoded == "" {
		return outcomeCursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return outcomeCursor{}, err
	}
	var cursor outcomeCursor
	if err := json.Unmarshal(raw, &cursor); err != nil ||
		cursor.OccurredAt.IsZero() || uuid.Validate(cursor.ID) != nil {
		return outcomeCursor{}, ErrInvalidInput
	}
	cursor.Present = true
	return cursor, nil
}

func nullableOutcomeCursorTime(cursor outcomeCursor) any {
	if !cursor.Present {
		return nil
	}
	return cursor.OccurredAt
}

func nullableOutcomeCursorID(cursor outcomeCursor) any {
	if !cursor.Present {
		return nil
	}
	return cursor.ID
}

func outcomeAttentionAt(stored Interaction) time.Time {
	if stored.AppointmentOccurredAt != nil {
		return *stored.AppointmentOccurredAt
	}
	if stored.EndedAt != nil {
		return *stored.EndedAt
	}
	return stored.StartedAt
}

func (m *Module) ReviewOutcome(
	ctx context.Context,
	identity access.Identity,
	interactionID string,
) error {
	interactionID = strings.TrimSpace(interactionID)
	if m.database == nil || m.access == nil {
		return ErrInvalidInput
	}
	if _, err := uuid.Parse(interactionID); err != nil {
		return ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin AI outcome review: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	stored, err := scanInteraction(tx.QueryRow(ctx, interactionSelect+`
		WHERE interaction.id = $1
		FOR UPDATE
	`, interactionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDenied
	}
	if err != nil {
		return fmt.Errorf("lock AI Interaction review: %w", err)
	}
	authorization, err := m.access.LockMutationAuthorization(
		ctx,
		tx,
		identity,
		stored.PracticeID,
		stored.LocationID,
	)
	if err != nil {
		return ErrDenied
	}
	reviewedAt := m.now()
	tag, err := tx.Exec(ctx, `
		UPDATE ai_interaction_attention
		SET reviewed_at = $3
		WHERE interaction_id = $1
			AND user_subject = $2
			AND reviewed_at IS NULL
	`, stored.ID, identity.Subject, reviewedAt)
	if err != nil {
		return fmt.Errorf("review AI Interaction outcome: %w", err)
	}
	if tag.RowsAffected() > 0 {
		if err := m.access.AuditOperatorMutation(
			ctx,
			tx,
			authorization,
			access.OperatorMutationAudit{
				Action:          "ai_interaction.review",
				ResourceType:    "ai_interaction",
				ResourceID:      stored.ID,
				ResourceVersion: 1,
				OccurredAt:      reviewedAt,
			},
		); err != nil {
			return err
		}
		if _, err := m.access.RecordWorkspaceChange(
			ctx,
			tx,
			stored.PracticeID,
		); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit AI outcome review: %w", err)
	}
	return nil
}

type Module struct {
	database productpostgres.Database
	access   *access.Module
	work     *work.Module
	now      func() time.Time
}

func New(
	database productpostgres.Database,
	accessModule *access.Module,
	now func() time.Time,
) *Module {
	if now == nil {
		now = time.Now
	}
	return &Module{
		database: database,
		access:   accessModule,
		work:     work.New(database, accessModule, now),
		now:      now,
	}
}

func (m *Module) Ingest(
	ctx context.Context,
	command IngestCommand,
) (Interaction, UpsertStatus, error) {
	normalizeCommand(&command)
	stage := messageLifecycleStage(command.Kind)
	if m.database == nil || m.access == nil || stage == 0 || !validCommand(command) {
		return Interaction{}, "", ErrInvalidInput
	}

	now := m.now().UTC().Truncate(time.Microsecond)
	payload, fingerprint, err := receiptPayload(command)
	if err != nil {
		return Interaction{}, "", fmt.Errorf("encode AI Interaction receipt: %w", err)
	}
	receipt, err := m.acceptReceipt(ctx, command, payload, fingerprint, now)
	if err != nil {
		return Interaction{}, "", err
	}
	return m.projectReceipt(ctx, receipt, command, stage, now)
}

func normalizeCommand(command *IngestCommand) {
	command.OfficeKey = strings.TrimSpace(command.OfficeKey)
	command.SourceCallID = strings.TrimSpace(command.SourceCallID)
	command.CallerPhone = strings.TrimSpace(command.CallerPhone)
	command.OfficePhone = strings.TrimSpace(command.OfficePhone)
	command.Summary = strings.TrimSpace(command.Summary)
	command.StartedAt = command.StartedAt.UTC().Truncate(time.Microsecond)
	if command.EndedAt != nil {
		endedAt := command.EndedAt.UTC().Truncate(time.Microsecond)
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
		command.Appointment.OccurredAt = command.Appointment.OccurredAt.UTC().Truncate(time.Microsecond)
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
		return command.Status == CallInProgress && command.EndedAt == nil &&
			command.Summary == "" && len(command.Transcript) == 0 &&
			command.Appointment == nil && len(command.SummaryPayload) == 0 &&
			len(command.CloseoutPayload) == 0
	case MessageSummary:
		return command.EndedAt != nil && command.Status != CallInProgress &&
			len(command.SummaryPayload) > 0 && len(command.CloseoutPayload) == 0 &&
			validOptionalAppointmentEvidence(command.Appointment)
	case MessageCloseout:
		return command.EndedAt != nil &&
			command.Status != CallInProgress &&
			len(command.CloseoutPayload) > 0 && len(command.SummaryPayload) == 0 &&
			validOptionalAppointmentEvidence(command.Appointment)
	case MessageOutcomeCheckpoint:
		return command.Status == CallInProgress && command.EndedAt == nil &&
			command.Summary == "" && len(command.Transcript) == 0 &&
			len(command.SummaryPayload) == 0 && len(command.CloseoutPayload) == 0 &&
			validAppointmentEvidence(command.Appointment)
	default:
		return false
	}
}

func validOptionalAppointmentEvidence(evidence *AppointmentEvidence) bool {
	return evidence == nil || validAppointmentEvidence(evidence)
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

func messageLifecycleStage(kind MessageKind) LifecycleStage {
	switch kind {
	case MessageStart, MessageOutcomeCheckpoint:
		return LifecycleStarted
	case MessageSummary:
		return LifecycleSummarized
	case MessageCloseout:
		return LifecycleClosed
	default:
		return 0
	}
}

func applyMessage(
	interaction *Interaction,
	command IngestCommand,
	stage LifecycleStage,
	now time.Time,
) error {
	advancesLifecycle := stage > interaction.LifecycleStage
	sameLifecycle := stage == interaction.LifecycleStage
	if advancesLifecycle {
		interaction.Status = command.Status
		interaction.EndedAt = command.EndedAt
		interaction.LifecycleStage = stage
	} else if sameLifecycle &&
		(interaction.Status != command.Status ||
			!equalOptionalTime(interaction.EndedAt, command.EndedAt)) {
		return ErrConflict
	}
	if command.Summary != "" {
		if interaction.Summary == "" || advancesLifecycle {
			interaction.Summary = command.Summary
		} else if sameLifecycle && interaction.Summary != command.Summary {
			return ErrConflict
		}
	}
	if advancesLifecycle && len(command.Transcript) > 0 {
		interaction.Transcript = command.Transcript
	} else if len(interaction.Transcript) == 0 && len(command.Transcript) > 0 {
		interaction.Transcript = command.Transcript
	} else if sameLifecycle {
		var err error
		interaction.Transcript, err = richerEvidenceJSON(
			interaction.Transcript,
			command.Transcript,
		)
		if err != nil {
			return err
		}
	}
	var err error
	interaction.SummaryPayload, err = richerEvidenceJSON(
		interaction.SummaryPayload,
		command.SummaryPayload,
	)
	if err != nil {
		return err
	}
	interaction.CloseoutPayload, err = richerEvidenceJSON(
		interaction.CloseoutPayload,
		command.CloseoutPayload,
	)
	if err != nil {
		return err
	}
	if command.Appointment != nil {
		if err := applyAppointmentEvidence(
			interaction,
			*command.Appointment,
			advancesLifecycle,
		); err != nil {
			return err
		}
	}
	interaction.UpdatedAt = now
	return nil
}

func richerEvidenceJSON(
	current json.RawMessage,
	candidate json.RawMessage,
) (json.RawMessage, error) {
	if len(candidate) == 0 {
		return current, nil
	}
	if len(current) == 0 {
		return candidate, nil
	}
	var currentValue, candidateValue any
	currentDecoder := json.NewDecoder(bytes.NewReader(current))
	currentDecoder.UseNumber()
	candidateDecoder := json.NewDecoder(bytes.NewReader(candidate))
	candidateDecoder.UseNumber()
	if currentDecoder.Decode(&currentValue) != nil ||
		candidateDecoder.Decode(&candidateValue) != nil {
		return nil, ErrConflict
	}
	if evidenceValueContains(candidateValue, currentValue) {
		return candidate, nil
	}
	if evidenceValueContains(currentValue, candidateValue) {
		return current, nil
	}
	return nil, ErrConflict
}

func evidenceValueContains(candidate any, current any) bool {
	switch currentValue := current.(type) {
	case map[string]any:
		candidateValue, ok := candidate.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range currentValue {
			candidateItem, found := candidateValue[key]
			if !found || !evidenceValueContains(candidateItem, value) {
				return false
			}
		}
		return true
	case []any:
		candidateValue, ok := candidate.([]any)
		if !ok || len(candidateValue) < len(currentValue) {
			return false
		}
		for index := range currentValue {
			if !evidenceValueContains(candidateValue[index], currentValue[index]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(candidate, current)
	}
}

func equalOptionalTime(left *time.Time, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}

func applyAppointmentEvidence(
	interaction *Interaction,
	evidence AppointmentEvidence,
	candidateWins bool,
) error {
	if interaction.AppointmentOccurredAt != nil {
		if evidence.OccurredAt.Before(*interaction.AppointmentOccurredAt) {
			return nil
		}
		if evidence.OccurredAt.Equal(*interaction.AppointmentOccurredAt) {
			if interaction.AppointmentAction != "" &&
				interaction.AppointmentAction != evidence.Action {
				return ErrConflict
			}
			var err error
			evidence, err = mergeSameAppointmentEvidence(
				*interaction,
				evidence,
				candidateWins,
			)
			if err != nil {
				return err
			}
		}
	}
	outcome := deriveOutcome(evidence)
	interaction.AppointmentAction = evidence.Action
	interaction.AppointmentOutcome = outcome
	interaction.AppointmentOccurredAt = &evidence.OccurredAt
	interaction.ExternalPatientID = evidence.ExternalPatientID
	interaction.OldAppointmentID = evidence.OldAppointmentID
	interaction.NewAppointmentID = evidence.NewAppointmentID
	if len(evidence.BookingResult) > 0 {
		interaction.BookingResult = evidence.BookingResult
	}
	if len(evidence.CancellationResult) > 0 {
		interaction.CancellationResult = evidence.CancellationResult
	}
	return nil
}

func mergeSameAppointmentEvidence(
	current Interaction,
	candidate AppointmentEvidence,
	candidateWins bool,
) (AppointmentEvidence, error) {
	var err error
	candidate.ExternalPatientID, err = mergeEvidenceText(
		current.ExternalPatientID,
		candidate.ExternalPatientID,
		candidateWins,
	)
	if err != nil {
		return AppointmentEvidence{}, err
	}
	candidate.OldAppointmentID, err = mergeEvidenceText(
		current.OldAppointmentID,
		candidate.OldAppointmentID,
		candidateWins,
	)
	if err != nil {
		return AppointmentEvidence{}, err
	}
	candidate.NewAppointmentID, err = mergeEvidenceText(
		current.NewAppointmentID,
		candidate.NewAppointmentID,
		candidateWins,
	)
	if err != nil {
		return AppointmentEvidence{}, err
	}
	if candidateWins {
		if len(candidate.BookingResult) == 0 {
			candidate.BookingResult = current.BookingResult
		}
		if len(candidate.CancellationResult) == 0 {
			candidate.CancellationResult = current.CancellationResult
		}
	} else {
		candidate.BookingResult, err = richerEvidenceJSON(
			current.BookingResult,
			candidate.BookingResult,
		)
		if err != nil {
			return AppointmentEvidence{}, err
		}
		candidate.CancellationResult, err = richerEvidenceJSON(
			current.CancellationResult,
			candidate.CancellationResult,
		)
		if err != nil {
			return AppointmentEvidence{}, err
		}
	}
	return candidate, nil
}

func mergeEvidenceText(current string, candidate string, candidateWins bool) (string, error) {
	if candidate == "" {
		return current, nil
	}
	if current == "" || current == candidate || candidateWins {
		return candidate, nil
	}
	return "", ErrConflict
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

func lockBySourceCall(
	ctx context.Context,
	tx pgx.Tx,
	practiceID string,
	sourceCallID string,
) (Interaction, bool, error) {
	interaction, err := scanInteraction(tx.QueryRow(ctx, interactionSelect+`
		WHERE interaction.practice_id = $1
			AND interaction.source_call_id = $2
		FOR UPDATE OF interaction
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
		COALESCE(interaction.appointment_action, ''),
		interaction.appointment_outcome,
		interaction.appointment_occurred_at,
		COALESCE(interaction.old_appointment_id, ''),
		COALESCE(interaction.new_appointment_id, ''),
		interaction.booking_result,
		interaction.cancellation_result,
		interaction.summary_payload,
		interaction.closeout_payload,
		interaction.lifecycle_stage,
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
		&interaction.AppointmentAction,
		&interaction.AppointmentOutcome,
		&interaction.AppointmentOccurredAt,
		&interaction.OldAppointmentID,
		&interaction.NewAppointmentID,
		&interaction.BookingResult,
		&interaction.CancellationResult,
		&interaction.SummaryPayload,
		&interaction.CloseoutPayload,
		&interaction.LifecycleStage,
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
				status, summary, transcript, appointment_action,
				appointment_outcome,
				appointment_occurred_at, old_appointment_id, new_appointment_id,
				booking_result, cancellation_result,
				summary_payload, closeout_payload, lifecycle_stage, created_at, updated_at
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
			appointment_action = $7,
			appointment_outcome = $8,
			appointment_occurred_at = $9,
			old_appointment_id = $10,
			new_appointment_id = $11,
			booking_result = $12,
			cancellation_result = $13,
			summary_payload = $14,
			closeout_payload = $15,
			lifecycle_stage = $16,
			updated_at = $17
		WHERE id = $1
	`,
		interaction.ID,
		nullIfEmpty(interaction.ExternalPatientID),
		interaction.EndedAt,
		interaction.Status,
		nullIfEmpty(interaction.Summary),
		nullIfEmptyJSON(interaction.Transcript),
		nullIfEmpty(string(interaction.AppointmentAction)),
		interaction.AppointmentOutcome,
		interaction.AppointmentOccurredAt,
		nullIfEmpty(interaction.OldAppointmentID),
		nullIfEmpty(interaction.NewAppointmentID),
		nullIfEmptyJSON(interaction.BookingResult),
		nullIfEmptyJSON(interaction.CancellationResult),
		nullIfEmptyJSON(interaction.SummaryPayload),
		nullIfEmptyJSON(interaction.CloseoutPayload),
		interaction.LifecycleStage,
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
		nullIfEmpty(string(interaction.AppointmentAction)),
		interaction.AppointmentOutcome,
		interaction.AppointmentOccurredAt,
		nullIfEmpty(interaction.OldAppointmentID),
		nullIfEmpty(interaction.NewAppointmentID),
		nullIfEmptyJSON(interaction.BookingResult),
		nullIfEmptyJSON(interaction.CancellationResult),
		nullIfEmptyJSON(interaction.SummaryPayload),
		nullIfEmptyJSON(interaction.CloseoutPayload),
		interaction.LifecycleStage,
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
