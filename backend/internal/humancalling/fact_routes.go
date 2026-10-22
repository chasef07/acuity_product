package humancalling

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
)

type callLegRole string

const (
	roleCaller      callLegRole = "CALLER"
	roleStaff       callLegRole = "STAFF"
	roleDestination callLegRole = "DESTINATION"
)

var callLegRoles = []callLegRole{roleCaller, roleStaff, roleDestination}

type clientStateKind string

const (
	kindAnswer                  clientStateKind = "answer"
	kindRingWindow              clientStateKind = "ring_window"
	kindDial                    clientStateKind = "dial"
	kindOutboundMedia           clientStateKind = "outbound_media"
	kindBridge                  clientStateKind = "bridge"
	kindVoicemailGreeting       clientStateKind = "voicemail_greeting"
	kindVoicemailRecording      clientStateKind = "voicemail_recording"
	kindStaffTransferTarget     clientStateKind = "staff_transfer_target"
	kindStaffTransferSource     clientStateKind = "staff_transfer_source"
	kindReconciled              clientStateKind = "reconciled"
	kindReconciledAbsent        clientStateKind = "reconciled_absent"
	kindCleanup                 clientStateKind = "cleanup"
	kindStaffHangup             clientStateKind = "staff_hangup"
	kindCleanupRetry            clientStateKind = "cleanup_retry"
	kindLateDialCleanup         clientStateKind = "late_dial_cleanup"
	kindLateFactCleanup         clientStateKind = "late_fact_cleanup"
	kindRoutingFailure          clientStateKind = "routing_failure"
	kindVoicemailComplete       clientStateKind = "voicemail_complete"
	kindTransferTerminalCleanup clientStateKind = "transfer_terminal_cleanup"
	kindTransferredCleanup      clientStateKind = "transferred_cleanup"
	kindTransferFailedCleanup   clientStateKind = "transfer_failed_cleanup"
	kindUnrecognized            clientStateKind = "unrecognized"
)

var clientStateKinds = []clientStateKind{
	kindAnswer, kindRingWindow, kindDial, kindOutboundMedia, kindBridge,
	kindVoicemailGreeting, kindVoicemailRecording, kindStaffTransferTarget,
	kindStaffTransferSource, kindReconciled, kindReconciledAbsent,
	kindCleanup, kindStaffHangup, kindCleanupRetry, kindLateDialCleanup,
	kindLateFactCleanup, kindRoutingFailure, kindVoicemailComplete,
	kindTransferTerminalCleanup, kindTransferredCleanup, kindTransferFailedCleanup,
}

var hangupRequestedKinds = []clientStateKind{
	kindCleanup, kindStaffHangup, kindCleanupRetry, kindLateDialCleanup,
	kindLateFactCleanup, kindRoutingFailure, kindVoicemailComplete,
	kindTransferTerminalCleanup, kindTransferredCleanup, kindTransferFailedCleanup,
}

func hangupRequested(kind clientStateKind) bool {
	return slices.Contains(hangupRequestedKinds, kind)
}

type callLegClientState struct {
	Version    int             `json:"v"`
	CallID     string          `json:"call"`
	CallLegID  string          `json:"call_leg"`
	Role       callLegRole     `json:"role"`
	Kind       clientStateKind `json:"kind,omitempty"`
	TransferID string          `json:"transfer,omitempty"`
}

func encodeCallLegClientState(
	callID string,
	callLegID string,
	role callLegRole,
	kind clientStateKind,
) string {
	return encodeStaffTransferClientState(callID, callLegID, role, kind, "")
}

func encodeStaffTransferClientState(
	callID string,
	callLegID string,
	role callLegRole,
	kind clientStateKind,
	transferID string,
) string {
	value, _ := json.Marshal(callLegClientState{
		Version:    2,
		CallID:     callID,
		CallLegID:  callLegID,
		Role:       role,
		Kind:       kind,
		TransferID: transferID,
	})
	return base64.StdEncoding.EncodeToString(value)
}

func parseCallLegClientState(value string) (callLegClientState, bool) {
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(decoded) > 512 {
		return callLegClientState{}, false
	}
	var state callLegClientState
	if err := json.Unmarshal(decoded, &state); err != nil ||
		state.Version != 2 || state.CallID == "" || state.CallLegID == "" ||
		!slices.Contains(callLegRoles, state.Role) {
		return callLegClientState{}, false
	}
	if !slices.Contains(clientStateKinds, state.Kind) {
		state.Kind = kindUnrecognized
	}
	return state, true
}

type factVerdict string

const (
	factApplied  factVerdict = "APPLIED"
	factObsolete factVerdict = "OBSOLETE"
	factWaiting  factVerdict = "WAITING_FOR_RELATED_FACT"
	factConflict factVerdict = "CONFLICT"
	factRejected factVerdict = "HANDOFF_REJECTED"
)

func (verdict factVerdict) err() error {
	switch verdict {
	case factApplied:
		return nil
	case factObsolete:
		return errTerminalOrObsoleteProviderFact
	case factWaiting:
		return errRelatedFactPending
	case factRejected:
		return ErrInvalidHandoff
	default:
		return ErrConflict
	}
}

func commitFact(ctx context.Context, tx pgx.Tx) (factVerdict, error) {
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	return factApplied, nil
}

type factRoute struct {
	fact FactType
	role callLegRole
	kind clientStateKind
}

type factHandler func(*Module, context.Context, ProviderFact, callLegClientState) (factVerdict, error)

var providerFactRoutes = routeProviderFacts()

func routeProviderFacts() map[factRoute]factHandler {
	routes := map[factRoute]factHandler{}
	add := func(handler factHandler, facts []FactType, roles []callLegRole, kinds ...[]clientStateKind) {
		for _, fact := range facts {
			for _, role := range roles {
				for _, kind := range slices.Concat(kinds...) {
					key := factRoute{fact: fact, role: role, kind: kind}
					if _, exists := routes[key]; exists {
						panic(fmt.Sprintf("provider fact route %+v is declared twice", key))
					}
					routes[key] = handler
				}
			}
		}
	}
	withoutClientState := func(handler factHandler, facts ...FactType) {
		add(handler, facts, []callLegRole{""}, []clientStateKind{""})
	}
	facts := func(facts ...FactType) []FactType { return facts }
	roles := func(roles ...callLegRole) []callLegRole { return roles }
	kinds := func(kinds ...clientStateKind) []clientStateKind { return kinds }

	callerKinds := kinds(kindAnswer, kindRingWindow, kindVoicemailGreeting, kindVoicemailRecording)
	dialKinds := kinds(kindDial, kindOutboundMedia)
	transferKinds := kinds(kindStaffTransferTarget, kindStaffTransferSource)
	reconcilerKinds := kinds(kindReconciled, kindReconciledAbsent)
	connectedHangupKinds := kinds(kindCleanup, kindStaffHangup)
	otherHangupKinds := kinds(kindCleanupRetry, kindLateDialCleanup, kindLateFactCleanup, kindRoutingFailure,
		kindVoicemailComplete, kindTransferTerminalCleanup, kindTransferredCleanup, kindTransferFailedCleanup)
	unrecognized := kinds(kindUnrecognized)
	everyKind := slices.Concat(callerKinds, dialKinds, kinds(kindBridge), transferKinds, reconcilerKinds, hangupRequestedKinds)
	notTransferTarget := slices.Concat(callerKinds, dialKinds,
		kinds(kindBridge, kindStaffTransferSource), reconcilerKinds, hangupRequestedKinds)

	caller, staff, destination := roles(roleCaller), roles(roleStaff), roles(roleDestination)
	dialed := facts(FactCallInitiated, FactCallAnswered)
	bridged := facts(FactCallBridged)
	playback := facts(FactPlaybackStarted, FactPlaybackEnded)
	speech := facts(FactSpeakStarted, FactSpeakEnded)

	withoutClientState((*Module).admitHandoff, FactCallInitiated)
	withoutClientState((*Module).applyCallerAnswered, FactCallAnswered)
	withoutClientState((*Module).applyCorrelatedBridge, FactCallBridged)
	withoutClientState((*Module).applyHangup, FactCallHangup)
	withoutClientState(unexpectedProviderFact, FactPlaybackStarted, FactPlaybackEnded,
		FactSpeakStarted, FactSpeakEnded, FactRecordingSaved, FactRecordingError)

	add((*Module).applyStaffTransferTargetFact, slices.Concat(dialed, bridged), staff, kinds(kindStaffTransferTarget))
	add((*Module).applyStaffInitiated, dialed, staff, notTransferTarget)
	add((*Module).applyOutboundDestinationFact, dialed, destination, notTransferTarget)
	add((*Module).applyCallerAnswered, facts(FactCallAnswered), caller, notTransferTarget)
	add(unexpectedProviderFact, facts(FactCallInitiated), caller, notTransferTarget)
	add(unexpectedProviderFact, dialed, roles(roleCaller, roleDestination), kinds(kindStaffTransferTarget))
	add(unexpectedProviderFact, dialed, callLegRoles, unrecognized)

	add((*Module).applyStaffTransferBridge, bridged, callLegRoles, kinds(kindStaffTransferSource))
	add((*Module).applyBridge, bridged, staff, kinds(kindBridge))
	add((*Module).applyCorrelatedBridge, bridged, staff, kinds(kindOutboundMedia), connectedHangupKinds)
	add((*Module).applyOutboundStaffBridge, bridged, staff, callerKinds, kinds(kindDial), reconcilerKinds,
		otherHangupKinds)
	add((*Module).applyCallerBridge, bridged, caller, callerKinds, dialKinds, kinds(kindBridge), reconcilerKinds, hangupRequestedKinds)
	add((*Module).applyOutboundBridge, bridged, destination, kinds(kindBridge, kindDial), connectedHangupKinds)
	add(unexpectedProviderFact, bridged, destination,
		callerKinds, kinds(kindOutboundMedia, kindStaffTransferTarget), reconcilerKinds, otherHangupKinds)
	add(unexpectedProviderFact, bridged, caller, kinds(kindStaffTransferTarget))
	add(unexpectedProviderFact, bridged, callLegRoles, unrecognized)

	add((*Module).applyHangup, facts(FactCallHangup), callLegRoles, everyKind, unrecognized)

	add((*Module).applyRingWindowFact, playback, caller, kinds(kindRingWindow))
	add(obsoleteAfterHangupRequest, playback, caller, hangupRequestedKinds)
	add(unexpectedProviderFact, playback, caller, kinds(kindAnswer, kindVoicemailGreeting, kindVoicemailRecording),
		dialKinds, kinds(kindBridge), transferKinds, reconcilerKinds, unrecognized)
	add((*Module).applyOutboundRingtoneFact, playback, staff, kinds(kindOutboundMedia), hangupRequestedKinds)
	add(unexpectedProviderFact, playback, staff, callerKinds, kinds(kindDial, kindBridge),
		transferKinds, reconcilerKinds, unrecognized)
	add(unexpectedProviderFact, playback, destination, everyKind, unrecognized)

	add((*Module).applyVoicemailSpeechFact, speech, caller, kinds(kindVoicemailGreeting))
	add(obsoleteAfterHangupRequest, speech, caller, hangupRequestedKinds)
	add(unexpectedProviderFact, speech, caller, kinds(kindAnswer, kindRingWindow, kindVoicemailRecording),
		dialKinds, kinds(kindBridge), transferKinds, reconcilerKinds, unrecognized)
	add(unexpectedProviderFact, speech, roles(roleStaff, roleDestination), everyKind, unrecognized)

	add((*Module).applyVoicemailRecordingSaved, facts(FactRecordingSaved), caller, everyKind)
	add((*Module).applyVoicemailRecordingError, facts(FactRecordingError), caller, kinds(kindVoicemailRecording))
	add(unexpectedProviderFact, facts(FactRecordingError), caller, kinds(kindAnswer, kindRingWindow, kindVoicemailGreeting),
		dialKinds, kinds(kindBridge), transferKinds, reconcilerKinds, hangupRequestedKinds)
	add((*Module).applyConnectedCallRecordingSaved, facts(FactRecordingSaved), roles(roleStaff, roleDestination), everyKind)
	add((*Module).applyConnectedCallRecordingError, facts(FactRecordingError), roles(roleStaff, roleDestination),
		kinds(kindBridge, kindStaffHangup))
	add(unexpectedProviderFact, facts(FactRecordingError), roles(roleStaff, roleDestination), callerKinds, dialKinds,
		transferKinds, reconcilerKinds, kinds(kindCleanup), otherHangupKinds)
	add(unexpectedProviderFact, facts(FactRecordingSaved, FactRecordingError), callLegRoles, unrecognized)
	return routes
}

func unexpectedProviderFact(*Module, context.Context, ProviderFact, callLegClientState) (factVerdict, error) {
	return factConflict, nil
}

func obsoleteAfterHangupRequest(*Module, context.Context, ProviderFact, callLegClientState) (factVerdict, error) {
	return factObsolete, nil
}

func (m *Module) ApplyProviderFact(ctx context.Context, fact ProviderFact) error {
	verdict, err := m.applyProviderFact(ctx, fact)
	if err != nil {
		return err
	}
	return verdict.err()
}

func (m *Module) applyProviderFact(ctx context.Context, fact ProviderFact) (factVerdict, error) {
	if fact.EventID == "" || fact.Type == "" || fact.OccurredAt.IsZero() {
		return "", ErrInvalidInput
	}
	state, _ := parseCallLegClientState(fact.ClientState)
	handler, routed := providerFactRoutes[factRoute{fact: fact.Type, role: state.Role, kind: state.Kind}]
	if !routed {
		return "", fmt.Errorf("%w: unsupported provider fact %s", ErrInvalidInput, fact.Type)
	}
	return handler(m, ctx, fact, state)
}
