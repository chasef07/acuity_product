package architecture

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/observability"
)

func TestEveryProviderCommandKeepsItsMetricLabel(t *testing.T) {
	actions := typedStringConstants(t,
		filepath.Join(repositoryRoot(t), "backend", "internal", "humancalling"), "CommandAction")
	if len(actions) == 0 {
		t.Fatal("found no provider command actions")
	}
	for name, value := range actions {
		action := observability.CommandAction(strings.ToLower(value))
		for _, event := range []observability.Event{
			observability.ProviderCommandCompleted(action, observability.CommandSent, time.Second, time.Second),
			observability.ProviderCommandStage(action, observability.CommandStageProvider, observability.CommandStageSucceeded, time.Second),
		} {
			var output bytes.Buffer
			observability.NewLogger("worker", "test", slog.New(slog.NewJSONHandler(&output, nil))).Observe(event)
			var entry map[string]any
			if err := json.Unmarshal(output.Bytes(), &entry); err != nil {
				t.Fatalf("decode metric log: %v", err)
			}
			if entry["action"] != string(action) {
				t.Errorf("%s in %v has action label %v, want %s", name, entry["metric"], entry["action"], action)
			}
		}
	}
}
