package engine

import "testing"

func TestBindRefusesEnvironmentName(t *testing.T) {
	const cmd = `S=/tmp/x; cat "$S/a.txt"`
	if got := classifyCommandIn(cmd, testHost{}); got != decisionAllow {
		t.Fatalf("premise: %q with an empty environment = %v, want Allow", cmd, got)
	}
	// An exported S would carry the new value into every child process.
	if got := classifyCommandIn(cmd, testHost{env: map[string]bool{"S": true}}); got != decisionFallThrough {
		t.Errorf("%q with S set in the environment = %v, want FallThrough", cmd, got)
	}
}

// A caller that supplies no Host must lose acceleration, never safety: no
// binding is believed and no scratch write allows.
func TestNoHostIsConservative(t *testing.T) {
	for _, cmd := range []string{
		`S=/tmp/x; cat "$S/a.txt"`,
		`grep -n foo f > /tmp/x/out.txt`,
	} {
		if got := Classify(cmd).Class; got != NotReadOnly {
			t.Errorf("Classify(%q) with no Host = %v, want NotReadOnly", cmd, got)
		}
	}
	if got := Classify("ls -la").Class; got != ReadOnly {
		t.Errorf("premise: Classify(ls -la) = %v, want ReadOnly", got)
	}
}

// The engine's path rules for a scratch write, with the filesystem half
// stubbed to "writable": what is refused here is refused before the Host is
// asked.
func TestScratchTargetShape(t *testing.T) {
	const dir = "/tmp/x/s"
	h := testHost{scratch: dir, writable: func(string) bool { return true }}
	for c, want := range map[string]decision{
		"grep -n foo f > /tmp/x/s/out.txt":               decisionAllow,
		`S=/tmp/x/s; grep -n foo f > "$S/out.json"`:      decisionAllow,
		"grep -n foo f > /tmp/x/s/sub/a.txt":             decisionFallThrough,
		"grep -n foo f > /tmp/x/s/.hidden.txt":           decisionFallThrough,
		"grep -n foo f > /tmp/x/s/config":                decisionFallThrough,
		"grep -n foo f > /tmp/x/s/run.sh":                decisionFallThrough,
		"grep -n foo f > /tmp/x/s/../out.txt":            decisionFallThrough,
		"grep -n foo f > /tmp/x/sx/out.txt":              decisionFallThrough,
		"grep -n foo f > /tmp/out.txt":                   decisionFallThrough,
		`true || S=/tmp/x/s; grep -n foo f > "$S/o.txt"`: decisionFallThrough,
	} {
		if got := classifyCommandIn(c, h); got != want {
			t.Errorf("classifyCommandIn(%q) = %v, want %v", c, got, want)
		}
	}
	// The Host's answer is final: a target that passes the path rules still
	// falls through when the Host says it cannot be written safely.
	refuse := testHost{scratch: dir, writable: func(string) bool { return false }}
	if got := classifyCommandIn("grep -n foo f > /tmp/x/s/out.txt", refuse); got != decisionFallThrough {
		t.Errorf("with ScratchWritable=false = %v, want FallThrough", got)
	}
}
