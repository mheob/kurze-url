//go:build windows

package cli

import "os"

// fileOwnedByOther is always false on Windows: files there have no owner in
// the sense of a user id, and a profile belonging to the administrator is
// still one the person's own account may be allowed to change.
func fileOwnedByOther(os.FileInfo) bool {
	return false
}

// runningAsRoot is always false on Windows, which has no superuser id.
func runningAsRoot() bool {
	return false
}
