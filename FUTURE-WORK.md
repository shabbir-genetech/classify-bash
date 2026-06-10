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

## 5. `journalctl` and `gh` read subcommands — **PARTIALLY IMPLEMENTED (2026-06-10)**

Scope was locked by sign-off (2026-06-09): **D1** include `gh api` GET-only · **D2**
`gh` observed-only subcommands · **D3** exclude journalctl alternate-location flags.
Implementation then surfaced a safety finding that **invalidated D1 and the `gh repo
view` half of D2** under the current engine (see "Implementation finding" below);
those parts are deferred to **§8**, which unblocks them. What actually shipped:

| target | status | note |
|--------|--------|------|
| `journalctl` (read flags) | ✅ shipped | flag-only — `AllowAnyPositional: false` (see below) |
| `gh auth status` | ✅ shipped | no positional, no dangerous flag → safe today |
| `gh repo view` | ⏸ deferred → §8 | needs the `OWNER/REPO` positional + has `--web` |
| `gh api` (GET-only) | ⏸ deferred → §8 | needs the endpoint positional + has `-X`/`-f`/`-F` |

The shipped pieces still follow the **whitelist-only** discipline: dangerous modes
are left *off* the whitelist (they fall through by default) and recorded in
`// deliberately excluded` comments, never as a deny-list.

### Implementation finding — why D1 and `gh repo view` deferred
The spec claimed whitelist-only closes the write vector: "never list `-X`/`--web`
and they're rejected." **False under the current engine.** `matchGNU` treats every
token *after the first positional* as opaque data and does **not** re-validate
flag-shaped tokens once a positional opens the section (`handlePositionals` just
accepts the tail). Verified against the binary: `cat file -X` → allow, `git log
HEAD --output=/etc/passwd` → allow. That is harmless for `cat`/`git log` (no
dangerous flag exists), but `gh repo view` and `gh api` **require** a positional
(the repo / the endpoint), so a trailing `gh repo view o/r --web` (browser launch)
or `gh api repos/o/r -X POST` (network write) would be **swallowed and allowed**,
and real `gh` parses it. Whitelist data cannot fix this — it needs §8.

The same hazard is why **`journalctl` shipped with `AllowAnyPositional: false`**:
journalctl carries destructive flags (`--vacuum-size`, `--rotate`), so allowing a
positional field-match would let `journalctl x --vacuum-size=1G` slip a delete past
us. Accepting *no* positional means no token ever opens the gate, so every
unwhitelisted flag is always rejected. Cost: positional field-matches
(`journalctl _SYSTEMD_UNIT=foo`) fall through — none appear in observed use; they
wait on §8 too. This covers 100% of the corpus's (flag-only) journalctl demand.

### Shipped: `journalctl` — `styleGNU`, `AllowAnyPositional: false`, `ArgvDataSafe: false`
- **Whitelisted (read/query/filter/format):** `-t/--identifier`, `-u/--unit`,
  `--user-unit`, `-n/--lines`, `-S/--since`, `-U/--until`, `-p/--priority`,
  `-g/--grep`, `--case-sensitive`, `-b/--boot`, `-k/--dmesg`, `-o/--output`,
  `--output-fields`, `-r/--reverse`, `-e/--pager-end`, `-x/--catalog`, `-a/--all`,
  `-q/--quiet`, `-m/--merge`, `--utc`, `--no-pager`, `--no-tail`, `--no-full`,
  `--no-hostname`, `-N/--fields`, `-F/--field`, `--list-boots`, `--header`,
  `--disk-usage`, `--version`, `-h/--help`.
- **Deliberately excluded (fall through):** `--vacuum-size`, `--vacuum-time`,
  `--vacuum-files` (delete journals), `--rotate`, `--flush`, `--sync`,
  `--relinquish-var`, `--smart-relinquish-var`, `--setup-keys`, `--update-catalog`,
  `--verify`; **`-f/--follow`** (never terminates — `TestNotYetAllowed`, not the
  wall); and per **D3** the alternate-location readers `-D/--directory`, `--file`,
  `--root`, `-M/--machine`, `--namespace`.

### Shipped: `gh` — modeled on `gitSpec()`, `ArgvDataSafe: false` everywhere
v1 ships only `gh auth status` (the rest of the tree waits on §8):

```
gh
└── auth → status      (login/logout/refresh/token/setup-git excluded)
```

`gh auth status` — flags `-h/--hostname` (arg), `-a/--active`, `--help`.
*Excluded:* `-t/--show-token` (prints the auth token — secret disclosure). No
positional, no dangerous flag, so an unknown flag or any positional falls through.

### Code & tests (as built)
- `commands.go`: registered `"journalctl"` (Tier C) + `"gh"` (Tier B); builders
  `journalctlSpec`, `ghSpec`, `ghAuthSpec`, `ghAuthStatusSpec`. No engine changes.
- **`TestMustAllow`:** `journalctl -t classify-bash --no-pager`,
  `journalctl -t classify-bash -o cat`, `journalctl -u sshd -n 50 --no-pager`,
  `journalctl --since 2026-06-09 -r`, `journalctl`, `gh auth status`,
  `gh auth status --active`, `gh auth status --hostname github.com`.
- **`TestMustNotAllow` (the wall):** `journalctl --vacuum-size=1G`/`--vacuum-time`/
  `--rotate`/`--flush`/`--sync`/`--update-catalog`/`--setup-keys`,
  `journalctl _SYSTEMD_UNIT=x --rotate`, `journalctl /usr/bin/foo --vacuum-size=1G`
  (the post-positional guards), `gh auth login`/`logout`/`refresh`/`token`,
  `gh auth status --show-token`, `gh repo edit … --visibility public`,
  `gh repo delete`/`create`, `gh pr create`/`merge`, `gh api … -X POST`,
  `gh api … -f name=x`, `gh repo view o/r --web`.
- **`TestNotYetAllowed` (harmless, await §8):** `journalctl -f`,
  `journalctl _SYSTEMD_UNIT=sshd`, `journalctl /usr/bin/foo`,
  `gh repo view o/r [--json visibility]`, `gh api repos/o/r --jq .visibility`,
  `gh pr view 123`, `gh issue list`.

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

## 8. Validate flags *after* positionals in `matchGNU` — **RESEARCH (unblocks §5 D1/D2)**

### The limitation (found implementing §5, 2026-06-10)
`matchGNU` stops parsing flags at the first positional: it calls
`handlePositionals(args[i:])`, which accepts the entire tail as data without
re-checking whether later tokens are flag-shaped. So `cat file -X` and `git log
HEAD --output=/etc/passwd` both **allow**. This is sound *only* because every
`AllowAnyPositional` command shipped so far has **no dangerous flag** — there is
nothing harmful for a swallowed trailing token to trigger.

That assumption breaks for any reader whose dangerous behavior lives in a *flag*
while its primary argument is a *positional*. `gh repo view <repo> --web` (browser
launch), `gh api <endpoint> -X POST` (network write), and `journalctl <match>
--vacuum-size=1G` (delete) all have a required positional, so the dangerous flag
lands *after* it and is swallowed. §5 worked around this by (a) shipping only the
no-positional forms (`gh auth status`) and (b) giving `journalctl`
`AllowAnyPositional: false` — but that also blocks the legitimate positional forms
(`gh repo view <repo>`, `gh api <endpoint>`, `journalctl _SYSTEMD_UNIT=foo`).

### Idea
Make `matchGNU` follow GNU getopt **permutation** semantics: keep scanning and
*validating* flags against `spec.Flags` even after positionals have appeared,
instead of dumping the tail into `handlePositionals`. A token starting with `-`
(and not `--`/`---…`) after a positional is still a flag and must match the
whitelist or fall through; a non-flag token is a positional. Net effect:
- `git log HEAD -p` → still allows (`-p` is whitelisted) — no regression for the
  common "flag after a revision" idiom.
- `gh api repos/o/r -X POST` → **falls through** (`-X` not whitelisted), closing
  the write vector that §5 D1 could not.
- `gh repo view o/r --web` → **falls through** (`--web` not whitelisted).

Once this lands, the §5 deferrals become safe: add `gh repo view` and `gh api`
(GET-only) exactly as the original §5 spec described (the flag lists are already
written there), flip `journalctl` back to `AllowAnyPositional: true`, and move the
corresponding `TestNotYetAllowed` cases up to `TestMustAllow`.

### Why it is real engine work (not a data change)
- **Blast radius.** `matchGNU` is the most-exercised matcher; every `styleGNU`
  command's corpus case must stay green. The change must preserve today's
  "positional then whitelisted flag" allows while *adding* rejection of
  unwhitelisted post-positional flags. The existing comment on `commandSpec`
  ("flags after the first positional are NOT accepted") is currently *aspirational*
  — the code accepts them as data; §8 makes the comment true.
- **Interactions to re-derive, not assume:** the `--` end-of-flags rule
  (everything after stays positional — must remain); substituted (`subst`) tokens
  after positionals (still positional-only, still `ArgvDataSafe`-gated); the
  `AllowAnyPositional == false` specs (a stray post-positional flag should keep
  falling through, as now); and `ArgvDataSafe` readers (`cat file -X` — should the
  permuted scan still treat `-X` as harmless data? `cat` is safe under any argv, so
  yes — likely an `ArgvDataSafe` short-circuit in the permuted path).

### Open questions before a spec
- Does any *currently-allowed* corpus case rely on a non-whitelisted flag being
  swallowed after a positional? (Audit `TestMustAllow`; if yes, those flags must be
  added to their specs or the case re-examined.)
- Permutation vs. POSIX strict-ordering: GNU permutes by default but
  `POSIXLY_CORRECT`/a leading `-` in optstring stops at the first positional. We
  parse statically with no env; pick permutation (matches GNU default, the common
  case) and document it.
- Whether `ArgvDataSafe` commands keep the "tail is data" fast path or also switch
  to validated scanning (they have no dangerous flag, so either is safe; the fast
  path is simpler and preserves `cat file --anything`).

### Acceptance
`gh repo view o/r --web`, `gh api repos/o/r -X POST`, and the existing
`journalctl … --vacuum-*` post-positional guards stay in `TestMustNotAllow`;
`git log HEAD -p` and the §5-deferred reads move to / stay in `TestMustAllow`; no
regression anywhere in the corpus.

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
