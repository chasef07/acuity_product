package messaging

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/telnyxsignature"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Direction string

const (
	DirectionInbound  Direction = "INBOUND"
	DirectionOutbound Direction = "OUTBOUND"
)

type DeliveryState string

const (
	DeliverySending   DeliveryState = "SENDING"
	DeliverySent      DeliveryState = "SENT"
	DeliveryDelivered DeliveryState = "DELIVERED"
	DeliveryFailed    DeliveryState = "FAILED"
	DeliveryUnknown   DeliveryState = "UNKNOWN"
)

type MessageCreateStatus string

const (
	MessageCreated   MessageCreateStatus = "created"
	MessageDuplicate MessageCreateStatus = "duplicate"
)

var (
	ErrDenied                 = errors.New("messaging access denied")
	ErrInvalidInput           = errors.New("invalid messaging input")
	ErrConflict               = errors.New("messaging conflict")
	ErrBlocked                = errors.New("messaging destination is opted out")
	ErrAmbiguous              = errors.New("messaging provider effect is ambiguous")
	ErrRejected               = errors.New("messaging provider rejected the command")
	errUnmatchedProviderEvent = errors.New("unmatched messaging provider event")
)

var (
	canonicalPhone = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)
	idempotencyKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,199}$`)
)

type Thread struct {
	ID              string
	PracticeID      string
	LocationID      string
	LocationName    string
	OfficePhone     string
	ExternalPhone   string
	DisplayName     string
	NameSource      string
	OutboundBlocked bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type ThreadSummary struct {
	Thread
	Preview         string
	LatestDirection Direction
	LatestDelivery  DeliveryState
	LatestActivity  time.Time
	Unread          bool
}

type Message struct {
	ID                string
	Thread            Thread
	Direction         Direction
	Body              string
	Sender            string
	Destination       string
	Delivery          DeliveryState
	SafeFailureCode   string
	ProviderMessageID string
	TaskID            string
	RetryOfMessageID  string
	Attachment        *Attachment
	CreatedAt         time.Time
	UpdatedAt         time.Time
	Version           int64
}

type QueryThreadsCommand struct {
	Identity   access.Identity
	PracticeID string
	LocationID string
	Search     string
	Cursor     string
	Limit      int
}

type ThreadPage struct {
	Items      []ThreadSummary
	NextCursor string
}

type EngagementLocation struct {
	ID   string
	Name string
}

type EngagementSummary struct {
	Phone          string
	DisplayName    string
	Locations      []EngagementLocation
	LatestActivity time.Time
	OpenTaskCount  int
	Unread         bool
}

type QueryEngagementsCommand struct {
	Identity   access.Identity
	PracticeID string
	Phone      string
}

type EngagementPage struct {
	Items []EngagementSummary
}

func (m *Module) QueryEngagements(
	ctx context.Context,
	command QueryEngagementsCommand,
) (EngagementPage, error) {
	command.PracticeID = strings.TrimSpace(command.PracticeID)
	phone, err := normalizePhone(command.Phone)
	if m.pool == nil || m.access == nil || command.PracticeID == "" || err != nil {
		return EngagementPage{}, ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return EngagementPage{}, fmt.Errorf("begin Engagement lookup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorization, err := m.access.LockReadAuthorization(
		ctx,
		tx,
		command.Identity,
		command.PracticeID,
		"",
	)
	if err != nil {
		return EngagementPage{}, ErrDenied
	}
	locationIDs := make([]string, 0, len(authorization.Locations))
	for _, location := range authorization.Locations {
		locationIDs = append(locationIDs, location.ID)
	}
	if len(locationIDs) == 0 {
		return EngagementPage{}, ErrDenied
	}

	var summary EngagementSummary
	var found bool
	err = tx.QueryRow(ctx, `
		WITH evidence AS (
			SELECT
				thread.location_id,
				thread.updated_at AS occurred_at,
				COALESCE(thread.display_name, '') AS display_name
			FROM messaging_threads thread
			WHERE thread.practice_id = $1
				AND thread.location_id::text = ANY($2::text[])
				AND thread.external_phone = $3
			UNION ALL
			SELECT
				call.location_id,
				call.updated_at,
				COALESCE(handoff.display_name, '')
			FROM human_calling_calls call
			LEFT JOIN human_calling_handoffs handoff ON handoff.id = call.source_handoff_id
			WHERE call.practice_id = $1
				AND call.location_id::text = ANY($2::text[])
				AND COALESCE(handoff.phone, call.destination_phone) = $3
			UNION ALL
			SELECT
				task.location_id,
				task.updated_at,
				COALESCE(task.caller_name, '')
			FROM work_tasks task
			WHERE task.practice_id = $1
				AND task.location_id::text = ANY($2::text[])
				AND task.phone = $3
		)
		SELECT
			$3,
			COALESCE((
				SELECT display_name
				FROM evidence
				WHERE display_name <> ''
				ORDER BY occurred_at DESC
				LIMIT 1
			), ''),
			COALESCE(max(occurred_at), '-infinity'::timestamptz),
			(
				SELECT count(*)
				FROM work_tasks task
				WHERE task.practice_id = $1
					AND task.location_id::text = ANY($2::text[])
					AND task.phone = $3
					AND task.state = 'OPEN'
			),
			EXISTS (
				SELECT 1
				FROM messaging_threads thread
				JOIN messaging_thread_unreads unread ON unread.thread_id = thread.id
				WHERE thread.practice_id = $1
					AND thread.location_id::text = ANY($2::text[])
					AND thread.external_phone = $3
					AND unread.user_subject = $4
			),
			count(*) > 0
		FROM evidence
	`, command.PracticeID, locationIDs, phone, command.Identity.Subject).Scan(
		&summary.Phone,
		&summary.DisplayName,
		&summary.LatestActivity,
		&summary.OpenTaskCount,
		&summary.Unread,
		&found,
	)
	if err != nil {
		return EngagementPage{}, fmt.Errorf("query Engagement summary: %w", err)
	}
	if !found {
		if err := tx.Commit(ctx); err != nil {
			return EngagementPage{}, fmt.Errorf("commit empty Engagement lookup: %w", err)
		}
		return EngagementPage{Items: []EngagementSummary{}}, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT DISTINCT location.id::text, location.name
		FROM access_locations location
		JOIN (
			SELECT location_id
			FROM messaging_threads
			WHERE practice_id = $1 AND external_phone = $3
			UNION
			SELECT call.location_id
			FROM human_calling_calls call
			LEFT JOIN human_calling_handoffs handoff ON handoff.id = call.source_handoff_id
			WHERE call.practice_id = $1
				AND COALESCE(handoff.phone, call.destination_phone) = $3
			UNION
			SELECT location_id
			FROM work_tasks
			WHERE practice_id = $1 AND phone = $3
		) evidence ON evidence.location_id = location.id
		WHERE location.practice_id = $1
			AND location.id::text = ANY($2::text[])
		ORDER BY location.name, location.id::text
	`, command.PracticeID, locationIDs, phone)
	if err != nil {
		return EngagementPage{}, fmt.Errorf("query Engagement Locations: %w", err)
	}
	for rows.Next() {
		var location EngagementLocation
		if err := rows.Scan(&location.ID, &location.Name); err != nil {
			rows.Close()
			return EngagementPage{}, fmt.Errorf("scan Engagement Location: %w", err)
		}
		summary.Locations = append(summary.Locations, location)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return EngagementPage{}, fmt.Errorf("iterate Engagement Locations: %w", err)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return EngagementPage{}, fmt.Errorf("commit Engagement lookup: %w", err)
	}
	return EngagementPage{Items: []EngagementSummary{summary}}, nil
}

type pageCursor struct {
	OccurredAt time.Time `json:"occurredAt"`
	ID         string    `json:"id"`
}

type QueryTimelineCommand struct {
	Identity access.Identity
	ThreadID string
	Cursor   string
	Limit    int
}

type QueryPhoneTimelineCommand struct {
	Identity   access.Identity
	PracticeID string
	Phone      string
	Cursor     string
	Limit      int
}

type TimelineItem struct {
	Type         string
	ID           string
	OccurredAt   time.Time
	TaskActivity string
	Message      Message
	Call         humancalling.CallHistoryItem
	Task         work.Task
}

type TimelinePage struct {
	Items      []TimelineItem
	NextCursor string
}

func (m *Module) QueryPhoneTimeline(
	ctx context.Context,
	command QueryPhoneTimelineCommand,
) (TimelinePage, error) {
	command.PracticeID = strings.TrimSpace(command.PracticeID)
	command.Phone = strings.TrimSpace(command.Phone)
	if m.pool == nil ||
		m.access == nil ||
		command.PracticeID == "" ||
		!canonicalPhone.MatchString(command.Phone) {
		return TimelinePage{}, ErrInvalidInput
	}
	limit := command.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 50 {
		return TimelinePage{}, ErrInvalidInput
	}
	cursor, err := decodeTimelineItemCursor(command.Cursor)
	if err != nil {
		return TimelinePage{}, ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return TimelinePage{}, fmt.Errorf("begin phone Engagement History: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorization, err := m.access.LockReadAuthorization(
		ctx,
		tx,
		command.Identity,
		command.PracticeID,
		"",
	)
	if err != nil {
		return TimelinePage{}, ErrDenied
	}
	locationIDs := make([]string, 0, len(authorization.Locations))
	for _, location := range authorization.Locations {
		locationIDs = append(locationIDs, location.ID)
	}
	if len(locationIDs) == 0 {
		return TimelinePage{}, ErrDenied
	}

	items := make([]TimelineItem, 0, (limit+1)*3)
	messageRows, err := tx.Query(ctx, `
		SELECT
			thread.id::text,
			thread.practice_id::text,
			thread.location_id::text,
			location.name,
			thread.office_phone,
			thread.external_phone,
			COALESCE(thread.display_name, ''),
			COALESCE(thread.name_source, ''),
			thread.outbound_blocked,
			thread.created_at,
			thread.updated_at,
			message.id::text,
			message.direction,
			COALESCE(message.body, ''),
			message.sender,
			message.destination,
			message.delivery_state,
			COALESCE(message.safe_failure_code, ''),
			COALESCE(message.provider_message_id, ''),
			COALESCE(message.task_id::text, ''),
			COALESCE(message.retry_of_message_id::text, ''),
			COALESCE(attachment.id::text, ''),
			COALESCE(attachment.direction, ''),
			COALESCE(attachment.state, ''),
			COALESCE(attachment.file_name, ''),
			COALESCE(attachment.content_type, ''),
			COALESCE(attachment.byte_size, 0),
			COALESCE(attachment.created_at, message.created_at),
			COALESCE(attachment.updated_at, message.updated_at),
			message.created_at,
			message.updated_at,
			message.version
		FROM messaging_messages message
		JOIN messaging_threads thread ON thread.id = message.thread_id
		JOIN access_locations location
			ON location.practice_id = thread.practice_id
			AND location.id = thread.location_id
		LEFT JOIN messaging_attachments attachment
			ON attachment.message_id = message.id
		WHERE thread.practice_id = $1
			AND thread.external_phone = $2
			AND thread.location_id::text = ANY($3::text[])
			AND (
				$4::timestamptz IS NULL
				OR message.created_at < $4
				OR (
					message.created_at = $4
					AND 'MESSAGE:' || message.id::text < $5
				)
			)
		ORDER BY message.created_at DESC, message.id DESC
		LIMIT $6
	`, command.PracticeID, command.Phone, locationIDs,
		nullableCursorTime(cursor), nullableCursorID(cursor), limit+1,
	)
	if err != nil {
		return TimelinePage{}, fmt.Errorf("query phone Messages: %w", err)
	}
	for messageRows.Next() {
		var message Message
		var attachment Attachment
		if err := messageRows.Scan(
			&message.Thread.ID,
			&message.Thread.PracticeID,
			&message.Thread.LocationID,
			&message.Thread.LocationName,
			&message.Thread.OfficePhone,
			&message.Thread.ExternalPhone,
			&message.Thread.DisplayName,
			&message.Thread.NameSource,
			&message.Thread.OutboundBlocked,
			&message.Thread.CreatedAt,
			&message.Thread.UpdatedAt,
			&message.ID,
			&message.Direction,
			&message.Body,
			&message.Sender,
			&message.Destination,
			&message.Delivery,
			&message.SafeFailureCode,
			&message.ProviderMessageID,
			&message.TaskID,
			&message.RetryOfMessageID,
			&attachment.ID,
			&attachment.Direction,
			&attachment.State,
			&attachment.FileName,
			&attachment.ContentType,
			&attachment.ByteSize,
			&attachment.CreatedAt,
			&attachment.UpdatedAt,
			&message.CreatedAt,
			&message.UpdatedAt,
			&message.Version,
		); err != nil {
			messageRows.Close()
			return TimelinePage{}, fmt.Errorf("scan phone Message: %w", err)
		}
		if attachment.ID != "" {
			attachment.MessageID = message.ID
			message.Attachment = &attachment
		}
		items = append(items, TimelineItem{
			Type:       "MESSAGE",
			ID:         message.ID,
			OccurredAt: message.CreatedAt,
			Message:    message,
		})
	}
	if err := messageRows.Err(); err != nil {
		messageRows.Close()
		return TimelinePage{}, fmt.Errorf("iterate phone Messages: %w", err)
	}
	messageRows.Close()

	callRows, err := tx.Query(ctx, `
		SELECT
			call.id::text,
			call.direction,
			call.created_at,
			call.ended_at,
			CASE
				WHEN bridged_staff.bridged_at IS NOT NULL AND call.ended_at IS NOT NULL
				THEN GREATEST(
					0,
					EXTRACT(EPOCH FROM (call.ended_at - bridged_staff.bridged_at))::bigint
				)
				ELSE 0
			END,
			call.location_id::text,
			location.name,
			COALESCE(membership.email, ''),
			COALESCE(handoff.transfer_reason, ''),
			CASE
				WHEN call.disposition_outcome IN ('FOLLOW_UP_REQUIRED', 'CREATE_TASK')
					THEN 'FOLLOW_UP_REQUIRED'
				WHEN call.disposition_outcome IS NOT NULL THEN 'RESOLVED'
				WHEN call.terminal_outcome = 'VOICEMAIL' THEN 'VOICEMAIL'
				WHEN call.terminal_outcome IN ('MISSED', 'ABANDONED') THEN 'MISSED'
				WHEN call.terminal_outcome IS NOT NULL AND bridged_staff.id IS NOT NULL
					THEN 'NEEDS_DISPOSITION'
				WHEN call.terminal_outcome IS NOT NULL THEN 'UNANSWERED'
				WHEN bridged_staff.state = 'BRIDGED' THEN 'CONNECTED'
				WHEN bridged_staff.state = 'BRIDGE_PENDING' THEN 'CONNECTING'
				ELSE 'RINGING'
			END
		FROM human_calling_calls call
		LEFT JOIN human_calling_handoffs handoff ON handoff.id = call.source_handoff_id
		JOIN access_locations location
			ON location.practice_id = call.practice_id
			AND location.id = call.location_id
		LEFT JOIN LATERAL (
			SELECT leg.id, leg.staff_subject, leg.state, leg.bridged_at
			FROM human_calling_call_legs leg
			WHERE leg.call_id = call.id AND leg.role = 'STAFF'
				AND leg.bridged_at IS NOT NULL
			ORDER BY leg.bridged_at DESC NULLS LAST, leg.updated_at DESC, leg.id DESC
			LIMIT 1
		) bridged_staff ON true
		LEFT JOIN access_memberships membership
			ON membership.practice_id = call.practice_id
			AND membership.user_subject = bridged_staff.staff_subject
		WHERE call.practice_id = $1
			AND call.location_id::text = ANY($2::text[])
			AND COALESCE(handoff.phone, call.destination_phone) = $3
			AND (
				$4::timestamptz IS NULL
				OR call.created_at < $4
				OR (
					call.created_at = $4
					AND 'CALL:' || call.id::text < $5
				)
			)
		ORDER BY call.created_at DESC, call.id DESC
		LIMIT $6
	`, command.PracticeID, locationIDs, command.Phone,
		nullableCursorTime(cursor), nullableCursorID(cursor), limit+1,
	)
	if err != nil {
		return TimelinePage{}, fmt.Errorf("query phone Calls: %w", err)
	}
	for callRows.Next() {
		var call humancalling.CallHistoryItem
		if err := callRows.Scan(
			&call.ID,
			&call.Direction,
			&call.StartedAt,
			&call.EndedAt,
			&call.DurationSeconds,
			&call.LocationID,
			&call.LocationName,
			&call.AnsweredByEmail,
			&call.TransferReason,
			&call.Outcome,
		); err != nil {
			callRows.Close()
			return TimelinePage{}, fmt.Errorf("scan phone Call: %w", err)
		}
		call.Type = "CALL"
		items = append(items, TimelineItem{
			Type:       "CALL",
			ID:         call.ID,
			OccurredAt: call.StartedAt,
			Call:       call,
		})
	}
	if err := callRows.Err(); err != nil {
		callRows.Close()
		return TimelinePage{}, fmt.Errorf("iterate phone Calls: %w", err)
	}
	callRows.Close()

	taskRows, err := tx.Query(ctx, `
		SELECT
			activity.id::text,
			task.id::text,
			activity.kind,
			activity.occurred_at
		FROM work_tasks task
		JOIN work_task_activities activity ON activity.task_id = task.id
		WHERE task.practice_id = $1
			AND task.location_id::text = ANY($2::text[])
			AND task.phone = $3
			AND (
				$4::timestamptz IS NULL
				OR activity.occurred_at < $4
				OR (
					activity.occurred_at = $4
					AND 'TASK:' || activity.id::text < $5
				)
			)
		ORDER BY activity.occurred_at DESC, activity.id DESC
		LIMIT $6
	`, command.PracticeID, locationIDs, command.Phone,
		nullableCursorTime(cursor), nullableCursorID(cursor), limit+1,
	)
	if err != nil {
		return TimelinePage{}, fmt.Errorf("query phone Tasks: %w", err)
	}
	type taskActivityRow struct {
		id, taskID, kind string
		occurredAt       time.Time
	}
	activities := []taskActivityRow{}
	for taskRows.Next() {
		var activity taskActivityRow
		if err := taskRows.Scan(
			&activity.id, &activity.taskID, &activity.kind, &activity.occurredAt,
		); err != nil {
			taskRows.Close()
			return TimelinePage{}, fmt.Errorf("scan phone Task Activity: %w", err)
		}
		activities = append(activities, activity)
	}
	if err := taskRows.Err(); err != nil {
		taskRows.Close()
		return TimelinePage{}, fmt.Errorf("iterate phone Task Activities: %w", err)
	}
	taskRows.Close()
	for _, activity := range activities {
		task, err := readConversationTask(ctx, tx, activity.taskID)
		if err != nil {
			return TimelinePage{}, fmt.Errorf("scan phone Task: %w", err)
		}
		items = append(items, TimelineItem{
			Type:         "TASK",
			ID:           activity.id,
			OccurredAt:   activity.occurredAt,
			TaskActivity: activity.kind,
			Task:         task,
		})
	}

	sort.Slice(items, func(left int, right int) bool {
		if items[left].OccurredAt.Equal(items[right].OccurredAt) {
			return timelineItemKey(items[left]) > timelineItemKey(items[right])
		}
		return items[left].OccurredAt.After(items[right].OccurredAt)
	})
	nextCursor := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		nextCursor = encodePageCursor(pageCursor{
			OccurredAt: last.OccurredAt,
			ID:         timelineItemKey(last),
		})
	}
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
	if err := tx.Commit(ctx); err != nil {
		return TimelinePage{}, fmt.Errorf("commit phone Engagement History: %w", err)
	}
	return TimelinePage{Items: items, NextCursor: nextCursor}, nil
}

func timelineItemKey(item TimelineItem) string {
	return item.Type + ":" + item.ID
}

type MarkReadCommand struct {
	Identity         access.Identity
	ThreadID         string
	SupportSessionID string
}

type CreateFollowUpTaskCommand struct {
	Identity         access.Identity
	MessageID        string
	Title            string
	SupportSessionID string
}

type SendCommand struct {
	Identity                  access.Identity
	PracticeID                string
	LocationID                string
	ThreadID                  string
	Destination               string
	Body                      string
	TaskID                    string
	AttachmentID              string
	RetryOfMessageID          string
	DuplicateRiskAcknowledged bool
	IdempotencyKey            string
	SupportSessionID          string
}

type SendAgainCommand struct {
	Identity                  access.Identity
	MessageID                 string
	IdempotencyKey            string
	DuplicateRiskAcknowledged bool
	SupportSessionID          string
}

type LocationProvision struct {
	PracticeKey        string
	LocationKey        string
	Sender             string
	MessagingProfileID string
	Active             *bool
}

type ProviderCommand struct {
	ID                 string
	MessageID          string
	Sender             string
	Destination        string
	Body               string
	MediaURL           string
	CallbackToken      string
	MessagingProfileID string
}

type ProviderResult struct {
	MessageID string
	State     DeliveryState
}

type WebhookReceipt struct {
	EventID   string
	Duplicate bool
}

type Provider interface {
	Send(context.Context, ProviderCommand) (ProviderResult, error)
	Reconcile(context.Context, string) (ProviderResult, error)
}

type Config struct {
	WebhookPublicKeys  []ed25519.PublicKey
	WebhookTolerance   time.Duration
	AttachmentStore    AttachmentObjectStore
	MediaPublicBaseURL string
	MediaSigningKey    []byte
	MediaURLTTL        time.Duration
	HTTPClient         *http.Client
}

// Module owns location texting configuration, Message Threads, Messages, and
// their durable provider commands.
type Module struct {
	pool     *pgxpool.Pool
	access   *access.Module
	work     *work.Module
	provider Provider
	config   Config
	now      func() time.Time
}

func New(
	pool *pgxpool.Pool,
	accessModule *access.Module,
	workModule *work.Module,
	provider Provider,
	config Config,
	now func() time.Time,
) *Module {
	if now == nil {
		now = time.Now
	}
	if config.WebhookTolerance == 0 {
		config.WebhookTolerance = 5 * time.Minute
	}
	if config.MediaURLTTL == 0 {
		config.MediaURLTTL = 10 * time.Minute
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &Module{
		pool:     pool,
		access:   accessModule,
		work:     workModule,
		provider: provider,
		config:   config,
		now:      now,
	}
}

func (m *Module) Provision(
	ctx context.Context,
	input []LocationProvision,
) error {
	if m.pool == nil {
		return ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin Messaging provisioning: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := m.ProvisionInTx(ctx, tx, input); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Messaging provisioning: %w", err)
	}
	return nil
}

func (m *Module) ProvisionInTx(
	ctx context.Context,
	tx pgx.Tx,
	input []LocationProvision,
) error {
	if tx == nil {
		return ErrInvalidInput
	}
	for _, configured := range input {
		configured.PracticeKey = strings.TrimSpace(configured.PracticeKey)
		configured.LocationKey = strings.TrimSpace(configured.LocationKey)
		var err error
		configured.Sender, err = normalizePhone(configured.Sender)
		configured.MessagingProfileID = strings.TrimSpace(
			configured.MessagingProfileID,
		)
		if err != nil ||
			configured.PracticeKey == "" ||
			configured.LocationKey == "" ||
			configured.MessagingProfileID == "" ||
			len(configured.MessagingProfileID) > 255 {
			return ErrInvalidInput
		}
		active := true
		if configured.Active != nil {
			active = *configured.Active
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO messaging_location_configurations (
				practice_id,
				location_id,
				sender,
				messaging_profile_id,
				active,
				updated_at
			)
			SELECT
				practice.id,
				location.id,
				$3,
				$4,
				$5,
				$6
			FROM access_practices practice
			JOIN access_locations location
				ON location.practice_id = practice.id
			WHERE practice.provisioning_key = $1
				AND location.provisioning_key = $2
			ON CONFLICT (practice_id, location_id)
			DO UPDATE SET
				sender = EXCLUDED.sender,
				messaging_profile_id = EXCLUDED.messaging_profile_id,
				active = EXCLUDED.active,
				updated_at = EXCLUDED.updated_at
		`, configured.PracticeKey, configured.LocationKey, configured.Sender,
			configured.MessagingProfileID, active, m.now())
		if err != nil {
			return fmt.Errorf("provision Location texting configuration: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return fmt.Errorf(
				"%w: unknown practice or location provisioning key",
				ErrInvalidInput,
			)
		}
	}
	return nil
}

func (m *Module) Send(
	ctx context.Context,
	command SendCommand,
) (Message, MessageCreateStatus, error) {
	normalizeSendCommand(&command)
	if m.pool == nil ||
		m.access == nil ||
		strings.TrimSpace(command.Identity.Subject) == "" ||
		strings.TrimSpace(command.PracticeID) == "" ||
		strings.TrimSpace(command.LocationID) == "" ||
		!idempotencyKey.MatchString(command.IdempotencyKey) ||
		(command.Body == "" && command.AttachmentID == "") ||
		utf8.RuneCountInString(command.Body) > 1600 {
		return Message{}, "", ErrInvalidInput
	}
	if command.AttachmentID != "" && uuid.Validate(command.AttachmentID) != nil {
		return Message{}, "", ErrInvalidInput
	}
	if command.RetryOfMessageID != "" &&
		uuid.Validate(command.RetryOfMessageID) != nil {
		return Message{}, "", ErrInvalidInput
	}
	if command.ThreadID == "" {
		destination, err := normalizePhone(command.Destination)
		if err != nil {
			return Message{}, "", ErrInvalidInput
		}
		command.Destination = destination
	} else if command.Destination != "" {
		destination, err := normalizePhone(command.Destination)
		if err != nil {
			return Message{}, "", ErrInvalidInput
		}
		command.Destination = destination
	}

	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Message{}, "", fmt.Errorf("begin Message send: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock(
			hashtextextended($1, 0)
				# hashtextextended($2, 1)
				# hashtextextended($3, 2)
		)
	`, command.PracticeID, command.Identity.Subject,
		command.IdempotencyKey,
	); err != nil {
		return Message{}, "", fmt.Errorf("lock Message idempotency key: %w", err)
	}
	authorization, err := m.access.LockMutationAuthorization(
		ctx,
		tx,
		command.Identity,
		command.PracticeID,
		command.LocationID,
		command.SupportSessionID,
	)
	if err != nil {
		if errors.Is(err, access.ErrSupportRequired) ||
			errors.Is(err, access.ErrSupportExpired) ||
			errors.Is(err, access.ErrSupportRevoked) ||
			errors.Is(err, access.ErrSupportPracticeMismatch) {
			return Message{}, "", err
		}
		return Message{}, "", ErrDenied
	}
	var originalAttachmentFileName, originalAttachmentType string
	var originalAttachmentBytes int
	if command.RetryOfMessageID != "" {
		var originalThreadID, originalBody, originalDestination, originalTaskID string
		var originalPracticeID, originalLocationID string
		var originalDelivery DeliveryState
		var originalAttachmentID string
		if err := tx.QueryRow(ctx, `
			SELECT
				message.thread_id::text,
				message.practice_id::text,
				message.location_id::text,
				COALESCE(message.body, ''),
				message.destination,
				COALESCE(message.task_id::text, ''),
				message.delivery_state,
				COALESCE(attachment.id::text, ''),
				COALESCE(attachment.file_name, ''),
				COALESCE(attachment.content_type, ''),
				COALESCE(attachment.byte_size, 0)
			FROM messaging_messages message
			LEFT JOIN messaging_attachments attachment
				ON attachment.message_id = message.id
			WHERE message.id = $1
			FOR UPDATE OF message
		`, command.RetryOfMessageID).Scan(
			&originalThreadID,
			&originalPracticeID,
			&originalLocationID,
			&originalBody,
			&originalDestination,
			&originalTaskID,
			&originalDelivery,
			&originalAttachmentID,
			&originalAttachmentFileName,
			&originalAttachmentType,
			&originalAttachmentBytes,
		); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Message{}, "", ErrDenied
			}
			return Message{}, "", fmt.Errorf("lock original Message attempt: %w", err)
		}
		if originalPracticeID != command.PracticeID ||
			originalLocationID != command.LocationID ||
			command.ThreadID != originalThreadID ||
			command.Destination != originalDestination ||
			command.Body != originalBody ||
			command.TaskID != originalTaskID ||
			(originalAttachmentID == "") != (command.AttachmentID == "") {
			return Message{}, "", ErrConflict
		}
		if originalDelivery != DeliveryFailed &&
			(originalDelivery != DeliveryUnknown ||
				!command.DuplicateRiskAcknowledged) {
			return Message{}, "", ErrConflict
		}
	}
	var sender, profileID string
	if err := tx.QueryRow(ctx, `
		SELECT sender, messaging_profile_id
		FROM messaging_location_configurations
		WHERE practice_id = $1
			AND location_id = $2
			AND active
		FOR SHARE
	`, command.PracticeID, command.LocationID).Scan(
		&sender,
		&profileID,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Message{}, "", ErrConflict
		}
		return Message{}, "", fmt.Errorf("load Location texting configuration: %w", err)
	}

	var thread Thread
	if command.ThreadID != "" {
		thread, err = loadThread(ctx, tx, command.ThreadID)
		if errors.Is(err, pgx.ErrNoRows) {
			return Message{}, "", ErrDenied
		}
		if err != nil {
			return Message{}, "", err
		}
		if thread.PracticeID != command.PracticeID ||
			thread.LocationID != command.LocationID ||
			thread.OfficePhone != sender ||
			(command.Destination != "" &&
				command.Destination != thread.ExternalPhone) {
			return Message{}, "", ErrConflict
		}
		command.Destination = thread.ExternalPhone
	}
	fingerprint, err := sendFingerprint(command)
	if err != nil {
		return Message{}, "", err
	}
	replayed, existingFingerprint, err := loadMessageByIdempotency(
		ctx,
		tx,
		command.PracticeID,
		command.Identity.Subject,
		command.IdempotencyKey,
	)
	if err == nil {
		if !bytes.Equal(existingFingerprint, fingerprint[:]) {
			return Message{}, "", ErrConflict
		}
		if err := tx.Commit(ctx); err != nil {
			return Message{}, "", fmt.Errorf("commit Message replay: %w", err)
		}
		return replayed, MessageDuplicate, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Message{}, "", err
	}

	now := m.now()
	if thread.ID == "" {
		if err := tx.QueryRow(ctx, `
			INSERT INTO messaging_threads (
				practice_id,
				location_id,
				office_phone,
				external_phone,
				created_at,
				updated_at
			)
			VALUES ($1, $2, $3, $4, $5, $5)
			ON CONFLICT (
				practice_id,
				location_id,
				office_phone,
				external_phone
			)
			DO UPDATE SET updated_at = EXCLUDED.updated_at
			RETURNING
				id::text,
				practice_id::text,
				location_id::text,
				office_phone,
				external_phone,
				COALESCE(display_name, ''),
				COALESCE(name_source, ''),
				outbound_blocked,
				created_at,
				updated_at
		`, command.PracticeID, command.LocationID, sender,
			command.Destination, now,
		).Scan(
			&thread.ID,
			&thread.PracticeID,
			&thread.LocationID,
			&thread.OfficePhone,
			&thread.ExternalPhone,
			&thread.DisplayName,
			&thread.NameSource,
			&thread.OutboundBlocked,
			&thread.CreatedAt,
			&thread.UpdatedAt,
		); err != nil {
			return Message{}, "", fmt.Errorf("find or create Message Thread: %w", err)
		}
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE messaging_threads
			SET updated_at = GREATEST(updated_at, $2)
			WHERE id = $1
		`, thread.ID, now); err != nil {
			return Message{}, "", fmt.Errorf("advance Message Thread: %w", err)
		}
		thread.UpdatedAt = now
	}
	thread.LocationName = authorization.ActiveLocation.Name
	if thread.OutboundBlocked {
		return Message{}, "", ErrBlocked
	}
	if command.TaskID != "" {
		if m.work == nil {
			return Message{}, "", ErrInvalidInput
		}
		if _, err := m.work.LockOpenMessageTask(
			ctx,
			tx,
			command.TaskID,
			command.PracticeID,
			command.LocationID,
			thread.ID,
			thread.ExternalPhone,
		); err != nil {
			if errors.Is(err, work.ErrConflict) {
				return Message{}, "", ErrConflict
			}
			if errors.Is(err, work.ErrDenied) {
				return Message{}, "", ErrDenied
			}
			return Message{}, "", err
		}
	}
	var attachment *Attachment
	if command.AttachmentID != "" {
		if m.config.AttachmentStore == nil {
			return Message{}, "", ErrInvalidInput
		}
		var pending Attachment
		var attachmentPracticeID, attachmentLocationID, actorSubject string
		var expiresAt *time.Time
		if err := tx.QueryRow(ctx, `
			SELECT
				id::text,
				practice_id::text,
				location_id::text,
				direction,
				state,
				actor_subject,
				file_name,
				content_type,
				byte_size,
				expires_at,
				created_at,
				updated_at
			FROM messaging_attachments
			WHERE id = $1
			FOR UPDATE
		`, command.AttachmentID).Scan(
			&pending.ID,
			&attachmentPracticeID,
			&attachmentLocationID,
			&pending.Direction,
			&pending.State,
			&actorSubject,
			&pending.FileName,
			&pending.ContentType,
			&pending.ByteSize,
			&expiresAt,
			&pending.CreatedAt,
			&pending.UpdatedAt,
		); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return Message{}, "", ErrConflict
			}
			return Message{}, "", fmt.Errorf("lock pending attachment: %w", err)
		}
		if pending.Direction != DirectionOutbound ||
			pending.State != AttachmentPending ||
			attachmentPracticeID != command.PracticeID ||
			attachmentLocationID != command.LocationID ||
			actorSubject != command.Identity.Subject ||
			expiresAt == nil ||
			!expiresAt.After(m.now()) {
			return Message{}, "", ErrConflict
		}
		if command.RetryOfMessageID != "" &&
			(pending.FileName != originalAttachmentFileName ||
				pending.ContentType != originalAttachmentType ||
				pending.ByteSize != originalAttachmentBytes) {
			return Message{}, "", ErrConflict
		}
		attachment = &pending
	}

	messageID := uuid.NewString()
	if _, err := tx.Exec(ctx, `
		INSERT INTO messaging_messages (
			id,
			thread_id,
			practice_id,
			location_id,
			direction,
			body,
			sender,
			destination,
			delivery_state,
			task_id,
			retry_of_message_id,
			created_by_subject,
			created_at,
			updated_at
		)
		VALUES (
			$1, $2, $3, $4, 'OUTBOUND', NULLIF($5, ''), $6, $7, 'SENDING',
			NULLIF($8, '')::uuid, NULLIF($9, '')::uuid, $10, $11, $11
		)
	`, messageID, thread.ID, command.PracticeID, command.LocationID,
		command.Body, sender, command.Destination, command.TaskID,
		command.RetryOfMessageID, command.Identity.Subject, now,
	); err != nil {
		return Message{}, "", fmt.Errorf("commit outbound Message: %w", err)
	}
	if attachment != nil {
		if _, err := tx.Exec(ctx, `
			UPDATE messaging_attachments
			SET
				message_id = $2,
				state = 'STORED',
				expires_at = NULL,
				updated_at = $3
			WHERE id = $1
				AND state = 'PENDING'
				AND message_id IS NULL
		`, attachment.ID, messageID, now); err != nil {
			return Message{}, "", fmt.Errorf("consume pending attachment: %w", err)
		}
		attachment.MessageID = messageID
		attachment.State = AttachmentStored
		attachment.UpdatedAt = now
	}
	callbackToken, err := newOpaqueToken()
	if err != nil {
		return Message{}, "", err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO messaging_provider_commands (
			id,
			message_id,
			practice_id,
			location_id,
			actor_subject,
			idempotency_key,
			input_fingerprint,
			callback_token,
			messaging_profile_id,
			next_attempt_at,
			created_at,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10, $10)
	`, uuid.NewString(), messageID, command.PracticeID, command.LocationID,
		command.Identity.Subject, command.IdempotencyKey, fingerprint[:],
		callbackToken, profileID, now,
	); err != nil {
		return Message{}, "", fmt.Errorf("commit Message provider command: %w", err)
	}
	if err := m.access.AuditSupportedMutation(
		ctx,
		tx,
		authorization,
		access.SupportedMutationAudit{
			Action:          "message.sent",
			ResourceType:    "message",
			ResourceID:      messageID,
			ResourceVersion: 1,
			OccurredAt:      now,
		},
	); err != nil {
		return Message{}, "", err
	}
	if _, err := m.access.RecordWorkspaceChange(
		ctx,
		tx,
		command.PracticeID,
	); err != nil {
		return Message{}, "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return Message{}, "", fmt.Errorf("commit Message send: %w", err)
	}
	return Message{
		ID:               messageID,
		Thread:           thread,
		Direction:        DirectionOutbound,
		Body:             command.Body,
		Sender:           sender,
		Destination:      command.Destination,
		Delivery:         DeliverySending,
		TaskID:           command.TaskID,
		RetryOfMessageID: command.RetryOfMessageID,
		Attachment:       attachment,
		CreatedAt:        now,
		UpdatedAt:        now,
		Version:          1,
	}, MessageCreated, nil
}

func (m *Module) SendAgain(
	ctx context.Context,
	command SendAgainCommand,
) (Message, MessageCreateStatus, error) {
	command.Identity.Subject = strings.TrimSpace(command.Identity.Subject)
	command.MessageID = strings.TrimSpace(command.MessageID)
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	command.SupportSessionID = strings.TrimSpace(command.SupportSessionID)
	if command.Identity.Subject == "" ||
		command.MessageID == "" ||
		!idempotencyKey.MatchString(command.IdempotencyKey) {
		return Message{}, "", ErrInvalidInput
	}
	original, err := m.readMessageForRetry(ctx, command)
	if err != nil {
		return Message{}, "", err
	}
	replayed, found, err := m.loadSendAgainReplay(
		ctx,
		command,
		original.Thread.PracticeID,
		original.ID,
	)
	if err != nil {
		return Message{}, "", err
	}
	if found {
		return replayed, MessageDuplicate, nil
	}
	if original.Delivery != DeliveryFailed &&
		(original.Delivery != DeliveryUnknown ||
			!command.DuplicateRiskAcknowledged) {
		return Message{}, "", ErrConflict
	}
	attachmentID := ""
	if original.Attachment != nil {
		content, err := m.OpenAttachment(
			ctx,
			command.Identity,
			original.Attachment.ID,
		)
		if err != nil {
			return Message{}, "", err
		}
		retryAttachmentID := uuid.NewSHA1(
			uuid.NameSpaceOID,
			[]byte(strings.Join([]string{
				original.Thread.PracticeID,
				command.Identity.Subject,
				command.IdempotencyKey,
				original.ID,
			}, "\x00")),
		).String()
		clone, err := m.uploadAttachment(ctx, UploadAttachmentCommand{
			Identity:         command.Identity,
			PracticeID:       original.Thread.PracticeID,
			LocationID:       original.Thread.LocationID,
			FileName:         content.Attachment.FileName,
			DeclaredType:     content.Attachment.ContentType,
			Content:          content.Content,
			SupportSessionID: command.SupportSessionID,
		}, retryAttachmentID, command.IdempotencyKey, original.ID)
		if err != nil {
			return Message{}, "", err
		}
		attachmentID = clone.ID
	}
	return m.Send(ctx, SendCommand{
		Identity:                  command.Identity,
		PracticeID:                original.Thread.PracticeID,
		LocationID:                original.Thread.LocationID,
		ThreadID:                  original.Thread.ID,
		Destination:               original.Destination,
		Body:                      original.Body,
		TaskID:                    original.TaskID,
		AttachmentID:              attachmentID,
		RetryOfMessageID:          original.ID,
		DuplicateRiskAcknowledged: command.DuplicateRiskAcknowledged,
		IdempotencyKey:            command.IdempotencyKey,
		SupportSessionID:          command.SupportSessionID,
	})
}

func (m *Module) readMessageForRetry(
	ctx context.Context,
	command SendAgainCommand,
) (Message, error) {
	// Match Send's idempotency -> Access -> Message lock order so a replaying
	// browser request cannot deadlock the transaction creating its first attempt.
	var practiceID, locationID string
	if err := m.pool.QueryRow(ctx, `
		SELECT practice_id::text, location_id::text
		FROM messaging_messages
		WHERE id = $1
	`, command.MessageID).Scan(&practiceID, &locationID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Message{}, ErrDenied
		}
		return Message{}, fmt.Errorf("resolve Message retry scope: %w", err)
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Message{}, fmt.Errorf("begin Message retry read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		SELECT pg_advisory_xact_lock(
			hashtextextended($1, 0)
				# hashtextextended($2, 1)
				# hashtextextended($3, 2)
		)
	`, practiceID, command.Identity.Subject,
		command.IdempotencyKey,
	); err != nil {
		return Message{}, fmt.Errorf("lock Message retry read: %w", err)
	}
	if _, err := m.access.LockReadAuthorization(
		ctx,
		tx,
		command.Identity,
		practiceID,
		locationID,
	); err != nil {
		return Message{}, ErrDenied
	}
	message, err := loadMessage(ctx, tx, command.MessageID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Message{}, ErrDenied
	}
	if err != nil {
		return Message{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Message{}, fmt.Errorf("commit Message retry read: %w", err)
	}
	return message, nil
}

func (m *Module) loadSendAgainReplay(
	ctx context.Context,
	command SendAgainCommand,
	practiceID string,
	originalMessageID string,
) (Message, bool, error) {
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Message{}, false, fmt.Errorf("begin Message new-attempt replay: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	replayed, _, err := loadMessageByIdempotency(
		ctx,
		tx,
		practiceID,
		command.Identity.Subject,
		command.IdempotencyKey,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return Message{}, false, fmt.Errorf("commit empty Message new-attempt replay: %w", err)
		}
		return Message{}, false, nil
	}
	if err != nil {
		return Message{}, false, err
	}
	if replayed.RetryOfMessageID != originalMessageID {
		return Message{}, false, ErrConflict
	}
	if _, err := m.access.LockMutationAuthorization(
		ctx,
		tx,
		command.Identity,
		replayed.Thread.PracticeID,
		replayed.Thread.LocationID,
		command.SupportSessionID,
	); err != nil {
		if isSupportAuthorizationError(err) {
			return Message{}, false, err
		}
		return Message{}, false, ErrDenied
	}
	if err := tx.Commit(ctx); err != nil {
		return Message{}, false, fmt.Errorf("commit Message new-attempt replay: %w", err)
	}
	return replayed, true, nil
}

// ProcessNextCommand claims one committed send intent, records that the
// provider write has begun, then performs the external effect outside the
// PostgreSQL transaction. Any uncertain outcome becomes UNKNOWN and is never
// selected for another write.
func (m *Module) ProcessNextCommand(ctx context.Context) (bool, error) {
	if m.pool == nil || m.access == nil || m.provider == nil {
		return false, ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin Message provider command: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var command ProviderCommand
	var practiceID, locationID string
	var attachmentID string
	var blocked, active bool
	if err := tx.QueryRow(ctx, `
		SELECT
			provider_command.id::text,
			message.id::text,
			message.practice_id::text,
			message.location_id::text,
			message.sender,
			message.destination,
			COALESCE(message.body, ''),
			provider_command.callback_token,
			provider_command.messaging_profile_id,
			COALESCE(attachment.id::text, ''),
			thread.outbound_blocked,
			COALESCE(configuration.active, false)
		FROM messaging_provider_commands provider_command
		JOIN messaging_messages message
			ON message.id = provider_command.message_id
		JOIN messaging_threads thread
			ON thread.id = message.thread_id
		LEFT JOIN messaging_location_configurations configuration
			ON configuration.practice_id = message.practice_id
			AND configuration.location_id = message.location_id
			AND configuration.sender = message.sender
			AND configuration.messaging_profile_id =
				provider_command.messaging_profile_id
		LEFT JOIN messaging_attachments attachment
			ON attachment.message_id = message.id
			AND attachment.state = 'STORED'
		WHERE provider_command.state = 'PENDING'
			AND provider_command.next_attempt_at <= $1
		ORDER BY provider_command.created_at, provider_command.id
		FOR UPDATE OF provider_command SKIP LOCKED
		LIMIT 1
	`, m.now()).Scan(
		&command.ID,
		&command.MessageID,
		&practiceID,
		&locationID,
		&command.Sender,
		&command.Destination,
		&command.Body,
		&command.CallbackToken,
		&command.MessagingProfileID,
		&attachmentID,
		&blocked,
		&active,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.Commit(ctx); err != nil {
				return false, fmt.Errorf("commit empty Message command claim: %w", err)
			}
			return false, nil
		}
		return false, fmt.Errorf("claim Message provider command: %w", err)
	}
	mediaUnavailable := false
	if attachmentID != "" {
		command.MediaURL, err = m.ProviderMediaURL(attachmentID)
		mediaUnavailable = err != nil
	}
	if blocked || !active || mediaUnavailable {
		code := "OUTBOUND_BLOCKED"
		if !active {
			code = "SENDER_CONFIGURATION_CHANGED"
		}
		if mediaUnavailable {
			code = "ATTACHMENT_UNAVAILABLE"
		}
		if _, err := tx.Exec(ctx, `
			UPDATE messaging_provider_commands
			SET state = 'FAILED', last_error_code = $2, completed_at = $3,
				updated_at = $3
			WHERE id = $1
		`, command.ID, code, m.now()); err != nil {
			return false, fmt.Errorf("fail pre-write Message command: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE messaging_messages
			SET delivery_state = 'FAILED', safe_failure_code = $2,
				version = version + 1, updated_at = $3
			WHERE id = $1
		`, command.MessageID, code, m.now()); err != nil {
			return false, fmt.Errorf("fail pre-write Message: %w", err)
		}
		if _, err := m.access.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
			return false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("commit pre-write Message failure: %w", err)
		}
		return true, nil
	}
	writeStartedAt := m.now()
	if _, err := tx.Exec(ctx, `
		UPDATE messaging_provider_commands
		SET state = 'WRITING', write_started_at = $2, updated_at = $2
		WHERE id = $1
	`, command.ID, writeStartedAt); err != nil {
		return false, fmt.Errorf("mark Message provider write begun: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit Message provider write intent: %w", err)
	}

	result, providerErr := m.provider.Send(ctx, command)
	finishTx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return true, fmt.Errorf("begin Message provider result: %w", err)
	}
	defer func() { _ = finishTx.Rollback(ctx) }()
	commandState := "SENT"
	deliveryState := DeliverySent
	errorCode := ""
	providerMessageID := strings.TrimSpace(result.MessageID)
	if providerErr != nil || providerMessageID == "" {
		commandState = "UNKNOWN"
		deliveryState = DeliveryUnknown
		errorCode = "PROVIDER_OUTCOME_UNKNOWN"
		if errors.Is(providerErr, ErrRejected) {
			commandState = "FAILED"
			deliveryState = DeliveryFailed
			errorCode = "PROVIDER_REJECTED"
		}
	}
	finishedAt := m.now()
	if _, err := finishTx.Exec(ctx, `
		UPDATE messaging_provider_commands
		SET
			state = $2,
			provider_message_id = NULLIF($3, ''),
			last_error_code = NULLIF($4, ''),
			completed_at = $5,
			reconcile_until = CASE
				WHEN $2 = 'UNKNOWN'
					THEN $5::timestamptz + interval '24 hours'
				ELSE NULL
			END,
			updated_at = $5
		WHERE id = $1 AND state = 'WRITING'
	`, command.ID, commandState, providerMessageID, errorCode, finishedAt); err != nil {
		return true, fmt.Errorf("record Message provider command result: %w", err)
	}
	if _, err := finishTx.Exec(ctx, `
		UPDATE messaging_messages
		SET
			delivery_state = $2,
			provider_message_id = NULLIF($3, ''),
			safe_failure_code = NULLIF($4, ''),
			version = version + 1,
			updated_at = $5
		WHERE id = $1 AND delivery_state = 'SENDING'
	`, command.MessageID, deliveryState, providerMessageID, errorCode, finishedAt); err != nil {
		return true, fmt.Errorf("project Message provider result: %w", err)
	}
	if _, err := m.access.RecordWorkspaceChange(
		ctx,
		finishTx,
		practiceID,
	); err != nil {
		return true, err
	}
	if err := finishTx.Commit(ctx); err != nil {
		return true, fmt.Errorf("commit Message provider result: %w", err)
	}
	_ = locationID
	return true, nil
}

// RecoverInterruptedCommands closes the process-crash window after a provider
// write began. The command is deliberately not replayed because its external
// effect cannot be known safely.
func (m *Module) RecoverInterruptedCommands(ctx context.Context) error {
	if m.pool == nil || m.access == nil {
		return ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin Message command recovery: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		UPDATE messaging_provider_commands command
		SET
			state = 'UNKNOWN',
			last_error_code = 'PROVIDER_OUTCOME_UNKNOWN',
			completed_at = $1,
			reconcile_until = $1 + interval '24 hours',
			updated_at = $1
		FROM messaging_messages message
		WHERE command.message_id = message.id
			AND command.state = 'WRITING'
			AND command.write_started_at <
				$1::timestamptz - interval '30 seconds'
		RETURNING message.id::text, message.practice_id::text
	`, m.now())
	if err != nil {
		return fmt.Errorf("recover interrupted Message commands: %w", err)
	}
	defer rows.Close()
	type recovered struct {
		messageID  string
		practiceID string
	}
	recoveredCommands := []recovered{}
	for rows.Next() {
		var item recovered
		if err := rows.Scan(&item.messageID, &item.practiceID); err != nil {
			return fmt.Errorf("scan recovered Message command: %w", err)
		}
		recoveredCommands = append(recoveredCommands, item)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate recovered Message commands: %w", err)
	}
	for _, item := range recoveredCommands {
		if _, err := tx.Exec(ctx, `
			UPDATE messaging_messages
			SET
				delivery_state = 'UNKNOWN',
				safe_failure_code = 'PROVIDER_OUTCOME_UNKNOWN',
				version = version + 1,
				updated_at = $2
			WHERE id = $1 AND delivery_state = 'SENDING'
		`, item.messageID, m.now()); err != nil {
			return fmt.Errorf("project interrupted Message command: %w", err)
		}
		if _, err := m.access.RecordWorkspaceChange(
			ctx,
			tx,
			item.practiceID,
		); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Message command recovery: %w", err)
	}
	return nil
}

// ReconcileNextCommand performs one read-only provider lookup for an unknown
// command that already has a provider Message identity. It never repeats the
// original write.
func (m *Module) ReconcileNextCommand(ctx context.Context) (bool, error) {
	if m.pool == nil || m.access == nil || m.provider == nil {
		return false, ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin Message reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var commandID, messageID, practiceID, providerMessageID string
	if err := tx.QueryRow(ctx, `
		SELECT
			command.id::text,
			message.id::text,
			message.practice_id::text,
			command.provider_message_id
		FROM messaging_provider_commands command
		JOIN messaging_messages message ON message.id = command.message_id
		WHERE command.state IN ('UNKNOWN', 'RECONCILING')
			AND command.provider_message_id IS NOT NULL
			AND command.next_attempt_at <= $1
			AND command.reconcile_until > $1
		ORDER BY command.next_attempt_at, command.created_at, command.id
		FOR UPDATE OF command SKIP LOCKED
		LIMIT 1
	`, m.now()).Scan(
		&commandID,
		&messageID,
		&practiceID,
		&providerMessageID,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if err := tx.Commit(ctx); err != nil {
				return false, fmt.Errorf("commit empty Message reconciliation: %w", err)
			}
			return false, nil
		}
		return false, fmt.Errorf("claim Message reconciliation: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE messaging_provider_commands
		SET state = 'RECONCILING', updated_at = $2
		WHERE id = $1
	`, commandID, m.now()); err != nil {
		return false, fmt.Errorf("mark Message reconciliation: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit Message reconciliation claim: %w", err)
	}

	result, providerErr := m.provider.Reconcile(ctx, providerMessageID)
	finishTx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return true, fmt.Errorf("begin Message reconciliation result: %w", err)
	}
	defer func() { _ = finishTx.Rollback(ctx) }()
	if providerErr != nil ||
		result.MessageID != providerMessageID ||
		(result.State != DeliverySent &&
			result.State != DeliveryDelivered &&
			result.State != DeliveryFailed) {
		if _, err := finishTx.Exec(ctx, `
			UPDATE messaging_provider_commands
			SET
				state = 'UNKNOWN',
				next_attempt_at = LEAST(
					reconcile_until,
					$2::timestamptz + interval '5 minutes'
				),
				last_error_code = 'RECONCILIATION_INCONCLUSIVE',
				updated_at = $2
			WHERE id = $1 AND state = 'RECONCILING'
		`, commandID, m.now()); err != nil {
			return true, fmt.Errorf("retain unknown Message reconciliation: %w", err)
		}
		if err := finishTx.Commit(ctx); err != nil {
			return true, fmt.Errorf("commit unknown Message reconciliation: %w", err)
		}
		return true, nil
	}
	var current DeliveryState
	if err := finishTx.QueryRow(ctx, `
		SELECT delivery_state
		FROM messaging_messages
		WHERE id = $1
		FOR UPDATE
	`, messageID).Scan(&current); err != nil {
		return true, fmt.Errorf("lock reconciled Message: %w", err)
	}
	next, changed, contradictory := advanceDelivery(current, result.State)
	commandState := "SENT"
	if result.State == DeliveryFailed {
		commandState = "FAILED"
	}
	if contradictory {
		commandState = "UNKNOWN"
	}
	if changed {
		if _, err := finishTx.Exec(ctx, `
			UPDATE messaging_messages
			SET
				delivery_state = $2,
				safe_failure_code = CASE
					WHEN $2 = 'FAILED' THEN 'PROVIDER_DELIVERY_FAILED'
					ELSE NULL
				END,
				version = version + 1,
				updated_at = $3
			WHERE id = $1
		`, messageID, next, m.now()); err != nil {
			return true, fmt.Errorf("project reconciled Message: %w", err)
		}
		if _, err := m.access.RecordWorkspaceChange(
			ctx,
			finishTx,
			practiceID,
		); err != nil {
			return true, err
		}
	}
	if _, err := finishTx.Exec(ctx, `
		UPDATE messaging_provider_commands
		SET
			state = $2,
			next_attempt_at = CASE
				WHEN $2 = 'UNKNOWN' THEN LEAST(
					COALESCE(
						reconcile_until,
						$3::timestamptz + interval '24 hours'
					),
					$3::timestamptz + interval '5 minutes'
				)
				ELSE next_attempt_at
			END,
			last_error_code = CASE
				WHEN $2 = 'UNKNOWN' THEN 'CONTRADICTORY_TERMINAL_EVIDENCE'
				ELSE NULL
			END,
			completed_at = CASE WHEN $2 = 'UNKNOWN' THEN NULL ELSE $3 END,
			updated_at = $3
		WHERE id = $1 AND state = 'RECONCILING'
	`, commandID, commandState, m.now()); err != nil {
		return true, fmt.Errorf("finish Message reconciliation: %w", err)
	}
	if err := finishTx.Commit(ctx); err != nil {
		return true, fmt.Errorf("commit Message reconciliation result: %w", err)
	}
	return true, nil
}

// ReceiveWebhook verifies the exact raw Telnyx payload and durably receipts
// the unique provider event before returning. Projection is worker-owned.
func (m *Module) ReceiveWebhook(
	ctx context.Context,
	callbackToken string,
	rawBody []byte,
	signatureTimestamp string,
	signature string,
) (WebhookReceipt, error) {
	verifier, validVerifier := telnyxsignature.New(
		m.config.WebhookPublicKeys, m.config.WebhookTolerance, m.now,
	)
	if m.pool == nil || !validVerifier ||
		m.config.WebhookTolerance <= 0 ||
		len(rawBody) == 0 ||
		len(rawBody) > 2*1024*1024 {
		return WebhookReceipt{}, ErrInvalidInput
	}
	timestamp, err := strconv.ParseInt(
		strings.TrimSpace(signatureTimestamp),
		10,
		64,
	)
	if err != nil {
		return WebhookReceipt{}, ErrInvalidInput
	}
	signedAt := time.Unix(timestamp, 0)
	if delta := m.now().Sub(signedAt); delta < -m.config.WebhookTolerance ||
		delta > m.config.WebhookTolerance {
		return WebhookReceipt{}, ErrInvalidInput
	}
	if !verifier.Verify(rawBody, signatureTimestamp, signature) {
		return WebhookReceipt{}, ErrInvalidInput
	}
	envelope, err := decodeWebhook(rawBody)
	if err != nil {
		return WebhookReceipt{}, ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return WebhookReceipt{}, fmt.Errorf("begin provider receipt: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		INSERT INTO messaging_provider_receipts (
			event_id,
			event_type,
			callback_token,
			occurred_at,
			received_at,
			signature_timestamp,
			raw_body
		)
		VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7)
		ON CONFLICT (event_id) DO NOTHING
	`, envelope.Data.ID, envelope.Data.EventType, strings.TrimSpace(callbackToken),
		envelope.Data.OccurredAt, m.now(), timestamp, rawBody,
	)
	if err != nil {
		return WebhookReceipt{}, fmt.Errorf("commit provider receipt: %w", err)
	}
	duplicate := tag.RowsAffected() == 0
	if duplicate {
		var existingCallbackToken string
		var existingRawBody []byte
		if err := tx.QueryRow(ctx, `
			SELECT COALESCE(callback_token, ''), raw_body
			FROM messaging_provider_receipts
			WHERE event_id = $1
			FOR UPDATE
		`, envelope.Data.ID).Scan(
			&existingCallbackToken,
			&existingRawBody,
		); err != nil {
			return WebhookReceipt{}, fmt.Errorf(
				"load duplicate provider receipt: %w",
				err,
			)
		}
		if existingCallbackToken != strings.TrimSpace(callbackToken) ||
			!sameWebhookEventData(existingRawBody, rawBody) {
			return WebhookReceipt{}, ErrConflict
		}
		if _, err := tx.Exec(ctx, `
			UPDATE messaging_provider_receipts
			SET duplicate_count = duplicate_count + 1
			WHERE event_id = $1
		`, envelope.Data.ID); err != nil {
			return WebhookReceipt{}, fmt.Errorf("count duplicate provider receipt: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return WebhookReceipt{}, fmt.Errorf("commit provider receipt transaction: %w", err)
	}
	return WebhookReceipt{
		EventID:   envelope.Data.ID,
		Duplicate: duplicate,
	}, nil
}

func sameWebhookEventData(left []byte, right []byte) bool {
	leftEnvelope, leftErr := decodeWebhook(left)
	rightEnvelope, rightErr := decodeWebhook(right)
	if leftErr != nil || rightErr != nil ||
		leftEnvelope.Data.RecordType != rightEnvelope.Data.RecordType ||
		leftEnvelope.Data.EventType != rightEnvelope.Data.EventType ||
		leftEnvelope.Data.ID != rightEnvelope.Data.ID ||
		!leftEnvelope.Data.OccurredAt.Equal(rightEnvelope.Data.OccurredAt) {
		return false
	}
	var leftPayload, rightPayload any
	if json.Unmarshal(leftEnvelope.Data.Payload, &leftPayload) != nil ||
		json.Unmarshal(rightEnvelope.Data.Payload, &rightPayload) != nil {
		return false
	}
	leftCanonical, leftErr := json.Marshal(leftPayload)
	rightCanonical, rightErr := json.Marshal(rightPayload)
	return leftErr == nil &&
		rightErr == nil &&
		bytes.Equal(leftCanonical, rightCanonical)
}

func (m *Module) ProcessNextReceipt(ctx context.Context) (bool, error) {
	if m.pool == nil || m.access == nil {
		return false, ErrInvalidInput
	}
	claimTx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, fmt.Errorf("begin provider receipt claim: %w", err)
	}
	defer func() { _ = claimTx.Rollback(ctx) }()
	var eventID, callbackToken string
	var rawBody []byte
	if err := claimTx.QueryRow(ctx, `
		SELECT event_id, COALESCE(callback_token, ''), raw_body
		FROM messaging_provider_receipts
		WHERE state = 'PENDING'
			OR (
				state = 'PROCESSING'
				AND processing_started_at <= $1::timestamptz - interval '30 seconds'
			)
		ORDER BY received_at, event_id
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`, m.now()).Scan(&eventID, &callbackToken, &rawBody); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			if err := claimTx.Commit(ctx); err != nil {
				return false, fmt.Errorf("commit empty provider receipt claim: %w", err)
			}
			return false, nil
		}
		return false, fmt.Errorf("claim provider receipt: %w", err)
	}
	if _, err := claimTx.Exec(ctx, `
		UPDATE messaging_provider_receipts
		SET state = 'PROCESSING', processing_started_at = $2
		WHERE event_id = $1
	`, eventID, m.now()); err != nil {
		return false, fmt.Errorf("mark provider receipt processing: %w", err)
	}
	if err := claimTx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit provider receipt claim: %w", err)
	}

	envelope, err := decodeWebhook(rawBody)
	if err != nil {
		if recordErr := m.finishReceipt(
			ctx,
			eventID,
			"FAILED",
			"INVALID_PROVIDER_EVENT",
		); recordErr != nil {
			return true, recordErr
		}
		return true, nil
	}
	if callbackToken == "" {
		if envelope.Data.EventType == "message.received" {
			err := m.projectInboundReceipt(ctx, eventID, envelope)
			if err == nil {
				return true, nil
			}
			return true, m.finishProjectionError(ctx, eventID, err)
		}
		return true, m.finishProjectionError(
			ctx,
			eventID,
			errUnmatchedProviderEvent,
		)
	}
	if err := m.projectOutboundReceipt(
		ctx,
		eventID,
		callbackToken,
		envelope,
	); err != nil {
		return true, m.finishProjectionError(ctx, eventID, err)
	}
	return true, nil
}

func (m *Module) finishProjectionError(
	ctx context.Context,
	eventID string,
	projectionErr error,
) error {
	switch {
	case errors.Is(projectionErr, ErrInvalidInput):
		return m.finishReceipt(
			ctx,
			eventID,
			"FAILED",
			"INVALID_PROVIDER_EVENT",
		)
	case errors.Is(projectionErr, ErrConflict),
		errors.Is(projectionErr, errUnmatchedProviderEvent):
		return m.finishReceipt(
			ctx,
			eventID,
			"UNKNOWN",
			"UNMATCHED_PROVIDER_EVENT",
		)
	default:
		return projectionErr
	}
}

func (m *Module) ReadMessage(
	ctx context.Context,
	identity access.Identity,
	messageID string,
) (Message, error) {
	if m.pool == nil || m.access == nil || strings.TrimSpace(messageID) == "" {
		return Message{}, ErrDenied
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Message{}, fmt.Errorf("begin Message read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	message, err := loadMessage(ctx, tx, messageID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Message{}, ErrDenied
	}
	if err != nil {
		return Message{}, err
	}
	if _, err := m.access.LockReadAuthorization(
		ctx,
		tx,
		identity,
		message.Thread.PracticeID,
		message.Thread.LocationID,
	); err != nil {
		return Message{}, ErrDenied
	}
	if err := tx.Commit(ctx); err != nil {
		return Message{}, fmt.Errorf("commit Message read: %w", err)
	}
	return message, nil
}

func (m *Module) QueryThreads(
	ctx context.Context,
	command QueryThreadsCommand,
) (ThreadPage, error) {
	command.PracticeID = strings.TrimSpace(command.PracticeID)
	command.LocationID = strings.TrimSpace(command.LocationID)
	command.Search = strings.TrimSpace(command.Search)
	if m.pool == nil ||
		m.access == nil ||
		command.PracticeID == "" {
		return ThreadPage{}, ErrInvalidInput
	}
	cursor, err := decodePageCursor(command.Cursor)
	if err != nil {
		return ThreadPage{}, ErrInvalidInput
	}
	limit := command.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 50 {
		return ThreadPage{}, ErrInvalidInput
	}
	searchPhone := ""
	if command.Search != "" {
		var err error
		searchPhone, err = normalizePhone(command.Search)
		if err != nil {
			return ThreadPage{Items: []ThreadSummary{}}, nil
		}
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ThreadPage{}, fmt.Errorf("begin Message Thread query: %w", err)
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
		return ThreadPage{}, ErrDenied
	}
	locationIDs := make([]string, 0, len(authorization.Locations))
	if authorization.ActiveLocation != nil {
		locationIDs = append(locationIDs, authorization.ActiveLocation.ID)
	} else {
		for _, location := range authorization.Locations {
			locationIDs = append(locationIDs, location.ID)
		}
	}
	rows, err := tx.Query(ctx, `
		SELECT
			thread.id::text,
			thread.practice_id::text,
			thread.location_id::text,
			location.name,
			thread.office_phone,
			thread.external_phone,
			COALESCE(thread.display_name, ''),
			COALESCE(thread.name_source, ''),
			thread.outbound_blocked,
			thread.created_at,
			thread.updated_at,
			COALESCE(latest.preview, ''),
			COALESCE(latest.direction, ''),
			COALESCE(latest.delivery_state, ''),
			COALESCE(activity.occurred_at, thread.updated_at),
			EXISTS (
				SELECT 1
				FROM messaging_thread_unreads unread
				WHERE unread.thread_id = thread.id
					AND unread.user_subject = $4
			)
		FROM messaging_threads thread
		JOIN access_locations location
			ON location.practice_id = thread.practice_id
			AND location.id = thread.location_id
		LEFT JOIN LATERAL (
			SELECT
				COALESCE(
					message.body,
					CASE
						WHEN attachment.content_type = 'application/pdf' THEN 'PDF'
						WHEN attachment.id IS NOT NULL THEN 'Image'
						ELSE ''
					END
				) AS preview,
				message.direction,
				message.delivery_state,
				message.created_at
			FROM messaging_messages message
			LEFT JOIN messaging_attachments attachment
				ON attachment.message_id = message.id
			WHERE message.thread_id = thread.id
			ORDER BY message.created_at DESC, message.id DESC
			LIMIT 1
		) latest ON true
		LEFT JOIN LATERAL (
			SELECT max(event.occurred_at) AS occurred_at
			FROM (
				SELECT latest.created_at AS occurred_at
				UNION ALL
				SELECT call.created_at
				FROM human_calling_calls call
				JOIN human_calling_handoffs handoff
					ON handoff.id = call.source_handoff_id
				WHERE call.practice_id = thread.practice_id
					AND call.location_id = thread.location_id
					AND handoff.phone = thread.external_phone
				UNION ALL
				SELECT task.created_at
				FROM work_tasks task
				WHERE task.practice_id = thread.practice_id
					AND task.location_id = thread.location_id
					AND (
						task.message_thread_id = thread.id
						OR (
							task.message_thread_id IS NULL
							AND task.phone = thread.external_phone
						)
					)
			) event
		) activity ON true
		WHERE thread.practice_id = $1
			AND thread.location_id::text = ANY($2::text[])
			AND ($3 = '' OR thread.external_phone = $3)
			AND (
				$6::timestamptz IS NULL
				OR COALESCE(activity.occurred_at, thread.updated_at) < $6
				OR (
					COALESCE(activity.occurred_at, thread.updated_at) = $6
					AND thread.id < $7
				)
			)
		ORDER BY
			COALESCE(activity.occurred_at, thread.updated_at) DESC,
			thread.id DESC
		LIMIT $5
	`, command.PracticeID, locationIDs, searchPhone,
		command.Identity.Subject, limit+1, nullableCursorTime(cursor),
		nullableCursorID(cursor),
	)
	if err != nil {
		return ThreadPage{}, fmt.Errorf("query Message Threads: %w", err)
	}
	defer rows.Close()
	items := make([]ThreadSummary, 0, limit+1)
	for rows.Next() {
		var item ThreadSummary
		if err := rows.Scan(
			&item.ID,
			&item.PracticeID,
			&item.LocationID,
			&item.LocationName,
			&item.OfficePhone,
			&item.ExternalPhone,
			&item.DisplayName,
			&item.NameSource,
			&item.OutboundBlocked,
			&item.CreatedAt,
			&item.UpdatedAt,
			&item.Preview,
			&item.LatestDirection,
			&item.LatestDelivery,
			&item.LatestActivity,
			&item.Unread,
		); err != nil {
			return ThreadPage{}, fmt.Errorf("scan Message Thread: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return ThreadPage{}, fmt.Errorf("iterate Message Threads: %w", err)
	}
	nextCursor := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		nextCursor = encodePageCursor(pageCursor{
			OccurredAt: last.LatestActivity,
			ID:         last.ID,
		})
	}
	if err := tx.Commit(ctx); err != nil {
		return ThreadPage{}, fmt.Errorf("commit Message Thread query: %w", err)
	}
	return ThreadPage{Items: items, NextCursor: nextCursor}, nil
}

func (m *Module) QueryTimeline(
	ctx context.Context,
	command QueryTimelineCommand,
) (TimelinePage, error) {
	command.ThreadID = strings.TrimSpace(command.ThreadID)
	if m.pool == nil ||
		m.access == nil ||
		command.ThreadID == "" {
		return TimelinePage{}, ErrInvalidInput
	}
	cursor, err := decodePageCursor(command.Cursor)
	if err != nil {
		return TimelinePage{}, ErrInvalidInput
	}
	limit := command.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 50 {
		return TimelinePage{}, ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return TimelinePage{}, fmt.Errorf("begin conversation timeline: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	thread, err := loadThread(ctx, tx, command.ThreadID)
	if errors.Is(err, pgx.ErrNoRows) {
		return TimelinePage{}, ErrDenied
	}
	if err != nil {
		return TimelinePage{}, err
	}
	if _, err := m.access.LockReadAuthorization(
		ctx,
		tx,
		command.Identity,
		thread.PracticeID,
		thread.LocationID,
	); err != nil {
		return TimelinePage{}, ErrDenied
	}
	rows, err := tx.Query(ctx, `
		SELECT
			message.id::text,
			message.direction,
			COALESCE(message.body, ''),
			message.sender,
			message.destination,
			message.delivery_state,
			COALESCE(message.safe_failure_code, ''),
			COALESCE(message.provider_message_id, ''),
			COALESCE(message.task_id::text, ''),
			COALESCE(message.retry_of_message_id::text, ''),
			COALESCE(attachment.id::text, ''),
			COALESCE(attachment.direction, ''),
			COALESCE(attachment.state, ''),
			COALESCE(attachment.file_name, ''),
			COALESCE(attachment.content_type, ''),
			COALESCE(attachment.byte_size, 0),
			COALESCE(attachment.created_at, message.created_at),
			COALESCE(attachment.updated_at, message.updated_at),
			message.created_at,
			message.updated_at,
			message.version
		FROM messaging_messages message
		LEFT JOIN messaging_attachments attachment
			ON attachment.message_id = message.id
		WHERE message.thread_id = $1
			AND (
				$3::timestamptz IS NULL
				OR message.created_at < $3
				OR (message.created_at = $3 AND message.id < $4)
			)
		ORDER BY message.created_at DESC, message.id DESC
		LIMIT $2
	`, thread.ID, limit+1, nullableCursorTime(cursor), nullableCursorID(cursor))
	if err != nil {
		return TimelinePage{}, fmt.Errorf("query conversation timeline: %w", err)
	}
	defer rows.Close()
	items := make([]TimelineItem, 0, limit+1)
	for rows.Next() {
		var message Message
		var attachment Attachment
		message.Thread = thread
		if err := rows.Scan(
			&message.ID,
			&message.Direction,
			&message.Body,
			&message.Sender,
			&message.Destination,
			&message.Delivery,
			&message.SafeFailureCode,
			&message.ProviderMessageID,
			&message.TaskID,
			&message.RetryOfMessageID,
			&attachment.ID,
			&attachment.Direction,
			&attachment.State,
			&attachment.FileName,
			&attachment.ContentType,
			&attachment.ByteSize,
			&attachment.CreatedAt,
			&attachment.UpdatedAt,
			&message.CreatedAt,
			&message.UpdatedAt,
			&message.Version,
		); err != nil {
			return TimelinePage{}, fmt.Errorf("scan conversation Message: %w", err)
		}
		if attachment.ID != "" {
			attachment.MessageID = message.ID
			message.Attachment = &attachment
		}
		items = append(items, TimelineItem{
			Type:       "MESSAGE",
			ID:         message.ID,
			OccurredAt: message.CreatedAt,
			Message:    message,
		})
	}
	if err := rows.Err(); err != nil {
		return TimelinePage{}, fmt.Errorf("iterate conversation timeline: %w", err)
	}
	rows.Close()

	callRows, err := tx.Query(ctx, `
		SELECT
			call.id::text,
			call.direction,
			call.created_at,
			call.ended_at,
			CASE
				WHEN bridged_staff.bridged_at IS NOT NULL AND call.ended_at IS NOT NULL
				THEN GREATEST(
					0,
					EXTRACT(EPOCH FROM (call.ended_at - bridged_staff.bridged_at))::bigint
				)
				ELSE 0
			END,
			call.location_id::text,
			location.name,
			COALESCE(membership.email, ''),
			COALESCE(handoff.transfer_reason, ''),
			CASE
				WHEN call.disposition_outcome IN ('FOLLOW_UP_REQUIRED', 'CREATE_TASK')
					THEN 'FOLLOW_UP_REQUIRED'
				WHEN call.disposition_outcome IS NOT NULL THEN 'RESOLVED'
				WHEN call.terminal_outcome = 'VOICEMAIL' THEN 'VOICEMAIL'
				WHEN call.terminal_outcome IN ('MISSED', 'ABANDONED') THEN 'MISSED'
				WHEN call.terminal_outcome IS NOT NULL AND bridged_staff.id IS NOT NULL
					THEN 'NEEDS_DISPOSITION'
				WHEN call.terminal_outcome IS NOT NULL THEN 'UNANSWERED'
				WHEN bridged_staff.state = 'BRIDGED' THEN 'CONNECTED'
				WHEN bridged_staff.state = 'BRIDGE_PENDING' THEN 'CONNECTING'
				ELSE 'RINGING'
			END
		FROM human_calling_calls call
		LEFT JOIN human_calling_handoffs handoff ON handoff.id = call.source_handoff_id
		JOIN access_locations location
			ON location.practice_id = call.practice_id
			AND location.id = call.location_id
		LEFT JOIN LATERAL (
			SELECT leg.id, leg.staff_subject, leg.state, leg.bridged_at
			FROM human_calling_call_legs leg
			WHERE leg.call_id = call.id AND leg.role = 'STAFF'
				AND leg.bridged_at IS NOT NULL
			ORDER BY leg.bridged_at DESC NULLS LAST, leg.updated_at DESC, leg.id DESC
			LIMIT 1
		) bridged_staff ON true
		LEFT JOIN access_memberships membership
			ON membership.practice_id = call.practice_id
			AND membership.user_subject = bridged_staff.staff_subject
		WHERE call.practice_id = $1
			AND call.location_id = $2
			AND COALESCE(handoff.phone, call.destination_phone) = $3
			AND (
				$4::timestamptz IS NULL
				OR call.created_at < $4
				OR (call.created_at = $4 AND call.id < $5)
			)
		ORDER BY call.created_at DESC, call.id DESC
		LIMIT $6
	`, thread.PracticeID, thread.LocationID, thread.ExternalPhone,
		nullableCursorTime(cursor), nullableCursorID(cursor), limit+1,
	)
	if err != nil {
		return TimelinePage{}, fmt.Errorf("query conversation Calls: %w", err)
	}
	for callRows.Next() {
		var call humancalling.CallHistoryItem
		if err := callRows.Scan(
			&call.ID,
			&call.Direction,
			&call.StartedAt,
			&call.EndedAt,
			&call.DurationSeconds,
			&call.LocationID,
			&call.LocationName,
			&call.AnsweredByEmail,
			&call.TransferReason,
			&call.Outcome,
		); err != nil {
			callRows.Close()
			return TimelinePage{}, fmt.Errorf("scan conversation Call: %w", err)
		}
		call.Type = "CALL"
		items = append(items, TimelineItem{
			Type:       "CALL",
			ID:         call.ID,
			OccurredAt: call.StartedAt,
			Call:       call,
		})
	}
	if err := callRows.Err(); err != nil {
		callRows.Close()
		return TimelinePage{}, fmt.Errorf("iterate conversation Calls: %w", err)
	}
	callRows.Close()

	taskRows, err := tx.Query(ctx, `
		SELECT
			task.id::text,
			task.practice_id::text,
			task.location_id::text,
			location.name,
			task.call_id::text,
			task.phone,
			task.title,
			task.state,
			task.origin,
			task.urgency,
			task.category,
			task.caller_name,
			task.source_call_id,
			task.source_message,
			task.source_message_id::text,
			task.message_thread_id::text,
			task.recovery_outcome,
			task.created_by_kind,
			task.created_by_subject,
			task.created_by_email,
			task.created_at,
			task.completed_by_subject,
			task.completed_by_email,
			task.completed_at,
			task.version,
			task.updated_at,
			$3::text,
			task.state = 'OPEN' AND EXISTS (
				SELECT 1
				FROM messaging_thread_unreads unread
				WHERE unread.thread_id = $3::uuid
					AND unread.user_subject = $8
			)
		FROM work_tasks task
		JOIN access_locations location
			ON location.practice_id = task.practice_id
			AND location.id = task.location_id
		WHERE task.practice_id = $1
			AND task.location_id = $2
			AND (
				task.message_thread_id = $3::uuid
				OR (
					task.message_thread_id IS NULL
					AND task.phone = $4
				)
			)
			AND (
				$5::timestamptz IS NULL
				OR task.created_at < $5
				OR (task.created_at = $5 AND task.id < $6)
			)
		ORDER BY task.created_at DESC, task.id DESC
		LIMIT $7
	`, thread.PracticeID, thread.LocationID, thread.ID, thread.ExternalPhone,
		nullableCursorTime(cursor), nullableCursorID(cursor), limit+1,
		command.Identity.Subject,
	)
	if err != nil {
		return TimelinePage{}, fmt.Errorf("query conversation Tasks: %w", err)
	}
	for taskRows.Next() {
		task, err := scanConversationTask(taskRows)
		if err != nil {
			taskRows.Close()
			return TimelinePage{}, fmt.Errorf("scan conversation Task: %w", err)
		}
		items = append(items, TimelineItem{
			Type:       "TASK",
			ID:         task.ID,
			OccurredAt: task.CreatedAt,
			Task:       task,
		})
	}
	if err := taskRows.Err(); err != nil {
		taskRows.Close()
		return TimelinePage{}, fmt.Errorf("iterate conversation Tasks: %w", err)
	}
	taskRows.Close()

	sort.Slice(items, func(left int, right int) bool {
		if items[left].OccurredAt.Equal(items[right].OccurredAt) {
			return items[left].ID > items[right].ID
		}
		return items[left].OccurredAt.After(items[right].OccurredAt)
	})
	nextCursor := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		nextCursor = encodePageCursor(pageCursor{
			OccurredAt: last.OccurredAt,
			ID:         last.ID,
		})
	}
	for left, right := 0, len(items)-1; left < right; left, right = left+1, right-1 {
		items[left], items[right] = items[right], items[left]
	}
	if err := tx.Commit(ctx); err != nil {
		return TimelinePage{}, fmt.Errorf("commit conversation timeline: %w", err)
	}
	return TimelinePage{Items: items, NextCursor: nextCursor}, nil
}

func (m *Module) MarkRead(
	ctx context.Context,
	command MarkReadCommand,
) error {
	command.ThreadID = strings.TrimSpace(command.ThreadID)
	command.SupportSessionID = strings.TrimSpace(command.SupportSessionID)
	if m.pool == nil || m.access == nil || command.ThreadID == "" {
		return ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin Message Thread read: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	thread, err := loadThread(ctx, tx, command.ThreadID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrDenied
	}
	if err != nil {
		return err
	}
	authorization, err := m.access.LockMutationAuthorization(
		ctx,
		tx,
		command.Identity,
		thread.PracticeID,
		thread.LocationID,
		command.SupportSessionID,
	)
	if err != nil {
		if errors.Is(err, access.ErrSupportRequired) ||
			errors.Is(err, access.ErrSupportExpired) ||
			errors.Is(err, access.ErrSupportRevoked) ||
			errors.Is(err, access.ErrSupportPracticeMismatch) {
			return err
		}
		return ErrDenied
	}
	tag, err := tx.Exec(ctx, `
		DELETE FROM messaging_thread_unreads
		WHERE thread_id = $1 AND user_subject = $2
	`, thread.ID, command.Identity.Subject)
	if err != nil {
		return fmt.Errorf("mark Message Thread read: %w", err)
	}
	if tag.RowsAffected() > 0 {
		if err := m.access.AuditSupportedMutation(
			ctx,
			tx,
			authorization,
			access.SupportedMutationAudit{
				Action:          "message_thread.read",
				ResourceType:    "message_thread",
				ResourceID:      thread.ID,
				ResourceVersion: 1,
				OccurredAt:      m.now(),
			},
		); err != nil {
			return err
		}
		if _, err := m.access.RecordWorkspaceChange(
			ctx,
			tx,
			thread.PracticeID,
		); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Message Thread read: %w", err)
	}
	return nil
}

func (m *Module) ApplyTaskUnread(
	ctx context.Context,
	identity access.Identity,
	tasks []work.Task,
) error {
	if m.pool == nil || m.access == nil {
		return ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin Task unread projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorizedLocations := map[string]struct{}{}
	practiceIDs := make([]string, 0, len(tasks))
	locationIDs := make([]string, 0, len(tasks))
	phones := make([]string, 0, len(tasks))
	messageThreadIDs := make([]string, 0, len(tasks))
	unreadEnabled := make([]bool, 0, len(tasks))
	for index := range tasks {
		task := &tasks[index]
		task.Unread = false
		task.ConversationThreadID = ""
		authorizationKey := task.PracticeID + ":" + task.LocationID
		if _, ok := authorizedLocations[authorizationKey]; !ok {
			if _, err := m.access.LockReadAuthorization(
				ctx,
				tx,
				identity,
				task.PracticeID,
				task.LocationID,
			); err != nil {
				return ErrDenied
			}
			authorizedLocations[authorizationKey] = struct{}{}
		}
		practiceIDs = append(practiceIDs, task.PracticeID)
		locationIDs = append(locationIDs, task.LocationID)
		phones = append(phones, task.Phone)
		messageThreadIDs = append(messageThreadIDs, task.MessageThreadID)
		unreadEnabled = append(unreadEnabled, task.State == work.TaskOpen)
	}
	rows, err := tx.Query(ctx, `
		WITH task_input AS (
			SELECT
				input.task_index,
				input.practice_id::uuid AS practice_id,
				input.location_id::uuid AS location_id,
				input.phone,
				NULLIF(input.message_thread_id, '')::uuid AS message_thread_id,
				input.unread_enabled
			FROM unnest(
				$1::text[],
				$2::text[],
				$3::text[],
				$4::text[],
				$5::boolean[]
			) WITH ORDINALITY AS input(
				practice_id,
				location_id,
				phone,
				message_thread_id,
				unread_enabled,
				task_index
			)
		)
		SELECT
			task_input.task_index,
			thread.id::text,
			task_input.unread_enabled
				AND unread.thread_id IS NOT NULL
		FROM task_input
		JOIN LATERAL (
			SELECT candidate.id
			FROM messaging_threads candidate
			WHERE candidate.practice_id = task_input.practice_id
				AND candidate.location_id = task_input.location_id
				AND (
					(
						task_input.message_thread_id IS NOT NULL
						AND candidate.id = task_input.message_thread_id
					)
					OR (
						task_input.message_thread_id IS NULL
						AND candidate.external_phone = task_input.phone
					)
				)
			ORDER BY candidate.updated_at DESC, candidate.id DESC
			LIMIT 1
		) thread ON true
		LEFT JOIN messaging_thread_unreads unread
			ON unread.thread_id = thread.id
			AND unread.user_subject = $6
	`, practiceIDs, locationIDs, phones, messageThreadIDs, unreadEnabled,
		identity.Subject,
	)
	if err != nil {
		return fmt.Errorf("project Task conversations: %w", err)
	}
	for rows.Next() {
		var taskIndex int
		var threadID string
		var unread bool
		if err := rows.Scan(&taskIndex, &threadID, &unread); err != nil {
			rows.Close()
			return fmt.Errorf("scan Task conversation projection: %w", err)
		}
		taskIndex--
		if taskIndex < 0 || taskIndex >= len(tasks) {
			rows.Close()
			return errors.New("task conversation projection index is invalid")
		}
		tasks[taskIndex].ConversationThreadID = threadID
		tasks[taskIndex].Unread = unread
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate Task conversation projection: %w", err)
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Task unread projection: %w", err)
	}
	return nil
}

func (m *Module) CreateFollowUpTask(
	ctx context.Context,
	command CreateFollowUpTaskCommand,
) (work.Task, work.TaskCreateStatus, error) {
	command.MessageID = strings.TrimSpace(command.MessageID)
	command.Title = strings.TrimSpace(command.Title)
	command.SupportSessionID = strings.TrimSpace(command.SupportSessionID)
	if m.pool == nil ||
		m.access == nil ||
		m.work == nil ||
		command.MessageID == "" ||
		len(command.Title) > 500 {
		return work.Task{}, "", ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return work.Task{}, "", fmt.Errorf("begin Message Task creation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var threadID, practiceID, locationID, phone string
	if err := tx.QueryRow(ctx, `
		SELECT
			thread.id::text,
			message.practice_id::text,
			message.location_id::text,
			thread.external_phone
		FROM messaging_messages message
		JOIN messaging_threads thread ON thread.id = message.thread_id
		WHERE message.id = $1
		FOR SHARE OF message, thread
	`, command.MessageID).Scan(
		&threadID,
		&practiceID,
		&locationID,
		&phone,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return work.Task{}, "", ErrDenied
		}
		return work.Task{}, "", fmt.Errorf("load source Message: %w", err)
	}
	authorization, err := m.access.LockMutationAuthorization(
		ctx,
		tx,
		command.Identity,
		practiceID,
		locationID,
		command.SupportSessionID,
	)
	if err != nil {
		if errors.Is(err, access.ErrSupportRequired) ||
			errors.Is(err, access.ErrSupportExpired) ||
			errors.Is(err, access.ErrSupportRevoked) ||
			errors.Is(err, access.ErrSupportPracticeMismatch) {
			return work.Task{}, "", err
		}
		return work.Task{}, "", ErrDenied
	}
	task, status, err := m.work.EnsureMessageFollowUp(
		ctx,
		tx,
		work.EnsureMessageFollowUpCommand{
			MessageID:  command.MessageID,
			ThreadID:   threadID,
			PracticeID: practiceID,
			LocationID: locationID,
			Phone:      phone,
			Title:      command.Title,
			Creator:    authorization.Actor,
		},
	)
	if err != nil {
		if errors.Is(err, work.ErrConflict) {
			return work.Task{}, "", ErrConflict
		}
		if errors.Is(err, work.ErrInvalidInput) {
			return work.Task{}, "", ErrInvalidInput
		}
		return work.Task{}, "", err
	}
	result, err := tx.Exec(ctx, `
		UPDATE messaging_messages
		SET
			task_id = $2,
			updated_at = $3,
			version = version + 1
		WHERE id = $1
			AND (task_id IS NULL OR task_id = $2)
	`, command.MessageID, task.ID, m.now().UTC())
	if err != nil {
		return work.Task{}, "", fmt.Errorf("link Message follow-up Task: %w", err)
	}
	if result.RowsAffected() != 1 {
		return work.Task{}, "", ErrConflict
	}
	if status == work.TaskCreated {
		if err := m.access.AuditSupportedMutation(
			ctx,
			tx,
			authorization,
			access.SupportedMutationAudit{
				Action:          "task.created_from_message",
				ResourceType:    "task",
				ResourceID:      task.ID,
				ResourceVersion: task.Version,
				OccurredAt:      task.CreatedAt,
			},
		); err != nil {
			return work.Task{}, "", err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return work.Task{}, "", fmt.Errorf("commit Message Task creation: %w", err)
	}
	return task, status, nil
}

type webhookEnvelope struct {
	Data struct {
		RecordType string          `json:"record_type"`
		EventType  string          `json:"event_type"`
		ID         string          `json:"id"`
		OccurredAt time.Time       `json:"occurred_at"`
		Payload    json.RawMessage `json:"payload"`
	} `json:"data"`
}

type messageWebhookPayload struct {
	ID             string             `json:"id"`
	From           providerPhone      `json:"from"`
	To             providerRecipients `json:"to"`
	DeliveryStatus string             `json:"delivery_status"`
	Text           string             `json:"text"`
	Media          []struct {
		URL         string `json:"url"`
		ContentType string `json:"content_type"`
	} `json:"media"`
}

type providerPhone string

func (phone *providerPhone) UnmarshalJSON(raw []byte) error {
	var direct string
	if err := json.Unmarshal(raw, &direct); err == nil {
		*phone = providerPhone(direct)
		return nil
	}
	var object struct {
		PhoneNumber string `json:"phone_number"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return err
	}
	*phone = providerPhone(object.PhoneNumber)
	return nil
}

type providerRecipient struct {
	Phone  providerPhone
	Status string
}

type providerRecipients []providerRecipient

func (recipients *providerRecipients) UnmarshalJSON(raw []byte) error {
	var direct string
	if err := json.Unmarshal(raw, &direct); err == nil {
		*recipients = providerRecipients{{Phone: providerPhone(direct)}}
		return nil
	}
	var objects []struct {
		PhoneNumber string `json:"phone_number"`
		Status      string `json:"status"`
	}
	if err := json.Unmarshal(raw, &objects); err == nil {
		result := make(providerRecipients, 0, len(objects))
		for _, object := range objects {
			result = append(result, providerRecipient{
				Phone:  providerPhone(object.PhoneNumber),
				Status: object.Status,
			})
		}
		*recipients = result
		return nil
	}
	var object struct {
		PhoneNumber string `json:"phone_number"`
		Status      string `json:"status"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return err
	}
	*recipients = providerRecipients{{
		Phone:  providerPhone(object.PhoneNumber),
		Status: object.Status,
	}}
	return nil
}

func decodeWebhook(rawBody []byte) (webhookEnvelope, error) {
	var envelope webhookEnvelope
	decoder := json.NewDecoder(bytes.NewReader(rawBody))
	if err := decoder.Decode(&envelope); err != nil {
		return webhookEnvelope{}, err
	}
	if envelope.Data.RecordType != "event" ||
		strings.TrimSpace(envelope.Data.EventType) == "" ||
		strings.TrimSpace(envelope.Data.ID) == "" ||
		envelope.Data.OccurredAt.IsZero() ||
		len(envelope.Data.Payload) == 0 {
		return webhookEnvelope{}, ErrInvalidInput
	}
	return envelope, nil
}

func (m *Module) projectOutboundReceipt(
	ctx context.Context,
	eventID string,
	callbackToken string,
	envelope webhookEnvelope,
) error {
	var payload messageWebhookPayload
	if err := json.Unmarshal(envelope.Data.Payload, &payload); err != nil {
		return ErrInvalidInput
	}
	payload.ID = strings.TrimSpace(payload.ID)
	if payload.ID == "" {
		return ErrInvalidInput
	}
	status := payload.DeliveryStatus
	if status == "" && len(payload.To) > 0 {
		status = payload.To[0].Status
	}
	evidence, ok := deliveryEvidence(
		envelope.Data.EventType,
		status,
	)
	if !ok {
		return ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin delivery projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var commandID, messageID, practiceID, providerMessageID string
	var current DeliveryState
	if err := tx.QueryRow(ctx, `
		SELECT
			provider_command.id::text,
			message.id::text,
			message.practice_id::text,
			message.delivery_state,
			COALESCE(message.provider_message_id, '')
		FROM messaging_provider_commands provider_command
		JOIN messaging_messages message
			ON message.id = provider_command.message_id
		WHERE provider_command.callback_token = $1
		FOR UPDATE OF provider_command, message
	`, callbackToken).Scan(
		&commandID,
		&messageID,
		&practiceID,
		&current,
		&providerMessageID,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errUnmatchedProviderEvent
		}
		return err
	}
	if providerMessageID != "" && providerMessageID != payload.ID {
		return ErrConflict
	}
	next, changed, contradictory := advanceDelivery(current, evidence)
	if contradictory {
		if _, err := tx.Exec(ctx, `
			UPDATE messaging_provider_commands
			SET state = 'RECONCILING',
				last_error_code = 'CONTRADICTORY_TERMINAL_EVIDENCE',
				next_attempt_at = LEAST(
					COALESCE(
						reconcile_until,
						$2::timestamptz + interval '24 hours'
					),
					$2::timestamptz + interval '5 minutes'
				),
				reconcile_until = COALESCE(
					reconcile_until,
					$2::timestamptz + interval '24 hours'
				),
				updated_at = $2
			WHERE id = $1
		`, commandID, m.now()); err != nil {
			return fmt.Errorf("retain contradictory delivery evidence: %w", err)
		}
	}
	if changed {
		if _, err := tx.Exec(ctx, `
			UPDATE messaging_messages
			SET
				delivery_state = $2,
				provider_message_id = COALESCE(provider_message_id, $3),
				safe_failure_code = CASE
					WHEN $2 = 'FAILED' THEN 'PROVIDER_DELIVERY_FAILED'
					ELSE NULL
				END,
				version = version + 1,
				updated_at = $4
			WHERE id = $1
		`, messageID, next, payload.ID, m.now()); err != nil {
			return fmt.Errorf("project delivery state: %w", err)
		}
		if _, err := m.access.RecordWorkspaceChange(
			ctx,
			tx,
			practiceID,
		); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE messaging_provider_commands
		SET
			provider_message_id = COALESCE(provider_message_id, $2),
			state = CASE
				WHEN state = 'UNKNOWN' AND $3 IN ('SENT', 'DELIVERED')
					THEN 'SENT'
				WHEN state = 'UNKNOWN' AND $3 = 'FAILED'
					THEN 'FAILED'
				ELSE state
			END,
			updated_at = $4
		WHERE id = $1
	`, commandID, payload.ID, evidence, m.now()); err != nil {
		return fmt.Errorf("correlate provider delivery evidence: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE messaging_provider_receipts
		SET state = 'APPLIED', projected_at = $2,
			projection_error_code = NULL
		WHERE event_id = $1
	`, eventID, m.now()); err != nil {
		return fmt.Errorf("mark delivery receipt applied: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delivery projection: %w", err)
	}
	return nil
}

func (m *Module) projectInboundReceipt(
	ctx context.Context,
	eventID string,
	envelope webhookEnvelope,
) error {
	var payload messageWebhookPayload
	if err := json.Unmarshal(envelope.Data.Payload, &payload); err != nil {
		return ErrInvalidInput
	}
	providerMessageID := strings.TrimSpace(payload.ID)
	body := strings.TrimSpace(payload.Text)
	from, fromErr := normalizePhone(string(payload.From))
	toValue := ""
	if len(payload.To) > 0 {
		toValue = string(payload.To[0].Phone)
	}
	to, toErr := normalizePhone(toValue)
	if providerMessageID == "" ||
		fromErr != nil ||
		toErr != nil ||
		(body == "" && len(payload.Media) == 0) ||
		len(payload.Media) > 1 ||
		utf8.RuneCountInString(body) > 1600 {
		return ErrInvalidInput
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin inbound Message projection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var practiceID, locationID string
	if err := tx.QueryRow(ctx, `
		SELECT practice_id::text, location_id::text
		FROM messaging_location_configurations
		WHERE sender = $1 AND active
		FOR SHARE
	`, to).Scan(&practiceID, &locationID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errUnmatchedProviderEvent
		}
		return err
	}
	occurredAt := envelope.Data.OccurredAt
	var threadID string
	var wasBlocked bool
	var priorOptOutAt *time.Time
	var priorOptOutEventID string
	if err := tx.QueryRow(ctx, `
		INSERT INTO messaging_threads (
			practice_id,
			location_id,
			office_phone,
			external_phone,
			created_at,
			updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $5)
		ON CONFLICT (
			practice_id,
			location_id,
			office_phone,
			external_phone
		)
		DO UPDATE SET updated_at = GREATEST(
			messaging_threads.updated_at,
			EXCLUDED.updated_at
		)
		RETURNING
			id::text,
			outbound_blocked,
			opt_out_evidence_at,
			COALESCE(opt_out_evidence_event_id, '')
	`, practiceID, locationID, to, from, occurredAt).Scan(
		&threadID,
		&wasBlocked,
		&priorOptOutAt,
		&priorOptOutEventID,
	); err != nil {
		return fmt.Errorf("find inbound Message Thread: %w", err)
	}
	messageID := uuid.NewString()
	tag, err := tx.Exec(ctx, `
		INSERT INTO messaging_messages (
			id,
			thread_id,
			practice_id,
			location_id,
			direction,
			body,
			sender,
			destination,
			delivery_state,
			provider_message_id,
			created_at,
			updated_at
		)
		VALUES (
			$1, $2, $3, $4, 'INBOUND', NULLIF($5, ''), $6, $7,
			'DELIVERED', $8, $9, $9
		)
		ON CONFLICT (provider_message_id) DO NOTHING
	`, messageID, threadID, practiceID, locationID, body, from, to,
		providerMessageID, occurredAt,
	)
	if err != nil {
		return fmt.Errorf("append inbound Message: %w", err)
	}
	inserted := tag.RowsAffected() > 0
	if inserted && len(payload.Media) == 1 {
		providerMediaURL := strings.TrimSpace(payload.Media[0].URL)
		provisionalType := strings.ToLower(strings.TrimSpace(
			payload.Media[0].ContentType,
		))
		switch provisionalType {
		case "image/jpeg", "image/png", "image/gif", "image/webp", "application/pdf":
		default:
			if strings.EqualFold(filepath.Ext(providerMediaURL), ".pdf") {
				provisionalType = "application/pdf"
			} else {
				provisionalType = "image/jpeg"
			}
		}
		state := AttachmentProcessing
		parsedMediaURL, parseErr := url.Parse(providerMediaURL)
		if parseErr != nil ||
			parsedMediaURL.Scheme != "https" ||
			parsedMediaURL.Host == "" {
			state = AttachmentUnavailable
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO messaging_attachments (
				id,
				practice_id,
				location_id,
				message_id,
				direction,
				state,
				file_name,
				content_type,
				provider_media_url,
				created_at,
				updated_at
			)
			VALUES (
				$1, $2, $3, $4, 'INBOUND', $5, $6, $7, $8, $9, $9
			)
		`, uuid.NewString(), practiceID, locationID, messageID, state,
			attachmentFileName(provisionalType), provisionalType,
			providerMediaURL, occurredAt,
		); err != nil {
			return fmt.Errorf("record inbound attachment: %w", err)
		}
	}
	if inserted {
		if _, err := tx.Exec(ctx, `
			INSERT INTO messaging_thread_unreads (
				thread_id,
				user_subject,
				unread_since,
				latest_message_id
			)
			SELECT
				$1,
				membership.user_subject,
				$2,
				$3
			FROM access_memberships membership
			WHERE membership.practice_id = $4
				AND membership.revoked_at IS NULL
				AND (
					membership.location_scope = 'ALL'
					OR EXISTS (
						SELECT 1
						FROM access_membership_locations location_grant
						WHERE location_grant.membership_id = membership.id
							AND location_grant.practice_id = membership.practice_id
							AND location_grant.location_id = $5
					)
				)
			ON CONFLICT (thread_id, user_subject)
			DO UPDATE SET
				unread_since = LEAST(
					messaging_thread_unreads.unread_since,
					EXCLUDED.unread_since
				),
				latest_message_id = EXCLUDED.latest_message_id
		`, threadID, occurredAt, messageID, practiceID, locationID); err != nil {
			return fmt.Errorf("mark inbound Message unread: %w", err)
		}
	}
	blocked := isStop(body)
	started := isStart(body)
	isNewerOptOutEvidence := priorOptOutAt == nil ||
		occurredAt.After(*priorOptOutAt) ||
		(occurredAt.Equal(*priorOptOutAt) &&
			((blocked && !wasBlocked) ||
				(blocked == wasBlocked &&
					eventID > priorOptOutEventID)))
	applyOptOut := (blocked || started) && isNewerOptOutEvidence
	optOutChanged := applyOptOut &&
		((blocked && !wasBlocked) || (started && wasBlocked))
	if applyOptOut {
		if _, err := tx.Exec(ctx, `
			UPDATE messaging_threads
			SET
				outbound_blocked = $2,
				opt_out_evidence_at = $3,
				opt_out_evidence_event_id = $4,
				updated_at = GREATEST(updated_at, $3)
			WHERE id = $1
		`, threadID, blocked, occurredAt, eventID); err != nil {
			return fmt.Errorf("project provider opt-out evidence: %w", err)
		}
		if blocked {
			if _, err := tx.Exec(ctx, `
				WITH failed_commands AS (
					UPDATE messaging_provider_commands command
					SET
						state = 'FAILED',
						last_error_code = 'OUTBOUND_BLOCKED',
						completed_at = $2,
						updated_at = $2
					FROM messaging_messages message
					WHERE command.message_id = message.id
						AND message.thread_id = $1
						AND command.state = 'PENDING'
					RETURNING message.id
				)
				UPDATE messaging_messages message
				SET
					delivery_state = 'FAILED',
					safe_failure_code = 'OUTBOUND_BLOCKED',
					version = version + 1,
					updated_at = $2
				FROM failed_commands
				WHERE message.id = failed_commands.id
			`, threadID, m.now()); err != nil {
				return fmt.Errorf("fail queued Messages after opt-out: %w", err)
			}
		}
	}
	if inserted || optOutChanged {
		if _, err := m.access.RecordWorkspaceChange(
			ctx,
			tx,
			practiceID,
		); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE messaging_provider_receipts
		SET state = 'APPLIED', projected_at = $2,
			projection_error_code = NULL
		WHERE event_id = $1
	`, eventID, m.now()); err != nil {
		return fmt.Errorf("mark inbound receipt applied: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit inbound Message projection: %w", err)
	}
	return nil
}

func (m *Module) finishReceipt(
	ctx context.Context,
	eventID string,
	state string,
	errorCode string,
) error {
	if _, err := m.pool.Exec(ctx, `
		UPDATE messaging_provider_receipts
		SET state = $2, projection_error_code = $3, projected_at = $4
		WHERE event_id = $1
	`, eventID, state, errorCode, m.now()); err != nil {
		return fmt.Errorf("finish provider receipt: %w", err)
	}
	return nil
}

func deliveryEvidence(
	eventType string,
	status string,
) (DeliveryState, bool) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "sent", "accepted", "queued":
		return DeliverySent, true
	case "delivered":
		return DeliveryDelivered, true
	case "failed", "sending_failed", "delivery_failed", "undelivered":
		return DeliveryFailed, true
	}
	if eventType == "message.sent" {
		return DeliverySent, true
	}
	return "", false
}

func advanceDelivery(
	current DeliveryState,
	evidence DeliveryState,
) (DeliveryState, bool, bool) {
	if current == evidence {
		return current, false, false
	}
	switch current {
	case DeliverySending, DeliveryUnknown:
		return evidence, true, false
	case DeliverySent:
		if evidence == DeliveryDelivered || evidence == DeliveryFailed {
			return evidence, true, false
		}
		return current, false, false
	case DeliveryDelivered, DeliveryFailed:
		return current, false, evidence != current
	default:
		return current, false, false
	}
}

func isStop(body string) bool {
	switch strings.ToUpper(strings.TrimSpace(body)) {
	case "STOP", "STOPALL", "UNSUBSCRIBE", "CANCEL", "END", "QUIT":
		return true
	default:
		return false
	}
}

func isStart(body string) bool {
	switch strings.ToUpper(strings.TrimSpace(body)) {
	case "START", "UNSTOP":
		return true
	default:
		return false
	}
}

func encodePageCursor(cursor pageCursor) string {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodePageCursor(raw string) (*pageCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	var cursor pageCursor
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil ||
		cursor.OccurredAt.IsZero() ||
		uuid.Validate(cursor.ID) != nil {
		return nil, ErrInvalidInput
	}
	return &cursor, nil
}

func decodeTimelineItemCursor(raw string) (*pageCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	var cursor pageCursor
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil ||
		cursor.OccurredAt.IsZero() {
		return nil, ErrInvalidInput
	}
	parts := strings.SplitN(cursor.ID, ":", 2)
	if len(parts) != 2 ||
		(parts[0] != "MESSAGE" &&
			parts[0] != "CALL" &&
			parts[0] != "TASK") ||
		uuid.Validate(parts[1]) != nil {
		return nil, ErrInvalidInput
	}
	return &cursor, nil
}

func nullableCursorTime(cursor *pageCursor) any {
	if cursor == nil {
		return nil
	}
	return cursor.OccurredAt
}

func nullableCursorID(cursor *pageCursor) any {
	if cursor == nil {
		return nil
	}
	return cursor.ID
}

type conversationTaskScanner interface {
	Scan(...any) error
}

func readConversationTask(ctx context.Context, tx pgx.Tx, taskID string) (work.Task, error) {
	return scanConversationTask(tx.QueryRow(ctx, `
		SELECT
			task.id::text,
			task.practice_id::text,
			task.location_id::text,
			location.name,
			task.call_id::text,
			task.phone,
			task.title,
			task.state,
			task.origin,
			task.urgency,
			task.category,
			task.caller_name,
			task.source_call_id,
			task.source_message,
			task.source_message_id::text,
			task.message_thread_id::text,
			task.recovery_outcome,
			task.created_by_kind,
			task.created_by_subject,
			task.created_by_email,
			task.created_at,
			task.completed_by_subject,
			task.completed_by_email,
			task.completed_at,
			task.version,
			task.updated_at,
			COALESCE(task.message_thread_id::text, ''),
			false
		FROM work_tasks task
		JOIN access_locations location
			ON location.practice_id = task.practice_id
			AND location.id = task.location_id
		WHERE task.id = $1
	`, taskID))
}

func scanConversationTask(scanner conversationTaskScanner) (work.Task, error) {
	var task work.Task
	var callID, category, callerName, sourceCall, sourceMessage *string
	var messageID, messageThreadID, recoveryOutcome *string
	var createdEmail, completedSubject, completedEmail *string
	if err := scanner.Scan(
		&task.ID,
		&task.PracticeID,
		&task.LocationID,
		&task.LocationName,
		&callID,
		&task.Phone,
		&task.Title,
		&task.State,
		&task.Origin,
		&task.Urgency,
		&category,
		&callerName,
		&sourceCall,
		&sourceMessage,
		&messageID,
		&messageThreadID,
		&recoveryOutcome,
		&task.CreatedBy.Kind,
		&task.CreatedBy.Subject,
		&createdEmail,
		&task.CreatedAt,
		&completedSubject,
		&completedEmail,
		&task.CompletedAt,
		&task.Version,
		&task.UpdatedAt,
		&task.ConversationThreadID,
		&task.Unread,
	); err != nil {
		return work.Task{}, err
	}
	if callID != nil {
		task.CallID = *callID
	}
	if category != nil {
		task.Category = work.TaskCategory(*category)
	}
	if callerName != nil {
		task.CallerName = *callerName
	}
	if sourceCall != nil {
		task.SourceCallID = *sourceCall
	}
	if sourceMessage != nil {
		task.SourceMessage = *sourceMessage
	}
	if messageID != nil {
		task.MessageID = *messageID
	}
	if messageThreadID != nil {
		task.MessageThreadID = *messageThreadID
	}
	if recoveryOutcome != nil {
		task.RecoveryOutcome = work.RecoveryOutcome(*recoveryOutcome)
	}
	if createdEmail != nil {
		task.CreatedBy.Email = *createdEmail
	}
	if completedSubject != nil && completedEmail != nil {
		task.CompletedBy = &work.ActorSnapshot{
			Kind:    access.ActorHuman,
			Subject: *completedSubject,
			Email:   *completedEmail,
		}
	}
	return task, nil
}

func loadMessageByIdempotency(
	ctx context.Context,
	tx pgx.Tx,
	practiceID string,
	actorSubject string,
	key string,
) (Message, []byte, error) {
	var result Message
	var fingerprint []byte
	row := tx.QueryRow(ctx, `
		SELECT
			message.id::text,
			thread.id::text,
			thread.practice_id::text,
			thread.location_id::text,
			location.name,
			thread.office_phone,
			thread.external_phone,
			COALESCE(thread.display_name, ''),
			COALESCE(thread.name_source, ''),
			thread.outbound_blocked,
			thread.created_at,
			thread.updated_at,
			message.direction,
			COALESCE(message.body, ''),
			message.sender,
			message.destination,
			message.delivery_state,
			COALESCE(message.safe_failure_code, ''),
			COALESCE(message.provider_message_id, ''),
			COALESCE(message.task_id::text, ''),
			COALESCE(message.retry_of_message_id::text, ''),
			COALESCE(attachment.id::text, ''),
			COALESCE(attachment.direction, ''),
			COALESCE(attachment.state, ''),
			COALESCE(attachment.file_name, ''),
			COALESCE(attachment.content_type, ''),
			COALESCE(attachment.byte_size, 0),
			COALESCE(attachment.created_at, message.created_at),
			COALESCE(attachment.updated_at, message.updated_at),
			message.created_at,
			message.updated_at,
			message.version,
			command.input_fingerprint
		FROM messaging_provider_commands command
		JOIN messaging_messages message ON message.id = command.message_id
		LEFT JOIN messaging_attachments attachment
			ON attachment.message_id = message.id
		JOIN messaging_threads thread ON thread.id = message.thread_id
		JOIN access_locations location
			ON location.practice_id = thread.practice_id
			AND location.id = thread.location_id
		WHERE command.practice_id = $1
			AND command.actor_subject = $2
			AND command.idempotency_key = $3
	`, practiceID, actorSubject, key)
	var attachment Attachment
	err := row.Scan(
		&result.ID,
		&result.Thread.ID,
		&result.Thread.PracticeID,
		&result.Thread.LocationID,
		&result.Thread.LocationName,
		&result.Thread.OfficePhone,
		&result.Thread.ExternalPhone,
		&result.Thread.DisplayName,
		&result.Thread.NameSource,
		&result.Thread.OutboundBlocked,
		&result.Thread.CreatedAt,
		&result.Thread.UpdatedAt,
		&result.Direction,
		&result.Body,
		&result.Sender,
		&result.Destination,
		&result.Delivery,
		&result.SafeFailureCode,
		&result.ProviderMessageID,
		&result.TaskID,
		&result.RetryOfMessageID,
		&attachment.ID,
		&attachment.Direction,
		&attachment.State,
		&attachment.FileName,
		&attachment.ContentType,
		&attachment.ByteSize,
		&attachment.CreatedAt,
		&attachment.UpdatedAt,
		&result.CreatedAt,
		&result.UpdatedAt,
		&result.Version,
		&fingerprint,
	)
	if err == nil && attachment.ID != "" {
		attachment.MessageID = result.ID
		result.Attachment = &attachment
	}
	return result, fingerprint, err
}

func loadMessage(
	ctx context.Context,
	tx pgx.Tx,
	messageID string,
) (Message, error) {
	var result Message
	row := tx.QueryRow(ctx, `
		SELECT
			message.id::text,
			thread.id::text,
			thread.practice_id::text,
			thread.location_id::text,
			location.name,
			thread.office_phone,
			thread.external_phone,
			COALESCE(thread.display_name, ''),
			COALESCE(thread.name_source, ''),
			thread.outbound_blocked,
			thread.created_at,
			thread.updated_at,
			message.direction,
			COALESCE(message.body, ''),
			message.sender,
			message.destination,
			message.delivery_state,
			COALESCE(message.safe_failure_code, ''),
			COALESCE(message.provider_message_id, ''),
			COALESCE(message.task_id::text, ''),
			COALESCE(message.retry_of_message_id::text, ''),
			COALESCE(attachment.id::text, ''),
			COALESCE(attachment.direction, ''),
			COALESCE(attachment.state, ''),
			COALESCE(attachment.file_name, ''),
			COALESCE(attachment.content_type, ''),
			COALESCE(attachment.byte_size, 0),
			COALESCE(attachment.created_at, message.created_at),
			COALESCE(attachment.updated_at, message.updated_at),
			message.created_at,
			message.updated_at,
			message.version
		FROM messaging_messages message
		LEFT JOIN messaging_attachments attachment
			ON attachment.message_id = message.id
		JOIN messaging_threads thread ON thread.id = message.thread_id
		JOIN access_locations location
			ON location.practice_id = thread.practice_id
			AND location.id = thread.location_id
		WHERE message.id = $1
		FOR SHARE OF message, thread
	`, messageID)
	var attachment Attachment
	err := row.Scan(
		&result.ID,
		&result.Thread.ID,
		&result.Thread.PracticeID,
		&result.Thread.LocationID,
		&result.Thread.LocationName,
		&result.Thread.OfficePhone,
		&result.Thread.ExternalPhone,
		&result.Thread.DisplayName,
		&result.Thread.NameSource,
		&result.Thread.OutboundBlocked,
		&result.Thread.CreatedAt,
		&result.Thread.UpdatedAt,
		&result.Direction,
		&result.Body,
		&result.Sender,
		&result.Destination,
		&result.Delivery,
		&result.SafeFailureCode,
		&result.ProviderMessageID,
		&result.TaskID,
		&result.RetryOfMessageID,
		&attachment.ID,
		&attachment.Direction,
		&attachment.State,
		&attachment.FileName,
		&attachment.ContentType,
		&attachment.ByteSize,
		&attachment.CreatedAt,
		&attachment.UpdatedAt,
		&result.CreatedAt,
		&result.UpdatedAt,
		&result.Version,
	)
	if err != nil {
		return Message{}, fmt.Errorf("load Message: %w", err)
	}
	if attachment.ID != "" {
		attachment.MessageID = result.ID
		result.Attachment = &attachment
	}
	return result, nil
}

func loadThread(
	ctx context.Context,
	tx pgx.Tx,
	threadID string,
) (Thread, error) {
	var thread Thread
	if err := tx.QueryRow(ctx, `
		SELECT
			thread.id::text,
			thread.practice_id::text,
			thread.location_id::text,
			location.name,
			thread.office_phone,
			thread.external_phone,
			COALESCE(thread.display_name, ''),
			COALESCE(thread.name_source, ''),
			thread.outbound_blocked,
			thread.created_at,
			thread.updated_at
		FROM messaging_threads thread
		JOIN access_locations location
			ON location.practice_id = thread.practice_id
			AND location.id = thread.location_id
		WHERE thread.id = $1
		FOR SHARE OF thread
	`, threadID).Scan(
		&thread.ID,
		&thread.PracticeID,
		&thread.LocationID,
		&thread.LocationName,
		&thread.OfficePhone,
		&thread.ExternalPhone,
		&thread.DisplayName,
		&thread.NameSource,
		&thread.OutboundBlocked,
		&thread.CreatedAt,
		&thread.UpdatedAt,
	); err != nil {
		return Thread{}, fmt.Errorf("load Message Thread: %w", err)
	}
	return thread, nil
}

func normalizeSendCommand(command *SendCommand) {
	command.Identity.Subject = strings.TrimSpace(command.Identity.Subject)
	command.PracticeID = strings.TrimSpace(command.PracticeID)
	command.LocationID = strings.TrimSpace(command.LocationID)
	command.ThreadID = strings.TrimSpace(command.ThreadID)
	command.Destination = strings.TrimSpace(command.Destination)
	command.Body = strings.TrimSpace(command.Body)
	command.TaskID = strings.TrimSpace(command.TaskID)
	command.AttachmentID = strings.TrimSpace(command.AttachmentID)
	command.RetryOfMessageID = strings.TrimSpace(command.RetryOfMessageID)
	command.IdempotencyKey = strings.TrimSpace(command.IdempotencyKey)
	command.SupportSessionID = strings.TrimSpace(command.SupportSessionID)
}

func normalizePhone(value string) (string, error) {
	value = strings.TrimSpace(value)
	if canonicalPhone.MatchString(value) {
		return value, nil
	}
	var digits strings.Builder
	openParenthesis := -1
	closeParenthesis := -1
	for index, character := range value {
		switch {
		case character >= '0' && character <= '9':
			digits.WriteRune(character)
		case character == '+' && index == 0:
		case character == ' ' || character == '-' || character == '.':
		case character == '(' && openParenthesis == -1:
			openParenthesis = index
		case character == ')' && closeParenthesis == -1:
			closeParenthesis = index
		default:
			return "", ErrInvalidInput
		}
	}
	if (openParenthesis == -1) != (closeParenthesis == -1) ||
		(openParenthesis >= 0 && closeParenthesis <= openParenthesis) {
		return "", ErrInvalidInput
	}
	normalized := digits.String()
	if len(normalized) == 10 {
		normalized = "1" + normalized
	}
	normalized = "+" + normalized
	if !canonicalPhone.MatchString(normalized) {
		return "", ErrInvalidInput
	}
	return normalized, nil
}

func NormalizePhone(value string) (string, error) {
	return normalizePhone(value)
}

func sendFingerprint(command SendCommand) ([32]byte, error) {
	encoded, err := json.Marshal(struct {
		ActorSubject     string `json:"actorSubject"`
		PracticeID       string `json:"practiceId"`
		LocationID       string `json:"locationId"`
		Destination      string `json:"destination"`
		Body             string `json:"body"`
		ThreadID         string `json:"threadId"`
		TaskID           string `json:"taskId"`
		AttachmentID     string `json:"attachmentId"`
		RetryOfMessageID string `json:"retryOfMessageId"`
	}{
		ActorSubject:     command.Identity.Subject,
		PracticeID:       command.PracticeID,
		LocationID:       command.LocationID,
		Destination:      command.Destination,
		Body:             command.Body,
		ThreadID:         command.ThreadID,
		TaskID:           command.TaskID,
		AttachmentID:     command.AttachmentID,
		RetryOfMessageID: command.RetryOfMessageID,
	})
	if err != nil {
		return [32]byte{}, fmt.Errorf("encode Message fingerprint: %w", err)
	}
	return sha256.Sum256(encoded), nil
}

func newOpaqueToken() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("create provider callback token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
