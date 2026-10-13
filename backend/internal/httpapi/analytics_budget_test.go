package httpapi

import (
	"context"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestAnalyticsBudgetAdmitsBoundedConcurrencyAndReleasesOnFinish(t *testing.T) {
	server := &Server{config: Config{RequestTimeout: 10 * time.Second}}
	request := httptest.NewRequest("POST", "/v1/analytics/bookings/query", nil)
	contexts := make([]context.Context, 0, analyticsConcurrency)
	finishes := make([]func(), 0, analyticsConcurrency)
	for range analyticsConcurrency {
		ctx, finish, ok := server.beginAnalytics(httptest.NewRecorder(), request)
		if !ok {
			t.Fatalf("analytics request %d of %d rejected", len(finishes)+1, analyticsConcurrency)
		}
		defer finish()
		deadline, present := ctx.Deadline()
		if !present || time.Until(deadline) > analyticsTimeout {
			t.Fatal("analytics must have its own short deadline")
		}
		contexts = append(contexts, ctx)
		finishes = append(finishes, finish)
	}
	busy := httptest.NewRecorder()
	if _, _, ok := server.beginAnalytics(busy, request); ok {
		t.Fatal("analytics request beyond the limit acquired a permit")
	}
	if busy.Code != 429 || busy.Header().Get("Retry-After") != "1" {
		t.Fatalf("busy response: %d %v", busy.Code, busy.Header())
	}
	live := httptest.NewRecorder()
	server.GetLiveness(live, httptest.NewRequest("GET", "/health/live", nil))
	if live.Code != 200 {
		t.Fatalf("analytics blocked liveness: %d", live.Code)
	}
	finishes[0]()
	finishes[0]()
	if contexts[0].Err() == nil {
		t.Fatal("finishing analytics did not cancel work")
	}
	_, nextFinish, ok := server.beginAnalytics(httptest.NewRecorder(), request)
	if !ok {
		t.Fatal("finished request retained its permit")
	}
	defer nextFinish()
	if _, _, ok := server.beginAnalytics(httptest.NewRecorder(), request); ok {
		t.Fatal("repeated finish released more than one permit")
	}
}

func TestAnalyticsBudgetHonorsShorterRequestTimeout(t *testing.T) {
	server := &Server{config: Config{RequestTimeout: 500 * time.Millisecond}}
	ctx, finish, ok := server.beginAnalytics(httptest.NewRecorder(), httptest.NewRequest("POST", "/v1/analytics/bookings/query", nil))
	if !ok {
		t.Fatal("analytics request rejected")
	}
	defer finish()
	deadline, present := ctx.Deadline()
	if !present || time.Until(deadline) > 500*time.Millisecond {
		t.Fatal("analytics deadline exceeded the request timeout")
	}
}

func TestAnalyticsBudgetAdmitsExactlyTheLimitUnderSimultaneousRequests(t *testing.T) {
	server := &Server{config: Config{RequestTimeout: 10 * time.Second}}
	const attempts = 16
	var (
		start    sync.WaitGroup
		done     sync.WaitGroup
		mu       sync.Mutex
		admitted []func()
		rejected int
	)
	start.Add(1)
	for range attempts {
		done.Go(func() {
			start.Wait()
			recorder := httptest.NewRecorder()
			_, finish, ok := server.beginAnalytics(recorder, httptest.NewRequest("POST", "/v1/analytics/costs/query", nil))
			mu.Lock()
			defer mu.Unlock()
			if ok {
				admitted = append(admitted, finish)
			} else if recorder.Code == 429 {
				rejected++
			}
		})
	}
	start.Done()
	done.Wait()
	for _, finish := range admitted {
		finish()
	}
	if len(admitted) != analyticsConcurrency || rejected != attempts-analyticsConcurrency {
		t.Fatalf("admitted=%d rejected=%d, want %d and %d", len(admitted), rejected, analyticsConcurrency, attempts-analyticsConcurrency)
	}
	if active := server.analyticsActive.Load(); active != 0 {
		t.Fatalf("active analytics after finish = %d", active)
	}
}
