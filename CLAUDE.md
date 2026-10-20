## Agent skills

### Issue tracker

Issues are tracked in this repo's GitHub Issues via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default five-role vocabulary (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`). See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one root `GLOSSARY.md` plus `docs/adr/`. See `docs/agents/domain.md`.

## Codebase

- Before adding a business table, business operation, or permission: read `backend/README.md` (tenant isolation, operation layer, login and permissions).
- Before adding an admin page: read `admin/README.md`.
- Running the app locally or end-to-end tests: `scripts/dev.sh up|down|reset|status`, then `npm run e2e` in `admin/`.
- Checking that a test guards a protection: `backend/scripts/mutcheck.sh`.
