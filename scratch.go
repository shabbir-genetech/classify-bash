package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Scratchpad writes (FUTURE-WORK §10). The harness sends every PreToolUse event
// a `scratchpad_dir`: a per-session directory it tells the agent to use for
// temporary output. Writing a data file there — `cmd > "$S/out.txt"` — changes
// nothing outside it, so a write redirect into it may be allowed. The checks
// below are what make "changes nothing outside it" true, not merely likely:
//
//   - The directory is real and private: absolute and clean, no symlink in its
//     path, a directory, owned by us, not group/world-writable, and not an
//     ancestor of $HOME or the working directory (a harness bug that sent "/"
//     or $HOME must not turn into "write anywhere").
//   - The target is a file directly in it: no subdirectory, no dotfile, and a
//     data extension from scratchExts. Tools discover config by NAME — a bare
//     repo's `config`/`HEAD`, `.git/config`, `.jj/`, `flake.nix`, `*.toml` —
//     and every one of those names is refused, so a file written here cannot be
//     picked up as configuration by an allowed `git -C`/`jj -R` later.
//   - If the target exists it is a regular file, owned by us, with one link: a
//     symlink or a hard link would carry the write to a file elsewhere.
//
// Residual, stated rather than hidden: the checks run at classify time, so a
// process racing to swap the target between the hook and bash's open() is not
// prevented. Anything able to do that already runs code as this user.

// scratchExts is a positive list of extensions for files that are data. It
// leaves out every executable or config-shaped suffix (.sh .py .js .nix .toml
// .yaml .conf .ini …) and extension-less names.
var scratchExts = map[string]bool{
	".txt": true, ".log": true, ".out": true, ".err": true,
	".json": true, ".jsonl": true, ".md": true, ".csv": true,
	".tsv": true, ".psv": true, ".diff": true, ".patch": true,
	".html": true, ".xml": true, ".sql": true, ".grep": true,
	".sum": true, ".sorted": true,
}

// validScratchDir decodes the event's scratchpad_dir and returns it only when
// it is safe to treat as a write-anywhere-inside sandbox; otherwise "" (the
// feature is off for this call and write redirects fall through as before).
// cwd is the event's cwd field, checked alongside the process's own.
func validScratchDir(raw, cwd json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var dir string
	if json.Unmarshal(raw, &dir) != nil || dir == "" {
		return ""
	}
	if !scratchDirShapeOK(dir) {
		return ""
	}
	if real, err := filepath.EvalSymlinks(dir); err != nil || real != dir {
		return ""
	}
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() || fi.Mode().Perm()&0o022 != 0 {
		return ""
	}
	if uid, _, ok := ownerLinks(fi); !ok || uid != os.Getuid() {
		return ""
	}
	var guarded []string
	if home, err := os.UserHomeDir(); err == nil {
		guarded = append(guarded, home)
	}
	if wd, err := os.Getwd(); err == nil {
		guarded = append(guarded, wd)
	}
	var evCWD string
	if len(cwd) > 0 && json.Unmarshal(cwd, &evCWD) == nil && evCWD != "" {
		guarded = append(guarded, evCWD)
	}
	for _, p := range guarded {
		if p == dir || strings.HasPrefix(p, dir+"/") {
			return ""
		}
	}
	return dir
}

// scratchDirShapeOK is the textual part of validScratchDir: absolute, clean, at
// least three levels deep (so never "/", "/tmp" or "/home"), and free of the
// characters exactPath refuses, so a target built on it can still be exact.
func scratchDirShapeOK(dir string) bool {
	return filepath.IsAbs(dir) && filepath.Clean(dir) == dir && strings.Count(dir, "/") >= 3 &&
		!strings.ContainsAny(dir, "\\*?[]{}~ \t\n(")
}

// scratchTargetOK reports whether path (already exact, see exactPath) is a data
// file directly inside the validated scratchpad dir that a write cannot escape.
func scratchTargetOK(dir, path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Dir(path) != dir {
		return false
	}
	base := filepath.Base(path)
	if strings.HasPrefix(base, ".") || !scratchExts[strings.ToLower(filepath.Ext(base))] {
		return false
	}
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return true // bash creates it, owned by us, inside a directory we own
	}
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	uid, links, ok := ownerLinks(fi)
	return ok && uid == os.Getuid() && links == 1
}
