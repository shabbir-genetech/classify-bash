package main

import "strings"

// flagStyle controls how a CommandSpec interprets its Flags list.
type flagStyle int

const (
	// styleGNU: short flags are single letters that may be clustered (`-la`,
	// `-n5`); long flags use the `--name`/`--name=value` form. The standard
	// shape for ls, cat, grep, git, etc.
	styleGNU flagStyle = iota
	// styleFind: every flag is a `-name` form (single dash, full word), no
	// clustering, no `=value` syntax. Values follow as the next argument.
	// Used by find(1).
	styleFind
)

// flagSpec describes one allowed flag for a command.
type flagSpec struct {
	Short       string // single letter (one rune); "" if long-only
	Long        string // full name without leading dashes; "" if short-only
	TakesArg    bool   // requires a value: consumes the next word if no `=value`
	OptionalArg bool   // accepts `--name=value` form but does NOT consume a separate next arg
	// Examples: ls --color (none) / --color=auto (with) — OptionalArg=true.
	// vs. git log --pretty=foo or git log --pretty foo — TakesArg=true.
}

// commandSpec is a strict whitelist for one command (or subcommand).
//
// Matching rules (styleGNU):
//   - Walk args; long flags `--x`/`--x=v` matched against Long, short flag
//     clusters `-abc`/`-n5` matched letter-by-letter against Short.
//   - A `--` token ends flag parsing; subsequent args are positional.
//   - The first non-flag arg either selects a Subcommand (if Subcommands is
//     non-nil) or marks the start of positionals.
//   - Flags after the first positional are NOT accepted (positionals "close"
//     the flag section). Strict, but matches typical safe-use patterns.
//   - If Subcommands is non-nil and no subcommand is selected, fall through.
//   - If Subcommands is nil and AllowAnyPositional is false, any positional
//     causes fall-through.
//
// Matching rules (styleFind):
//   - Args may be positionals (paths, anything not starting with `-`) or
//     `-name`-style flags in any order. Unknown flag → fall through. No
//     clustering, no `=value`.
type commandSpec struct {
	Style              flagStyle
	Flags              []flagSpec
	Subcommands        map[string]*commandSpec
	AllowAnyPositional bool
}

func (s *commandSpec) findShort(letter string) (flagSpec, bool) {
	for _, f := range s.Flags {
		if f.Short == letter {
			return f, true
		}
	}
	return flagSpec{}, false
}

func (s *commandSpec) findLong(name string) (flagSpec, bool) {
	for _, f := range s.Flags {
		if f.Long == name {
			return f, true
		}
	}
	return flagSpec{}, false
}

func (s *commandSpec) match(args []string) bool {
	switch s.Style {
	case styleGNU:
		return s.matchGNU(args)
	case styleFind:
		return s.matchFind(args)
	default:
		failLoud("unknown flag style: %d", s.Style)
		return false // unreachable
	}
}

func (s *commandSpec) matchGNU(args []string) bool {
	i := 0
	for i < len(args) {
		arg := args[i]

		// `--` ends flag parsing; remaining args are positional/subcommand-positional.
		if arg == "--" {
			return s.handlePositionals(args[i+1:])
		}

		// Long flag: `--name` or `--name=value`.
		if strings.HasPrefix(arg, "--") && len(arg) > 2 {
			name, val, hasVal := arg[2:], "", false
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				val = name[eq+1:]
				name = name[:eq]
				hasVal = true
				_ = val
			}
			f, ok := s.findLong(name)
			if !ok {
				return false
			}
			if hasVal {
				if !f.TakesArg && !f.OptionalArg {
					return false
				}
			} else if f.TakesArg {
				if i+1 >= len(args) {
					return false
				}
				i++ // consume required value
			}
			// !hasVal && !TakesArg: fine (OptionalArg or no-arg flag, both work)
			i++
			continue
		}

		// Short flag cluster: `-`, `-l`, `-la`, `-n5`, `-n` (with value next).
		if strings.HasPrefix(arg, "-") && len(arg) > 1 {
			cluster := arg[1:]
			consumedNext := false
			for j := 0; j < len(cluster); j++ {
				letter := string(cluster[j])
				f, ok := s.findShort(letter)
				if !ok {
					return false
				}
				if f.TakesArg {
					if j+1 < len(cluster) {
						// Rest of cluster is the value; nothing more to scan.
					} else {
						// Cluster ends with a value-taking flag; consume next arg.
						if i+1 >= len(args) {
							return false
						}
						consumedNext = true
					}
					break
				}
			}
			i++
			if consumedNext {
				i++
			}
			continue
		}

		// First positional.
		return s.handlePositionals(args[i:])
	}

	// No positional encountered.
	return s.handlePositionals(nil)
}

// handlePositionals processes the positional tail (after flags or `--`).
// If Subcommands is non-nil, the first element is the subcommand name and the
// rest are re-parsed by the subcommand's spec. Otherwise we accept iff the
// spec allows positionals (or there are none).
func (s *commandSpec) handlePositionals(args []string) bool {
	if s.Subcommands != nil {
		if len(args) == 0 {
			return false // subcommand required
		}
		sub, ok := s.Subcommands[args[0]]
		if !ok {
			return false
		}
		return sub.match(args[1:])
	}
	if s.AllowAnyPositional {
		return true
	}
	return len(args) == 0
}

func (s *commandSpec) matchFind(args []string) bool {
	i := 0
	for i < len(args) {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") {
			// Positional (a path). Any literal path is fine; non-literal words
			// were already rejected in literalWords.
			i++
			continue
		}
		// Both `-name` and `--name` accepted (find historically uses single
		// dash, but some long forms also accept `--`).
		name := strings.TrimLeft(arg, "-")
		if name == "" {
			return false
		}
		f, ok := s.findLong(name)
		if !ok {
			return false
		}
		i++
		if f.TakesArg {
			if i >= len(args) {
				return false
			}
			i++ // skip value (any literal is fine)
		}
	}
	return true
}
