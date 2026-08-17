# Acuity Portal Vision

Acuity Portal is the shared operating workspace where medical-practice teams
turn patient communication into accountable action.

Its job is to make work that needs human attention visible, give staff the
context to act, make responsibility obvious, and preserve evidence that the
patient's need reached a real outcome.

## North Star

Every patient need that requires staff action becomes accountable work and
remains visible until it reaches a real outcome.

Success means:

- a resolved interaction creates no unnecessary Task;
- an unresolved need becomes one durable Task with a clear next action;
- staff can see what needs doing, who owns it when assigned, and its Activity
  timeline; and
- completion records what happened without erasing the history.

Activity without an outcome is not success.

## Product Principles

- **Organize around work, not channels.** Calls, voicemails, messages, notes,
  and AI Interactions are evidence around patient work, not separate inboxes to
  reconcile from memory.
- **Make unresolved work durable.** A patient need that still requires action
  must not live only in a conversation, notification, or staff member's memory.
- **Avoid queue noise.** When an interaction resolves the need, preserve the
  evidence without creating work that no one needs to do.
- **One Task, one accountable outcome.** Keep the need, next action, ownership,
  and definition of completion understandable in one pass.
- **Share visibility; clarify responsibility.** Assignment establishes who is
  responsible without hiding the Task from the rest of the authorized team.
- **Keep context beside action.** Staff should be able to understand what the
  patient needs and what has already happened without reconstructing the story
  across systems.
- **Truth over interface optimism.** Provider evidence and committed product
  state—not clicks, loading states, or fluent automation—establish what
  actually happened.
- **Automate without hiding responsibility.** AI may resolve routine work or
  create well-supported Tasks, while ambiguous and consequential decisions
  remain visible to humans.
- **Treat context as context.** A phone number, name, transcript, or handoff can
  help staff act, but it does not establish verified patient identity or become
  a medical record.
- **Design for handoffs and failure.** Unanswered calls, failed delivery,
  partial outcomes, retries, reassignment, and reopening are normal product
  states that must stay visible and recoverable.
- **Learn from failure.** Turn useful failures into stronger invariants,
  diagnostics, tests, or simpler workflows.

## Non-Negotiables

- No unresolved patient need disappears inside a transcript, voicemail,
  notification, channel inbox, or individual memory.
- No Task becomes invisible because it is assigned, deprioritized, or
  completed.
- No call, message, automated action, or completion is represented as
  successful without supporting evidence.
- No automation conceals its source, decision, failure, or remaining human
  responsibility.
- No Contact Context is treated as canonical patient identity.
- No failure is hidden behind reassuring language or silent fallback behavior.

## Product Boundary

The Abita AI agent owns the automated patient conversation. Acuity Portal owns
the shared staff workspace, accountable work, human communication, and evidence
across AI and human Interactions.

`README.md` owns the current architecture.
`docs/acuity-portal-product-technical-spec.md` owns detailed product behavior
and the release bar. `CONTEXT.md` owns domain vocabulary. GitHub Issues own
committed product work.
