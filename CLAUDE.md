## Agent skills

### Issue tracker

Issues are tracked in this repo's GitHub Issues via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five-role vocabulary (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one root `GLOSSARY.md` plus `docs/adr/`. See `docs/agents/domain.md`.

@AGENTS.md

## Claude-Orchestrated Codex Workflow

### Roles

- Claude is the primary agent, architect, lead reviewer, and final decision-maker.
- Codex is the implementation agent, secondary reviewer, and test executor.
- Claude owns architecture and scope decisions.
- Claude does not edit production code unless authorized.
- This workflow overrides skills that have Claude write code (e.g. `/implement`, `/tdd`): Claude writes the brief and the seams, Codex writes the code and tests.

### Workflow

1. Claude inspects code, tests, configuration, and git status.
2. Claude prepares a scoped implementation brief per `docs/agents/codex-brief.md` (dispatch flags, gates, report path), naming the `CODING_STANDARDS.md` rules the task touches. Briefs, review prompts, PR bodies, relay output (`--out-dir .dev/codex/<task>/runs`), and Codex's reports go in `.dev/codex/<task>/` (gitignored); `/tmp` does not survive a reboot.
3. After authorization, Codex implements the changes.
4. Claude independently reviews the complete diff against the brief and `CODING_STANDARDS.md`, rule by rule.
5. A fresh, read-only Codex session performs secondary review against the same.
6. Claude verifies every finding and decides what needs fixing.
7. Codex applies confirmed fixes; Claude reviews them again.
8. Codex runs the project's applicable verification commands (see `AGENTS.md` and `backend/README.md`).
9. Claude re-runs those commands outside the sandbox, checks actual results, and performs final acceptance.

### Constraints

- Preserve pre-existing user changes.
- Only Codex performs implementation edits.
- Secondary review must be read-only.
- Never invent test commands or hide failures.
- Do not introduce unnecessary architecture or abstractions.
- Do not commit, push, deploy, or perform destructive actions without explicit authorization.
- Never claim success without real verification evidence.
- When a rule in `CODING_STANDARDS.md` is added or changed, check existing code against it; open an issue for whatever the same PR does not fix.
