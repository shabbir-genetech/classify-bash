//go:build !windows && !plan9

package main

import (
	"os"
	"syscall"
)

// ownerLinks returns a file's owning uid and hard-link count.
func ownerLinks(fi os.FileInfo) (uid int, links uint64, ok bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return int(st.Uid), uint64(st.Nlink), true
}
