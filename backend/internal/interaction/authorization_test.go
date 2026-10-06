package interaction

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
)

func TestAudiencePermitsOnlyItsRoles(t *testing.T) {
	staff := access.Authorization{Membership: access.Membership{Role: access.RoleStaff}}
	admin := access.Authorization{Membership: access.Membership{Role: access.RoleAdmin}}
	operator := access.Authorization{PlatformOperator: true}
	for _, c := range []struct {
		name          string
		who           audience
		authorization access.Authorization
		want          bool
	}{
		{"staff reads staff scope", audienceStaff, staff, true},
		{"staff cannot read admin scope", audienceAdmin, staff, false},
		{"admin reads admin scope", audienceAdmin, admin, true},
		{"operator reads admin scope", audienceAdmin, operator, true},
		{"admin cannot read operator scope", audienceOperator, admin, false},
		{"operator reads operator scope", audienceOperator, operator, true},
	} {
		if got := c.who.permits(c.authorization); got != c.want {
			t.Errorf("%s = %t, want %t", c.name, got, c.want)
		}
	}
}

func TestMissingDependenciesAreUnavailableNotInvalidInput(t *testing.T) {
	ctx := context.Background()
	module := New(nil, nil, func() time.Time { return time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC) })
	identity := access.Identity{Subject: "synthetic", Email: "synthetic@example.test", EmailVerified: true}
	practice := "00000000-0000-0000-0000-000000000001"
	interaction := "00000000-0000-0000-0000-000000000002"
	calls := map[string]error{}
	_, calls["read"] = module.Read(ctx, identity, interaction)
	_, calls["analytics"] = module.QueryAnalytics(ctx, QueryAnalyticsCommand{Identity: identity, PracticeID: practice, Range: AnalyticsRange7Days})
	_, calls["agent calls"] = module.QueryAgentCalls(ctx, QueryAgentCallsCommand{QueryAnalyticsCommand: QueryAnalyticsCommand{Identity: identity, PracticeID: practice, Range: AnalyticsRange7Days}})
	_, calls["costs"] = module.QueryCostAnalytics(ctx, QueryCostAnalyticsCommand{Identity: identity, PracticeID: practice, Range: AnalyticsRange7Days, TimeZone: "UTC"})
	_, calls["bookings"] = module.QueryBookingAnalytics(ctx, QueryBookingAnalyticsCommand{Identity: identity, PracticeID: practice, Days: 7, TimeZone: "UTC"})
	_, calls["flag"] = module.FlagAgentCallIssue(ctx, identity, interaction, AgentCallIssueOther)
	_, calls["review"] = module.ReviewCallIssue(ctx, identity, interaction, CallIssueConfirmed)
	_, calls["tags"] = module.OperatorManualTags(ctx, identity, interaction, nil)
	_, calls["recover"] = module.RecoverSourceClock(ctx, identity, interaction)
	calls["retire"] = module.RetireLegacySummary(ctx, identity, interaction)
	_, calls["worker"] = module.ProcessNextReceipt(ctx)
	for name, err := range calls {
		if !errors.Is(err, errUnavailable) {
			t.Errorf("%s error = %v, want unavailable", name, err)
		}
	}
}

func TestReportingZoneRejectsAmbiguousZones(t *testing.T) {
	for name, want := range map[string]bool{"": false, "Local": false, "Not/AZone": false, "UTC": true, "America/New_York": true} {
		if _, got := reportingZone(name); got != want {
			t.Errorf("reportingZone(%q) = %t, want %t", name, got, want)
		}
	}
}
