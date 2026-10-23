package engine

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// decision is the classifier's outcome for one command.
type decision int

const (
	decisionFallThrough decision = iota // exit 0 no JSON; user gets the normal prompt
	decisionAllow                       // emit allow JSON
)

// scope is the shell state the classifier tracks while it walks one command:
// the variables bound to a literal earlier in the same command (FUTURE-WORK §6),
// and the Host, which answers what the engine may not look up itself (is a
// name set in the environment, is there a scratchpad, §10). A bound `$VAR` is
// replaced by its literal, so every later check sees exactly the string bash
// would.
type scope struct {
	vars map[string]string
	host Host
}

func newScope(h Host) *scope {
	return &scope{vars: map[string]string{}, host: h}
}

// child copies the bindings for a construct bash runs in a subshell or runs
// conditionally: it sees what is bound now, and nothing it binds escapes.
func (s *scope) child() *scope {
	c := &scope{vars: make(map[string]string, len(s.vars)), host: s.host}
	for k, v := range s.vars {
		c.vars[k] = v
	}
	return c
}

// classifyCommandIn parses cmd as bash and returns decisionAllow only when every
// statement, every nested construct, every redirect, every word, and every flag
// matches an entry in our strict whitelist. Any uncertainty returns
// decisionFallThrough. Unrecognized mvdan/sh AST node kinds call failLoud.
// h must be non-nil (ClassifyWith substitutes noHost).
func classifyCommandIn(cmd string, h Host) decision {
	r := strings.NewReader(cmd)
	f, err := syntax.NewParser().Parse(r, "")
	if err != nil {
		// Parse failure is expected for unusual but legitimate bash. Fall through.
		return decisionFallThrough
	}
	// A command made only of assignments has nothing to accelerate, and in a
	// harness whose shell persists between calls it is a state change that
	// outlives this one. Assignments are accepted only as setup for a command.
	if onlyAssignments(f.Stmts) {
		return decisionFallThrough
	}
	if classifyStmts(f.Stmts, newScope(h)) {
		return decisionAllow
	}
	return decisionFallThrough
}

func onlyAssignments(stmts []*syntax.Stmt) bool {
	for _, st := range stmts {
		call, ok := st.Cmd.(*syntax.CallExpr)
		if !ok || len(call.Args) > 0 || len(call.Assigns) == 0 {
			return false
		}
	}
	return true
}

// classifyStmts walks a statement list in order. bash runs a list sequentially
// and unconditionally, so a literal assignment binds for every later statement.
func classifyStmts(stmts []*syntax.Stmt, sc *scope) bool {
	if len(stmts) == 0 {
		return false
	}
	for _, st := range stmts {
		if !classifyStmtBind(st, sc) {
			return false
		}
	}
	return true
}

// classifyStmtBind accepts an assignment-only statement by binding it into sc,
// and hands every other statement to classifyStmt.
func classifyStmtBind(st *syntax.Stmt, sc *scope) bool {
	if call, ok := st.Cmd.(*syntax.CallExpr); ok && len(call.Args) == 0 && len(call.Assigns) > 0 {
		return bindAssigns(st, call, sc)
	}
	return classifyStmt(st, sc)
}

func classifyStmt(stmt *syntax.Stmt, sc *scope) bool {
	if stmt.Background || stmt.Coprocess || stmt.Negated {
		return false
	}
	for _, r := range stmt.Redirs {
		if !safeRedirect(r, sc) {
			return false
		}
	}
	return classifyCmd(stmt.Cmd, sc)
}

func classifyCmd(cmd syntax.Command, sc *scope) bool {
	switch c := cmd.(type) {
	case *syntax.CallExpr:
		return classifyCall(c, sc)
	case *syntax.BinaryCmd:
		// Both sides must be safe. What differs is which bindings each side sees.
		switch c.Op {
		case syntax.AndStmt:
			// The right side runs only if everything to its left succeeded, so
			// bindings flow along an && chain. They stop at the end of it: if an
			// earlier link failed, a later assignment never ran.
			chain := sc.child()
			return classifyAndChain(c.X, chain) && classifyAndChain(c.Y, chain)
		case syntax.OrStmt, syntax.Pipe, syntax.PipeAll:
			// || runs its right side only when the left FAILED, and each pipeline
			// stage is its own subshell, so neither side sees the other's bindings.
			return classifyStmtBind(c.X, sc.child()) && classifyStmtBind(c.Y, sc.child())
		default:
			failLoud("unknown binary op: %v", c.Op)
			return false // unreachable
		}
	case *syntax.Subshell:
		// ( … ) — recurse into the inner statements. Each must classify safe
		// on its own; same rule as && / ||. Parens are just a scope boundary,
		// not a bypass: bindings come in, none go out.
		return classifyStmts(c.Stmts, sc.child())
	case *syntax.Block,
		*syntax.IfClause, *syntax.ForClause, *syntax.WhileClause, *syntax.CaseClause,
		*syntax.FuncDecl, *syntax.LetClause, *syntax.TimeClause, *syntax.CoprocClause,
		*syntax.TestClause, *syntax.DeclClause, *syntax.ArithmCmd:
		// Out of scope for v1: too easy to hide writes inside these.
		return false
	case nil:
		// A bare redirect like `> file` parses as a Stmt with nil Cmd. Treat as
		// fall-through (it's a write).
		return false
	default:
		// Any new AST node kind from a future mvdan/sh release lands here.
		// Fail loud so we notice and extend the classifier rather than
		// silently treating it as unsafe.
		failLoud("unknown command kind: %T", c)
		return false // unreachable
	}
}

// classifyAndChain classifies one link of an && chain in the chain's own scope,
// flattening nested && so `A=1 && B=2 && cmd "$A$B"` sees both bindings
// (mvdan/sh parses it left-associatively, as ((A=1 && B=2) && cmd)).
func classifyAndChain(st *syntax.Stmt, chain *scope) bool {
	if b, ok := st.Cmd.(*syntax.BinaryCmd); ok && b.Op == syntax.AndStmt &&
		len(st.Redirs) == 0 && !st.Background && !st.Coprocess && !st.Negated {
		return classifyAndChain(b.X, chain) && classifyAndChain(b.Y, chain)
	}
	return classifyStmtBind(st, chain)
}

// classifyCall checks a single command invocation against the whitelist.
func classifyCall(c *syntax.CallExpr, sc *scope) bool {
	// An env-prefix assignment (`FOO=bar cmd`) exports FOO to cmd alone, which
	// is how a pager, editor or loader gets swapped in. Never allowed. A
	// statement that is ONLY assignments is handled by bindAssigns instead.
	if len(c.Assigns) > 0 {
		return false
	}
	if len(c.Args) == 0 {
		return false
	}
	// The command name must be a literal so we know which spec applies; a
	// substituted name like `$(echo ls)`, or even a bound `$VAR`, is rejected.
	name, ok := wordLiteral(c.Args[0])
	if !ok {
		return false
	}
	spec, ok := safeCommands[name]
	if !ok {
		return false
	}
	toks, ok := argTokens(c.Args[1:], sc)
	if !ok {
		return false
	}
	return spec.match(toks)
}

// safeRedirect returns true for redirects that cannot cause a write, that
// write only to /dev/null (observationally inert), or that write a data file
// in the session's scratchpad (§10, see scratchWriteOK).
func safeRedirect(r *syntax.Redirect, sc *scope) bool {
	if r == nil || r.Word == nil {
		return false
	}
	// `{name}>file` makes bash ASSIGN the new descriptor number to name. That
	// would silently rebind a variable this walk tracks as a literal.
	if r.N != nil && strings.HasPrefix(r.N.Value, "{") {
		return false
	}
	target, ok := resolveWord(r.Word, sc)
	if !ok {
		return false // e.g. `> $(...)` or an unbound `> $VAR`
	}
	switch r.Op {
	case syntax.RdrIn:
		// `< file` — pure read. Safe iff the source is a literal path.
		return true
	case syntax.RdrOut, syntax.AppOut, syntax.RdrAll, syntax.AppAll, syntax.ClbOut:
		if target == "/dev/null" {
			return true
		}
		// The scratchpad check needs the path bash will really open, so it
		// re-resolves the word under the stricter exactPath rules.
		path, ok := exactPath(r.Word, sc)
		return ok && scratchWriteOK(path, sc.host)
	case syntax.RdrInOut:
		// `<>` opens read-write without truncating; nothing needs it.
		return target == "/dev/null"
	case syntax.Hdoc, syntax.DashHdoc:
		// A heredoc feeds stdin to the command, which must still classify safe on
		// its own. Its BODY, though, is a word: with an unquoted delimiter bash
		// expands it, so `cat <<EOF` around `$(cmd)` or a backquote runs cmd.
		// mvdan/sh leaves a quoted-delimiter body (<<'EOF', <<"EOF", <<\EOF)
		// unparsed as a single Lit, so "every part is a Lit" is exactly "bash
		// will not expand this body". Anything else — even a plain $VAR, which
		// could be ${x:=…} or $((…)) — falls through.
		return hdocBodyLiteral(r.Hdoc)
	case syntax.WordHdoc:
		// Here-string `<<< word`: the word was already required to be literal
		// above, so there is no hidden expansion.
		return true
	case syntax.DplIn, syntax.DplOut:
		// Duplicate-fd redirects (`2>&1`, `<&3`). These don't open new files,
		// so they're inert from a write-safety perspective.
		return true
	default:
		// Unknown redirect op (future mvdan/sh release). Be loud.
		failLoud("unknown redirect op: %v", r.Op)
		return false // unreachable
	}
}

// hdocBodyLiteral reports whether a heredoc body contains no expansion at all.
// A nil body (an empty heredoc) is literal.
func hdocBodyLiteral(body *syntax.Word) bool {
	if body == nil {
		return true
	}
	for _, part := range body.Parts {
		if _, ok := part.(*syntax.Lit); !ok {
			return false
		}
	}
	return true
}

// argTokens classifies each operand word as either a literal value or an opaque
// "substituted" operand — a quoted `$(...)` whose inner command classifies
// read-only safe. A `$VAR` bound to a literal earlier in the command counts as
// that literal (resolveWord). Returns (nil, false) if any word is neither: an
// unbound `$VAR`, an unquoted `$(...)`, a process substitution, arithmetic, an
// extglob, or a quoted substitution whose inner command is not safe. The
// substituted bit travels with the token into the matchers, which accept it only
// as a positional and only for an ArgvDataSafe spec.
func argTokens(ws []*syntax.Word, sc *scope) ([]argToken, bool) {
	out := make([]argToken, 0, len(ws))
	for _, w := range ws {
		if s, ok := resolveWord(w, sc); ok {
			out = append(out, argToken{lit: s})
			continue
		}
		if wordQuotedSubst(w, sc) {
			out = append(out, argToken{subst: true})
			continue
		}
		return nil, false
	}
	return out, true
}

// wordQuotedSubst reports whether w is a "quoted-substitution" word: literal text
// plus one or more `$(...)` command substitutions that appear ONLY inside double
// quotes, where every substitution's inner command classifies read-only safe. A
// bare (unquoted) `$(...)`, or any `$VAR` / `$((...))` / `<(...)` / extglob
// anywhere, makes it not a quoted-substitution word. Requiring double quotes pins
// the expansion to a single argv operand (no word-splitting, no globbing), so it
// can never inject extra argv words.
func wordQuotedSubst(w *syntax.Word, sc *scope) bool {
	if w == nil {
		return false
	}
	sawSubst := false
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit, *syntax.SglQuoted:
			// Literal text outside/inside single quotes — fine.
		case *syntax.DblQuoted:
			for _, inner := range p.Parts {
				switch ip := inner.(type) {
				case *syntax.Lit:
					// Literal text within the quotes — fine.
				case *syntax.ParamExp:
					// A bound variable is literal text here; the token is opaque
					// anyway, so its value does not matter, only that it is bound.
					if _, ok := boundParam(ip, sc); !ok {
						return false
					}
				case *syntax.CmdSubst:
					// A substitution is a subshell: it sees our bindings, keeps its own.
					if !classifyCmdSubst(ip, sc.child()) {
						return false
					}
					sawSubst = true
				default:
					// Unbound $VAR, ArithmExp, ProcSubst, nested quotes, … reject.
					return false
				}
			}
		default:
			// Bare (unquoted) CmdSubst, ParamExp, ArithmExp, ProcSubst, ExtGlob.
			return false
		}
	}
	return sawSubst
}

// classifyCmdSubst reports whether a `$(...)` command substitution is read-only
// safe: every inner statement must itself classify safe — the same recursion as a
// pipe stage or subshell. The mksh `${ …;}` / `${|…;}` forms are rejected.
func classifyCmdSubst(cs *syntax.CmdSubst, sc *scope) bool {
	if cs == nil || len(cs.Stmts) == 0 {
		return false
	}
	if cs.TempFile || cs.ReplyVar {
		return false
	}
	return classifyStmts(cs.Stmts, sc)
}

// wordLiteral returns the literal string value of a Word if and only if every
// part is a Lit, a SglQuoted (always literal), or a DblQuoted whose own parts
// are all Lit. Anything else (ParamExp, CmdSubst, ArithmExp, ProcSubst,
// ExtGlob) makes the word unclassifiable for our purposes.
func wordLiteral(w *syntax.Word) (string, bool) {
	if w == nil {
		return "", false
	}
	var b strings.Builder
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, inner := range p.Parts {
				lit, ok := inner.(*syntax.Lit)
				if !ok {
					return "", false
				}
				b.WriteString(lit.Value)
			}
		default:
			// ParamExp ($VAR), CmdSubst ($(...)), ArithmExp ($((...))),
			// ProcSubst (<(...)), ExtGlob, etc. all land here.
			return "", false
		}
	}
	return b.String(), true
}
