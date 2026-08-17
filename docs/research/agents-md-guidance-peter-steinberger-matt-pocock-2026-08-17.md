# AGENTS.md guidance from Peter Steinberger and Matt Pocock

Researched 2026-08-17 from first-party posts and public repositories. Repository
links are pinned to the `HEAD` revisions observed during this review.

## Conclusion

The useful pattern is not a comprehensive handbook. A root `AGENTS.md` should
be a small standing brief containing only the context and constraints that apply
to nearly every task, plus pointers that tell the agent when to read deeper
sources. Product direction belongs in `VISION.md`; domain language, architecture,
and repeatable workflows belong in their own docs or skills.

For Acuity Product, the most important missing instruction is:

```markdown
## Product direction

Before planning a feature, judging product fit, or changing user-visible
behavior, read `VISION.md`. Treat it as the source of truth for product direction
and boundaries.
```

This is better than copying the vision into `AGENTS.md`: the agent gets a clear
load rule, while `VISION.md` remains the single source of truth.

## What “buzz agents md” most likely means

The strongest inference is that “buzz” was a typo for **“best AGENTS.md.”** No
Peter Steinberger or Matt Pocock practice called “Buzz AGENTS.md” appeared in
their first-party material. There is an unrelated public repository named
`block/buzz`, but it has no evident connection to either person or to the rest of
the request.

## Matt Pocock

- Matt defines `AGENTS.md` as the project's standing brief. He recommends adding
  information the agent cannot derive from code: non-obvious commands,
  conventions, and hard constraints. A repeated correction is a candidate rule,
  but the file should stay short and declarative. Detailed material should be
  progressively disclosed through a context pointer or skill. [AI coding
  dictionary](https://github.com/mattpocock/dictionary-of-ai-coding/blob/251fec7ec3b08059e4203863024e6123090a54e3/dictionary/AGENTS.md.md)
- His complete guide says the ideal file is as small as possible. His stated
  minimum is a one-sentence project description, a non-default package manager,
  non-standard build/typecheck commands, and anything genuinely relevant to
  every task. He warns that generated inventories and fragile path descriptions
  become stale and poison context. [A Complete Guide to
  AGENTS.md](https://www.aihero.dev/a-complete-guide-to-agents-md)
- The same guide recommends a lightweight pointer such as “For TypeScript
  conventions, see `docs/TYPESCRIPT.md`,” with nested instruction files only
  where their scope applies. This directly supports pointing to `VISION.md`
  instead of embedding it.
- His current writing guidance sharpens this further: a context pointer should
  name what the referenced document contains **and** the conditions that should
  trigger reading it. It also says each meaning should have one source of truth.
  This directly supports “read `VISION.md` before product-fit or behavior
  decisions,” rather than a vague unconditional link or a copied vision.
  [Writing for agents
  skill](https://github.com/mattpocock/skills/blob/9c9f36ccd3995266cd675468af71639c8dde1ec5/skills/productivity/writing-for-agents/SKILL.md)
- His current skills repository makes the split concrete: `AGENTS.md` is only a
  symlink, the short instruction file records repository invariants and pointers,
  and `CONTEXT.md` owns domain language. [AGENTS.md
  symlink](https://github.com/mattpocock/skills/blob/9c9f36ccd3995266cd675468af71639c8dde1ec5/AGENTS.md) ·
  [instruction file](https://github.com/mattpocock/skills/blob/9c9f36ccd3995266cd675468af71639c8dde1ec5/CLAUDE.md) ·
  [domain context](https://github.com/mattpocock/skills/blob/9c9f36ccd3995266cd675468af71639c8dde1ec5/CONTEXT.md)
- His setup skill adds a thin `## Agent skills` block that points to separate
  issue-tracker, triage-label, and domain-layout files. It does not paste those
  workflows into the root instruction file. [Setup
  skill](https://github.com/mattpocock/skills/blob/9c9f36ccd3995266cd675468af71639c8dde1ec5/skills/engineering/setup-matt-pocock-skills/SKILL.md)

## Peter Steinberger

- Peter's October 2025 post is useful as a warning, not a template: he described
  his then-roughly-800-line agent file as “organizational scar tissue” and said it
  needed cleanup. He also recommended ordinary human wording instead of
  threatening, all-caps prompt tricks. [Just Talk To
  It](https://steipete.me/posts/just-talk-to-it#how-does-your-agentsclaude-file-look-like)
- By December 2025, Peter described a clearer context design: keep subsystem and
  feature docs under `docs/`, then use a small amount of standing instruction to
  make the model read relevant docs. [Shipping at
  Inference-Speed](https://steipete.me/posts/2025/shipping-at-inference-speed#my-workflow)
- His current public setup sharpens that boundary: “Skills own tool workflows.
  This file: hard rules only.” The companion README says downstream repositories
  should point to shared rules, then keep only repository-specific rules locally;
  shared blocks should not be copied into every repo. [Current shared
  AGENTS.MD](https://github.com/steipete/agent-scripts/blob/dc4f583a2c1a6f3a93e81a972eee89f59aca32f7/AGENTS.MD) ·
  [agent-scripts README](https://github.com/steipete/agent-scripts/blob/dc4f583a2c1a6f3a93e81a972eee89f59aca32f7/README.md)
- Peter's current GitHub triage skill explicitly says to read `VISION.md` before
  judging autonomous product fit and to use it as the product-fit source of
  truth. This is direct first-party support for the Acuity instruction proposed
  above. [GitHub project triage
  skill](https://github.com/steipete/agent-scripts/blob/dc4f583a2c1a6f3a93e81a972eee89f59aca32f7/skills/github-project-triage/SKILL.md)

## Recommended Acuity Product shape

Keep the root file short and project-specific, in this order:

1. One sentence describing Acuity Product and its purpose.
2. The `VISION.md` load rule above.
3. Pointers to `README.md` for architecture and `CONTEXT.md` / `docs/agents/domain.md`
   for domain language.
4. Only project-wide hard boundaries that cannot be inferred from code.
5. The one easy-to-run-wrong verification command:
   `go test -p 1 ./backend/... ./deploy -count=1`. Ordinary `pnpm` scripts can
   remain discoverable in `web/package.json` unless repeated failures show that
   a pointer is needed.
6. The existing short issue-tracker, triage-label, domain-doc, and Telnyx skill
   pointers.

Do not duplicate the contents of `VISION.md`, `README.md`, architecture docs,
domain docs, or skills. Remove generic advice the model already knows, fragile
directory inventories, and one-off incident rules. A rule earns permanent root
space when it applies broadly, is not obvious from the repository, and has
already prevented or explains a repeated failure.
