package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/chasef07/acuity_product/backend/internal/workspace"
	"github.com/google/uuid"
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

func TestConversationTimelineResponseMapsCallTaskVoicemailLengthAndResolvedLabel(t *testing.T) {
	id := func(last byte) string {
		return uuid.UUID{15: last}.String()
	}
	placed := humancalling.CallHistoryItem{
		ID: id(1), Type: "CALL", Direction: "OUTBOUND", LocationID: id(9), Outcome: humancalling.CallUnanswered,
		TaskID: id(2), TaskTitle: "Return missed call",
	}
	voicemail := humancalling.CallHistoryItem{
		ID: id(3), Type: "CALL", Direction: "INBOUND", LocationID: id(9), Outcome: humancalling.CallVoicemail,
		VoicemailDurationSeconds: 23,
	}
	page, err := conversationTimelineResponse(workspace.TimelinePage{Items: []workspace.TimelineItem{
		{Type: "CALL", ID: placed.ID, Call: placed},
		{Type: "CALL", ID: voicemail.ID, Call: voicemail},
		{Type: "AI_INTERACTION", ID: id(4), AIInteraction: interaction.OutcomeItem{ID: id(4), LocationID: id(9), Resolved: true}},
		{Type: "AI_INTERACTION", ID: id(5), AIInteraction: interaction.OutcomeItem{ID: id(5), LocationID: id(9)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(page.Items)
	if err != nil {
		t.Fatal(err)
	}
	var items []struct {
		Call          map[string]any `json:"call"`
		AiInteraction map[string]any `json:"aiInteraction"`
	}
	if err := json.Unmarshal(encoded, &items); err != nil {
		t.Fatal(err)
	}
	if got := items[0].Call; got["taskId"] != placed.TaskID || got["taskTitle"] != placed.TaskTitle || got["voicemailDurationSeconds"] != nil {
		t.Fatalf("placed call = %v, want its Task and no voicemail length", got)
	}
	if got := items[1].Call; got["voicemailDurationSeconds"] != float64(23) || got["taskId"] != nil || got["taskTitle"] != nil {
		t.Fatalf("voicemail call = %v, want 23 seconds and no Task", got)
	}
	if items[2].AiInteraction["resolved"] != true || items[3].AiInteraction["resolved"] != false {
		t.Fatalf("resolved = %v/%v, want true/false", items[2].AiInteraction["resolved"], items[3].AiInteraction["resolved"])
	}
}
