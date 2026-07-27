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
- **Triage the log with `TestTriage`, never by hand.** `triage_test.go` is the
  supported path and replaces ad-hoc analysis:

  ```bash
  journalctl -t classify-bash -o cat > /tmp/corpus.jsonl
  CORPUS=/tmp/corpus.jsonl SINCE=<date commands.go last changed> \
    nix develop --command go test -run TestTriage -count=1 -v .
  ```

  It calls `classifyCommand`/`safeCommands` directly, so its verdicts are the
  classifier's own; add `SHOW=<tag>` to print verbatim examples for a tag. It
  skips without `CORPUS`, so `go test ./...` is unaffected. Three reasons not to
  hand-roll this again, each a bug that shipped a wrong conclusion:
  - **Never regex-split a command to find the blocker.** Splitting on `&&`/`;`
    blames the line's first token, so `cd /repo && devenv up` reads as a `cd`
    problem. Two separate passes (2026-06-09, 2026-07-27) "discovered" a needed
    `cd`-prefix feature this way; `cd` has been whitelisted since 2026-06-02.
  - **Never judge allow/fall-through by exit code.** The hook exits 0 for *both*;
    the only signal is whether it prints allow JSON. A shell helper testing `&&`
    on the exit code reports everything as ALLOW and silently invalidates a whole
    probe table.
  - **Split the corpus at the last `commands.go` change** (`jj log -r
    'files(commands.go)'`). Older records were judged by an older whitelist, so
    they overstate the gaps; `TestTriage` reports that split for you.
- **A gap is only real if the *subcommand* is read-only.** When a global flag is
  missing from a parent spec, every use tips at that flag — `jj -R <path> log`
  (a real gap) and `jj -R <path> commit` (correct rejection) look identical until
  you split by subcommand. Conflating them overstated one finding ~4x. `TestTriage`
  tags these `[sub: …]` for this reason.
- **Audit flags from the spec data, not from invocations.** Testing a flag on one
  subcommand says nothing about its siblings: a 2026-07-27 pass concluded "git is
  clean" after checking `--textconv` on `git log`/`git blame` (where it is not
  whitelisted) and missed it on `cat-file`/`grep` (where it was, and where it
  executed). Walk `safeCommands` programmatically — a throwaway `go test` that
  prints every `TakesArg` flag with its owning command/subcommand path — then ask
  of each value "what *is* this?". See FUTURE-WORK.md §9 and DESIGN.md "Flag
  values that are programs".
- **Confirm an exec path by running it, not by reading the man page.** Point the
  flag at a script that touches a marker file and check for the marker. Three
  findings were confirmed this way in under a minute each, and one *disconfirmed*
  a plausible-sounding claim: `ui.editor` + `jj st` executes nothing (`st` never
  opens an editor) — the real vector was `ui.pager`, which fires on `log`/`diff`.
  Man pages describe intent; the marker file reports behaviour. Corollary: give
  the probe script a non-blocking body (`touch marker` and exit) — an `exec cat`
  tail once hung a `sort --compress-program` probe for two minutes.
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
