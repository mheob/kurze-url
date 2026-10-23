package credentials

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Modes of the fallback file and its directory. The directory is normally the
// one the config file lives in, which the config package creates with the
// same mode; whichever package gets there first sets it.
const (
	dirMode  fs.FileMode = 0o700
	fileMode fs.FileMode = 0o600
)

// tempPattern names the temporary file written beside the real one. The
// leading dot keeps it out of casual listings, and the suffix makes it
// recognisable to a person who finds one after a crash.
const tempPattern = ".credentials-*.tmp"

// readFile loads the tokens from the file at path. A file that does not exist
// is ErrNotFound; a file that cannot be read or decoded is an error of its
// own, not "not logged in", because the person has a file they would not
// expect `login` to be needed for.
func readFile(path string) (Tokens, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Tokens{}, ErrNotFound
	}
	if err != nil {
		return Tokens{}, fmt.Errorf("read the credentials file: %w", err)
	}
	tokens, err := decode(data)
	if err != nil {
		return Tokens{}, fmt.Errorf("credentials file %s: %w", path, err)
	}
	return tokens, nil
}

// writeFile replaces the file at path with data, atomically and privately.
//
// The data goes into a temporary file in the same directory, is synced, and
// is renamed over the target, so a crash or a second process refreshing at
// the same time leaves the old document or the new one and never half of
// either. The temporary file is created 0600 and chmodded to 0600 again
// because a restrictive umask can only narrow the first, not make it exact.
// Every failure after the temporary file exists removes it, so no token is
// left lying beside the real file.
func writeFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("create the credentials directory: %w", err)
	}

	tmp, err := os.CreateTemp(dir, tempPattern)
	if err != nil {
		return fmt.Errorf("create a temporary credentials file: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			// Close first: Windows will not remove an open file. The error of
			// a second Close after a successful one is of no interest.
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()

	if err := tmp.Chmod(fileMode); err != nil {
		return fmt.Errorf("restrict the temporary credentials file: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write the temporary credentials file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync the temporary credentials file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close the temporary credentials file: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("replace the credentials file: %w", err)
	}
	committed = true
	return nil
}

// removeFile deletes the file at path. A file that is not there is the
// expected state, not an error.
func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
