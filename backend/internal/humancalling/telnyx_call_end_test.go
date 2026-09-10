package humancalling_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

func TestTelnyxCallEndRequiresExactPositiveEvidence(t *testing.T) {
	for _, tc := range []struct {
		name           string
		change         func(map[string]any)
		status         int
		ended, wantErr bool
	}{
		{name: "ended", ended: true},
		{name: "alive", change: func(d map[string]any) { d["is_alive"] = true }},
		{name: "missing end time", change: func(d map[string]any) { delete(d, "end_time") }},
		{name: "missing alive state", change: func(d map[string]any) { delete(d, "is_alive") }, wantErr: true},
		{name: "different control", change: func(d map[string]any) { d["call_control_id"] = "other" }, wantErr: true},
		{name: "different leg", change: func(d map[string]any) { d["call_leg_id"] = "other" }, wantErr: true},
		{name: "different session", change: func(d map[string]any) { d["call_session_id"] = "other" }, wantErr: true},
		{name: "invalid end time", change: func(d map[string]any) { d["end_time"] = "invalid" }, wantErr: true},
		{name: "absent is not ended", status: http.StatusNotFound, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adapter := observationAdapter(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v2/calls/control-1" {
					t.Errorf("unexpected provider request %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.status != 0 {
					w.WriteHeader(tc.status)
					return
				}
				data := map[string]any{"call_control_id": "control-1", "call_leg_id": "leg-1", "call_session_id": "session-1", "is_alive": false, "end_time": "2026-09-09T08:00:00Z"}
				if tc.change != nil {
					tc.change(data)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
			})
			ended, err := adapter.IsCallEnded(context.Background(), "control-1", "leg-1", "session-1")
			if ended != tc.ended || (err != nil) != tc.wantErr {
				t.Fatalf("ended=%t err=%v", ended, err)
			}
		})
	}
}
