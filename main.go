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
	// Resolve logging config first, before anything that can failLoud, so the
	// global is in place. Strict: a bad flag failLouds (exit 2).
	cfg, err := parseLogFlags(os.Args[1:])
	if err != nil {
		failLoud("bad flag: %v", err)
	}
	logCfg = cfg

	ev, err := decodeEvent(os.Stdin)
	if err != nil {
		failLoud("%v", err)
	}
	currentCommand = ev.ToolInput.Command

	if classifyCommand(ev.ToolInput.Command) == decisionAllow {
		emitAllow()
	}
	// Fall-through: best-effort log, then silent exit 0.
	logNonAllow(logCfg, "fallthrough", ev.ToolInput.Command, "")
}

// logCfg and currentCommand are process-global because failLoud — reachable from
// deep in the classifier, before main regains control — needs them to record a
// failloud event. Both stay zero (nil / "") until main resolves them, so any
// failLoud that fires earlier (e.g. a bad flag) simply logs nothing.
var (
	logCfg         *logConfig
	currentCommand string
)

// failLoud prints "classify-bash: <msg>" to stderr and exits with code 2.
// Used for every contract violation we want to be noisy about so we hear
// about it rather than silently ship a stale classifier. It also best-effort
// logs a failloud record (a no-op unless logging is configured and enabled).
func failLoud(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	logNonAllow(logCfg, "failloud", currentCommand, msg)
	fmt.Fprintf(os.Stderr, "classify-bash: %s\n", msg)
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
