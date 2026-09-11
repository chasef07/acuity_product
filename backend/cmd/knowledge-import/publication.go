package main

import (
	"context"
	"errors"
	"strings"

	"github.com/chasef07/acuity_product/backend/internal/knowledge"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type publicationReceipt struct {
	Applied                bool               `json:"applied"`
	Unchanged              bool               `json:"unchanged"`
	ActiveRevisionVerified bool               `json:"activeRevisionVerified"`
	Revision               knowledge.Revision `json:"revision"`
	Provenance             string             `json:"provenance"`
	SourceGitCommit        string             `json:"sourceGitCommit"`
	PracticeID             string             `json:"practiceId"`
	OfficeKey              string             `json:"officeKey"`
	Sections               int                `json:"sections"`
}

// preparePublication captures one authorized office snapshot. Unchanged is a
// read-only observation at this statement; a later legitimate publication may
// supersede it. Changed sources use the captured revision as ReplaceCorpus's CAS
// expectation, never retrying over another operator's concurrent publication.
func preparePublication(ctx context.Context, pool *pgxpool.Pool, command knowledge.ImportCommand, email string, automatic bool) (knowledge.ImportCommand, *publicationReceipt, error) {
	if err := pool.QueryRow(ctx, `SELECT user_subject FROM access_platform_operators WHERE email=$1 AND user_subject IS NOT NULL`, email).Scan(&command.ActorSubject); err != nil {
		return command, nil, errors.New("import requires an existing bound Platform Operator")
	}
	if err := knowledge.ValidateImport(command); err != nil {
		return command, nil, err
	}
	if !automatic {
		return command, nil, nil
	}
	var revision *knowledge.Revision
	var existing []knowledge.Section
	var provenance string
	err := pool.QueryRow(ctx, `SELECT
 CASE WHEN r.id IS NULL THEN 'null'::jsonb ELSE jsonb_build_object('id',r.id,'contentHash',r.content_hash,'model',r.embedding_model,'dimensions',r.embedding_dimensions,'createdAt',r.created_at) END,
 COALESCE(r.provenance,''),
 COALESCE((SELECT jsonb_agg(jsonb_build_object('id',p.section_id,'title',p.title,'text',p.text)) FROM knowledge_passages p WHERE p.revision_id=r.id),'[]'::jsonb)
 FROM access_abita_office_locations route
 LEFT JOIN knowledge_corpora c ON c.practice_id=route.practice_id AND c.office_key=route.office_key
 LEFT JOIN knowledge_revisions r ON r.id=c.revision_id
 WHERE route.practice_id=$1 AND route.office_key=$2`, command.PracticeID, command.OfficeKey).Scan(&revision, &provenance, &existing)
	if err != nil {
		return command, nil, errors.New("could not read authorized office corpus snapshot")
	}
	if revision != nil && equalSections(existing, command.Sections) {
		return command, &publicationReceipt{
			Unchanged: true, ActiveRevisionVerified: true,
			Revision: *revision, Provenance: provenance,
			SourceGitCommit: strings.TrimPrefix(command.Provenance, "git:"),
			PracticeID:      command.PracticeID, OfficeKey: command.OfficeKey, Sections: len(existing),
		}, nil
	}
	command.ExpectedRevisionID = ""
	if revision != nil {
		command.ExpectedRevisionID = revision.ID
	}
	command.ID = uuid.NewSHA1(uuid.MustParse(command.ID), []byte(command.ExpectedRevisionID)).String()
	return command, nil, nil
}

func equalSections(existing, desired []knowledge.Section) bool {
	if len(existing) != len(desired) {
		return false
	}
	byID := make(map[string]knowledge.Section, len(existing))
	for _, section := range existing {
		byID[section.ID] = section
	}
	for _, section := range desired {
		if byID[section.ID] != section {
			return false
		}
	}
	return true
}
