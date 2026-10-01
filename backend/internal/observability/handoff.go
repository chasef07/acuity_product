package observability

func HandoffRejected(reason string) Event {
	return event("acuity_call_center_handoff_rejection",
		"reason", bounded(reason, "provider_identity", "connection", "address",
			"missing_handoff", "ambiguous_handoff"))
}
