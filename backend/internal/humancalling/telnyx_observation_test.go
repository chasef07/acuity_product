package humancalling_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/humancalling"
)

// Synthetic values with the nested read-API payload structure observed on
// 2026-09-08. The envelope timestamp is intentionally different from the event's
// timestamp, as it is in provider responses; the webhook owns occurrence time.
func currentTelnyxEvent(name string) map[string]any {
	return map[string]any{
		"name": name, "type": "webhook", "leg_id": "leg-1", "application_session_id": "session-1",
		"occurred_at": "2026-08-05 12:00:01.123456",
		"payload": map[string]any{
			"record_type": "event", "event_type": name, "webhook_id": "webhook-" + name,
			"created_at": "2026-08-05T12:00:01.123456Z",
			"payload": map[string]any{
				"call_control_id": "control-1", "call_leg_id": "leg-1", "call_session_id": "session-1",
				"connection_id": "connection-1", "occurred_at": "2026-08-05T12:00:01.000000Z",
			},
		},
	}
}

func eventPayload(event map[string]any) map[string]any {
	return event["payload"].(map[string]any)["payload"].(map[string]any)
}

func observationAdapter(t *testing.T, handler http.HandlerFunc) *humancalling.TelnyxAdapter {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	adapter, err := humancalling.NewTelnyxAdapter(humancalling.TelnyxConfig{
		APIKey: "synthetic-key", BaseURL: server.URL + "/v2", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return adapter
}

func observeSyntheticCall(ctx context.Context, adapter *humancalling.TelnyxAdapter) (humancalling.ProviderCallObservation, error) {
	return adapter.ObserveCall(ctx, "connection-1", "control-1", "leg-1", "", time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC))
}

func TestTelnyxObservationPreservesCurrentWebhookEvidence(t *testing.T) {
	names := []string{"call.initiated", "call.answered", "call.bridged", "call.playback.started", "call.playback.ended", "call.speak.started", "call.speak.ended", "call.recording.saved", "call.recording.error", "call.hangup"}
	var events []map[string]any
	for _, name := range names {
		event := currentTelnyxEvent(name)
		payload := eventPayload(event)
		payload["status"] = "completed"
		payload["from"], payload["to"] = "+15555550100", "+15555550101"
		payload["hangup_cause"], payload["hangup_source"], payload["sip_hangup_cause"] = "normal_clearing", "caller", "200"
		payload["recording_id"] = "recording-1"
		payload["recording_started_at"], payload["recording_ended_at"] = "2026-08-05T12:00:00Z", "2026-08-05T12:00:01Z"
		payload["call_quality_stats"] = map[string]any{"inbound": map[string]any{"mos": "4.1"}}
		events = append(events, event)
	}
	adapter := observationAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "active_calls") {
			fmt.Fprint(w, `{"data":[],"meta":{"total_items":0,"cursors":{"after":null,"before":null},"next":null,"previous":null}}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": events, "meta": map[string]any{"page_number": 1, "total_pages": 1}})
	})
	observation, err := observeSyntheticCall(context.Background(), adapter)
	if err != nil || len(observation.Events) != len(events) {
		t.Fatalf("observation = %#v, err = %v", observation, err)
	}
	for index, fact := range observation.Events {
		if string(fact.Type) != names[index] || fact.EventID != "webhook-"+names[index] || fact.OccurredAt.Nanosecond() != 0 || fact.CallLegID != "leg-1" || fact.CallSessionID != "session-1" || fact.CallControlID != "control-1" {
			t.Fatalf("event identity/time lost: %#v", fact)
		}
		if fact.PlaybackStatus != "completed" || fact.HangupCause != "normal_clearing" || fact.TerminationSource != "caller" || fact.SIPCause != "200" || fact.RecordingID != "recording-1" || fact.RecordingEndedAt.Sub(fact.RecordingStartedAt) != time.Second || fact.CallQualityStats == nil || fact.From != "+15555550100" {
			t.Fatalf("event payload lost: %#v", fact)
		}
	}
}

func TestTelnyxObservationRejectsContradictoryCurrentEvidence(t *testing.T) {
	tests := []struct {
		name   string
		change func(map[string]any)
	}{
		{"requested leg", func(e map[string]any) { e["leg_id"] = "another-leg" }},
		{"nested leg", func(e map[string]any) { eventPayload(e)["call_leg_id"] = "another-leg" }},
		{"nested session", func(e map[string]any) { eventPayload(e)["call_session_id"] = "another-session" }},
		{"control", func(e map[string]any) { eventPayload(e)["call_control_id"] = "another-control" }},
		{"connection", func(e map[string]any) { eventPayload(e)["connection_id"] = "another-connection" }},
		{"legacy alias", func(e map[string]any) { e["call_leg_id"] = "another-leg" }},
		{"nested type", func(e map[string]any) { e["payload"].(map[string]any)["event_type"] = "call.hangup" }},
		{"invalid timestamp", func(e map[string]any) { eventPayload(e)["occurred_at"] = "invalid" }},
		{"missing session", func(e map[string]any) { delete(e, "application_session_id") }},
		{"missing payload", func(e map[string]any) { e["payload"] = map[string]any{} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			event := currentTelnyxEvent("call.answered")
			tc.change(event)
			adapter := observationAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "active_calls") {
					fmt.Fprint(w, `{"data":[]}`)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"data": []any{event}})
			})
			observation, err := observeSyntheticCall(context.Background(), adapter)
			if err == nil || len(observation.Events) != 0 {
				t.Fatalf("invalid evidence accepted: %#v, %v", observation, err)
			}
		})
	}
}

func TestTelnyxObservationCursorPaginationAndActiveIdentity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		pages    int
		conflict bool
	}{
		{"terminal nonempty cursor page", 1, false},
		{"identical identity across pages", 2, false},
		{"conflicting identity across pages", 2, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			adapter := observationAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if !strings.HasSuffix(r.URL.Path, "active_calls") {
					fmt.Fprint(w, `{"data":[]}`)
					return
				}
				page := int(requests.Add(1))
				if page > tc.pages {
					t.Errorf("unexpected repeated page %d", page)
					http.Error(w, "too many requests", 500)
					return
				}
				if r.URL.Query().Get("page[number]") != "" {
					t.Error("cursor request used page number")
				}
				if page == 2 && r.URL.Query().Get("page[after]") != "cursor-2" {
					t.Error("missing cursor")
				}
				control := "control-1"
				if tc.conflict && page == 2 {
					control = "other-control"
				}
				var cursor, next any
				if page < tc.pages {
					cursor = "cursor-2"
					next = "https://untrusted.invalid/should-never-be-requested"
				}
				json.NewEncoder(w).Encode(map[string]any{
					"data": []any{map[string]any{"call_control_id": control, "call_leg_id": "leg-1", "call_session_id": "session-1"}},
					"meta": map[string]any{"cursors": map[string]any{"after": cursor}, "next": next},
				})
			})
			observation, err := observeSyntheticCall(context.Background(), adapter)
			if tc.conflict {
				if !errors.Is(err, humancalling.ErrAmbiguousEffect) {
					t.Fatalf("conflict = %v", err)
				}
			} else if err != nil || !observation.Active {
				t.Fatalf("observation = %#v, %v", observation, err)
			}
			if int(requests.Load()) != tc.pages {
				t.Fatalf("requests = %d", requests.Load())
			}
		})
	}
}

func TestTelnyxObservationBoundsPagination(t *testing.T) {
	for _, mode := range []string{"repeated cursor", "changing cursors", "wrong numbered page"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int32
			adapter := observationAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				n := requests.Add(1)
				if n > 100 {
					t.Error("page bound exceeded")
					http.Error(w, "bound", 500)
					return
				}
				switch mode {
				case "repeated cursor":
					fmt.Fprint(w, `{"data":[],"meta":{"cursors":{"after":"same"},"next":"next"}}`)
				case "changing cursors":
					fmt.Fprintf(w, `{"data":[],"meta":{"cursors":{"after":"cursor-%d"},"next":"next"}}`, n)
				default:
					fmt.Fprint(w, `{"data":[{"name":"ignored.synthetic.event"}],"meta":{"page_number":1,"total_pages":2}}`)
				}
			})
			_, err := observeSyntheticCall(context.Background(), adapter)
			if !errors.Is(err, humancalling.ErrAmbiguousEffect) {
				t.Fatalf("paging error = %v", err)
			}
		})
	}
}

func TestTelnyxObservationLaterPagesHonorCallerDeadline(t *testing.T) {
	for _, endpoint := range []string{"active_calls", "call_events"} {
		t.Run(endpoint, func(t *testing.T) {
			var laterPage atomic.Bool
			adapter := observationAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if !strings.HasSuffix(r.URL.Path, endpoint) {
					fmt.Fprint(w, `{"data":[]}`)
					return
				}
				if r.URL.Query().Get("page[after]") != "" || r.URL.Query().Get("page[number]") == "2" || (endpoint == "active_calls" && r.URL.Query().Get("page[number]") != "") {
					laterPage.Store(true)
					select {
					case <-r.Context().Done():
						return
					case <-time.After(2 * time.Second):
						fmt.Fprint(w, `{"data":[]}`)
						return
					}
				}
				if endpoint == "active_calls" {
					fmt.Fprint(w, `{"data":[{"call_leg_id":"other-leg"}],"meta":{"cursors":{"after":"next"},"next":"next"}}`)
				} else {
					fmt.Fprint(w, `{"data":[{"name":"ignored.synthetic.event"}],"meta":{"page_number":1,"total_pages":2}}`)
				}
			})
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			start := time.Now()
			_, err := observeSyntheticCall(ctx, adapter)
			if err == nil || time.Since(start) > time.Second || !laterPage.Load() {
				t.Fatalf("deadline escaped: elapsed=%s, later page=%v, err=%v", time.Since(start), laterPage.Load(), err)
			}
		})
	}
}

func TestTelnyxObservationRecordingFailureUsesCurrentPayload(t *testing.T) {
	adapter := observationAdapter(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "recordings") {
			fmt.Fprint(w, `{"data":[]}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"data": []any{currentTelnyxEvent("call.recording.error")}})
	})
	_, err := adapter.ResolveRecording(context.Background(), "leg-1", "session-1")
	if !errors.Is(err, humancalling.ErrProviderRecordingFailed) {
		t.Fatalf("recording failure = %v", err)
	}
}
