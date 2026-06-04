# CLAUDE.md

Guidance for Claude Code when working in this repo.

## What this is

`classify-bash` is a Claude Code **PreToolUse hook for the `Bash` tool**. It reads
one hook event on stdin, parses the embedded shell command, and emits an `allow`
permission decision **only** when the command matches a strict read-only
whitelist; anything else falls through silently to the normal permission prompt.
It is an accelerator, never a gate — see [README.md](README.md) for the contract
and [DESIGN.md](DESIGN.md) for the rationale (allow-only, positive whitelist,
tiers A–E, AST handling, the goawk fork).

## Build / test

```bash
nix develop          # dev shell: go, gopls, gotools, delve
go test ./...        # the classifier corpus (TestMustAllow / TestMustNotAllow / TestEventDecode*)
nix flake check      # runs that corpus as a flake check — MUST pass before trusting a change
nix build            # build the binary as a Nix derivation -> ./result/bin/classify-bash
```

A newly-created `.go` file must be `git add`-ed before `nix build` (Nix uses a
git-aware source; an untracked file is invisible and the build fails
`undefined: <symbol>`).

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
- **LF line endings** enforced via `.gitattributes` (`*.go`/`*.nix`/`*.md`). CRLF
  breaks the inline shell in `flake.nix`'s checks. If a working copy ever shows
  CRLF: `for f in $(git ls-files); do sed -i 's/\r$//' "$f"; done`.
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
