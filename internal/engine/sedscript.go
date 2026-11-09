package engine

import "strings"

// sed scripts (FUTURE-WORK §4). sedScript is a fail-closed parser for the
// read-and-print subset agents actually send — line ranges, regex addresses,
// p/d/q/=, and substitution — and refuses everything else rather than modelling
// it. In the 2026-11-09 corpus that subset was about 98% of literal scripts.
//
// Accepted grammar (GNU sed 4.x):
//
//	script  := command { (';' | newline) command }   blanks allowed around both
//	command := [ addr [ ',' addr ] [ '!' ] ] ( 'p' | 'd' | '=' | ('q'|'Q') digits   (q/Q: one address at most)
//	                                           | 's' DELIM regex DELIM repl DELIM flags )
//	addr    := digits | '$' | '/' regex '/' [ 'I' ]
//	flags   := { 'g' | 'p' | 'I' | 'i' | digit }
//
// What it refuses, by kind:
//   - exec: the `e` command and the s///e flag;
//   - write: `w`, `W` and the s///w flag (a file named after them is opened even
//     when nothing is written);
//   - read another file: `r`, `R` (no logged demand);
//   - everything else (a, i, c, y, {}, labels and branches, n/N/h/H/g/G/x, l, F,
//     z, comments, `addr,+N`, `first~step`, `\cREGEXc`): no logged demand, and an
//     unrecognised construct is exactly where a misparse would hide one of the
//     above.
//
// The delimiter in a regex is literal inside a bracket expression, as in GNU sed
// (`s/[/]/X/` is one regex; `s/[/]/X/w f` really writes f), so the scanner tracks
// brackets; a backslash or newline inside one is refused rather than modelled.

// sedDelims are the s-command delimiters accepted: punctuation with no meaning in
// a regex or replacement that the scanner would have to reason about.
const sedDelims = "/|#,:@%"

func sedScript(s string) bool {
	p := &sedParser{s: s}
	return p.script()
}

type sedParser struct {
	s string
	i int
}

func (p *sedParser) eof() bool { return p.i >= len(p.s) }

// at reports whether the next byte is one of set.
func (p *sedParser) at(set string) bool {
	return !p.eof() && strings.IndexByte(set, p.s[p.i]) >= 0
}

func (p *sedParser) skip(set string) {
	for p.at(set) {
		p.i++
	}
}

const sedDigits = "0123456789"

func (p *sedParser) script() bool {
	commands := 0
	for {
		p.skip(" \t;\n")
		if p.eof() {
			return commands > 0
		}
		if !p.command() {
			return false
		}
		commands++
		p.skip(" \t")
		if !p.eof() && !p.at(";\n") {
			return false // anything else after a command: a flag we do not know
		}
	}
}

func (p *sedParser) command() bool {
	present, ok := p.address()
	if !ok {
		return false
	}
	ranged := false
	if present {
		p.skip(" \t")
		if p.at(",") {
			p.i++
			p.skip(" \t")
			if present, ok = p.address(); !present || !ok {
				return false
			}
			ranged = true
		}
		p.skip(" \t")
		if p.at("!") {
			p.i++
			p.skip(" \t")
		}
	}
	switch {
	case p.at("pd="):
		p.i++
		return true
	case p.at("qQ") && !ranged: // GNU: q and Q take one address at most
		p.i++
		p.skip(sedDigits)
		return true
	case p.at("s"):
		p.i++
		return p.substitute()
	}
	return false
}

// address reports whether an address is present and, if so, well formed.
func (p *sedParser) address() (present, ok bool) {
	switch {
	case p.at(sedDigits):
		p.skip(sedDigits)
		return true, true
	case p.at("$"):
		p.i++
		return true, true
	case p.at("/"):
		p.i++
		if !p.delimited('/', true) {
			return true, false
		}
		if p.at("I") {
			p.i++
		}
		return true, true
	}
	return false, true
}

func (p *sedParser) substitute() bool {
	if !p.at(sedDelims) {
		return false
	}
	delim := p.s[p.i]
	p.i++
	if !p.delimited(delim, true) || !p.delimited(delim, false) {
		return false
	}
	p.skip("gpIi" + sedDigits)
	return true
}

// delimited consumes text up to and including an unescaped delim: a regex
// (brackets true) or a replacement. A newline, or a backslash with nothing after
// it, is refused.
func (p *sedParser) delimited(delim byte, brackets bool) bool {
	for !p.eof() {
		c := p.s[p.i]
		switch {
		case c == '\n':
			return false
		case c == '\\':
			p.i++
			if p.eof() || p.s[p.i] == '\n' {
				return false
			}
			p.i++
		case brackets && c == '[':
			if !p.bracket() {
				return false
			}
		case c == delim:
			p.i++
			return true
		default:
			p.i++
		}
	}
	return false
}

// bracket consumes a bracket expression: an optional `^`, a literal leading `]`,
// then up to the closing `]`, skipping `[:class:]`, `[.coll.]` and `[=equiv=]`.
func (p *sedParser) bracket() bool {
	p.i++ // '['
	if p.at("^") {
		p.i++
	}
	if p.at("]") {
		p.i++
	}
	for !p.eof() {
		c := p.s[p.i]
		switch {
		case c == ']':
			p.i++
			return true
		case c == '\\' || c == '\n':
			return false
		case c == '[' && p.i+1 < len(p.s) && strings.IndexByte(":.=", p.s[p.i+1]) >= 0:
			closer := string(p.s[p.i+1]) + "]"
			end := strings.Index(p.s[p.i+2:], closer)
			if end < 0 {
				return false
			}
			p.i += 2 + end + len(closer)
		default:
			p.i++
		}
	}
	return false
}
