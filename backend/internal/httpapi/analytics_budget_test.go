package httpapi

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestAnalyticsBudgetRejectsOverlapAndReleasesOnFinish(t *testing.T) {
	server := &Server{config: Config{RequestTimeout: 10 * time.Second}}
	request := httptest.NewRequest("POST", "/v1/analytics/bookings/query", nil)
	ctx, finish, ok := server.beginAnalytics(httptest.NewRecorder(), request)
	if !ok {
		t.Fatal("first analytics request rejected")
	}
	defer finish()
	deadline, present := ctx.Deadline()
	if !present || time.Until(deadline) > 2*time.Second {
		t.Fatal("analytics must have its own short deadline")
	}
	busy := httptest.NewRecorder()
	if _, _, ok := server.beginAnalytics(busy, request); ok {
		t.Fatal("overlapping analytics request acquired a permit")
	}
	if busy.Code != 429 || busy.Header().Get("Retry-After") != "1" {
		t.Fatalf("busy response: %d %v", busy.Code, busy.Header())
	}
	// Other HTTP work is unaffected while analytics holds its permit.
	live := httptest.NewRecorder()
	server.GetLiveness(live, httptest.NewRequest("GET", "/health/live", nil))
	if live.Code != 200 {
		t.Fatalf("analytics blocked liveness: %d", live.Code)
	}
	finish()
	if ctx.Err() == nil {
		t.Fatal("finishing analytics did not cancel work")
	}
	_, nextFinish, ok := server.beginAnalytics(httptest.NewRecorder(), request)
	if !ok {
		t.Fatal("finished request retained its permit")
	}
	nextFinish()
}
