// backlog-recovery is an explicit, audited operator repair tool. It never runs
// in a worker and never sends provider commands. Default execution is a dry run.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/chasef07/acuity_product/backend/internal/humancalling"
	"github.com/chasef07/acuity_product/backend/internal/interaction"
	"github.com/chasef07/acuity_product/backend/internal/migrations"
	"github.com/chasef07/acuity_product/backend/internal/work"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	group := flag.String("group", "", "acknowledgements, ai-clock, ai-legacy, calling, or schema")
	apply := flag.Bool("apply", false, "apply one audited transaction per selected item")
	before := flag.String("before", "", "exclusive RFC3339 receipt creation cutoff")
	limit := flag.Int("limit", 1, "maximum items to inspect or repair")
	flag.Parse()
	if err := run(*group, *apply, *before, *limit); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(group string, apply bool, before string, limit int) error {
	cutoff, err := time.Parse(time.RFC3339, before)
	if err != nil || limit < 1 || limit > 200 {
		return errors.New("a valid --before and --limit between 1 and 200 are required")
	}
	if os.Getenv("RECOVERY_DATABASE_URL") == "" || os.Getenv("RECOVERY_OPERATOR_EMAIL") == "" {
		return errors.New("RECOVERY_DATABASE_URL and RECOVERY_OPERATOR_EMAIL are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	config, err := pgxpool.ParseConfig(os.Getenv("RECOVERY_DATABASE_URL"))
	if err != nil {
		return errors.New("invalid recovery database configuration")
	}
	config.MaxConns, config.MinConns = 1, 0
	config.ConnConfig.RuntimeParams["statement_timeout"] = "15000"
	config.ConnConfig.RuntimeParams["lock_timeout"] = "2000"
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return errors.New("could not open recovery database")
	}
	defer pool.Close()
	operator := access.Identity{Email: os.Getenv("RECOVERY_OPERATOR_EMAIL"), EmailVerified: true}
	if err := pool.QueryRow(ctx, `SELECT user_subject FROM access_platform_operators WHERE email=$1 AND user_subject IS NOT NULL`, operator.Email).Scan(&operator.Subject); err != nil {
		return errors.New("recovery requires an existing bound Platform Operator")
	}
	if group == "schema" {
		var ready, applied bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name='0044_interrupted_credential_commands.sql'),EXISTS(SELECT 1 FROM schema_migrations WHERE name='0045_retired_legacy_interaction_receipts.sql')`).Scan(&ready, &applied); err != nil {
			return err
		}
		if !ready {
			return errors.New("recovery requires the existing 0044 schema")
		}
		if apply && !applied {
			if err := migrations.ApplyThrough(ctx, pool, "0045_retired_legacy_interaction_receipts.sql"); err != nil {
				return err
			}
			applied = true
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"migration": "0045_retired_legacy_interaction_receipts.sql", "applied": applied})
	}
	queries := map[string]string{
		"acknowledgements": `SELECT id::text FROM work_task_acknowledgements WHERE state='PENDING' AND safe_failure_code='SENDER_CONFIGURATION_UNAVAILABLE' AND created_at<$1 ORDER BY created_at,id LIMIT $2`,
		"ai-clock":         `SELECT id::text FROM ai_interaction_receipts WHERE state='QUARANTINED' AND projection_error_code='SOURCE_CONFLICT' AND kind IN ('START','OUTCOME_CHECKPOINT','CLOSEOUT') AND received_at<$1 ORDER BY received_at,id LIMIT $2`,
		"ai-legacy":        `SELECT id::text FROM ai_interaction_receipts WHERE state='QUARANTINED' AND kind='SUMMARY' AND received_at<$1 ORDER BY received_at,id LIMIT $2`,
		"calling":          `SELECT call_id::text FROM human_calling_provider_receipts WHERE state='QUARANTINED' AND event_type='call.playback.ended' AND projection_error_code='PROJECTION_APPLY_FACT_CONFLICT' AND received_at<$1 GROUP BY call_id ORDER BY call_id LIMIT $2`,
	}
	query, ok := queries[group]
	if !ok {
		return errors.New("unknown recovery group")
	}
	rows, err := pool.Query(ctx, query, cutoff, limit)
	if err != nil {
		return fmt.Errorf("select repair candidates: %w", err)
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	if err := encoder.Encode(map[string]any{"group": group, "candidates": len(ids), "apply": apply}); err != nil {
		return err
	}
	if !apply {
		return nil
	}
	a := access.New(pool, time.Now)
	i := interaction.New(pool, a, time.Now)
	c := humancalling.New(pool, a, nil, humancalling.Config{HandoffTokenKey: []byte(os.Getenv("RECOVERY_HANDOFF_KEY"))}, time.Now)
	for index, id := range ids {
		if group == "acknowledgements" {
			err = retireAcknowledgement(ctx, pool, a, operator, id, cutoff)
		} else if group == "ai-legacy" {
			err = i.RetireLegacySummary(ctx, operator, id)
		} else if group == "calling" {
			err = recoverRingtone(ctx, c, operator, id)
		} else {
			_, err = i.RecoverSourceClock(ctx, operator, id)
		}
		if err != nil {
			return fmt.Errorf("stopped %s at item %d (%s): %w", group, index+1, opaque(id), err)
		}
		if err := verify(ctx, pool, group, id); err != nil {
			return fmt.Errorf("verification stopped %s at item %d (%s): %w", group, index+1, opaque(id), err)
		}
		if err := encoder.Encode(map[string]any{"item": index + 1, "reference": opaque(id), "verified": true}); err != nil {
			return err
		}
	}
	return encoder.Encode(map[string]any{"group": group, "verified_repairs": len(ids)})
}

func retireAcknowledgement(ctx context.Context, pool *pgxpool.Pool, a *access.Module, operator access.Identity, id string, cutoff time.Time) error {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var practiceID, locationID string
	if err := tx.QueryRow(ctx, `SELECT task.practice_id::text,task.location_id::text FROM work_task_acknowledgements ack JOIN work_tasks task ON task.id=ack.task_id WHERE ack.id=$1 AND ack.state='PENDING' AND ack.safe_failure_code='SENDER_CONFIGURATION_UNAVAILABLE' AND ack.created_at<$2 AND ack.created_at<now()-interval '7 days' AND ack.message_id IS NULL FOR UPDATE OF ack`, id, cutoff).Scan(&practiceID, &locationID); err != nil {
		return err
	}
	authorization, err := a.LockMutationAuthorization(ctx, tx, operator, practiceID, locationID)
	if err != nil || !authorization.PlatformOperator {
		return access.ErrDenied
	}
	now := time.Now().UTC()
	if err := work.New(pool, a, time.Now).MarkTaskAcknowledgementNotNeeded(ctx, tx, id, "HISTORICAL_ACKNOWLEDGEMENT_SUPPRESSED", now); err != nil {
		return err
	}
	if err := a.AuditOperatorMutation(ctx, tx, authorization, access.OperatorMutationAudit{
		Action: "task_acknowledgement.retired", ResourceType: "task_acknowledgement", ResourceID: id, ResourceVersion: 1, OccurredAt: now,
	}); err != nil {
		return err
	}
	if _, err := a.RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func verify(ctx context.Context, pool *pgxpool.Pool, group, id string) error {
	query := `SELECT state='PROJECTED' AND EXISTS(SELECT 1 FROM access_audit_events WHERE action='ai_interaction.source_clock_recovered' AND details->>'resourceId'=$1) FROM ai_interaction_receipts WHERE id=$1::uuid`
	if group == "acknowledgements" {
		query = `SELECT state='NOT_NEEDED' AND message_id IS NULL AND next_attempt_at IS NULL AND EXISTS(SELECT 1 FROM access_audit_events WHERE action='task_acknowledgement.retired' AND details->>'resourceId'=$1) FROM work_task_acknowledgements WHERE id=$1::uuid`
	}
	if group == "ai-legacy" {
		query = `SELECT state='RETIRED' AND projected_at IS NULL AND EXISTS(SELECT 1 FROM access_audit_events WHERE action='ai_interaction.legacy_receipt_retired' AND details->>'resourceId'=$1) FROM ai_interaction_receipts WHERE id=$1::uuid`
	}
	if group == "calling" {
		query = `
			SELECT terminal_outcome IS NOT NULL AND ended_at IS NOT NULL
				AND NOT EXISTS (
					SELECT 1 FROM human_calling_provider_receipts
					WHERE call_id=$1 AND state IN ('PENDING','PROCESSING','QUARANTINED')
				)
				AND NOT EXISTS (
					SELECT 1 FROM human_calling_call_legs
					WHERE call_id=$1 AND state NOT IN ('ENDED','FAILED')
				)
				AND NOT EXISTS (
					SELECT 1 FROM human_calling_provider_commands
					WHERE call_id=$1 AND state IN ('PENDING','SENDING','SENT','AMBIGUOUS')
				)
				AND EXISTS (
					SELECT 1 FROM access_audit_events audit
					JOIN human_calling_provider_receipts receipt
						ON receipt.event_id=audit.details->>'resourceId'
					WHERE audit.action='provider_receipt.recovered'
						AND receipt.call_id=$1 AND receipt.state='APPLIED'
				)
			FROM human_calling_calls WHERE id=$1
		`
	}
	var verified bool
	if err := pool.QueryRow(ctx, query, id).Scan(&verified); err != nil {
		return err
	}
	if !verified {
		return errors.New("state and audit did not converge")
	}
	return nil
}

func recoverRingtone(ctx context.Context, calling *humancalling.Module, operator access.Identity, callID string) error {
	if os.Getenv("RECOVERY_HANDOFF_KEY") == "" {
		return errors.New("calling recovery requires the configured recovery-reference key")
	}
	timeline, err := calling.ReadOperatorTimeline(ctx, operator, callID)
	if err != nil {
		return err
	}
	references := map[string]bool{}
	for _, entry := range timeline.Entries {
		if entry.RecoveryReference != "" {
			references[entry.RecoveryReference] = true
		}
	}
	if len(references) != 1 {
		return errors.New("expected exactly one quarantined receipt on the selected Call")
	}
	for reference := range references {
		return calling.RecoverQuarantinedRingtone(ctx, humancalling.RequeueQuarantinedReceiptCommand{
			Identity: operator, PracticeID: timeline.PracticeID, ReceiptReference: reference,
		})
	}
	return errors.New("missing receipt reference")
}

func opaque(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:6])
}
