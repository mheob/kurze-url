//go:build !windows

package cli

import (
	"os"
	"path/filepath"
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
