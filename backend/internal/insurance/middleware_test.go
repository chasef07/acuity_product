package insurance_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/insurance"
)

const secret = "synthetic-middleware-secret"

func TestMiddlewareClientRequiresBaseURLAndSecret(t *testing.T) {
	for _, scenario := range [][2]string{{"", secret}, {"middleware.example", secret}, {"https://middleware.example", ""}, {"https://middleware.example", "   "}} {
		if _, err := insurance.NewMiddlewareClient(scenario[0], scenario[1], nil); err == nil {
			t.Errorf("client accepted base URL %q with secret set=%t", scenario[0], scenario[1] != "")
		}
	}
}

func middlewareClient(t *testing.T, handler http.HandlerFunc) *insurance.MiddlewareClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := insurance.NewMiddlewareClient(server.URL, secret, nil)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func respond(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

func TestValidateSendsTheCatalogAndAcceptsAValidVerdict(t *testing.T) {
	var method, path, authorization string
	var received insurance.Catalog
	client := middlewareClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, path, authorization = r.Method, r.URL.Path, r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		received, _ = insurance.ParseCatalog(body)
		respond(http.StatusOK, `{"valid":true}`)(w, r)
	})
	if err := client.Validate(context.Background(), syntheticCatalog()); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if method != http.MethodPost || path != "/api/insurance/catalog/validate" || authorization != "Bearer "+secret || !reflect.DeepEqual(received, syntheticCatalog()) {
		t.Fatalf("request = %s %s %q, catalog %+v", method, path, authorization, received)
	}
}

func TestValidateReturnsEveryMiddlewareErrorVerbatim(t *testing.T) {
	client := middlewareClient(t, respond(http.StatusBadRequest, `{"valid":false,"errors":[
		{"region":"spring_hill_crystal_river","table":"spring_hill","planId":"north-hmo","rule":"unknown_doctor_column","message":"Dr. Nobody is not a doctor column."},
		{"region":"","table":"","planId":"","rule":"unknown_carrier","message":"car99999 is not a carrier."}]}`))
	var rejected *insurance.ValidationError
	err := client.Validate(context.Background(), syntheticCatalog())
	if !errors.As(err, &rejected) || errors.Is(err, insurance.ErrUnavailable) {
		t.Fatalf("validate error = %v, want a validation error", err)
	}
	want := []insurance.CatalogError{
		{Region: "spring_hill_crystal_river", Table: "spring_hill", PlanID: "north-hmo", Rule: "unknown_doctor_column", Message: "Dr. Nobody is not a doctor column."},
		{Rule: "unknown_carrier", Message: "car99999 is not a carrier."},
	}
	if !reflect.DeepEqual(rejected.Errors, want) {
		t.Fatalf("errors = %+v", rejected.Errors)
	}
}

func TestValidateIsUnavailableWithoutAClearVerdict(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"server error":           respond(http.StatusInternalServerError, `{"valid":false,"errors":[{"rule":"x","message":"y"}]}`),
		"wrong secret":           respond(http.StatusUnauthorized, `{}`),
		"route not deployed":     respond(http.StatusNotFound, `404 page not found`),
		"rejection with no list": respond(http.StatusBadRequest, `{"valid":false,"errors":[]}`),
		"ok but invalid":         respond(http.StatusOK, `{"valid":false,"errors":[{"rule":"x","message":"y"}]}`),
		"no verdict":             respond(http.StatusOK, `{}`),
		"not json":               respond(http.StatusOK, `ok`),
	} {
		var rejected *insurance.ValidationError
		err := middlewareClient(t, handler).Validate(context.Background(), syntheticCatalog())
		if !errors.Is(err, insurance.ErrUnavailable) || errors.As(err, &rejected) {
			t.Errorf("%s: validate error = %v, want unavailable", name, err)
		}
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	client, err := insurance.NewMiddlewareClient(closed.URL, secret, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Validate(context.Background(), syntheticCatalog()); !errors.Is(err, insurance.ErrUnavailable) {
		t.Fatalf("unreachable validate error = %v", err)
	}
}

func TestCatalogReadsTheMiddlewareExportStrictly(t *testing.T) {
	export, err := os.ReadFile("testdata/export.json")
	if err != nil {
		t.Fatal(err)
	}
	var path string
	client := middlewareClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		respond(http.StatusOK, string(export))(w, r)
	})
	catalog, err := client.Catalog(context.Background())
	if err != nil || path != "/api/insurance/catalog" {
		t.Fatalf("catalog: path %s, %v", path, err)
	}
	if encoded, _ := json.Marshal(catalog); string(encoded)+"\n" != string(export) {
		t.Fatal("catalog read changed the export")
	}
	for name, handler := range map[string]http.HandlerFunc{
		"unknown field": respond(http.StatusOK, `{"carriers":[],"regions":[],"revision":"1"}`),
		"server error":  respond(http.StatusInternalServerError, `{}`),
		"rejected":      respond(http.StatusBadRequest, `{}`),
	} {
		if _, err := middlewareClient(t, handler).Catalog(context.Background()); !errors.Is(err, insurance.ErrUnavailable) {
			t.Errorf("%s: catalog error = %v, want unavailable", name, err)
		}
	}
}
