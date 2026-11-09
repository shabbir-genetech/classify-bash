package engine

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestSedScriptAgainstGNUSandbox makes GNU sed itself the judge of sedScript.
// GNU's --sandbox mode rejects, at parse time and before anything runs, every
// program containing e, w or r in any spelling — including scripts whose
// structure only GNU's own parser sees (a delimiter inside a bracket
// expression: `s/[/]/g;/w x⏎/p` is a write to x to GNU, though a parser that
// ends the regex at the first `/` reads a harmless script; DataDog rshell's
// does, which is why it is not used here). So every script sedScript accepts
// must pass `sed --sandbox`, and must create no file.
//
// The scripts are the table cases plus every combination of a set of fragments
// chosen to stress the parser: delimiters, brackets, escapes, separators, and
// each forbidden command spliced in.
//
// It runs ~17k sed processes (~30 s), so it runs only with SED_ORACLE=1: always
// in `nix flake check` (which also supplies GNU sed and fails if it is missing),
// not in the package build's checkPhase that every consumer rebuild pays for.
func TestSedScriptAgainstGNUSandbox(t *testing.T) {
	if os.Getenv("SED_ORACLE") != "1" {
		t.Skip("set SED_ORACLE=1 to check sedScript against GNU sed --sandbox")
	}
	sed, err := exec.LookPath("sed")
	if err != nil {
		t.Fatal("SED_ORACLE=1 but no sed on PATH")
	}
	if out, err := exec.Command(sed, "--version").Output(); err != nil || !strings.Contains(string(out), "GNU sed") {
		t.Fatal("SED_ORACLE=1 but sed is not GNU sed")
	}
	dir := t.TempDir()

	addrs := []string{"", "1", "$", "1,3", "/a/", "/[/]/", "/x/I", "0,/y/", "/a\\/b/,$", "3!"}
	cmds := []string{
		"p", "d", "=", "q", "Q5",
		"s/a/b/", "s/[/]/X/", "s|a|b|g", "s/a\\/b/c/2gI", "s#x#y#p", "s/[[:space:]]*$//",
		"s/a/b/w f", "s/[/]/g;/w f\n/p", "s/[/]/X/w f", "w f", "W f", "e true", "s/a/b/e",
		"r /etc/hostname", "R /etc/hostname", "y/a/b/", "{p}", "s/a/b/M", "s/[\\n]/x/",
		// Halves of the bracket exploit: GNU reads `s/[/]/g;s|a/w f|b|` as one s
		// whose flags begin `w f|b|`; a bracket-unaware parser sees two commands.
		"s/[/]/g", "s|a/w f|b|",
	}
	seps := []string{";", "\n", " ; "}
	var scripts []string
	for _, a := range addrs {
		for _, c := range cmds {
			scripts = append(scripts, a+c)
			for _, s := range seps {
				for _, c2 := range cmds {
					scripts = append(scripts, a+c+s+c2)
				}
			}
		}
	}

	accepted := 0
	for _, s := range scripts {
		if !sedScript(s) {
			continue
		}
		accepted++
		cmd := exec.Command(sed, "--sandbox", "-n", "-e", s)
		cmd.Dir = dir
		cmd.Stdin = strings.NewReader("")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("sedScript accepts %q but GNU sed --sandbox rejects it: %v %s", s, err, out)
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) > 0 {
		t.Errorf("an accepted script created %d file(s) in the sandbox dir", len(entries))
	}
	// Guard against a vacuous pass: the generator must exercise the accepting path.
	if accepted < 1000 {
		t.Errorf("only %d generated scripts were accepted; the oracle is not being exercised", accepted)
	}
	t.Logf("%d of %d generated scripts accepted, every one passed GNU sed --sandbox", accepted, len(scripts))
}
