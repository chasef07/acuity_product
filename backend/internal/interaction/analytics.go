package interaction

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type AnalyticsRange string

const (
	AnalyticsRange24Hours AnalyticsRange = "24h"
	AnalyticsRange7Days   AnalyticsRange = "7d"
	AnalyticsRange30Days  AnalyticsRange = "30d"
)

type QueryAnalyticsCommand struct {
	Identity   access.Identity
	PracticeID string
	LocationID string
	Range      AnalyticsRange
	Cursor     string
	Limit      int
}

type AnalyticsSummary struct {
	TotalCalls        int
	BookingCount      int
	CancellationCount int
	RescheduleCount   int
	P50SttMs          *int
	P90SttMs          *int
	P99SttMs          *int
	P50TtftMs         *int
	P90TtftMs         *int
	P99TtftMs         *int
	P50TtsTtfbMs      *int
	P90TtsTtfbMs      *int
	P99TtsTtfbMs      *int
	P50TotalLatencyMs *int
	P90TotalLatencyMs *int
	P99TotalLatencyMs *int
	TransferCount     int
	TransferRate      float64
	ToolCallCount     int
	ToolErrorCount    int
	ToolFailureRate   float64
	latencySamples    latencyValueSet
}

type AnalyticsCall struct {
	ID                  string
	LocationID          string
	LocationName        string
	SourceCallID        string
	Phone               string
	StartedAt           time.Time
	EndedAt             *time.Time
	Status              CallStatus
	DurationSeconds     int
	P50SttMs            *int
	P50TtftMs           *int
	P50TtsTtfbMs        *int
	P50TotalLatencyMs   *int
	ToolCallCount       int
	ToolErrorCount      int
	ToolActions         []string
	Transferred         bool
	TranscriptAvailable bool
}

type AnalyticsPage struct {
	Summary    AnalyticsSummary
	Calls      []AnalyticsCall
	NextCursor string
}

type TimelineKind string

const (
	TimelineCallerMessage TimelineKind = "CALLER_MESSAGE"
	TimelineAgentMessage  TimelineKind = "AGENT_MESSAGE"
	TimelineToolCall      TimelineKind = "TOOL_CALL"
	TimelineToolResult    TimelineKind = "TOOL_RESULT"
)

type TimelineItem struct {
	Kind           TimelineKind
	OccurredAt     time.Time
	Text           string
	Name           string
	CallID         string
	Payload        map[string]any
	Error          string
	SttMs          *int
	TtftMs         *int
	TtsTtfbMs      *int
	TotalLatencyMs *int
}

type ToolExecution struct {
	CallID        string
	Name          string
	OccurredAt    time.Time
	Status        string // Native LiveKit execution status.
	OutputClass   string // Historical Agent output classification only.
	DomainOutcome string // Correlated Acuity domain outcome, when present.
	DomainStatus  string // Correlated Acuity business-result status, when present.
	TaskID        string // Durable Product Task proving Staff Task follow-up.
}

type OperatorAnalyticsDetail struct {
	Interaction       Interaction
	P50SttMs          *int
	P50TtftMs         *int
	P50TtsTtfbMs      *int
	P50TotalLatencyMs *int
	Timeline          []TimelineItem
	ToolExecutions    []ToolExecution
}

type analyticsCursor struct {
	Range      AnalyticsRange `json:"range"`
	PracticeID string         `json:"practiceId"`
	LocationID string         `json:"locationId,omitempty"`
	StartedAt  time.Time      `json:"startedAt"`
	ID         string         `json:"id"`
}

type analyticsProjection struct {
	call               AnalyticsCall
	appointmentOutcome AppointmentOutcome
	transcript         json.RawMessage
	closeoutPayload    json.RawMessage
	latencySamples     latencyValueSet
}

func (m *Module) QueryAnalytics(
	ctx context.Context,
	command QueryAnalyticsCommand,
) (AnalyticsPage, error) {
	normalizeAnalyticsCommand(&command)
	duration, ok := analyticsRangeDuration(command.Range)
	if m.database == nil || m.access == nil || !ok ||
		!validUUID(command.PracticeID) ||
		(command.LocationID != "" && !validUUID(command.LocationID)) ||
		command.Limit < 1 || command.Limit > 100 {
		return AnalyticsPage{}, ErrInvalidInput
	}
	cursor, err := decodeAnalyticsCursor(command)
	if err != nil {
		return AnalyticsPage{}, ErrInvalidInput
	}

	to := m.now().UTC().Truncate(time.Microsecond)
	from := to.Add(-duration)
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return AnalyticsPage{}, fmt.Errorf("begin operator AI analytics query: %w", err)
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
		if errors.Is(err, access.ErrDenied) {
			return AnalyticsPage{}, ErrDenied
		}
		return AnalyticsPage{}, fmt.Errorf("authorize operator AI analytics query: %w", err)
	}
	if !authorization.PlatformOperator {
		return AnalyticsPage{}, ErrDenied
	}

	locationIDs := authorizedLocationIDs(authorization, command.LocationID)
	if len(locationIDs) == 0 {
		return AnalyticsPage{}, ErrDenied
	}
	summary, err := queryAnalyticsSummary(ctx, tx, command, locationIDs, from, to)
	if err != nil {
		return AnalyticsPage{}, err
	}
	calls, hasMore, err := queryAnalyticsCalls(
		ctx,
		tx,
		command,
		locationIDs,
		from,
		to,
		cursor,
	)
	if err != nil {
		return AnalyticsPage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AnalyticsPage{}, fmt.Errorf("commit operator AI analytics query: %w", err)
	}

	page := AnalyticsPage{Summary: summary, Calls: calls}
	if hasMore && len(page.Calls) > 0 {
		page.NextCursor, err = encodeAnalyticsCursor(command, page.Calls[len(page.Calls)-1])
		if err != nil {
			return AnalyticsPage{}, fmt.Errorf("encode operator AI analytics cursor: %w", err)
		}
	}
	return page, nil
}

func queryAnalyticsSummary(
	ctx context.Context,
	tx pgx.Tx,
	command QueryAnalyticsCommand,
	locationIDs []string,
	from time.Time,
	to time.Time,
) (AnalyticsSummary, error) {
	rows, err := tx.Query(ctx, `
		SELECT
			interaction.started_at,
			interaction.status,
			interaction.appointment_outcome,
			COALESCE(interaction.transcript, '{}'::jsonb),
			COALESCE(interaction.closeout_payload, '{}'::jsonb)
		FROM ai_interactions interaction
		WHERE interaction.practice_id = $1
			AND interaction.location_id::text = ANY($2::text[])
			AND interaction.started_at >= $3
			AND interaction.started_at <= $4
	`, command.PracticeID, locationIDs, from, to)
	if err != nil {
		return AnalyticsSummary{}, fmt.Errorf("query operator AI analytics summary: %w", err)
	}
	defer rows.Close()
	summary := AnalyticsSummary{}
	for rows.Next() {
		var projection analyticsProjection
		if err := rows.Scan(
			&projection.call.StartedAt,
			&projection.call.Status,
			&projection.appointmentOutcome,
			&projection.transcript,
			&projection.closeoutPayload,
		); err != nil {
			return AnalyticsSummary{}, fmt.Errorf("scan operator AI analytics summary: %w", err)
		}
		projectAnalyticsEvidence(&projection)
		summarizeAnalyticsProjection(&summary, projection)
	}
	if err := rows.Err(); err != nil {
		return AnalyticsSummary{}, fmt.Errorf("iterate operator AI analytics summary: %w", err)
	}
	finalizeAnalyticsSummary(&summary)
	return summary, nil
}

func summarizeAnalyticsProjection(summary *AnalyticsSummary, projection analyticsProjection) {
	summary.TotalCalls++
	switch projection.appointmentOutcome {
	case OutcomeBooking:
		summary.BookingCount++
	case OutcomeCancellation:
		summary.CancellationCount++
	case OutcomeReschedule:
		summary.RescheduleCount++
	}
	if projection.call.Transferred {
		summary.TransferCount++
	}
	summary.ToolCallCount += projection.call.ToolCallCount
	summary.ToolErrorCount += projection.call.ToolErrorCount
	summary.latencySamples.stt = append(
		summary.latencySamples.stt,
		projection.latencySamples.stt...,
	)
	summary.latencySamples.ttft = append(
		summary.latencySamples.ttft,
		projection.latencySamples.ttft...,
	)
	summary.latencySamples.ttsTtfb = append(
		summary.latencySamples.ttsTtfb,
		projection.latencySamples.ttsTtfb...,
	)
	summary.latencySamples.total = append(
		summary.latencySamples.total,
		projection.latencySamples.total...,
	)
}

func finalizeAnalyticsSummary(summary *AnalyticsSummary) {
	if summary.TotalCalls > 0 {
		summary.TransferRate = float64(summary.TransferCount) / float64(summary.TotalCalls)
	}
	if summary.ToolCallCount > 0 {
		summary.ToolFailureRate = float64(summary.ToolErrorCount) / float64(summary.ToolCallCount)
	}
	summary.P50SttMs = medianMilliseconds(summary.latencySamples.stt)
	summary.P90SttMs = percentileMilliseconds(summary.latencySamples.stt, 90)
	summary.P99SttMs = percentileMilliseconds(summary.latencySamples.stt, 99)
	summary.P50TtftMs = medianMilliseconds(summary.latencySamples.ttft)
	summary.P90TtftMs = percentileMilliseconds(summary.latencySamples.ttft, 90)
	summary.P99TtftMs = percentileMilliseconds(summary.latencySamples.ttft, 99)
	summary.P50TtsTtfbMs = medianMilliseconds(summary.latencySamples.ttsTtfb)
	summary.P90TtsTtfbMs = percentileMilliseconds(summary.latencySamples.ttsTtfb, 90)
	summary.P99TtsTtfbMs = percentileMilliseconds(summary.latencySamples.ttsTtfb, 99)
	summary.P50TotalLatencyMs = medianMilliseconds(summary.latencySamples.total)
	summary.P90TotalLatencyMs = percentileMilliseconds(summary.latencySamples.total, 90)
	summary.P99TotalLatencyMs = percentileMilliseconds(summary.latencySamples.total, 99)
	summary.latencySamples = latencyValueSet{}
}

func queryAnalyticsCalls(
	ctx context.Context,
	tx pgx.Tx,
	command QueryAnalyticsCommand,
	locationIDs []string,
	from time.Time,
	to time.Time,
	cursor *analyticsCursor,
) ([]AnalyticsCall, bool, error) {
	var cursorStartedAt any
	var cursorID any
	if cursor != nil {
		cursorStartedAt = cursor.StartedAt
		cursorID = cursor.ID
	}
	rows, err := tx.Query(ctx, `
		SELECT
			interaction.id::text,
			interaction.location_id::text,
			location.name,
			interaction.source_call_id,
			interaction.phone,
			interaction.started_at,
			interaction.ended_at,
			interaction.status,
			COALESCE(interaction.transcript, '{}'::jsonb),
			COALESCE(interaction.closeout_payload, '{}'::jsonb),
			interaction.transcript IS NOT NULL
		FROM ai_interactions interaction
		JOIN access_locations location
			ON location.practice_id = interaction.practice_id
			AND location.id = interaction.location_id
		WHERE interaction.practice_id = $1
			AND interaction.location_id::text = ANY($2::text[])
			AND interaction.started_at >= $3
			AND interaction.started_at <= $4
			AND (
				$5::timestamptz IS NULL OR
				(interaction.started_at, interaction.id) < ($5, $6::uuid)
			)
		ORDER BY interaction.started_at DESC, interaction.id DESC
		LIMIT $7
	`, command.PracticeID, locationIDs, from, to, cursorStartedAt, cursorID, command.Limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("query operator AI analytics page: %w", err)
	}
	defer rows.Close()
	projections := make([]analyticsProjection, 0, command.Limit+1)
	for rows.Next() {
		var projection analyticsProjection
		if err := rows.Scan(
			&projection.call.ID,
			&projection.call.LocationID,
			&projection.call.LocationName,
			&projection.call.SourceCallID,
			&projection.call.Phone,
			&projection.call.StartedAt,
			&projection.call.EndedAt,
			&projection.call.Status,
			&projection.transcript,
			&projection.closeoutPayload,
			&projection.call.TranscriptAvailable,
		); err != nil {
			return nil, false, fmt.Errorf("scan operator AI analytics page: %w", err)
		}
		projectAnalyticsCall(&projection, to)
		projections = append(projections, projection)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("iterate operator AI analytics page: %w", err)
	}
	hasMore := len(projections) > command.Limit
	if hasMore {
		projections = projections[:command.Limit]
	}
	calls := make([]AnalyticsCall, 0, len(projections))
	for _, projection := range projections {
		calls = append(calls, projection.call)
	}
	return calls, hasMore, nil
}

func (m *Module) ReadOperatorAnalytics(
	ctx context.Context,
	identity access.Identity,
	interactionID string,
) (OperatorAnalyticsDetail, error) {
	if m.database == nil || m.access == nil || !validUUID(strings.TrimSpace(interactionID)) {
		return OperatorAnalyticsDetail{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return OperatorAnalyticsDetail{}, fmt.Errorf("begin operator AI analytics detail: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	stored, err := scanInteraction(tx.QueryRow(ctx, interactionSelect+`
		WHERE interaction.id = $1
	`, interactionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return OperatorAnalyticsDetail{}, ErrDenied
	}
	if err != nil {
		return OperatorAnalyticsDetail{}, fmt.Errorf("read operator AI analytics detail: %w", err)
	}
	authorization, err := m.access.LockReadAuthorization(
		ctx,
		tx,
		identity,
		stored.PracticeID,
		stored.LocationID,
	)
	if err != nil {
		if errors.Is(err, access.ErrDenied) {
			return OperatorAnalyticsDetail{}, ErrDenied
		}
		return OperatorAnalyticsDetail{}, fmt.Errorf("authorize operator AI analytics detail: %w", err)
	}
	if !authorization.PlatformOperator {
		return OperatorAnalyticsDetail{}, ErrDenied
	}
	if err := tx.Commit(ctx); err != nil {
		return OperatorAnalyticsDetail{}, fmt.Errorf("commit operator AI analytics detail: %w", err)
	}

	timeline, samples := normalizeTimeline(
		stored.Transcript,
		stored.CloseoutPayload,
		stored.StartedAt,
	)
	return OperatorAnalyticsDetail{
		Interaction:       stored,
		P50SttMs:          medianMilliseconds(samples.stt),
		P50TtftMs:         medianMilliseconds(samples.ttft),
		P50TtsTtfbMs:      medianMilliseconds(samples.ttsTtfb),
		P50TotalLatencyMs: medianMilliseconds(samples.total),
		Timeline:          timeline,
		ToolExecutions: normalizeToolExecutions(
			stored.Transcript,
			stored.CloseoutPayload,
			stored.StartedAt,
		),
	}, nil
}

func normalizeAnalyticsCommand(command *QueryAnalyticsCommand) {
	command.PracticeID = strings.TrimSpace(command.PracticeID)
	command.LocationID = strings.TrimSpace(command.LocationID)
	command.Cursor = strings.TrimSpace(command.Cursor)
	if command.Limit == 0 {
		command.Limit = 50
	}
}

func analyticsRangeDuration(value AnalyticsRange) (time.Duration, bool) {
	switch value {
	case AnalyticsRange24Hours:
		return 24 * time.Hour, true
	case AnalyticsRange7Days:
		return 7 * 24 * time.Hour, true
	case AnalyticsRange30Days:
		return 30 * 24 * time.Hour, true
	default:
		return 0, false
	}
}

func validUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func authorizedLocationIDs(authorization access.Authorization, locationID string) []string {
	if locationID != "" {
		return []string{locationID}
	}
	result := make([]string, 0, len(authorization.Locations))
	for _, location := range authorization.Locations {
		result = append(result, location.ID)
	}
	return result
}

func projectAnalyticsCall(projection *analyticsProjection, now time.Time) {
	endedAt := now
	if projection.call.EndedAt != nil {
		endedAt = *projection.call.EndedAt
	}
	seconds := int(math.Round(endedAt.Sub(projection.call.StartedAt).Seconds()))
	projection.call.DurationSeconds = max(seconds, 0)
	projectAnalyticsEvidence(projection)
}

func projectAnalyticsEvidence(projection *analyticsProjection) {
	closeout := decodeRecord(projection.closeoutPayload)
	turnMetrics, _ := json.Marshal(arrayValue(closeout["turnMetrics"]))
	samples := analyticsLatencySamples(projection.transcript, turnMetrics)
	projection.call.P50SttMs = medianMilliseconds(samples.stt)
	projection.call.P50TtftMs = medianMilliseconds(samples.ttft)
	projection.call.P50TtsTtfbMs = medianMilliseconds(samples.ttsTtfb)
	projection.call.P50TotalLatencyMs = medianMilliseconds(samples.total)
	projection.latencySamples = samples
	executions := normalizeToolExecutions(
		projection.transcript,
		projection.closeoutPayload,
		projection.call.StartedAt,
	)
	projection.call.ToolCallCount = len(executions)
	projection.call.ToolActions = []string{}
	seenActions := map[string]struct{}{}
	for _, execution := range executions {
		if execution.Status == "ERROR" {
			projection.call.ToolErrorCount++
		}
		if _, seen := seenActions[execution.Name]; execution.Name != "" && !seen {
			seenActions[execution.Name] = struct{}{}
			projection.call.ToolActions = append(projection.call.ToolActions, execution.Name)
		}
	}
	projection.call.Transferred = projection.call.Status == CallEscalated
}

func encodeAnalyticsCursor(command QueryAnalyticsCommand, call AnalyticsCall) (string, error) {
	encoded, err := json.Marshal(analyticsCursor{
		Range:      command.Range,
		PracticeID: command.PracticeID,
		LocationID: command.LocationID,
		StartedAt:  call.StartedAt,
		ID:         call.ID,
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeAnalyticsCursor(command QueryAnalyticsCommand) (*analyticsCursor, error) {
	if command.Cursor == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(command.Cursor)
	if err != nil {
		return nil, err
	}
	var cursor analyticsCursor
	if err := json.Unmarshal(decoded, &cursor); err != nil {
		return nil, err
	}
	if cursor.Range != command.Range || cursor.PracticeID != command.PracticeID ||
		cursor.LocationID != command.LocationID || cursor.StartedAt.IsZero() ||
		!validUUID(cursor.ID) {
		return nil, ErrInvalidInput
	}
	return &cursor, nil
}

type latencyValueSet struct {
	stt     []float64
	ttft    []float64
	ttsTtfb []float64
	total   []float64
}

func latencySamples(raw json.RawMessage) latencyValueSet {
	var entries []any
	decodeJSON(raw, &entries)
	result := latencyValueSet{}
	for _, value := range entries {
		entry := recordValue(value)
		metrics := recordValue(entry["metrics"])
		appendLatencyValues(&result, metrics)
	}
	return result
}

func analyticsLatencySamples(
	transcript json.RawMessage,
	turnMetricsRaw json.RawMessage,
) latencyValueSet {
	turnSamples := latencySamples(turnMetricsRaw)
	var turnMetricEntries []any
	decodeJSON(turnMetricsRaw, &turnMetricEntries)
	turnMetrics := turnMetricsByEntries(turnMetricEntries)
	transcriptSamples := latencyValueSet{}
	for _, value := range transcriptItems(decodeRecord(transcript)) {
		record := recordValue(value)
		metrics := recordValue(record["metrics"])
		if itemID := firstRecordString(record, "id"); itemID != "" && len(turnMetrics[itemID]) > 0 {
			metrics = mergeRecords(metrics, turnMetrics[itemID])
		}
		appendLatencyValues(&transcriptSamples, metrics)
	}
	return latencySamplesWithFallback(turnSamples, transcriptSamples)
}

func latencySamplesWithFallback(
	primary latencyValueSet,
	fallback latencyValueSet,
) latencyValueSet {
	if len(primary.stt) == 0 {
		primary.stt = fallback.stt
	}
	if len(primary.ttft) == 0 {
		primary.ttft = fallback.ttft
	}
	if len(primary.ttsTtfb) == 0 {
		primary.ttsTtfb = fallback.ttsTtfb
	}
	if len(primary.total) == 0 {
		primary.total = fallback.total
	}
	return primary
}

func appendLatencyValues(result *latencyValueSet, metrics map[string]any) {
	if value, ok := latencyMilliseconds(metrics,
		[]string{"sttMs", "transcriptionDelayMs", "transcription_delay_ms"},
		[]string{"transcriptionDelay", "transcription_delay"}); ok {
		result.stt = append(result.stt, value)
	}
	if value, ok := latencyMilliseconds(metrics,
		[]string{"ttftMs", "llmNodeTtftMs", "llm_node_ttft_ms"},
		[]string{"llmNodeTtft", "llm_node_ttft"}); ok {
		result.ttft = append(result.ttft, value)
	}
	if value, ok := latencyMilliseconds(metrics,
		[]string{"ttsTtfbMs", "ttsNodeTtfbMs", "tts_node_ttfb_ms"},
		[]string{"ttsNodeTtfb", "tts_node_ttfb"}); ok {
		result.ttsTtfb = append(result.ttsTtfb, value)
	}
	if value, ok := latencyMilliseconds(metrics,
		[]string{"totalLatencyMs", "e2eLatencyMs", "e2e_latency_ms"},
		[]string{"e2eLatency", "e2e_latency"}); ok {
		result.total = append(result.total, value)
	}
}

func latencyMilliseconds(
	metrics map[string]any,
	millisecondKeys []string,
	secondKeys []string,
) (float64, bool) {
	for _, key := range millisecondKeys {
		if value, ok := positiveNumber(metrics[key]); ok {
			return value, true
		}
	}
	for _, key := range secondKeys {
		if value, ok := positiveNumber(metrics[key]); ok {
			return value * 1000, true
		}
	}
	return 0, false
}

func positiveNumber(value any) (float64, bool) {
	var number float64
	switch typed := value.(type) {
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		number = parsed
	case float64:
		number = typed
	case float32:
		number = float64(typed)
	case int:
		number = float64(typed)
	case int64:
		number = float64(typed)
	default:
		return 0, false
	}
	return number, number > 0 && !math.IsInf(number, 0) && !math.IsNaN(number)
}

func medianMilliseconds(values []float64) *int {
	if len(values) == 0 {
		return nil
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	middle := len(ordered) / 2
	value := ordered[middle]
	if len(ordered)%2 == 0 {
		value = (ordered[middle-1] + value) / 2
	}
	result := int(math.Round(value))
	return &result
}

func percentileMilliseconds(values []float64, percentile float64) *int {
	if len(values) == 0 || percentile < 0 || percentile > 100 {
		return nil
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	index := int(math.Ceil(percentile/100*float64(len(ordered)))) - 1
	index = max(0, min(index, len(ordered)-1))
	result := int(math.Round(ordered[index]))
	return &result
}

func normalizeToolExecutions(
	transcript json.RawMessage,
	closeoutPayload json.RawMessage,
	fallback time.Time,
) []ToolExecution {
	closeout := decodeRecord(closeoutPayload)
	if _, current := closeout["domainOutcomes"]; current {
		return nativeToolExecutions(transcript, closeout, fallback)
	}

	raw, _ := json.Marshal(arrayValue(closeout["toolExecutions"]))
	return toolExecutionsFromRaw(raw, fallback)
}

func nativeToolExecutions(
	transcript json.RawMessage,
	closeout map[string]any,
	fallback time.Time,
) []ToolExecution {
	items := transcriptItems(decodeRecord(transcript))
	outputsByCallID := map[string]map[string]any{}
	receiptsByCallID := domainOutcomeReceiptsByCallID(closeout)
	for _, value := range items {
		record := recordValue(value)
		if strings.EqualFold(firstRecordString(record, "type"), "function_call_output") {
			if callID := firstRecordString(record, "call_id", "callId"); callID != "" {
				outputsByCallID[callID] = record
			}
		}
	}

	result := make([]ToolExecution, 0)
	for index, value := range items {
		call := recordValue(value)
		if !strings.EqualFold(firstRecordString(call, "type"), "function_call") {
			continue
		}
		callID := firstRecordString(call, "call_id", "callId")
		name := firstRecordString(call, "name")
		if callID == "" || name == "" {
			continue
		}
		output := outputsByCallID[callID]
		receipt := receiptsByCallID[callID]
		status := nativeToolExecutionStatus(output)
		domainOutcome, domainStatus, taskID := domainReceiptProjection(receipt)
		occurredAt := timestampValue(
			firstRecordValue(call, "created_at", "createdAt"),
			timestampValue(
				firstRecordValue(output, "created_at", "createdAt"),
				fallback.Add(time.Duration(index)*time.Nanosecond),
			),
		)
		result = append(result, ToolExecution{
			CallID:        callID,
			Name:          name,
			OccurredAt:    occurredAt,
			Status:        status,
			DomainOutcome: domainOutcome,
			DomainStatus:  domainStatus,
			TaskID:        taskID,
		})
	}
	sort.SliceStable(result, func(left, right int) bool {
		return result[left].OccurredAt.Before(result[right].OccurredAt)
	})
	return result
}

func nativeToolExecutionStatus(output map[string]any) string {
	if output == nil {
		return "INCOMPLETE"
	}
	value, present := output["is_error"]
	if !present {
		value, present = output["isError"]
	}
	isError, valid := value.(bool)
	if !present || !valid {
		return "INCOMPLETE"
	}
	if isError {
		return "ERROR"
	}
	return "SUCCESS"
}

func domainReceiptProjection(receipt map[string]any) (string, string, string) {
	outcome := firstRecordString(receipt, "outcome")
	status := normalizedDomainStatus(firstRecordString(receipt, "status"))
	if outcome == "" || status == "" {
		return "", "", ""
	}
	if (outcome == "staff_task_created" || outcome == "staff_task_duplicate") && status == "success" {
		taskID := firstRecordString(recordValue(receipt["evidence"]), "taskId", "task_id")
		if taskID == "" {
			return "", "", ""
		}
		return outcome, status, taskID
	}
	return outcome, status, ""
}

func normalizedDomainStatus(status string) string {
	switch strings.ToLower(status) {
	case "success", "blocked", "partial", "ambiguous", "failed":
		return strings.ToLower(status)
	default:
		return ""
	}
}

func domainOutcomeReceiptsByCallID(closeout map[string]any) map[string]map[string]any {
	result := map[string]map[string]any{}
	for _, value := range arrayValue(closeout["domainOutcomes"]) {
		receipt := recordValue(value)
		if callID := firstRecordString(receipt, "callId", "call_id"); callID != "" {
			result[callID] = receipt
		}
	}
	return result
}

func toolExecutionsFromRaw(raw json.RawMessage, fallback time.Time) []ToolExecution {
	var values []any
	decodeJSON(raw, &values)
	result := make([]ToolExecution, 0, len(values))
	for index, value := range values {
		record := recordValue(value)
		name := firstRecordString(record, "toolName", "tool_name", "name")
		callID := firstRecordString(record, "callId", "call_id", "id")
		if name == "" || callID == "" {
			continue
		}
		status := strings.ToUpper(firstRecordString(record, "status"))
		if status != "SUCCESS" && status != "ERROR" {
			continue
		}
		result = append(result, ToolExecution{
			CallID:      callID,
			Name:        name,
			OccurredAt:  timestampValue(firstRecordValue(record, "createdAt", "created_at"), fallback.Add(time.Duration(index)*time.Nanosecond)),
			Status:      status,
			OutputClass: firstRecordString(record, "outputClass", "output_class"),
		})
	}
	return result
}

func normalizeTimeline(
	transcript json.RawMessage,
	closeoutPayload json.RawMessage,
	fallback time.Time,
) ([]TimelineItem, latencyValueSet) {
	report := decodeRecord(transcript)
	items := transcriptItems(report)
	closeout := decodeRecord(closeoutPayload)
	turnMetrics := turnMetricsByItem(closeout)
	turnRaw, _ := json.Marshal(arrayValue(closeout["turnMetrics"]))
	turnSamples := latencySamples(turnRaw)
	result := make([]TimelineItem, 0, len(items))
	transcriptSamples := latencyValueSet{}
	for index, value := range items {
		record := recordValue(value)
		item, ok := normalizeTimelineItem(record, fallback.Add(time.Duration(index)*time.Nanosecond))
		if !ok {
			continue
		}
		metrics := recordValue(record["metrics"])
		if itemID := firstRecordString(record, "id"); itemID != "" && len(turnMetrics[itemID]) > 0 {
			metrics = mergeRecords(metrics, turnMetrics[itemID])
		}
		item.SttMs, item.TtftMs, item.TtsTtfbMs, item.TotalLatencyMs = latencyPointers(metrics)
		appendLatencyValues(&transcriptSamples, metrics)
		result = append(result, item)
	}
	samples := latencySamplesWithFallback(turnSamples, transcriptSamples)
	sort.SliceStable(result, func(left, right int) bool {
		return result[left].OccurredAt.Before(result[right].OccurredAt)
	})
	return result, samples
}

func transcriptItems(report map[string]any) []any {
	for _, value := range []any{
		recordValue(report["chat_history"])["items"],
		recordValue(report["chatHistory"])["items"],
		report["items"],
	} {
		if items := arrayValue(value); items != nil {
			return items
		}
	}
	return []any{}
}

func normalizeTimelineItem(record map[string]any, fallback time.Time) (TimelineItem, bool) {
	typeName := strings.ToLower(firstRecordString(record, "type"))
	role := strings.ToLower(firstRecordString(record, "role"))
	item := TimelineItem{OccurredAt: timestampValue(
		firstRecordValue(record, "created_at", "createdAt", "occurredAt"),
		fallback,
	)}
	switch typeName {
	case "function_call":
		item.Kind = TimelineToolCall
		item.Name = firstRecordString(record, "name")
		item.CallID = firstRecordString(record, "call_id", "callId")
		item.Payload = normalizedPayload(firstRecordValue(record, "arguments", "args"))
		return item, item.Name != "" && item.CallID != ""
	case "function_call_output":
		item.Kind = TimelineToolResult
		item.Name = firstRecordString(record, "name")
		item.CallID = firstRecordString(record, "call_id", "callId")
		output := firstRecordValue(record, "output")
		item.Payload = normalizedPayload(output)
		if boolValue(firstRecordValue(record, "is_error", "isError")) {
			item.Error = errorText(output)
		}
		return item, item.CallID != ""
	case "", "message":
		switch role {
		case "user":
			item.Kind = TimelineCallerMessage
		case "assistant":
			item.Kind = TimelineAgentMessage
		default:
			return TimelineItem{}, false
		}
		item.Text = messageText(record)
		return item, item.Text != ""
	default:
		return TimelineItem{}, false
	}
}

func turnMetricsByItem(closeout map[string]any) map[string]map[string]any {
	return turnMetricsByEntries(arrayValue(closeout["turnMetrics"]))
}

func turnMetricsByEntries(entries []any) map[string]map[string]any {
	result := map[string]map[string]any{}
	for _, value := range entries {
		record := recordValue(value)
		if itemID := firstRecordString(record, "itemId", "item_id"); itemID != "" {
			result[itemID] = recordValue(record["metrics"])
		}
	}
	return result
}

func latencyPointers(metrics map[string]any) (*int, *int, *int, *int) {
	values := latencyValueSet{}
	appendLatencyValues(&values, metrics)
	return medianMilliseconds(values.stt), medianMilliseconds(values.ttft),
		medianMilliseconds(values.ttsTtfb), medianMilliseconds(values.total)
}

func messageText(record map[string]any) string {
	if text := firstRecordString(record, "text"); text != "" {
		return text
	}
	parts := []string{}
	for _, value := range arrayValue(record["content"]) {
		switch typed := value.(type) {
		case string:
			if text := strings.TrimSpace(typed); text != "" {
				parts = append(parts, text)
			}
		case map[string]any:
			if text := firstRecordString(typed, "text", "transcript"); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func normalizedPayload(value any) map[string]any {
	if text, ok := value.(string); ok {
		var decoded any
		if decodeJSON([]byte(text), &decoded) {
			value = decoded
		}
	}
	if record, ok := value.(map[string]any); ok {
		return record
	}
	if value == nil {
		return nil
	}
	return map[string]any{"value": value}
}

func errorText(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "tool execution failed"
	}
	return string(encoded)
}

func timestampValue(value any, fallback time.Time) time.Time {
	if text, ok := value.(string); ok {
		if parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(text)); err == nil {
			return parsed.UTC()
		}
	}
	if number, ok := positiveNumber(value); ok {
		if number < 100_000_000_000 {
			number *= 1000
		}
		seconds, fraction := math.Modf(number / 1000)
		return time.Unix(int64(seconds), int64(fraction*float64(time.Second))).UTC()
	}
	return fallback.UTC()
}

func mergeRecords(primary map[string]any, fallback map[string]any) map[string]any {
	result := make(map[string]any, len(primary)+len(fallback))
	for key, value := range fallback {
		result[key] = value
	}
	for key, value := range primary {
		result[key] = value
	}
	return result
}

func firstRecordString(record map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := record[key]; ok {
			if text := anyString(value); text != "" {
				return text
			}
		}
	}
	return ""
}

func firstRecordValue(record map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := record[key]; ok {
			return value
		}
	}
	return nil
}

func anyString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func arrayValue(value any) []any {
	result, _ := value.([]any)
	return result
}

func boolValue(value any) bool {
	result, _ := value.(bool)
	return result
}

func decodeJSON(raw []byte, target any) bool {
	if len(raw) == 0 {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(target) == nil
}
