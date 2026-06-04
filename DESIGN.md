# Design notes

Deeper rationale behind `classify-bash`. The [README](README.md) covers the
contract and how to run it; this file records *why* the classifier is shaped the
way it is, for anyone extending it.

## Why allow-only, never deny

The hook only ever emits `allow`. It never emits `deny`/`ask`, and anything it
cannot positively classify falls through (exit 0, no stdout) to Claude Code's
normal permission prompt. The hook is an accelerator, not a gate: a bug in it can
only *fail to speed something up*, never wave through something the normal flow
would have stopped. This asymmetry is the whole safety argument — keep it.

## Strict positive whitelist

Every command, subcommand, and flag is enumerated positively in `commands.go`.
An unknown command, unknown subcommand, or unknown flag on a known command →
fall through. We never write "allow X except when Y": a future release of some
tool may add a state-mutating `Z` we did not anticipate, and an
allow-except-list would wave it through. A positive list fails closed instead.

### Tiers

- **A — no-subcommand reads** (`cat`, `ls`, `grep`, `cd`) plus stdout-only
  filters (`tr`, `cut`, `sort`, `uniq`, `paste`): `styleGNU`, `AllowAnyPositional`,
  no write flags.
- **B — command + subcommand** (`git status`/`log`, `jj`, `nix eval`,
  `docker ps`, `systemctl status`): `styleGNU` + `Subcommands` dispatch.
- **C — flag-aware dual-use** (`find`): `styleFind`.
- **D — transparent wrappers** (`devenv shell --`, `nix shell PKGS --`):
  `styleWrapper`. The literal `--` is REQUIRED; the tail after it is looked up in
  the whitelist and classified recursively.
- **E — scripting with a whitelisted body** (`awk`): `styleAwk` — see below.

## Defensive JSON contract

Input is decoded with `DisallowUnknownFields`; the event must declare
`hook_event_name == "PreToolUse"` and `tool_name == "Bash"`. Any deviation exits
2 with a `classify-bash: <reason>` line on stderr. We would rather fail loud than
silently ship a stale classifier.

**Tracking harness-added fields.** Because the decoder is strict, any new field
the harness starts sending on the event or inside `tool_input` will make decoding
exit 2 — which *blocks the Bash call*. The fix for any future field is mechanical:
add it as an ignored `json.RawMessage` in the right struct (a top-level field goes
on `event`, a `tool_input` field goes on `toolInput` in `event.go`), add an
accept-test covering an event that carries it, and rebuild. No `go.mod` change, so
the vendor hash is unaffected. This has already happened once for an `effort`
field that the harness began attaching both inside `tool_input` and at the top
level — both structs now carry an ignored `Effort`.

## AST handling (`classifyCmd`)

The shell command is parsed with `mvdan.cc/sh/v3/syntax`. Compound forms are
handled conservatively:

- `BinaryCmd` (`&&`, `||`, pipe) and `Subshell` (`( … )`) **recurse** — every
  inner statement must itself classify as safe.
- `Block` and every other compound kind (`If`/`For`/`While`/`Case`/`FuncDecl`/
  `Let`/`Time`/`Coproc`/`Test`/`Decl`/`Arithm`) are **rejected**.
- An unrecognized AST node kind → fail loud, exit 2. A new `mvdan/sh` release
  that introduces a node we do not handle should stop the classifier, not be
  silently treated as safe.

## `styleAwk` and the goawk fork

For `awk`, the program itself is parsed and walked: `classifyAwkProgram`
(`awk.go`) positively whitelists every node. It rejects output redirects
(`>`, `>>`, `|`), `getline` from a pipe or file, `system`/`close`/`fflush`, and
user-defined functions; the builtin allowlist is
`length`/`substr`/`sprintf`/`tolower`/`toupper`/`gsub`/`sub`/`match`/`split`/
`index` plus the math functions. Pre-program flags are short-only value flags
(`-F sep`, `-v var=val`); the `-f script.awk`, `-e prog`, and gawk extension
forms are deliberately out of scope for v1.

Walking the program needs goawk's AST types, which upstream keeps in an
unimportable `internal/ast` package. A **thin goawk fork** (see the `replace`
directive in `go.mod`) adds an `ast/` package that re-exports those types; that
is the *only* reason for the fork. See [PUBLIC-READINESS.md](PUBLIC-READINESS.md)
for what to do about that fork before making this repo public.

## Deferred wrapper shapes

Useful but each needs its own handling; bundling them into v1 would obscure the
design:

- **Flag-introduced** (`nix develop -c CMD`, `xargs -I{} CMD …`) — needs a
  `WrapFlag` variant naming which flag introduces the wrapped command.
- **Inline** (`env VAR=val CMD`, `nice CMD`) — first non-flag positional starts
  the wrapped command, no `--` required.
- **AST-level** (`time CMD`) — bash parses `time` as `TimeClause`, currently
  rejected in `classifyCmd`; would add a recursive case there.

## Build gotcha

`nix build` / `nix flake check` use a VCS-aware source, so a newly-created `.go`
file must be **snapshotted by the VCS** before building — otherwise the build
fails with errors like `undefined: classifyAwkProgram` because Nix never saw the
file.

The local working copy is **jujutsu (`jj`)** — there is a `.jj/` and no `.git/`,
so `git add` does not apply. jj auto-snapshots the working copy on every `jj`
command, so it is enough to run any `jj` command (e.g. `jj status`) after creating
the file, before building. (Upstream is consumed as a `git+ssh` flake input, so a
git checkout would instead need `git add`; same underlying requirement, different
tool.)
