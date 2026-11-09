package engine

import "mvdan.cc/sh/v3/syntax"

// for loops over literal words (FUTURE-WORK §7). A loop over a fixed list whose
// body classifies safe for every value is "safe things, repeated". The values
// are literals in the command, so unlike `while read` nothing the classifier
// cannot see reaches the body. `while`, `until`, C-style `for ((…))`, `select`
// and `for x; do` (which iterates the positional parameters) stay refused.

// forValue is one loop value bash will use exactly as written: a word made of
// path-like characters only, so no glob, brace, tilde, whitespace, quote or
// expansion can make bash's value differ from the one the body was checked with.
var forValue = allOf(nonEmpty, runesIn("-_./:@%+,="))

// forLoop validates the one accepted shape, `for NAME in WORD…; do BODY; done`,
// and returns its variable and values. classifyFor and the triage blame share
// it, so the two can never disagree about which loops are in scope.
func forLoop(c *syntax.ForClause, sc *scope) (string, []string, bool) {
	if c.Select || c.Braces || len(c.Do) == 0 {
		return "", nil, false
	}
	it, ok := c.Loop.(*syntax.WordIter)
	if !ok || it.Name == nil || !it.InPos.IsValid() || len(it.Items) == 0 {
		return "", nil, false
	}
	if !bindableName(it.Name.Value, sc.host) {
		return "", nil, false
	}
	vals := make([]string, 0, len(it.Items))
	for _, w := range it.Items {
		v, ok := resolveWord(w, sc)
		if !ok || !forValue(v) {
			return "", nil, false
		}
		vals = append(vals, v)
	}
	return it.Name.Value, vals, true
}

// classifyFor accepts a loop forLoop validates when the body classifies safe with
// the variable bound to each value in turn. The body runs in the current shell,
// so a binding it made would hold afterwards, from whichever iteration made it
// last; rather than track that, a body that binds or rebinds ANY name — the loop
// variable included — is refused. Afterwards the variable holds the last value,
// as in bash (break and continue are not whitelisted, so every value runs).
func classifyFor(c *syntax.ForClause, sc *scope) bool {
	name, vals, ok := forLoop(c, sc)
	if !ok || onlyAssignments(c.Do) {
		return false
	}
	for _, v := range vals {
		if !forBodyOK(c, sc, name, v) {
			return false
		}
	}
	sc.vars[name] = vals[len(vals)-1]
	return true
}

// forBodyOK classifies one iteration and refuses a body that changed any binding.
func forBodyOK(c *syntax.ForClause, sc *scope, name, v string) bool {
	base := sc.child()
	base.vars[name] = v
	body := base.child()
	return classifyStmts(c.Do, body) && len(changedNames(base, body)) == 0
}
