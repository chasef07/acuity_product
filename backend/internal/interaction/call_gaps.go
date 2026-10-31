package interaction

import (
	"context"
	"fmt"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
)

type GapResolution string

const (
	KnowledgeGapAnswered GapResolution = "ANSWERED"
	InsuranceGapAdded    GapResolution = "ADDED"
	GapNotNeeded         GapResolution = "NOT_NEEDED"
)

type GapReview struct {
	Resolution GapResolution `json:"resolution"`
	ReviewedAt time.Time     `json:"reviewedAt"`
}

type callGap struct {
	question string
	reviews  string
	audit    string
	fixed    GapResolution
}

var (
	knowledgeCallGap = callGap{question: "knowledge_available", reviews: "ai_interaction_knowledge_gap_reviews", audit: "ai_interaction.knowledge_gap_closed", fixed: KnowledgeGapAnswered}
	insuranceCallGap = callGap{question: "insurance_listed", reviews: "ai_interaction_insurance_gap_reviews", audit: "ai_interaction.insurance_gap_closed", fixed: InsuranceGapAdded}
)

func (gap callGap) judged() string {
	return `EXISTS (SELECT 1 FROM ai_interaction_scorecard_answers gap
   WHERE gap.interaction_id = i.id AND gap.question = '` + gap.question + `' AND NOT gap.answer)`
}

func (gap callGap) open() string {
	return gap.judged() + ` AND NOT EXISTS (SELECT 1 FROM ` + gap.reviews + ` review
   WHERE review.interaction_id = i.id)`
}

func (m *Module) CloseKnowledgeGap(ctx context.Context, identity access.Identity, id string, resolution GapResolution) (GapReview, error) {
	return m.closeGap(ctx, identity, id, knowledgeCallGap, resolution)
}

func (m *Module) CloseInsuranceGap(ctx context.Context, identity access.Identity, id string, resolution GapResolution) (GapReview, error) {
	return m.closeGap(ctx, identity, id, insuranceCallGap, resolution)
}

func (m *Module) closeGap(ctx context.Context, identity access.Identity, id string, gap callGap, resolution GapResolution) (GapReview, error) {
	if resolution != gap.fixed && resolution != GapNotNeeded {
		return GapReview{}, ErrInvalidInput
	}
	tx, authorization, err := m.beginInteractionAccess(ctx, identity, id, true, audienceStaff)
	if err != nil {
		return GapReview{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var judged bool
	if err := tx.QueryRow(ctx, `SELECT `+gap.judged()+` FROM ai_interactions i WHERE i.id = $1`, id).Scan(&judged); err != nil {
		return GapReview{}, fmt.Errorf("read %s gap: %w", gap.question, err)
	}
	if !judged {
		return GapReview{}, ErrInvalidInput
	}
	result, err := tx.Exec(ctx, `INSERT INTO `+gap.reviews+` (interaction_id, resolution, reviewed_by, reviewed_at) VALUES ($1, $2, $3, $4) ON CONFLICT (interaction_id) DO NOTHING`, id, resolution, identity.Subject, m.now())
	if err != nil {
		return GapReview{}, fmt.Errorf("close %s gap: %w", gap.question, err)
	}
	if result.RowsAffected() > 0 {
		if err := m.access.AuditOperatorMutation(ctx, tx, authorization, access.OperatorMutationAudit{Action: gap.audit, ResourceType: "ai_interaction", ResourceID: id, ResourceVersion: 1, OccurredAt: m.now()}); err != nil {
			return GapReview{}, err
		}
	}
	var review GapReview
	if err := tx.QueryRow(ctx, `SELECT resolution, reviewed_at FROM `+gap.reviews+` WHERE interaction_id = $1`, id).Scan(&review.Resolution, &review.ReviewedAt); err != nil {
		return GapReview{}, fmt.Errorf("read %s gap review: %w", gap.question, err)
	}
	if review.Resolution != resolution {
		return GapReview{}, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return GapReview{}, err
	}
	return review, nil
}
