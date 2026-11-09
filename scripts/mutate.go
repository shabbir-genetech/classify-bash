//go:build ignore

// mutate runs a mutation pass over the safety checks: each mutant disables one
// check in a scratch copy of the tree, and the named test tables must catch it.
//
//	go run scripts/mutate.go testdata/mutants/*.json
//	go run scripts/mutate.go -run 'TestMustNotAllow' testdata/mutants/gh.json
//
// A mutants file is a JSON array of
//
//	{"name": "...", "file": "internal/engine/x.go", "old": "...", "new": "...",
//	 "expect": "killed" | "survives", "why": "...", "run": "TestNotYetAllowed"}
//
// "run" is optional and overrides -run for that mutant: a check whose refusal is
// harmless-but-unsupported is guarded by TestNotYetAllowed, not the wall. "env"
// is optional extra environment for the test run (e.g. {"SED_ORACLE": "1"} for
// a check only the GNU sed oracle can catch).
//
// "old" must occur in "file" EXACTLY once — an absent or ambiguous anchor is an
// error, never a silent no-op. A mutant counts as killed only when a failure
// names a _test.go line: a compile error is NOT a kill (deleting a condition can
// leave a variable unused, and the build failure would read as a pass). A
// mutant expected to survive is a deliberate second lock; "why" names the lock
// that covers it. The exit status is nonzero if any result differs from its
// expectation, so a stored mutants file is also a regression test of the tests.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type mutant struct {
	Name   string            `json:"name"`
	File   string            `json:"file"`
	Old    string            `json:"old"`
	New    string            `json:"new"`
	Expect string            `json:"expect"`
	Why    string            `json:"why"`
	Run    string            `json:"run"`
	Env    map[string]string `json:"env"`
}

// testLine is the one place a regex reads text: go test's own failure format,
// `file_test.go:123:`, which has no parser of its own.
var testLine = regexp.MustCompile(`\S+_test\.go:\d+:.*`)

func main() {
	run := flag.String("run", "TestMustNotAllow|TestSpecInvariants", "test tables that must catch each mutant")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: go run scripts/mutate.go [-run REGEX] MUTANTS.json...")
		os.Exit(2)
	}
	root, err := os.Getwd()
	check(err)
	failed := 0
	for _, path := range flag.Args() {
		var ms []mutant
		data, err := os.ReadFile(path)
		check(err)
		check(json.Unmarshal(data, &ms))
		for _, m := range ms {
			tables := *run
			if m.Run != "" {
				tables = m.Run
			}
			got, detail := runOne(root, m, tables)
			ok := got == m.Expect
			if !ok {
				failed++
			}
			mark := "ok  "
			if !ok {
				mark = "FAIL"
			}
			fmt.Printf("%s %-9s (want %-8s) %-40s %s\n", mark, got, m.Expect, m.Name, detail)
		}
	}
	if failed > 0 {
		fmt.Printf("%d mutant(s) did not match their expectation\n", failed)
		os.Exit(1)
	}
}

// runOne applies m to a fresh copy of root and reports killed / survives /
// compile-error / bad-anchor, with the line that decided it.
func runOne(root string, m mutant, run string) (string, string) {
	if m.Expect != "killed" && m.Expect != "survives" {
		return "bad-expect", fmt.Sprintf("expect must be killed or survives, got %q", m.Expect)
	}
	work, err := os.MkdirTemp("", "mutate-*")
	check(err)
	defer os.RemoveAll(work)
	check(copyTree(root, work))

	target := filepath.Join(work, m.File)
	src, err := os.ReadFile(target)
	if err != nil {
		return "bad-anchor", err.Error()
	}
	if n := strings.Count(string(src), m.Old); n != 1 {
		return "bad-anchor", fmt.Sprintf("old occurs %d times in %s, want exactly 1", n, m.File)
	}
	check(os.WriteFile(target, []byte(strings.Replace(string(src), m.Old, m.New, 1)), 0o644))

	cmd := exec.Command("go", "test", "-count=1", "-run", run, "./...")
	cmd.Dir = work
	cmd.Env = os.Environ()
	for k, v := range m.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	if line := testLine.Find(out); line != nil {
		return "killed", string(line)
	}
	if err == nil {
		return "survives", m.Why
	}
	return "compile-error", firstLine(out)
}

// copyTree copies root to dst, skipping VCS state and build output.
func copyTree(root, dst string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		// SkipDir only for directories: returned for a file, WalkDir would skip
		// the rest of its parent. The `result` build symlink is not a regular
		// file, so the check below drops it.
		if d.IsDir() && (rel == ".jj" || rel == ".git") {
			return filepath.SkipDir
		}
		out := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil // symlinks and the like are not source
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		f, err := os.Create(out)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(f, in)
		return err
	})
}

func firstLine(b []byte) string {
	s, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	return s
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "mutate:", err)
		os.Exit(2)
	}
}
