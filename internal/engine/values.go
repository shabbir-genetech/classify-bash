package engine

import (
	"net/url"
	"strings"
)

// Composable checks for flagSpec.Value and commandSpec.Positional. Each returns
// or is a func(string) bool, so a spec's check is built by composing these
// rather than written out by hand; ghvalues.go is the first user. Write a check
// positively (the shapes known to be safe) and let anything else fail.

// allOf accepts v only if every check accepts it.
func allOf(checks ...func(string) bool) func(string) bool {
	return func(v string) bool {
		for _, c := range checks {
			if !c(v) {
				return false
			}
		}
		return true
	}
}

// nonEmpty rejects the empty string.
func nonEmpty(v string) bool { return v != "" }

// runesIn accepts a string made only of ASCII letters, digits, and the runes in
// extra. The empty string passes; compose with nonEmpty where that matters.
func runesIn(extra string) func(string) bool {
	return func(v string) bool {
		for _, r := range v {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
				strings.ContainsRune(extra, r)) {
				return false
			}
		}
		return true
	}
}

// allDigits accepts a non-empty run of ASCII digits (a count, a limit).
func allDigits(v string) bool {
	for _, r := range v {
		if r < '0' || r > '9' {
			return false
		}
	}
	return v != ""
}

// oneOf accepts exactly one of the given words (an enumerated option value).
func oneOf(words ...string) func(string) bool {
	return func(v string) bool {
		for _, w := range words {
			if v == w {
				return true
			}
		}
		return false
	}
}

// whenValue is the common auto|always|never option value (--color, --hyperlink).
var whenValue = oneOf("auto", "always", "never")

// notURL accepts a value that net/url parses as a bare path reference: no
// scheme, host, userinfo or opaque part. A value that does not parse is refused.
// It answers "could a tool read this as a URL?" with the parser such tools use,
// not by scanning for characters.
func notURL(v string) bool {
	u, err := url.Parse(v)
	return err == nil && u.Scheme == "" && u.Host == "" && u.User == nil && u.Opaque == ""
}
