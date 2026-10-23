package main

import (
	"strings"
	"testing"

	"github.com/shabbir-genetech/classify-bash/internal/adapter/claude"
	"github.com/shabbir-genetech/classify-bash/internal/engine"
)

// These drive shouldAllow and the presets rather than classifyEvent, which
// reads stdin and exits; they assert the decision, not the process plumbing.

func TestShouldAllowWithholdsOnDrift(t *testing.T) {
	ro := engine.Result{Class: engine.ReadOnly}
	withhold := strictness{onUnknownField: "withhold", onUnknownAST: "fail"}
	if !shouldAllow(ro, nil, withhold) {
		t.Fatal("premise: read-only with no drift must allow")
	}
	if shouldAllow(ro, []string{"surprise"}, withhold) {
		t.Error("an unknown field under withhold must not allow")
	}
	logPolicy := strictness{onUnknownField: "log", onUnknownAST: "fail"}
	if !shouldAllow(ro, []string{"surprise"}, logPolicy) {
		t.Error("under log, drift is recorded but classification stands")
	}
	for _, c := range []engine.Class{engine.NotReadOnly, engine.Unparseable} {
		if shouldAllow(engine.Result{Class: c}, nil, withhold) {
			t.Errorf("class %v must never allow", c)
		}
	}
}

// The legacy path is what the deployed registration (`classify-bash --log …`)
// takes, so it must be exactly the pre-merge behaviour: withhold on a new
// field, fail on a stale classifier.
func TestPresetsPreserveBehaviour(t *testing.T) {
	if legacyStrictness != (strictness{onUnknownField: "withhold", onUnknownAST: "fail"}) {
		t.Errorf("legacyStrictness = %+v, want withhold/fail", legacyStrictness)
	}
	lenient, err := resolveMode("claude-lenient")
	if err != nil || lenient.onUnknownField != "withhold" || lenient.onUnknownAST != "fallthrough" {
		t.Errorf("claude-lenient = %+v, %v; want withhold/fallthrough", lenient, err)
	}
	for name, s := range modes {
		if err := validateStrictness(s); err != nil {
			t.Errorf("mode %s is invalid: %v", name, err)
		}
	}
	if validateStrictness(strictness{onUnknownField: "typo", onUnknownAST: "fail"}) == nil {
		t.Error("an unknown policy value must be rejected")
	}
}

// End to end through the real decoder: the field the harness sends on every
// event is enumerated (allow), an unknown one withholds, and the safety probe
// never allows whatever the drift.
func TestDecodeThenDecide(t *testing.T) {
	s := legacyStrictness
	for payload, want := range map[string]bool{
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"},"scratchpad_dir":"/tmp/x"}`:                 true,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls"},"totally_new_field":"x"}`:                   false,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf /tmp/zzz"},"scratchpad_dir":"/x"}`:        false,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git status","effort":"medium"},"prompt_id":"p"}`: true,
	} {
		ev, drift, err := claude.DecodeEvent(strings.NewReader(payload))
		if err != nil {
			t.Fatalf("decode %s: %v", payload, err)
		}
		res := engine.ClassifyWith(ev.ToolInput.Command, fsHost{osHost{}})
		if got := shouldAllow(res, drift, s); got != want {
			t.Errorf("%s: allow = %v, want %v (drift %v)", payload, got, want, drift)
		}
	}
}
