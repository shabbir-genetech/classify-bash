# Future work

Design ideas for `classify-bash` that are **agreed-in-principle but not yet
implemented**. This file exists so the reasoning, caveats, and open research
questions survive between sessions — read it alongside [DESIGN.md](DESIGN.md)
(the rationale for what *is* built) and [README.md](README.md) (the contract).

Every idea here must preserve the project's one load-bearing invariant: the hook
only ever *adds* an `allow`, and anything it cannot positively classify falls
through to the normal permission prompt. A bug may fail to accelerate; it must
never wave through a write/exec/network side effect. Keep that asymmetry in every
extension below.

Status legend: **IMPLEMENTED** (shipped) · **APPROVED** (signed off, ready to
implement) · **CANDIDATE** (log-surfaced, low-risk, awaiting sign-off) ·
**DEFERRED** (agreed valuable, parked) · **RESEARCH** (needs investigation before
a spec).

> **Paths:** bare filenames below (`classify.go`, `commands.go`, `spec.go`,
> `awk.go`) are in `internal/engine/`. They sat at the repo root until the
> package split; the file contents and line references are unchanged unless noted.

### IMPLEMENTED — make the unknown-AST-node failure configurable

The exposure: the classifier called `failLoud` (exit 2) on an `mvdan/sh` AST node
kind it did not handle, and since `PreToolUse` exit 2 *blocks* the tool, that was
the last remaining path by which this binary could take the Bash tool down. Same
shape as the harness-field problem from a different direction — an upstream parser
upgrade introducing a node kind would block every command containing it.

Shipped (in the fork, merged here 2026-10-23) as `--on-unknown-ast=fail|fallthrough`,
one of two strictness axes in `cmd/classify-bash/policy.go`, with `claude-lenient`
selecting `fallthrough` for deployed registrations. The legacy bare registration
keeps `fail`, on purpose: it must reproduce the pre-merge behaviour exactly.

Two decisions worth not relitigating:

- **The engine no longer decides what "fail" means.** `Classify` returns a
  `Result` carrying `Unparseable` plus the error; the *adapter* maps that to exit
  2, a block, or an ordinary decline. The engine cannot call `os.Exit` at all.
- **One flag, not one per site.** From an adapter's point of view an unknown
  command-AST kind and an unknown awk-AST kind mean the same thing — the
  classifier does not understand a tree it saw. Per-site switches would be
  speculative generality with no consumer.

**Inventory of the blocking sites (re-counted 2026-10-23, after the merge).** The
engine's aborts are policy-gated now; the CLI's exits are not:

| Kind | Sites | Blocks? |
|---|---|---|
| Unhandled construct (engine abort → `Unparseable`) | `classify.go` command kind, redirect op, **binary op** (added with §6 scoping); `spec.go` flag style; `awk.go` stmt and expr kind | only if `--on-unknown-ast=fail` (legacy path, `claude-strict`) |
| Operator error at the registration site | `cmd/classify-bash/hook.go`: bad `--log-*` flag (legacy and `hook` paths) | yes, deliberately |
| Policy asked to be loud | `hook.go`: unknown field under `fail` | yes, by request |
| I/O failure while emitting | `hook.go` `emitAllow`: `write stdout` | **yes — still open, see below** |

**`write stdout` failing still blocks, and it should not.** `emitAllow` calls
`failLoud` if writing the allow JSON fails, so a full disk — or a host that closes
the pipe early — blocks every *allowable* command, while unallowable ones are
unaffected (they never write). That is the inverse of the intended failure
direction. Reproduced 2026-09-10 against the pre-merge binary:

```console
$ echo '{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}' \
    | classify-bash > /dev/full
classify-bash: write stdout: write /dev/stdout: no space left on device
$ echo $?
2
```

**Smallest correct fix: `failOpen` there instead of `failLoud`** — the allow was not
delivered, so no-opinion is exactly right, and an `undecodable` record carries the
reason. A two-line change with no interface implications; the merge moved it from
`main.go` to `hook.go` without changing it.

**A precedent worth keeping in mind.** `fb5978c` solved the unknown-field problem by
separating *withholding the allow* (always safe — the hook only ever adds one) from
*blocking the call* (exit 2). The AST flag keeps a blocking value; if the legacy
path is ever retired, the argument for `fail` there is the same one the strict JSON
decoder rested on, and the same counter applies: a blocking gate cannot warn you,
it just stops you. An unhandled node means the classifier cannot reason about the
command, so falling through is not merely safe but *correct*.
### Two kinds of "must not allow" (test taxonomy)

The corpus splits fall-through cases by *why* they fall through, because the two
have opposite regression semantics:

- **`TestMustNotAllow`** — the safety wall. The command, *as written*, would cause
  a write/exec/network/unverifiable side effect if allowed. A regression here is a
  security incident.
- **`TestNotYetAllowed`** — harmless *as written*; falls through only because a
  classifier feature below isn't built. A regression here (it starts to Allow) is
  the *expected* result when that feature lands — move the case up to
  `TestMustAllow`. A case belongs here ONLY if provably harmless; when in doubt it
  goes in the wall.

When you implement any item below, the acceptance step is the same: the relevant
`TestNotYetAllowed` cases move to `TestMustAllow`, and you add the new
`TestMustNotAllow` cases for the unsafe siblings the feature must still reject.

---

## Observed demand — first log-corpus analysis (2026-06-09)

The opt-in journal sink (`journalctl -t classify-bash`) was read end-to-end for
the first time on 2026-06-09. This is our first *empirical* signal about which
fall-throughs actually happen, and it both re-orders the items below and adds
three new ones (§§5–7). Caveat up front: **121 records, one day, one operator** —
directional, not a stable rate. Re-run on a larger / multi-user corpus before
treating the ordering as settled.

### Health signal — clean
- **All 121 records are `kind:"fallthrough"`; zero `failloud`.** No `failLoud`
  fired in real traffic: no strict-decoder name-drift (no unrecognized event /
  `tool_input` field), and no unknown `mvdan/sh` AST node, redirect op, or flag
  style. The fail-loud surfaces are quiet — nothing is silently going stale.
- **~99 of 121 genuinely mutate** (commits, pushes, `nix build`/`flake check`, a
  privileged escalation, `rm`, an interpreter, repo-visibility edits, real `>`
  writes) and *correctly* fell through. The allow-only invariant held in the wild:
  nothing side-effecting was accelerated.

### Methodology learning — replay, don't infer
The first two passes of this analysis drew **wrong** conclusions from regex
inference (blamed the `cd` prefix, then statement sequencing). Both were refuted
by replaying the actual lines through the built binary: `cd <literal>`, `command
-v`, `git` read subcommands (`show`/`log`/`rev-parse`), extended `grep` flags
(`-rhoE`), `;`/newline sequencing, and literal `"$(...)"` **all already allow**.
The lesson for the next corpus pass: **classify each candidate by piping it
through `./result/bin/classify-bash`, not by pattern-matching the string.**

### The real misses (~20 read-only commands), verified by replay
Of the ~20 records that were read-only yet fell through (an accelerator miss, not
a safety event), the verified blockers are:

| bucket | what it is | status |
|--------|------------|--------|
| **un-whitelisted reader** | `gh` (read subcmds: `api`, `repo view`, `auth status`), `journalctl`, `sed` | coverage gap → new §5 (`gh`/`journalctl`); `sed` is §4 |
| **`$VAR` expansion** | `T=<literal>; … git show "$T:f"` — a *locally-assigned literal*, not `read`-bound | refines §3 → new §6 |
| **`for` loop** | `for f in <literal words>; do <read with $f>; done` | §3 is `while`-only → new §7 |

The dominant real idiom is interactive investigation: `cd` to a literal dir, then
a sequence of `echo "=== label ==="` interleaved with read-only `grep` / `git
show` / `sed`, frequently parameterized by a variable holding a literal commit
hash. Two sub-blockers stack in that idiom — the assignment-only statement
(`T=…`) is itself rejected (`classifyCall` drops any stmt with `Assigns`), *and*
the later `$T` expansion is rejected by `wordLiteral`. §6 must clear both.

### What this says about the existing roadmap
- **Tier-2 `--` leniency (§2) and the `sed` parser (§4) unlock ≈0 of the observed
  misses.** `sed` appeared only as `sed -i` (write) or buried inside an
  already-rejected pipeline — confirming §4's first-principles "ROI ≈ 0" call,
  now with data. Leave both deferred.
- The cheap, high-frequency wins are the un-whitelisted readers (§5) and the
  literal-`$VAR` / literal-`for` pair (§6 → §7). None is approved yet — they need
  the usual design-first sign-off — but the log says they would retire the bulk of
  real read-only misses.

## Observed demand — third pass (2026-07-27, `TestTriage`)

1,976 records (2026-06-09 → 07-27), the first pass run with `triage_test.go`
rather than by hand. Same caveat as before: one operator, one host — directional.
Numbers below count records whose **primary blocker** is the named construct,
restricted to the 1,746 logged after `commands.go` last changed (2026-06-11);
the other 230 were judged by an older whitelist. Only 12 of the full 1,972
fall-throughs would allow under the current build, so the corpus is live, not stale.

- **Health: 4 `failloud`, all the strict-decoder tripwire** — `prompt_id` ×2 and
  `dangerouslyDisableSandbox` ×1 from real traffic, `totally_new_field` ×1 from a
  deliberate probe. Both real fields are now enumerated (`59e564fd`). No unknown
  AST node, redirect op, or flag style has ever fired. This is the 4th instance of
  the harness-field trap; expect a 5th.
  - **The 5th arrived and ended the mechanism (2026-09-10, `scratchpad_dir`).**
    Claude Code 2.1.267 attaches it to *every* `PreToolUse` event, so it was a
    whole-session Bash outage rather than the intermittent shape of `effort`.
    That instance is why `DisallowUnknownFields` was removed instead of gaining a
    fifth enumerated field — see DESIGN.md, "Defensive JSON contract".
    **Consequence for this corpus: `failloud` is no longer the health signal to
    read.** Unknown fields now surface as `schema_drift` records naming the field,
    so a future triage pass should count those; a nonzero `failloud` count now
    means a bad `--log-*` flag or an unknown AST node, both of which are
    operator- or dependency-driven rather than harness-driven.
- **`jj` global flags before a subcommand — the one substantial new gap (~66).**
  **IMPLEMENTED 2026-07-27** (`rnrrlwul`); see DESIGN-jj-global-flags.md. A
  dedicated re-count over the same corpus put the read-only win at 147 records,
  higher than the ~66 estimated here, because this pass counted only the three
  subcommands named below while the whitelist covers more (`show`, `op log`,
  `bookmark list`, `git remote list`).
  `-R`/`--repository` lives in `jjCommonFlags()` (attached to *subcommand* specs)
  while top-level `jjSpec()` has `Flags: nil`, so `jj st` allows but
  `jj -R <path> st` falls through. Read-only uses: `log` 29, `diff` 21, `status`/`st`
  16. The same shape also blocks `commit` 118 / `git` 30 / `new` 17, which is the
  correct outcome — count only the read-only subcommands. Pure whitelist data, no
  classifier change.
- **`journalctl -b -1` (14). IMPLEMENTED 2026-07-27** (`rnkurlso`) via a new
  `flagSpec.DashValueOK`. The diagnosis recorded here — "`-b` is `OptionalArg`,
  so the negative-number value parses as a flag" — was **wrong in its
  implication**, and the correction is the useful part: `OptionalArg` is behaving
  correctly (it means "`--name=value` only, never consume the next token"), and
  `journalctl -b 0` / `-b all` *already* classified allow, because
  `AllowAnyPositional` swallows a non-dash value. Only a **dash-prefixed** value
  failed. So this was not a `matchGNU` bug to fix but a missing capability to
  express: "this flag may take a dash-prefixed value". `DashValueOK` consumes one
  following token matching `^[+-][0-9]+$` — digits only, so it can never swallow a
  real flag.
- **Structural, unchanged from the 2026-06 pass:** `<variable assignment>` 196 and
  `<for/while loop>` 93 remain the two largest buckets. Both mostly wrap a mutating
  `jj commit`, so the read-only payoff is far below the raw counts — §6/§7 stay
  deferred on that basis.
- **`sed` 67 / `pgrep` 44 — re-opened, contradicting the 2026-06 read.** The earlier
  pass saw `sed` only as `-i` or inside already-rejected pipelines and called ROI ≈ 0
  (§4). Post-freeze it is the largest `MISSING-CMD`, essentially all `sed -n '<range>p'
  <file>` on this host. `pgrep -af <pattern>` is a clean read-only leaf and a good
  `ArgvDataSafe` candidate. Neither is approved; `sed` still needs §4's parser
  question answered (`-i`, and the `w`/`W`/`s///w` write commands) before a spec.
  Both remain open after the 2026-07-27 session — they are now the two largest
  known accelerator gaps.
- **Correctly rejected, no action:** `curl` 61 (network), `mariadb`/`mysql` 51,
  `rm` 17, `kill` 18, `pkexec`/`nrs` 24, `devenv shell`/`up`/`processes` 134
  (execution wrappers, see §2's "Deferred wrapper shape"), `python3`/`bash` 33.

## Observed demand — fourth pass (2026-10-23, `TestTriage` + replay)

13,118 fall-through records over four weeks (2026-09-25 → 10-23; the journal held no
older ones), judged by the `392eebb` whitelist, which predates them all, so nothing
was already-fixed noise. 0 `failloud`, 0 `schema_drift`.

- **`<variable assignment>` was the primary blocker on 9,573 records (73%)** — no
  longer "mostly wrapping a mutating commit" (third pass): 82% of assignment-bearing
  records bound a path under the session scratchpad, in the shape `S=<scratchpad>`,
  `cmd > "$S/out.txt"`, `cat "$S/out.txt"`. The harness asks agents to keep temporary
  output in the scratchpad, and no-truncation rules steer them to redirect rather
  than pipe into `tail`, so the idiom is what a rule-following agent sends.
- **The two levers stack.** Rewriting the commands and replaying: inlining literal
  assignments alone flipped 3.8%; inlining *and* dropping the scratchpad redirect and
  read-back flipped 16.5%. Shipping §6 + §10 together, the built binary flipped
  **2,110 records (16.1%)**, with 0 blocked.
- **Next tier, each a few hundred records:** `MISSING-CMD sed` 369 (§4 — now real
  demand), `<for/while loop>` 240 (§7), `MISSING-CMD az` 188, `command grep` 65
  (`command`'s spec rejects the wrapped command's flags). Small read-only gaps:
  `jj file show`, `jj --ignore-working-copy`, `git ls-remote`, `pgrep`.
- **Correctly rejected, no action:** `devenv shell` 323, `jj commit`/`bookmark`/`git
  push`/`new` (~420 together), `rm` 55, `pkexec` 38, interpreters.
- **Method notes.** A regex segmenter attributed ~3.7k records to fragments (`{`,
  `(cd …`, `for`) that are artifacts of splitting on newlines — `TestTriage` again
  had the right answer. And the log is not durable: the host's journal kept only
  about four weeks (it is capped by file *count*, so retention is measured in boots),
  so a "since logging began" claim must check the first record's timestamp.

---

## 1. Command substitution `$(...)` — **IMPLEMENTED (v1 — Tier 1)**

Shipped: quoted `"$(...)"` with a read-only inner command, accepted as an opaque
positional by an `ArgvDataSafe` outer command. The `xargsWrappable` map was
replaced by the `ArgvDataSafe` field on `commandSpec` (one source of truth), and
`echo`/`ls`/`readlink`/`basename`/`dirname` were added to it.

The authoritative spec is now **DESIGN.md "Command substitution"** (rules, the
embedded-vs-separate flag-token asymmetry, hazards) plus the `TestMustAllow` /
`TestMustNotAllow` / `TestNotYetAllowed` corpus. One implementation finding worth
flagging here because it corrected the original design: `ls --color="$(ls)"` is a
single opaque token and **allows** for an `ArgvDataSafe` command (any operand value
is harmless) — only the *separate-token* `head -n "$(…)"` form falls through on the
"flag arguments must be literal" rule. The remaining tier (`--` leniency below)
stays deferred.

---

## 2. Command substitution Tier 2: `--` leniency — **DEFERRED (v2)**

> **Log evidence (2026-06-09):** 0 observed demand in the first corpus — no
> fall-through would have been rescued by `--` leniency. Stays deferred; revisit
> only if real `sort -- "$(…)"`-shaped traffic appears.

### Idea
A literal `--` tells a getopt-style command "everything after is positional, not
a flag," which **eliminates the flag-injection vector**. Since `ArgvDataSafe`
mostly excludes commands *because an injected token could be a flag*, a preceding
`--` widens what's safe:

- **Tier 1 — `ArgvDataSafe` (no `--` needed):** safe under *any* argv token,
  flag-shaped or not. (`cat`, `grep`, `echo`, `ls`, …)
- **Tier 2 — requires a literal `--` before the operand:** safe under any
  *positional value* but **not** flag-safe — its only write/exec path is via a
  flag, which `--` closes.

Canonical example: `sort` is in `safeCommands` (read-only spec; `-o`/`--output`
not whitelisted) but **not** `ArgvDataSafe`, because a substituted value `-o/x` or
`-oFILE` would reach the real `sort` and write. `sort -- "$(…)"` can only read +
sort the named file. Represented as a second spec bool,
`ArgvDataSafeAfterDashDash` — like Tier 1, a *subset of the main list* marked by a
property, never a new command list.

### Tier 2 is a whitelist, never a blacklist
`ArgvDataSafeAfterDashDash` is a positive, default-`false` opt-in: a command
receives a substituted operand after `--` **only if it is individually vetted and
marked**, with `mustAllow`/`mustNotAllow` tests — exactly like `ArgvDataSafe` and
`safeCommands` itself. The tempting shortcut is a *blacklist* — "after a literal
`--`, allow a substituted operand for *any* command except a deny-list of
dangerous ones" — and it is **wrong**: it fails *open*, so a tool we never
considered, or a future release that adds a `--`-immune write path, slips through.
This is why `find` being `--`-immune (its `-delete`/`-exec` predicates ignore
`--`) is simply *a reason to leave it off the whitelist*, not something a blacklist
would have to enumerate. Default-deny, positively add. See DESIGN.md "Strict
positive whitelist".

### Caveats / why it's its own vetted set
- **`--` is not universal.** Non-getopt tools parse their own grammar: `find`
  keeps treating `-delete`/`-exec` as predicates regardless of a leading `--`, so
  `--` does **not** make `find` safe. Tier 2 therefore needs its own **vetted**
  set, not "any command + `--`".
- The matcher must track *"a literal `--` preceded this operand"* (a real `--`,
  never a substituted one) within the same simple command. `matchWrapper` already
  has `--`-required precedent to borrow from.
- This is *why two sets are justified* (the Q4 "DRY" question): Tier 1 and Tier 2
  encode **different** properties — flag-safe vs. positional-value-safe — so two
  spec booleans, not drift of one.

### Open research before implementing
- Enumerate Tier-2 candidates and **verify each command's `--` semantics**
  actually neutralize its only write path (start: `sort`; survey `uniq` —
  positional `OUT` file writes even *with* `--`, so likely excluded; check the
  GNU coreutils that have an `-o`/output-file flag).
- Confirm `mvdan/sh` exposes `--` position cleanly enough to track per-operand.

---

## 3. `while` loops — **DEFERRED**

### Structural feasibility
`while COND; do BODY; done` where COND and every BODY statement classify safe is
"safe things, repeated" — defensible in isolation. `classify.go` currently
rejects `*syntax.WhileClause` outright ("too easy to hide writes inside these").

### Why it buys almost nothing today
The useful idiom is `while read VAR; do … "$VAR" …; done`, which needs **two**
things we don't have and don't want lightly:

- `read` whitelisted — a builtin that **assigns a shell variable**; and
- **`$VAR` expansion allowed** — a far deeper invariant break than `$(...)`,
  requiring **taint-tracking**: "$VAR is safe because `read` bound it in this
  loop." That value still flows into argv as attacker-controlled data — the same
  stdin→argv hazard, so any command consuming `"$VAR"` would itself need to be
  `ArgvDataSafe` (Tier 1) reasoning applied to *variable* expansion, not just
  command substitution.

Without those, every realistic loop body references the loop variable and falls
through anyway. Plus auto-allowing `while true; do …; done` hangs the tool with
no prompt (a UX problem, though not a *write* — our threat model is side effects,
not DoS).

### Open research before implementing
- A taint model for loop-bound variables (which expansions are provably
  data-only) and how it composes with the `ArgvDataSafe` set.
- Whether to scope to a closed form (`while read X; do <ArgvDataSafe cmds with
  "$X"> ; done`) rather than general `while`.
- Sequencing: this should land *after* command substitution, since it reuses the
  same data-into-argv reasoning.

---

## 4. `sed` parser — **DEFERRED / on-demand (RESEARCH)**

> **Log evidence (2026-06-09):** confirms "ROI ≈ 0". In the first corpus `sed`
> showed up only as `sed -i` (write — correctly rejected) or inside an
> already-rejected pipeline; no read-only `sed` line fell through *because of sed
> itself*. Demand bar for building this is unmet.
>
> **Superseded.** The third pass (2026-07-27) found `sed` the largest `MISSING-CMD`
> (67), and the fourth (2026-10-23) the primary blocker on **369** records, nearly
> all `sed -n '<range>p' <file>`. The demand bar is now met; the parser question
> (`-i`, and the `e`, `w`/`W`, `r`/`R`, `s///w` commands) is what remains.

### Feasibility
Doable by direct analogy to `awk.go` (`classifyAwkProgram` positively
whitelists awk AST nodes via the goawk fork): parse the sed script, walk it, and
allow only a read-only, stdout-only subset, failing closed on anything
unrecognized.

### Blockers / why it's low priority
- **No clean, well-licensed Go sed *parser* library** to reuse the way goawk gave
  us an awk AST. We'd hand-roll a parser for a fiddly language (addresses,
  ranges, branches/labels, `{}` groups, `y///`) where a single parse bug can hide
  a side-effecting command — exactly the failure mode the project exists to
  avoid. Any parser must **fail closed**.
- **Must reject:** `-i`/`--in-place`, `-f scriptfile` (script isn't a literal in
  argv), `w`/`W` and `s///w` (file writes), `e` and `s///e` (shell exec), and
  arguably `r`/`R` (reads arbitrary files into output — info disclosure).
- **ROI ≈ 0 on current logs.** The two real `sed` uses observed are blocked by
  their *surrounding* constructs (a `while` loop and a `$(...)` list), not by
  `sed` itself, and `awk` already covers most read-only sed idioms
  (substitute-and-print, field work). Revisit only if real demand appears *after*
  command substitution lands.

### Open research before implementing
- Survey existing Go sed implementations for a vendorable, permissively-licensed
  parser (license check is a hard gate here — see `checks.licenses`), vs.
  hand-rolling a formal sed-subset grammar.
- Settle the `r`/`R` (file-read) policy against the precedent that `awk FILE` and
  `cat FILE` already read arbitrary literal paths.

---

## 5. `journalctl` and `gh` read subcommands — **IMPLEMENTED (2026-06-10)**

Scope locked by sign-off (2026-06-09): **D1** include `gh api` GET-only · **D2**
`gh` observed-only subcommands · **D3** exclude journalctl alternate-location flags.
Shipped in two steps: `journalctl` (flag forms) + `gh auth status` first; then
**§8** (post-positional flag validation) unblocked the positional-bearing forms —
`gh repo view`, `gh api`, and journalctl positional field-matches. All now live:

| target | status |
|--------|--------|
| `journalctl` read flags + positional matches | ✅ (`AllowAnyPositional: true`, safe via §8) |
| `gh auth status` | ✅ |
| `gh repo view` | ✅ (via §8) |
| `gh api` (GET-only) | ✅ (via §8) |

Whitelist-only throughout: dangerous modes are left *off* the whitelist (they fall
through by default) and recorded in `// deliberately excluded` comments, never as a
deny-list. `ArgvDataSafe: false` on every spec here — none may receive an opaque,
attacker-controlled operand (`gh` writes over the network; journalctl's `--vacuum-*`
is a flag-reachable delete).

### Origin of §8 — the finding that drove the engine change
The original spec assumed whitelist-only closes the write vector: "never list
`-X`/`--web` and they're rejected." That was **false** for the pre-§8 engine —
`matchGNU` swallowed every token after the first positional as data without
re-validating flag-shaped ones (`cat file -X` → allow). Harmless for `cat`/`git
log` (no dangerous flag), but `gh repo view <repo> --web` and `gh api <endpoint>
-X POST` carry the dangerous flag in *after* the required positional, so they would
have been allowed. That finding became **§8** (now implemented): for non-
`ArgvDataSafe` commands, flags are validated wherever they appear, so the trailing
`--web`/`-X` is rejected. journalctl likewise now safely sets
`AllowAnyPositional: true` — a `journalctl _SYSTEMD_UNIT=x --vacuum-size=1G` is
rejected on the unwhitelisted flag even after the positional.

### `journalctl` — `styleGNU`, `AllowAnyPositional: true`, `ArgvDataSafe: false`
- **Whitelisted (read/query/filter/format):** `-t/--identifier`, `-u/--unit`,
  `--user-unit`, `-n/--lines`, `-S/--since`, `-U/--until`, `-p/--priority`,
  `-g/--grep`, `--case-sensitive`, `-b/--boot`, `-k/--dmesg`, `-o/--output`,
  `--output-fields`, `-r/--reverse`, `-e/--pager-end`, `-x/--catalog`, `-a/--all`,
  `-q/--quiet`, `-m/--merge`, `--utc`, `--no-pager`, `--no-tail`, `--no-full`,
  `--no-hostname`, `-N/--fields`, `-F/--field`, `--list-boots`, `--header`,
  `--disk-usage`, `--version`, `-h/--help`. Positionals = field matches
  (`_SYSTEMD_UNIT=foo`) / executable paths.
- **Deliberately excluded (fall through):** `--vacuum-size`, `--vacuum-time`,
  `--vacuum-files` (delete journals), `--rotate`, `--flush`, `--sync`,
  `--relinquish-var`, `--smart-relinquish-var`, `--setup-keys`, `--update-catalog`,
  `--verify`; **`-f/--follow`** (never terminates — `TestNotYetAllowed`, not the
  wall); and per **D3** the alternate-location readers `-D/--directory`, `--file`,
  `--root`, `-M/--machine`, `--namespace`.

### `gh` — modeled on `gitSpec()`, `ArgvDataSafe: false` everywhere
```
gh
├── auth → status   (-h/--hostname, -a/--active; EXCL -t/--show-token = secret)
├── repo → view     (--json, -q/--jq, -t/--template, -b/--branch, -R/--repo;
│                     OWNER/REPO positional; EXCL -w/--web = browser launch)
└── api             (GET-only: --cache, --paginate, -q/--jq, -t/--template,
                      --hostname, -i/--include, --slurp; ENDPOINT positional;
                      EXCL -X/--method, -f/--raw-field, -F/--field, --input, -H)
```
All mutating subcommands (`repo edit|create|delete`, `pr/issue/release create…`,
`auth login|logout|token`) are simply absent → fall through.

### Field note — first deploy, observed 2026-06-10
A general learning worth keeping: **§5's real reach is narrower than the raw
"journalctl demand" count suggests.** Most observed journalctl use *redirects to a
file* (`journalctl … > /tmp/log`, to parse later), which falls through on the
universal write-redirect rule **regardless of §5** — only direct-to-terminal
queries accelerate. Not a §5 limitation; the redirect rule interacting with the
dominant usage pattern. (Measured the same day: replaying all 158 logged
fall-throughs through the then-new binary, 10 flipped to allow — only ~2
§5-attributable, the rest earlier features the deployed binary predated.)

### Code & tests (as built)
- `commands.go`: `"journalctl"` (Tier C) + `"gh"` (Tier B); builders
  `journalctlSpec`, `ghSpec`, `ghAuthSpec`, `ghAuthStatusSpec`, `ghRepoSpec`,
  `ghRepoViewSpec`, `ghApiSpec`. Engine change for the positional forms is §8.
- **`TestMustAllow`:** the journalctl flag + positional forms, `gh auth status`
  (+`--active`/`--hostname`), `gh repo view o/r [--json … -q …]`,
  `gh api repos/o/r --jq … / --paginate`, plus the §8 guard `git log HEAD -p`.
- **`TestMustNotAllow` (the wall):** the journalctl management flags and the
  post-positional guards (`journalctl _SYSTEMD_UNIT=x --rotate`,
  `gh repo view o/r --json name --web`, `gh api repos/o/r --paginate -X DELETE`),
  the gh mutating subcommands, and `gh auth status --show-token`.
- **`TestNotYetAllowed`:** `journalctl -f` (follow, excluded); `gh pr view 123`,
  `gh issue list` (read subcommands outside the observed-only set).

---

## 6. `$VAR` expansion for locally-assigned literals — **IMPLEMENTED (2026-10-23)**

> **Shipped** with §10. The research questions below were answered as follows; the
> rationale is DESIGN.md "Literal-bound variables", the rule list `vars.go`.
> - *Symbol-table model:* a `scope` per walk. A statement list binds sequentially;
>   an `&&` chain binds along itself and no further; `||` branches and pipeline
>   stages are isolated; `( … )` and `"$(…)"` inherit and never leak. Re-assignment
>   overwrites. Nothing else whitelisted can assign (`printf` has no `-v`;
>   `{name}>` redirects are now rejected).
> - *Default-closed:* every unbound name, every operator form (`${a:-b}`, `${#a}`,
>   …), and every right-hand side with an expansion, tilde, backslash or `$'…'`
>   falls through. An unquoted use must not split or glob.
> - *Not anticipated here:* the **name**. Assigning an exported variable changes
>   child processes, so bindable names are 1–3 characters, minus a reserved few,
>   and absent from the hook's environment. A command made only of assignments
>   still falls through.
> - *Acceptance* as proposed, plus the scope and name cases; every check was
>   mutation-tested (DESIGN.md "AST handling").

*Original research note, kept for the reasoning:*

### Idea
The single biggest *structural* blocker in the first corpus. The real idiom is

```
T=24e0610d
git show "$T:app/x.php"
```

— a variable **assigned a literal earlier in the same compound command**, then
expanded as data. This is a strictly smaller and safer slice of §3's taint problem
than the `while read VAR` case: a locally-assigned literal is **not** attacker-
controlled (no stdin → argv), so it sidesteps the stdin-argv hazard entirely.

### Two sub-blockers must both clear
1. **The assignment statement itself is rejected.** `classifyCall` drops any stmt
   with `len(c.Assigns) > 0` (env mutation we don't auto-allow). A literal-only
   assignment (`T=24e0610d`, RHS classifies via `wordLiteral`) would need to become
   a recognized, side-effect-free statement that *binds a name to a literal* in a
   per-invocation symbol table.
2. **The expansion is rejected.** `wordLiteral` (and `argTokens`) reject every
   `ParamExp`. The classifier would have to resolve `$T` against that symbol table
   and treat it as the literal it was bound to — failing closed on any name not
   bound to a literal in this same parse (no env, no `read`, no `$(...)`-assigned
   value unless that value is itself argv-data-safe).

### Why it's safe in this closed form
The value is known at classify time (it came from a literal in the same string),
so it is exactly as safe as writing the literal inline. The danger is scope creep:
the moment a tracked var can be bound from `read`, `$(...)`, or the environment, we
are back in full §3 taint-tracking and the `ArgvDataSafe` reasoning must apply to
*variable* expansion, not just command substitution. Keep v1 to **literal-bound
names only**.

### Open research before implementing
- The symbol-table model: scope (per `*syntax.File`? per compound command?),
  shadowing, and re-assignment. Confirm `mvdan/sh` gives assignment nodes and
  expansion sites cleanly enough to thread a binding through.
- Default-closed behavior for every unbound or non-literal-bound `$VAR`.
- Acceptance: `T=…; git show "$T:f"` and `echo "$T"` move to `TestMustAllow`;
  `read T; echo "$T"`, `T=$(…); rm "$T"`, and bare `$HOME`/`$PATH` stay in
  `TestMustNotAllow` (or `TestNotYetAllowed` only where provably harmless).

---

## 7. `for X in <literal words>; do <read body>; done` — **RESEARCH (depends on §6)**

### Idea
`for`-loops appeared in the corpus over **literal word lists** with read-only
bodies (`for f in a.php b.php; do git show "HEAD:$f"; done`). A loop over a fixed
literal list whose body classifies safe is "safe things, repeated" — the same
defensibility argument §3 makes for `while`, but *without* the `while read` taint
problem because the iteration values are literals in the source.

### Why it depends on §6
`classify.go` rejects `*syntax.ForClause` outright (line ~83). Opening that gate is
only half the work: every realistic body references the loop variable `$X`, which
is a `ParamExp` and falls through today. So §7 needs §6's symbol-table machinery,
extended to **bind the loop variable to each literal word in turn** — the loop
classifies allow iff the word list is all literals *and* the body classifies safe
with `$X` bound to a literal.

### Scope guard (whitelist, not blacklist)
- Word list must be **all literals** — reject `for f in *` (glob), `for f in
  $(…)` (cmdsubst), `for f in $LIST` (var). A glob/cmdsubst list reintroduces
  attacker-controllable iteration values.
- Body recurses through the existing classifier with `$X` bound; no special-casing.
- `while`, `until`, C-style `for ((;;))`, and infinite forms stay rejected (§3 and
  the DoS note there still apply).

### Open research before implementing
- Whether to land §6 first and treat §7 as "§6 + bind loop var", or design them
  together (the symbol-table is shared).
- Acceptance: `for f in a b; do echo "$f"; done` and the literal-list `git show`
  form move to `TestMustAllow`; `for f in *`, `for f in $(ls)`, and any write/exec
  body stay in `TestMustNotAllow`.

---

## 8. Validate flags *after* positionals in `matchGNU` — **IMPLEMENTED (2026-06-10)**

### The limitation it fixed
Pre-§8, `matchGNU` stopped parsing flags at the first positional and accepted the
whole tail as data without re-checking flag-shaped tokens, so `cat file -X` and
`git log HEAD --output=/etc/passwd` both allowed. Sound only because every
`AllowAnyPositional` command then shipped had no dangerous flag. It broke for any
reader whose dangerous behavior is a *flag* while its primary argument is a
*positional* — `gh repo view <repo> --web`, `gh api <endpoint> -X POST`,
`journalctl <match> --vacuum-size=1G` — where the dangerous flag lands after the
positional and was swallowed. (Found implementing §5; that finding became this item.)

### What shipped
`matchGNU` now follows GNU getopt **permutation**: it keeps scanning and validating
flags against `spec.Flags` even after positionals appear, so an unwhitelisted flag
falls through wherever it lands. `handlePositionals` was replaced by
`dispatchSubcommand` (first positional selects a subcommand, as before) +
`acceptPositionals` (leaf decision over the collected positionals). The behavior:
- `git log HEAD -p` → allows (`-p` whitelisted) — no regression for "flag after arg".
- `gh api repos/o/r -X POST` / `gh repo view o/r --web` → **fall through** (`-X` /
  `--web` not whitelisted, even after the positional).
- `journalctl _SYSTEMD_UNIT=x --vacuum-size=1G` → falls through.

**Resolved design decisions (the former open questions):**
- *Permutation vs. POSIX strict-ordering* → **permutation** (matches GNU default;
  we parse statically with no env to honor `POSIXLY_CORRECT`).
- *Do `ArgvDataSafe` commands keep the "tail is data" fast path?* → **yes.** An
  `ArgvDataSafe` command is safe under any argv (flag-shaped included), so its first
  positional still closes flag parsing and the rest is opaque data — this preserves
  `cat file --anything` and is the documented exception in `matchGNU`. Non-
  `ArgvDataSafe` commands get full permuted validation.
- *Audit for regressions* → none: the whole corpus stayed green; the only newly-
  rejected forms are unwhitelisted post-positional flags (never in `TestMustAllow`).

The `--` end-of-flags rule, subst-token gating, and `AllowAnyPositional == false`
behavior are all preserved. This is what let §5's `gh repo view` / `gh api` /
journalctl positional matches go live (their `TestNotYetAllowed` cases moved up to
`TestMustAllow`).

---

## Cross-cutting note: `safeCommands` vs. the argv-data-safe property

A recurring point of confusion worth stating once:

- **`safeCommands`** is the single master whitelist. Every allowed command lives
  here exactly once, each with a spec that governs its **literal** tokens (which
  flags / subcommands / positionals are permitted). Many entries *can* mutate in
  general but are gated to read-only forms (e.g. `jj log` yes, `jj commit` no).
- **`ArgvDataSafe`** (and, if built, **`ArgvDataSafeAfterDashDash`**) are not
  lists of commands — they are **per-command properties** marking which
  `safeCommands` entries are additionally safe to hand an *opaque, attacker-
  controlled* operand (from `xargs` stdin or `$(...)`). They are strict subsets of
  the master list. Keep them as fields on the spec, not as parallel maps, so a
  command name is never written down twice.

## 9. Exec-path flag audit — **PARTIALLY DONE (2026-07-27), re-audit outstanding**

Not a feature; a class of defect and the work still owed against it. The rule and
the completed findings live in DESIGN.md "Flag values that are programs" — this
section records only what is *left to do*, so it does not drift out of the
roadmap.

### What happened

A single question — "this flag takes a value; what *is* the value?" — turned up
**five shipped exec paths** in whitelist data that had been reviewed before:
`jj --tool`, `sort --compress-program`, `nix eval --expr/--file/--apply`,
`jj --config-toml` (inert only by jj-version accident), and
`git --textconv/--filters` on `cat-file`/`grep`. All are removed and pinned.

The defect was never one careless review. It is that "is this flag read-only?"
is the wrong question and nobody was asking the right one. `jj --tool` mutates
nothing *itself*; it just names the program that does.

### Still outstanding

1. **Re-audit the specs that predate the rule.** The sweep enumerated all 374
   `TakesArg` flags and checked the obvious program-valued shapes, but only
   `jj`, `sort`, `nix`, `git`, `find`, `rg`, `xargs` and `awk` were walked
   properly. `docker`, `systemctl` and `gh` are the largest specs given only a
   format-flag spot-check. Expect a low but non-zero yield — `git` looked clean
   twice before `--textconv` turned up.
2. **Sibling-subcommand check.** The `--textconv` miss came from testing a flag
   on `git log`/`git blame` (where it is not whitelisted) and concluding "git is
   clean" — it was whitelisted on `cat-file` and `grep`. Any audit must walk the
   spec data per subcommand, not sample invocations. Worth a mechanical helper:
   dump every (subcommand, flag) pair rather than eyeballing.
3. **Consider a standing test.** Everything found is pinned individually in
   `TestMustNotAllow`, but nothing *prevents the next one*. An enumeration test
   that fails when a spec adds a `TakesArg` flag whose name matches a suspicious
   set (`tool`, `exec`, `pager`, `editor`, `program`, `expr`, `eval`, `filter`,
   `command`, `textconv`, `config*`) would turn this from a periodic audit into a
   tripwire. Cheap; the false-positive rate is the open question — `--filter` on
   `docker ps` is a data selector, not a program.

### Method that worked, for whoever picks this up

Enumerate flags from the spec data programmatically (a throwaway `go test` that
walks `safeCommands` and prints every `TakesArg` flag with its owning
command/subcommand path), then for each ask what the value is. When the answer
is unclear, point the flag at a script that touches a marker file and check
whether the marker appears — that is how `--tool`, `--compress-program` and
`--textconv` were each confirmed in under a minute. Do not reason from the man
page alone: `nix eval --expr` reads as "evaluate an expression" and the
consequence (arbitrary file read plus outbound network) only becomes obvious
when you run it.

---

## 10. Write redirects into the session scratchpad — **IMPLEMENTED (2026-10-23)**

### Why
The harness sends every event a per-session `scratchpad_dir` and tells the agent to
keep temporary output there; some workspace rules require write-to-a-file over a
truncating pipe. So `cmd > "$S/out.txt"` is what a well-behaved agent sends, and the
universal write-redirect rule (only `/dev/null`) made every one of them prompt. With
§6 it was the second half of the dominant idiom (fourth pass: 3.8% from §6 alone,
16.1% shipped together).

### What shipped
A write redirect (`>`, `>>`, `N>`, `&>`, `&>>`, `>|`; not `<>`) allows when its exact
target is a data file directly inside a validated scratchpad. Rationale: DESIGN.md
"Scratchpad writes"; the checks: `internal/engine/scratch.go` and `cmd/classify-bash/scratch.go`. In the corpus 98.9% of scratch writes
were top-level and 87% `.txt`, so the strict shape costs almost nothing. The feature
is off (old behaviour) when the event has no usable `scratchpad_dir`, and on
Windows/Plan 9, where ownership cannot be read.

### Open
- **Extensions.** The list is deliberately short and data-only. Source-file dumps
  (`.cs`, `.ts`) were seen in the corpus; adding one is a judgment that no allowed
  command auto-loads that suffix as configuration or code.
- **Subdirectories.** Refused, because a directory is where configuration
  discovery starts (`.git`, a bare repo, `.jj`). 1.1% of scratch writes used one.
- **`tee "$S/x"`.** Same destination, different mechanism; `tee` is not whitelisted.
- **Persistent shells.** A harness whose shell survives between calls could carry a
  binding into a later call. The classifier never reads bindings across calls (each
  call starts with an empty scope), so the only effect is an unexported shell
  variable, which the name rule already constrains.
