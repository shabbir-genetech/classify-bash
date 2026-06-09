# Project memory

Committed, version-controlled working notes for this repo, imported into
`CLAUDE.md`. Durable conventions and learnings live here; keep it short and
factual. (Project *facts* about the code belong in CLAUDE.md / DESIGN.md /
README.md — this file is for how we work, not what the code is.)

## Working preferences

- **Build verbosely**: `nix build -L` (streams build logs) over plain `nix build`.
- **Don't trim output by piping**: avoid `| tail`/`| head` just to shorten output;
  run the plain command. Feed a program's stdin via input redirection
  (`prog < file`), not a pipe. For classify-bash binary smoke tests, write the
  PreToolUse event JSON to a file and run `./result/bin/classify-bash < event.json`.
- **Chain dependent steps with `&&`**, not `;`, so a failed step aborts the rest.
- **Reading the audit log efficiently**: the hook logs to the journal
  (`--log --log-to=auto` in `settings.json`, systemd host → journal, not the file
  sink). Read it in one shot with `journalctl -t classify-bash -o cat` and summarize
  in a single pass — don't dump every record into context and re-run analyses over it.
  Health-check = is there any `failloud` (none ⇒ classifier healthy); the rest is
  bucketing fall-throughs. Note each triage pass adds self-referential records (the
  `journalctl`/`python3` heredoc analysis commands fall through too). See DESIGN.md
  "Reading the log".
- **Cross-compile when touching imports**: the dev shell `go test` and `nix flake
  check` build only for the host (Linux), so they miss cross-platform breaks (a
  Unix-only stdlib import can pass them and still fail on Windows). Sanity-check
  with `GOOS=windows go build ./...` / `GOOS=darwin go build ./...`. See DESIGN.md
  "Build gotcha".

## Version control (jj)

- Prefer `jj commit -m "…"` over `jj describe`.
- Land a change with: `jj commit -m "…" && jj bookmark set master -r @- && jj git push --bookmark master`.
- No AI attribution in commit messages (also in CLAUDE.md "Conventions"). See the
  CLAUDE.md "Version control" section for the jj-vs-git gotchas (auto-snapshot,
  the `result` symlink, `/result` in `.gitignore`).

## Maintaining this file

- This file is the **committed** project memory. `CLAUDE.md` pulls it in with
  `@.claude/memory.md`, so it loads every session and travels with the repo —
  edit it here and commit to change a shared convention.
- Claude Code's per-user store (`~/.claude/projects/<repo>/memory/`) is **local
  and never committed**. Don't duplicate committed facts there; that store's
  `MEMORY.md` is just a pointer back to this file. Use it only for things that
  should stay personal/uncommitted.
- Keep entries short and factual, and prefer linking to the authoritative section
  (CLAUDE.md / DESIGN.md / README.md) over restating it.
