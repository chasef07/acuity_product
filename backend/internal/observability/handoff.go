package observability

// HandoffRejected records the failed admission check without provider or patient
// identifiers. It describes one admission attempt, not a failed patient outcome.
func HandoffRejected(reason string) Event {
	return event("acuity_call_center_handoff_rejection",
		"reason", bounded(reason, "provider_identity", "connection", "address",
			"missing_handoff", "ambiguous_handoff"))
}
