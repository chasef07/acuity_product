package deploy_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortalCanaryAuthenticatesAndReadsCriticalCallingState(t *testing.T) {
	const token = "synthetic-canary-secret"
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "missing credential", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "synthetic"})
	}))
	defer server.Close()

	command := exec.Command("node", filepath.Join(
		deployDirectory(t), "observability", "run-portal-canary.mjs",
	))
	command.Env = append(os.Environ(),
		"PORTAL_API_URL="+server.URL,
		"PORTAL_CANARY_BEARER_TOKEN="+token,
		"PORTAL_CANARY_ALLOW_HTTP=1",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run portal canary: %v\n%s", err, output)
	}
	if strings.Join(paths, ",") != "/v1/access,/v1/calling/state" {
		t.Fatalf("canary paths = %v", paths)
	}
	if strings.Contains(string(output), token) ||
		!strings.Contains(string(output), `"status":"ok"`) {
		t.Fatalf("canary output is unsafe or incomplete: %s", output)
	}
}

func TestPortalCanaryFailureDoesNotPrintResponseOrCredential(t *testing.T) {
	const token = "synthetic-canary-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "patient@example.test", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	command := exec.Command("node", filepath.Join(
		deployDirectory(t), "observability", "run-portal-canary.mjs",
	))
	command.Env = append(os.Environ(),
		"PORTAL_API_URL="+server.URL,
		"PORTAL_CANARY_BEARER_TOKEN="+token,
		"PORTAL_CANARY_ALLOW_HTTP=1",
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatal("canary accepted an unavailable Access response")
	}
	for _, forbidden := range []string{token, "patient@example.test"} {
		if strings.Contains(string(output), forbidden) {
			t.Fatalf("canary failure output contains %q: %s", forbidden, output)
		}
	}
}
