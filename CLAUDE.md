## Agent skills

### Issue tracker

Issues are tracked in this repo's GitHub Issues via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five-role vocabulary (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one root `GLOSSARY.md` plus `docs/adr/`. See `docs/agents/domain.md`.

## Codebase

- Before adding a business table, business operation, permission, or store setting: read `backend/README.md` (tenant isolation, operation layer, login and permissions).
- Before adding an admin page: read `admin/README.md`.
- Running the app locally or end-to-end tests: `scripts/dev.sh up|down|reset|status`, then `npm run e2e` in `admin/`.
- Checking that a test guards a protection: `backend/scripts/mutcheck.sh`.

## Claude-Orchestrated Codex Workflow

### Roles

- Claude is the primary agent, architect, lead reviewer, and final decision-maker.
- Codex is the implementation agent, secondary reviewer, and test executor.
- Claude owns architecture and scope decisions.
- Claude does not edit production code unless authorized.
- This workflow overrides skills that have Claude write code (e.g. `/implement`, `/tdd`): Claude writes the brief and the seams, Codex writes the code and tests.

### Workflow

1. Claude inspects code, tests, configuration, and git status.
2. Claude prepares a scoped implementation brief.
3. After authorization, Codex implements the changes.
4. Claude independently reviews the complete diff.
5. A fresh, read-only Codex session performs secondary review.
6. Claude verifies every finding and decides what needs fixing.
7. Codex applies confirmed fixes; Claude reviews them again.
8. Codex runs the project's applicable verification commands (see Codebase above and `backend/README.md`).
9. Claude checks actual results and performs final acceptance.

### Constraints

- Preserve pre-existing user changes.
- Only Codex performs implementation edits.
- Secondary review must be read-only.
- Never invent test commands or hide failures.
- Do not introduce unnecessary architecture or abstractions.
- Do not commit, push, deploy, or perform destructive actions without explicit authorization.
- Never claim success without real verification evidence.
