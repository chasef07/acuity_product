package knowledge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"time"

	"golang.org/x/oauth2/google"
)

// GoogleEmbedder uses the pinned regional Vertex prediction contract. The HTTP
// client is the provider seam; nil selects Application Default Credentials.
type GoogleEmbedder struct {
	client   *http.Client
	endpoint string
}

func NewGoogleEmbedder(ctx context.Context, project, location string, client *http.Client) (*GoogleEmbedder, error) {
	if !regexp.MustCompile(`^[a-z][a-z0-9-]{4,61}[a-z0-9]$`).MatchString(project) || !regexp.MustCompile(`^[a-z]+-[a-z]+[0-9]+$`).MatchString(location) {
		return nil, fmt.Errorf("invalid Google knowledge project or location")
	}
	if client == nil {
		var err error
		client, err = google.DefaultClient(ctx, "https://www.googleapis.com/auth/cloud-platform")
		if err != nil {
			return nil, fmt.Errorf("Google knowledge credentials unavailable")
		}
	}
	return &GoogleEmbedder{client: client, endpoint: fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/publishers/google/models/%s:predict", location, project, location, Model)}, nil
}

func (g *GoogleEmbedder) Embed(ctx context.Context, texts []string, task TaskType) ([][]float32, error) {
	if len(texts) == 0 || len(texts) > 128 || (task != RetrievalDocument && task != RetrievalQuery) {
		return nil, ErrInvalidInput
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	vectors := make([][]float32, 0, len(texts))
	// Five inputs per request keeps the legacy multilingual model's regional
	// request limit explicit. A failed batch never returns a partial corpus.
	for start := 0; start < len(texts); start += 5 {
		end := min(start+5, len(texts))
		instances := make([]map[string]any, 0, end-start)
		for _, text := range texts[start:end] {
			if len(text) == 0 || len(text) > 24000 {
				return nil, ErrInvalidInput
			}
			instances = append(instances, map[string]any{"content": text, "task_type": task})
		}
		body, err := json.Marshal(map[string]any{"instances": instances, "parameters": map[string]any{"autoTruncate": false, "outputDimensionality": Dimensions}})
		if err != nil {
			return nil, ErrInvalidInput
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, &ProviderFailure{Code: "response"}
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := g.client.Do(req)
		if err != nil {
			return nil, &ProviderFailure{Code: "transport"}
		}
		var result struct {
			Predictions []struct {
				Embeddings struct {
					Values     []float32 `json:"values"`
					Statistics struct {
						Truncated bool `json:"truncated"`
					} `json:"statistics"`
				} `json:"embeddings"`
			} `json:"predictions"`
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, &ProviderFailure{Code: "http", Status: resp.StatusCode}
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&result)
		resp.Body.Close()
		if err != nil || len(result.Predictions) != len(instances) {
			return nil, &ProviderFailure{Code: "response"}
		}
		for _, prediction := range result.Predictions {
			embedding := prediction.Embeddings
			if embedding.Statistics.Truncated || len(embedding.Values) != Dimensions {
				return nil, &ProviderFailure{Code: "incomplete"}
			}
			norm := float64(0)
			for _, v := range embedding.Values {
				if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
					return nil, &ProviderFailure{Code: "response"}
				}
				norm += float64(v) * float64(v)
			}
			if norm == 0 {
				return nil, &ProviderFailure{Code: "response"}
			}
			vectors = append(vectors, embedding.Values)
		}
	}
	return vectors, nil
}
