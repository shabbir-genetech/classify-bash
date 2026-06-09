# Design notes

Deeper rationale behind `classify-bash`. The [README](README.md) covers the
contract and how to run it; this file records *why* the classifier is shaped the
way it is, for anyone extending it.

## Why allow-only, never deny

The hook only ever emits `allow`. It never emits `deny`/`ask`, and anything it
cannot positively classify falls through (exit 0, no stdout) to Claude Code's
normal permission prompt. The hook is an accelerator, not a gate: a bug in it can
only *fail to speed something up*, never wave through something the normal flow
would have stopped. This asymmetry is the whole safety argument — keep it.

## Strict positive whitelist

Every command, subcommand, and flag is enumerated positively in `commands.go`.
An unknown command, unknown subcommand, or unknown flag on a known command →
fall through. We never write "allow X except when Y": a future release of some
tool may add a state-mutating `Z` we did not anticipate, and an
allow-except-list would wave it through. A positive list fails closed instead.

### Tiers

- **A — no-subcommand reads** (`cat`, `ls`, `grep`, `cd`) plus stdout-only
  filters (`tr`, `cut`, `sort`, `uniq`, `paste`): `styleGNU`, `AllowAnyPositional`,
  no write flags.
- **B — command + subcommand** (`git status`/`log`, `jj`, `nix eval`,
  `docker ps`, `systemctl status`): `styleGNU` + `Subcommands` dispatch.
- **C — flag-aware dual-use** (`find`): `styleFind`.
- **D — transparent wrappers** (`devenv shell --`, `nix shell PKGS --`):
  `styleWrapper`. The literal `--` is REQUIRED; the tail after it is looked up in
  the whitelist and classified recursively.
- **E — scripting with a whitelisted body** (`awk`): `styleAwk` — see below.
- **F — stdin-append wrapper** (`xargs`): `styleXargs`. Distinct from D — there
  is no `--`, the first non-flag token is the wrapped command, and recursion is
  gated by a *curated subset* of the whitelist (`xargsWrappable`), not the whole
  thing. See below for why.

## Defensive JSON contract

Input is decoded with `DisallowUnknownFields`; the event must declare
`hook_event_name == "PreToolUse"` and `tool_name == "Bash"`. Any deviation exits
2 with a `classify-bash: <reason>` line on stderr. We would rather fail loud than
silently ship a stale classifier.

**Tracking harness-added fields.** Because the decoder is strict, any new field
the harness starts sending on the event or inside `tool_input` will make decoding
exit 2 — which *blocks the Bash call*. The fix for any future field is mechanical:
add it as an ignored `json.RawMessage` in the right struct (a top-level field goes
on `event`, a `tool_input` field goes on `toolInput` in `event.go`), add an
accept-test covering an event that carries it, and rebuild. No `go.mod` change, so
the vendor hash is unaffected. This has already happened once for an `effort`
field that the harness began attaching both inside `tool_input` and at the top
level — both structs now carry an ignored `Effort`.

## Logging non-allowed commands

Logging is **opt-in** and records only the *non-allowed* cases — fall-through and
`failLoud` — as one JSON line each (`log.go`). It exists so you can see what is
failing to accelerate and notice classifier staleness. Several design choices are
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
drops. Everything else (`log.go`) stays portable, so the file sink works on every
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

## AST handling (`classifyCmd`)

The shell command is parsed with `mvdan.cc/sh/v3/syntax`. Compound forms are
handled conservatively:

- `BinaryCmd` (`&&`, `||`, pipe) and `Subshell` (`( … )`) **recurse** — every
  inner statement must itself classify as safe.
- `Block` and every other compound kind (`If`/`For`/`While`/`Case`/`FuncDecl`/
  `Let`/`Time`/`Coproc`/`Test`/`Decl`/`Arithm`) are **rejected**.
- An unrecognized AST node kind → fail loud, exit 2. A new `mvdan/sh` release
  that introduces a node we do not handle should stop the classifier, not be
  silently treated as safe.

## `styleAwk` and the goawk fork

For `awk`, the program itself is parsed and walked: `classifyAwkProgram`
(`awk.go`) positively whitelists every node. It rejects output redirects
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
upstreaming the `ast/` re-export to drop the `replace` is optional cleanup (see
[PUBLIC-READINESS.md](PUBLIC-READINESS.md)).

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

So `styleXargs` recurses into a **curated subset**, `xargsWrappable`, whose
membership rule is strictly stronger than "is read-only with these args": a
command qualifies only if it has **no write/mutate path under any argv at all**,
because stdin can supply any argv. The v1 set is the minimal core one actually
pipes into xargs — `cat`, `head`, `tail`, `wc`, `grep`, `rg`, `stat`, `file`,
`cut`, the `*sum`/`cksum` hashers. Every subcommand/exec command (`git`, `jj`,
`nix`, `docker`, `systemctl`, `find`, `awk`, `devenv`) and every command with an
excluded write flag (`sort`, `date`, `uniq`, `jq`, `env`, `hostname`, `ls` is
simply deferred) stays out, even though they are in `safeCommands`. The
replace-mode flags `-I`/`-i`/`--replace` are not whitelisted on `xargs` itself,
so `xargs -I{} sh -c '… {}'` falls through as an unknown flag. `xargsWrappable`
keys must stay a subset of `safeCommands`; a drift makes `classifyWrapped` fail
loud.

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
