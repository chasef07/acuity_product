package insurance

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/google/uuid"
)

var (
	ErrInvalidInput = errors.New("invalid insurance input")
	ErrUnavailable  = errors.New("insurance rules are unavailable")
)

type Coverage string

const (
	Medical       Coverage = "medical"
	RoutineVision Coverage = "routine_vision"
)

var outcomes = []string{"accepted", "not_accepted", "needs_staff_task"}
var requirements = []string{"prior_authorization", "pcp_referral", "staff_verify"}

type Plan struct {
	PlanID           string   `json:"planId"`
	Label            string   `json:"label"`
	Names            []string `json:"names"`
	CarrierCode      string   `json:"carrierCode"`
	CarrierID        string   `json:"carrierId"`
	CarrierName      string   `json:"carrierName"`
	Outcome          string   `json:"outcome"`
	AllowedProviders []string `json:"allowedProviders"`
	Requirements     []string `json:"requirements"`
	CallerNotice     string   `json:"callerNotice"`
	Note             string   `json:"note"`
	AcceptedAt       []string `json:"acceptedAt"`
}

type PlansPage struct {
	LocationID string   `json:"locationId"`
	Coverage   Coverage `json:"coverage"`
	Plans      []Plan   `json:"plans"`
}

type Routes interface {
	ReadLocationAbitaOfficeKey(ctx context.Context, identity access.Identity, practiceID, locationID string) (string, error)
}

type Query struct {
	Identity   access.Identity
	PracticeID string
	LocationID string
	Coverage   Coverage
}

type Module struct {
	routes Routes
	rules  *MiddlewareClient
}

func New(routes Routes, rules *MiddlewareClient) (*Module, error) {
	if routes == nil || rules == nil {
		return nil, ErrInvalidInput
	}
	return &Module{routes: routes, rules: rules}, nil
}

func (m *Module) Plans(ctx context.Context, query Query) (PlansPage, error) {
	if query.Coverage != Medical && query.Coverage != RoutineVision || uuid.Validate(query.PracticeID) != nil || uuid.Validate(query.LocationID) != nil {
		return PlansPage{}, ErrInvalidInput
	}
	office, err := m.routes.ReadLocationAbitaOfficeKey(ctx, query.Identity, query.PracticeID, query.LocationID)
	if err != nil {
		return PlansPage{}, err
	}
	plans, err := m.rules.Plans(ctx, office, query.Coverage)
	if errors.Is(err, access.ErrNoOfficeRoute) {
		return PlansPage{}, err
	}
	if err != nil {
		return PlansPage{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	page := PlansPage{LocationID: query.LocationID, Coverage: query.Coverage, Plans: make([]Plan, 0, len(plans))}
	for _, plan := range plans {
		if !knownPlan(plan) {
			return PlansPage{}, fmt.Errorf("%w: plan %q has an unknown outcome or requirement", ErrUnavailable, plan.PlanID)
		}
		plan.Names = nonNil(plan.Names)
		plan.AllowedProviders = nonNil(plan.AllowedProviders)
		plan.Requirements = nonNil(plan.Requirements)
		plan.AcceptedAt = nonNil(plan.AcceptedAt)
		page.Plans = append(page.Plans, plan)
	}
	return page, nil
}

func knownPlan(plan Plan) bool {
	return plan.PlanID != "" && plan.Label != "" && slices.Contains(outcomes, plan.Outcome) &&
		!slices.ContainsFunc(plan.Requirements, func(kind string) bool { return !slices.Contains(requirements, kind) })
}

func nonNil[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}
