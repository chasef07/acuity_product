package access_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
)

func TestAccessGrantActivatesSelectedMembershipOnVerifiedEmailDiscovery(t *testing.T) {
	pool := testdb.Open(t)
	current := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	module := access.New(pool, func() time.Time { return current })

	provisioning := access.Provisioning{
		Environment: "test",
		RequestedBy: "google-access-test",
		Practices: []access.PracticeProvision{{
			Key:  "abita-eye-group",
			Name: "Abita Eye Group",
			Locations: []access.LocationProvision{
				{Key: "hollywood", Name: "Hollywood"},
				{Key: "sweetwater", Name: "Sweetwater"},
			},
			AccessGrants: []access.AccessGrantProvision{{
				Key:                  "staff-google-access",
				Email:                "staff@abita.test",
				Role:                 access.RoleStaff,
				LocationScope:        access.LocationScopeSelected,
				SelectedLocationKeys: []string{"sweetwater"},
			}},
		}},
	}
	_, err := module.Provision(context.Background(), provisioning)
	if err != nil {
		t.Fatalf("provision email access: %v", err)
	}
	current = current.AddDate(10, 0, 0)

	preview, err := module.InspectSignUpEligibility(context.Background(), "STAFF@ABITA.TEST")
	if err != nil {
		t.Fatalf("inspect provisioned email: %v", err)
	}
	if preview.Kind != access.SignUpEligibilityAccessGrant || preview.Email != "staff@abita.test" {
		t.Fatalf("provisioned email preview = %#v", preview)
	}

	identity := access.Identity{
		Subject:       "google-subject-1",
		Email:         "STAFF@ABITA.TEST",
		EmailVerified: true,
	}
	discovery, err := module.DiscoverActor(context.Background(), identity)
	if err != nil {
		t.Fatalf("activate provisioned email: %v", err)
	}
	if len(discovery.Practices) != 1 || discovery.Practices[0].Membership == nil {
		t.Fatalf("discovery = %#v", discovery)
	}
	membership := discovery.Practices[0].Membership
	if membership.Role != access.RoleStaff || membership.LocationScope != access.LocationScopeSelected {
		t.Fatalf("membership = %#v", membership)
	}
	if locations := discovery.Practices[0].Locations; len(locations) != 1 || locations[0].Name != "Sweetwater" {
		t.Fatalf("locations = %#v", locations)
	}
	var claimedBy string
	if err := pool.QueryRow(context.Background(), `
		SELECT access_grant.claimed_by_subject
		FROM access_grants access_grant
		JOIN access_memberships membership ON membership.access_grant_id = access_grant.id
		WHERE access_grant.email = 'staff@abita.test'
	`).Scan(&claimedBy); err != nil {
		t.Fatalf("read claimed Access Grant origin: %v", err)
	}
	if claimedBy != identity.Subject {
		t.Fatalf("claimed Access Grant subject = %q", claimedBy)
	}

	replayed, err := module.DiscoverActor(context.Background(), identity)
	if err != nil {
		t.Fatalf("rediscover provisioned email: %v", err)
	}
	if replayed.Practices[0].Membership.ID != membership.ID {
		t.Fatalf("rediscovery changed Membership: %s != %s", replayed.Practices[0].Membership.ID, membership.ID)
	}
	reconciled, err := module.Provision(context.Background(), provisioning)
	if err != nil {
		t.Fatalf("reconcile unchanged Access Grant: %v", err)
	}
	if reconciled.AccessGrantCount != 0 {
		t.Fatalf("unchanged reconciliation created %d Access Grants", reconciled.AccessGrantCount)
	}
	changed := provisioning
	changed.Practices = append([]access.PracticeProvision(nil), provisioning.Practices...)
	changed.Practices[0].AccessGrants = []access.AccessGrantProvision{{
		Key:                  "staff-google-access",
		Email:                "staff@abita.test",
		Role:                 access.RoleStaff,
		LocationScope:        access.LocationScopeSelected,
		SelectedLocationKeys: []string{"hollywood"},
	}}
	if _, err := module.Provision(context.Background(), changed); !errors.Is(err, access.ErrInvalidInput) {
		t.Fatalf("changed Access Grant reconciliation error = %v", err)
	}
}

func TestAccessGrantRevocationDeniesSignUpAndIsAudited(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	module := access.New(pool, func() time.Time { return now })
	operator := access.Identity{
		Subject: "operator-subject", Email: "operator@acuity.test", EmailVerified: true,
	}
	if _, err := module.Provision(context.Background(), access.Provisioning{
		Environment: "test", RequestedBy: "access-grant-revocation-test",
		PlatformOperators: []string{operator.Email},
		Practices: []access.PracticeProvision{{
			Key: "abita-eye-group", Name: "Abita Eye Group",
			Locations: []access.LocationProvision{{Key: "sweetwater", Name: "Sweetwater"}},
			AccessGrants: []access.AccessGrantProvision{{
				Key: "pending-staff", Email: "pending@abita.test",
				Role: access.RoleStaff, LocationScope: access.LocationScopeAll,
			}},
		}},
	}); err != nil {
		t.Fatalf("provision revocable Access Grant: %v", err)
	}
	discovery, err := module.DiscoverActor(context.Background(), operator)
	if err != nil {
		t.Fatalf("discover Platform Operator: %v", err)
	}
	var grantID string
	if err := pool.QueryRow(context.Background(), `
		SELECT id::text FROM access_grants WHERE email = 'pending@abita.test'
	`).Scan(&grantID); err != nil {
		t.Fatalf("read pending Access Grant: %v", err)
	}
	command := access.RevokeAccessGrantCommand{
		Identity: operator, PracticeID: discovery.Practices[0].ID, AccessGrantID: grantID,
	}
	if err := module.RevokeAccessGrant(context.Background(), command); err != nil {
		t.Fatalf("revoke Access Grant: %v", err)
	}
	if err := module.RevokeAccessGrant(context.Background(), command); err != nil {
		t.Fatalf("repeat Access Grant revocation: %v", err)
	}
	if _, err := module.InspectSignUpEligibility(context.Background(), "pending@abita.test"); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("revoked Access Grant eligibility error = %v", err)
	}
	var auditCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM access_audit_events
		WHERE action = 'access_grant.revoked' AND details->>'accessGrantId' = $1
	`, grantID).Scan(&auditCount); err != nil {
		t.Fatalf("count Access Grant revocation audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("Access Grant revocation audit count = %d, want 1", auditCount)
	}
}

func TestProvisioningOwnsAbitaOfficeToLocationRoutes(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	module := access.New(pool, func() time.Time { return now })
	provision := func(routeLocation string) {
		t.Helper()
		locations := []access.LocationProvision{
			{Key: "office-1", Name: "Office 1"},
			{Key: "office-2", Name: "Office 2"},
		}
		switch routeLocation {
		case "office-1":
			locations[0].AbitaOfficeKeys = []string{"spring-hill"}
		case "office-2":
			locations[1].AbitaOfficeKeys = []string{"spring-hill"}
		}
		if _, err := module.Provision(
			context.Background(),
			access.Provisioning{
				Environment: "test",
				RequestedBy: "slice-4-access-test",
				Practices: []access.PracticeProvision{{
					Key:       "abita-eye-group",
					Name:      "Abita Eye Group",
					Locations: locations,
				}},
			},
		); err != nil {
			t.Fatalf("provision Abita office route: %v", err)
		}
	}
	provision("office-1")

	var practiceID string
	if err := pool.QueryRow(context.Background(), `
		SELECT id::text
		FROM access_practices
		WHERE provisioning_key = 'abita-eye-group'
	`).Scan(&practiceID); err != nil {
		t.Fatalf("load Abita route Practice: %v", err)
	}
	service := access.ServiceIdentity{
		Subject:       "abita-route-test",
		PracticeID:    practiceID,
		LocationScope: access.LocationScopeAll,
		Capabilities:  []access.ServiceCapability{access.ServiceCapabilityCreateTask},
	}
	authorize := func() (access.ServiceAuthorization, error) {
		t.Helper()
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatalf("begin Abita route authorization: %v", err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		return module.LockServiceAuthorization(
			context.Background(),
			tx,
			service,
			"spring-hill",
			access.ServiceCapabilityCreateTask,
		)
	}
	first, err := authorize()
	if err != nil {
		t.Fatalf("authorize first Abita route: %v", err)
	}
	var firstLocationID string
	if err := pool.QueryRow(context.Background(), `
		SELECT id::text
		FROM access_locations
		WHERE practice_id = $1 AND provisioning_key = 'office-1'
	`, practiceID).Scan(&firstLocationID); err != nil {
		t.Fatalf("load first Abita route Location: %v", err)
	}
	if first.LocationID != firstLocationID {
		t.Fatalf(
			"first Abita route Location = %q, want %q",
			first.LocationID,
			firstLocationID,
		)
	}

	provision("office-2")
	second, err := authorize()
	if err != nil {
		t.Fatalf("authorize moved Abita route: %v", err)
	}
	if second.LocationID == first.LocationID {
		t.Fatalf("moved Abita route retained Location %q", second.LocationID)
	}

	provision("")
	if _, err := authorize(); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("removed Abita route error = %v, want denied", err)
	}
}

func TestProvisioningRoutesMultipleOfficesToOneOperationalLocation(t *testing.T) {
	pool := testdb.Open(t)
	module := access.New(pool, func() time.Time {
		return time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	})

	if _, err := module.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "operational-location-test",
		Practices: []access.PracticeProvision{{
			Key:  "abita-eye-group",
			Name: "Abita Eye Group",
			Locations: []access.LocationProvision{{
				Key:             "south-florida-medical",
				Name:            "South Florida Medical",
				AbitaOfficeKeys: []string{"hollywood", "sweetwater"},
			}},
		}},
	}); err != nil {
		t.Fatalf("provision operational Location: %v", err)
	}

	var practiceID string
	if err := pool.QueryRow(context.Background(), `
		SELECT id::text FROM access_practices
		WHERE provisioning_key = 'abita-eye-group'
	`).Scan(&practiceID); err != nil {
		t.Fatalf("load Practice: %v", err)
	}
	service := access.ServiceIdentity{
		Subject:       "abita-route-test",
		PracticeID:    practiceID,
		LocationScope: access.LocationScopeAll,
		Capabilities:  []access.ServiceCapability{access.ServiceCapabilityCreateTask},
	}
	resolve := func(officeKey string) access.ServiceAuthorization {
		t.Helper()
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatalf("begin service authorization: %v", err)
		}
		defer func() { _ = tx.Rollback(context.Background()) }()
		authorization, err := module.LockServiceAuthorization(
			context.Background(),
			tx,
			service,
			officeKey,
			access.ServiceCapabilityCreateTask,
		)
		if err != nil {
			t.Fatalf("authorize office %q: %v", officeKey, err)
		}
		return authorization
	}

	hollywood := resolve("hollywood")
	sweetwater := resolve("sweetwater")
	if hollywood.LocationID != sweetwater.LocationID {
		t.Fatalf(
			"South Florida office routes diverged: Hollywood %q, Sweetwater %q",
			hollywood.LocationID,
			sweetwater.LocationID,
		)
	}
}

func TestProvisioningRequireEmptyAccessStateRejectsExistingConfiguration(t *testing.T) {
	pool := testdb.Open(t)
	module := access.New(pool, nil)
	if _, err := module.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "legacy-configuration-test",
		Practices: []access.PracticeProvision{{
			Key:  "legacy-practice",
			Name: "Legacy Practice",
			Locations: []access.LocationProvision{{
				Key:  "legacy-location",
				Name: "Legacy Location",
			}},
		}},
	}); err != nil {
		t.Fatalf("seed legacy access state: %v", err)
	}

	_, err := module.Provision(context.Background(), access.Provisioning{
		Environment:             "production",
		RequestedBy:             "clean-launch-test",
		RequireEmptyAccessState: true,
		Practices: []access.PracticeProvision{{
			Key:  "new-practice",
			Name: "New Practice",
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "requires empty Access state") {
		t.Fatalf("nonempty Access provisioning error = %v", err)
	}

	var practiceCount int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*) FROM access_practices
	`).Scan(&practiceCount); err != nil {
		t.Fatalf("count Practices after rejected provisioning: %v", err)
	}
	if practiceCount != 1 {
		t.Fatalf("Practices after rejected provisioning = %d, want 1", practiceCount)
	}
}

func TestLocationScopeIsDynamicForAdminAndAllButExplicitForSelectedStaff(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	module := access.New(pool, func() time.Time { return now })

	_, err := module.Provision(context.Background(), access.Provisioning{
		Environment: "test",
		RequestedBy: "slice-1-integration-test",
		Practices: []access.PracticeProvision{{
			Key:  "abita-eye-group",
			Name: "Abita Eye Group",
			Locations: []access.LocationProvision{
				{Key: "fixture-location-1", Name: "Fixture Location 1"},
				{Key: "fixture-location-2", Name: "Fixture Location 2"},
			},
			AccessGrants: []access.AccessGrantProvision{
				{
					Key:           "admin",
					Email:         "admin@abita.test",
					Role:          access.RoleAdmin,
					LocationScope: access.LocationScopeAll,
				},
				{
					Key:           "all-staff",
					Email:         "all@abita.test",
					Role:          access.RoleStaff,
					LocationScope: access.LocationScopeAll,
				},
				{
					Key:                  "selected-staff",
					Email:                "selected@abita.test",
					Role:                 access.RoleStaff,
					LocationScope:        access.LocationScopeSelected,
					SelectedLocationKeys: []string{"fixture-location-1"},
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
	for _, identity := range identities {
		discovery, err := module.DiscoverActor(context.Background(), identity)
		if err != nil {
			t.Fatalf("activate Google identity %q: %v", identity.Email, err)
		}
		practiceID = discovery.Practices[0].ID
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
			AccessGrants: []access.AccessGrantProvision{
				{
					Key:           "admin",
					Email:         "admin@abita.test",
					Role:          access.RoleAdmin,
					LocationScope: access.LocationScopeAll,
				},
				{
					Key:           "all-staff",
					Email:         "all@abita.test",
					Role:          access.RoleStaff,
					LocationScope: access.LocationScopeAll,
				},
				{
					Key:                  "selected-staff",
					Email:                "selected@abita.test",
					Role:                 access.RoleStaff,
					LocationScope:        access.LocationScopeSelected,
					SelectedLocationKeys: []string{"fixture-location-1"},
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

func TestPlatformOperatorMutatesAcrossPracticesAndKeepsRealActor(t *testing.T) {
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

	mutation, err := module.AddLocation(context.Background(), access.AddLocationCommand{
		Identity:   operator,
		PracticeID: practiceA.ID,
		Key:        "fixture-a-2",
		Name:       "Fixture A 2",
	})
	if err != nil {
		t.Fatalf("add Location as operator: %v", err)
	}
	if mutation.Audit.ActorSubject != operator.Subject || mutation.Audit.Action != "location.added" {
		t.Fatalf("mutation audit = %#v", mutation.Audit)
	}

	_, err = module.AddLocation(context.Background(), access.AddLocationCommand{
		Identity:   operator,
		PracticeID: practiceB.ID,
		Key:        "fixture-b-2",
		Name:       "Fixture B 2",
	})
	if err != nil {
		t.Fatalf("add Location in second Practice: %v", err)
	}

	_, err = module.AddLocation(context.Background(), access.AddLocationCommand{
		Identity: access.Identity{
			Subject:       "unknown-subject",
			Email:         "unknown@acuity.test",
			EmailVerified: true,
		},
		PracticeID: practiceA.ID,
		Key:        "denied-location",
		Name:       "Denied Location",
	})
	if !errors.Is(err, access.ErrDenied) {
		t.Fatalf("non-operator mutation error = %v", err)
	}

	stillVisible, err := module.ResolveActor(context.Background(), operator, practiceB.ID, "")
	if err != nil {
		t.Fatalf("global operator resolution: %v", err)
	}
	if !stillVisible.PlatformOperator || stillVisible.Practice.ID != practiceB.ID {
		t.Fatalf("operator visibility = %#v", stillVisible)
	}
}

func TestAccessGrantDiscoveryAndRequestedLocationStayInsideAccess(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	module := access.New(pool, func() time.Time { return now })

	_, err := module.Provision(context.Background(), access.Provisioning{
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
			AccessGrants: []access.AccessGrantProvision{{
				Key:                  "selected-staff",
				Email:                "selected@abita.test",
				Role:                 access.RoleStaff,
				LocationScope:        access.LocationScopeSelected,
				SelectedLocationKeys: []string{"fixture-location-1"},
			}},
		}},
	})
	if err != nil {
		t.Fatalf("provision Access Grant discovery: %v", err)
	}

	operatorEligibility, err := module.InspectSignUpEligibility(context.Background(), "FOUNDER@ACUITY.TEST")
	if err != nil {
		t.Fatalf("inspect Platform Operator eligibility: %v", err)
	}
	if operatorEligibility.Kind != access.SignUpEligibilityPlatformOperator {
		t.Fatalf("operator eligibility = %#v", operatorEligibility)
	}

	identity := access.Identity{
		Subject:       "selected-subject",
		Email:         "selected@abita.test",
		EmailVerified: true,
	}
	discovery, err := module.DiscoverActor(context.Background(), identity)
	if err != nil {
		t.Fatalf("discover member access: %v", err)
	}
	if discovery.PlatformOperator ||
		len(discovery.Practices) != 1 ||
		discovery.Practices[0].Membership == nil {
		t.Fatalf("member discovery = %#v", discovery)
	}
	accepted, err := module.ResolveActor(
		context.Background(), identity, discovery.Practices[0].ID, "",
	)
	if err != nil {
		t.Fatalf("resolve selected Google user: %v", err)
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

func TestPlatformOperatorHasOperationalAccessWithoutMemberships(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 8, 12, 0, 0, 0, time.UTC)
	module := access.New(pool, func() time.Time { return now })
	operator := access.Identity{
		Subject:       "founder-subject",
		Email:         "founder@acuity.test",
		EmailVerified: true,
	}

	if _, err := module.Provision(context.Background(), access.Provisioning{
		Environment:       "test",
		RequestedBy:       "operator-staff-test",
		PlatformOperators: []string{operator.Email},
		Practices: []access.PracticeProvision{
			{
				Key:       "customer-practice",
				Name:      "Customer Practice",
				Locations: []access.LocationProvision{{Key: "customer", Name: "Customer"}},
			},
			{
				Key:       "acuity-demo",
				Name:      "Acuity Demo",
				Locations: []access.LocationProvision{{Key: "demo", Name: "Demo"}},
			},
		},
	}); err != nil {
		t.Fatalf("provision operator access: %v", err)
	}
	eligibility, err := module.InspectSignUpEligibility(context.Background(), operator.Email)
	if err != nil || eligibility.Kind != access.SignUpEligibilityPlatformOperator {
		t.Fatalf("operator sign-up eligibility = %#v, err = %v", eligibility, err)
	}

	discovery, err := module.DiscoverActor(context.Background(), operator)
	if err != nil {
		t.Fatalf("discover dual-role operator: %v", err)
	}
	if !discovery.PlatformOperator || len(discovery.Practices) != 2 {
		t.Fatalf("operator discovery = %#v", discovery)
	}
	var demo, customer access.PracticeAccess
	for _, practice := range discovery.Practices {
		switch practice.Name {
		case "Acuity Demo":
			demo = practice
		case "Customer Practice":
			customer = practice
		}
	}
	if demo.Membership != nil || customer.Membership != nil {
		t.Fatalf("operator Memberships = demo:%#v customer:%#v", demo.Membership, customer.Membership)
	}
	if !demo.CallingEnabled || !customer.CallingEnabled {
		t.Fatalf("operator calling = demo:%t customer:%t", demo.CallingEnabled, customer.CallingEnabled)
	}

	tx, err := pool.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	practiceIDs, err := module.LockOperationalActor(context.Background(), tx, operator)
	if err != nil {
		t.Fatalf("lock dual-role operator: %v", err)
	}
	if len(practiceIDs) != 2 {
		t.Fatalf("operational Practices = %#v, want both Practices", practiceIDs)
	}
	if _, err := module.LockMembershipAuthorization(
		context.Background(), tx, operator, demo.ID, demo.Locations[0].ID,
	); err != nil {
		t.Fatalf("authorize demo Staff operation: %v", err)
	}
	if _, err := module.LockMembershipAuthorization(
		context.Background(), tx, operator, customer.ID, customer.Locations[0].ID,
	); err != nil {
		t.Fatalf("authorize customer operation: %v", err)
	}

	var operational bool
	if err := pool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1 FROM access_operational_users WHERE user_subject = $1
		)
	`, operator.Subject).Scan(&operational); err != nil {
		t.Fatal(err)
	}
	if !operational {
		t.Fatal("dual-role operator is missing from access_operational_users")
	}
}

func TestProvisioningRejectsOperatorSpecificAccessGrant(t *testing.T) {
	pool := testdb.Open(t)
	module := access.New(pool, nil)
	_, err := module.Provision(context.Background(), access.Provisioning{
		Environment:       "test",
		RequestedBy:       "operator-invariant-test",
		PlatformOperators: []string{"operator@acuity.test"},
		Practices: []access.PracticeProvision{{
			Key:  "operator-practice",
			Name: "Operator Practice",
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "operator-staff",
				Email:         "operator@acuity.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}},
		}},
	})
	if !errors.Is(err, access.ErrInvalidInput) ||
		!strings.Contains(err.Error(), "Platform Operators do not use Access Grants") {
		t.Fatalf("operator-specific Access Grant error = %v", err)
	}
}

func TestMembershipRevocationTakesEffectOnNextResolutionAndIsAudited(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	module := access.New(pool, func() time.Time { return now })
	ctx := context.Background()
	operator := access.Identity{
		Subject:       "founder-subject",
		Email:         "founder@acuity.test",
		EmailVerified: true,
	}

	_, err := module.Provision(ctx, access.Provisioning{
		Environment:       "test",
		RequestedBy:       "slice-1-revocation-test",
		PlatformOperators: []string{operator.Email},
		Practices: []access.PracticeProvision{{
			Key:       "abita-eye-group",
			Name:      "Abita Eye Group",
			Locations: []access.LocationProvision{{Key: "fixture-1", Name: "Fixture Location 1"}},
			AccessGrants: []access.AccessGrantProvision{{
				Key:           "active-staff",
				Email:         "active@abita.test",
				Role:          access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}},
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
	memberIdentity := access.Identity{
		Subject:       "active-subject",
		Email:         "active@abita.test",
		EmailVerified: true,
	}
	memberDiscovery, err := module.DiscoverActor(ctx, memberIdentity)
	if err != nil {
		t.Fatalf("activate active Membership: %v", err)
	}
	authorization, err := module.ResolveActor(ctx, memberIdentity, memberDiscovery.Practices[0].ID, "")
	if err != nil {
		t.Fatalf("resolve active Membership: %v", err)
	}
	if err := module.RevokeMembership(ctx, access.RevokeMembershipCommand{
		Identity:     operator,
		PracticeID:   practiceID,
		MembershipID: authorization.Membership.ID,
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
		if event.Action == "membership.revoked" {
			if event.ActorSubject != operator.Subject {
				t.Fatalf("revocation audit = %#v", event)
			}
		}
	}
	if !actions["membership.revoked"] {
		t.Fatalf("audit actions = %#v", actions)
	}
}
