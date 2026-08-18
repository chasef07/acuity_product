package humancalling_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPlatformOperatorSelectsOldestAttachedReceiptFromExactQuarantineGroup(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 18, 10, 0, 0, 0, time.UTC)
	ctx := context.Background()
	accessModule := access.New(pool, func() time.Time { return now })
	operator := access.Identity{
		Subject: "recovery-operator-subject", Email: "recovery-operator@acuity.test",
		EmailVerified: true,
	}
	staff := access.Identity{
		Subject: "recovery-staff-subject", Email: "recovery-staff@practice.test",
		EmailVerified: true,
	}
	if _, err := accessModule.Provision(ctx, access.Provisioning{
		Environment: "test", RequestedBy: "provider-receipt-recovery-test",
		PlatformOperators: []string{operator.Email},
		Practices: []access.PracticeProvision{{
			Key: "receipt-recovery", Name: "Receipt Recovery",
			Locations: []access.LocationProvision{{Key: "main", Name: "Main"}},
			AccessGrants: []access.AccessGrantProvision{{
				Key: "staff", Email: staff.Email, Role: access.RoleStaff,
				LocationScope: access.LocationScopeAll,
			}},
		}},
	}); err != nil {
		t.Fatalf("provision receipt recovery access: %v", err)
	}
	discovery, err := accessModule.DiscoverActor(ctx, operator)
	if err != nil || len(discovery.Practices) != 1 || len(discovery.Practices[0].Locations) != 1 {
		t.Fatalf("discover receipt recovery Practice: discovery=%#v err=%v", discovery, err)
	}
	practiceID := discovery.Practices[0].ID
	locationID := discovery.Practices[0].Locations[0].ID
	const (
		oldCallID    = "20000000-0000-4000-8000-000000000001"
		newCallID    = "20000000-0000-4000-8000-000000000002"
		oldHandoffID = "20000000-0000-4000-8000-000000000101"
		newHandoffID = "20000000-0000-4000-8000-000000000102"
	)
	seedRecoveryCall(t, pool, practiceID, locationID, oldCallID, oldHandoffID, "old", now)
	seedRecoveryCall(t, pool, practiceID, locationID, newCallID, newHandoffID, "new", now)
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_provider_receipts (
			event_id, call_id, event_type, occurred_at, received_at,
			signature_timestamp, raw_body, state, projection_attempts,
			projection_error_code, last_attempt_at, next_attempt_at,
			projected_at, quarantined_at
		) VALUES
			('private-oldest-event', $1, 'call.answered', $3, $3, 1,
			 '\x707269766174652d6f6c64', 'QUARANTINED', 10,
			 'PROJECTION_RETRY_EXHAUSTED', $4, $4, $4, $4),
			('private-newer-event', $2, 'call.answered', $5, $5, 1,
			 '\x707269766174652d6e6577', 'QUARANTINED', 11,
			 'PROJECTION_RETRY_EXHAUSTED', $4, $4, $4, $4),
			('private-other-code', $2, 'call.answered', $6, $6, 1,
			 '\x707269766174652d6f74686572', 'QUARANTINED', 12,
			 'RELATED_FACT_TIMEOUT', $4, $4, $4, $4),
			('private-unattached-event', NULL, 'call.answered', $7, $7, 1,
			 '\x707269766174652d6f727068616e', 'QUARANTINED', 13,
			 'PROJECTION_RETRY_EXHAUSTED', $4, $4, $4, $4)
	`, oldCallID, newCallID, now.Add(-10*time.Minute), now,
		now.Add(-5*time.Minute), now.Add(-4*time.Minute), now.Add(-20*time.Minute)); err != nil {
		t.Fatalf("seed receipt recovery quarantine: %v", err)
	}

	calling := humancalling.New(pool, accessModule, nil, humancalling.Config{
		HandoffTokenKey: []byte("0123456789abcdef0123456789abcdef"),
	}, func() time.Time { return now })
	candidate, err := calling.SelectProviderReceiptCandidate(ctx,
		humancalling.ProviderReceiptCandidateQuery{
			Identity: operator, PracticeID: practiceID,
			EventType: "call.answered", ErrorCode: "PROJECTION_RETRY_EXHAUSTED",
		})
	if err != nil {
		t.Fatalf("select provider receipt candidate: %v", err)
	}
	if candidate.PracticeID != practiceID || candidate.CallID != oldCallID ||
		candidate.EventType != "call.answered" ||
		candidate.ErrorCode != "PROJECTION_RETRY_EXHAUSTED" ||
		candidate.Attempts != 10 || candidate.AgeSeconds != 600 ||
		candidate.RemainingGroupCount != 1 || len(candidate.ReceiptReference) != 43 {
		t.Fatalf("provider receipt candidate = %#v", candidate)
	}
	encoded, err := json.Marshal(candidate)
	if err != nil {
		t.Fatalf("encode provider receipt candidate: %v", err)
	}
	for _, private := range []string{
		"private-oldest-event", "private-newer-event", "private-other-code",
		"private-unattached-event", "private-old", "private-new", "private-orphan",
	} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("provider receipt candidate exposed %q: %s", private, encoded)
		}
	}
	if _, err := calling.SelectProviderReceiptCandidate(ctx,
		humancalling.ProviderReceiptCandidateQuery{
			Identity: staff, PracticeID: practiceID,
			EventType: "call.answered", ErrorCode: "PROJECTION_RETRY_EXHAUSTED",
		}); err != humancalling.ErrDenied {
		t.Fatalf("Staff candidate selection error = %v, want denied", err)
	}
	if _, err := calling.SelectProviderReceiptCandidate(ctx,
		humancalling.ProviderReceiptCandidateQuery{
			Identity: operator, PracticeID: practiceID,
			EventType: "call.answered", ErrorCode: "UNRESTRICTED_PRIVATE_ERROR",
		}); err != humancalling.ErrInvalidInput {
		t.Fatalf("unbounded candidate group error = %v, want invalid input", err)
	}
}

func TestPlatformOperatorReadsOneReceiptRecoveryStatusAcrossRequeue(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 18, 11, 0, 0, 0, time.UTC)
	ctx := context.Background()
	accessModule := access.New(pool, func() time.Time { return now })
	operator := access.Identity{
		Subject: "status-operator-subject", Email: "status-operator@acuity.test",
		EmailVerified: true,
	}
	if _, err := accessModule.Provision(ctx, access.Provisioning{
		Environment: "test", RequestedBy: "provider-receipt-status-test",
		PlatformOperators: []string{operator.Email},
		Practices: []access.PracticeProvision{{
			Key: "receipt-status", Name: "Receipt Status",
			Locations: []access.LocationProvision{{Key: "main", Name: "Main"}},
		}},
	}); err != nil {
		t.Fatalf("provision receipt status access: %v", err)
	}
	discovery, err := accessModule.DiscoverActor(ctx, operator)
	if err != nil || len(discovery.Practices) != 1 || len(discovery.Practices[0].Locations) != 1 {
		t.Fatalf("discover receipt status Practice: discovery=%#v err=%v", discovery, err)
	}
	practiceID := discovery.Practices[0].ID
	locationID := discovery.Practices[0].Locations[0].ID
	const (
		callID    = "21000000-0000-4000-8000-000000000001"
		handoffID = "21000000-0000-4000-8000-000000000101"
		eventID   = "private-status-event"
	)
	seedRecoveryCall(t, pool, practiceID, locationID, callID, handoffID, "status", now)
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_call_legs (
			call_id, role, sequence, state, provider_call_control_id,
			provider_call_leg_id, provider_call_session_id
		) VALUES ($1, 'CALLER', 1, 'RINGING', 'private-control',
			'private-leg', 'private-session')
	`, callID); err != nil {
		t.Fatalf("seed receipt status CallLeg: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_provider_commands (
			call_id, action, target_id, state, attempts, created_at, updated_at
		) VALUES
			($1, 'HANGUP_LEG', 'private-target', 'RECONCILED', 1, $2, $2),
			($1, 'BRIDGE', 'private-target', 'AMBIGUOUS', 2, $2, $2)
	`, callID, now.Add(-time.Minute)); err != nil {
		t.Fatalf("seed receipt status commands: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_provider_receipts (
			event_id, call_id, event_type, occurred_at, received_at,
			signature_timestamp, raw_body, state, projection_attempts,
			projection_error_code, duplicate_count, last_attempt_at,
			next_attempt_at, projected_at, quarantined_at
		) VALUES ($1, $2, 'call.hangup', $3, $3, 1,
			'\x707269766174652d737461747573', 'QUARANTINED', 10,
			'PROJECTION_RETRY_EXHAUSTED', 2, $4, $4, $4, $4)
	`, eventID, callID, now.Add(-5*time.Minute), now); err != nil {
		t.Fatalf("seed receipt status quarantine: %v", err)
	}

	calling := humancalling.New(pool, accessModule, nil, humancalling.Config{
		HandoffTokenKey: []byte("0123456789abcdef0123456789abcdef"),
	}, func() time.Time { return now })
	candidate, err := calling.SelectProviderReceiptCandidate(ctx,
		humancalling.ProviderReceiptCandidateQuery{
			Identity: operator, PracticeID: practiceID,
			EventType: "call.hangup", ErrorCode: "PROJECTION_RETRY_EXHAUSTED",
		})
	if err != nil {
		t.Fatalf("select receipt status candidate: %v", err)
	}
	status, err := calling.ReadProviderReceiptRecoveryStatus(ctx,
		humancalling.ProviderReceiptRecoveryStatusQuery{
			Identity: operator, PracticeID: practiceID,
			ReceiptReference: candidate.ReceiptReference,
		})
	if err != nil {
		t.Fatalf("read provider receipt recovery status: %v", err)
	}
	if status.PracticeID != practiceID || status.CallID != callID ||
		status.ReceiptReference != candidate.ReceiptReference ||
		status.EventType != "call.hangup" ||
		status.ErrorCode != "PROJECTION_RETRY_EXHAUSTED" ||
		status.State != humancalling.ReceiptQuarantined || status.Attempts != 10 ||
		status.AgeSeconds != 300 || status.DuplicateCount != 2 ||
		status.CallVersion != 1 || len(status.CallLegStates) != 1 ||
		status.CallLegStates[0].State != "RINGING" || status.CallLegStates[0].Count != 1 ||
		len(status.CommandStates) != 2 || status.ActiveReceiptCount != 0 ||
		status.QuarantinedReceiptCount != 1 || status.RequeueAuditCount != 0 ||
		status.ResolutionAuditCount != 0 {
		t.Fatalf("provider receipt recovery status = %#v", status)
	}
	if status.CommandStates[0].State != "AMBIGUOUS" || status.CommandStates[0].Count != 1 ||
		status.CommandStates[1].State != "RECONCILED" || status.CommandStates[1].Count != 1 {
		t.Fatalf("provider receipt command states = %#v", status.CommandStates)
	}
	if _, err := calling.ReadProviderReceiptRecoveryStatus(ctx,
		humancalling.ProviderReceiptRecoveryStatusQuery{
			Identity:         operator,
			PracticeID:       "21000000-0000-4000-8000-000000000099",
			ReceiptReference: candidate.ReceiptReference,
		}); !errors.Is(err, humancalling.ErrConflict) {
		t.Fatalf("cross-Practice receipt status error = %v, want conflict", err)
	}
	if _, err := calling.RequeueQuarantinedReceipt(ctx,
		humancalling.RequeueQuarantinedReceiptCommand{
			Identity:         operator,
			PracticeID:       "21000000-0000-4000-8000-000000000099",
			ReceiptReference: candidate.ReceiptReference,
		}); err == nil {
		t.Fatal("cross-Practice receipt requeue unexpectedly succeeded")
	}
	if _, err := calling.RequeueQuarantinedReceipt(ctx,
		humancalling.RequeueQuarantinedReceiptCommand{
			Identity: operator, PracticeID: practiceID,
			ReceiptReference: candidate.ReceiptReference,
		}); err != nil {
		t.Fatalf("requeue receipt for status read: %v", err)
	}
	status, err = calling.ReadProviderReceiptRecoveryStatus(ctx,
		humancalling.ProviderReceiptRecoveryStatusQuery{
			Identity: operator, PracticeID: practiceID,
			ReceiptReference: candidate.ReceiptReference,
		})
	if err != nil {
		t.Fatalf("read requeued provider receipt recovery status: %v", err)
	}
	if status.State != humancalling.ReceiptPending || status.Attempts != 0 ||
		status.ErrorCode != "MANUALLY_REQUEUED" || status.ActiveReceiptCount != 1 ||
		status.QuarantinedReceiptCount != 0 || status.RequeueAuditCount != 1 ||
		status.ResolutionAuditCount != 0 {
		t.Fatalf("requeued provider receipt recovery status = %#v", status)
	}
	encoded, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("encode provider receipt recovery status: %v", err)
	}
	for _, private := range []string{
		eventID, "private-status", "private-control", "private-leg",
		"private-session", "private-target",
	} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("provider receipt recovery status exposed %q: %s", private, encoded)
		}
	}
}

func TestPlatformOperatorTerminallyResolvesOneUnreplayableReceiptWithAtomicAudit(t *testing.T) {
	pool := testdb.Open(t)
	now := time.Date(2026, time.August, 18, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	accessModule := access.New(pool, func() time.Time { return now })
	operator := access.Identity{
		Subject: "resolve-operator-subject", Email: "resolve-operator@acuity.test",
		EmailVerified: true,
	}
	if _, err := accessModule.Provision(ctx, access.Provisioning{
		Environment: "test", RequestedBy: "provider-receipt-resolution-test",
		PlatformOperators: []string{operator.Email},
		Practices: []access.PracticeProvision{{
			Key: "receipt-resolution", Name: "Receipt Resolution",
			Locations: []access.LocationProvision{{Key: "main", Name: "Main"}},
		}},
	}); err != nil {
		t.Fatalf("provision receipt resolution access: %v", err)
	}
	discovery, err := accessModule.DiscoverActor(ctx, operator)
	if err != nil || len(discovery.Practices) != 1 || len(discovery.Practices[0].Locations) != 1 {
		t.Fatalf("discover receipt resolution Practice: discovery=%#v err=%v", discovery, err)
	}
	practiceID := discovery.Practices[0].ID
	locationID := discovery.Practices[0].Locations[0].ID
	const (
		callID        = "22000000-0000-4000-8000-000000000001"
		handoffID     = "22000000-0000-4000-8000-000000000101"
		eventID       = "private-unreplayable-event"
		rollbackEvent = "private-rollback-event"
	)
	seedRecoveryCall(t, pool, practiceID, locationID, callID, handoffID, "resolve", now)
	originalRaw := []byte("private-original-receipt-evidence")
	rollbackRaw := []byte("private-rollback-receipt-evidence")
	if _, err := pool.Exec(ctx, `
		INSERT INTO human_calling_provider_receipts (
			event_id, call_id, event_type, occurred_at, received_at,
			signature_timestamp, raw_body, state, projection_attempts,
			projection_error_code, last_attempt_at, next_attempt_at,
			projected_at, quarantined_at
		) VALUES
			($1, $3, 'call.answered', $4, $4, 1, $5, 'QUARANTINED', 10,
			 'PROJECTION_RETRY_EXHAUSTED', $4, $4, $4, $4),
			($2, $3, 'call.answered', $6, $6, 1, $7, 'QUARANTINED', 11,
			 'PROJECTION_RETRY_EXHAUSTED', $4, $4, $4, $4)
	`, eventID, rollbackEvent, callID, now.Add(-time.Minute), originalRaw,
		now.Add(-30*time.Second), rollbackRaw); err != nil {
		t.Fatalf("seed unreplayable receipt quarantine: %v", err)
	}

	calling := humancalling.New(pool, accessModule, nil, humancalling.Config{
		HandoffTokenKey: []byte("0123456789abcdef0123456789abcdef"),
	}, func() time.Time { return now })
	candidate, err := calling.SelectProviderReceiptCandidate(ctx,
		humancalling.ProviderReceiptCandidateQuery{
			Identity: operator, PracticeID: practiceID,
			EventType: "call.answered", ErrorCode: "PROJECTION_RETRY_EXHAUSTED",
		})
	if err != nil {
		t.Fatalf("select unreplayable receipt: %v", err)
	}
	if _, err := calling.ResolveUnreplayableProviderReceipt(ctx,
		humancalling.ResolveUnreplayableProviderReceiptCommand{
			Identity:         operator,
			PracticeID:       "22000000-0000-4000-8000-000000000099",
			ReceiptReference: candidate.ReceiptReference,
		}); err == nil {
		t.Fatal("cross-Practice provider receipt resolution unexpectedly succeeded")
	}
	resolved, err := calling.ResolveUnreplayableProviderReceipt(ctx,
		humancalling.ResolveUnreplayableProviderReceiptCommand{
			Identity: operator, PracticeID: practiceID,
			ReceiptReference: candidate.ReceiptReference,
		})
	if err != nil {
		t.Fatalf("resolve unreplayable provider receipt: %v", err)
	}
	if resolved.ReceiptReference != candidate.ReceiptReference ||
		resolved.State != humancalling.ReceiptFailed {
		t.Fatalf("provider receipt resolution = %#v", resolved)
	}
	var state, errorCode string
	var raw []byte
	var attempts int64
	var processingStarted, quarantinedAt *time.Time
	var lastAttempt, nextAttempt, projectedAt time.Time
	if err := pool.QueryRow(ctx, `
		SELECT state, projection_error_code, raw_body, projection_attempts,
			processing_started_at, last_attempt_at, next_attempt_at,
			projected_at, quarantined_at
		FROM human_calling_provider_receipts
		WHERE event_id = $1
	`, eventID).Scan(
		&state, &errorCode, &raw, &attempts, &processingStarted, &lastAttempt,
		&nextAttempt, &projectedAt, &quarantinedAt,
	); err != nil {
		t.Fatalf("read resolved provider receipt evidence: %v", err)
	}
	if state != string(humancalling.ReceiptFailed) ||
		errorCode != "PROJECTION_RETRY_EXHAUSTED" || string(raw) != string(originalRaw) ||
		attempts != 10 || processingStarted != nil || quarantinedAt != nil ||
		!lastAttempt.Equal(now.Add(-time.Minute)) || !nextAttempt.Equal(now) ||
		!projectedAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("resolved provider receipt evidence changed: state=%s code=%s raw=%q attempts=%d processing=%v last=%v next=%v projected=%v quarantine=%v",
			state, errorCode, raw, attempts, processingStarted, lastAttempt,
			nextAttempt, projectedAt, quarantinedAt)
	}
	var auditCount int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM access_audit_events
		WHERE actor_type = 'PLATFORM_OPERATOR'
			AND actor_subject = $1
			AND practice_id::text = $2
			AND action = 'provider_receipt.resolved_unreplayable'
			AND details->>'resourceType' = 'provider_receipt'
			AND details->>'resourceId' = $3
			AND details->>'resourceVersion' = '10'
	`, operator.Subject, practiceID, eventID).Scan(&auditCount); err != nil {
		t.Fatalf("read provider receipt resolution audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("provider receipt resolution audit count = %d, want 1", auditCount)
	}

	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION reject_receipt_resolution_audit() RETURNS trigger
		LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.action = 'provider_receipt.resolved_unreplayable' THEN
				RAISE EXCEPTION 'synthetic audit failure';
			END IF;
			RETURN NEW;
		END
		$$;
		CREATE TRIGGER reject_receipt_resolution_audit
		BEFORE INSERT ON access_audit_events
		FOR EACH ROW EXECUTE FUNCTION reject_receipt_resolution_audit()
	`); err != nil {
		t.Fatalf("install synthetic resolution audit failure: %v", err)
	}
	rollbackCandidate, err := calling.SelectProviderReceiptCandidate(ctx,
		humancalling.ProviderReceiptCandidateQuery{
			Identity: operator, PracticeID: practiceID,
			EventType: "call.answered", ErrorCode: "PROJECTION_RETRY_EXHAUSTED",
		})
	if err != nil {
		t.Fatalf("select rollback receipt: %v", err)
	}
	if _, err := calling.ResolveUnreplayableProviderReceipt(ctx,
		humancalling.ResolveUnreplayableProviderReceiptCommand{
			Identity: operator, PracticeID: practiceID,
			ReceiptReference: rollbackCandidate.ReceiptReference,
		}); err == nil {
		t.Fatal("resolution succeeded despite audit failure")
	}
	if err := pool.QueryRow(ctx, `
		SELECT state, projection_error_code, raw_body
		FROM human_calling_provider_receipts
		WHERE event_id = $1
	`, rollbackEvent).Scan(&state, &errorCode, &raw); err != nil {
		t.Fatalf("read rolled-back provider receipt: %v", err)
	}
	if state != string(humancalling.ReceiptQuarantined) ||
		errorCode != "PROJECTION_RETRY_EXHAUSTED" || string(raw) != string(rollbackRaw) {
		t.Fatalf("audit failure did not roll back receipt: state=%s code=%s raw=%q",
			state, errorCode, raw)
	}
}

func seedRecoveryCall(
	t *testing.T,
	pool *pgxpool.Pool,
	practiceID string,
	locationID string,
	callID string,
	handoffID string,
	prefix string,
	now time.Time,
) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_handoffs (
			id, service_subject, practice_id, location_id, source_call_id,
			idempotency_key, input_fingerprint, expires_at
		) VALUES ($1, 'receipt-recovery', $2, $3, $4, $5, '\x01', $6)
	`, handoffID, practiceID, locationID, prefix+"-source", prefix+"-handoff",
		now.Add(time.Hour)); err != nil {
		t.Fatalf("seed %s receipt recovery handoff: %v", prefix, err)
	}
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO human_calling_calls (
			id, source_handoff_id, practice_id, location_id, caller_phone
		) VALUES ($1, $2, $3, $4, '+15555550100')
	`, callID, handoffID, practiceID, locationID); err != nil {
		t.Fatalf("seed %s receipt recovery Call: %v", prefix, err)
	}
}
