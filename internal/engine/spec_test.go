package engine

import "testing"

// TestSpecInvariants checks the whitelist DATA for shapes the matchers would
// silently ignore. A field that is set but never consulted reads as a safety
// check that is not there, so each such shape fails here instead.
func TestSpecInvariants(t *testing.T) {
	var walk func(path string, s *commandSpec)
	walk = func(path string, s *commandSpec) {
		if s.Positional != nil {
			// Only matchGNU's leaf acceptPositionals consults it.
			if s.Style != styleGNU {
				t.Errorf("%s: Positional is set on a non-GNU spec, which never checks it", path)
			}
			if s.Subcommands != nil {
				t.Errorf("%s: Positional is set on a spec with Subcommands, which dispatches instead", path)
			}
			if !s.AllowAnyPositional {
				t.Errorf("%s: Positional is set without AllowAnyPositional, so no positional reaches it", path)
			}
		}
		if s.ArgvDataSafe && !s.AllowAnyPositional {
			// acceptPositionals relies on this: an opaque operand passes the
			// ArgvDataSafe gate and must still meet the literal-positional one.
			t.Errorf("%s: ArgvDataSafe without AllowAnyPositional", path)
		}
		for _, f := range s.Flags {
			if f.Value == nil {
				continue
			}
			if !f.TakesArg && !f.OptionalArg {
				t.Errorf("%s: flag %q/%q has a Value check but takes no value", path, f.Short, f.Long)
			}
			if s.Style != styleGNU {
				t.Errorf("%s: flag %q/%q has a Value check on a non-GNU spec, which never checks it", path, f.Short, f.Long)
			}
		}
		for name, sub := range s.Subcommands {
			walk(path+" "+name, sub)
		}
	}
	for name, s := range safeCommands {
		walk(name, s)
	}
}
