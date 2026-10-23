# Codex brief

How Claude dispatches a task to Codex. Codex sees only the brief: it does not read `CLAUDE.md`, so everything below that Codex must do goes into the brief itself.

## Dispatch

```bash
node <codex-delegate>/scripts/relay.mjs --brief .dev/codex/<task>/brief.txt \
  --network-access --cd <repo> --out-dir .dev/codex/<task>/runs > .dev/codex/<task>/relay.log 2>&1
```

- `--network-access` on every implementation dispatch and every `--session <threadId>` resume; a resume also needs `--sandbox workspace-write` beside it. Without it the sandbox has no Docker and no browser, so `go test`, `mutcheck.sh` and e2e all fail on the environment.
- Read-only reviews use `--read-only --ignore-user-config` instead.
- While a read-only review is running, do not run mutchecks or other edits in the same worktree: the reviewer reads whatever is on disk, and a temporary mutation shows up as a finding.
- Before dispatching, run `scripts/dev.sh down` and confirm ports 8080 and 3000 are free (`ss -ltnp | grep -E ':8080 |:3000 '`). The sandbox cannot see processes started outside it: a sandboxed `dev.sh down` deletes their pid files without stopping them, leaving orphans that block the next `dev.sh up`.

## Brief skeleton

```xml
<task>   repo, branch (already checked out), goal; no commit, no push, no branch switch;
         files to read first; the CODING_STANDARDS.md rules this task touches, quoted </task>
<change> current state, required behaviour, what to leave untouched; tests and mutchecks per change </change>
<gates>  the commands below </gates>
<report> write to .dev/codex/<task>/report.md, then summarise it in the final message </report>
```

- When the brief deletes a variable, check, or `finally` block, name what it protected and what protects it afterwards. A deleted request counter or cleanup is a silent regression otherwise.
- Before writing "existing tests pass unchanged", grep the tests for assertions and fixtures that pin the behaviour this task changes. List the ones the brief allows Codex to change; otherwise Codex stops mid-task to ask.

## Gates

Codex runs every applicable gate through `scripts/gate.py .dev/codex/<task> <label> -- …` and fixes failures before reporting. Each output goes to a log file under `.dev/codex/<task>/`; read the exit code, never pipe a long-running command through a filter.

- `backend/`: `gofmt -l .` (no output), `go vet ./...`, `golangci-lint run ./...`, `go tool sqlc generate` then `go tool sqlc diff`, `go test ./...` (no `-count=1`: Go reruns only packages whose code or dependencies changed; `mutcheck.sh` forces a real run itself), and the task's protection checks (`scripts/mutchecks.py <manifest> --since main`, see below).
- `admin/`: `npm run generate:api` then check `app/utils/api.d.ts` changed only if the API did, `npm run lint`, `npm run typecheck`.
- e2e: the specs covering the pages or components the task changed (`npm run e2e -- e2e/<name>.spec.ts`); the full suite when shared code changed (`useApi`, composables, `helpers.ts`, layouts). As **one** shell command: the sandbox kills background processes when each command ends, so `dev.sh` and the tests must share one command.

  ```bash
  L=.dev/codex/<task>; if scripts/dev.sh reset > $L/reset.log 2>&1; then (cd admin && npm run e2e -- <specs> > ../$L/e2e.log 2>&1); s=$?; echo "e2e exit=$s"; else s=$?; echo "reset failed exit=$s"; fi; scripts/dev.sh down > $L/down.log 2>&1; (exit $s)
  ```

  The command exits with the reset or e2e status, not `down`'s. Drop `<specs>` to run the full suite.

- A mutcheck counts as caught only when the failing test names are the tests guarding that protection. A failure caused by the environment (Docker, browser, port) is not a catch: report it as not run.
- A mutation that moves code (not just deletes it) is easy to get wrong on the command line: build the original and replacement text with a script from the file, and check the log says "caught by". "does not build" or "NOT caught" is not a pass. Run every mutcheck on the final code: one recorded before a later edit (for example, before deleting a query it calls) may no longer build.
- A protection in the admin UI (no `mutcheck.sh` there): temporarily remove it, run the guarding e2e in the same one-command form, restore it, report both results.
- Every protection of a P0 ticket lives in one manifest, `.dev/codex/<ticket>/manifest.py`, run by `backend/scripts/mutchecks.py` (its docstring gives the format). Each step, and each fix round, appends or rewrites its entries, runs only the affected ones with `--since main` (entries whose protected file or guard test file the branch changed; go entries run only their guard tests), and runs `backend/scripts/mutchecks.py --locate` (no manifest) every round, so a change that moves code under another ticket's protections is caught: an entry whose original no longer matches is rewritten in the step whose edit moved it. The full manifest runs once per ticket, on main after its last step merges (CLAUDE.md workflow step 10), not in every round.
- When a Codex run ends in any way (finished, quota, crash), run `backend/scripts/mutcheck.sh --restore` before reviewing: an interrupted mutation leaves its backup in `.dev/mutcheck-backup/`.

## Report

`.dev/codex/<task>/report.md`, never `/tmp`: files changed; how each change was made and which test guards it; every gate and mutcheck with its real command and result, failures pasted verbatim; any decision that departs from the brief, with the reason.
