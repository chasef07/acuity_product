package interaction

import (
	"context"
	"fmt"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
)

type KnowledgeGapResolution string

const (
	KnowledgeGapAnswered  KnowledgeGapResolution = "ANSWERED"
	KnowledgeGapNotNeeded KnowledgeGapResolution = "NOT_NEEDED"
)

type KnowledgeGapReview struct {
	Resolution KnowledgeGapResolution `json:"resolution"`
	ReviewedAt time.Time              `json:"reviewedAt"`
}

const judgedKnowledgeGap = `EXISTS (SELECT 1 FROM ai_interaction_scorecard_answers gap
   WHERE gap.interaction_id = i.id AND gap.question = 'knowledge_available' AND NOT gap.answer)`

const openKnowledgeGap = judgedKnowledgeGap + ` AND NOT EXISTS (SELECT 1 FROM ai_interaction_knowledge_gap_reviews review
   WHERE review.interaction_id = i.id)`

func (m *Module) CloseKnowledgeGap(ctx context.Context, identity access.Identity, id string, resolution KnowledgeGapResolution) (KnowledgeGapReview, error) {
	if resolution != KnowledgeGapAnswered && resolution != KnowledgeGapNotNeeded {
		return KnowledgeGapReview{}, ErrInvalidInput
	}
	tx, authorization, err := m.beginInteractionAccess(ctx, identity, id, true, audienceStaff)
	if err != nil {
		return KnowledgeGapReview{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var judged bool
	if err := tx.QueryRow(ctx, `SELECT `+judgedKnowledgeGap+` FROM ai_interactions i WHERE i.id = $1`, id).Scan(&judged); err != nil {
		return KnowledgeGapReview{}, fmt.Errorf("read knowledge gap: %w", err)
	}
	if !judged {
		return KnowledgeGapReview{}, ErrInvalidInput
	}
	result, err := tx.Exec(ctx, `INSERT INTO ai_interaction_knowledge_gap_reviews (interaction_id, resolution, reviewed_by, reviewed_at) VALUES ($1, $2, $3, $4) ON CONFLICT (interaction_id) DO NOTHING`, id, resolution, identity.Subject, m.now())
	if err != nil {
		return KnowledgeGapReview{}, fmt.Errorf("close knowledge gap: %w", err)
	}
	if result.RowsAffected() > 0 {
		if err := m.access.AuditOperatorMutation(ctx, tx, authorization, access.OperatorMutationAudit{Action: "ai_interaction.knowledge_gap_closed", ResourceType: "ai_interaction", ResourceID: id, ResourceVersion: 1, OccurredAt: m.now()}); err != nil {
			return KnowledgeGapReview{}, err
		}
	}
	var review KnowledgeGapReview
	if err := tx.QueryRow(ctx, `SELECT resolution, reviewed_at FROM ai_interaction_knowledge_gap_reviews WHERE interaction_id = $1`, id).Scan(&review.Resolution, &review.ReviewedAt); err != nil {
		return KnowledgeGapReview{}, fmt.Errorf("read knowledge gap review: %w", err)
	}
	if review.Resolution != resolution {
		return KnowledgeGapReview{}, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return KnowledgeGapReview{}, err
	}
	return review, nil
}
