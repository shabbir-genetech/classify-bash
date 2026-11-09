package engine

import (
	"net/url"
	"strings"
)

// Value checks for gh arguments that can name a host (flagSpec.Value,
// commandSpec.Positional).
//
// gh sends its credentials to whatever host a repository, PR or endpoint
// argument names. Verified 2026-11-09 (gh 2.102, GH_DEBUG=api, a fake
// GH_ENTERPRISE_TOKEN, an unresolvable .invalid host): `HOST/o/r`,
// `git@HOST:o/r`, and an `http(s)://` URL in any letter case all sent
// `Authorization: token …` to HOST. `HOST:o/r`, a scheme-less
// `HOST/o/r/pull/1` PR selector, and an endpoint `//HOST/x` stayed on
// github.com. Without a token these are still outbound requests to a
// caller-chosen host.
//
// Each check parses the value and accepts only a structure that cannot name a
// host. A repository is parsed as its two segments; for the rest the question is
// "is this a URL?", answered by net/url — the parser gh itself uses — and a value
// that does not parse is rejected. Defensively, a value must ALSO stay inside a
// positive character set, so a form neither check anticipated (a %-escape, a
// backslash, `@`, a control character) is refused rather than reasoned about. A
// legitimate endpoint outside the set costs a prompt; that is the intended trade.

// ghRepoName accepts exactly OWNER/REPO: two name segments and one slash.
func ghRepoName(v string) bool {
	owner, repo, ok := strings.Cut(v, "/")
	return ok && ghNameSegment(owner) && ghNameSegment(repo)
}

func ghNameSegment(s string) bool {
	return s != "" && onlyRunes(s, "-_.")
}

// onlyRunes reports whether every rune of s is an ASCII letter, a digit, or one
// of extra.
func onlyRunes(s, extra string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			strings.ContainsRune(extra, r)) {
			return false
		}
	}
	return true
}

// notURL reports whether v parses as a URL reference with no scheme, host,
// userinfo or opaque part — a bare path. `//HOST/x` is refused though gh keeps
// it on github.com: a parsed host is reason enough.
func notURL(v string) bool {
	u, err := url.Parse(v)
	return err == nil && u.Scheme == "" && u.Host == "" && u.User == nil && u.Opaque == ""
}

// ghEndpoint accepts a `gh api` endpoint that is a path (or `graphql`), with an
// optional query: path characters, gh's `{owner}`-style placeholders, and
// `?`/`=`/`&`/`,` for the query.
func ghEndpoint(v string) bool {
	return v != "" && onlyRunes(v, "-_./{}?=&,+~") && notURL(v)
}

// ghPRSelector accepts a PR number (`123`, `#123`) or a branch name.
func ghPRSelector(v string) bool {
	return v != "" && onlyRunes(v, "-_./#+") && notURL(v)
}
