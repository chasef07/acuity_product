## Agent skills

### Issue tracker

Issues and PRDs are tracked in GitHub Issues. See `docs/agents/issue-tracker.md`.

### Triage labels

This repo uses the five default triage labels. See `docs/agents/triage-labels.md`.

### Product workflow

- Portfolio: `https://github.com/users/chasef07/projects/1`.
- `needs-grilling` means material decisions remain open; run `grill-me` and do
  not implement.
- `ready-for-agent` means the issue is the implementation contract; run
  `implement` against it.
- After `to-spec`, add the issue to Project 1 with `gh project item-add`, then
  set Product, Work Type, Priority, Size, Cycle, and Status. GitHub Free only
  auto-adds Observatory issues.
- Work on one issue and one branch at a time. Link the pull request to the issue.
- Merged or released work moves to `Measuring`. Only production evidence moves
  it to `Done`.
- Agents may shape, specify, implement, test, and open draft pull requests. Do
  not merge, deploy, reprioritize, or mutate production without explicit
  authority.

### Domain docs

This is a single-context repo. See `docs/agents/domain.md`.

### Telnyx

Official Telnyx implementation skills are vendored in `.agents/skills`. Read
`.agents/skills/README.md` before using them.
