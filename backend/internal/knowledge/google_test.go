package knowledge_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/knowledge"
)

type embeddingTransport func(*http.Request) (*http.Response, error)

func (f embeddingTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGoogleEmbeddingContractRejectsTruncationAndProviderDetails(t *testing.T) {
	for _, scenario := range []string{"complete", "truncated", "wrong dimensions", "provider failure"} {
		t.Run(scenario, func(t *testing.T) {
			client := &http.Client{Transport: embeddingTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "us-east1-aiplatform.googleapis.com" || !strings.HasSuffix(r.URL.Path, "/text-multilingual-embedding-002:predict") {
					t.Fatalf("wrong pinned endpoint: %s", r.URL)
				}
				var input struct {
					Instances []struct {
						Content string `json:"content"`
						Task    string `json:"task_type"`
					} `json:"instances"`
					Parameters struct {
						AutoTruncate bool `json:"autoTruncate"`
						Dimensions   int  `json:"outputDimensionality"`
					} `json:"parameters"`
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Fatal(err)
				}
				if input.Parameters.AutoTruncate || input.Parameters.Dimensions != 768 || len(input.Instances) != 1 || input.Instances[0].Task != "RETRIEVAL_QUERY" {
					t.Fatalf("invalid provider request: %+v", input)
				}
				if scenario == "provider failure" {
					return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader("private provider diagnostics")), Header: http.Header{}}, nil
				}
				count := 768
				if scenario == "wrong dimensions" {
					count = 3
				}
				values := make([]float32, count)
				values[0] = 1
				body, _ := json.Marshal(map[string]any{"predictions": []any{map[string]any{"embeddings": map[string]any{"values": values, "statistics": map[string]any{"truncated": scenario == "truncated"}}}}})
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body))), Header: http.Header{}}, nil
			})}
			provider, err := knowledge.NewGoogleEmbedder(context.Background(), "synthetic-project", "us-east1", client)
			if err != nil {
				t.Fatal(err)
			}
			vectors, err := provider.Embed(context.Background(), []string{"What time does the office close?"}, knowledge.RetrievalQuery)
			if scenario == "complete" {
				if err != nil || len(vectors) != 1 || len(vectors[0]) != 768 {
					t.Fatalf("vectors=%d err=%v", len(vectors), err)
				}
			} else if err == nil || strings.Contains(err.Error(), "private provider") {
				t.Fatalf("expected sanitized failure: %v", err)
			}
			if scenario == "provider failure" && knowledge.FailureCode(err) != "provider_rate_limited" {
				t.Fatalf("rate limit must remain actionable without provider body: %v", err)
			}
			if (scenario == "truncated" || scenario == "wrong dimensions") && knowledge.FailureCode(err) != "provider_embedding_incomplete" {
				t.Fatalf("incomplete embedding must remain distinguishable: %v", err)
			}
		})
	}
}
