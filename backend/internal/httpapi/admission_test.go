package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/admission"
)

func TestPortalAdmissionAbsorbsShortReadBurst(t *testing.T) {
	server := &Server{admission: admission.New(4)}
	var active, maximum atomic.Int32
	handler := server.withPortalAdmission(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for previous := maximum.Load(); current > previous; previous = maximum.Load() {
			if maximum.CompareAndSwap(previous, current) {
				break
			}
		}
		// Represent the short independent reads issued together by a page load.
		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	}))
	start := make(chan struct{})
	results := make(chan int, 8)
	for range 8 {
		go func() {
			<-start
			request := httptest.NewRequest(http.MethodGet, "/v1/workspace", nil)
			request.Pattern = "GET /v1/workspace"
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			results <- response.Code
		}()
	}
	close(start)
	for range 8 {
		if status := <-results; status != http.StatusNoContent {
			t.Errorf("short read burst status=%d, want %d", status, http.StatusNoContent)
		}
	}
	if maximum.Load() != 2 {
		t.Fatalf("concurrent background handlers=%d, want 2", maximum.Load())
	}
}

func TestPortalAdmissionBoundsWaitAndReleasesCanceledHandlers(t *testing.T) {
	server := &Server{admission: admission.New(4)}
	entered := make(chan admission.Class, 4)
	handler := server.withPortalAdmission(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- admission.ClassOf(r.Context())
		if r.URL.Query().Get("hold") == "true" {
			<-r.Context().Done()
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	startHeld := func(pattern string) (context.CancelFunc, <-chan struct{}) {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		req := httptest.NewRequest(http.MethodGet, "/?hold=true", nil).WithContext(ctx)
		req.Pattern = pattern
		done := make(chan struct{})
		go func() {
			handler.ServeHTTP(httptest.NewRecorder(), req)
			close(done)
		}()
		select {
		case <-entered:
		case <-time.After(time.Second):
			cancel()
			t.Fatal("admitted handler did not start")
		}
		return cancel, done
	}
	cancelFirst, firstDone := startHeld("GET /v1/engagements/{phone}/timeline")
	defer cancelFirst()
	cancelSecond, secondDone := startHeld("POST /v1/operator/ai-analytics/query")
	defer cancelSecond()
	cancelSync, syncDone := startHeld("GET /v1/calling/state")
	defer cancelSync()

	for _, pattern := range []string{
		"GET /v1/workspace", "GET /v1/calling/state",
		"GET /v1/calling/calls/{callId}/history",
		"GET /v1/calling/calls/{callId}/hangup",
	} {
		request := httptest.NewRequest(http.MethodGet, "/v1/calling/readiness", nil)
		request.Pattern = pattern
		request.Header.Set("X-Priority", "calling-control")
		request = request.WithContext(admission.WithClass(request.Context(), admission.CallingControl))
		response := httptest.NewRecorder()
		started := time.Now()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "1" {
			t.Fatalf("overloaded %s status=%d retryAfter=%q", pattern, response.Code, response.Header().Get("Retry-After"))
		}
		if elapsed := time.Since(started); elapsed < 75*time.Millisecond || elapsed > 250*time.Millisecond {
			t.Fatalf("background admission exceeded its short wait budget: %s", elapsed)
		}
	}
	waiting, cancelWaiting := context.WithCancel(context.Background())
	waiterDone := make(chan struct{})
	go func() {
		request := httptest.NewRequest(http.MethodGet, "/v1/workspace", nil).WithContext(waiting)
		request.Pattern = "GET /v1/workspace"
		handler.ServeHTTP(httptest.NewRecorder(), request)
		close(waiterDone)
	}()
	cancelWaiting()
	select {
	case <-waiterDone:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("canceled admission waiter did not return promptly")
	}
	control := httptest.NewRequest(http.MethodPost, "/v1/calling/calls/synthetic/hangup", nil)
	control.Pattern = "POST /v1/calling/calls/{callId}/hangup"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, control)
	if response.Code != http.StatusNoContent || <-entered != admission.CallingControl {
		t.Fatal("live command did not bypass non-control admission")
	}
	cancelFirst()
	<-firstDone
	replacementCancel, replacementDone := startHeld("GET /v1/workspace")
	replacementCancel()
	<-replacementDone
	cancelSecond()
	<-secondDone
	cancelSync()
	<-syncDone

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodGet, "/v1/workspace", nil).WithContext(canceled)
	request.Pattern = "GET /v1/workspace"
	handler.ServeHTTP(httptest.NewRecorder(), request)
	select {
	case <-entered:
		t.Fatal("already canceled request entered the handler")
	default:
	}
}
