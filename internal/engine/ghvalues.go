package engine

import "strings"

// Value checks for gh arguments that can name a host, composed from values.go.
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
// host: a repository as its two segments, the rest by notURL. Defensively, a
// value must ALSO stay inside a positive character set, so a form neither check
// anticipated (a %-escape, a backslash, `@`, a control character) is refused
// rather than reasoned about. A legitimate value outside the set costs a prompt;
// that is the intended trade.

// ghNameSegment is one OWNER or REPO segment.
var ghNameSegment = allOf(nonEmpty, runesIn("-_."))

// ghRepoName accepts exactly OWNER/REPO: two name segments and one slash.
func ghRepoName(v string) bool {
	owner, repo, ok := strings.Cut(v, "/")
	return ok && ghNameSegment(owner) && ghNameSegment(repo)
}

// ghEndpoint accepts a `gh api` endpoint that is a path (or `graphql`), with an
// optional query: path characters, gh's `{owner}`-style placeholders, and
// `?`/`=`/`&`/`,` for the query.
var ghEndpoint = allOf(nonEmpty, runesIn("-_./{}?=&,+~"), notURL)

// ghPRSelector accepts a PR number (`123`, `#123`) or a branch name.
var ghPRSelector = allOf(nonEmpty, runesIn("-_./#+"), notURL)
