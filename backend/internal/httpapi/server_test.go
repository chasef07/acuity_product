package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/work"
)

func TestPreflightCachingDoesNotCacheDataOrBypassRequestAuthorization(t *testing.T) {
	server := &Server{config: Config{AllowedOrigins: []string{"https://acuityhealth.io"}}}
	requests := 0
	handler := server.withRequestMetadata(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusUnauthorized)
	}))
	for _, scenario := range []struct {
		method, origin, maxAge string
		status                 int
	}{
		{http.MethodOptions, "https://acuityhealth.io", "600", http.StatusNoContent},
		{http.MethodOptions, "https://untrusted.example", "", http.StatusNoContent},
		{http.MethodGet, "https://acuityhealth.io", "", http.StatusUnauthorized},
	} {
		request := httptest.NewRequest(scenario.method, "/v1/access", nil)
		request.Header.Set("Origin", scenario.origin)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != scenario.status || response.Header().Get("Access-Control-Max-Age") != scenario.maxAge {
			t.Errorf("%s from %s: status=%d, preflight cache duration=%q", scenario.method, scenario.origin, response.Code, response.Header().Get("Access-Control-Max-Age"))
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Error("product response became cacheable")
		}
	}
	if requests != 1 {
		t.Fatalf("protected request handler calls=%d, want 1", requests)
	}
}

func TestTaskPageResponseMapsEveryTaskCategoryCount(t *testing.T) {
	categories := work.TaskCategoryCounts{}
	want := map[string]int{}
	for i, category := range work.TaskCategories {
		categories[category] = i + 1
		want[string(category)] = i + 1
	}
	response, err := taskPageResponse(work.TaskPage{Counts: &work.TaskFolderCounts{Categories: categories}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(response.Counts.Categories)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]int
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("category counts = %v, want %v", got, want)
	}
}
