package main

import "fmt"

// strictness is the two orthogonal axes along which this binary can be told how
// to react to things it does not recognise. They are deliberately separate from
// which host adapter is in use: a policy is reusable across adapters, and an
// adapter should not get to smuggle in a strictness choice.
type strictness struct {
	// onUnknownField governs a field in the host's event that this binary does
	// not enumerate.
	//
	//   fail     — exit 2, blocking the call. Every unknown field ever seen here
	//              was a benign harness addition, and "fail" has caused three
	//              full-session outages while catching nothing. The axis keeps
	//              the value because the design calls for it, not because it is
	//              ever recommended.
	//   withhold — log schema_drift and do not allow this call; the host prompts
	//              as for any unclassified command. The rule is that we do not
	//              auto-approve while blind to part of the payload, and we do not
	//              block over it either. The default for this repo's hook.
	//   log      — log schema_drift and classify as if the field were absent.
	//   ignore   — classify as if the field were absent, silently.
	onUnknownField string // fail | withhold | log | ignore

	// onUnknownAST governs an AST node kind the classifier cannot walk, meaning
	// the classifier has fallen behind mvdan/sh or goawk. Unlike a harness field,
	// this one has a real argument for "fail": we control when that dependency
	// moves, and a stale classifier is worth hearing about.
	onUnknownAST string // fail | fallthrough
}

// modes are named presets over (adapter + strictness). They exist so a
// registration line reads as an intent rather than a pile of flags.
//
// claude-lenient withholds on an unknown field rather than logging and
// allowing: the fork this layout came from used "log", which auto-approves
// while blind to part of the payload — the behaviour fb5978c removed. Pass
// --on-unknown-field=log to opt back into it.
var modes = map[string]strictness{
	"claude-strict":  {onUnknownField: "fail", onUnknownAST: "fail"},
	"claude-lenient": {onUnknownField: "withhold", onUnknownAST: "fallthrough"},
}

// legacyStrictness is what the binary did before subcommands existed, and is
// what the compatibility path uses: withhold on a new harness field, stay loud
// about a stale classifier. It matches NEITHER named preset, which is the point.
// Preserving it exactly is what makes the compat path safe to deploy ahead of
// the registration edit.
var legacyStrictness = strictness{onUnknownField: "withhold", onUnknownAST: "fail"}

func resolveMode(name string) (strictness, error) {
	s, ok := modes[name]
	if !ok {
		return strictness{}, fmt.Errorf("unknown --mode %q (want claude-strict or claude-lenient)", name)
	}
	return s, nil
}

func validateStrictness(s strictness) error {
	switch s.onUnknownField {
	case "fail", "withhold", "log", "ignore":
	default:
		return fmt.Errorf("unknown --on-unknown-field %q (want fail, withhold, log, or ignore)", s.onUnknownField)
	}
	switch s.onUnknownAST {
	case "fail", "fallthrough":
	default:
		return fmt.Errorf("unknown --on-unknown-ast %q (want fail or fallthrough)", s.onUnknownAST)
	}
	return nil
}
