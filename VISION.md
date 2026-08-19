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

### Simplicity

- Build the simplest system that fully solves the real problem.
- Write clean, elegant code that is digestible in one pass.
- Delete stale, dead, duplicated, or unnecessary code whenever it is in scope.
- Prefer boring primitives, clear names, explicit control flow, and fewer moving
  parts.
- Prefer one owner, one state, and one source of truth.
- New abstractions, dependencies, configuration, and compatibility paths must
  earn their complexity.

### Craft

- Care about the small things. Names, states, contracts, errors, copy, layout,
  timing, and transitions shape the product.
- Work with extreme precision and attention to detail across frontend, backend,
  operations, and the full staff and patient journey.
- Make responsibilities narrow, boundaries explicit, state transitions obvious,
  and failure modes visible and recoverable.
- Trace behavior end to end. A locally correct component is not enough when the
  complete experience is wrong.
- Prefer code and interfaces that explain themselves over work that merely looks
  clever or impressive.

### Failure Analysis and Continuous Improvement

- Capture the failing state before changing it: what failed, how to reproduce it,
  the observable evidence, and the boundary that owns it.
- Explain why it failed. Fix the cause at the owning boundary, not only the
  downstream symptom.
- Record what changed, why it improves the system, and any remaining risk.
- Compare the failing state with the new state using the same scenario and
  observable before-and-after proof.
- Turn each useful failure into a stronger invariant, test, diagnostic, or
  simpler design so the system improves continuously.
- Do not hide failures with reassuring language, silent fallbacks, or weaker
  checks. Keep failure visible and recoverable.
- Weak evidence means no-change is valid.

### Acuity Portal Principles

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
- **Learn from operational failure.** Preserve useful provider and product
  evidence so each failure improves the staff workflow.

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
