package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/shabbir-genetech/classify-bash/internal/engine"
)

// Scratchpad writes (FUTURE-WORK §10), host half. The harness sends every
// PreToolUse event a `scratchpad_dir`: a per-session directory it tells the
// agent to use for temporary output. Writing a data file there changes nothing
// outside it, so a write redirect into it may be allowed. The engine decides
// what it can from the path (top level, no dotfile, a data extension —
// internal/engine/scratch.go); this file decides what needs the filesystem:
//
//   - The directory is real and private: absolute and clean, no symlink in its
//     path, a directory, owned by us, not group/world-writable, and not an
//     ancestor of $HOME or the working directory (a harness bug that sent "/"
//     or $HOME must not turn into "write anywhere").
//   - If the target exists it is a regular file, owned by us, with one link: a
//     symlink or a hard link would carry the write to a file elsewhere.
//
// Residual, stated rather than hidden: the checks run at classify time, so a
// process racing to swap the target between the hook and bash's open() is not
// prevented. Anything able to do that already runs code as this user.

// osHost is the engine.Host for a command about to run on this machine: the
// environment is this process's (which the host's shell inherits), and the
// scratchpad is whatever validScratchDir accepted, or none.
type osHost struct {
	scratch string
}

var _ engine.Host = osHost{}

func (osHost) EnvSet(name string) bool {
	_, set := os.LookupEnv(name)
	return set
}

func (h osHost) ScratchDir() string { return h.scratch }

func (h osHost) ScratchWritable(path string) bool {
	return h.scratch != "" && scratchFileWritable(path)
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
// characters the engine's exactPath refuses, so a target built on it can still
// be exact.
func scratchDirShapeOK(dir string) bool {
	return filepath.IsAbs(dir) && filepath.Clean(dir) == dir && strings.Count(dir, "/") >= 3 &&
		!strings.ContainsAny(dir, "\\*?[]{}~ \t\n(")
}

// scratchFileWritable is the filesystem half of a scratch write: the target is
// absent (bash creates it, owned by us, inside a directory we own) or a regular
// file we own with exactly one link.
func scratchFileWritable(path string) bool {
	fi, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if err != nil || !fi.Mode().IsRegular() {
		return false
	}
	uid, links, ok := ownerLinks(fi)
	return ok && uid == os.Getuid() && links == 1
}
