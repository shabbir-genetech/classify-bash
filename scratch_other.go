//go:build windows || plan9

package main

import "os"

// ownerLinks cannot read ownership here, so it reports failure and the
// scratchpad feature stays off: write redirects fall through as before.
func ownerLinks(os.FileInfo) (int, uint64, bool) {
	return 0, 0, false
}
