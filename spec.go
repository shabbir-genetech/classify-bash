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
	// styleWrapper: command is a transparent wrapper that execs the argv after
	// a literal `--` separator. Pre-`--` segment matches GNU-style flags from
	// spec.Flags plus optional positionals (gated by AllowAnyPositional). The
	// wrapped command is looked up in safeCommands and matched recursively, so
	// safety is inherited rather than loosened. Used by `devenv shell --` and
	// `nix shell PKGS --`.
	styleWrapper
	// styleAwk: awk-shape command line `[flag…] PROGRAM [files…]`. Pre-program
	// flags are short-only (`-F sep`, `-v var=val`); all listed flags must take
	// arguments. The first non-flag positional is the awk program source,
	// classified by classifyAwkProgram (positive-whitelisted awk AST). Any
	// remaining positionals are file paths, accepted as-is (literalWords has
	// already rejected words with shell expansion). `--` ends flag parsing.
	styleAwk
	// styleXargs: stdin-append wrapper `xargs [flag…] CMD [INITIAL-ARG…]`. Unlike
	// styleWrapper there is NO `--` separator — the first non-flag token is the
	// wrapped command, and the rest are fixed initial-arguments. The wrapped
	// command is looked up in a CURATED subset (xargsWrappable), not the full
	// safeCommands, because xargs appends stdin tokens to the wrapped argv that
	// we cannot see at classify time; only commands with no write path under ANY
	// argv are safe to wrap. See matchXargs and DESIGN.md.
	styleXargs
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
	case styleWrapper:
		return s.matchWrapper(args)
	case styleXargs:
		return s.matchXargs(args)
	case styleAwk:
		return s.matchAwk(args)
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

		// Long flag: `--name` or `--name=value`. A long-flag name never starts
		// with another `-`, so tokens like `---` or `----foo` are NOT flags —
		// they fall through to positional handling. This matches how GNU getopt
		// treats such tokens (e.g. `echo ---` prints `---`).
		if strings.HasPrefix(arg, "--") && len(arg) > 2 && arg[2] != '-' {
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

		// Short flag cluster: `-l`, `-la`, `-n5`, `-n` (with value next). A short
		// flag's first character is never `-`, so `---` etc. fall through to
		// positional handling alongside `---foo` from the long-flag branch.
		if strings.HasPrefix(arg, "-") && len(arg) > 1 && arg[1] != '-' {
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

// matchWrapper accepts argv of the shape `[flag…] [positional…] -- CMD [ARG…]`.
// Pre-`--` flags must match spec.Flags (GNU style). Pre-`--` positionals are
// accepted iff spec.AllowAnyPositional is true. The `--` separator is REQUIRED:
// without it (or with no command after it) we fall through, since bare wrapper
// invocations like `devenv shell` open an interactive shell whose safety cannot
// be statically classified. The tail after `--` is looked up in safeCommands
// and matched recursively, so the wrapper inherits the wrapped command's safety
// rules verbatim — it never loosens them.
func (s *commandSpec) matchWrapper(args []string) bool {
	i := 0
	for i < len(args) {
		arg := args[i]

		if arg == "--" {
			tail := args[i+1:]
			if len(tail) == 0 {
				return false // `devenv shell --` with no command
			}
			sub, ok := safeCommands[tail[0]]
			if !ok {
				return false
			}
			return sub.match(tail[1:])
		}

		// Long flag: `--name` or `--name=value`. Same dash-prefix rule as
		// matchGNU — `---foo` is data, not a flag.
		if strings.HasPrefix(arg, "--") && len(arg) > 2 && arg[2] != '-' {
			name, hasVal := arg[2:], false
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				name = name[:eq]
				hasVal = true
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
				i++
			}
			i++
			continue
		}

		// Short flag cluster. Same dash-prefix rule as matchGNU.
		if strings.HasPrefix(arg, "-") && len(arg) > 1 && arg[1] != '-' {
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
						// rest of cluster is the value
					} else {
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

		// Positional before `--`. Only allowed if the spec opts in.
		if !s.AllowAnyPositional {
			return false
		}
		i++
	}

	// No `--` encountered: wrapper invoked without an explicit command.
	return false
}

// matchXargs accepts argv of the shape `[flag…] CMD [INITIAL-ARG…]`. xargs's own
// flags (spec.Flags, GNU style) are parsed until the first non-flag token, which
// is the wrapped command name; the remainder are fixed initial-arguments. The
// command is looked up in xargsWrappable — a curated subset of safeCommands — and
// its initial-arguments classified recursively.
//
// The curated subset (not the full whitelist) is load-bearing: xargs appends
// stdin tokens to the wrapped argv, and those tokens are invisible here and are
// parsed by the wrapped program (including as flags). Recursing into a command
// that has any write path under some argv (e.g. `sort -o`, `git push`) would let
// stdin inject that path — a write the direct form would have prompted on. Only
// commands with no write path under ANY argv belong in xargsWrappable. The
// replace-mode flags `-I`/`-i`/`--replace` are deliberately absent from
// xargsSpec, so they fall through here as unknown flags.
func (s *commandSpec) matchXargs(args []string) bool {
	i := 0
	for i < len(args) {
		arg := args[i]

		// `--` ends xargs flag parsing; the next token is the wrapped command.
		if arg == "--" {
			return s.classifyWrapped(args[i+1:])
		}

		// Long flag: `--name` or `--name=value`. Same dash-prefix rule as matchGNU.
		if strings.HasPrefix(arg, "--") && len(arg) > 2 && arg[2] != '-' {
			name, hasVal := arg[2:], false
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				name = name[:eq]
				hasVal = true
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
				i++
			}
			i++
			continue
		}

		// Short flag cluster. Same dash-prefix rule as matchGNU.
		if strings.HasPrefix(arg, "-") && len(arg) > 1 && arg[1] != '-' {
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
						// rest of cluster is the value
					} else {
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

		// First non-flag token: the wrapped command.
		return s.classifyWrapped(args[i:])
	}

	// No wrapped command (bare `xargs`, or flags only). Bare xargs defaults to
	// running /bin/echo on stdin items; require an explicit command instead.
	return false
}

// classifyWrapped looks up tail[0] in the curated xargsWrappable set and, if
// present, recursively classifies the fixed initial-arguments tail[1:].
func (s *commandSpec) classifyWrapped(tail []string) bool {
	if len(tail) == 0 {
		return false
	}
	if !xargsWrappable[tail[0]] {
		return false
	}
	sub, ok := safeCommands[tail[0]]
	if !ok {
		// xargsWrappable keys are a subset of safeCommands; a miss means the two
		// have drifted out of sync. Be loud rather than silently mis-handle.
		failLoud("xargsWrappable command %q absent from safeCommands", tail[0])
		return false // unreachable
	}
	return sub.match(tail[1:])
}

// matchAwk parses argv of the shape `[flag…] PROGRAM [files…]`. Pre-program
// short flags from spec.Flags all take arguments (`-F sep`, `-v var=val`).
// The first non-flag positional is the awk program, validated by
// classifyAwkProgram. Long flags (`--name`) and short-without-arg flags
// fall through — gawk extensions and the `-f file` script-load form are
// deliberately out of scope for v1.
func (s *commandSpec) matchAwk(args []string) bool {
	i := 0
	for i < len(args) {
		arg := args[i]

		// `--` ends flag parsing; first remaining arg is the program.
		if arg == "--" {
			return s.matchAwkProgramAndFiles(args[i+1:])
		}

		// Long flags not supported in v1.
		if strings.HasPrefix(arg, "--") {
			return false
		}

		// Short flag: must be one of the listed flags, must take an arg.
		if strings.HasPrefix(arg, "-") && len(arg) > 1 {
			letter := string(arg[1])
			f, ok := s.findShort(letter)
			if !ok {
				return false
			}
			if !f.TakesArg {
				return false
			}
			if len(arg) > 2 {
				// `-Fsep` form — value attached to the flag token.
				i++
				continue
			}
			// `-F sep` form — value is the next argument.
			if i+1 >= len(args) {
				return false
			}
			i += 2
			continue
		}

		// First non-flag positional: the awk program.
		return s.matchAwkProgramAndFiles(args[i:])
	}
	// No program found.
	return false
}

// matchAwkProgramAndFiles takes the tail starting at the awk program.
// args[0] is the program source; args[1:] are input file paths. Files are
// accepted as-is (literalWords already rejected anything with expansion).
func (s *commandSpec) matchAwkProgramAndFiles(args []string) bool {
	if len(args) == 0 {
		return false
	}
	return classifyAwkProgram(args[0])
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
