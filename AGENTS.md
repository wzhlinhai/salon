## Codebase

- Before adding a business table, business operation, permission, or store setting: read `backend/README.md` (tenant isolation, operation layer, login and permissions).
- Before adding an admin page: read `admin/README.md`.
- Writing or reviewing code: apply `CODING_STANDARDS.md` rule by rule.
- Running the app locally: `scripts/dev.sh up|down|reset|status`. End-to-end tests: `scripts/dev.sh reset`, then `npm run e2e` in `admin/` (the full suite locks last month, so it needs a fresh database).
- Checking that a test guards a protection: `backend/scripts/mutcheck.sh`.
