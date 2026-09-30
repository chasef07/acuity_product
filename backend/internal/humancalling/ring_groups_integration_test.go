package humancalling_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5"
)

func TestLocationRingGroupRestrictsInboundFanout(t *testing.T) {
	for _, scenario := range []struct {
		name                                        string
		restricted, unavailable, unclaimed, revoked bool
		want                                        int
	}{
		{name: "only selected member", restricted: true, want: 1},
		{name: "unavailable member", restricted: true, unavailable: true, want: 0},
		{name: "unclaimed email", restricted: true, unclaimed: true, want: 0},
		{name: "revoked member", restricted: true, revoked: true, want: 0},
		{name: "unconfigured location retains fanout", want: 3},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx := context.Background()
			pool := testdb.Open(t)
			now := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
			accessModule := access.New(pool, func() time.Time { return now })
			auth, staff := provisionConcurrentStaff(t, accessModule, now, "ring-group", 2)
			operator := access.Identity{Subject: "ring-operator", Email: "operator@synthetic.test", EmailVerified: true}
			if _, err := pool.Exec(ctx, `INSERT INTO access_platform_operators(email,user_subject) VALUES ($1,$2)`, operator.Email, operator.Subject); err != nil {
				t.Fatal(err)
			}
			provider := &recordingProvider{}
			calling := humancalling.New(pool, accessModule, provider, humancalling.Config{
				HandoffSIPDomain: "synthetic.sip.telnyx.com", StaffSIPDomain: "sip.telnyx.com",
				RingWindowDuration: 20 * time.Second, HandoffTokenKey: []byte("0123456789abcdef0123456789abcdef"),
				CallControlID: "staff-call-control-connection", CredentialConnectionID: "staff-credential-connection",
				FromNumber: "+14843336938", RingbackURL: "https://media.synthetic.test/ringback.wav",
			}, func() time.Time { return now })
			if scenario.restricted {
				email := staff[0].Email
				if scenario.unclaimed {
					email = "unclaimed@synthetic.test"
				}
				tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(ctx)
				// Repeated provisioning normalizes and replaces the complete member list.
				for i := 0; i < 2; i++ {
					if err := calling.ProvisionLocationRingGroupsInTx(ctx, tx, []humancalling.LocationRingGroupProvision{{PracticeKey: "ring-group-practice", LocationKey: "ring-group-location", MemberEmails: []string{" " + strings.ToUpper(email) + " ", email}}}, "ring-test"); err != nil {
						t.Fatal(err)
					}
				}
				if err := tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				var audits int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM access_audit_events WHERE action='calling.ring_group_configured'`).Scan(&audits); err != nil || audits != 1 {
					t.Fatalf("idempotent ring audits=%d err=%v", audits, err)
				}
			}
			prepareCredentials(t, calling)
			readyConcurrentStaff(t, calling, append(slices.Clone(staff), operator), "ring-browser")
			if scenario.unavailable {
				if _, err := pool.Exec(ctx, `UPDATE human_calling_softphone_leases SET desired_available=false WHERE user_subject=$1`, staff[0].Subject); err != nil {
					t.Fatal(err)
				}
			}
			if scenario.revoked {
				if _, err := pool.Exec(ctx, `UPDATE access_memberships SET revoked_at=$2 WHERE user_subject=$1`, staff[0].Subject, now); err != nil {
					t.Fatal(err)
				}
			}
			// Ring exclusion must not remove the second Staff member's workspace access.
			if _, err := accessModule.ResolveActor(ctx, staff[1], auth.Practice.ID, auth.Locations[0].ID); err != nil {
				t.Fatal(err)
			}
			if _, err := calling.CreateHandoff(ctx, humancalling.CreateHandoffCommand{
				Service:    humancalling.ServiceIdentity{Subject: "ring-agent", PracticeID: auth.Practice.ID},
				LocationID: auth.Locations[0].ID, SourceCallID: "ring-source", IdempotencyKey: "ring-handoff",
				Contact: humancalling.ContactContext{Phone: "+15555550100"},
			}); err != nil {
				t.Fatal(err)
			}
			caller := humancalling.ProviderFact{
				EventID: "ring-initiated", Type: humancalling.FactCallInitiated, OccurredAt: now,
				ConnectionID: "staff-call-control-connection", CallControlID: "ring-caller-control", CallLegID: "ring-caller-leg", CallSessionID: "ring-caller-session",
				From: "+15555550100", To: "+14843989071",
			}
			if err := calling.ApplyProviderFact(ctx, caller); err != nil {
				t.Fatal(err)
			}
			processAllCommands(t, calling)
			caller.EventID = "ring-answered"
			caller.Type = humancalling.FactCallAnswered
			caller.OccurredAt = now.Add(time.Second)
			if err := calling.ApplyProviderFact(ctx, caller); err != nil {
				t.Fatal(err)
			}
			processAllCommands(t, calling)
			var subjects []string
			if err := pool.QueryRow(ctx, `SELECT COALESCE(array_agg(staff_subject ORDER BY staff_subject),'{}') FROM human_calling_call_legs WHERE role='STAFF'`).Scan(&subjects); err != nil {
				t.Fatal(err)
			}
			if len(subjects) != scenario.want {
				t.Fatalf("ring subjects=%v, want %d", subjects, scenario.want)
			}
			if scenario.restricted && scenario.want == 1 && subjects[0] != staff[0].Subject {
				t.Fatalf("wrong ring member: %v", subjects)
			}
			if provider.count(humancalling.CommandDialStaff) != scenario.want {
				t.Fatalf("unexpected provider dial count")
			}
			if scenario.want == 0 {
				ring := provider.last(humancalling.CommandStartRingWindow)
				state, _ := ring.Payload["client_state"].(string)
				if err := calling.ApplyProviderFact(ctx, humancalling.ProviderFact{
					EventID: "ring-completed", Type: humancalling.FactPlaybackEnded, OccurredAt: now.Add(21 * time.Second),
					CallControlID: caller.CallControlID, CallLegID: caller.CallLegID, CallSessionID: caller.CallSessionID,
					ClientState: state, PlaybackStatus: "completed",
				}); err != nil {
					t.Fatal(err)
				}
				processAllCommands(t, calling)
				if provider.count(humancalling.CommandSpeakVoicemail) != 1 {
					t.Fatalf("unanswered ring group did not start voicemail")
				}
			}
		})
	}
}

func TestLocationRingGroupAuditCommitsWithProvisioning(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Open(t)
	now := time.Date(2026, time.September, 30, 12, 0, 0, 0, time.UTC)
	accessModule := access.New(pool, func() time.Time { return now })
	auth, staff := provisionConcurrentStaff(t, accessModule, now, "ring-audit", 1)
	calling := humancalling.New(pool, accessModule, nil, humancalling.Config{}, nil)
	provision := func(tx pgx.Tx) {
		t.Helper()
		if err := calling.ProvisionLocationRingGroupsInTx(ctx, tx, []humancalling.LocationRingGroupProvision{{PracticeKey: "ring-audit-practice", LocationKey: "ring-audit-location", MemberEmails: []string{staff[0].Email}}}, "ring-audit-test"); err != nil {
			t.Fatal(err)
		}
	}
	count := func(querier interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	}) (groups, audits int) {
		t.Helper()
		if err := querier.QueryRow(ctx, `
			SELECT (SELECT count(*) FROM human_calling_location_ring_groups),
				(SELECT count(*) FROM access_audit_events
				 WHERE action = 'calling.ring_group_configured' AND actor_type = 'PROVISIONER'
					AND actor_subject = 'ring-audit-test' AND practice_id = $1
					AND details = jsonb_build_object('locationId', $2::text, 'memberCount', 1))
		`, auth.Practice.ID, auth.Locations[0].ID).Scan(&groups, &audits); err != nil {
			t.Fatal(err)
		}
		return groups, audits
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	provision(tx)
	if groups, audits := count(tx); groups != 1 || audits != 1 {
		t.Fatalf("in-transaction ring groups=%d audits=%d", groups, audits)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if groups, audits := count(pool); groups != 0 || audits != 0 {
		t.Fatalf("rolled-back ring groups=%d audits=%d", groups, audits)
	}

	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	provision(tx)
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if groups, audits := count(pool); groups != 1 || audits != 1 {
		t.Fatalf("committed ring groups=%d audits=%d", groups, audits)
	}
}

func TestLocationRingGroupRejectsEmptyOrInvalidMembers(t *testing.T) {
	pool := testdb.Open(t)
	calling := humancalling.New(pool, access.New(pool, nil), nil, humancalling.Config{}, nil)
	for _, emails := range [][]string{nil, {}, {""}, {"not-an-email"}, {"Person <person@synthetic.test>"}} {
		tx, err := pool.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		err = calling.ProvisionLocationRingGroupsInTx(context.Background(), tx, []humancalling.LocationRingGroupProvision{{PracticeKey: "unused", LocationKey: "unused", MemberEmails: emails}}, "ring-test")
		tx.Rollback(context.Background())
		if err != humancalling.ErrInvalidInput {
			t.Fatalf("invalid group error=%v", err)
		}
	}
}
