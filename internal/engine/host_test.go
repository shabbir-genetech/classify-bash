package engine

// testHost is the Host the white-box tables run against: an explicit, empty
// environment (so they do not depend on whatever the suite runs in — a nix
// build sets lowercase `out`, `src`, …) and, unless a test sets one, no
// scratchpad. writable, when non-nil, stands in for the filesystem checks the
// real Host makes; the engine's own path rules still run first.
type testHost struct {
	env      map[string]bool
	scratch  string
	writable func(path string) bool
}

func (h testHost) EnvSet(name string) bool { return h.env[name] }
func (h testHost) ScratchDir() string      { return h.scratch }
func (h testHost) ScratchWritable(path string) bool {
	return h.writable != nil && h.writable(path)
}

// classifyCommand classifies cmd the way the tables and the triage replay
// expect: empty environment, no scratchpad.
func classifyCommand(cmd string) decision {
	return classifyCommandIn(cmd, testHost{})
}
