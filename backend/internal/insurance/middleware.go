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

	"github.com/chasef07/acuity_product/backend/internal/access"
)

const middlewareTimeout = 5 * time.Second

var middlewareOffices = map[string]string{
	"sweetwater-optical":        "sweetwater",
	"north-miami-beach-optical": "north_miami_beach_optical",
	"spring-hill":               "spring_hill",
	"crystal-river":             "crystal_river",
}

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
	parsed, err := url.Parse(baseURL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || strings.TrimSpace(secret) == "" {
		return nil, fmt.Errorf("middleware base URL and API secret are required")
	}
	if client == nil {
		client = &http.Client{}
	}
	return &MiddlewareClient{baseURL: strings.TrimRight(parsed.String(), "/"), secret: secret, client: client}, nil
}

func (c *MiddlewareClient) Plans(ctx context.Context, office string, coverage Coverage) ([]Plan, error) {
	ctx, cancel := context.WithTimeout(ctx, middlewareTimeout)
	defer cancel()
	office = middlewareOffice(office)
	query := url.Values{"office": {office}, "coverage": {string(coverage)}}
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
	if response.StatusCode == http.StatusBadRequest {
		return nil, fmt.Errorf("%w: middleware plans returned HTTP %d", access.ErrNoOfficeRoute, response.StatusCode)
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, fmt.Errorf("middleware plans returned HTTP %d", response.StatusCode)
	}
	var body struct {
		OfficeID string   `json:"officeId"`
		Coverage Coverage `json:"coverage"`
		Plans    []Plan   `json:"plans"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 2<<20)).Decode(&body); err != nil || body.Plans == nil || body.OfficeID != office || body.Coverage != coverage {
		return nil, fmt.Errorf("middleware plans response is invalid")
	}
	return body.Plans, nil
}
