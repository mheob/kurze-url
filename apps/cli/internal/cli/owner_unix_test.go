//go:build !windows

package cli

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// fakeInfo is a file whose file system reports no owner.
type fakeInfo struct{ os.FileInfo }

func (fakeInfo) Sys() any { return nil }

func TestFileOwnedByOtherIsFalseForTheProcessOwnFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rc")
	require.NoError(t, os.WriteFile(path, nil, 0o644))
	info, err := os.Stat(path)
	require.NoError(t, err)

	require.False(t, fileOwnedByOther(info))
}

func TestFileOwnedByOtherDoesNotGuessWithoutAnOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rc")
	require.NoError(t, os.WriteFile(path, nil, 0o644))
	info, err := os.Stat(path)
	require.NoError(t, err)

	require.False(t, fileOwnedByOther(fakeInfo{info}))
}

// ownedBy is a file the file system says belongs to the given user.
type ownedBy struct {
	os.FileInfo
	uid uint32
}

func (o ownedBy) Sys() any { return &syscall.Stat_t{Uid: o.uid} }

// The true answer needs a file of a second user, which a test cannot make, so
// the file system's report is faked instead.
func TestFileOwnedByOtherIsTrueForAnotherUsersFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rc")
	require.NoError(t, os.WriteFile(path, nil, 0o644))
	info, err := os.Stat(path)
	require.NoError(t, err)

	require.True(t, fileOwnedByOther(ownedBy{info, uint32(os.Geteuid()) + 1}))
	require.False(t, fileOwnedByOther(ownedBy{info, uint32(os.Geteuid())}))
}
