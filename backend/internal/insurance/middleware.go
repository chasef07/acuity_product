package insurance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const middlewareTimeout = 5 * time.Second

var middlewareOffices = map[string]string{"sweetwater-optical": "sweetwater"}

func middlewareOffice(officeKey string) string {
	if office, found := middlewareOffices[officeKey]; found {
		return office
	}
	return officeKey
}

type MiddlewareClient struct {
	baseURL string
	secret  string
	client  *http.Client
}

func NewMiddlewareClient(baseURL, secret string, client *http.Client) (*MiddlewareClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || strings.TrimSpace(secret) == "" {
		return nil, fmt.Errorf("middleware base URL and API secret are required")
	}
	if client == nil {
		client = &http.Client{}
	}
	return &MiddlewareClient{baseURL: strings.TrimRight(parsed.String(), "/"), secret: strings.TrimSpace(secret), client: client}, nil
}

func (c *MiddlewareClient) Plans(ctx context.Context, office string, coverage Coverage) ([]Plan, error) {
	ctx, cancel := context.WithTimeout(ctx, middlewareTimeout)
	defer cancel()
	query := url.Values{"office": {middlewareOffice(office)}, "coverage": {string(coverage)}}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/insurance/plans?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build middleware plans request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+c.secret)
	request.Header.Set("Accept", "application/json")
	response, err := c.client.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("middleware plans request timed out")
		}
		return nil, fmt.Errorf("middleware plans connection failed")
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: middleware plans returned HTTP %d", ErrNoOffice, response.StatusCode)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("middleware plans returned HTTP %d", response.StatusCode)
	}
	var body struct {
		Plans []Plan `json:"plans"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&body); err != nil || body.Plans == nil {
		return nil, fmt.Errorf("middleware plans response is invalid")
	}
	return body.Plans, nil
}
