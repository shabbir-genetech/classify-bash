package main

import (
	"strings"
	"testing"
)

// An unrecognized payload field must WITHHOLD the allow without blocking. This
// is the narrow guarantee the gate exists for: classify-bash never auto-approves
// while blind to part of the event, but a harness addition must not be able to
// take the Bash tool down (it did, five times, under the old strict decoder).
//
// These tests drive shouldAllow rather than main, so they assert the decision
// only; that the process exits 0 in every one of these cases is a property of
// main having no other exit path here — there is no failLoud on this route.
func TestDriftWithholdsAllowForOtherwiseAllowableCommand(t *testing.T) {
	// `ls` is squarely on the whitelist: with no drift it allows.
	if !shouldAllow("ls", nil) {
		t.Fatal("shouldAllow(\"ls\", nil) = false, want true (test premise broken: ls must be whitelisted)")
	}
	// Same command, one unknown field: the allow is withheld.
	if shouldAllow("ls", []string{"surprise"}) {
		t.Error("shouldAllow(\"ls\", [surprise]) = true, want false — an unknown field must withhold the allow")
	}
}

// Drift must not be the ONLY thing consulted: a non-whitelisted command stays
// non-allowed whether or not the payload drifted. Guards against a refactor that
// turns the gate into the sole decision.
func TestDriftGateDoesNotOverrideTheClassifier(t *testing.T) {
	for _, drift := range [][]string{nil, {"surprise"}} {
		if shouldAllow("rm -rf /tmp/zzz", drift) {
			t.Errorf("shouldAllow(rm -rf, %v) = true, want false", drift)
		}
	}
}

// Every field the harness currently sends is enumerated, so a realistic event
// must produce no drift and therefore still accelerate. If this fails after a
// Claude Code upgrade, the harness added a field: enumerate it on event or
// toolInput (see decodeEvent) rather than relaxing the gate.
func TestCurrentHarnessPayloadStillAllows(t *testing.T) {
	const body = `{"hook_event_name":"PreToolUse","tool_name":"Bash",` +
		`"tool_input":{"command":"ls","description":"list","timeout":120,` +
		`"dangerouslyDisableSandbox":false},` +
		`"session_id":"s","transcript_path":"/t","cwd":"/c","permission_mode":"default",` +
		`"tool_use_id":"tu","agent_id":"a","agent_type":"claude","effort":"medium",` +
		`"prompt_id":"p","scratchpad_dir":"/tmp/scratch"}`

	ev, drift, err := decodeEvent(strings.NewReader(body))
	if err != nil {
		t.Fatalf("decodeEvent: unexpected error %v", err)
	}
	if len(drift) != 0 {
		t.Fatalf("drift = %v, want none — enumerate the new field on the struct", drift)
	}
	if !shouldAllow(ev.ToolInput.Command, drift) {
		t.Error("a fully-enumerated harness payload must still allow a whitelisted command")
	}
}

// Drift inside tool_input gates too, not just top-level fields.
func TestDriftInsideToolInputWithholdsAllow(t *testing.T) {
	const body = `{"hook_event_name":"PreToolUse","tool_name":"Bash",` +
		`"tool_input":{"command":"ls","brand_new_input_field":true}}`

	ev, drift, err := decodeEvent(strings.NewReader(body))
	if err != nil {
		t.Fatalf("decodeEvent: unexpected error %v", err)
	}
	// Nested drift is reported with a tool_input. prefix so the record says
	// where the field appeared.
	if len(drift) != 1 || drift[0] != "tool_input.brand_new_input_field" {
		t.Fatalf("drift = %v, want [tool_input.brand_new_input_field]", drift)
	}
	if shouldAllow(ev.ToolInput.Command, drift) {
		t.Error("unknown tool_input field must withhold the allow")
	}
}
