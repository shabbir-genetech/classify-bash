//go:build !windows && !plan9

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/shabbir-genetech/classify-bash/internal/engine"
)

// scratchFixture makes a private scratchpad directory and returns its real path.
func scratchFixture(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func rawJSON(t *testing.T, s string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestValidScratchDir(t *testing.T) {
	dir := scratchFixture(t)
	if got := validScratchDir(rawJSON(t, dir), nil); got != dir {
		t.Fatalf("validScratchDir(%q) = %q, want it accepted", dir, got)
	}

	link := filepath.Join(scratchFixture(t), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	// A symlink higher up the path, with a real directory as the last
	// component, which an Lstat of the final element cannot see.
	parent := scratchFixture(t)
	if err := os.Mkdir(filepath.Join(parent, "real"), 0o700); err != nil {
		t.Fatal(err)
	}
	viaLink := filepath.Join(scratchFixture(t), "up")
	if err := os.Symlink(parent, viaLink); err != nil {
		t.Fatal(err)
	}
	shared := scratchFixture(t)
	if err := os.Chmod(shared, 0o770); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(scratchFixture(t), "f.txt")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	for name, raw := range map[string]json.RawMessage{
		"absent":           nil,
		"not a string":     json.RawMessage(`42`),
		"empty":            rawJSON(t, ""),
		"relative":         rawJSON(t, "tmp/x/y"),
		"root":             rawJSON(t, "/"),
		"too shallow":      rawJSON(t, "/tmp"),
		"not clean":        rawJSON(t, dir+"/."),
		"missing":          rawJSON(t, dir+"/nope"),
		"through symlink":  rawJSON(t, link),
		"symlinked parent": rawJSON(t, filepath.Join(viaLink, "real")),
		"group-writable":   rawJSON(t, shared),
		"a file":           rawJSON(t, file),
		"cwd":              rawJSON(t, wd),
		"ancestor of cwd":  rawJSON(t, filepath.Dir(wd)),
	} {
		if got := validScratchDir(raw, nil); got != "" {
			t.Errorf("%s: validScratchDir(%s) = %q, want rejected", name, raw, got)
		}
	}
	// The event's own cwd is guarded too.
	if got := validScratchDir(rawJSON(t, dir), rawJSON(t, filepath.Join(dir, "proj"))); got != "" {
		t.Errorf("validScratchDir with event cwd inside it = %q, want rejected", got)
	}
}

// The shape rules alone: the filesystem checks in validScratchDir reject most
// shallow paths for other reasons (mode, ownership, being an ancestor of the
// working directory), which would hide a broken depth or character rule.
func TestScratchDirShape(t *testing.T) {
	for dir, want := range map[string]bool{
		"/a/b/c":    true,
		"/tmp/x/y":  true,
		"/":         false,
		"/tmp":      false,
		"/a/b":      false, // two levels: still too shallow
		"a/b/c":     false,
		"/a/b/c/":   false, // not clean
		"/a/b/../c": false,
		"/a/b/c d":  false,
		"/a/b/*":    false,
		"/a/b/~":    false,
		"/a/b/\\x":  false,
		"/a/b/{x}":  false,
	} {
		if got := scratchDirShapeOK(dir); got != want {
			t.Errorf("scratchDirShapeOK(%q) = %v, want %v", dir, got, want)
		}
	}
}

func TestScratchRedirectAllow(t *testing.T) {
	dir := scratchFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "exists.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{
		"grep -n foo f > DIR/out.txt",
		"grep -n foo f >> DIR/out.log",
		"grep -n foo f 2> DIR/err.txt",
		"grep -n foo f &> DIR/all.txt",
		"grep -n foo f >| DIR/clobber.txt",
		"grep -n foo f > DIR/exists.txt", // an existing regular file we own
		"grep -n foo f > 'DIR/quoted.json'",
		`S=DIR; grep -n foo f > "$S/out.txt"`,
		"S=DIR\ngrep -n foo f > \"$S/o.txt\" 2>&1\ncat \"$S/o.txt\"", // the idiom
		`S=DIR && jj log -r @ > "$S/log.txt"`,
		`S=DIR; F="$S/x.md"; cat a > "$F"`,
	} {
		c = strings.ReplaceAll(c, "DIR", dir)
		if got := classifyIn(c, dir); got != engine.ReadOnly {
			t.Errorf("classifyCommandIn(%q) = %v, want Allow", c, got)
		}
	}
}

func TestScratchRedirectNotAllow(t *testing.T) {
	dir := scratchFixture(t)
	elsewhere := scratchFixture(t)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.Mkdir(filepath.Join(dir, "sub"), 0o700))
	must(os.Mkdir(filepath.Join(dir, "adir.txt"), 0o700))
	must(os.Symlink("/etc/passwd", filepath.Join(dir, "link.txt")))
	must(os.WriteFile(filepath.Join(elsewhere, "victim.txt"), nil, 0o600))
	must(os.Link(filepath.Join(elsewhere, "victim.txt"), filepath.Join(dir, "hard.txt")))
	must(syscall.Mkfifo(filepath.Join(dir, "fifo.txt"), 0o600))

	for _, c := range []string{
		"grep -n foo f > DIR/sub/a.txt",   // subdirectory
		"grep -n foo f > DIR/.hidden.txt", // dotfile
		"grep -n foo f > DIR/config",      // a bare repo's config: no extension
		"grep -n foo f > DIR/HEAD",
		"grep -n foo f > DIR/flake.nix", // config-shaped extension
		"grep -n foo f > DIR/config.toml",
		"grep -n foo f > DIR/run.sh",     // executable extension
		"grep -n foo f > DIR/../out.txt", // escapes
		"grep -n foo f > DIR/./out.txt",  // not clean
		"grep -n foo f > DIR//out.txt",
		"grep -n foo f > DIRx/out.txt",      // a sibling sharing the prefix
		"grep -n foo f > DIR/*.txt",         // glob
		"grep -n foo f > DIR/{a,b}.txt",     // brace expansion
		"grep -n foo f > DIR/link.txt",      // symlink out
		"grep -n foo f > DIR/hard.txt",      // hard link to a file elsewhere
		"grep -n foo f > DIR/fifo.txt",      // not a regular file
		"grep -n foo f > DIR/adir.txt",      // a directory
		"grep -n foo f <> DIR/rw.txt",       // read-write open
		"grep -n foo f {fd}> DIR/fd.txt",    // named fd assigns a variable
		"rm x > DIR/out.txt",                // the command itself still classifies
		"> DIR/bare.txt",                    // bare redirect, no command
		`S='DIR/*'; grep -n foo f > $S.txt`, // unquoted glob through a binding
		`grep -n foo f > "$S/out.txt"`,      // unbound
		"grep -n foo f > $'DIR/out.txt'",    // ANSI-C quoting
		"grep -n foo f > /tmp/out.txt",      // outside the scratchpad
		// A binding bash never made would aim the write somewhere else: in each
		// of these S is unset when the redirect runs, so the target is /o.txt.
		`true || S=DIR; grep -n foo f > "$S/o.txt"`,
		`false && S=DIR; grep -n foo f > "$S/o.txt"`,
		`S=DIR | true; grep -n foo f > "$S/o.txt"`,
		`(S=DIR); grep -n foo f > "$S/o.txt"`,
		`echo "$(S=DIR)"; grep -n foo f > "$S/o.txt"`,
	} {
		c = strings.ReplaceAll(c, "DIR", dir)
		if got := classifyIn(c, dir); got != engine.NotReadOnly {
			t.Errorf("classifyCommandIn(%q) = %v, want FallThrough", c, got)
		}
	}
	// Without a validated scratchpad, nothing changes.
	c := "grep -n foo f > " + dir + "/out.txt"
	if got := classifyIn(c, ""); got != engine.NotReadOnly {
		t.Errorf("classifyCommandIn(%q, \"\") = %v, want FallThrough", c, got)
	}
}
