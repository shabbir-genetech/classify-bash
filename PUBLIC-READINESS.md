# Public-readiness checklist

> **STATUS: PUBLIC since 2026-06-09** at `github.com/shabbir-genetech/classify-bash`.
> Every item below is resolved. This file is kept as the record of what was checked
> and as a reusable gate if the history is ever rewritten or needs re-verifying.

This repo was extracted from a private parent repository with its history
**scrubbed** so it could be made public safely. The checklist that gated the flip:

## 1. goawk fork — RESOLVED

`go.mod` pins a goawk fork via a `replace` directive so `styleAwk` can walk the
goawk AST (it re-exports goawk's `internal/ast` types — see [DESIGN.md](DESIGN.md)).

This is no longer a blocker: the fork is **public** at
`github.com/shabbir-genetech/goawk`, so a public `go build` resolves it via the
module proxy, and the owner handle is the same account this repo publishes under —
it is the project's public identity, not a secret. The `replace` directive can stay
as-is. (Optional future cleanup: upstream the `ast/` re-export to goawk and drop the
`replace` entirely — nice-to-have, not required.)

## 2. Re-run the leak gate (must be clean)

The history was scrubbed at extraction time, but re-verify from a fresh clone of
exactly what you are about to publish. Fill the `<…>` placeholders in at the shell
**from a private note** — they are deliberately not written into this file so the
file itself carries none of the tokens it guards against.

> **Note on VCS.** The local working copy is **jujutsu (`jj`)**, which has no
> `.git/`, so the `git` commands below will not run against it directly. Run the
> gate against the actual git repository you are about to publish — either the
> `git+ssh` upstream, a colocated checkout (`jj git init --colocate` in a fresh
> clone), or `jj git export` first. The published artifact is git, so the gate
> must be verified in git form regardless.

```bash
# (1) no real home paths / internal project names anywhere in history.
#     <internal-path-tokens> = e.g. your real $HOME basename, internal project slugs.
#     EXPECTED (not leaks): the corpus uses generic `/home/user` and `/path/to/...`
#     placeholders in test fixtures — those are fine.
git log -p | grep -niE '<internal-path-tokens>' && echo "PATH LEAK"
# (2) no internal email prefixes / domains / org names anywhere.
#     <internal-id-tokens> = e.g. work email local-part, company domain, org slugs.
#     NOTE: the `shabbir-genetech` GitHub handle is NOT a token to scrub — it is the
#     public publishing account. Guard the *other* internal ids (work email, etc.).
git log -p | grep -niE '<internal-id-tokens>' && echo "ID LEAK"
# (3) author identity is a safe placeholder, not a real name/email:
git log --format='%an <%ae>' | sort -u   # expect only the placeholder identity
```

The fork-owner-handle check is retired: the handle is now the public publishing
identity (see section 1), so it is no longer a secret to scan for. Any hit on (1)
or (2), or an unexpected author in (3), is a deal-breaker — stop and re-scrub
(`git filter-repo --replace-text` / `--replace-message`) before publishing.

**Result (2026-06-09):** PASSED. Run against a fresh `git clone` of the published
remote over all (`--all`) history — author *and* committer identity are the demo
placeholder only; no email addresses; no `webadmin`/`genetech.co`; the only
absolute-home-path hits are the corpus's generic `/home/user` and `/path/to`
placeholders; `genetech`/`shabbir` appear only as the publishing handle. The
owner also confirmed there are no internal project codenames to scrub.

## 3. Final review

- **`LICENSE` — done:** MIT, copyright `shabbir-genetech` (adjust the holder to a
  legal name/entity if you prefer).
- **Module path — done:** `go.mod` is `github.com/shabbir-genetech/classify-bash`,
  so `go install github.com/shabbir-genetech/classify-bash@latest` works once the
  repo is public.
- **Third-party licenses — done:** the only linked deps are goawk (MIT © Ben Hoyt
  — the fork retained the notice) and mvdan/sh (BSD-3-Clause © Daniel Martí), both
  permissive and compatible with our MIT. `THIRD_PARTY_LICENSES` reproduces their
  notices for binary redistribution, and `nix flake check`'s `checks.licenses`
  (`go-licenses check`) fails if a future copyleft dependency sneaks in.
- **Commit subjects — done:** skimmed `git log` over all history; all subjects are
  generic, none reveal private context.
- **Docs — done:** `README.md`, `DESIGN.md`, and this file carry no internal
  hostnames or paths (the `shabbir-genetech` handle is fine — it is the publishing
  account).
- **Outstanding (optional, not blockers):** swap the `LICENSE` copyright holder to
  a legal name/entity if preferred; add a GitHub Actions workflow running
  `nix flake check`; upstream the goawk `ast/` re-export to drop the `replace`.

**Note — opt-in logging writes literal commands.** This is not a repo-content
leak, but a deployment one: with `--log` enabled, the hook records every
non-allowed command verbatim to the journal or a file (`log.go`). It is **off by
default**; the default file path resolves at runtime from `$XDG_STATE_HOME`/`$HOME`
(no hardcoded paths). When enabling it on a shared or recorded host, treat that log
as containing whatever Claude tried to run, and scope its location/retention
accordingly.
