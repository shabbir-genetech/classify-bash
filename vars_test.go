package main

import "testing"

// The classifier refuses to bind a name that is set in its own environment.
// The test tables must not depend on the environment the suite runs in (a nix
// build sets lowercase `out`, `src`, …; a developer shell may set anything), so
// they run with an empty one. TestBindRefusesEnvironmentName covers the rule.
func init() {
	envLookup = func(string) (string, bool) { return "", false }
}

func TestBindRefusesEnvironmentName(t *testing.T) {
	saved := envLookup
	defer func() { envLookup = saved }()

	const cmd = `S=/tmp/x; cat "$S/a.txt"`
	if got := classifyCommand(cmd); got != decisionAllow {
		t.Fatalf("premise: classifyCommand(%q) = %v, want Allow with an empty environment", cmd, got)
	}
	// An exported S would carry the new value into every child process.
	envLookup = func(n string) (string, bool) { return "", n == "S" }
	if got := classifyCommand(cmd); got != decisionFallThrough {
		t.Errorf("classifyCommand(%q) with S in the environment = %v, want FallThrough", cmd, got)
	}
}
