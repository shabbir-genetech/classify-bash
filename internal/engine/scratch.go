package engine

import (
	"path/filepath"
	"strings"
)

// Scratchpad writes (FUTURE-WORK §10), engine half. A write redirect may allow
// when its exact target is a data file at the top of the Host's validated
// scratchpad. The engine owns the rules it can decide from the path alone; the
// Host owns the ones that need the filesystem (the directory is real and
// private; an existing target is a regular file we own with one link). The full
// argument is DESIGN.md "Scratchpad writes".
//
// The path rules exist because tools discover configuration by NAME — a bare
// repo's `config`/`HEAD`, `.git/config`, `.jj/`, `flake.nix`, `*.toml` — and an
// allowed `git -C`/`jj -R` pointed at the scratchpad would honour it. So the
// target sits at the top of the directory, is not a dotfile, and carries an
// extension from a positive data list: no configuration format is on it.

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

// scratchWriteOK reports whether path (already exact, see exactPath) may be
// written: a top-level data file in the Host's scratchpad, which the Host
// confirms cannot carry the write elsewhere.
func scratchWriteOK(path string, h Host) bool {
	dir := h.ScratchDir()
	return dir != "" && scratchTargetShapeOK(dir, path) && h.ScratchWritable(path)
}

// scratchTargetShapeOK is the path-only half of scratchWriteOK.
func scratchTargetShapeOK(dir, path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Dir(path) != dir {
		return false
	}
	base := filepath.Base(path)
	return !strings.HasPrefix(base, ".") && scratchExts[strings.ToLower(filepath.Ext(base))]
}
