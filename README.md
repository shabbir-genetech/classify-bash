# classify-bash

A Claude Code PreToolUse hook for the `Bash` tool. Reads a single hook event
on stdin, parses the embedded shell command with `mvdan.cc/sh/v3/syntax`, and
emits an `allow` permission decision **only** when the command matches a
strict whitelist of read-only forms.

## Design

- **Allow-only.** The hook never emits `deny` or `ask`. Unsafe or
  unclassifiable commands fall through (exit 0 with no stdout) to Claude
  Code's normal permission prompt. We accelerate, we do not gate.
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

## Extending the whitelist

1. Audit ALL flags in the manpage for the command you want to add. Pick the
   ones that cannot mutate state.
2. Add a `commandSpec` entry in `commands.go` enumerating those flags
   positively. Document any deliberately-excluded flags in a comment so
   future reviewers see that they were considered.
3. Add `mustAllow` cases for the new safe forms and `mustNotAllow` cases for
   each known write-mode flag plus an `--unknown-flag` form.
4. `nix flake check` must pass before the change can be trusted.

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
  `WrapFlag` variant naming which flag introduces the wrapped command.
- **Inline** (`env VAR=val CMD`, `nice CMD`, `nice -n 10 CMD`) — first
  non-flag positional starts the wrapped command, no `--` required.
- **AST-level** (`time CMD`) — bash parses `time` as `TimeClause`, currently
  rejected in `classifyCmd`. Would add a recursive case there.
