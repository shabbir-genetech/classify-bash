# Design notes

Deeper rationale behind `classify-bash`. The [README](README.md) covers the
contract and how to run it; this file records *why* the classifier is shaped the
way it is, for anyone extending it. Agreed-but-unbuilt ideas (command
substitution, `--` leniency, `while`, a `sed` parser) live in
[FUTURE-WORK.md](FUTURE-WORK.md) with their caveats and open research.

## The engine knows nothing about any host

`internal/engine` classifies a command and returns a `Class` — `ReadOnly`,
`NotReadOnly`, or `Unparseable` — and **never a permission decision**. What "not
read-only" should cost a user is the host's business, and the supported hosts
genuinely disagree: Claude Code can *grant* permission, pi can only *block*, and
oh-my-pi adjusts an approval tier. A verdict type shaped like any one of them
cannot express the other two.

Two consequences the layout enforces rather than documents:

- **The engine cannot call `os.Exit`.** It aborts a walk by panicking with a
  private type that `Classify` recovers. Five call sites deep in a recursive walk
  therefore need no signature changes, and no host semantics leak inward.
- **`NotReadOnly` is the zero value.** A forgotten branch or a partially built
  `Result` degrades to the safe answer, never to an allow.

Host wire formats live in `cmd/classify-bash` (the `hook` adapter) and in the
shims under `contrib/`, all built on the same `check` primitive.

## Why allow-only, never deny

The tool only ever *adds* an allow. Anything it cannot positively classify falls
through to the host's normal permission flow. It is an accelerator, not a gate: a
bug in it can only *fail to speed something up*, never wave through something the
normal flow would have stopped. This asymmetry is the whole safety argument —
keep it.

Concretely, under Claude Code's `PreToolUse` protocol **exit 2 blocks the tool**,
so exit 2 is the one thing that can violate the asymmetry. It is reserved for the
two cases nothing upstream can trigger: operator error at the registration site (a
bad `--log-*` flag), and a strictness policy the operator explicitly asked to be
loud about. Everything else — including every decode problem — fails open.

## Strict positive whitelist

Every command, subcommand, and flag is enumerated positively in
`internal/engine/commands.go`.
An unknown command, unknown subcommand, or unknown flag on a known command →
fall through. We never write "allow X except when Y": a future release of some
tool may add a state-mutating `Z` we did not anticipate, and an
allow-except-list would wave it through. A positive list fails closed instead.

### Flag values that are programs

A positive whitelist enumerates flag *names*. It says nothing about what a
flag's *value* means, and that is where this classifier has actually been
wrong. A command can be a pure reader and still hand an argument straight to
`execve`:

> **A flag whose value is a program — or a program text the tool will evaluate
> — is an exec path however read-only the subcommand looks.**

Three shapes, all found *shipped* in this whitelist during the 2026-07-27 sweep,
none hypothesised:

- **The value is a program.** `jj diff --tool /tmp/evil` classified allow and
  executed the script; `sort --compress-program NAME` runs NAME for spill files.
  Both were whitelisted on otherwise-correct read-only specs.
- **The value is evaluated.** `nix eval --expr` is not a sandbox:
  `--impure --expr 'builtins.readFile /etc/hostname'` returns the file, and
  `builtins.fetchurl` performs real outbound requests — read a secret,
  exfiltrate by URL. `--file` is the same hole via a path.
- **The value sets one of the above.** `jj --config 'ui.pager=…'` is an exec
  path because `log`/`diff`/`show` spawn the pager — the injected program runs
  on precisely the subcommands the whitelist accelerates.

Two consequences worth stating, because both bit us:

*Inertness is not safety.* `jj --config-toml` was whitelisted and harmless only
because jj 0.41 had removed that flag name. A version accident is not a
property; it was excluded on the class instead.

*Removing one hole can be what makes a neighbour safe — say so.* `nix`'s
`--arg`/`--argstr` values are Nix expressions, and they survive the sweep only
because `--file` was removed in the same change: flakes refuse them, and the
non-flake form they need is `-f/--file`. That dependency is recorded at
`nixGenericSpec()` so restoring `--file` forces a re-review rather than
silently reopening the hole.

The counter-example that shows the rule has an edge: `sort --random-source=FILE`
*looks* identical in shape but is kept, because coreutils only reads bytes from
that file and never executes it. Shape is the trigger for the question, not the
answer — check what the tool does with the value.

The audit method that found these is mechanical and worth repeating when adding
commands: enumerate every flag with `TakesArg`, and for each ask what the value
is. See README "Extending the whitelist" step 2 for the checklist form.

**`awk` was audited separately and is clean** — worth recording, because it is
the one place a full language is whitelisted and the natural next suspect.
`system()`, `print | "cmd"`, `"cmd" | getline`, `getline < file`, `print > file`,
`close`/`fflush` and user-defined functions all fall through, and each already
had a `TestMustNotAllow` case. `classifyAwkProgram`'s positive node walk was
built for exactly this; no change was needed. The one thing that *does* pass is
`ENVIRON` — deliberate, since bare `env` and `printenv` are whitelisted at Tier
A and dump the environment more directly.

The format-string and property-selector shapes were audited on 2026-07-27 and
are clean, with one exception found along the way:

- **`docker --format`** is Go `text/template`. It can only call functions the
  host registers and methods on the value being rendered; docker registers
  string helpers (`json`, `lower`, `split`) and no filesystem or exec access
  (`{{env "HOME"}}`, `{{exec "id"}}`, `{{os.ReadFile …}}` all fail to parse).
- **`systemctl --property`** is a pure selector — an unknown name yields empty
  output, nothing is evaluated.
- **`git --pretty`/`--format`** placeholders are formatting directives only; no
  placeholder invokes a program.
- **`git --textconv` / `--filters` were an exec path and have been removed.**
  They run a program named by gitattributes/git-config
  (`diff.<d>.textconv`, `filter.<f>.clean`). Verified: both
  `git cat-file --textconv` and `git grep --textconv` executed the configured
  filter. They were whitelisted on `git cat-file` and `git grep` — note the
  earlier pass called git "clean" after testing `--textconv` only on `git log`
  and `git blame`, where it is *not* whitelisted. **Checking a flag on one
  subcommand says nothing about its siblings; check the spec data, not one
  invocation.** `--no-textconv` is kept: it only disables the path.

This one differs from the others in threat model — it needs attacker-controlled
repo config to do anything — but an allowed command executing a program at all
breaks the contract, so it goes.

### Tiers

- **A — no-subcommand reads** (`cat`, `ls`, `grep`, `cd`) plus stdout-only
  filters (`tr`, `cut`, `sort`, `uniq`, `paste`): `styleGNU`, `AllowAnyPositional`,
  no write flags.
- **B — command + subcommand** (`git status`/`log`, `jj log`/`bookmark list`,
  `nix eval`, `docker ps`, `systemctl status`): `styleGNU` + `Subcommands`
  dispatch. A nested namespace exposes only its read-only leaves — `jj git`
  whitelists `remote list` but not `push`/`fetch`; `jj bookmark` whitelists `list`
  but not `set`/`move`/`delete`.
- **C — flag-aware dual-use** (`find`): `styleFind`.
- **D — transparent wrappers** (`devenv shell --`, `nix shell PKGS --`):
  `styleWrapper`. The literal `--` is REQUIRED; the tail after it is looked up in
  the whitelist and classified recursively.
- **E — scripting with a whitelisted body** (`awk`): `styleAwk` — see below.
- **F — stdin-append wrapper** (`xargs`): `styleXargs`. Distinct from D — there
  is no `--`, the first non-flag token is the wrapped command, and recursion is
  gated by the wrapped command's `ArgvDataSafe` flag (commands with no write path
  under *any* argv), not the whole whitelist. See below for why.

## Defensive JSON contract

The event must declare `hook_event_name == "PreToolUse"` and `tool_name == "Bash"`
and carry a non-empty command. Anything else — malformed JSON, trailing data, the
wrong event or tool — **falls through**: exit 0, empty stdout, one `undecodable`
log record. The host then applies its normal permission flow, exactly as it would
if this hook were not installed.

**An unknown field is reported, and by default withholds the allow.** A field the
harness starts sending that this binary does not know decodes normally and is
recorded as a `schema_drift` record naming it. What it then costs is the hook's
`--on-unknown-field` policy (`cmd/classify-bash/policy.go`): under **`withhold`** —
the legacy bare registration and `claude-lenient` — the auto-approval is suppressed
for that call, so the hook exits 0 with empty stdout and the host prompts exactly as
it would for any unclassifiable command. `log` classifies as if the field were
absent, `ignore` does the same silently, and `fail` blocks (exit 2; kept only
because the axis should exist).

Two separate properties, deliberately: we will not hand out an `allow` while
blind to part of the payload (an unknown field could in principle be one that
changes what the command *does*), and a harness addition must never be able to
block the Bash tool — which is precisely what the old strict decoder did, five
times. Withholding an acceleration is a cost measured in permission prompts;
blocking is measured in dead sessions. (The fork this layout came from used `log`
for its lenient mode, which auto-approves while blind; the 2026-10-23 merge made
`withhold` the default on both paths.)

Note the asymmetry this preserves: the hook can only ever *add* an allow, so
degrading to "no opinion" is always safe. To silence the
record — and restore acceleration for payloads carrying the field — add it as an
ignored `json.RawMessage` in the right struct (a
top-level field goes on `event`, a `tool_input` field goes on `toolInput` in
`internal/adapter/claude/event.go`) and add an accept-test covering an event that
carries it. No `go.mod` change, so the vendor hash is unaffected. The known-field
sets are derived from the struct tags by reflection, so the struct is the single
source of truth.

**Why this reversed.** The decoder originally set `DisallowUnknownFields` as a
deliberate tripwire: a silently-ignored field could be one that changes what the
command *does*, so we would rather block than ship a stale classifier. In practice
the tripwire fired on harmless additions and the cost was severe, because
`PreToolUse` exit 2 does not merely warn — it *blocks the tool*:

- `effort` — inside `tool_input` and at the top level.
- `agent_id` / `agent_type` — sub-agent Bash calls.
- `prompt_id` — top-level, correlates the call to the originating prompt.
- `dangerouslyDisableSandbox` — inside `tool_input` on sandbox-disabled calls.
- `scratchpad_dir` — top-level, sent on *every* event. This one did not trip once
  and pass; it disabled the Bash tool for entire sessions, repeatedly, and the fix
  itself requires Bash on a deployed consumer.

Five additions in, the pattern is clear: the harness will keep adding fields, and
enumerating each one after it causes an outage is not a strategy. The signal was
worth keeping; putting it on the exit code was not. It now lives in the log, where
it costs nothing to be wrong.

This also restores the contract the rest of the design rests on — a bug here can
at worst fail to accelerate, never block. The one remaining blocking case, an
unrecognized `mvdan/sh` AST node kind, is now a policy too: the engine aborts the
walk (`Unparseable`) instead of exiting, and `--on-unknown-ast` decides — `fail`
(exit 2) on the legacy path and `claude-strict`, `fallthrough` under
`claude-lenient` and in `check`.

## Logging non-allowed commands

Logging is **opt-in** and writes one JSON line per record
(`internal/logging/log.go`). There are **five** `kind`s:

| `kind` | Meaning | Decision |
|---|---|---|
| `fallthrough` | No whitelist match | no `allow`; host prompts |
| `failloud` | Bad `--log-*` flag, or a policy set to `fail` that fired | exit 2, **blocks** |
| `schema_drift` | Unknown harness field(s); `reason` names them | under `withhold` (the default): allow withheld → host prompts (exit 0); under `log`: informational |
| `undecodable` | Unusable event (malformed JSON, wrong tool, empty command) | fail open, exit 0 |
| `deprecated_invocation` | The bare, pre-subcommand registration was used | informational; written on **every** call, allowed ones included |

It exists so you can see what is failing to accelerate and notice classifier
staleness. A `schema_drift` record under `withhold` is usually followed by a
`fallthrough` record for the same call — the cause, then its effect. Apart from
`deprecated_invocation`, an allowed command is never logged, so a high
`deprecated_invocation` count means the registration should move to
`classify-bash hook --mode=claude-lenient`. Several design choices are
non-obvious enough to record here.

**Off by default.** The documented contract is silence on fall-through, and a log
contains the literal commands Claude tried to run. So the binary defaults to no
logging; the registration site (a Nix-generated `settings.json`) opts in with
`--log`. Default-off keeps the public contract honest and the privacy story simple.

**Synchronous, not async.** The tempting design is to hand the decision back and
log in a goroutine afterwards. It does not work: a goroutine cannot outlive
`os.Exit`, and the hook *must* exit for Claude Code to proceed — on fall-through,
exit 0 *is* the signal, so there is no "after we returned" window inside the
process. The only way to truly defer is a detached child process, which costs
milliseconds (fork+exec; a bare fork is unsafe in Go's multithreaded runtime) to
avoid a microsecond append. So we write synchronously before exit. This is fine
because the non-allow path is already headed for a permission prompt — orders of
magnitude slower than the write.

**Journal via stdlib `log/syslog`, not native journald.** The journal sink would
ideally use the native protocol for indexed fields (`journalctl
CLASSIFY_BASH_COMMAND=…`), but that needs `coreos/go-systemd`, which changes the
`buildGoModule` `vendorHash` and churns `go.sum`. `log/syslog` is in the standard
library, needs no dependency, and still lands in journald via `/dev/log` on systemd
systems — the cost is grep-only (`MESSAGE`), no indexed fields. Worth a dependency
later only if field queries become a real workflow.

One non-obvious cost did surface: `log/syslog` imports `net`, which enables **cgo**
and makes the hermetic flake-check build want a C compiler it has no reason to
carry (`cgo: C compiler "gcc" not found`). The fix is `CGO_ENABLED=0`, set in
`flake.nix` on both the package and the test check — pure-Go `net` builds fine, and
unix-socket dialing needs no DNS resolver, so syslog still works. This also matches
the static-binary deploy. See "Build gotcha". With a live `/dev/log` present,
`--log-to=auto` therefore sends records to journald, not the fallback file — query
them with `journalctl -t classify-bash`.

`log/syslog` is also **Unix-only** (`!windows && !plan9`); importing it
unconditionally would make the whole package fail to compile on Windows. So the
journal sink is split by build tag: `writeJournal` lives in `journal_unix.go`
(`//go:build !windows && !plan9`, uses `log/syslog`), with a stub in
`journal_other.go` that returns an error on Windows/Plan9. The stub keeps the
binary buildable everywhere; there, `auto` falls back to the file and `journal`
drops. Everything else (`internal/logging/log.go`) stays portable, so the file sink works on every
platform. Keep this split if you add another OS-restricted sink.

**Strictness split by failure class.** This is the one place logging touches the
safety argument. Two different failures, two different postures:

- *Log writes* (socket down, disk full, unwritable dir) are transient and
  environmental — every error is **swallowed**. A write must never change the
  decision and never `failLoud`, or the side-channel logger becomes a path that can
  block a tool call, breaking the allow-only asymmetry. The earlier-stated
  invariant "logging never blocks" lives here, scoped to *writes*.
- *Log config* (the flags) is static, deterministic operator input — a bad flag
  **`failLoud`s (exit 2)**, the same posture as the strict JSON decoder above. A
  typo in `--log-to` blocks all Bash acceleration until fixed; that is the intended
  trade (you hear about a misconfigured logger on the first call, rather than
  silently not logging). Because the flags resolve *before* the sink config exists,
  a flag-parse `failLoud` itself logs nothing — it only prints to stderr and exits.

`failLoud` is reachable from deep inside the classifier and calls `os.Exit` on the
spot, so `main` never regains control; to let it record a `failloud` line it reads
two package-level globals (`logCfg`, `currentCommand`) that `main` populates as
soon as they are known. Both stay zero until then, so any early `failLoud` simply
logs nothing.

**Reading the log: not every fall-through is a candidate.** The log answers "what
failed to accelerate," not "what *should* have." A fall-through is only a miss if
the command is genuinely read-only; a side-effecting command falling through to a
one-keystroke prompt is the *correct* outcome, and whitelisting it would trade that
confirmation for a real regression. The healthy signal to look for first is the
*absence* of `failloud` records — those mean classifier staleness (an unknown AST
node or a harness field the decoder rejects), and they block calls. Fall-throughs
do not. When triaging the fall-throughs, three buckets recur (a 2026-06 pass over
the journal hit all three):

- *Whitelist it* — read-only and recurring. That pass added `jj bookmark list` and
  `jj git remote list` (read-only leaves of otherwise-mutating namespaces).
- *Leave it* — mutates state, touches the network, or runs arbitrary code, however
  benign it looks in the log. The pass deliberately rejected `jj git fetch` (network
  fetch; moves remote-tracking bookmarks, a `.jj/` write — a force-pushed remote
  becomes a bookmark *conflict*, not silent loss, but still a write), `nix flake
  lock` (rewrites `flake.lock`, fetches to resolve revs), and `nix develop --command
  CMD` (an execution wrapper — a "Deferred wrapper shape" below — that runs arbitrary
  commands and would breach allow-only outright).
- *Already covered* — the blocker was elsewhere in the same pipe/list, not the
  command you noticed. The pass nearly re-added `grep`, which has had a full spec all
  along; the real blockers on those lines were `jj bookmark` and a non-whitelisted
  `sed`. Confirm against `commands.go` before adding.

Read the records in one shot with `journalctl -t classify-bash -o cat` (each line is
the raw JSON record — no journald framing) and summarize them in a single pass rather
than scrolling the lot; a triage healthy-check is mostly counting `kind` (any
`failloud`?) and bucketing the fall-throughs by leading command. One caveat the
records make obvious: **triaging the log pollutes the log.** The analysis commands are
themselves Bash tool calls — `journalctl … -o cat`, a `python3 - <<'EOF'` heredoc, an
`ls`/`echo` probe — and they fall through (an interpreter is never whitelisted, and
most multi-statement analysis lists contain one), so each pass appends a tail of near-duplicate self-referential
records. Expect them, and read the *substantive* fall-throughs as the ones that predate
your current session.

The hard-won method note from the first full corpus pass (2026-06-09): **classify
each candidate by piping it through `./result/bin/classify-bash`, never by
regex-matching the log string.** That pass twice blamed the wrong construct (the
`cd` prefix, then statement sequencing) from string inference alone; replay refuted
both — `cd <literal>`, `command -v`, `git` read subcommands, extended `grep` flags,
`;`/newline sequencing, and literal `"$(...)"` already allow. It is the same
"already covered" trap above, one level up: the blocker is almost never the command
your eye lands on. The findings and the resulting re-prioritization (un-whitelisted
`gh`/`journalctl` readers; literal-`$VAR` and literal-`for` as the real structural
levers; Tier-2 `--` and a `sed` parser at ≈0 demand *in that corpus*) live in
[FUTURE-WORK.md](FUTURE-WORK.md) "Observed demand". The `sed` half did not last: the
2026-10-23 pass found `sed` the primary blocker on 369 of 13,118 records (see that
section's fourth pass). A demand finding is a property of one corpus, not of the tool.

**That note was not enough: use `TestTriage`.** The 2026-07-27 pass re-derived the
bogus `cd`-prefix finding *despite* the paragraph above naming `cd` explicitly — a
prose warning does not survive contact with a 2000-record corpus and an eager regex.
The method is now code, in `triage_test.go`:

```bash
journalctl -t classify-bash -o cat > /tmp/corpus.jsonl
CORPUS=/tmp/corpus.jsonl SINCE=2026-06-12 \
  nix develop --command go test -run TestTriage -count=1 -v ./internal/engine/
```

It reports the `failloud` health check, replays every record against the current
whitelist to separate already-fixed noise from live gaps, and attributes **one**
blocker per record by descending the same structure `classifyStmt` walks —
`MISSING-CMD x` (absent from the whitelist), `SPEC-REJECT x @ tok [sub: y]` (present
but the spec rejected `tok`), `EXPANSION-ARG x`, or a structural tag. `SHOW=<tag>`
prints verbatim examples. Set `SINCE` to the date `commands.go` last changed
(`jj log -r 'files(commands.go)'`); records older than that were judged by a
different whitelist. Two traps it encodes, both of which produced wrong numbers
before it existed: the hook **exits 0 for allow *and* fall-through** (only printed
JSON distinguishes them, so exit-code probes report everything as allowed), and a
missing *global* flag makes a read-only and a mutating use tip at the same token —
`jj -R p log` vs `jj -R p commit` — so the `[sub: …]` split is what tells a real gap
from the intended outcome.

Two corpus-scale moves from the second pass (2026-06-10), both replay-based:
- **"What would shipping/redeploying buy?"** Replay *every* logged fall-through
  through the new binary and count the ones that flip to allow. The log only holds
  fall-throughs (the running hook never logs an allow), so any flip is a form the
  new build would newly accelerate — a direct, per-record measure of a change's
  real value, not a guess.
- **Detect deploy lag.** Run the same forms through the deployed `PATH` binary and
  the freshly built `./result` binary side by side; divergence means the consumer
  hasn't re-locked/rebuilt yet (see "How it's deployed" — a change only reaches the
  hook after commit + push + downstream re-lock). One caveat the second pass made
  vivid: a feature can be live yet show ~no benefit because the dominant usage
  pattern is blocked by an *unrelated* rule — e.g. `journalctl … > file` falls
  through on the write-redirect rule no matter what the whitelist says (since
  2026-10-23, unless `file` is a data file in the session scratchpad — "Scratchpad
  writes" below). The same pass measured the inverse: literal `$VAR` alone flipped
  3.8% of records and only 16.1% together with scratchpad writes, because the two
  blockers stack in one idiom. Replay the *combination* before judging a lever.

## AST handling (`classifyCmd`)

The shell command is parsed with `mvdan.cc/sh/v3/syntax`. Compound forms are
handled conservatively:

- `BinaryCmd` (`&&`, `||`, pipe) and `Subshell` (`( … )`) **recurse** — every
  inner statement must itself classify as safe. They also decide which variable
  bindings each side sees ("Literal-bound variables" below); an unknown binary
  operator fails loud.
- An **assignment-only** statement is accepted as a binding, never on its own: a
  command made only of assignments falls through.
- `Block` and every other compound kind (`If`/`For`/`While`/`Case`/`FuncDecl`/
  `Let`/`Time`/`Coproc`/`Test`/`Decl`/`Arithm`) are **rejected**.
- An unrecognized AST node kind → fail loud, exit 2. A new `mvdan/sh` release
  that introduces a node we do not handle should stop the classifier, not be
  silently treated as safe.

**Redirects are words too — check every word bash will expand.** A heredoc's body
is expanded when its delimiter is unquoted, so `cat <<EOF` around `$(cmd)` runs
`cmd`. From the first commit until `0751c71` (2026-10-23) `safeRedirect` checked
only the delimiter, and that form classified Allow — arbitrary execution behind an
auto-approval, found by reading the code, not by any test or log record (the log
never records an allow). A body now passes only if every part is a `Lit`, which is
exactly how `mvdan/sh` stores a quoted-delimiter (`<<'EOF'`, `<<"EOF"`, `<<\EOF`) or
plain-text body. The same reading found `{name}>file`, which *assigns* `name`; it is
rejected because it would rebind a tracked variable. The lesson generalises: when
a node has more than one `*syntax.Word` field, each one is an expansion site, and
an unchecked field is a hole whatever the others say.

**Mutation-test the safety checks.** A suite that passes on the first run proves
nothing about the checks it never reaches. For the 2026-10-23 work every check was
disabled in turn in a scratch copy and the suite required to fail: the first pass
left two survivors (a symlinked *parent* directory, and the depth rule, both masked
by other checks), and three scope rules were killed only by `TestNotYetAllowed`,
which is not the safety wall. All three gaps got wall cases. A check that only a
deferred-feature table notices is unguarded.

## `styleAwk` and the goawk fork

For `awk`, the program itself is parsed and walked: `classifyAwkProgram`
(`internal/engine/awk.go`) positively whitelists every node. It rejects output redirects
(`>`, `>>`, `|`), `getline` from a pipe or file, `system`/`close`/`fflush`, and
user-defined functions; the builtin allowlist is
`length`/`substr`/`sprintf`/`tolower`/`toupper`/`gsub`/`sub`/`match`/`split`/
`index` plus the math functions. Pre-program flags are short-only value flags
(`-F sep`, `-v var=val`); the `-f script.awk`, `-e prog`, and gawk extension
forms are deliberately out of scope for v1.

Walking the program needs goawk's AST types, which upstream keeps in an
unimportable `internal/ast` package. A **thin goawk fork** (see the `replace`
directive in `go.mod`) adds an `ast/` package that re-exports those types; that
is the *only* reason for the fork. The fork is public
(`github.com/shabbir-genetech/goawk`), so `go build` resolves it normally;
upstreaming the `ast/` re-export to drop the `replace` is optional cleanup.

## `styleXargs` and the stdin-argv hazard

`xargs CMD [INITIAL-ARGS]` runs `CMD INITIAL-ARGS <stdin-items>`. It looks like
another transparent wrapper, but it is *not* safe to treat like `styleWrapper`,
for one reason: **the stdin items become argv on the wrapped command, and we
never see them.** The wrapped program parses those tokens itself — including any
that look like flags.

That breaks the allow-only invariant if we recurse into the full whitelist. `sort`
is whitelisted but its write flag `-o` is not, so `sort -o /tmp/x` falls through
to a prompt today. Yet `printf '\-o\n/tmp/x\n' | xargs sort` would classify `sort`
with *no* fixed args — allowed — and then stdin injects `-o /tmp/x`, a write we
just waved through. The same hole exists for `xargs git` (stdin → `push`),
`xargs date` (→ `-s`, set the clock), `xargs uniq` (the `IN OUT` positional
write), `xargs jq` (`-i`), and `xargs env` (runs an arbitrary command).

So `styleXargs` recurses only into commands flagged **`ArgvDataSafe`** on their
spec, whose membership rule is strictly stronger than "is read-only with these
args": a command qualifies only if it has **no write/mutate path under any argv at
all**, because stdin can supply any argv. The set is the minimal core one actually
pipes into xargs — `cat`, `tac`, `nl`, `head`, `tail`, `wc`, `grep`/`egrep`/`fgrep`,
`rg`, `stat`, `file`, `cut`, the `*sum`/`cksum` hashers, plus the pure readers
`echo`, `ls`, `readlink`, `basename`, `dirname`. Every subcommand/exec command
(`git`, `jj`, `nix`, `docker`, `systemctl`, `find`, `awk`, `devenv`) and every
command with an excluded write flag (`sort -o`, `date -s`, `uniq`'s `IN OUT`, `jq
-i`, `env`) stays out, even though they are in `safeCommands`. The replace-mode
flags `-I`/`-i`/`--replace` are not whitelisted on `xargs` itself, so `xargs -I{}
sh -c '… {}'` falls through as an unknown flag. `ArgvDataSafe` lives on the
`commandSpec` itself — there is no separate list to keep in sync, and the same flag
gates command substitution (next section).

## Command substitution `$(...)`

The same stdin→argv hazard appears in `$(...)`: the inner command runs, and its
*output* becomes an operand of the outer command — data we cannot see at classify
time. So command substitution reuses the `ArgvDataSafe` gate. `wordLiteral` still
rejects every expansion; `argTokens` then admits exactly one extra shape, a
**quoted** `"$(...)"` whose inner command classifies read-only
(`wordQuotedSubst`/`classifyCmdSubst`, recursing through `classifyStmt` like a pipe
stage). Rules, all fail-closed:

- The substitution must be **double-quoted** — bare `$(...)` word-splits and globs
  into an unknown number of argv words; quoting pins it to one operand.
- The result is an **opaque positional**: never the command name (that must stay
  literal so we know the spec), never a flag, never a flag's *argument* (`head -n
  "$(…)"` falls through), and accepted only by an **`ArgvDataSafe`** outer command
  — one safe under any operand value, even a flag-shaped one. So `echo "$(ls)"` and
  `cat "$(readlink -f x)"` accelerate; `sort "$(ls)"` does not.
- An unbound `$VAR`, `$((…))`, `<(…)`, extglob, and the mksh `${ …;}` forms remain
  rejected (a *literal-bound* `$VAR` is its literal — next section); an unsafe inner
  command (`echo "$(rm x)"`) or an unsafe inner pipeline stage
  (`echo "$(cat x | sed …)"`) falls the whole thing through.

One asymmetry worth knowing (it surprised the implementation's own first test
expectation): a substitution **embedded in a flag-shaped token**,
`ls --color="$(ls)"`, is a *single* word containing a `CmdSubst`, so `argTokens`
classifies the whole token as one opaque substituted operand — and it **allows**,
because `ls` is `ArgvDataSafe` (any operand value, flag-shaped or not, is harmless).
But the **separate-token** form `head -n "$(…)"` falls through, because there `-n`
is its own literal flag that demands a literal next token. Both outcomes are safe —
one accelerates, one prompts — but the rule "flag arguments must be literal" only
bites the separate-token shape. For a *non*-`ArgvDataSafe` command the embedded form
is correctly rejected (`sort --output="$(ls)"` → the opaque positional fails the
`ArgvDataSafe` check), which is the case that actually matters for safety.

A `--`-leniency tier (admitting commands like `sort` after a literal `--`) is
designed but deferred — see [FUTURE-WORK.md](FUTURE-WORK.md).

## Literal-bound variables (`$VAR`, since 2026-10-23)

`S=/tmp/x; grep foo "$S/a.txt"` is the single commonest shape agents send: the
variable was assigned a literal a moment earlier, so `"$S/a.txt"` is as knowable as
the literal path. The walk now carries a `scope`, binds each literal assignment,
and replaces a bound `$S`/`${S}` by its value, so every later check — the command's
spec, the redirect rules — sees the exact string bash would. The mechanics and the
full rule list are the header comment of `vars.go`; the argument rests on three
properties:

- **The value is exact.** Nothing on the right-hand side may be interpreted at
  assignment time (substitution, unbound variable, tilde, backslash, `$'…'`), and an
  unquoted *use* must not word-split or glob. Where text and meaning can differ, the
  binding is refused, not approximated.
- **The binding is believed only where bash made it.** A statement list binds for
  what follows; an `&&` chain binds along the chain and no further (an earlier link
  may have failed); `||` branches and pipeline stages are isolated (conditional,
  subshell); `( … )` and `"$(…)"` inherit but never leak. Every one of those
  conservative choices is a wall case where the leak would aim a scratchpad write
  at a different file, plus a `TestNotYetAllowed` case where it is merely harmless.
- **The assignment does nothing else.** Assigning an *exported* name changes every
  later child's environment — `HOME=…; git log` reads another `.gitconfig`, whose
  pager is a program. So only 1–3-character names are bindable, minus a reserved
  handful (`IFS`, `PS4`, `PWD`, …), and never one set in the hook's own environment.
  This is a positive rule: in the 2026-10-23 corpus about 94% of assignments used
  such names, and the names that steer programs (`PATH`, `HOME`, `PAGER`, `GIT_*`,
  `LD_*`) are all longer. Nothing else in the whitelist can assign a variable
  (`printf` has no `-v`; `read`, `declare`, `export`, `eval` are absent), which is
  what keeps the scope exact.

## Scratchpad writes (since 2026-10-23)

The harness gives each session a private `scratchpad_dir` and tells the agent to put
temporary output there, and some workspace rules *require* write-to-a-file over
truncating a pipe — so `cmd > "$S/out.txt"` is the shape a well-behaved agent sends,
and until now every one of them prompted. A write redirect now allows when its exact
target (`exactPath`) is a data file directly inside the event's `scratchpad_dir`.
The checks are the header comments of `internal/engine/scratch.go` (the path rules) and
`cmd/classify-bash/scratch.go` (the filesystem half, behind `engine.Host`); the argument:

- **The directory is validated per call**, not trusted: real path, a directory we
  own, not group/world-writable, and not an ancestor of `$HOME` or the working
  directory — a harness that sent `/` or `$HOME` must not mean "write anywhere".
- **The target cannot be picked up as configuration.** Tools find config by name —
  a bare repo's `config`/`HEAD`, `.git/`, `.jj/`, `flake.nix`, `*.toml` — and an
  allowed `git -C`/`jj -R` pointed at the scratchpad would honour it (`core.pager` is
  a program). So the target must sit at the top of the directory, not be a dotfile,
  and carry an extension from a positive data list (`.txt`, `.json`, `.log`, …): no
  configuration format is on it. In the corpus 98.9% of scratch writes were already
  top-level and 87% were `.txt`.
- **The write cannot be carried elsewhere.** An existing target must be a regular
  file we own with one link: a symlink or a hard link would redirect the write.

Residual, stated: the checks run at classify time, so a process racing to swap the
target before bash opens it is not prevented — but anything able to do that already
runs code as this user.

## Deferred wrapper shapes

Useful but each needs its own handling; bundling them into v1 would obscure the
design:

- **Flag-introduced** (`nix develop -c CMD`, `xargs -I{} CMD …`) — needs a
  `WrapFlag` variant naming which flag introduces the wrapped command. (Plain
  `xargs CMD` is handled by `styleXargs` above; only the replace-mode `-I{}`
  form, which splices the stdin item into the middle of the argv, stays
  deferred.)
- **Inline** (`env VAR=val CMD`, `nice CMD`) — first non-flag positional starts
  the wrapped command, no `--` required.
- **AST-level** (`time CMD`) — bash parses `time` as `TimeClause`, currently
  rejected in `classifyCmd`; would add a recursive case there.

## Build gotcha

`nix build` / `nix flake check` use a VCS-aware source, so a newly-created `.go`
file must be **snapshotted by the VCS** before building — otherwise the build
fails with errors like `undefined: classifyAwkProgram` because Nix never saw the
file.

The local working copy is **jujutsu (`jj`)** — there is a `.jj/` and no `.git/`,
so `git add` does not apply. jj auto-snapshots the working copy on every `jj`
command, so it is enough to run any `jj` command (e.g. `jj status`) after creating
the file, before building. (Upstream is consumed as a `git+ssh` flake input, so a
git checkout would instead need `git add`; same underlying requirement, different
tool.)

The flip side of auto-snapshot: `nix build` drops a `result` symlink into the
working copy, and jj will snapshot it too. `/result` is in `.gitignore` to keep
it out of commits — check `jj st` before committing so a stray artifact doesn't
ride along.

**cgo via stdlib `net`.** Importing a stdlib package that pulls in `net` — e.g.
`log/syslog` for the journal sink — enables cgo, so the pure-Go hermetic build
needs `CGO_ENABLED=0` (set in `flake.nix` on both the package's `env` and the test
check's `runCommand`). Without it the build fails `cgo: C compiler "gcc" not
found`, because the check carries only `pkgs.go`, no compiler. The dev shell
happens to have a `cc` (via `stdenv`), so `go test` there can mask this — trust
`nix flake check`, not just the dev-shell run. Keep CGO disabled when adding any
net-importing stdlib package.

**Host-only gates → cross-compile for portability.** Both the dev-shell `go test`
and `nix flake check` build only for the *host* platform (Linux here), so neither
catches a cross-*platform* break. The journal sink learned this the hard way:
`log/syslog` is Unix-only, and an unconditional import compiled cleanly on Linux
(green flake check) while silently breaking the Windows build. When you add or move
a stdlib import, sanity-check with `GOOS=windows go build ./...` and `GOOS=darwin
go build ./...` — they need no C toolchain and catch exactly the regression the
gates miss. (That is why the journal sink is build-tagged; see "Logging
non-allowed commands".)

**The license check runs hermetically.** `checks.licenses` runs `go-licenses
check ./...` in the no-network sandbox, and it works because `go mod vendor`
preserves each dependency's `LICENSE` file — go-licenses reads them from the
vendored tree under `-mod=vendor`. (`go-licenses report` additionally warns it
cannot compute license *URLs* offline; that is cosmetic — `check` only needs the
local license text.) The bundled-notice file is generated, not the check's job:
rerun `scripts/gen-third-party-licenses.sh` when dependencies change.
