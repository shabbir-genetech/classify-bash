package main

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

// classifyCommand parses cmd as bash and returns decisionAllow only when every
// statement, every nested construct, every redirect, every word, and every flag
// matches an entry in our strict whitelist. Any uncertainty returns
// decisionFallThrough. Unrecognized mvdan/sh AST node kinds call failLoud.
func classifyCommand(cmd string) decision {
	r := strings.NewReader(cmd)
	f, err := syntax.NewParser().Parse(r, "")
	if err != nil {
		// Parse failure is expected for unusual but legitimate bash. Fall through.
		return decisionFallThrough
	}
	if classifyFile(f) {
		return decisionAllow
	}
	return decisionFallThrough
}

func classifyFile(f *syntax.File) bool {
	if len(f.Stmts) == 0 {
		return false
	}
	for _, stmt := range f.Stmts {
		if !classifyStmt(stmt) {
			return false
		}
	}
	return true
}

func classifyStmt(stmt *syntax.Stmt) bool {
	if stmt.Background || stmt.Coprocess || stmt.Negated {
		return false
	}
	for _, r := range stmt.Redirs {
		if !safeRedirect(r) {
			return false
		}
	}
	return classifyCmd(stmt.Cmd)
}

func classifyCmd(cmd syntax.Command) bool {
	switch c := cmd.(type) {
	case *syntax.CallExpr:
		return classifyCall(c)
	case *syntax.BinaryCmd:
		// && and || — both sides must be safe. Pipelines are *syntax.BinaryCmd
		// with Op == Pipe in mvdan/sh.
		if !classifyStmt(c.X) {
			return false
		}
		return classifyStmt(c.Y)
	case *syntax.Subshell:
		// ( … ) — recurse into the inner statements. Each must classify safe
		// on its own; same rule as && / ||. Parens are just a scope boundary,
		// not a bypass. The empty-Stmts guard mirrors classifyFile.
		if len(c.Stmts) == 0 {
			return false
		}
		for _, s := range c.Stmts {
			if !classifyStmt(s) {
				return false
			}
		}
		return true
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

// classifyCall checks a single command invocation against the whitelist.
func classifyCall(c *syntax.CallExpr) bool {
	// Assignments-only statements (`FOO=bar`) parse as CallExpr with empty Args
	// and non-empty Assigns. We don't whitelist any of these — they mutate the
	// shell env in ways we'd rather not auto-allow.
	if len(c.Assigns) > 0 {
		return false
	}
	if len(c.Args) == 0 {
		return false
	}
	words, ok := literalWords(c.Args)
	if !ok {
		return false
	}
	spec, ok := safeCommands[words[0]]
	if !ok {
		return false
	}
	return spec.match(words[1:])
}

// safeRedirect returns true for redirects that cannot cause a write (or that
// write only to /dev/null, which is observationally inert).
func safeRedirect(r *syntax.Redirect) bool {
	if r == nil || r.Word == nil {
		return false
	}
	target, ok := wordLiteral(r.Word)
	if !ok {
		return false // e.g. `> $(...)` or `> $VAR`
	}
	switch r.Op {
	case syntax.RdrIn:
		// `< file` — pure read. Safe iff the source is a literal path.
		return true
	case syntax.RdrOut, syntax.AppOut, syntax.RdrAll, syntax.AppAll, syntax.ClbOut, syntax.RdrInOut:
		// Any write-capable redirect must target literal /dev/null.
		return target == "/dev/null"
	case syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
		// Heredocs only feed stdin to the command. The command itself still
		// needs to classify safe; the heredoc body is content, not a write.
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

// literalWords flattens a list of syntax.Word into plain strings, refusing any
// word that contains a non-literal part (variable expansion, command
// substitution, process substitution, arithmetic, extglob, etc.). Returns
// (nil, false) if any word is non-literal.
func literalWords(ws []*syntax.Word) ([]string, bool) {
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		s, ok := wordLiteral(w)
		if !ok {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
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
