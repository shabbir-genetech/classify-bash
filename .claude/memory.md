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

## Version control (jj)

- Prefer `jj commit -m "…"` over `jj describe`.
- Land a change with: `jj commit -m "…" && jj bookmark set master -r @- && jj git push --bookmark master`.
- No AI attribution in commit messages (also in CLAUDE.md "Conventions"). See the
  CLAUDE.md "Version control" section for the jj-vs-git gotchas (auto-snapshot,
  the `result` symlink, `/result` in `.gitignore`).
