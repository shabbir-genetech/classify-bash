# classify-bash

A Claude Code PreToolUse hook for the `Bash` tool. Reads a single hook event
on stdin, parses the embedded shell command with `mvdan.cc/sh/v3/syntax`, and
emits an `allow` permission decision **only** when the command matches a
strict whitelist of read-only forms.

## Design

- **Allow-only.** The hook never emits `deny` or `ask`. Unsafe or
  unclassifiable commands fall through (exit 0 with no stdout) to Claude
  Code's normal permission prompt. We accelerate, we do not gate.
  These non-allowed cases can optionally be logged — see [Logging](#logging-opt-in).
- **Strict whitelist.** Every command, subcommand, and flag is enumerated
  positively in `commands.go`. Unknown command, unknown subcommand, or
  unknown flag on a known command → fall through. We never write
  "allow X except when Y" because a future release may introduce a Z we
  did not anticipate.
- **Defensive contract.** JSON input is decoded with
  `DisallowUnknownFields`; the event must declare
  `hook_event_name == "PreToolUse"` and `tool_name == "Bash"`. Any deviation
  exits 2 with a `classify-bash: <reason>` line on stderr. Any unrecognized
  `mvdan/sh` AST node kind also exits 2 — we would rather see a loud failure
  than silently ship a stale classifier.

## Failure modes

| Situation                                           | Exit | Stdout      | Stderr                          |
| --------------------------------------------------- | ---- | ----------- | ------------------------------- |
| Command matches the whitelist                       | 0    | allow JSON  | empty                           |
| Command is unsafe, unknown, or has an unknown flag  | 0    | empty       | empty                           |
| Bash parser refuses the input                       | 0    | empty       | empty                           |
| JSON contract violation (incl. unknown fields)      | 2    | empty       | `classify-bash: <reason>`       |
| Unknown `mvdan/sh` AST node kind                    | 2    | empty       | `classify-bash: unknown ...`    |

## Logging (opt-in)

Off by default — the hook stays silent on fall-through. When enabled with `--log`,
every **non-allowed** command is recorded as one best-effort JSON line: both the
fall-through cases and the `failLoud` (contract-violation) cases. Allowed commands
are never logged. Logging can only fail to record — it never changes the decision
and never blocks a call.

| Flag         | Default                              | Meaning                                                                       |
| ------------ | ------------------------------------ | ----------------------------------------------------------------------------- |
| `--log`      | off                                  | enable logging                                                                |
| `--log-to`   | `auto`                               | `auto` (journal if reachable, else file), `journal` (strict), or `file`       |
| `--log-file` | `$XDG_STATE_HOME/classify-bash/log`  | file path for `file`, and the `auto` fallback (then `~/.local/state/...`)      |

Each record is one line:

```json
{"ts":"2026-…Z","kind":"fallthrough","command":"rm -rf /tmp/x"}
{"ts":"2026-…Z","kind":"failloud","command":"","reason":"decode stdin: json: unknown field \"surprise\""}
```

`reason` appears only for `failloud` events; `orig_len` (original byte length)
appears only when the command was truncated (4 KB cap). On systemd the journal
sink lands in journald via `/dev/log` — query `journalctl -t classify-bash` and
grep the message. The journal sink is **Linux/macOS only** (it uses `log/syslog`);
on Windows/Plan9 it is unavailable, so `auto` uses the file and `journal` drops.

**Strictness is split by failure class:** log *writes* are best-effort (every
error swallowed), but log *config* is validated strictly — a bad flag exits 2 and
blocks the call, the same posture as the JSON decoder. So `--log-to=typo` will
stop every Bash call until fixed; this is intentional (you hear about a
misconfigured logger immediately rather than silently not logging). See DESIGN.md.

**Privacy:** records are the literal commands, verbatim. The default path is under
`$XDG_STATE_HOME`/`$HOME` (resolved at runtime, never hardcoded). On a shared or
recorded host, treat the log as containing whatever Claude tried to run, and scope
its location and retention accordingly.

## Build and test

The sub-flake exposes a Go-aware dev shell and a buildGoModule package.

```bash
# Dev shell with go, gopls, gotools, delve.
nix develop

# Inside the shell:
go mod tidy
go test ./...

# Build the binary as a Nix derivation:
nix build

# Run the test corpus via `nix flake check`:
nix flake check
```

`nix flake check` runs two checks: the test corpus and a `go-licenses` guard that
fails on any non-permissive dependency license. `THIRD_PARTY_LICENSES` — generated
by `scripts/gen-third-party-licenses.sh` — reproduces the bundled dependencies'
notices (goawk/MIT, mvdan/sh/BSD-3-Clause) for **binary** redistribution.

### Without Nix

It is a plain Go module — no Nix required to build or install:

```bash
# Install the latest published version straight onto $PATH:
go install github.com/shabbir-genetech/classify-bash@latest

# Or from a checkout:
go build -o classify-bash .   # or: go install .
go test ./...
```

Put the resulting binary on `$PATH` and register it the same way (see
[Registration](#registration)). Two notes:

- **The goawk dependency is a `replace`-pinned fork** (it re-exports goawk AST
  types `styleAwk` needs). The fork is **public**
  (`github.com/shabbir-genetech/goawk`), so `go build`/`go install` resolves it via
  the module proxy with no extra setup.
- **Platform support: Linux and macOS.** Windows/Plan9 build too, but the
  **journal** sink is unavailable there (it uses `log/syslog`, which those
  platforms lack), so `--log-to=journal` is a no-op and `--log-to=auto` always
  uses the file. The journal proper needs systemd (Linux). For a fully static
  binary, `CGO_ENABLED=0 go build` (the journal sink imports `net`).

## Manual smoke test

```bash
./result/bin/classify-bash <<<'{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls -la"}}'
# -> {"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}

./result/bin/classify-bash <<<'{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf /tmp/x"}}'
# -> (no output, exit 0)

./result/bin/classify-bash <<<'{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"},"surprise":true}'
# -> classify-bash: decode stdin: json: unknown field "surprise"
# -> (exit 2)
```

Logging is off by default. Enable it (here to a file) and a non-allowed command is
recorded as one JSON line; allowed commands are not:

```bash
./result/bin/classify-bash --log --log-to=file --log-file=/tmp/cb.log \
  <<<'{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf /tmp/x"}}'
cat /tmp/cb.log
# -> {"ts":"…Z","kind":"fallthrough","command":"rm -rf /tmp/x"}

# A bad flag is strict — it exits 2 and blocks the call (like the JSON decoder):
./result/bin/classify-bash --log --log-to=banana <<<'…'
# -> classify-bash: bad flag: unknown --log-to "banana" (want auto, journal, or file)
# -> (exit 2)
```

With `--log-to=auto` on a systemd host (a live `/dev/log`), records go to the
journal instead of the file — read them with `journalctl -t classify-bash`.

## Registration

Once the binary is on `$PATH`, add this to `~/.claude/settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {"type": "command", "command": "classify-bash"}
        ]
      }
    ]
  }
}
```

To turn on the audit log (see [Logging](#logging-opt-in)), pass the flags in the
command, e.g. `"command": "classify-bash --log --log-to=auto"`.

## Extending the whitelist

1. Audit ALL flags in the manpage for the command you want to add. Pick the
   ones that cannot mutate state.
2. Add a `commandSpec` entry in `commands.go` enumerating those flags
   positively. Document any deliberately-excluded flags in a comment so
   future reviewers see that they were considered.
3. Add `TestMustAllow` cases for the new safe forms and `TestMustNotAllow` cases
   for each known write-mode flag plus an `--unknown-flag` form. If you implement
   a deferred feature (e.g. a new flag style), also move the now-supported cases
   out of `TestNotYetAllowed` into `TestMustAllow`.
4. `nix flake check` must pass before the change can be trusted.

To also let a command receive an **attacker-controlled argv token** — be
**wrappable by `xargs`** *and* accept a `"$(...)"` command-substitution operand —
set `ArgvDataSafe: true` on its spec in `commands.go`. Only do so if it clears a
*stronger* bar than the whitelist itself: it must have **no write/mutate path under
any argv at all** (because xargs appends stdin items, and `$(...)` injects an
operand value, that the classifier never sees). A command whose spec merely
*excludes* a write flag (e.g. `sort`'s `-o`, `date`'s `-s`) does **not** qualify —
the injected token could supply that flag. Add a `mustAllow` `xargs <cmd> …` (and/or
`<cmd> "$(…)"`) case plus the matching `mustNotAllow`. `ArgvDataSafe` is the single
source of truth — no parallel list. See "Flag styles" (`styleXargs`) and DESIGN.md.

Before setting **`AllowAnyPositional: true`**, confirm the command has **no
dangerous flag at all** — not merely that you excluded it from the spec. `matchGNU`
stops validating flags at the first positional and accepts every later token as
data (so `cat file -X` allows `-X` as data). That is safe only when no swallowed
token can trigger anything: a reader whose write/exec/network path is a *flag*
(`gh repo view --web`, `journalctl --vacuum-size`) would have that flag ride in
*after* its positional and be allowed. Such a command must **not** set
`AllowAnyPositional` until §8 (validate post-positional flags) lands — give it a
fixed flag set with no positional instead, or leave the positional forms in
`TestNotYetAllowed`. See FUTURE-WORK.md §5/§8.

## Flag styles

- **`styleGNU`** (default): standard `-x`/`--name`/`--name=value`/clustered
  shorts. Used by most commands and all `Subcommands` dispatch.
- **`styleFind`**: every flag is `-name` form (single dash, full word), no
  clustering, no `=value`. Used by `find(1)`.
- **`styleWrapper`**: transparent wrapper for `[flag…] [positional…] -- CMD
  [ARG…]`. The literal `--` is REQUIRED — without it, the spec falls through
  (this is what makes `devenv shell` distinct from `devenv shell -- CMD`). The
  tail after `--` is looked up in `safeCommands` and matched recursively, so
  the wrapped command's whitelist rules apply unchanged. Pre-`--` positionals
  are accepted iff `AllowAnyPositional` is true (used for `nix shell PKGS --`).
- **`styleXargs`**: stdin-append wrapper `xargs [flag…] CMD [INITIAL-ARG…]`.
  Unlike `styleWrapper` there is **no `--` separator** — the first non-flag token
  is the wrapped command. The command is accepted only if its spec is
  **`ArgvDataSafe`** (in `commands.go`), not merely in the whitelist, and its
  initial-arguments are matched recursively. The gate matters because xargs
  appends stdin items to the wrapped argv that we never see, so only commands
  with no write path under *any* argv are wrappable (see "Flag styles" rationale
  in DESIGN.md). The replace-mode flags `-I`/`-i`/`--replace` are not
  whitelisted, so `xargs -I{} sh -c …` falls through.
- **`styleAwk`**: awk-shape command line `[flag…] PROGRAM [files…]` where the
  script itself is classified by walking the goawk AST (`awk.go`). Allowed
  pre-program flags are short-only and take values (`-F sep`, `-v var=val`);
  the first non-flag positional is the awk program, parsed via
  `github.com/benhoyt/goawk/parser` and accepted only when every node passes
  the positive whitelist below. Trailing positionals are input files.
  The `-f script.awk` script-load form, the `-e prog` multi-program form, and
  gawk extensions (`-i`, `-l`, `--long-flags`) are deliberately not in v1.
  Inside the awk program:
    - `print`/`printf` with any redirection (`>`, `>>`, `|`) → reject
    - `getline` from a pipe or a file → reject
    - `system(...)` (and other builtins not on the allowlist: `close`,
      `fflush`) → reject
    - User-defined functions (definition or call) → reject
    - Everything else (field/var refs, arithmetic, string ops, control flow,
      `length`/`substr`/`sprintf`/`gsub`/`split`/...) → recurse and accept

### Deferred wrapper shapes

These are useful but each needs its own style/handling — bundling them with v1
would obscure the design.

- **Flag-introduced** (`nix develop -c CMD`, `xargs -I{} CMD …`) — needs a
  `WrapFlag` variant naming which flag introduces the wrapped command. (Plain
  `xargs CMD` is handled by `styleXargs`; only the replace-mode `-I{}` form,
  which inserts the stdin item mid-argv, remains deferred.)
- **Inline** (`env VAR=val CMD`, `nice CMD`, `nice -n 10 CMD`) — first
  non-flag positional starts the wrapped command, no `--` required.
- **AST-level** (`time CMD`) — bash parses `time` as `TimeClause`, currently
  rejected in `classifyCmd`. Would add a recursive case there.
