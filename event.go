package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// event captures every known top-level field in a Claude Code PreToolUse
// payload. We only act on HookEventName, ToolName, and ToolInput.Command;
// every other field is enumerated solely so DisallowUnknownFields stays
// useful — when Claude Code adds a NEW top-level field, decoding fails
// loud and we get to decide whether the addition affects our classifier
// rather than silently ignoring it.
//
// Captured fields use json.RawMessage rather than concrete types so that
// type changes to ignored fields (e.g. cwd switching from string to object)
// don't trip the decoder. Only schema NAME drift is loud — type drift on
// fields we don't read is silent on purpose.
type event struct {
	HookEventName string    `json:"hook_event_name"`
	ToolName      string    `json:"tool_name"`
	ToolInput     toolInput `json:"tool_input"`

	SessionID      json.RawMessage `json:"session_id"`
	TranscriptPath json.RawMessage `json:"transcript_path"`
	CWD            json.RawMessage `json:"cwd"`
	PermissionMode json.RawMessage `json:"permission_mode"`
	ToolUseID      json.RawMessage `json:"tool_use_id"`
	AgentID        json.RawMessage `json:"agent_id"`
	AgentType      json.RawMessage `json:"agent_type"`
	Effort         json.RawMessage `json:"effort"`
}

// toolInput is the Bash tool's input shape. command is the only field we
// classify on; the others are enumerated for the same reason as the
// top-level context fields.
type toolInput struct {
	Command string `json:"command"`

	Description     json.RawMessage `json:"description"`
	Timeout         json.RawMessage `json:"timeout"`
	RunInBackground json.RawMessage `json:"run_in_background"`
	Effort          json.RawMessage `json:"effort"`
}

// decodeEvent reads exactly one JSON event from r and validates it against
// our expected contract. Returns a descriptive error for every violation;
// the caller converts that into a fail-loud exit.
func decodeEvent(r io.Reader) (*event, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()

	var ev event
	if err := dec.Decode(&ev); err != nil {
		return nil, fmt.Errorf("decode stdin: %w", err)
	}
	// Reject trailing data: the hook contract is exactly one event per invocation.
	if dec.More() {
		return nil, fmt.Errorf("decode stdin: unexpected trailing data after event")
	}

	if ev.HookEventName != "PreToolUse" {
		return nil, fmt.Errorf("unexpected hook_event_name %q (want PreToolUse)", ev.HookEventName)
	}
	if ev.ToolName != "Bash" {
		return nil, fmt.Errorf("unexpected tool_name %q (want Bash)", ev.ToolName)
	}
	if strings.TrimSpace(ev.ToolInput.Command) == "" {
		return nil, fmt.Errorf("tool_input.command is empty")
	}
	return &ev, nil
}
