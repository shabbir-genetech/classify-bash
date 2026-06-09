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

## 5. `journalctl` and `gh` read subcommands — **CANDIDATE (log-driven, 2026-06-09)**

Pure whitelist coverage, no new classifier feature — the highest-hit-rate, lowest-
risk items the first corpus surfaced. These are *extend-the-whitelist* tasks
(README "Extending the whitelist"), not research; they still want design-first
sign-off and the usual `mustAllow` + wall (`mustNotAllow`) cases.

- **`journalctl`** — a pure reader; no write/exec/network path. The observed uses
  are `journalctl -t <tag> --no-pager [-o cat]`. Straightforward `styleGNU` spec.
  Watch the corners that are *not* read-only and must stay off the flag list:
  `--flush`, `--rotate`, `--vacuum-*`, `--setup-keys`, `--update-catalog`, and the
  `--output=` variants that can shell out. Default-deny flags; enumerate only the
  read/query ones (`-t/--identifier`, `-u/--unit`, `-n/--lines`, `--since`,
  `--until`, `--no-pager`, `-o/--output` restricted to safe formatters, `-g/--grep`,
  `-r/--reverse`, `-k/--dmesg`-style read filters).
- **`gh`** — *not* a pure reader: it has heavy mutate/network-write subcommands
  (`repo edit`, `pr create|merge|close`, `release create`, `api` with `-X POST…`).
  Whitelist it the way `jj` is whitelisted: **per-subcommand**, allowing only the
  read forms seen in the corpus — `gh auth status`, `gh repo view`, and the
  read-only slice of `gh api` (GET only; reject `-X/--method` ≠ GET and `-f/--field`
  writes). `gh api` is the sharp edge — a substituted/elevated method turns a
  "read" into a write, so treat any non-GET as fall-through and keep `api` off
  `ArgvDataSafe`.

Acceptance: the relevant `journalctl … ` / `gh repo view …` lines move into
`TestMustAllow`; add `TestMustNotAllow` for `journalctl --vacuum-size=1G`,
`gh repo edit … --visibility public`, `gh api … -X POST`, etc.

---

## 6. `$VAR` expansion for locally-assigned literals — **RESEARCH (refines §3)**

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
