## Codebase

- Before adding a business table, business operation, permission, or store setting: read `backend/README.md` (tenant isolation, operation layer, login and permissions).
- Before adding an admin page: read `admin/README.md`.
- Writing or reviewing code: apply `CODING_STANDARDS.md` rule by rule.
- Running the app locally or end-to-end tests: `scripts/dev.sh up|down|reset|status`, then `npm run e2e` in `admin/`.
- Checking that a test guards a protection: `backend/scripts/mutcheck.sh`.
