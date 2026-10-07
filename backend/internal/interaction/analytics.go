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
	ManualTag  string
	Identity   access.Identity
	PracticeID string
	LocationID string
	Range      AnalyticsRange
	Cursor     string
	Limit      int
}

type AnalyticsDay struct {
	Date          string
	TotalCalls    int
	CallMinutes   float64
	TransferCount int
	TransferRate  *float64
}

func analyticsDays(from, through time.Time) []AnalyticsDay {
	days := []AnalyticsDay{}
	for day := from.UTC().Truncate(24 * time.Hour); !day.After(through); day = day.AddDate(0, 0, 1) {
		days = append(days, AnalyticsDay{Date: day.Format(time.DateOnly)})
	}
	return days
}

type AnalyticsSummary struct {
	Daily             []AnalyticsDay
	Diagnostics       AnalyticsDiagnostics
	Quality           AnalyticsQuality
	Versions          AnalyticsVersions
	diagnostics       *diagnosticsAccumulator
	quality           *qualityAccumulator
	versions          *versionAccumulator
	TotalCalls        int
	TotalCallMinutes  float64
	BookingCount      int
	CancellationCount int
	RescheduleCount   int
	TransferCount     int
	TransferRate      float64
	ToolCallCount     int
	ToolErrorCount    int
	ToolFailureRate   float64
}

type AnalyticsCall struct {
	ReviewReasons       []string
	ManualTags          []string
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
	AvailableTags []string
	PendingIssues []OperatorCallIssue
	Summary       *AnalyticsSummary
	Calls         []AnalyticsCall
	NextCursor    string
}

type TimelineKind string

const (
	TimelineCallerMessage TimelineKind = "CALLER_MESSAGE"
	TimelineAgentMessage  TimelineKind = "AGENT_MESSAGE"
	TimelineToolCall      TimelineKind = "TOOL_CALL"
	TimelineToolResult    TimelineKind = "TOOL_RESULT"
)

type TimelineItem struct {
	ItemID         string
	DurationMs     *int
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
	MiddlewareRequests []MiddlewareRequestDiagnostic
	DurationMs         *int
	CallID             string
	Name               string
	OccurredAt         time.Time
	Status             string
	OutputClass        string
	DomainOutcome      string
	DomainStatus       string
	TaskID             string
}

type OperatorAnalyticsDetail struct {
	Interaction       Interaction
	Issue             *OperatorCallIssue
	P50SttMs          *int
	P50TtftMs         *int
	P50TtsTtfbMs      *int
	P50TotalLatencyMs *int
	Timeline          []TimelineItem
	ToolExecutions    []ToolExecution
}

type analyticsCursor struct {
	ManualTag  string         `json:"manualTag,omitempty"`
	Through    time.Time      `json:"through"`
	Range      AnalyticsRange `json:"range"`
	PracticeID string         `json:"practiceId"`
	LocationID string         `json:"locationId,omitempty"`
	StartedAt  time.Time      `json:"startedAt"`
	ID         string         `json:"id"`
}

type analyticsProjection struct {
	executions         []ToolExecution
	call               AnalyticsCall
	appointmentOutcome AppointmentOutcome
	transcript         json.RawMessage
	closeoutPayload    json.RawMessage
	quality            qualitySample
	versions           callVersions
}

func (m *Module) QueryAnalytics(
	ctx context.Context,
	command QueryAnalyticsCommand,
) (AnalyticsPage, error) {
	normalizeAnalyticsCommand(&command)
	duration, ok := analyticsRangeDuration(command.Range)
	if !ok || command.Limit < 1 || command.Limit > 100 {
		return AnalyticsPage{}, ErrInvalidInput
	}
	to := m.now().UTC().Truncate(time.Microsecond)
	var cursor *analyticsCursor
	if command.Cursor != "" {
		cursor = &analyticsCursor{}
		if !decodeCursor(command, cursor, cursor, to) {
			return AnalyticsPage{}, ErrInvalidInput
		}
		to = cursor.Through
	}
	from := to.Add(-duration)
	tx, locationIDs, err := m.beginAnalyticsScope(ctx, command.Identity, command.PracticeID, command.LocationID, audienceOperator)
	if err != nil {
		return AnalyticsPage{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var summary *AnalyticsSummary
	var pendingIssues []OperatorCallIssue
	if cursor == nil {
		value, err := queryAnalyticsSummary(ctx, tx, command, locationIDs, from, to)
		if err != nil {
			return AnalyticsPage{}, err
		}
		summary = &value
		pendingIssues, err = pendingCallIssues(ctx, tx, command.PracticeID, locationIDs)
		if err != nil {
			return AnalyticsPage{}, fmt.Errorf("query pending call issues: %w", err)
		}
	}
	calls, next, err := queryAnalyticsCalls(
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
	availableTags, err := availableManualTags(ctx, tx, command.PracticeID)
	if err != nil {
		return AnalyticsPage{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AnalyticsPage{}, fmt.Errorf("commit operator AI analytics query: %w", err)
	}

	page := AnalyticsPage{Summary: summary, Calls: calls, AvailableTags: availableTags, PendingIssues: pendingIssues}
	if next != nil {
		page.NextCursor, err = encodeCursor(newAnalyticsCursor(command, next.StartedAt, next.ID, to))
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
	versions := newVersionAccumulator()
	if err := queryVersionBaseline(ctx, tx, command.PracticeID, locationIDs, from, versions); err != nil {
		return AnalyticsSummary{}, err
	}
	rows, err := tx.Query(ctx, `
		SELECT
			interaction.id::text,
			interaction.started_at,
			interaction.ended_at,
			interaction.status,
			interaction.appointment_outcome,
			interaction.analytics_evidence -> 'transcript',
			interaction.analytics_evidence -> 'closeout',
			interaction.closeout_payload -> 'evaluation',
			interaction.closeout_payload -> 'contextFootprint',
			interaction.cost_usage_evidence,
			CASE WHEN issue.interaction_id IS NULL THEN '' ELSE COALESCE(issue.review_outcome, 'PENDING') END,
			interaction.location_id::text,
			COALESCE(interaction.version_agent, ''),
			COALESCE(interaction.version_prompts, ''),
			COALESCE(interaction.version_tools, ''),
			COALESCE(interaction.version_knowledge, ''),
			COALESCE(interaction.version_judges, ''),
			COALESCE(interaction.version_evaluator, '')
		FROM ai_interactions interaction
		LEFT JOIN ai_interaction_issues issue ON issue.interaction_id = interaction.id
		WHERE interaction.practice_id = $1
			AND interaction.location_id = ANY($2::uuid[])
			AND interaction.started_at >= $3
			AND interaction.started_at <= $4
 AND ($5 = '' OR EXISTS (SELECT 1 FROM ai_interaction_manual_tags tag WHERE tag.interaction_id=interaction.id AND tag.practice_id=$1 AND tag.tag_key=lower($5)))
		ORDER BY interaction.started_at, interaction.id
	`, command.PracticeID, locationIDs, from, to, command.ManualTag)
	if err != nil {
		return AnalyticsSummary{}, fmt.Errorf("query operator AI analytics summary: %w", err)
	}
	defer rows.Close()
	summary := AnalyticsSummary{Daily: analyticsDays(from, to), diagnostics: newDiagnosticsAccumulator()}
	summary.quality = newQualityAccumulator(summary.Daily)
	summary.versions = versions
	for rows.Next() {
		if summary.TotalCalls == analyticsRowLimit {
			return AnalyticsSummary{}, errors.New("operator AI analytics exceeds bounded reporting window")
		}
		var projection analyticsProjection
		var evaluation, footprint, usage json.RawMessage
		values := make([]string, len(versionDimensions))
		if err := rows.Scan(
			&projection.call.ID,
			&projection.call.StartedAt,
			&projection.call.EndedAt,
			&projection.call.Status,
			&projection.appointmentOutcome,
			&projection.transcript,
			&projection.closeoutPayload,
			&evaluation,
			&footprint,
			&usage,
			&projection.quality.staffFlag,
			&projection.versions.locationID,
			&values[0], &values[1], &values[2], &values[3], &values[4], &values[5],
		); err != nil {
			return AnalyticsSummary{}, fmt.Errorf("scan operator AI analytics summary: %w", err)
		}
		if len(projection.transcript) == 0 || len(projection.closeoutPayload) == 0 {
			return AnalyticsSummary{}, errors.New("operator AI analytics evidence backfill is incomplete")
		}
		projectAnalyticsEvidence(&projection)
		projection.quality.startedAt = projection.call.StartedAt
		projection.quality.evaluation = readEvaluation(evaluation)
		projection.quality.footprint = contextFootprint(footprint)
		projection.quality.usage = callTokenUsage(usage)
		projection.versions.values = map[string]string{}
		for i, dimension := range versionDimensions {
			projection.versions.values[dimension] = values[i]
		}
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
	var minutes float64
	if ended := projection.call.EndedAt; ended != nil && ended.After(projection.call.StartedAt) {
		minutes = ended.Sub(projection.call.StartedAt).Minutes()
	}
	summary.TotalCallMinutes += minutes
	date := projection.call.StartedAt.UTC().Format(time.DateOnly)
	for i := range summary.Daily {
		day := &summary.Daily[i]
		if day.Date == date {
			day.TotalCalls++
			day.CallMinutes += minutes
			if projection.call.Transferred {
				day.TransferCount++
			}
			break
		}
	}
	if summary.diagnostics == nil {
		summary.diagnostics = newDiagnosticsAccumulator()
	}
	summary.diagnostics.add(projection)
	if summary.quality == nil {
		summary.quality = newQualityAccumulator(summary.Daily)
	}
	summary.quality.add(projection.quality)
	if summary.versions == nil {
		summary.versions = newVersionAccumulator()
	}
	summary.versions.add(projection.call.StartedAt, projection.versions)
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
}

func finalizeAnalyticsSummary(summary *AnalyticsSummary) {
	for i := range summary.Daily {
		day := &summary.Daily[i]
		if day.TotalCalls > 0 {
			rate := float64(day.TransferCount) / float64(day.TotalCalls)
			day.TransferRate = &rate
		}
	}
	if summary.diagnostics == nil {
		summary.diagnostics = newDiagnosticsAccumulator()
	}
	summary.Diagnostics = summary.diagnostics.finish()
	summary.diagnostics = nil
	if summary.quality == nil {
		summary.quality = newQualityAccumulator(summary.Daily)
	}
	summary.Quality = summary.quality.finish()
	summary.quality = nil
	if summary.versions == nil {
		summary.versions = newVersionAccumulator()
	}
	summary.Versions = summary.versions.finish()
	summary.versions = nil
	if summary.TotalCalls > 0 {
		summary.TransferRate = float64(summary.TransferCount) / float64(summary.TotalCalls)
	}
	if summary.ToolCallCount > 0 {
		summary.ToolFailureRate = float64(summary.ToolErrorCount) / float64(summary.ToolCallCount)
	}
}

func queryAnalyticsCalls(
	ctx context.Context,
	tx pgx.Tx,
	command QueryAnalyticsCommand,
	locationIDs []string,
	from time.Time,
	to time.Time,
	cursor *analyticsCursor,
) ([]AnalyticsCall, *AnalyticsCall, error) {
	var cursorStartedAt any
	var cursorID any
	if cursor != nil {
		cursorStartedAt = cursor.StartedAt
		cursorID = cursor.ID
	}
	scanLimit := command.Limit + 1
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
			interaction.analytics_evidence -> 'transcript',
			interaction.analytics_evidence -> 'closeout',
			interaction.transcript IS NOT NULL,
 interaction.closeout_payload -> 'evaluation',
 ARRAY(SELECT tag.name FROM ai_manual_tags tag JOIN ai_interaction_manual_tags applied ON applied.practice_id=tag.practice_id AND applied.tag_key=tag.key WHERE applied.interaction_id=interaction.id ORDER BY tag.key)
		FROM ai_interactions interaction
		JOIN access_locations location
			ON location.practice_id = interaction.practice_id
			AND location.id = interaction.location_id
		WHERE interaction.practice_id = $1
			AND interaction.location_id = ANY($2::uuid[])
			AND interaction.started_at >= $3
			AND interaction.started_at <= $4
 AND ($8 = '' OR EXISTS (SELECT 1 FROM ai_interaction_manual_tags tag WHERE tag.interaction_id=interaction.id AND tag.practice_id=$1 AND tag.tag_key=lower($8)))
			AND (
				$5::timestamptz IS NULL OR
				(interaction.started_at, interaction.id) < ($5, $6::uuid)
			)
		ORDER BY interaction.started_at DESC, interaction.id DESC
		LIMIT $7
	`, command.PracticeID, locationIDs, from, to, cursorStartedAt, cursorID, scanLimit, command.ManualTag)
	if err != nil {
		return nil, nil, fmt.Errorf("query operator AI analytics page: %w", err)
	}
	defer rows.Close()
	projections := make([]analyticsProjection, 0, command.Limit+1)
	scanned := 0
	var last AnalyticsCall
	for rows.Next() {
		var projection analyticsProjection
		var evaluation json.RawMessage
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
			&evaluation,
			&projection.call.ManualTags,
		); err != nil {
			return nil, nil, fmt.Errorf("scan operator AI analytics page: %w", err)
		}
		scanned++
		last = projection.call
		projection.call.ReviewReasons = EvaluationReviewReasons(evaluation)
		projectAnalyticsCall(&projection, to)
		projections = append(projections, projection)
		if len(projections) > command.Limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate operator AI analytics page: %w", err)
	}
	var next *AnalyticsCall
	if len(projections) > command.Limit {
		projections = projections[:command.Limit]
		next = &projections[len(projections)-1].call
	} else if scanned == scanLimit {
		next = &last
	}
	calls := make([]AnalyticsCall, 0, len(projections))
	for _, projection := range projections {
		calls = append(calls, projection.call)
	}
	return calls, next, nil
}

func (m *Module) ReadOperatorAnalytics(
	ctx context.Context,
	identity access.Identity,
	interactionID string,
) (OperatorAnalyticsDetail, error) {
	interactionID = strings.TrimSpace(interactionID)
	tx, _, err := m.beginInteractionAccess(ctx, identity, interactionID, false, audienceOperator)
	if err != nil {
		return OperatorAnalyticsDetail{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	stored, err := readInteraction(ctx, tx, interactionID)
	if err != nil {
		return OperatorAnalyticsDetail{}, err
	}
	issue, err := readCallIssue(ctx, tx, interactionID)
	if err != nil {
		return OperatorAnalyticsDetail{}, fmt.Errorf("read operator AI call issue: %w", err)
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
		Issue:             issue,
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
	command.ManualTag = strings.Join(strings.Fields(command.ManualTag), " ")
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
	executions := normalizeToolExecutions(
		projection.transcript,
		projection.closeoutPayload,
		projection.call.StartedAt,
	)
	projection.executions = executions
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

func newAnalyticsCursor(command QueryAnalyticsCommand, startedAt time.Time, id string, through time.Time) analyticsCursor {
	return analyticsCursor{
		Through:    through,
		ManualTag:  command.ManualTag,
		Range:      command.Range,
		PracticeID: command.PracticeID,
		LocationID: command.LocationID,
		StartedAt:  startedAt,
		ID:         id,
	}
}

func encodeCursor(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeCursor(command QueryAnalyticsCommand, target any, cursor *analyticsCursor, now time.Time) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(command.Cursor)
	if err != nil || json.Unmarshal(decoded, target) != nil {
		return false
	}
	duration, _ := analyticsRangeDuration(command.Range)
	return cursor.ManualTag == command.ManualTag &&
		cursor.Range == command.Range && cursor.PracticeID == command.PracticeID && cursor.LocationID == command.LocationID &&
		!cursor.StartedAt.IsZero() && !cursor.Through.IsZero() && !cursor.Through.After(now) &&
		!cursor.StartedAt.After(cursor.Through) && !cursor.StartedAt.Before(cursor.Through.Add(-duration)) &&
		validUUID(cursor.ID)
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
	return sortedMedian(ordered)
}

func sortedMedian(ordered []float64) *int {
	if len(ordered) == 0 {
		return nil
	}
	middle := len(ordered) / 2
	value := ordered[middle]
	if len(ordered)%2 == 0 {
		value = (ordered[middle-1] + value) / 2
	}
	result := int(math.Round(value))
	return &result
}

func sortedPercentile(ordered []float64, percentile float64) *int {
	if len(ordered) == 0 || percentile < 0 || percentile > 100 {
		return nil
	}
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
			DurationMs:         toolDuration(call, output),
			CallID:             callID,
			Name:               name,
			OccurredAt:         occurredAt,
			Status:             status,
			DomainOutcome:      domainOutcome,
			DomainStatus:       domainStatus,
			MiddlewareRequests: middlewareRequestDiagnostics(receipt["middlewareRequests"]),
			TaskID:             taskID,
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
	executionDurations := map[string]*int{}
	for _, execution := range normalizeToolExecutions(transcript, closeoutPayload, fallback) {
		executionDurations[execution.CallID] = execution.DurationMs
	}
	for index := range result {
		result[index].DurationMs = executionDurations[result[index].CallID]
	}
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
	item := TimelineItem{ItemID: firstRecordString(record, "id"), OccurredAt: timestampValue(
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
