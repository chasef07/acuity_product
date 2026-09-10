package humancalling

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// These read endpoints drift independently of the command SDK: active_calls uses
// cursors, while call_events still uses numbered pages. Keep their wire contracts
// here and use the SDK only for authenticated, contextual HTTP requests.
const maxTelnyxObservationPages = 100

func (adapter *TelnyxAdapter) IsCallEnded(ctx context.Context, controlID, legID, sessionID string) (bool, error) {
	if controlID == "" || legID == "" || sessionID == "" {
		return false, ErrInvalidInput
	}
	var response struct {
		Data struct {
			ControlID string `json:"call_control_id"`
			LegID     string `json:"call_leg_id"`
			SessionID string `json:"call_session_id"`
			Active    *bool  `json:"is_alive"`
			EndTime   string `json:"end_time"`
		} `json:"data"`
	}
	if err := adapter.client.Get(ctx, "calls/"+url.PathEscape(controlID), nil, &response); err != nil {
		return false, classifyTelnyxSDKError(err)
	}
	status := response.Data
	if status.ControlID != controlID || status.LegID != legID || status.SessionID != sessionID || status.Active == nil {
		return false, fmt.Errorf("%w: incomplete or contradictory Telnyx Call status", ErrAmbiguousEffect)
	}
	if *status.Active || status.EndTime == "" {
		return false, nil
	}
	if _, err := parseTelnyxTime(status.EndTime); err != nil {
		return false, fmt.Errorf("%w: invalid Telnyx Call end time", ErrAmbiguousEffect)
	}
	return true, nil
}

type telnyxReadPage[T any] struct {
	Data []T `json:"data"`
	Meta struct {
		PageNumber int `json:"page_number"`
		TotalPages int `json:"total_pages"`
		Cursors    *struct {
			After string `json:"after"`
		} `json:"cursors"`
		Next string `json:"next"`
	} `json:"meta"`
}

type telnyxActiveCall struct {
	CallControlID string `json:"call_control_id"`
	CallLegID     string `json:"call_leg_id"`
	CallSessionID string `json:"call_session_id"`
	ClientState   string `json:"client_state"`
}

type telnyxReadEvent struct {
	Name       string          `json:"name"`
	LegID      string          `json:"leg_id"`
	SessionID  string          `json:"application_session_id"`
	OccurredAt string          `json:"occurred_at"`
	Payload    json.RawMessage `json:"payload"`
	// The published endpoint contract still uses these fields. Do not silently
	// accept conflicting identities if a response contains both representations.
	CallLegID      string         `json:"call_leg_id"`
	CallSessionID  string         `json:"call_session_id"`
	EventTimestamp string         `json:"event_timestamp"`
	Metadata       map[string]any `json:"metadata"`
}

func readTelnyxPages[T any](ctx context.Context, adapter *TelnyxAdapter, path string, query url.Values) ([]T, error) {
	var result []T
	seen := map[string]bool{}
	for pageNumber := 1; pageNumber <= maxTelnyxObservationPages; pageNumber++ {
		var page telnyxReadPage[T]
		if err := adapter.client.Get(ctx, path+"?"+query.Encode(), nil, &page); err != nil {
			return nil, classifyTelnyxSDKError(err)
		}
		if page.Data == nil {
			return nil, fmt.Errorf("%w: missing Telnyx observation data", ErrAmbiguousEffect)
		}
		result = append(result, page.Data...)
		if page.Meta.Cursors != nil {
			cursor := page.Meta.Cursors.After
			if cursor == "" {
				if page.Meta.Next != "" {
					return nil, fmt.Errorf("%w: missing Telnyx next cursor", ErrAmbiguousEffect)
				}
				return result, nil
			}
			if seen[cursor] {
				return nil, fmt.Errorf("%w: repeated Telnyx observation cursor", ErrAmbiguousEffect)
			}
			seen[cursor] = true
			// Never follow a provider-supplied URL with credentials. Only the
			// opaque cursor travels to the original trusted endpoint.
			query.Del("page[number]")
			query.Set("page[after]", cursor)
			continue
		}
		if page.Meta.Next != "" {
			return nil, fmt.Errorf("%w: missing Telnyx next cursor", ErrAmbiguousEffect)
		}
		if page.Meta.TotalPages > 0 && (page.Meta.PageNumber != pageNumber || page.Meta.TotalPages < pageNumber) {
			return nil, fmt.Errorf("%w: inconsistent Telnyx observation page", ErrAmbiguousEffect)
		}
		if page.Meta.TotalPages <= 1 || page.Meta.PageNumber == page.Meta.TotalPages {
			return result, nil
		}
		query.Set("page[number]", strconv.Itoa(pageNumber+1))
	}
	return nil, fmt.Errorf("%w: Telnyx observation page limit exceeded", ErrAmbiguousEffect)
}

func (adapter *TelnyxAdapter) ObserveCall(ctx context.Context, connectionID, callControlID, callLegID, clientState string, since time.Time) (ProviderCallObservation, error) {
	if !validTelnyxResourceID(connectionID) || since.IsZero() ||
		(strings.TrimSpace(callLegID) == "" && strings.TrimSpace(clientState) == "") {
		return ProviderCallObservation{}, ErrInvalidInput
	}
	activeCalls, err := readTelnyxPages[telnyxActiveCall](ctx, adapter,
		"connections/"+url.PathEscape(connectionID)+"/active_calls", url.Values{"page[size]": {"250"}})
	if err != nil {
		return ProviderCallObservation{}, err
	}
	observation := ProviderCallObservation{}
	for _, active := range activeCalls {
		if !((callLegID != "" && active.CallLegID == callLegID) ||
			(callLegID == "" && clientState != "" && active.ClientState == clientState)) {
			continue
		}
		if active.CallControlID == "" || active.CallLegID == "" || active.CallSessionID == "" {
			return ProviderCallObservation{}, fmt.Errorf("%w: incomplete active Telnyx Call identity", ErrAmbiguousEffect)
		}
		if observation.Active {
			if observation.CallControlID == active.CallControlID && observation.CallLegID == active.CallLegID && observation.CallSessionID == active.CallSessionID {
				continue
			}
			return ProviderCallObservation{}, fmt.Errorf("%w: multiple active Telnyx Calls match one CallLeg", ErrAmbiguousEffect)
		}
		observation.Active = true
		observation.CallControlID, observation.CallLegID, observation.CallSessionID = active.CallControlID, active.CallLegID, active.CallSessionID
	}
	if observation.Active {
		if callControlID != "" && observation.CallControlID != callControlID {
			return ProviderCallObservation{}, fmt.Errorf("%w: active Telnyx Call identity changed", ErrDefinitiveProviderFailure)
		}
		callLegID = observation.CallLegID
		callControlID = observation.CallControlID
	}
	if callLegID == "" {
		return observation, nil
	}
	events, err := readTelnyxPages[telnyxReadEvent](ctx, adapter, "call_events", url.Values{
		"page[size]": {"100"}, "filter[leg_id]": {callLegID}, "filter[type]": {"webhook"},
		"filter[occurred_at][gte]": {since.UTC().Format(time.RFC3339Nano)},
	})
	if err != nil {
		return ProviderCallObservation{}, err
	}
	sessionID := observation.CallSessionID
	for _, event := range events {
		fact, known, err := event.fact(callLegID, sessionID)
		if err != nil {
			return ProviderCallObservation{}, err
		}
		if !known {
			continue
		}
		if (fact.CallControlID != "" && callControlID != "" && fact.CallControlID != callControlID) ||
			(fact.ConnectionID != "" && fact.ConnectionID != connectionID) {
			return ProviderCallObservation{}, fmt.Errorf("%w: contradictory Telnyx Call event control identity", ErrDefinitiveProviderFailure)
		}
		sessionID = fact.CallSessionID
		observation.Events = append(observation.Events, fact)
	}
	return observation, nil
}

func (event telnyxReadEvent) fact(legID, sessionID string) (ProviderFact, bool, error) {
	switch FactType(event.Name) {
	case FactCallInitiated, FactCallAnswered, FactCallBridged, FactCallHangup,
		FactPlaybackStarted, FactPlaybackEnded, FactSpeakStarted, FactSpeakEnded, FactRecordingSaved, FactRecordingError:
	default:
		return ProviderFact{}, false, nil
	}
	current := event.LegID != "" || event.SessionID != "" || event.OccurredAt != "" || len(event.Payload) != 0
	if current {
		if (event.CallLegID != "" && event.CallLegID != event.LegID) ||
			(event.CallSessionID != "" && event.CallSessionID != event.SessionID) {
			return ProviderFact{}, false, fmt.Errorf("%w: conflicting Telnyx Call event schemas", ErrDefinitiveProviderFailure)
		}
		event.CallLegID, event.CallSessionID = event.LegID, event.SessionID
	}
	if event.CallLegID == "" || event.CallSessionID == "" {
		return ProviderFact{}, false, fmt.Errorf("%w: missing Telnyx Call event identity", ErrAmbiguousEffect)
	}
	if event.CallLegID != legID || (sessionID != "" && event.CallSessionID != sessionID) {
		return ProviderFact{}, false, fmt.Errorf("%w: contradictory Telnyx Call event identity", ErrDefinitiveProviderFailure)
	}
	var raw []byte
	if current {
		var payload struct {
			RecordType string          `json:"record_type"`
			EventType  string          `json:"event_type"`
			WebhookID  string          `json:"webhook_id"`
			Payload    json.RawMessage `json:"payload"`
		}
		if json.Unmarshal(event.Payload, &payload) != nil || payload.EventType != event.Name {
			return ProviderFact{}, false, fmt.Errorf("%w: invalid Telnyx Call event payload", ErrAmbiguousEffect)
		}
		var timing struct {
			OccurredAt string `json:"occurred_at"`
		}
		if json.Unmarshal(payload.Payload, &timing) != nil {
			return ProviderFact{}, false, fmt.Errorf("%w: missing Telnyx Call event payload", ErrAmbiguousEffect)
		}
		if _, err := parseTelnyxTime(timing.OccurredAt); err != nil {
			return ProviderFact{}, false, fmt.Errorf("%w: invalid Telnyx Call event timestamp", ErrAmbiguousEffect)
		}
		// The read envelope's occurred_at is a timezone-free database timestamp.
		// The nested webhook retains the canonical RFC3339 event time and ID.
		raw, _ = json.Marshal(map[string]any{"data": map[string]any{
			"record_type": payload.RecordType, "event_type": payload.EventType,
			"id": payload.WebhookID, "occurred_at": timing.OccurredAt, "payload": payload.Payload,
		}})
	} else {
		raw, _ = rawCallEvent(event.Metadata)
	}
	if len(raw) != 0 {
		fact, known, err := normalizeTelnyxFact(raw)
		if err != nil {
			return ProviderFact{}, false, fmt.Errorf("%w: invalid Telnyx raw Call event", ErrAmbiguousEffect)
		}
		if fact.CallLegID != event.CallLegID || fact.CallSessionID != event.CallSessionID || string(fact.Type) != event.Name {
			return ProviderFact{}, false, fmt.Errorf("%w: contradictory Telnyx raw Call event identity", ErrDefinitiveProviderFailure)
		}
		return fact, known, nil
	}
	// Published legacy summaries have no webhook payload. Only lifecycle facts
	// (and the explicit recording failure) can be represented without one.
	switch FactType(event.Name) {
	case FactCallInitiated, FactCallAnswered, FactCallBridged, FactCallHangup, FactRecordingError:
	default:
		return ProviderFact{}, false, nil
	}
	timestamp, err := parseTelnyxTime(event.EventTimestamp)
	if err != nil {
		return ProviderFact{}, false, fmt.Errorf("%w: invalid Telnyx Call event timestamp", ErrAmbiguousEffect)
	}
	digest := sha256.Sum256([]byte(event.Name + "\x00" + event.CallLegID + "\x00" + event.CallSessionID + "\x00" + timestamp.UTC().Format(time.RFC3339Nano)))
	return ProviderFact{EventID: fmt.Sprintf("telnyx-call-event-%x", digest[:]), Type: FactType(event.Name), OccurredAt: timestamp, CallLegID: event.CallLegID, CallSessionID: event.CallSessionID}, true, nil
}

func (adapter *TelnyxAdapter) recordingFailed(ctx context.Context, callLegID, callSessionID string) (time.Time, error) {
	events, err := readTelnyxPages[telnyxReadEvent](ctx, adapter, "call_events", url.Values{
		"page[size]": {"2"}, "filter[leg_id]": {callLegID}, "filter[application_session_id]": {callSessionID},
		"filter[name]": {string(FactRecordingError)}, "filter[type]": {"webhook"},
	})
	if err != nil {
		return time.Time{}, err
	}
	for _, event := range events {
		fact, known, err := event.fact(callLegID, callSessionID)
		if err != nil {
			return time.Time{}, err
		}
		if !known || fact.Type != FactRecordingError {
			return time.Time{}, fmt.Errorf("%w: contradictory Telnyx recording error identity", ErrDefinitiveProviderFailure)
		}
		return fact.OccurredAt, nil
	}
	return time.Time{}, nil
}

func rawCallEvent(metadata map[string]any) ([]byte, bool) {
	for _, key := range []string{"raw", "raw_event", "event"} {
		raw, ok := metadata[key]
		if !ok || raw == nil {
			continue
		}
		if encoded, ok := raw.(string); ok {
			return []byte(encoded), true
		}
		encoded, err := json.Marshal(raw)
		return encoded, err == nil
	}
	return nil, false
}
