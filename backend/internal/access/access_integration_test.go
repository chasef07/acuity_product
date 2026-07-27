package access_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestInvitationAcceptanceCreatesAuthorizedMembershipWithoutCredentials(t *testing.T) {
	pool := testdb.Open(t)
	module := access.New(pool, func() time.Time {
		return time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	})

	provisioned, err := module.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "slice-1-integration-test",
		Practices: []access.PracticeProvision{{
			Key:  "abita-eye-group",
			Name: "Abita Eye Group",
			Locations: []access.LocationProvision{{
				Key:  "fixture-location-1",
				Name: "Fixture Location 1",
			}},
			Invitations: []access.InvitationProvision{{
				Key:           "first-admin",
				Email:         "admin@abita.test",
				Role:          access.RoleAdmin,
				LocationScope: access.LocationScopeAll,
				ExpiresAt:     time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC),
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if len(provisioned.Invitations) != 1 || provisioned.Invitations[0].Token == "" {
		t.Fatalf("expected one new invitation credential, got %#v", provisioned.Invitations)
	}

	authorized, err := module.AcceptInvitation(
		context.Background(),
		access.Identity{
			Subject:       "better-auth-user-1",
			Email:         "ADMIN@ABITA.TEST",
			EmailVerified: true,
		},
		provisioned.Invitations[0].Token,
	)
	if err != nil {
		t.Fatalf("accept invitation: %v", err)
	}
	if authorized.Actor.Subject != "better-auth-user-1" {
		t.Fatalf("actor subject = %q", authorized.Actor.Subject)
	}
	if authorized.Practice.Name != "Abita Eye Group" {
		t.Fatalf("practice = %q", authorized.Practice.Name)
	}
	if authorized.Membership.Role != access.RoleAdmin {
		t.Fatalf("role = %q", authorized.Membership.Role)
	}
	if authorized.Membership.LocationScope != access.LocationScopeAll {
		t.Fatalf("location scope = %q", authorized.Membership.LocationScope)
	}
	if len(authorized.Locations) != 1 || authorized.Locations[0].Name != "Fixture Location 1" {
		t.Fatalf("locations = %#v", authorized.Locations)
	}

	replayed, err := module.AcceptInvitation(
		context.Background(),
		access.Identity{
			Subject:       "better-auth-user-1",
			Email:         "admin@abita.test",
			EmailVerified: true,
		},
		provisioned.Invitations[0].Token,
	)
	if err != nil {
		t.Fatalf("replay invitation: %v", err)
	}
	if replayed.Membership.ID != authorized.Membership.ID {
		t.Fatalf("replay created a different membership: %s != %s", replayed.Membership.ID, authorized.Membership.ID)
	}
}

func TestProvisionRejectsInvitationThatIsAlreadyExpired(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	module := access.New(pool, func() time.Time { return now })

	_, err := module.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "slice-1-integration-test",
		Practices: []access.PracticeProvision{{
			Key:  "abita-eye-group",
			Name: "Abita Eye Group",
			Locations: []access.LocationProvision{{
				Key:  "fixture-location-1",
				Name: "Fixture Location 1",
			}},
			Invitations: []access.InvitationProvision{{
				Key:           "expired-admin",
				Email:         "expired@abita.test",
				Role:          access.RoleAdmin,
				LocationScope: access.LocationScopeAll,
				ExpiresAt:     now.Add(-time.Minute),
			}},
		}},
	})
	if !errors.Is(err, access.ErrInvalidInput) {
		t.Fatalf("expired provisioning error = %v", err)
	}
}

func TestLocationScopeIsDynamicForAdminAndAllButExplicitForSelectedStaff(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	module := access.New(pool, func() time.Time { return now })

	initial, err := module.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "slice-1-integration-test",
		Practices: []access.PracticeProvision{{
			Key:  "abita-eye-group",
			Name: "Abita Eye Group",
			Locations: []access.LocationProvision{
				{Key: "fixture-location-1", Name: "Fixture Location 1"},
				{Key: "fixture-location-2", Name: "Fixture Location 2"},
			},
			Invitations: []access.InvitationProvision{
				{
					Key:           "admin",
					Email:         "admin@abita.test",
					Role:          access.RoleAdmin,
					LocationScope: access.LocationScopeAll,
					ExpiresAt:     now.Add(24 * time.Hour),
				},
				{
					Key:           "all-staff",
					Email:         "all@abita.test",
					Role:          access.RoleStaff,
					LocationScope: access.LocationScopeAll,
					ExpiresAt:     now.Add(24 * time.Hour),
				},
				{
					Key:                  "selected-staff",
					Email:                "selected@abita.test",
					Role:                 access.RoleStaff,
					LocationScope:        access.LocationScopeSelected,
					SelectedLocationKeys: []string{"fixture-location-1"},
					ExpiresAt:            now.Add(24 * time.Hour),
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("provision initial scope: %v", err)
	}

	identities := []access.Identity{
		{Subject: "admin-subject", Email: "admin@abita.test", EmailVerified: true},
		{Subject: "all-subject", Email: "all@abita.test", EmailVerified: true},
		{Subject: "selected-subject", Email: "selected@abita.test", EmailVerified: true},
	}
	var practiceID string
	for index, identity := range identities {
		authorized, err := module.AcceptInvitation(context.Background(), identity, initial.Invitations[index].Token)
		if err != nil {
			t.Fatalf("accept invitation %d: %v", index, err)
		}
		practiceID = authorized.Practice.ID
	}

	_, err = module.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "slice-1-integration-test",
		Practices: []access.PracticeProvision{{
			Key:  "abita-eye-group",
			Name: "Abita Eye Group",
			Locations: []access.LocationProvision{
				{Key: "fixture-location-1", Name: "Fixture Location 1"},
				{Key: "fixture-location-2", Name: "Fixture Location 2"},
				{Key: "fixture-location-7", Name: "Fixture Location 7"},
			},
			Invitations: []access.InvitationProvision{
				{
					Key:           "admin",
					Email:         "admin@abita.test",
					Role:          access.RoleAdmin,
					LocationScope: access.LocationScopeAll,
					ExpiresAt:     now.Add(24 * time.Hour),
				},
				{
					Key:           "all-staff",
					Email:         "all@abita.test",
					Role:          access.RoleStaff,
					LocationScope: access.LocationScopeAll,
					ExpiresAt:     now.Add(24 * time.Hour),
				},
				{
					Key:                  "selected-staff",
					Email:                "selected@abita.test",
					Role:                 access.RoleStaff,
					LocationScope:        access.LocationScopeSelected,
					SelectedLocationKeys: []string{"fixture-location-1"},
					ExpiresAt:            now.Add(24 * time.Hour),
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("provision seventh location: %v", err)
	}

	admin, err := module.ResolveActor(context.Background(), identities[0], practiceID, "")
	if err != nil {
		t.Fatalf("resolve Admin: %v", err)
	}
	allStaff, err := module.ResolveActor(context.Background(), identities[1], practiceID, "")
	if err != nil {
		t.Fatalf("resolve ALL Staff: %v", err)
	}
	selectedStaff, err := module.ResolveActor(context.Background(), identities[2], practiceID, "")
	if err != nil {
		t.Fatalf("resolve SELECTED Staff: %v", err)
	}

	if len(admin.Locations) != 3 {
		t.Fatalf("Admin locations = %d, want 3", len(admin.Locations))
	}
	if len(allStaff.Locations) != 3 {
		t.Fatalf("ALL Staff locations = %d, want 3", len(allStaff.Locations))
	}
	if len(selectedStaff.Locations) != 1 || selectedStaff.Locations[0].Name != "Fixture Location 1" {
		t.Fatalf("SELECTED Staff locations = %#v", selectedStaff.Locations)
	}
}

func TestPlatformOperatorMutationRequiresPracticeScopedSupportModeAndKeepsRealActor(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	module := access.New(pool, func() time.Time { return now })
	operator := access.Identity{
		Subject:       "founder-subject",
		Email:         "founder@acuity.test",
		EmailVerified: true,
	}

	_, err := module.Provision(context.Background(), access.Provisioning{
		Environment:       "test",
		RequestedBy:       "slice-1-integration-test",
		PlatformOperators: []string{"founder@acuity.test"},
		Practices: []access.PracticeProvision{
			{
				Key:       "abita-eye-group",
				Name:      "Abita Eye Group",
				Locations: []access.LocationProvision{{Key: "fixture-a", Name: "Fixture A"}},
			},
			{
				Key:       "another-practice",
				Name:      "Another Fixture Practice",
				Locations: []access.LocationProvision{{Key: "fixture-b", Name: "Fixture B"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("provision operator scope: %v", err)
	}

	discovery, err := module.DiscoverActor(context.Background(), operator)
	if err != nil {
		t.Fatalf("discover practices: %v", err)
	}
	if !discovery.PlatformOperator || len(discovery.Practices) != 2 {
		t.Fatalf("operator discovery = %#v", discovery)
	}
	practiceA := discovery.Practices[0]
	practiceB := discovery.Practices[1]

	_, err = module.AddLocation(context.Background(), access.AddLocationCommand{
		Identity:   operator,
		PracticeID: practiceA.ID,
		Key:        "fixture-a-2",
		Name:       "Fixture A 2",
	})
	if !errors.Is(err, access.ErrSupportRequired) {
		t.Fatalf("mutation without Support Mode error = %v", err)
	}

	support, err := module.EnterSupportMode(context.Background(), access.EnterSupportModeCommand{
		Identity:   operator,
		PracticeID: practiceA.ID,
		Reason:     "Validate the Slice 1 operator workflow",
		Duration:   30 * time.Minute,
	})
	if err != nil {
		t.Fatalf("enter Support Mode: %v", err)
	}

	_, err = module.AddLocation(context.Background(), access.AddLocationCommand{
		Identity:         operator,
		PracticeID:       practiceB.ID,
		SupportSessionID: support.ID,
		Key:              "fixture-b-2",
		Name:             "Fixture B 2",
	})
	if !errors.Is(err, access.ErrSupportPracticeMismatch) {
		t.Fatalf("cross-Practice Support Mode error = %v", err)
	}

	mutation, err := module.AddLocation(context.Background(), access.AddLocationCommand{
		Identity:         operator,
		PracticeID:       practiceA.ID,
		SupportSessionID: support.ID,
		Key:              "fixture-a-2",
		Name:             "Fixture A 2",
	})
	if err != nil {
		t.Fatalf("add Location in Support Mode: %v", err)
	}
	if mutation.Audit.ActorSubject != operator.Subject ||
		mutation.Audit.Reason != "Validate the Slice 1 operator workflow" ||
		mutation.Audit.SupportSessionID != support.ID {
		t.Fatalf("mutation audit = %#v", mutation.Audit)
	}

	now = now.Add(31 * time.Minute)
	_, err = module.AddLocation(context.Background(), access.AddLocationCommand{
		Identity:         operator,
		PracticeID:       practiceA.ID,
		SupportSessionID: support.ID,
		Key:              "fixture-a-3",
		Name:             "Fixture A 3",
	})
	if !errors.Is(err, access.ErrSupportExpired) {
		t.Fatalf("expired Support Mode error = %v", err)
	}

	stillVisible, err := module.ResolveActor(context.Background(), operator, practiceB.ID, "")
	if err != nil {
		t.Fatalf("global discovery after Support Mode expiry: %v", err)
	}
	if !stillVisible.PlatformOperator || stillVisible.Practice.ID != practiceB.ID {
		t.Fatalf("operator visibility = %#v", stillVisible)
	}
}

func TestInvitationEligibilityDiscoveryAndRequestedLocationStayInsideAccess(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	module := access.New(pool, func() time.Time { return now })

	provisioned, err := module.Provision(context.Background(), access.Provisioning{
		Environment:       "test",
		RequestedBy:       "slice-1-integration-test",
		PlatformOperators: []string{"founder@acuity.test"},
		Practices: []access.PracticeProvision{{
			Key:  "abita-eye-group",
			Name: "Abita Eye Group",
			Locations: []access.LocationProvision{
				{Key: "fixture-location-1", Name: "Fixture Location 1"},
				{Key: "fixture-location-2", Name: "Fixture Location 2"},
			},
			Invitations: []access.InvitationProvision{{
				Key:                  "selected-staff",
				Email:                "selected@abita.test",
				Role:                 access.RoleStaff,
				LocationScope:        access.LocationScopeSelected,
				SelectedLocationKeys: []string{"fixture-location-1"},
				ExpiresAt:            now.Add(24 * time.Hour),
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision invitation discovery: %v", err)
	}

	preview, err := module.InspectInvitation(context.Background(), access.InvitationInspection{
		Token: provisioned.Invitations[0].Token,
		Email: "SELECTED@ABITA.TEST",
	})
	if err != nil {
		t.Fatalf("inspect customer invitation: %v", err)
	}
	if preview.Kind != access.InvitationKindPractice ||
		preview.PracticeName != "Abita Eye Group" ||
		len(preview.Locations) != 1 {
		t.Fatalf("invitation preview = %#v", preview)
	}
	_, err = module.InspectInvitation(context.Background(), access.InvitationInspection{
		Token: provisioned.Invitations[0].Token,
		Email: "somebody-else@abita.test",
	})
	if !errors.Is(err, access.ErrDenied) {
		t.Fatalf("wrong-email invitation inspection error = %v", err)
	}

	operatorEligibility, err := module.InspectInvitation(context.Background(), access.InvitationInspection{
		Email: "FOUNDER@ACUITY.TEST",
	})
	if err != nil {
		t.Fatalf("inspect Platform Operator eligibility: %v", err)
	}
	if operatorEligibility.Kind != access.InvitationKindPlatformOperator {
		t.Fatalf("operator eligibility = %#v", operatorEligibility)
	}

	identity := access.Identity{
		Subject:       "selected-subject",
		Email:         "selected@abita.test",
		EmailVerified: true,
	}
	accepted, err := module.AcceptInvitation(context.Background(), identity, provisioned.Invitations[0].Token)
	if err != nil {
		t.Fatalf("accept selected invitation: %v", err)
	}
	discovery, err := module.DiscoverActor(context.Background(), identity)
	if err != nil {
		t.Fatalf("discover member access: %v", err)
	}
	if discovery.PlatformOperator ||
		len(discovery.Practices) != 1 ||
		discovery.Practices[0].Membership == nil ||
		discovery.Practices[0].Membership.ID != accepted.Membership.ID {
		t.Fatalf("member discovery = %#v", discovery)
	}

	_, err = module.ResolveActor(
		context.Background(),
		identity,
		accepted.Practice.ID,
		accepted.Locations[0].ID,
	)
	if err != nil {
		t.Fatalf("resolve selected Location: %v", err)
	}

	var unauthorizedLocationID string
	operatorDiscovery, err := module.DiscoverActor(context.Background(), access.Identity{
		Subject:       "founder-subject",
		Email:         "founder@acuity.test",
		EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("discover operator Locations: %v", err)
	}
	for _, location := range operatorDiscovery.Practices[0].Locations {
		if location.ID != accepted.Locations[0].ID {
			unauthorizedLocationID = location.ID
		}
	}
	_, err = module.ResolveActor(
		context.Background(),
		identity,
		accepted.Practice.ID,
		unauthorizedLocationID,
	)
	if !errors.Is(err, access.ErrDenied) {
		t.Fatalf("cross-Location resolution error = %v", err)
	}
}

func TestPlatformOperatorPrecedenceFollowsBoundSubjectAndFailsClosedOnConflict(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	module := access.New(pool, func() time.Time { return now })
	if _, err := module.Provision(context.Background(), access.Provisioning{
		Environment:       "test",
		RequestedBy:       "operator-identity-test",
		PlatformOperators: []string{"founder@acuity.test"},
		Practices: []access.PracticeProvision{{
			Key:  "operator-identity-practice",
			Name: "Operator Identity Practice",
		}},
	}); err != nil {
		t.Fatalf("provision operator identity: %v", err)
	}
	bound := access.Identity{
		Subject:       "founder-subject",
		Email:         "founder@acuity.test",
		EmailVerified: true,
	}
	discovery, err := module.DiscoverActor(context.Background(), bound)
	if err != nil || !discovery.PlatformOperator {
		t.Fatalf("bind configured operator: discovery=%#v err=%v", discovery, err)
	}
	changedEmail := bound
	changedEmail.Email = "founder+changed@acuity.test"
	discovery, err = module.DiscoverActor(context.Background(), changedEmail)
	if err != nil || !discovery.PlatformOperator {
		t.Fatalf("resolve bound operator by subject: discovery=%#v err=%v", discovery, err)
	}
	conflictingSubject := bound
	conflictingSubject.Subject = "different-subject"
	if _, err := module.DiscoverActor(context.Background(), conflictingSubject); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("conflicting operator identity error = %v, want denied", err)
	}
}

func TestInvitationAndMembershipRevocationTakeEffectOnNextResolutionAndAreAudited(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	module := access.New(pool, func() time.Time { return now })
	ctx := context.Background()
	operator := access.Identity{
		Subject:       "founder-subject",
		Email:         "founder@acuity.test",
		EmailVerified: true,
	}

	provisioned, err := module.Provision(ctx, access.Provisioning{
		Environment:       "test",
		RequestedBy:       "slice-1-revocation-test",
		PlatformOperators: []string{operator.Email},
		Practices: []access.PracticeProvision{{
			Key:       "abita-eye-group",
			Name:      "Abita Eye Group",
			Locations: []access.LocationProvision{{Key: "fixture-1", Name: "Fixture Location 1"}},
			Invitations: []access.InvitationProvision{
				{
					Key:           "pending-staff",
					Email:         "pending@abita.test",
					Role:          access.RoleStaff,
					LocationScope: access.LocationScopeAll,
					ExpiresAt:     now.Add(24 * time.Hour),
				},
				{
					Key:           "active-staff",
					Email:         "active@abita.test",
					Role:          access.RoleStaff,
					LocationScope: access.LocationScopeAll,
					ExpiresAt:     now.Add(24 * time.Hour),
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("provision revocation fixture: %v", err)
	}
	discovery, err := module.DiscoverActor(ctx, operator)
	if err != nil {
		t.Fatalf("discover operator: %v", err)
	}
	practiceID := discovery.Practices[0].ID
	support, err := module.EnterSupportMode(ctx, access.EnterSupportModeCommand{
		Identity:   operator,
		PracticeID: practiceID,
		Reason:     "Revoke fixture access",
		Duration:   time.Hour,
	})
	if err != nil {
		t.Fatalf("enter Support Mode: %v", err)
	}

	if err := module.RevokeInvitation(ctx, access.RevokeInvitationCommand{
		Identity:         operator,
		PracticeID:       practiceID,
		SupportSessionID: support.ID,
		InvitationID:     provisioned.Invitations[0].ID,
	}); err != nil {
		t.Fatalf("revoke invitation: %v", err)
	}
	_, err = module.AcceptInvitation(ctx, access.Identity{
		Subject:       "pending-subject",
		Email:         "pending@abita.test",
		EmailVerified: true,
	}, provisioned.Invitations[0].Token)
	if !errors.Is(err, access.ErrInvitationRevoked) {
		t.Fatalf("accept revoked invitation error = %v", err)
	}

	memberIdentity := access.Identity{
		Subject:       "active-subject",
		Email:         "active@abita.test",
		EmailVerified: true,
	}
	authorization, err := module.AcceptInvitation(ctx, memberIdentity, provisioned.Invitations[1].Token)
	if err != nil {
		t.Fatalf("accept active invitation: %v", err)
	}
	if err := module.RevokeMembership(ctx, access.RevokeMembershipCommand{
		Identity:         operator,
		PracticeID:       practiceID,
		SupportSessionID: support.ID,
		MembershipID:     authorization.Membership.ID,
	}); err != nil {
		t.Fatalf("revoke membership: %v", err)
	}
	if _, err := module.ResolveActor(ctx, memberIdentity, practiceID, ""); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("resolve revoked membership error = %v", err)
	}

	events, err := module.AuditTrail(ctx, operator, practiceID)
	if err != nil {
		t.Fatalf("load audit trail: %v", err)
	}
	actions := map[string]bool{}
	for _, event := range events {
		actions[event.Action] = true
		if event.Action == "invitation.revoked" || event.Action == "membership.revoked" {
			if event.ActorSubject != operator.Subject ||
				event.SupportSessionID != support.ID ||
				event.Reason != "Revoke fixture access" {
				t.Fatalf("revocation audit = %#v", event)
			}
		}
	}
	if !actions["invitation.revoked"] || !actions["membership.revoked"] {
		t.Fatalf("audit actions = %#v", actions)
	}
}
