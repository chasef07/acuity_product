package humancalling

import (
	"context"
	"encoding/json"
	"fmt"
)

func (m *Module) CallClassificationEvidence(ctx context.Context, practiceID, serviceSubject, sourceCallID string, expectedHandoff bool) (handoffs json.RawMessage, terminalReady, staffConnected, voicemailUnknown bool, err error) {
	err = m.database.QueryRow(ctx, `SELECT
 COALESCE(jsonb_agg(jsonb_build_object('expiresAt',handoff.expires_at,'terminalOutcome',call.terminal_outcome,
  'endedAt',call.ended_at,'staffLegs',(SELECT COALESCE(jsonb_agg(jsonb_build_object('state',leg.state,
   'answeredAt',leg.answered_at,'bridgedAt',leg.bridged_at,'endedAt',leg.ended_at)),'[]'::jsonb)
   FROM human_calling_call_legs leg WHERE leg.call_id=call.id AND leg.role='STAFF'))),'[]'::jsonb),
 COALESCE(bool_and((call.id IS NULL AND handoff.expires_at<=$5) OR
  (call.id IS NOT NULL AND call.ended_at IS NOT NULL AND NOT EXISTS(
   SELECT 1 FROM human_calling_call_legs leg WHERE leg.call_id=call.id AND leg.state NOT IN ('ENDED','FAILED')
  ))),NOT $4::boolean),
 COALESCE(bool_or(EXISTS(SELECT 1 FROM human_calling_call_legs leg
  WHERE leg.call_id=call.id AND leg.role='STAFF' AND leg.bridged_at IS NOT NULL)),false),
 COALESCE(bool_or(EXISTS(SELECT 1 FROM human_calling_voicemails voicemail
  WHERE voicemail.call_id=call.id AND voicemail.practice_id=$1 AND voicemail.outcome='VOICEMAIL')),false)
 FROM human_calling_handoffs handoff LEFT JOIN human_calling_calls call
  ON call.source_handoff_id=handoff.id AND call.practice_id=$1
 WHERE handoff.practice_id=$1 AND handoff.service_subject=$2 AND handoff.source_call_id=$3`, practiceID, serviceSubject, sourceCallID, expectedHandoff, m.now()).Scan(&handoffs, &terminalReady, &staffConnected, &voicemailUnknown)
	if err != nil {
		return nil, false, false, false, fmt.Errorf("load calling classification evidence: %w", err)
	}
	return handoffs, terminalReady, staffConnected, voicemailUnknown, nil
}
