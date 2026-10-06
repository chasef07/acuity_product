package insurance_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/insurance"
)

const (
	practiceID = "00000000-0000-0000-0000-000000000010"
	locationID = "00000000-0000-0000-0000-000000000011"
	secret     = "synthetic-middleware-secret"
)

type fakeRoutes struct {
	key string
	err error
}

func (routes fakeRoutes) ReadLocationAbitaOfficeKey(context.Context, access.Identity, string, string) (string, error) {
	return routes.key, routes.err
}

type middlewareCall struct {
	method, path, query, authorization string
}

func fakeMiddleware(t *testing.T, status int, response string) (*httptest.Server, *[]middlewareCall) {
	t.Helper()
	calls := []middlewareCall{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, middlewareCall{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, authorization: r.Header.Get("Authorization")})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if strings.HasPrefix(response, "[") {
			response = fmt.Sprintf(`{"officeId":%q,"coverage":%q,"plans":%s}`, r.URL.Query().Get("office"), r.URL.Query().Get("coverage"), response)
		}
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

func newModule(t *testing.T, routes insurance.Routes, baseURL string, client *http.Client) *insurance.Module {
	t.Helper()
	rules, err := insurance.NewMiddlewareClient(baseURL, secret, client)
	if err != nil {
		t.Fatalf("new middleware client: %v", err)
	}
	module, err := insurance.New(routes, rules)
	if err != nil {
		t.Fatalf("new insurance module: %v", err)
	}
	return module
}

func query(coverage insurance.Coverage) insurance.Query {
	return insurance.Query{Identity: access.Identity{Subject: "staff", EmailVerified: true}, PracticeID: practiceID, LocationID: locationID, Coverage: coverage}
}

func TestPlansMapsTheMiddlewareListForTheLocationOffice(t *testing.T) {
	server, calls := fakeMiddleware(t, http.StatusOK, `{"officeId":"hollywood","coverage":"medical","plans":[
		{"planId":"synthetic-hmo-medical","label":"Synthetic HMO","names":["Synthetic Health HMO"],"carrierCode":"SYN01","carrierId":"car00001","carrierName":"SYNTHETIC CARRIER","outcome":"needs_staff_task","allowedProviders":["Dr. Example"],"requirements":["pcp_referral","staff_verify"],"callerNotice":"Bring your referral.","note":"Referral from PCP","acceptedAt":[]},
		{"planId":"synthetic-medicaid-medical","label":"Synthetic Medicaid","outcome":"not_accepted","acceptedAt":["Sweetwater"]}
	]}`)
	module := newModule(t, fakeRoutes{key: "hollywood"}, server.URL, nil)

	page, err := module.Plans(context.Background(), query(insurance.Medical))
	if err != nil {
		t.Fatalf("plans: %v", err)
	}
	want := insurance.PlansPage{LocationID: locationID, Coverage: insurance.Medical, Plans: []insurance.Plan{
		{PlanID: "synthetic-hmo-medical", Label: "Synthetic HMO", Names: []string{"Synthetic Health HMO"}, CarrierCode: "SYN01", CarrierID: "car00001", CarrierName: "SYNTHETIC CARRIER", Outcome: "needs_staff_task", AllowedProviders: []string{"Dr. Example"}, Requirements: []string{"pcp_referral", "staff_verify"}, CallerNotice: "Bring your referral.", Note: "Referral from PCP", AcceptedAt: []string{}},
		{PlanID: "synthetic-medicaid-medical", Label: "Synthetic Medicaid", Names: []string{}, Outcome: "not_accepted", AllowedProviders: []string{}, Requirements: []string{}, AcceptedAt: []string{"Sweetwater"}},
	}}
	if !reflect.DeepEqual(page, want) {
		t.Fatalf("page = %#v\nwant %#v", page, want)
	}
	encoded, _ := json.Marshal(page)
	if strings.Contains(string(encoded), "null") {
		t.Fatalf("page encodes null lists: %s", encoded)
	}
	if len(*calls) != 1 || (*calls)[0].method != http.MethodGet || (*calls)[0].path != "/api/insurance/plans" ||
		(*calls)[0].query != "coverage=medical&office=hollywood" || (*calls)[0].authorization != "Bearer "+secret {
		t.Fatalf("middleware calls = %#v", *calls)
	}
}

func TestOfficeKeysTranslateToMiddlewareOffices(t *testing.T) {
	for key, office := range map[string]string{"sweetwater-optical": "sweetwater", "sweetwater": "sweetwater", "hollywood": "hollywood", "north-miami-beach-optical": "north_miami_beach_optical", "spring-hill": "spring_hill"} {
		server, calls := fakeMiddleware(t, http.StatusOK, `[]`)
		module := newModule(t, fakeRoutes{key: key}, server.URL, nil)
		if _, err := module.Plans(context.Background(), query(insurance.RoutineVision)); err != nil {
			t.Fatalf("%s plans: %v", key, err)
		}
		if len(*calls) != 1 || (*calls)[0].query != "coverage=routine_vision&office="+office {
			t.Fatalf("%s middleware calls = %#v, want office %s", key, *calls, office)
		}
	}
}

func TestEmptyPlanListIsAnEmptyPage(t *testing.T) {
	server, _ := fakeMiddleware(t, http.StatusOK, `[]`)
	page, err := newModule(t, fakeRoutes{key: "spring-hill"}, server.URL, nil).Plans(context.Background(), query(insurance.RoutineVision))
	if err != nil || page.Plans == nil || len(page.Plans) != 0 {
		t.Fatalf("empty page = %#v, %v", page, err)
	}
}

func TestMiddlewareFailuresAreUnavailable(t *testing.T) {
	for name, scenario := range map[string]struct {
		status   int
		response string
	}{
		"server error":        {http.StatusInternalServerError, `{}`},
		"missing route":       {http.StatusNotFound, `{}`},
		"wrong secret":        {http.StatusUnauthorized, `{}`},
		"invalid json":        {http.StatusOK, `not json`},
		"missing plans":       {http.StatusOK, `{"officeId":"hollywood","coverage":"medical"}`},
		"other office":        {http.StatusOK, `{"officeId":"sweetwater","coverage":"medical","plans":[]}`},
		"other coverage":      {http.StatusOK, `{"officeId":"hollywood","coverage":"routine_vision","plans":[]}`},
		"missing label":       {http.StatusOK, `[{"planId":"p","outcome":"accepted"}]`},
		"question outcome":    {http.StatusOK, `[{"planId":"p","label":"P","outcome":"needs_clarification"}]`},
		"unknown requirement": {http.StatusOK, `[{"planId":"p","label":"P","outcome":"accepted","requirements":["eligibility_call"]}]`},
	} {
		server, _ := fakeMiddleware(t, scenario.status, scenario.response)
		module := newModule(t, fakeRoutes{key: "hollywood"}, server.URL, nil)
		if _, err := module.Plans(context.Background(), query(insurance.Medical)); !errors.Is(err, insurance.ErrUnavailable) || errors.Is(err, insurance.ErrUnknownOffice) {
			t.Errorf("%s plans error = %v, want unavailable", name, err)
		}
	}
}

func TestMiddlewareRejectionMeansTheOfficeIsUnavailable(t *testing.T) {
	server, _ := fakeMiddleware(t, http.StatusBadRequest, `Unknown office`)
	module := newModule(t, fakeRoutes{key: "dev"}, server.URL, nil)
	_, err := module.Plans(context.Background(), query(insurance.Medical))
	if !errors.Is(err, insurance.ErrUnknownOffice) || errors.Is(err, insurance.ErrUnavailable) || errors.Is(err, access.ErrNoOfficeRoute) {
		t.Fatalf("plans error = %v, want unknown office", err)
	}
}

func TestMiddlewareTransportFailuresNameACoarseCause(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(200 * time.Millisecond):
		}
	}))
	defer slow.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := newModule(t, fakeRoutes{key: "hollywood"}, slow.URL, nil).Plans(ctx, query(insurance.Medical))
	if !errors.Is(err, insurance.ErrUnavailable) || !strings.HasSuffix(err.Error(), "middleware plans request timed out") {
		t.Fatalf("timed out plans error = %v", err)
	}

	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	_, err = newModule(t, fakeRoutes{key: "hollywood"}, closed.URL, nil).Plans(context.Background(), query(insurance.Medical))
	if !errors.Is(err, insurance.ErrUnavailable) || !strings.HasSuffix(err.Error(), "middleware plans connection failed") ||
		strings.Contains(err.Error(), closed.URL) || strings.Contains(err.Error(), secret) {
		t.Fatalf("connection failure error = %v", err)
	}
}

func TestLocationMustHaveOneOfficeAndBeAuthorized(t *testing.T) {
	server, calls := fakeMiddleware(t, http.StatusOK, `[]`)
	for _, want := range []error{access.ErrNoOfficeRoute, access.ErrDenied} {
		module := newModule(t, fakeRoutes{err: want}, server.URL, nil)
		if _, err := module.Plans(context.Background(), query(insurance.Medical)); !errors.Is(err, want) {
			t.Errorf("plans error = %v, want %v", err, want)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("middleware called without a single authorized office: %#v", *calls)
	}
}

func TestInvalidQueriesAreRejectedBeforeAnyLookup(t *testing.T) {
	module := newModule(t, fakeRoutes{err: errors.New("lookup must not run")}, "http://middleware.invalid", nil)
	badCoverage := query("dental")
	badLocation := query(insurance.Medical)
	badLocation.LocationID = "not-a-uuid"
	badPractice := query(insurance.Medical)
	badPractice.PracticeID = "not-a-uuid"
	for name, scenario := range map[string]insurance.Query{"coverage": badCoverage, "location": badLocation, "practice": badPractice} {
		if _, err := module.Plans(context.Background(), scenario); !errors.Is(err, insurance.ErrInvalidInput) {
			t.Errorf("%s error = %v, want invalid input", name, err)
		}
	}
}

func TestMiddlewareClientRequiresBaseURLAndSecret(t *testing.T) {
	for _, scenario := range [][2]string{{"", secret}, {"middleware.example", secret}, {"https://middleware.example", ""}, {"https://middleware.example", "   "}} {
		if _, err := insurance.NewMiddlewareClient(scenario[0], scenario[1], nil); err == nil {
			t.Errorf("client accepted base URL %q with secret set=%t", scenario[0], scenario[1] != "")
		}
	}
}
