package engine

import (
	"strings"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// Literal-bound variables (FUTURE-WORK §6). A statement made only of literal
// assignments (`S=/tmp/x`, `A=1 B="$A/y"`) is accepted and binds each name in
// the walk's scope; a later `$S` / `"$S"` / `${S}` is then replaced by its
// literal, so every downstream check — the spec, the redirect rules — sees the
// exact string bash would. The binding is only as safe as two properties, both
// enforced here:
//
//   - The value is exact. Nothing in the right-hand side may be interpreted by
//     bash at assignment time (no command/arithmetic substitution, no unbound
//     variable, no tilde, no backslash escape, no $'…' or $"…"), and an
//     UNQUOTED use may not word-split or glob (unquotedSafe).
//   - The assignment has no effect beyond the binding. If the name is exported,
//     assigning it changes the environment of every later child process —
//     `HOME=… ; git log` reads another ~/.gitconfig, whose pager is a program.
//     So only short scratch names are bindable (bindableName): 1–3 characters,
//     a few shell-meaningful ones reserved, and never a name the Host reports
//     as set in the environment the command will run in. With no Host, every
//     name counts as set, so nothing binds.
//
// Nothing else in the whitelist can assign a variable: printf has no -v,
// read/mapfile/declare/export/eval are not whitelisted, and `{name}>` redirects
// are rejected in safeRedirect. So the scope is exact for every command that
// can classify Allow.

// reservedNames are short names bash or common tools give meaning to.
var reservedNames = map[string]bool{
	"IFS": true, "ENV": true, "PWD": true, "UID": true,
	"PS1": true, "PS2": true, "PS3": true, "PS4": true,
	"TMP": true, "TZ": true,
}

// bindableName is a positive rule, not a deny-list: what agents actually bind
// is overwhelmingly one to three characters (S, D, OUT, SP, W, …), and the
// names that steer programs (PATH, HOME, PAGER, GIT_*, LD_*) are all longer.
func bindableName(n string, h Host) bool {
	if len(n) < 1 || len(n) > 3 || n == "_" || reservedNames[n] {
		return false
	}
	for i, r := range n {
		switch {
		case r == '_', r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	if h.EnvSet(n) {
		return false
	}
	return true
}

// bindAssigns accepts an assignment-only statement and binds it into sc. bash
// evaluates the assignments left to right, so `A=1 B="$A"` binds B to "1".
func bindAssigns(st *syntax.Stmt, call *syntax.CallExpr, sc *scope) bool {
	if st.Background || st.Coprocess || st.Negated || len(st.Redirs) > 0 {
		return false
	}
	for _, a := range call.Assigns {
		if a.Append || a.Naked || a.Index != nil || a.Array != nil || a.Name == nil {
			return false
		}
		if !bindableName(a.Name.Value, sc.host) {
			return false
		}
		v := ""
		if a.Value != nil {
			var ok bool
			if v, ok = rhsValue(a.Value, sc); !ok {
				return false
			}
		}
		sc.vars[a.Name.Value] = v
	}
	return true
}

// boundParam returns the literal a plain `$NAME` / `${NAME}` expands to, if NAME
// is bound in sc. Every operator form (${#a}, ${a:-b}, ${a/x/y}, ${!a}, …) is
// rejected: some assign, and all of them change the value.
func boundParam(p *syntax.ParamExp, sc *scope) (string, bool) {
	if p == nil || p.Param == nil || p.Excl || p.Length || p.Width ||
		p.Index != nil || p.Slice != nil || p.Repl != nil || p.Names != 0 || p.Exp != nil {
		return "", false
	}
	v, ok := sc.vars[p.Param.Value]
	return v, ok
}

// exitStatusParam reports whether p is a plain `$?` / `${?}`: the previous
// command's exit status, always a decimal 0–255 that the classifier cannot see.
// Its only use is as an OPAQUE operand (wordQuotedSubst), the channel a quoted
// `$(…)` already uses: a positional of an ArgvDataSafe command, nothing else.
// Every field of ParamExp is checked, so every operator form is refused —
// `${?:-$(cmd)}` and `${?/x/$(cmd)}` would RUN cmd.
func exitStatusParam(p *syntax.ParamExp) bool {
	return p != nil && p.Param != nil && p.Param.Value == "?" &&
		!p.Excl && !p.Length && !p.Width &&
		p.Index == nil && p.Slice == nil && p.Repl == nil && p.Names == 0 && p.Exp == nil
}

// unquotedSafe reports whether an unquoted expansion of v yields exactly one
// word equal to v: non-empty (an empty unquoted expansion removes the word),
// no IFS whitespace (no splitting) and no glob or extglob opener (no pathname
// expansion).
func unquotedSafe(v string) bool {
	return v != "" && !strings.ContainsAny(v, " \t\n*?[(")
}

// rhsValue resolves an assignment's right-hand side. Assignment values are not
// word-split or globbed, so an unquoted bound $VAR is safe whatever it holds.
func rhsValue(w *syntax.Word, sc *scope) (string, bool) {
	var b strings.Builder
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			// `~` is tilde-expanded in an assignment; a backslash is an escape.
			if strings.ContainsAny(p.Value, `\~`) {
				return "", false
			}
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			if p.Dollar { // $'…' interprets escapes
				return "", false
			}
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			if !dblQuotedInto(&b, p, sc) {
				return "", false
			}
		case *syntax.ParamExp:
			v, ok := boundParam(p, sc)
			if !ok {
				return "", false
			}
			b.WriteString(v)
		default:
			return "", false
		}
	}
	return b.String(), true
}

// dblQuotedInto appends a double-quoted part made of plain text and bound
// variables. $"…" (locale translation) and backslash escapes are rejected.
func dblQuotedInto(b *strings.Builder, p *syntax.DblQuoted, sc *scope) bool {
	if p.Dollar {
		return false
	}
	for _, inner := range p.Parts {
		switch ip := inner.(type) {
		case *syntax.Lit:
			if strings.Contains(ip.Value, `\`) {
				return false
			}
			b.WriteString(ip.Value)
		case *syntax.ParamExp:
			v, ok := boundParam(ip, sc)
			if !ok {
				return false
			}
			b.WriteString(v)
		default:
			return false
		}
	}
	return true
}

// resolveWord is the value bash gives a word whose only expansions are `$NAME` of
// names bound in sc — and wordLiteral, with no bindings, is the same function.
//
// It returns the word's VALUE — what bash passes after quote removal, escapes and
// brace expansion — computed by mvdan/sh's own expander rather than by hand.
// Taking the source text instead let `sort $'\x2do' x f`, `sort \-o x f` and
// `sort {-o,x} f` classify as plain positionals while bash ran `sort -o x f`, a
// write (fixed 2026-11-09). Two steps, both fail-closed:
//
//  1. Structure: only literal text, `'…'`/`$'…'` quotes, double quotes holding
//     text and bound variables, and unquoted bound variables that cannot split
//     or glob. `$"…"` is refused: a translation catalog could change its value.
//  2. Value: expand.Fields over the whole word (a brace can span quotes), with
//     exactly the scope's bindings as the environment and NoUnset, so nothing
//     unbound can expand to a silent empty string.
//
// resolveWord wants exactly ONE field (a command name, a redirect target, a flag
// value); argTokens takes every field of expandWord as its own argument, so
// `stat d/{a,b}.md` checks two operands and `sort {-o,x} f` meets sort's spec as
// `-o x f` and is refused there. Glob characters and a leading `~` stay as
// written, as before: the expander has no filesystem or HOME here, and those
// reach a spec as plain operands.
func resolveWord(w *syntax.Word, sc *scope) (string, bool) {
	fields, ok := expandWord(w, sc)
	if !ok || len(fields) != 1 {
		return "", false
	}
	return fields[0], true
}

// expandWord returns the arguments bash makes of w (see resolveWord).
func expandWord(w *syntax.Word, sc *scope) ([]string, bool) {
	if w == nil || !resolvableParts(w.Parts, sc, false) {
		return nil, false
	}
	pairs := make([]string, 0, len(sc.vars))
	for k, v := range sc.vars {
		pairs = append(pairs, k+"="+v)
	}
	cfg := &expand.Config{Env: expand.ListEnviron(pairs...), NoUnset: true}
	fields, err := expand.Fields(cfg, w)
	if err != nil || len(fields) == 0 {
		return nil, false
	}
	return fields, true
}

// resolvableParts is resolveWord's structural check. quoted reports whether the
// parts sit inside double quotes, where a bound value is not split or globbed.
func resolvableParts(parts []syntax.WordPart, sc *scope, quoted bool) bool {
	for _, part := range parts {
		switch p := part.(type) {
		case *syntax.Lit:
		case *syntax.SglQuoted:
			if quoted {
				return false // not reachable from the parser; refused regardless
			}
		case *syntax.DblQuoted:
			if quoted || p.Dollar || !resolvableParts(p.Parts, sc, true) {
				return false
			}
		case *syntax.ParamExp:
			v, ok := boundParam(p, sc)
			if !ok || (!quoted && !unquotedSafe(v)) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// exactPath resolves a redirect target to the path bash will open, refusing
// anything whose text and meaning could differ: unquoted glob, brace, tilde,
// whitespace or backslash; $'…' / $"…"; escapes inside double quotes. Used
// where a prefix check on the result is the security boundary (§10).
func exactPath(w *syntax.Word, sc *scope) (string, bool) {
	if w == nil {
		return "", false
	}
	var b strings.Builder
	for _, part := range w.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			if strings.ContainsAny(p.Value, "\\*?[]{}~ \t\n(") {
				return "", false
			}
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			if p.Dollar {
				return "", false
			}
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			if !dblQuotedInto(&b, p, sc) {
				return "", false
			}
		case *syntax.ParamExp:
			v, ok := boundParam(p, sc)
			if !ok || !unquotedSafe(v) {
				return "", false
			}
			b.WriteString(v)
		default:
			return "", false
		}
	}
	return b.String(), true
}
