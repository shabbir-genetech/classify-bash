# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

Committed working preferences and learnings live in @.claude/memory.md.

## What this is

`classify-bash` classifies a shell command against a strict read-only whitelist so
an agent CLI can skip its permission prompt for commands that cannot have a side
effect. It is an accelerator, never a gate — see [README.md](README.md) for the
contract and [DESIGN.md](DESIGN.md) for the rationale (allow-only, positive
whitelist, tiers A–F, AST handling, the goawk fork, opt-in logging).

The classifier itself is **host-agnostic**. Host wire formats live behind
subcommands: `check` (the primitive, and the contract every shim is built on),
`hook` (Claude Code `PreToolUse`), `doctor`. pi and oh-my-pi are integrated
through TypeScript shims in `contrib/` that call `check`.

## Architecture

Package layout, chosen so the engine cannot see the host:

```
cmd/classify-bash/        CLI: dispatch, check, hook, doctor, policy, the OS Host
internal/engine/          the classifier — no host, no JSON, no exit codes
internal/adapter/claude/  the PreToolUse wire format
internal/logging/         opt-in, best-effort audit log
contrib/{omp,pi}/         agent-CLI shims (installed to $out/share)
```

The engine-cannot-see-the-host rule is a convention, not enforced by a test; the
features that do need the host (§6's environment check, §10's filesystem checks)
reach it only through `engine.Host`.

Reading in this order is the fastest way to understand one event end to end:

- **`cmd/classify-bash/main.go`** — subcommand dispatch, and the usage text. A
  flags-only invocation with an event on stdin is routed to the legacy `hook`
  path (`looksLikeLegacyHook`) so the binary and a host's registration never have
  to be updated atomically. Usage errors exit **1**, never 2.
- **`cmd/classify-bash/check.go`** — the host-agnostic entry point and the shim
  contract: exit 0 read-only, 1 not, 2 this binary is broken or misused. Keep
  those numbers stable; shims depend on 2 ≠ 1. It classifies with the process's
  own environment and no scratchpad.
- **`cmd/classify-bash/policy.go`** — the two strictness axes and the mode
  presets. Deliberately separate from the adapter: a policy is reusable, and an
  adapter should not smuggle one in. Unknown fields: `fail | withhold | log |
  ignore`; **`withhold`** (log `schema_drift`, do not allow this call, never block)
  is what `claude-lenient` and the legacy path use. `legacyStrictness` is neither
  preset — withhold on a new field, fail on a stale classifier — and reproduces
  exactly what the bare registration did before the merge, which is what makes
  the compat path safe to deploy.
- **`cmd/classify-bash/hook.go`** — the Claude Code adapter body, plus
  `failLoud` / `failOpen` / `emitAllow` and the package-level
  `logCfg`/`currentCommand`. The decision itself is **`shouldAllow`** (read-only
  *and* not withheld), split out because the body reads stdin and exits; put
  decision logic there, not inline. `failLoud` is exit 2 and therefore **blocks
  the tool** — it is reserved for a bad `--log-*` flag and for a strictness policy
  the operator explicitly asked to be loud about. Everything else fails open.
- **`cmd/classify-bash/scratch.go`** + **`scratch_unix.go`/`scratch_other.go`** —
  the OS `engine.Host` (`osHost`) and the filesystem half of scratchpad writes
  (§10): `validScratchDir` (per-call validation of the event's `scratchpad_dir`)
  and `scratchFileWritable` (absent, or a regular file we own with one link).
  Ownership needs `syscall.Stat_t`, so the non-Unix build reports failure and the
  feature is off.
- **`cmd/classify-bash/doctor.go`** — reads the log and reports drift plus the
  adapter's field manifest. Findings are **windowed** (`--since`): reporting an
  append-only log in the present tense once produced a confidently false
  "the hook is still registered with the old invocation". "Is X still true?" is
  answered by recency (`registrationLooksStale`), never by a count.
- **`internal/engine/api.go`** — the engine's only public surface:
  `Classify(cmd)` and **`ClassifyWith(cmd, Host)`**, returning `Result{Class,
  Err}`. `NotReadOnly` is the zero value so a forgotten branch degrades to the
  safe answer; for the same reason a nil Host is `noHost`, which counts every name
  as set (nothing binds) and has no scratchpad. The engine **must never call
  `os.Exit`**: `failLoud` here panics with an unexported type that
  `recoverUnknown` catches, which is how call sites deep in a recursive walk
  abort without the engine knowing what "fail" means to a host. Non-matching
  panics are re-panicked — a bare `recover()` would turn a real bug into a silent
  permanent "nothing is ever read-only".
- **`internal/logging/log.go`** — opt-in, best-effort logging of the
  **non-allowed** cases (fall-through + `undecodable` + `schema_drift` +
  `failloud` + `deprecated_invocation`). Off by default; configured by CLI flags
  at the registration site, not by env. Two failure classes with different
  strictness: log *writes* are swallowed (never block); log *config* (flags) is
  validated strictly → `failLoud`. Journal sink is stdlib `log/syslog` (no new
  dependency). See DESIGN.md "Logging non-allowed commands".
- **`internal/logging/journal_unix.go` / `journal_other.go`** — the `writeJournal`
  sink, split by build tag because `log/syslog` is Unix-only. `_unix`
  (`!windows && !plan9`) uses syslog; `_other` is a stub that errors so
  Windows/Plan9 still build (the file sink works there, `journal` drops, `auto`
  falls back to file). Keep the binary portable — `log.go` itself imports nothing
  OS-restricted.
- **`internal/adapter/claude/event.go`** — tolerant JSON decode of the PreToolUse payload into
  `event`/`toolInput`, plus `unknownFields` for drift reporting. Only `command` is
  read; every other field is enumerated as an ignored `json.RawMessage`, and the
  known-field sets are derived from those struct tags by reflection — so the
  struct is the single source of truth, and enumerating a field silences its
  record AND (under `withhold`) restores auto-allow for payloads carrying it.
  Name-drift is *reported*; what it costs is the hook policy's call. Type-drift on
  ignored fields stays quiet. **Never reintroduce `DisallowUnknownFields`**:
  `PreToolUse` exit 2 blocks the tool, so rejecting a harmless new harness field
  takes the whole Bash tool down (it did, repeatedly — see DESIGN.md "Defensive
  JSON contract").
- **`internal/engine/classify.go`** — the shell-AST walk. `classifyCommandIn(cmd,
  host)` parses with `mvdan.cc/sh/v3/syntax` and walks with a `scope` (literal
  bindings + the Host). `&&`/`||`/pipe/`(subshell)` recurse — and decide which
  bindings each side sees — every other compound kind is rejected, an unknown AST
  node or binary operator calls `failLoud`. `wordLiteral` (still used for the
  command name, which must stay literal) rejects any expansion; `resolveWord` is
  `wordLiteral` plus bound `$VAR`. `argTokens` classifies each operand as a literal
  or — the one allowed expansion — a quoted `"$(...)"` command substitution whose
  inner command classifies read-only (`wordQuotedSubst`/`classifyCmdSubst`); that
  token reaches a spec only as an opaque positional, only for an `ArgvDataSafe`
  command. `safeRedirect` allows reads, writes to `/dev/null`, and writes of a data
  file into the scratchpad; it inspects heredoc **bodies** (an unquoted one is
  expanded by bash) and rejects `{name}>`.
- **`internal/engine/vars.go`** — literal-bound variables (FUTURE-WORK §6): which
  assignments bind (`bindAssigns`, `bindableName` — asks the Host whether a name
  is set), what a bound `$VAR` resolves to (`boundParam`, `resolveWord`), and the
  stricter `exactPath` used where a path prefix is the security boundary. The
  safety argument is its header comment.
- **`internal/engine/scratch.go`** — the path-only half of scratchpad writes (§10):
  top level, no dotfile, a data extension (`scratchExts`), then the Host's
  `ScratchWritable` for the filesystem half.
- **`internal/engine/spec.go`** — `commandSpec` + the five `flagStyle` matchers (`matchGNU`,
  `matchFind`, `matchWrapper`, `matchXargs`, `matchAwk`). This is the
  flag/subcommand/positional engine; the data it runs on lives in `commands.go`.
  `matchXargs` is the odd one out: no `--` separator (the first non-flag token is
  the wrapped command) and it recurses via `classifyWrapped`, which only accepts a
  command whose spec sets `ArgvDataSafe` — see the privacy/safety note below and
  DESIGN.md's "styleXargs and the stdin-argv hazard". The matchers take
  `[]argToken` (literal-or-substituted), so a `"$(...)"` operand reaches a spec
  only as an opaque positional, gated by the same `ArgvDataSafe` flag.
- **`internal/engine/commands.go`** — the actual whitelist data: `safeCommands` maps each command
  name to a `commandSpec`. This is where you add/extend allowed commands. The
  `ArgvDataSafe` flag on a spec marks a command safe to receive an attacker-
  controlled argv token (from `xargs` stdin or a `$(...)` substitution); it is the
  single source of truth for that — no parallel list — set only on leaf readers
  with no write path under any argv.
- **`internal/engine/awk.go`** — `classifyAwkProgram` walks an awk program's AST (via the goawk
  fork) for `styleAwk`, positively whitelisting nodes/builtins.

The safety argument is structural: the hook only ever *adds* an `allow`. A bug
can at worst fail to accelerate; it can never wave through something the normal
permission flow would have stopped. Preserve that asymmetry.

## Build / test

```bash
nix develop          # dev shell: go, gopls, gotools, delve, go-licenses
go test ./...        # every package: engine tables, adapter contract, CLI policy and §10
go test -run TestMustNotAllow ./...   # a single test function
nix flake check      # runs 2 checks: the corpus (checks.tests) AND a permissive-
                     # license guard (checks.licenses, go-licenses check) — MUST pass
nix build            # build the binary as a Nix derivation -> ./result/bin/classify-bash
./scripts/gen-third-party-licenses.sh  # regenerate THIRD_PARTY_LICENSES (in nix develop)
```

`checks.licenses` fails if any linked dependency carries a non-permissive
(e.g. copyleft) license. `THIRD_PARTY_LICENSES` reproduces the bundled deps'
notices for binary redistribution; it is generated, not hand-edited — rerun the
script above when dependencies change.

The test corpus is the spec: `TestMustAllow` (forms that must classify allow),
`TestMustNotAllow` (the safety wall — unsafe forms that must fall through),
`TestNotYetAllowed` (forms that are harmless *as written* but fall through only
because a classifier feature isn't built — a regression here is a feature landing,
not an incident), and `TestEventDecode*` (the JSON contract). Each case is a bare
command string in a table — add to the right table, don't write new test
functions. The engine's tables run against `testHost` (`internal/engine/host_test.go`): an
explicit empty environment and no scratchpad, so they never depend on the
environment the suite runs in. The exceptions are behaviour that needs a real
filesystem or the CLI: `cmd/classify-bash/scratch_test.go` (§10, real temp
directories, symlinks, hard links, a FIFO) and `hook_test.go` (`shouldAllow` and
the presets). A new fall-through case goes in `TestMustNotAllow` *unless* you can show
it is genuinely harmless, in which case it goes in `TestNotYetAllowed`. See
FUTURE-WORK.md "Two kinds of must not allow".

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

(DESIGN.md's "Build gotcha" calls out the jj-vs-git split explicitly; the
published upstream is consumed as a `github:` flake input — it moved off
`git+ssh://` on 2026-06-09 once the repo went public, a zero-rebuild change
because a git input strips `.git` and so hashes identically to the tarball at the
same rev — but local dev here is jj.)

### A second remote exists: `github.com/SBhojani/classify-bash`

Same owner. **Both repos are maintained; neither is being retired (owner's
decision, 2026-10-23).** They play different roles: this one is the Claude Code hook
that consumers pin; the fork is the host-agnostic line, with `check` as its
integration contract and pi/oh-my-pi shims. Each merges from the other when it
wants a change, so "behind" describes what is available to merge, not a backlog
either side owes the other. The remote is named `fork`, and `master@fork` is left
**untracked** so local `master` never follows it. This is a jj repo, so the recipe
is jj, not git:

```bash
jj git remote add fork git@github.com:SBhojani/classify-bash.git   # once per clone
jj git fetch --remote fork
jj log -r 'master..master@fork'      # what the fork has that we do not
jj log -r 'master@fork..master'      # what we have that the fork does not
jj log -r '<sha>-'                   # its parent: on our history → no refactor entanglement
jj duplicate <sha> --onto master     # jj's cherry-pick (jj ≥ 0.44 spelling; alias -d)
```

**2026-10-23: the fork's tip (`22feb86b`) was merged into `master`.** This repo now
has the fork's layout (`cmd/` + `internal/`), `check`/`hook`/`doctor`, the never-exit
engine and the shims, with this repo's work carried into it rather than alongside:
the heredoc fix, withhold-on-drift and §6/§10. Because `master` now contains the
fork's tip as an ancestor, **the fork can take everything here as a fast-forward**.
How the conflicts were resolved, for whoever merges next in either direction:

- **A plain resolution reverts the safety work.** jj does not follow renames, so
  the fork's `classify.go` → `internal/engine/classify.go` move came back as
  modify/delete conflicts, and the merged tree briefly held *two* classifiers: the
  fork's, which its binary builds, without the heredoc fix or §6/§10, and ours at
  the root. Taking the fork's side of each conflict would have shipped the
  heredoc hole again. The fix was to delete the root copies and put our versions
  of `classify.go`/`vars.go` and their tests into `internal/engine/`.
- **The host boundary was kept, not broken.** §6's environment check and §10's
  filesystem checks go through `engine.Host`; the OS implementation (`osHost`) is
  in `cmd/classify-bash/scratch.go`, and a nil Host is conservative (`noHost`).
- **Policy semantics were reconciled toward the safer side.** The fork's unknown-
  field values were `fail | log | ignore`; `withhold` was added and made the value
  of both the legacy path (so the bare registration behaves exactly as before) and
  `claude-lenient` (the fork's `log` auto-approved while blind to the payload).
  `doctor` and the deprecation record now recommend `claude-lenient`, not
  `claude-strict`, which blocks every call on a new harness field.
- **Identical code was not a conflict in substance.** The adapter's `event.go` and
  `logging/log.go` matched ours line for line apart from comments — the fork's
  `b6555e41` and our `e29c01f` are the same change — so the root copies were simply
  dropped.
- **Verified after:** every table passes in its new home, cross-compiles, and the
  mutation pass over all 30 safety checks — including the new Host, withhold and
  preset decisions — kills every mutant from a safety test, none by a compile error.

## Conventions

- **Strict positive whitelist** in `commands.go`: enumerate every command /
  subcommand / flag; unknown → fall through. Never "allow X except Y". When
  extending, follow the checklist in README "Extending the whitelist" and add
  `mustAllow` cases plus the matching wall (`mustNotAllow`) / deferred-safe
  (`TestNotYetAllowed`) cases.
- **A flag whose value is a program is an exec path** — however read-only the
  command looks. Enumerating flag *names* says nothing about what a *value*
  means, and this is where the whitelist has actually been wrong: `jj --tool`
  and `sort --compress-program` (value is a program), `nix eval --expr/--file`
  (value is evaluated — reads files, reaches the network), `jj --config` (value
  sets `ui.pager`, which the read-only subcommands spawn). All were shipped, not
  hypothetical. When adding any command, walk every `TakesArg` flag and ask what
  the value *is*; when in doubt, point it at a marker script and look. Note
  inertness is not safety (`--config-toml` was harmless only because jj 0.41
  dropped the name), and say in the comment whether an exclusion is a
  demonstrated exec path or merely no logged demand. See DESIGN.md "Flag values
  that are programs" and README "Extending the whitelist" step 2.
- **`ArgvDataSafe` is the one gate for attacker-controlled argv.** A command may
  receive an unseen operand (from `xargs` stdin or a `$(...)` substitution) only if
  its spec sets `ArgvDataSafe` — true only when it has no write/exec/network path
  under *any* argv, flag-shaped values included. It is a field on `commandSpec`, not
  a separate list; do not reintroduce a parallel set.
- **`AllowAnyPositional` is free; `ArgvDataSafe` + a flag-reachable side effect is
  the trap.** Since §8, `matchGNU` validates flags by getopt *permutation* — even
  after a positional — so a reader whose write/exec/network path is a flag
  (`gh repo view --web`, `journalctl --vacuum-size`) can set `AllowAnyPositional`
  safely; the unwhitelisted flag is rejected wherever it lands. The exception is
  `ArgvDataSafe`: such a command keeps the fast path (first positional closes flag
  parsing, rest is opaque data, so `cat file -X` allows `-X` as data), which is only
  sound when it has no flag-reachable side effect at all. So never set `ArgvDataSafe`
  on a spec with such a path. See README "Extending the whitelist" and `matchGNU`.
- **Non-blocking JSON decoder** (`internal/adapter/claude/event.go`). When the
  harness starts sending a new field on the event or inside `tool_input`, the event
  decodes, a `schema_drift` record names it, and under the `withhold` policy (the
  legacy path and `claude-lenient`) **the allow is withheld for that call** —
  exit 0, host prompts. Enumerate it as an ignored `json.RawMessage` on the right
  struct (`event` for top-level, `toolInput` for a `tool_input` field) + an
  accept-test; that both silences the record and restores acceleration. **Do NOT
  make this reject (nonzero exit) by default again** — withholding an allow costs
  prompts, rejecting costs dead sessions. See DESIGN.md.
- **Abort on the unknown; let the policy decide whether that blocks.** An
  unrecognized `mvdan/sh` AST node, redirect op, binary operator or flag style
  calls the engine's `failLoud`, which aborts the walk (`Unparseable`) rather than
  guessing. Keep new `switch` defaults loud. Whether an abort blocks is the hook's
  `--on-unknown-ast`: `fail` (exit 2) on the legacy path and `claude-strict`,
  `fallthrough` under `claude-lenient` and in `check`. Harness-driven input is the
  opposite case: it fails open, because there we cannot afford to block.
- **LF line endings** enforced via `.gitattributes` (`*.go`/`*.nix`/`*.md`). CRLF
  breaks the inline shell in `flake.nix`'s checks.
- No AI attribution in commit messages.

## Privacy invariant

This repo is **PUBLIC** (since 2026-06-09, at
`github.com/shabbir-genetech/classify-bash`); its history was scrubbed before the
flip. It stays clean going forward: do **not** commit genuinely-internal
identifiers (real home paths, internal project codenames, work email/domain). The
`shabbir-genetech` handle is **not** secret — it is the publishing account (and the
public goawk fork's owner), so it is fine in `go.mod`, docs, and the module path.
The pre-publication leak gate (a fresh-clone history scan for real home paths,
emails, and author identity) passed on 2026-06-09; re-run that scan if the history
is ever rewritten.

## How it's deployed

The binary is installed onto `$PATH` and registered in `~/.claude/settings.json`.
One known consumer is an external NixOS config that pulls this repo as a `github:`
flake input — so **a change here only reaches that consumer after a commit +
push**, then the consumer re-locks (`nix flake update classify-bash`) and rebuilds.
A long-lived `claude` session picks up the rebuilt hook on its next tool call.

**Registration form.** The recommended line is
`classify-bash hook --mode=claude-lenient --log --log-to=auto`. The bare
pre-subcommand form (`classify-bash --log --log-to=auto`) still works through the
legacy path and behaves exactly as before the 2026-10-23 merge, but with logging on
it writes a `deprecated_invocation` record on **every** call. **Switching order is not
critical for this change** (checked 2026-10-23): the pre-merge binary parses only
`--log*` flags and ignores `hook` and `--mode=…`, so it keeps classifying under the
new line. Deploying the binary first is still the habit — the general trap is a
*`--log*`* flag an old binary does not know, which it rejects with exit 2. Consumers that invoke the binary with no flags
at all (an adapter that pipes it a payload) also take the legacy path, with logging
off, so they need no change.
