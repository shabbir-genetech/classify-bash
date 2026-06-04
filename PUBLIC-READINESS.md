# Public-readiness checklist

This repo was extracted from a private parent repository with its history
**scrubbed** so it could be made public safely. It is currently **private**.
Run through this checklist **before** flipping it to public.

## 1. Resolve the private goawk fork (hard blocker)

`go.mod` pins a private fork via a `replace` directive so `styleAwk` can walk the
goawk AST (it re-exports goawk's `internal/ast` types — see
[DESIGN.md](DESIGN.md)). A public build must be able to resolve that dependency,
and the fork's owner handle would otherwise become public. Pick one:

- **Vendor it** — `go mod vendor` and commit `vendor/`, so there is no external
  dependency to fetch. (The `replace` directive still names the fork's path; pair
  with one of the options below if the handle itself must not appear.)
- **Publish the fork** under a neutral/public account and point `replace` at that.
- **Upstream the re-export** to goawk so the `replace` directive can be dropped
  entirely — the cleanest end state.

## 2. Re-run the leak gate (must be clean)

The history was scrubbed at extraction time, but re-verify from a fresh clone of
exactly what you are about to publish. Fill the `<…>` placeholders in at the shell
**from a private note** — they are deliberately not written into this file so the
file itself carries none of the tokens it guards against.

```bash
# (1) no real home paths / internal project names anywhere in history.
#     <internal-path-tokens> = e.g. your real $HOME basename, internal project slugs.
git log -p | grep -niE '<internal-path-tokens>' && echo "PATH LEAK"
# (2) no internal email prefixes / domains / org names anywhere.
#     <internal-id-tokens> = e.g. work email local-part, company domain, org slugs.
git log -p | grep -niE '<internal-id-tokens>' && echo "ID LEAK"
# (3) the goawk-fork owner handle appears ONLY where mechanically required
#     (go.mod / go.sum), and NOWHERE once step 1 removes the private fork:
git rev-list --all | while read c; do
  git grep -nI -e '<fork-owner-handle>' "$c" -- . ':!go.mod' ':!go.sum'
done | grep . && echo "HANDLE LEAK"
# (4) author identity is a safe placeholder, not a real name/email:
git log --format='%an <%ae>' | sort -u   # expect only the placeholder identity
```

Any hit is a deal-breaker — stop and re-scrub (`git filter-repo --replace-text` /
`--replace-message`) before publishing.

## 3. Final review

- Skim `git log --oneline` for any commit subject/body that reveals private
  context.
- Confirm `README.md`, `DESIGN.md`, and this file contain no internal handles,
  hostnames, or paths.
- Decide on a `LICENSE`.
