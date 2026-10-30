package insurance

import (
	"bytes"
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

const (
	middlewareTimeout = 5 * time.Second
	maxCatalogBytes   = 4 << 20
)

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

func officeLocations(routes []access.AbitaOfficeRoute) map[string][]string {
	locations := map[string][]string{}
	for _, route := range routes {
		office := middlewareOffice(route.OfficeKey)
		locations[office] = append(locations[office], route.LocationID)
	}
	return locations
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

func (c *MiddlewareClient) Catalog(ctx context.Context) (Catalog, error) {
	status, raw, err := c.send(ctx, "catalog", http.MethodGet, "/api/insurance/catalog", nil, maxCatalogBytes)
	if err != nil {
		return Catalog{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if status != http.StatusOK {
		return Catalog{}, fmt.Errorf("%w: middleware catalog returned HTTP %d", ErrUnavailable, status)
	}
	catalog, err := ParseCatalog(raw)
	if err != nil {
		return Catalog{}, fmt.Errorf("%w: middleware catalog response is invalid: %w", ErrUnavailable, err)
	}
	return catalog, nil
}

type CatalogError struct {
	Region  string `json:"region"`
	Table   string `json:"table"`
	PlanID  string `json:"planId"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

type ValidationError struct {
	Errors []CatalogError
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("the middleware rejected the insurance list with %d errors", len(e.Errors))
}

func (c *MiddlewareClient) Validate(ctx context.Context, catalog Catalog) error {
	status, raw, err := c.send(ctx, "validate", http.MethodPost, "/api/insurance/catalog/validate", catalog.encode(), 2<<20)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	var verdict struct {
		Valid  *bool          `json:"valid"`
		Errors []CatalogError `json:"errors"`
	}
	if err := json.Unmarshal(raw, &verdict); err != nil || verdict.Valid == nil {
		return fmt.Errorf("%w: middleware validate returned HTTP %d without a verdict", ErrUnavailable, status)
	}
	switch {
	case status == http.StatusOK && *verdict.Valid && len(verdict.Errors) == 0:
		return nil
	case status == http.StatusBadRequest && !*verdict.Valid && len(verdict.Errors) > 0:
		return &ValidationError{Errors: verdict.Errors}
	default:
		return fmt.Errorf("%w: middleware validate returned HTTP %d with an inconsistent verdict", ErrUnavailable, status)
	}
}

func (c *MiddlewareClient) send(ctx context.Context, name, method, path string, body []byte, limit int64) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, middlewareTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("build middleware %s request: %w", name, err)
	}
	request.Header.Set("Authorization", "Bearer "+c.secret)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.client.Do(request)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return 0, nil, fmt.Errorf("middleware %s request timed out", name)
		}
		return 0, nil, fmt.Errorf("middleware %s connection failed", name)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return 0, nil, fmt.Errorf("middleware %s response could not be read", name)
	}
	if int64(len(raw)) > limit {
		return 0, nil, fmt.Errorf("middleware %s response exceeds %d bytes", name, limit)
	}
	return response.StatusCode, raw, nil
}
