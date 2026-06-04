# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`classify-bash` is a Claude Code **PreToolUse hook for the `Bash` tool**. It reads
one hook event on stdin, parses the embedded shell command, and emits an `allow`
permission decision **only** when the command matches a strict read-only
whitelist; anything else falls through silently to the normal permission prompt.
It is an accelerator, never a gate — see [README.md](README.md) for the contract
and [DESIGN.md](DESIGN.md) for the rationale (allow-only, positive whitelist,
tiers A–E, AST handling, the goawk fork).

## Architecture

One event flows through a fixed pipeline; reading these in order is the fastest
way to understand the whole thing:

- **`main.go`** — entry point. Decode the event, classify the command, and on
  `decisionAllow` print the fixed allow JSON (hand-written, no `encoding/json` on
  the emit path). Everything else is silent exit 0. `failLoud` is the only path
  to exit 2.
- **`event.go`** — strict JSON decode (`DisallowUnknownFields`) of the PreToolUse
  payload into `event`/`toolInput`. Only `command` is read; every other field is
  enumerated as an ignored `json.RawMessage` so name-drift fails loud but
  type-drift on ignored fields stays quiet.
- **`classify.go`** — the shell-AST walk. `classifyCommand` parses with
  `mvdan.cc/sh/v3/syntax`, then recurses: `&&`/`||`/pipe/`(subshell)` recurse,
  every other compound kind is rejected, an unknown AST node calls `failLoud`.
  `wordLiteral`/`literalWords` reject any word with expansion (`$VAR`, `$(...)`,
  `<(...)`, …) — only fully-literal argv reaches a spec. `safeRedirect` allows
  reads and writes only to `/dev/null`.
- **`spec.go`** — `commandSpec` + the five `flagStyle` matchers (`matchGNU`,
  `matchFind`, `matchWrapper`, `matchXargs`, `matchAwk`). This is the
  flag/subcommand/positional engine; the data it runs on lives in `commands.go`.
  `matchXargs` is the odd one out: no `--` separator (the first non-flag token is
  the wrapped command) and it recurses via `classifyWrapped` into a curated
  subset, not the full whitelist — see the privacy/safety note below and
  DESIGN.md's "styleXargs and the stdin-argv hazard".
- **`commands.go`** — the actual whitelist data: `safeCommands` maps each command
  name to a `commandSpec`. This is where you add/extend allowed commands. It also
  holds `xargsWrappable`, the curated subset `xargs` may wrap (a strict subset of
  `safeCommands`; keep them in sync or `classifyWrapped` fails loud).
- **`awk.go`** — `classifyAwkProgram` walks an awk program's AST (via the goawk
  fork) for `styleAwk`, positively whitelisting nodes/builtins.

The safety argument is structural: the hook only ever *adds* an `allow`. A bug
can at worst fail to accelerate; it can never wave through something the normal
permission flow would have stopped. Preserve that asymmetry.

## Build / test

```bash
nix develop          # dev shell: go, gopls, gotools, delve
go test ./...        # the classifier corpus (TestMustAllow / TestMustNotAllow / TestEventDecode*)
go test -run TestMustNotAllow ./...   # a single test function
nix flake check      # runs that corpus as a flake check — MUST pass before trusting a change
nix build            # build the binary as a Nix derivation -> ./result/bin/classify-bash
```

The test corpus is the spec: `TestMustAllow` (forms that must classify allow),
`TestMustNotAllow` (forms that must fall through), and `TestEventDecode*` (the
JSON contract). Each case is a bare command string in a table — add to the table,
don't write new test functions.

## Version control: this is a jj repo, not git

The working copy is managed by **jujutsu (`jj`)** — there is a `.jj/` directory and
**no `.git/`**. `git` commands will not work here. Equivalents:

- jj auto-snapshots the working copy on every `jj` command, so a newly-created
  `.go` file is tracked as soon as you run any `jj` command (or `jj status`) — no
  explicit `add` step. This matters because **`nix build`/`nix flake check` use a
  VCS-aware source: an unsnapshotted new file is invisible and the build fails
  `undefined: <symbol>`.** Run a `jj` command (or `jj st`) after creating a file,
  before building.
- The CRLF-repair loop in older notes (`git ls-files | sed …`) becomes
  `jj file list` instead of `git ls-files`.
- The same auto-snapshot catches **build artifacts**: a `nix build` leaves a
  `result` symlink into `/nix/store`, which jj will snapshot and try to commit if
  it isn't ignored. `/result` is in `.gitignore` for exactly this reason — keep it
  there, and check `jj st` before committing so a stray `A result` (or other
  artifact) doesn't ride along.
- To land a change on the remote: `jj commit -m "…"`, then move the bookmark with
  `jj bookmark set master -r @-`, then `jj git push --bookmark master`. (No AI
  attribution in the message; see Conventions.)

(DESIGN.md's "Build gotcha" and PUBLIC-READINESS.md's leak gate now call out the
jj-vs-git split explicitly; the published upstream is consumed as a `git+ssh`
flake input, but local dev here is jj.)

## Conventions

- **Strict positive whitelist** in `commands.go`: enumerate every command /
  subcommand / flag; unknown → fall through. Never "allow X except Y". When
  extending, follow the checklist in README "Extending the whitelist" and add
  both `mustAllow` and `mustNotAllow` tests.
- **Strict JSON decoder** (`event.go`): `DisallowUnknownFields`. When the Claude
  Code harness starts sending a new field on the event or inside `tool_input`,
  decoding exits 2 and BLOCKS the call. Fix: add the field as an ignored
  `json.RawMessage` on the right struct (`event` for top-level, `toolInput` for
  a `tool_input` field) + an accept-test. See DESIGN.md.
- **Fail loud on the unknown**: an unrecognized `mvdan/sh` AST node, redirect op,
  or flag style calls `failLoud` (exit 2) rather than guessing — we'd rather block
  than ship a stale classifier. Keep new `switch` defaults loud.
- **LF line endings** enforced via `.gitattributes` (`*.go`/`*.nix`/`*.md`). CRLF
  breaks the inline shell in `flake.nix`'s checks.
- No AI attribution in commit messages.

## Privacy invariant

This repo's history was scrubbed so it can be made public later, and it is
currently **private**. Do not introduce internal identifiers (real home paths,
project names, emails, org names). The goawk-fork owner handle may appear **only**
in `go.mod`/`go.sum` (the mechanical `replace` pin) and nowhere else — not in
docs, comments, or commit messages. Before flipping the repo public, work through
[PUBLIC-READINESS.md](PUBLIC-READINESS.md) (resolve the private fork; re-run the
leak gate).

## How it's deployed

The binary is installed onto `$PATH` and registered by bare name `classify-bash`
in `~/.claude/settings.json`. One known consumer is an external NixOS config that
pulls this repo as a `git+ssh` flake input — so **a change here only reaches that
consumer after a commit + push**, then the consumer re-locks
(`nix flake lock --update-input classify-bash`) and rebuilds. A long-lived
`claude` session picks up the rebuilt hook on its next tool call.
