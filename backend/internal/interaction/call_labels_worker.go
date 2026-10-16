package interaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chasef07/acuity_product/backend/internal/access"
	productpostgres "github.com/chasef07/acuity_product/backend/internal/postgres"
	"github.com/jackc/pgx/v5"
)

type ClassificationCallingEvidence interface {
	CallClassificationEvidence(context.Context, string, string, string, bool) (handoffs json.RawMessage, terminalReady, staffConnected, voicemailUnknown bool, err error)
}

type ClassificationTaskEvidence interface {
	CallClassificationTasks(context.Context, string, string, string) (json.RawMessage, error)
}

type ClassificationWorker struct {
	database   productpostgres.Database
	classifier *CallLabelClassifier
	calling    ClassificationCallingEvidence
	tasks      ClassificationTaskEvidence
	now        func() time.Time
}

func NewClassificationWorker(database productpostgres.Database, classifier *CallLabelClassifier, calling ClassificationCallingEvidence, tasks ClassificationTaskEvidence) *ClassificationWorker {
	return &ClassificationWorker{database: database, classifier: classifier, calling: calling, tasks: tasks, now: time.Now}
}

func (w *ClassificationWorker) ProcessNextClassification(ctx context.Context) (bool, error) {
	now := w.now()
	if _, err := w.database.Exec(ctx, `UPDATE ai_interaction_classifications SET status='failed',
 error_code='classification_retries_exhausted',claim_token=NULL,lease_expires_at=NULL,updated_at=$1
 WHERE status='processing' AND lease_expires_at<=$1 AND attempts>=3`, now); err != nil {
		return false, fmt.Errorf("recover classification claim: %w", err)
	}
	var id, token string
	err := w.database.QueryRow(ctx, `WITH candidate AS (
 SELECT labels.interaction_id FROM ai_interaction_classifications labels
 JOIN ai_interactions interaction ON interaction.id=labels.interaction_id
 WHERE ((labels.status IN ('pending','failed') AND labels.next_attempt_at<=$1)
 OR (labels.status='processing' AND labels.lease_expires_at<=$1)) AND labels.attempts<3
 AND interaction.lifecycle_stage=3 AND interaction.closeout_payload IS NOT NULL
 ORDER BY labels.next_attempt_at,labels.interaction_id LIMIT 1 FOR UPDATE OF labels SKIP LOCKED
 ) UPDATE ai_interaction_classifications labels SET status='processing',attempts=attempts+1,
 claim_token=gen_random_uuid(),lease_expires_at=$1::timestamptz+interval '1 minute',updated_at=$1
 FROM candidate WHERE labels.interaction_id=candidate.interaction_id
 RETURNING labels.interaction_id::text,labels.claim_token::text`, now).Scan(&id, &token)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim call classification: %w", err)
	}

	evidence, ready, err := w.loadEvidence(ctx, id)
	if err == nil && !ready {
		_, err = w.database.Exec(ctx, `UPDATE ai_interaction_classifications SET status='pending',attempts=attempts-1,
 claim_token=NULL,lease_expires_at=NULL,error_code='awaiting_transfer_evidence',
 next_attempt_at=$3::timestamptz+interval '2 seconds',updated_at=$3
 WHERE interaction_id=$1 AND claim_token=$2::uuid`, id, token, w.now())
		if err != nil {
			return false, fmt.Errorf("defer classification for transfer evidence: %w", err)
		}
		return true, nil
	}
	result := classificationResult{}
	status := "complete"
	if err == nil {
		if !classificationHasConversation(evidence.Transcript) {
			status = "skipped"
			err = errors.New("conversation_evidence_missing")
		} else {
			result, err = w.classifier.classify(ctx, evidence)
			if err != nil {
				status = "failed"
				if evidence.VoicemailContentUnavailable && result.Intent != "" && result.Outcome == "" {
					status = "skipped"
				}
			}
		}
	} else {
		status = "failed"
	}
	errorCode := ""
	if err != nil {
		errorCode = err.Error()
		if strings.HasPrefix(errorCode, "load classification evidence:") {
			errorCode = "classification_evidence_load_failed"
		}
	}
	tx, saveErr := w.database.BeginTx(ctx, pgx.TxOptions{})
	if saveErr != nil {
		return true, fmt.Errorf("begin classification save: %w", saveErr)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var practiceID string
	saveErr = tx.QueryRow(ctx, `UPDATE ai_interaction_classifications labels SET
 intent=COALESCE($3,intent),outcome=COALESCE($4,outcome),status=$5,
 raw_judgments=CASE WHEN $6::jsonb IS NULL THEN raw_judgments
 ELSE (SELECT jsonb_agg(attempt.value ORDER BY attempt.ordinality) FROM (
  SELECT value,ordinality FROM jsonb_array_elements(COALESCE(raw_judgments,'[]'::jsonb)||jsonb_build_array($6::jsonb))
  WITH ORDINALITY ORDER BY ordinality DESC LIMIT 3
 ) attempt) END,error_code=$7,
 label_version=$8,model=$9,claim_token=NULL,lease_expires_at=NULL,
 next_attempt_at=$10::timestamptz+make_interval(secs => 30*power(4,attempts-1)::int),updated_at=$10
 FROM ai_interactions interaction
 WHERE labels.interaction_id=$1 AND labels.claim_token=$2::uuid AND interaction.id=labels.interaction_id
 RETURNING interaction.practice_id::text`, id, token, nullIfEmpty(string(result.Intent)), nullIfEmpty(string(result.Outcome)), status, nullIfEmptyJSON(result.Raw), nullIfEmpty(errorCode), CallLabelsVersion, callLabelModel, w.now()).Scan(&practiceID)
	if errors.Is(saveErr, pgx.ErrNoRows) {
		return true, nil
	}
	if saveErr != nil {
		return true, fmt.Errorf("save call classification: %w", saveErr)
	}
	if _, err := access.New(w.database, nil).RecordWorkspaceChange(ctx, tx, practiceID); err != nil {
		return true, fmt.Errorf("publish call classification: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return true, fmt.Errorf("commit call classification: %w", err)
	}
	if status == "failed" {
		return true, fmt.Errorf("call classification failed: %s", errorCode)
	}
	return true, nil
}

func (w *ClassificationWorker) loadEvidence(ctx context.Context, id string) (classificationEvidence, bool, error) {
	var evidence classificationEvidence
	var practice, service, source string
	err := w.database.QueryRow(ctx, `SELECT transcript,closeout_payload,practice_id::text,service_subject,source_call_id
 FROM ai_interactions WHERE id=$1`, id).Scan(&evidence.Transcript, &evidence.Closeout, &practice, &service, &source)
	if err != nil {
		return evidence, false, fmt.Errorf("load classification evidence: %w", err)
	}
	if w.calling == nil || w.tasks == nil {
		return evidence, false, errors.New("load classification evidence: readers missing")
	}
	expected := firstRecordString(decodeRecord(evidence.Closeout), "transferStatus") == "accepted"
	var ready bool
	evidence.Handoffs, ready, evidence.StaffConnected, evidence.VoicemailContentUnavailable, err = w.calling.CallClassificationEvidence(ctx, practice, service, source, expected)
	if err != nil {
		return evidence, false, fmt.Errorf("load classification evidence: %w", err)
	}
	if !ready {
		return evidence, false, nil
	}
	evidence.StaffTasks, err = w.tasks.CallClassificationTasks(ctx, practice, service, source)
	if err != nil {
		return evidence, false, fmt.Errorf("load classification evidence: %w", err)
	}
	return evidence, true, nil
}

func classificationHasConversation(transcript json.RawMessage) bool {
	for _, raw := range transcriptItems(decodeRecord(transcript)) {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		role := firstRecordString(item, "role")
		if role == "user" && (strings.TrimSpace(messageText(item)) != "" || firstRecordString(item, "content") != "") {
			return true
		}
	}
	return false
}
