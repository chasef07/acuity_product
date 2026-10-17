package interaction

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type callLabelProjection struct {
	Intent         *string
	Outcome        *string
	LabelingStatus *string
}

func validCallLabelFilters(command QueryAnalyticsCommand) bool {
	return (command.Intent == "" || ValidCallIntent(command.Intent)) && (command.Outcome == "" || ValidCallOutcome(command.Outcome))
}

func readCallLabelProjection(ctx context.Context, tx pgx.Tx, interactionID string) (callLabelProjection, error) {
	var labels callLabelProjection
	err := tx.QueryRow(ctx, `SELECT intent, outcome, status FROM ai_interaction_classifications WHERE interaction_id = $1`, interactionID).Scan(&labels.Intent, &labels.Outcome, &labels.LabelingStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return labels, nil
	}
	if err != nil {
		return callLabelProjection{}, fmt.Errorf("read call labels: %w", err)
	}
	return labels, nil
}
