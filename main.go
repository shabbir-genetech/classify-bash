// classify-bash is a Claude Code PreToolUse hook for the Bash tool. It reads a
// single PreToolUse event from stdin, classifies the embedded shell command
// against a strict whitelist of read-only commands and flags, and emits an
// "allow" permission decision when the command is unambiguously safe.
//
// Failure modes:
//   - Bash parse failure or unsafe command: exit 0 with no stdout (fall through
//     to Claude Code's normal permission prompt).
//   - JSON contract violation (unknown fields, wrong event/tool, missing
//     command): exit 2 with a "classify-bash: <reason>" line on stderr.
//   - Unknown AST node kind from mvdan/sh that the classifier does not handle:
//     exit 2 with a similar stderr message. Means the classifier is out of date
//     and must be extended before the upgrade can be trusted.
package main

import (
	"fmt"
	"os"
)

func main() {
	ev, err := decodeEvent(os.Stdin)
	if err != nil {
		failLoud("%v", err)
	}
	if classifyCommand(ev.ToolInput.Command) == decisionAllow {
		emitAllow()
	}
	// Otherwise: silent fall-through (exit 0, no output).
}

// failLoud prints "classify-bash: <msg>" to stderr and exits with code 2.
// Used for every contract violation we want to be noisy about so we hear
// about it rather than silently ship a stale classifier.
func failLoud(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "classify-bash: "+format+"\n", args...)
	os.Exit(2)
}

// emitAllow writes the PreToolUse allow JSON to stdout and exits 0.
func emitAllow() {
	// Hand-written to avoid pulling encoding/json into the hot path for a
	// fixed response. Stable across schema additions because we only emit
	// the fields Claude Code currently requires.
	const out = `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow"}}` + "\n"
	if _, err := os.Stdout.WriteString(out); err != nil {
		failLoud("write stdout: %v", err)
	}
	os.Exit(0)
}
