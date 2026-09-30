package interaction

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
)

type CallIssueOutcome string

const (
	CallIssueConfirmed  CallIssueOutcome = "CONFIRMED"
	CallIssueNotAnIssue CallIssueOutcome = "NOT_AN_ISSUE"
)

type CallIssueReview struct {
	Outcome    CallIssueOutcome
	ReviewedBy string
	ReviewedAt time.Time
}

type OperatorCallIssue struct {
	InteractionID string
	Phone         string
	StartedAt     time.Time
	Reason        AgentCallIssueReason
	ReportedBy    string
	ReportedAt    time.Time
	Review        *CallIssueReview
}

const operatorCallIssueSelect = `
SELECT interaction.id::text, interaction.phone, interaction.started_at, issue.reason,
 COALESCE(
  (SELECT email FROM access_memberships WHERE user_subject = issue.reported_by AND practice_id = interaction.practice_id),
  (SELECT email FROM access_platform_operators WHERE user_subject = issue.reported_by),
  'Unknown user'),
 issue.created_at, issue.review_outcome,
 COALESCE((SELECT email FROM access_platform_operators WHERE user_subject = issue.reviewed_by), 'Unknown user'),
 issue.reviewed_at
FROM ai_interaction_issues issue
JOIN ai_interactions interaction ON interaction.id = issue.interaction_id`

func scanOperatorCallIssue(row pgx.Row) (OperatorCallIssue, error) {
	var issue OperatorCallIssue
	var review CallIssueReview
	var outcome *CallIssueOutcome
	var reviewedAt *time.Time
	err := row.Scan(&issue.InteractionID, &issue.Phone, &issue.StartedAt, &issue.Reason, &issue.ReportedBy, &issue.ReportedAt, &outcome, &review.ReviewedBy, &reviewedAt)
	if outcome != nil {
		review.Outcome, review.ReviewedAt = *outcome, *reviewedAt
		issue.Review = &review
	}
	return issue, err
}

func readCallIssue(ctx context.Context, tx pgx.Tx, interactionID string) (*OperatorCallIssue, error) {
	issue, err := scanOperatorCallIssue(tx.QueryRow(ctx, operatorCallIssueSelect+` WHERE issue.interaction_id = $1`, interactionID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &issue, nil
}

func pendingCallIssues(ctx context.Context, tx pgx.Tx, practiceID string, locationIDs []string) ([]OperatorCallIssue, error) {
	rows, err := tx.Query(ctx, operatorCallIssueSelect+`
 WHERE interaction.practice_id = $1 AND interaction.location_id = ANY($2::uuid[]) AND issue.review_outcome IS NULL
 ORDER BY issue.created_at DESC, issue.interaction_id`, practiceID, locationIDs)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (OperatorCallIssue, error) {
		return scanOperatorCallIssue(row)
	})
}

func (m *Module) ReviewCallIssue(ctx context.Context, identity access.Identity, interactionID string, outcome CallIssueOutcome) (OperatorCallIssue, error) {
	if m.database == nil || m.access == nil || !validUUID(interactionID) || (outcome != CallIssueConfirmed && outcome != CallIssueNotAnIssue) {
		return OperatorCallIssue{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return OperatorCallIssue{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var practiceID, locationID string
	err = tx.QueryRow(ctx, `SELECT practice_id::text, location_id::text FROM ai_interactions WHERE id=$1`, interactionID).Scan(&practiceID, &locationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return OperatorCallIssue{}, ErrDenied
	}
	if err != nil {
		return OperatorCallIssue{}, err
	}
	authorization, err := m.access.LockMutationAuthorization(ctx, tx, identity, practiceID, locationID)
	if err != nil || !authorization.PlatformOperator {
		return OperatorCallIssue{}, ErrDenied
	}
	now := m.now()
	result, err := tx.Exec(ctx, `UPDATE ai_interaction_issues SET review_outcome=$2, reviewed_by=$3, reviewed_at=$4 WHERE interaction_id=$1 AND review_outcome IS DISTINCT FROM $2`, interactionID, outcome, identity.Subject, now)
	if err != nil {
		return OperatorCallIssue{}, err
	}
	if result.RowsAffected() > 0 {
		action := "ai_interaction.issue_" + strings.ToLower(string(outcome))
		if err := m.access.AuditOperatorMutation(ctx, tx, authorization, access.OperatorMutationAudit{Action: action, ResourceType: "ai_interaction", ResourceID: interactionID, ResourceVersion: 1, OccurredAt: now}); err != nil {
			return OperatorCallIssue{}, err
		}
	}
	issue, err := readCallIssue(ctx, tx, interactionID)
	if err != nil {
		return OperatorCallIssue{}, err
	}
	if issue == nil {
		return OperatorCallIssue{}, ErrInvalidInput
	}
	if err := tx.Commit(ctx); err != nil {
		return OperatorCallIssue{}, err
	}
	return *issue, nil
}
