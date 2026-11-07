//go:build !windows

package cli

import (
	"os"
	"syscall"
)

// fileOwnedByOther reports whether the file belongs to a user other than the
// one running the process. The effective user is the one that counts: it is
// who a file created now would belong to, and under `sudo` it is root while
// the home directory and its files still belong to the person.
//
// Where the file system does not say who owns a file, the answer is no, so
// the check never refuses on a guess.
func fileOwnedByOther(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) != os.Geteuid()
}

// runningAsRoot reports whether the effective user is the superuser.
func runningAsRoot() bool {
	return os.Geteuid() == 0
}
