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

Status legend: **APPROVED** (signed off, ready to implement) · **DEFERRED**
(agreed valuable, parked) · **RESEARCH** (needs investigation before a spec).

---

## 1. Command substitution `$(...)` — **APPROVED (v1 pending)**

### Goal
Let a whitelisted, read-only command receive an operand produced by `$(...)`
when the inner command is itself read-only-safe — without weakening allow-only /
fail-closed. This is the dominant real blocker in the audit log
(`echo "x $(readlink -f … )"`, `cat "$(… )"`, …).

### Invariant being changed
Today *only fully-literal argv reaches a spec* (`wordLiteral`, `classify.go`).
`$(...)` breaks that two ways:

1. **the inner command executes** — acceptable iff we recursively classify it
   read-only (identical to a pipe stage); and
2. **its output becomes an operand** the outer command sees as data we cannot
   read at classify time — this is **the same stdin→argv hazard** that `xargs`
   already faces and solves with a curated subset (see DESIGN.md "styleXargs and
   the stdin-argv hazard").

### Rules (v1 = Tier 1)
**Word acceptance.** A word is acceptable if it is either:

- **(A) literal** — today's `wordLiteral` (known string), or
- **(B) quoted-substitution** — parts are `Lit` / `SglQuoted` / `DblQuoted`,
  where a `DblQuoted` may also contain `CmdSubst` parts, and **every** such
  `CmdSubst`'s inner statements recursively classify safe via `classifyStmt`.

Still rejected anywhere in a word: `ParamExp` (`$VAR`), `ArithmExp`, `ProcSubst`
(`<(...)`), `ExtGlob`, and a `CmdSubst` that is **not inside double quotes**
(bare `$(...)` undergoes word-splitting + globbing → an unknown number of
injected argv words; quoting pins it to exactly one operand). Decision: **quoted
only.**

**Spec matching.**

- The command name `words[0]` must be type (A) literal — we must always know
  which spec applies. `$(echo ls) …` in command position stays rejected.
- A type-(B) operand is an **opaque positional placeholder**, accepted only by
  commands carrying the `ArgvDataSafe` property (below). It is **never** matched
  as a flag, and **never** as a flag's argument.
- Flag tokens and any `TakesArg` flag arguments must remain type (A) literal.
  (So `head -n "$(…)" f` and `ls --color="$(…)"` both fall through — the
  substitution sits in a flag-argument slot.)

### The `ArgvDataSafe` property and the planned refactor
The curated set of commands that may receive an attacker-controlled argv token
**already exists** as `xargsWrappable` (`commands.go`): *"a command belongs here
ONLY if it has NO write/mutate path under ANY argv … those tokens are parsed by
the wrapped program, including as flags."* That is **bit-for-bit** the property a
substituted-operand receiver needs. Command substitution is in fact the *smaller*
surface — `xargs` appends many unseen tokens; a quoted `"$(...)"` injects exactly
one — so `xargsWrappable ⊆ {safe substituted-operand receivers}`. Reuse it; do
not invent a parallel list.

**Refactor (fold into v1):** the standalone `xargsWrappable` map duplicates ~20
command *names* that are already keys in `safeCommands`, and needs a fail-loud
subset-sync guard (`classifyWrapped`, `spec.go`). Replace the map with an
**`ArgvDataSafe bool` field on `commandSpec`**. Then:

- one master list (`safeCommands`); the property lives on the spec, names appear
  once;
- the subset-sync guard becomes structurally impossible to violate (the bit *is*
  the spec) and can be deleted;
- both `matchXargs`/`classifyWrapped` and the new substitution check consult
  `spec.ArgvDataSafe`.

**Add these to `ArgvDataSafe` (all already in `safeCommands`, all pure readers
with no write flag):** `echo`, `ls`, `readlink`, `basename`, `dirname`. Side
benefit: `xargs echo` / `xargs ls` start accelerating too (one property, two
consumers).

### Hazards and why they're contained
- **Inner exec** → inner command is fully classified read-only; same as a pipe
  stage.
- **Output-as-operand / flag-injection** → the injected value could *look* like a
  flag (`-rf`, `--out=x`). For an `ArgvDataSafe` command, by definition no flag it
  honors can cause write/exec/network, so the worst case is "reads/prints
  something else" — the same information-disclosure surface `cat FILE` already
  has. Acceptable under the threat model (we guard side effects, not arbitrary
  read-only output).
- **Word-splitting** → neutralized by the quoted-only rule (one operand).

### Implementation sketch (contained, ~3 files)
- **classify.go** — add a sibling to `wordLiteral` validating (B) and recursing
  into `CmdSubst.Stmts`; `classifyCall` stops being all-or-nothing and classifies
  each arg as `{value, substituted}`.
- **spec.go** — the `match*` matchers learn the per-token `substituted` bit: a
  substituted token is acceptable only as a positional and only when
  `ArgvDataSafe`; flags and flag-args must be literal. (One signature ripple:
  `spec.match([]string)` → a small arg-token struct.)
- **commands.go** — delete `xargsWrappable`, add `ArgvDataSafe: true` to the
  member specs (+ the five readers above), delete the subset-sync fail-loud.

### Test corpus
- **mustAllow:** `echo "x $(ls)"`, `echo "$(cat /etc/hostname)"`,
  `cat "$(readlink -f /a/link)"`, nested `echo "$(head -1 "$(ls)")"`.
- **mustNotAllow:** bare `echo $(ls)` (splitting); `$(echo ls) -l` (name
  position); `echo "$(rm x)"` (inner unsafe); `find . -name "$(ls)"` (flag-arg
  slot + find not `ArgvDataSafe`); `head -n "$(ls)" f` (flag arg);
  `ls --color="$(ls)"` (flag-arg slot); `sort "$(ls)"` (sort not Tier 1 — see
  Tier 2).

---

## 2. Command substitution Tier 2: `--` leniency — **DEFERRED (v2)**

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
