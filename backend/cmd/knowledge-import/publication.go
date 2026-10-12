package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/chasef07/acuity_product/backend/internal/knowledge"
	"github.com/jackc/pgx/v5/pgxpool"
)

type publicationReceipt struct {
	Applied                bool               `json:"applied"`
	ActiveRevisionVerified bool               `json:"activeRevisionVerified"`
	Revision               knowledge.Revision `json:"revision"`
	Provenance             string             `json:"provenance"`
	SourceGitCommit        string             `json:"sourceGitCommit"`
	PracticeID             string             `json:"practiceId"`
	OfficeKey              string             `json:"officeKey"`
	Sections               int                `json:"sections"`
}

func preparePublication(ctx context.Context, pool *pgxpool.Pool, command knowledge.ImportCommand, email string) (knowledge.ImportCommand, error) {
	if err := pool.QueryRow(ctx, `SELECT user_subject FROM access_platform_operators WHERE email=$1 AND user_subject IS NOT NULL`, email).Scan(&command.ActorSubject); err != nil {
		return command, errors.New("import requires an existing bound Platform Operator")
	}
	if err := knowledge.ValidateImport(command); err != nil {
		return command, err
	}
	var active *string
	err := pool.QueryRow(ctx, `SELECT c.revision_id::text FROM access_abita_office_locations route LEFT JOIN knowledge_corpora c ON c.practice_id=route.practice_id AND c.office_key=route.office_key WHERE route.practice_id=$1 AND route.office_key=$2`, command.PracticeID, command.OfficeKey).Scan(&active)
	if err != nil {
		return command, errors.New("could not read the authorized office route")
	}
	if active != nil {
		return command, fmt.Errorf("office %q already has active revision %s; edit its knowledge in the Portal (POST /v1/knowledge/revisions)", command.OfficeKey, *active)
	}
	return command, nil
}
