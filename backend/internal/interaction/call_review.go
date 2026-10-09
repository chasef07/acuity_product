package interaction

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chasef07/acuity_product/backend/internal/access"
	"github.com/jackc/pgx/v5"
)

const (
	reviewQueueSize        = 20
	reviewQueueFlagged     = 10
	reviewQueueOverlap     = 3
	reviewMinimumSeconds   = 30
	reviewNoteLimit        = 2000
	reviewDisagreementRows = 200
)

var reviewFailureQuestions = map[string]bool{
	"need_understood":              true,
	"right_help":                   true,
	"clear_and_responsive":         true,
	"office_rules_grounded":        true,
	"appointment_datetime_correct": true,
	"no_results_retried":           true,
	"person_request_honored":       true,
	"staff_task_identified":        true,
}

var ErrReviewLocked = errors.New("call review already submitted")

type ReviewQueueCall struct {
	InteractionID   string     `json:"interactionId"`
	StartedAt       time.Time  `json:"startedAt"`
	LocationName    string     `json:"locationName"`
	DurationSeconds int        `json:"durationSeconds"`
	Sample          string     `json:"sample"`
	Overlap         bool       `json:"overlap"`
	CompletedAt     *time.Time `json:"completedAt,omitempty"`
}

type ReviewQueue struct {
	Date      string            `json:"date"`
	Reviewer  string            `json:"reviewer"`
	Available int               `json:"available"`
	Calls     []ReviewQueueCall `json:"calls"`
}

type ReviewQueueCommand struct {
	Identity   access.Identity
	PracticeID string
	Date       string
	TimeZone   string
}

type ReviewFact struct {
	Question string         `json:"question"`
	Answer   bool           `json:"answer"`
	Detail   map[string]any `json:"detail,omitempty"`
}

type ReviewAnswer struct {
	Question string `json:"question"`
	Answer   bool   `json:"answer"`
	Note     string `json:"note"`
}

type ReviewJudgeAnswer struct {
	Question    string   `json:"question"`
	Answer      bool     `json:"answer"`
	Probability *float64 `json:"probability,omitempty"`
	Version     string   `json:"version"`
}

type CallReview struct {
	InteractionID string              `json:"interactionId"`
	Assigned      bool                `json:"assigned"`
	Sample        string              `json:"sample,omitempty"`
	Facts         []ReviewFact        `json:"facts"`
	Questions     []string            `json:"questions"`
	Submitted     bool                `json:"submitted"`
	Note          string              `json:"note"`
	QuestionIdea  string              `json:"questionIdea"`
	Answers       []ReviewAnswer      `json:"answers"`
	Judge         []ReviewJudgeAnswer `json:"judge"`
}

type CallReviewSubmission struct {
	Answers      []ReviewAnswer
	Note         string
	QuestionIdea string
}

type JudgeAccuracyRow struct {
	Question       string `json:"question"`
	JudgeVersion   string `json:"judgeVersion"`
	Sample         int    `json:"sample"`
	Agreed         int    `json:"agreed"`
	HumanNo        int    `json:"humanNo"`
	FailuresCaught int    `json:"failuresCaught"`
	FalseAlarms    int    `json:"falseAlarms"`
}

type JudgeDisagreement struct {
	InteractionID    string    `json:"interactionId"`
	StartedAt        time.Time `json:"startedAt"`
	LocationName     string    `json:"locationName"`
	Question         string    `json:"question"`
	JudgeVersion     string    `json:"judgeVersion"`
	ReviewerEmail    string    `json:"reviewerEmail"`
	Human            bool      `json:"human"`
	Judge            bool      `json:"judge"`
	JudgeProbability *float64  `json:"judgeProbability,omitempty"`
	Note             string    `json:"note"`
}

type ReviewerAnswer struct {
	ReviewerEmail string `json:"reviewerEmail"`
	Answer        bool   `json:"answer"`
	Note          string `json:"note"`
}

type ReviewerDisagreement struct {
	InteractionID string           `json:"interactionId"`
	StartedAt     time.Time        `json:"startedAt"`
	LocationName  string           `json:"locationName"`
	Question      string           `json:"question"`
	Answers       []ReviewerAnswer `json:"answers"`
}

type GoldenSetCall struct {
	InteractionID string    `json:"interactionId"`
	StartedAt     time.Time `json:"startedAt"`
	LocationName  string    `json:"locationName"`
	Reviewers     []string  `json:"reviewers"`
	Answers       int       `json:"answers"`
	Disagreements int       `json:"disagreements"`
}

type JudgeAccuracy struct {
	ReviewedCalls         int                    `json:"reviewedCalls"`
	GoldenAnswers         int                    `json:"goldenAnswers"`
	GoldenSet             []GoldenSetCall        `json:"goldenSet"`
	Unjudged              int                    `json:"unjudged"`
	Rows                  []JudgeAccuracyRow     `json:"rows"`
	JudgeDisagreements    []JudgeDisagreement    `json:"judgeDisagreements"`
	ReviewerDisagreements []ReviewerDisagreement `json:"reviewerDisagreements"`
}

func reviewDay(date, timeZone string) (time.Time, time.Time, bool) {
	zone, valid := reportingZone(timeZone)
	if !valid {
		return time.Time{}, time.Time{}, false
	}
	day, err := time.ParseInLocation("2006-01-02", date, zone)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	return day, day.AddDate(0, 0, 1), true
}

func reviewRank(interactionID, date string) string {
	sum := sha256.Sum256([]byte(date + ":" + interactionID))
	return hex.EncodeToString(sum[:])
}

func reviewFlagged(answers map[string]bool, evaluation json.RawMessage) bool {
	for question, answer := range answers {
		if !answer && reviewFailureQuestions[question] {
			return true
		}
	}
	return len(EvaluationReviewReasons(evaluation)) > 0
}

func (m *Module) OpenReviewQueue(ctx context.Context, command ReviewQueueCommand) (ReviewQueue, error) {
	from, to, valid := reviewDay(command.Date, command.TimeZone)
	if !valid || !validUUID(command.PracticeID) || command.Identity.Subject == "" || to.After(m.now()) {
		return ReviewQueue{}, ErrInvalidInput
	}
	if err := m.available(); err != nil {
		return ReviewQueue{}, err
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ReviewQueue{}, fmt.Errorf("begin review queue: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	authorization, err := m.authorize(ctx, tx, command.Identity, command.PracticeID, "", false, audienceOperator)
	if err != nil {
		return ReviewQueue{}, err
	}
	locations := authorizedLocationIDs(authorization, "")
	if len(locations) == 0 {
		return ReviewQueue{}, ErrDenied
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('ai_call_review_queue'), hashtext($1 || $2))`, command.PracticeID, command.Date); err != nil {
		return ReviewQueue{}, fmt.Errorf("lock review queue: %w", err)
	}
	rows, err := tx.Query(ctx, `
		SELECT i.id::text, i.closeout_payload -> 'evaluation',
			COALESCE((SELECT jsonb_object_agg(a.question, a.answer) FROM ai_interaction_scorecard_answers a WHERE a.interaction_id = i.id), '{}'::jsonb),
			COALESCE((SELECT jsonb_agg(DISTINCT jsonb_build_object('reviewer', r.reviewer, 'overlap', r.overlap)) FROM ai_call_review_assignments r
				WHERE r.practice_id = i.practice_id AND r.review_date = $5::date AND r.interaction_id = i.id), '[]'::jsonb)
		FROM ai_interactions i
		WHERE i.practice_id = $1 AND i.location_id = ANY($2::uuid[])
			AND i.started_at >= $3 AND i.started_at < $4
			AND i.lifecycle_stage = 3 AND i.ended_at >= i.started_at + make_interval(secs => $6)
		LIMIT $7
	`, command.PracticeID, locations, from, to, command.Date, reviewMinimumSeconds, analyticsRowLimit+1)
	if err != nil {
		return ReviewQueue{}, fmt.Errorf("query review candidates: %w", err)
	}
	type candidate struct {
		id, rank             string
		flagged, mine, other bool
		overlap              bool
	}
	candidates := []candidate{}
	for rows.Next() {
		var id string
		var evaluation, answersRaw, assignmentsRaw json.RawMessage
		if err := rows.Scan(&id, &evaluation, &answersRaw, &assignmentsRaw); err != nil {
			rows.Close()
			return ReviewQueue{}, fmt.Errorf("read review candidate: %w", err)
		}
		answers := map[string]bool{}
		var assignments []struct {
			Reviewer string `json:"reviewer"`
			Overlap  bool   `json:"overlap"`
		}
		if json.Unmarshal(answersRaw, &answers) != nil || json.Unmarshal(assignmentsRaw, &assignments) != nil {
			rows.Close()
			return ReviewQueue{}, fmt.Errorf("decode review candidate")
		}
		item := candidate{id: id, rank: reviewRank(id, command.Date), flagged: reviewFlagged(answers, evaluation)}
		for _, assignment := range assignments {
			item.mine = item.mine || assignment.Reviewer == command.Identity.Subject
			item.other = item.other || (assignment.Reviewer != command.Identity.Subject && !assignment.Overlap)
			item.overlap = item.overlap || assignment.Overlap
		}
		candidates = append(candidates, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return ReviewQueue{}, fmt.Errorf("read review candidates: %w", err)
	}
	if len(candidates) > analyticsRowLimit {
		return ReviewQueue{}, fmt.Errorf("review candidates exceed bounded window")
	}
	sort.Slice(candidates, func(left, right int) bool { return candidates[left].rank < candidates[right].rank })
	assignedAlready := false
	for _, item := range candidates {
		assignedAlready = assignedAlready || item.mine
	}
	if !assignedAlready && len(candidates) > 0 {
		type pick struct {
			id, sample string
			overlap    bool
		}
		picks := []pick{}
		chosen := map[string]bool{}
		sample := func(item candidate) string {
			if item.flagged {
				return "flagged"
			}
			return "random"
		}
		existingOverlap := false
		for _, item := range candidates {
			existingOverlap = existingOverlap || item.overlap
		}
		for _, item := range candidates {
			if len(picks) < reviewQueueOverlap && ((existingOverlap && item.overlap) || (!existingOverlap && !item.other)) {
				picks = append(picks, pick{item.id, sample(item), true})
				chosen[item.id] = true
			}
		}
		flagged := 0
		for _, item := range picks {
			if item.sample == "flagged" {
				flagged++
			}
		}
		for _, wantFlagged := range []bool{true, false} {
			for _, item := range candidates {
				if len(picks) >= reviewQueueSize || (wantFlagged && flagged >= reviewQueueFlagged) {
					break
				}
				if chosen[item.id] || item.other || item.overlap || (wantFlagged && !item.flagged) {
					continue
				}
				picks = append(picks, pick{item.id, sample(item), false})
				chosen[item.id] = true
				if item.flagged {
					flagged++
				}
			}
		}
		for position, item := range picks {
			if _, err := tx.Exec(ctx, `
				INSERT INTO ai_call_review_assignments (practice_id, review_date, reviewer, reviewer_email, interaction_id, sample, overlap, position)
				VALUES ($1, $2::date, $3, $4, $5, $6, $7, $8)
			`, command.PracticeID, command.Date, command.Identity.Subject, command.Identity.Email, item.id, item.sample, item.overlap, position); err != nil {
				return ReviewQueue{}, fmt.Errorf("assign review call: %w", err)
			}
		}
		if len(picks) > 0 {
			if err := m.access.AuditOperatorMutation(ctx, tx, authorization, access.OperatorMutationAudit{Action: "ai_call_review.queue_assigned", ResourceType: "ai_call_review_queue", ResourceID: command.Date, ResourceVersion: 1, OccurredAt: m.now()}); err != nil {
				return ReviewQueue{}, err
			}
		}
	}
	queue := ReviewQueue{Date: command.Date, Reviewer: command.Identity.Email, Available: len(candidates), Calls: []ReviewQueueCall{}}
	listed, err := tx.Query(ctx, `
		SELECT r.interaction_id::text, i.started_at, i.location_id::text,
			GREATEST(0, EXTRACT(EPOCH FROM (i.ended_at - i.started_at)))::int,
			r.sample, r.overlap, r.completed_at
		FROM ai_call_review_assignments r
		JOIN ai_interactions i ON i.id = r.interaction_id
		WHERE r.practice_id = $1 AND r.review_date = $2::date AND r.reviewer = $3 AND i.location_id = ANY($4::uuid[])
		ORDER BY r.position
	`, command.PracticeID, command.Date, command.Identity.Subject, locations)
	if err != nil {
		return ReviewQueue{}, fmt.Errorf("list review queue: %w", err)
	}
	for listed.Next() {
		var call ReviewQueueCall
		if err := listed.Scan(&call.InteractionID, &call.StartedAt, &call.LocationName, &call.DurationSeconds, &call.Sample, &call.Overlap, &call.CompletedAt); err != nil {
			listed.Close()
			return ReviewQueue{}, fmt.Errorf("read review queue: %w", err)
		}
		call.LocationName = locationNames(authorization)[call.LocationName]
		queue.Calls = append(queue.Calls, call)
	}
	listed.Close()
	if err := listed.Err(); err != nil {
		return ReviewQueue{}, fmt.Errorf("read review queue: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return ReviewQueue{}, fmt.Errorf("commit review queue: %w", err)
	}
	return queue, nil
}

func reviewQuestions(facts map[string]bool) []string {
	questions := []string{}
	for _, question := range ScorecardQuestions {
		if question.Source != "judge" || (question.Key == "appointment_datetime_correct" && !facts["scheduling_tool_called"]) {
			continue
		}
		questions = append(questions, question.Key)
	}
	return questions
}

func (m *Module) ReadCallReview(ctx context.Context, identity access.Identity, interactionID string) (CallReview, error) {
	return m.callReview(ctx, identity, interactionID, nil)
}

func (m *Module) SubmitCallReview(ctx context.Context, identity access.Identity, interactionID string, submission CallReviewSubmission) (CallReview, error) {
	submission.Note = strings.TrimSpace(submission.Note)
	submission.QuestionIdea = strings.TrimSpace(submission.QuestionIdea)
	if utf8.RuneCountInString(submission.Note) > reviewNoteLimit || !utf8.ValidString(submission.Note) ||
		utf8.RuneCountInString(submission.QuestionIdea) > reviewNoteLimit || !utf8.ValidString(submission.QuestionIdea) {
		return CallReview{}, ErrInvalidInput
	}
	for index := range submission.Answers {
		submission.Answers[index].Note = strings.TrimSpace(submission.Answers[index].Note)
		if utf8.RuneCountInString(submission.Answers[index].Note) > reviewNoteLimit || !utf8.ValidString(submission.Answers[index].Note) {
			return CallReview{}, ErrInvalidInput
		}
	}
	return m.callReview(ctx, identity, interactionID, &submission)
}

func (m *Module) callReview(ctx context.Context, identity access.Identity, interactionID string, submission *CallReviewSubmission) (CallReview, error) {
	if identity.Subject == "" {
		return CallReview{}, ErrInvalidInput
	}
	tx, authorization, err := m.beginInteractionAccess(ctx, identity, interactionID, submission != nil, audienceOperator)
	if err != nil {
		return CallReview{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	review := CallReview{InteractionID: interactionID, Facts: []ReviewFact{}, Answers: []ReviewAnswer{}, Judge: []ReviewJudgeAnswer{}}
	facts := map[string]bool{}
	judge := map[string]ReviewJudgeAnswer{}
	rows, err := tx.Query(ctx, `
		SELECT question, source, answer, probability, scorecard_version, detail
		FROM ai_interaction_scorecard_answers WHERE interaction_id = $1
	`, interactionID)
	if err != nil {
		return CallReview{}, fmt.Errorf("read scorecard answers: %w", err)
	}
	for rows.Next() {
		var question, source, version string
		var answer bool
		var probability *float64
		var detail map[string]any
		if err := rows.Scan(&question, &source, &answer, &probability, &version, &detail); err != nil {
			rows.Close()
			return CallReview{}, fmt.Errorf("read scorecard answer: %w", err)
		}
		if source == "code" {
			facts[question] = answer
			review.Facts = append(review.Facts, ReviewFact{Question: question, Answer: answer, Detail: detail})
		} else {
			judge[question] = ReviewJudgeAnswer{Question: question, Answer: answer, Probability: probability, Version: version}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return CallReview{}, fmt.Errorf("read scorecard answers: %w", err)
	}
	sort.Slice(review.Facts, func(left, right int) bool {
		return scorecardQuestionByKey[review.Facts[left].Question].Code < scorecardQuestionByKey[review.Facts[right].Question].Code
	})
	review.Questions = reviewQuestions(facts)

	var reviewDate string
	err = tx.QueryRow(ctx, `
		SELECT sample, note, question_idea, completed_at IS NOT NULL, review_date::text
		FROM ai_call_review_assignments
		WHERE practice_id = $1 AND reviewer = $2 AND interaction_id = $3
		ORDER BY review_date DESC LIMIT 1
		FOR UPDATE
	`, authorization.Practice.ID, identity.Subject, interactionID).Scan(&review.Sample, &review.Note, &review.QuestionIdea, &review.Submitted, &reviewDate)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return CallReview{}, fmt.Errorf("read review assignment: %w", err)
	}
	review.Assigned = err == nil

	if submission != nil {
		if !review.Assigned {
			return CallReview{}, ErrInvalidInput
		}
		var answered bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ai_call_reviews WHERE interaction_id = $1 AND reviewer = $2)`, interactionID, identity.Subject).Scan(&answered); err != nil {
			return CallReview{}, fmt.Errorf("read call review state: %w", err)
		}
		if review.Submitted || answered {
			return CallReview{}, ErrReviewLocked
		}
		applicable := map[string]bool{}
		for _, question := range review.Questions {
			applicable[question] = true
		}
		answers := map[string]ReviewAnswer{}
		for _, answer := range submission.Answers {
			if !applicable[answer.Question] || answers[answer.Question].Question != "" {
				return CallReview{}, ErrInvalidInput
			}
			answers[answer.Question] = answer
		}
		if booking, answered := answers["booking_requested"]; answered && !booking.Answer && answers["time_offered"].Question != "" {
			return CallReview{}, ErrInvalidInput
		}
		for _, question := range review.Questions {
			required := question != "time_offered" || answers["booking_requested"].Answer
			if _, answered := answers[question]; required && !answered {
				return CallReview{}, ErrInvalidInput
			}
		}
		now := m.now()
		for _, answer := range answers {
			snapshot, judged := judge[answer.Question]
			var judgeAnswer *bool
			var judgeVersion any
			if judged {
				judgeAnswer = &snapshot.Answer
				judgeVersion = snapshot.Version
			}
			if _, err := tx.Exec(ctx, `
				INSERT INTO ai_call_reviews (
					interaction_id, practice_id, question, reviewer, reviewer_email, answer, note,
					review_date, sample, scorecard_version, judge_answer, judge_probability, judge_version, reviewed_at
				) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::date, $9, $10, $11, $12, $13, $14)
			`, interactionID, authorization.Practice.ID, answer.Question, identity.Subject, identity.Email, answer.Answer, answer.Note,
				reviewDate, review.Sample, ScorecardJudgeVersion, judgeAnswer, snapshot.Probability, judgeVersion, now); err != nil {
				return CallReview{}, fmt.Errorf("save call review: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `
			UPDATE ai_call_review_assignments SET note = $4, question_idea = $5, completed_at = $6
			WHERE practice_id = $1 AND reviewer = $2 AND interaction_id = $3 AND review_date = $7::date
		`, authorization.Practice.ID, identity.Subject, interactionID, submission.Note, submission.QuestionIdea, now, reviewDate); err != nil {
			return CallReview{}, fmt.Errorf("complete review assignment: %w", err)
		}
		if err := m.access.AuditOperatorMutation(ctx, tx, authorization, access.OperatorMutationAudit{Action: "ai_call_review.submitted", ResourceType: "ai_interaction", ResourceID: interactionID, ResourceVersion: 1, OccurredAt: now}); err != nil {
			return CallReview{}, err
		}
		review.Submitted, review.Note, review.QuestionIdea = true, submission.Note, submission.QuestionIdea
	}

	saved, err := tx.Query(ctx, `
		SELECT question, answer, note, judge_answer, judge_probability, COALESCE(judge_version, '')
		FROM ai_call_reviews WHERE interaction_id = $1 AND reviewer = $2
	`, interactionID, identity.Subject)
	if err != nil {
		return CallReview{}, fmt.Errorf("read call review: %w", err)
	}
	snapshots := map[string]ReviewJudgeAnswer{}
	for saved.Next() {
		var answer ReviewAnswer
		var judgeAnswer *bool
		var probability *float64
		var version string
		if err := saved.Scan(&answer.Question, &answer.Answer, &answer.Note, &judgeAnswer, &probability, &version); err != nil {
			saved.Close()
			return CallReview{}, fmt.Errorf("read call review: %w", err)
		}
		review.Answers = append(review.Answers, answer)
		if judgeAnswer != nil {
			snapshots[answer.Question] = ReviewJudgeAnswer{Question: answer.Question, Answer: *judgeAnswer, Probability: probability, Version: version}
		}
	}
	saved.Close()
	if err := saved.Err(); err != nil {
		return CallReview{}, fmt.Errorf("read call review: %w", err)
	}
	if len(review.Answers) > 0 {
		review.Submitted = true
	}
	if review.Submitted {
		for _, question := range ScorecardQuestions {
			if snapshot, found := snapshots[question.Key]; found {
				review.Judge = append(review.Judge, snapshot)
			} else if current, found := judge[question.Key]; found {
				review.Judge = append(review.Judge, current)
			}
		}
	}
	order := map[string]int{}
	for index, question := range ScorecardQuestions {
		order[question.Key] = index
	}
	sort.Slice(review.Answers, func(left, right int) bool {
		return order[review.Answers[left].Question] < order[review.Answers[right].Question]
	})
	if err := tx.Commit(ctx); err != nil {
		return CallReview{}, fmt.Errorf("commit call review: %w", err)
	}
	return review, nil
}

func (m *Module) QueryJudgeAccuracy(ctx context.Context, identity access.Identity, practiceID string) (JudgeAccuracy, error) {
	tx, authorization, locations, err := m.beginAnalyticsAuthorization(ctx, identity, practiceID, "", audienceOperator)
	if err != nil {
		return JudgeAccuracy{}, err
	}
	names := locationNames(authorization)
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		SELECT r.interaction_id::text, i.started_at, i.location_id::text, r.question, r.reviewer_email, r.answer, r.note,
			r.judge_answer, r.judge_probability, COALESCE(r.judge_version, '')
		FROM ai_call_reviews r
		JOIN ai_interactions i ON i.id = r.interaction_id
		WHERE r.practice_id = $1 AND i.location_id = ANY($2::uuid[])
		ORDER BY i.started_at DESC, r.interaction_id, r.question, r.reviewer_email
		LIMIT $3
	`, practiceID, locations, analyticsRowLimit+1)
	if err != nil {
		return JudgeAccuracy{}, fmt.Errorf("query judge accuracy: %w", err)
	}
	result := JudgeAccuracy{Rows: []JudgeAccuracyRow{}, JudgeDisagreements: []JudgeDisagreement{}, ReviewerDisagreements: []ReviewerDisagreement{}}
	type groupKey struct{ question, version string }
	groups := map[groupKey]*JudgeAccuracyRow{}
	calls := map[string]*GoldenSetCall{}
	callList := []string{}
	type callQuestion struct{ interaction, question string }
	byCallQuestion := map[callQuestion]*ReviewerDisagreement{}
	callOrder := []callQuestion{}
	count := 0
	for rows.Next() {
		count++
		var item JudgeDisagreement
		var judgeAnswer *bool
		if err := rows.Scan(&item.InteractionID, &item.StartedAt, &item.LocationName, &item.Question, &item.ReviewerEmail, &item.Human, &item.Note, &judgeAnswer, &item.JudgeProbability, &item.JudgeVersion); err != nil {
			rows.Close()
			return JudgeAccuracy{}, fmt.Errorf("read judge accuracy: %w", err)
		}
		item.LocationName = names[item.LocationName]
		if calls[item.InteractionID] == nil {
			calls[item.InteractionID] = &GoldenSetCall{InteractionID: item.InteractionID, StartedAt: item.StartedAt, LocationName: item.LocationName, Reviewers: []string{}}
			callList = append(callList, item.InteractionID)
		}
		golden := calls[item.InteractionID]
		golden.Answers++
		result.GoldenAnswers++
		if len(golden.Reviewers) == 0 || golden.Reviewers[len(golden.Reviewers)-1] != item.ReviewerEmail {
			seen := false
			for _, reviewer := range golden.Reviewers {
				seen = seen || reviewer == item.ReviewerEmail
			}
			if !seen {
				golden.Reviewers = append(golden.Reviewers, item.ReviewerEmail)
			}
		}
		key := callQuestion{item.InteractionID, item.Question}
		if byCallQuestion[key] == nil {
			byCallQuestion[key] = &ReviewerDisagreement{InteractionID: item.InteractionID, StartedAt: item.StartedAt, LocationName: item.LocationName, Question: item.Question}
			callOrder = append(callOrder, key)
		}
		byCallQuestion[key].Answers = append(byCallQuestion[key].Answers, ReviewerAnswer{ReviewerEmail: item.ReviewerEmail, Answer: item.Human, Note: item.Note})
		if judgeAnswer == nil {
			result.Unjudged++
			continue
		}
		group := groupKey{item.Question, item.JudgeVersion}
		if groups[group] == nil {
			groups[group] = &JudgeAccuracyRow{Question: item.Question, JudgeVersion: item.JudgeVersion}
		}
		row := groups[group]
		item.Judge = *judgeAnswer
		row.Sample++
		if !item.Human {
			row.HumanNo++
		}
		switch {
		case item.Human == item.Judge:
			row.Agreed++
			if !item.Human {
				row.FailuresCaught++
			}
		case item.Human:
			row.FalseAlarms++
		}
		if item.Human != item.Judge {
			golden.Disagreements++
		}
		if item.Human != item.Judge && len(result.JudgeDisagreements) < reviewDisagreementRows {
			result.JudgeDisagreements = append(result.JudgeDisagreements, item)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return JudgeAccuracy{}, fmt.Errorf("read judge accuracy: %w", err)
	}
	if count > analyticsRowLimit {
		return JudgeAccuracy{}, fmt.Errorf("judge accuracy exceeds bounded reporting window")
	}
	for _, key := range callOrder {
		group := byCallQuestion[key]
		seen := map[bool]bool{}
		for _, answer := range group.Answers {
			seen[answer.Answer] = true
		}
		if len(seen) > 1 && len(result.ReviewerDisagreements) < reviewDisagreementRows {
			result.ReviewerDisagreements = append(result.ReviewerDisagreements, *group)
		}
	}
	order := map[string]int{}
	for index, question := range ScorecardQuestions {
		order[question.Key] = index
	}
	for _, row := range groups {
		result.Rows = append(result.Rows, *row)
	}
	sort.Slice(result.Rows, func(left, right int) bool {
		if result.Rows[left].Question != result.Rows[right].Question {
			return order[result.Rows[left].Question] < order[result.Rows[right].Question]
		}
		return result.Rows[left].JudgeVersion < result.Rows[right].JudgeVersion
	})
	result.ReviewedCalls = len(calls)
	result.GoldenSet = []GoldenSetCall{}
	for _, id := range callList {
		result.GoldenSet = append(result.GoldenSet, *calls[id])
	}
	if err := tx.Commit(ctx); err != nil {
		return JudgeAccuracy{}, fmt.Errorf("commit judge accuracy: %w", err)
	}
	return result, nil
}

type GoldenSetEntry struct {
	Call     string `json:"call"`
	Question string `json:"question"`
	Judge    string `json:"judge"`
	Human    string `json:"human"`
	Note     string `json:"note"`
	Date     string `json:"date"`
	Sample   string `json:"sample"`
	Version  string `json:"scorecard"`
	JevV1    string `json:"jev_v1"`
}

type GoldenSetImport struct {
	Imported   int      `json:"imported"`
	Unresolved []string `json:"unresolved"`
}

func parseJudgeAnswer(value string) (*bool, *float64) {
	verdict, rest, _ := strings.Cut(strings.TrimSpace(value), " ")
	var answer bool
	switch strings.ToLower(verdict) {
	case "yes":
		answer = true
	case "no":
	default:
		return nil, nil
	}
	var probability float64
	if _, err := fmt.Sscanf(strings.Trim(rest, "() "), "%f", &probability); err != nil || probability < 0 || probability > 1 {
		return &answer, nil
	}
	return &answer, &probability
}

func (m *Module) ImportGoldenSet(ctx context.Context, practiceID string, reviewer access.Identity, entries []GoldenSetEntry, importedAt time.Time) (GoldenSetImport, error) {
	if err := m.available(); err != nil {
		return GoldenSetImport{}, err
	}
	if !validUUID(practiceID) || reviewer.Subject == "" || reviewer.Email == "" {
		return GoldenSetImport{}, ErrInvalidInput
	}
	tx, err := m.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return GoldenSetImport{}, fmt.Errorf("begin golden set import: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result := GoldenSetImport{Unresolved: []string{}}
	unresolved := map[string]bool{}
	for _, entry := range entries {
		if _, known := scorecardQuestionByKey[entry.Judge]; !known || scorecardQuestionByKey[entry.Judge].Source != "judge" || (entry.Human != "yes" && entry.Human != "no") || entry.Version == "" {
			return GoldenSetImport{}, fmt.Errorf("%w: golden set entry %s/%s", ErrInvalidInput, entry.Call, entry.Question)
		}
		if _, err := time.Parse("2006-01-02", entry.Date); err != nil {
			return GoldenSetImport{}, fmt.Errorf("%w: golden set date %q", ErrInvalidInput, entry.Date)
		}
		sample := entry.Sample
		if sample != "random" && sample != "flagged" && sample != "booking_pick" && sample != "manual" {
			return GoldenSetImport{}, fmt.Errorf("%w: golden set sample %q", ErrInvalidInput, entry.Sample)
		}
		var interactionID string
		err := tx.QueryRow(ctx, `SELECT id::text FROM ai_interactions WHERE practice_id = $1 AND source_call_id = $2`, practiceID, entry.Call).Scan(&interactionID)
		if errors.Is(err, pgx.ErrNoRows) {
			if !unresolved[entry.Call] {
				unresolved[entry.Call] = true
				result.Unresolved = append(result.Unresolved, entry.Call)
			}
			continue
		}
		if err != nil {
			return GoldenSetImport{}, fmt.Errorf("resolve golden set call: %w", err)
		}
		judgeAnswer, judgeProbability := parseJudgeAnswer(entry.JevV1)
		var judgeVersion any
		if judgeAnswer != nil {
			judgeVersion = entry.Version
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ai_call_reviews (
				interaction_id, practice_id, question, reviewer, reviewer_email, answer, note,
				review_date, sample, scorecard_version, judge_answer, judge_probability, judge_version, reviewed_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::date, $9, $10, $11, $12, $13, $14)
			ON CONFLICT (interaction_id, question, reviewer) DO UPDATE SET
				reviewer_email = EXCLUDED.reviewer_email, answer = EXCLUDED.answer, note = EXCLUDED.note,
				review_date = EXCLUDED.review_date, sample = EXCLUDED.sample, scorecard_version = EXCLUDED.scorecard_version,
				judge_answer = EXCLUDED.judge_answer, judge_probability = EXCLUDED.judge_probability,
				judge_version = EXCLUDED.judge_version, reviewed_at = EXCLUDED.reviewed_at
		`, interactionID, practiceID, entry.Judge, reviewer.Subject, reviewer.Email, entry.Human == "yes", strings.TrimSpace(entry.Note),
			entry.Date, sample, entry.Version, judgeAnswer, judgeProbability, judgeVersion, importedAt); err != nil {
			return GoldenSetImport{}, fmt.Errorf("import golden set entry: %w", err)
		}
		result.Imported++
	}
	if err := tx.Commit(ctx); err != nil {
		return GoldenSetImport{}, fmt.Errorf("commit golden set import: %w", err)
	}
	return result, nil
}
