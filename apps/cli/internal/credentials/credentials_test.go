package credentials_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"

	"github.com/mheob/kurze-url/apps/cli/internal/credentials"
)

// The keychain is process-wide state in go-keyring, so nothing in this file
// calls t.Parallel: two tests swapping the provider under each other would
// read each other's entries.

// keychainService and the environment names below are the keychain key from
// the spec ("service kurze-url, one entry per environment"). They are
// repeated here, not imported, so renaming the service in the package breaks
// a test instead of silently orphaning every stored login.
const keychainService = "kurze-url"

// TestMain puts an in-memory keychain in place before any test runs, so a
// test that forgets to choose one can never reach the real keychain of the
// machine that runs it.
func TestMain(m *testing.M) {
	keyring.MockInit()
	os.Exit(m.Run())
}

// workingKeychain replaces the keychain with an empty in-memory one.
func workingKeychain(t *testing.T) {
	t.Helper()
	keyring.MockInit()
}

// errKeychainDown is what a brokenKeychain answers every call with.
var errKeychainDown = errors.New("no secret service")

// brokenKeychain replaces the keychain with one whose every call fails with
// errKeychainDown, the way a Linux box without a Secret Service does.
func brokenKeychain(t *testing.T) {
	t.Helper()
	keyring.MockInitWithError(errKeychainDown)
}

// sampleTokens returns tokens that are obviously not real, with an expiry in
// UTC so a JSON round trip gives back the same instant.
func sampleTokens() credentials.Tokens {
	return credentials.Tokens{
		AccessToken:  "access-one",
		RefreshToken: "refresh-one",
		ExpiresAt:    time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC),
	}
}

// requireSameTokens compares two Tokens by value. ExpiresAt goes through
// Equal because a time that crossed JSON need not carry the same location
// pointer as the one that went in.
func requireSameTokens(t *testing.T, want, got credentials.Tokens) {
	t.Helper()
	require.Equal(t, want.AccessToken, got.AccessToken)
	require.Equal(t, want.RefreshToken, got.RefreshToken)
	require.True(t, want.ExpiresAt.Equal(got.ExpiresAt), "expires_at: want %s, got %s", want.ExpiresAt, got.ExpiresAt)
}

// keychainHolds puts tokens into the (working) keychain the way a Save does,
// without going through the Chain, so a test can set up a store the Chain's
// own rules would never have produced.
func keychainHolds(t *testing.T, env string, tokens credentials.Tokens) {
	t.Helper()
	data, err := json.Marshal(tokens)
	require.NoError(t, err)
	require.NoError(t, keyring.Set(keychainService, env, string(data)))
}

// directoryEntries lists the names inside dir, failing the test when it
// cannot be read.
func directoryEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestSaveUsesTheKeychain(t *testing.T) {
	workingKeychain(t)
	dir := t.TempDir()
	store := credentials.New("a", dir)

	require.NoError(t, store.Save(sampleTokens()))

	got, err := store.Load()
	require.NoError(t, err)
	requireSameTokens(t, sampleTokens(), got)
	require.False(t, store.FellBack())
	require.Empty(t, directoryEntries(t, dir), "a keychain save must not write a file")

	// The entry sits where the spec says: service kurze-url, user = the
	// environment name.
	_, err = keyring.Get(keychainService, "a")
	require.NoError(t, err)
}

func TestSaveFallsBackToA0600File(t *testing.T) {
	brokenKeychain(t)
	dir := filepath.Join(t.TempDir(), "kurze-url") // does not exist yet
	store := credentials.New("a", dir)

	require.NoError(t, store.Save(sampleTokens()))

	got, err := store.Load()
	require.NoError(t, err)
	requireSameTokens(t, sampleTokens(), got)
	require.True(t, store.FellBack())

	path := filepath.Join(dir, "credentials-a.json")
	require.Equal(t, path, store.FilePath())
	require.Equal(t, []string{"credentials-a.json"}, directoryEntries(t, dir), "no temporary file may remain")
	if runtime.GOOS != "windows" { // Windows reports 0666 whatever was asked
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
		dirInfo, err := os.Stat(dir)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())
	}
}

func TestSaveOverwritesTheFileInPlace(t *testing.T) {
	brokenKeychain(t)
	dir := t.TempDir()
	store := credentials.New("a", dir)
	require.NoError(t, store.Save(sampleTokens()))

	rotated := sampleTokens()
	rotated.RefreshToken = "refresh-two"
	require.NoError(t, store.Save(rotated))

	got, err := store.Load()
	require.NoError(t, err)
	requireSameTokens(t, rotated, got)
	require.Equal(t, []string{"credentials-a.json"}, directoryEntries(t, dir))
}

func TestLoadWithNothingStoredIsErrNotFound(t *testing.T) {
	t.Run("working keychain", func(t *testing.T) {
		workingKeychain(t)
		_, err := credentials.New("a", t.TempDir()).Load()
		require.ErrorIs(t, err, credentials.ErrNotFound)
	})
	t.Run("broken keychain", func(t *testing.T) {
		brokenKeychain(t)
		_, err := credentials.New("a", t.TempDir()).Load()
		require.ErrorIs(t, err, credentials.ErrNotFound)
	})
}

func TestLoadFindsTheFileWhenTheKeychainHasNothing(t *testing.T) {
	// A login that fell back to the file keeps working after the keychain
	// comes back, until the next Save moves it there.
	brokenKeychain(t)
	dir := t.TempDir()
	require.NoError(t, credentials.New("a", dir).Save(sampleTokens()))
	workingKeychain(t)

	got, err := credentials.New("a", dir).Load()

	require.NoError(t, err)
	requireSameTokens(t, sampleTokens(), got)
}

func TestLoadSkipsAnUnreadableKeychainEntry(t *testing.T) {
	brokenKeychain(t)
	dir := t.TempDir()
	require.NoError(t, credentials.New("a", dir).Save(sampleTokens()))
	workingKeychain(t)
	require.NoError(t, keyring.Set(keychainService, "a", "this is not json"))

	got, err := credentials.New("a", dir).Load()

	require.NoError(t, err)
	requireSameTokens(t, sampleTokens(), got)
}

// TestLoadWithBothStoresTakesTheLaterExpiry is the other half of the story
// above Delete: once the keychain recovers, its old entry must not shadow the
// newer login the file holds. A later login or rotation always expires later.
func TestLoadWithBothStoresTakesTheLaterExpiry(t *testing.T) {
	older := credentials.Tokens{AccessToken: "access-old", RefreshToken: "refresh-old", ExpiresAt: sampleTokens().ExpiresAt}
	newer := credentials.Tokens{AccessToken: "access-new", RefreshToken: "refresh-new", ExpiresAt: sampleTokens().ExpiresAt.Add(time.Hour)}
	tiedKeychain := credentials.Tokens{AccessToken: "access-k", RefreshToken: "refresh-keychain", ExpiresAt: older.ExpiresAt}
	tiedFile := credentials.Tokens{AccessToken: "access-f", RefreshToken: "refresh-file", ExpiresAt: older.ExpiresAt}

	for _, tc := range []struct {
		name           string
		keychain, file credentials.Tokens
		want           credentials.Tokens
	}{
		{"keychain is later", newer, older, newer},
		{"file is later", older, newer, newer},
		{"a tie goes to the keychain", tiedKeychain, tiedFile, tiedKeychain},
	} {
		t.Run(tc.name, func(t *testing.T) {
			brokenKeychain(t)
			dir := t.TempDir()
			require.NoError(t, credentials.New("a", dir).Save(tc.file))
			workingKeychain(t)
			keychainHolds(t, "a", tc.keychain)

			got, err := credentials.New("a", dir).Load()

			require.NoError(t, err)
			requireSameTokens(t, tc.want, got)
		})
	}
}

func TestLoadPrefersTheKeychainOverADamagedFile(t *testing.T) {
	// A file nobody asked for must not break a login the keychain holds.
	workingKeychain(t)
	dir := t.TempDir()
	keychainHolds(t, "a", sampleTokens())
	require.NoError(t, os.WriteFile(filepath.Join(dir, "credentials-a.json"), []byte("not json"), 0o600))

	got, err := credentials.New("a", dir).Load()

	require.NoError(t, err)
	requireSameTokens(t, sampleTokens(), got)
}

func TestLoadWithACorruptFileIsAnErrorThatLeaksNothing(t *testing.T) {
	workingKeychain(t)
	dir := t.TempDir()
	// Valid JSON of the wrong shape, so the decoder fails halfway through a
	// document that carries two secret-looking values.
	corrupt := `{"access_token":"SECRET-ACCESS","refresh_token":["SECRET-REFRESH"]}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "credentials-a.json"), []byte(corrupt), 0o600))

	_, err := credentials.New("a", dir).Load()

	require.Error(t, err)
	require.NotErrorIs(t, err, credentials.ErrNotFound, "a damaged file is not 'not logged in'")
	require.NotContains(t, err.Error(), "SECRET")
}

func TestDeleteRemovesBoth(t *testing.T) {
	// A file written while the keychain was down, then an entry that arrived
	// in the keychain afterwards: both exist, and logout must clear both.
	brokenKeychain(t)
	dir := t.TempDir()
	store := credentials.New("a", dir)
	require.NoError(t, store.Save(sampleTokens()))
	workingKeychain(t)
	require.NoError(t, keyring.Set(keychainService, "a", `{"access_token":"x","refresh_token":"y"}`))

	require.NoError(t, store.Delete())

	_, err := store.Load()
	require.ErrorIs(t, err, credentials.ErrNotFound)
	_, err = keyring.Get(keychainService, "a")
	require.ErrorIs(t, err, keyring.ErrNotFound)
	require.Empty(t, directoryEntries(t, dir))
}

func TestDeleteWithNothingStoredIsNotAnError(t *testing.T) {
	workingKeychain(t)
	require.NoError(t, credentials.New("a", t.TempDir()).Delete())
}

// The next three tests share one story, and it is why Delete never hides a
// keychain failure just because it also found a file. A login that worked
// leaves its entry in the keychain; the keychain then becomes unusable (a
// locked macOS keychain over SSH, no Secret Service in a headless session);
// Load says "not logged in"; the person logs in again and the Save falls back
// to the file. Now the file and the old entry both exist, and a logout that
// removes the file and answers "done" leaves a token behind.

func TestDeleteWithAFallbackFileRemovesItAndReportsAnUnreachableKeychain(t *testing.T) {
	brokenKeychain(t)
	dir := t.TempDir()
	store := credentials.New("a", dir)
	require.NoError(t, store.Save(sampleTokens()))

	err := store.Delete()

	require.ErrorIs(t, err, credentials.ErrKeychainUnavailable)
	require.ErrorIs(t, err, errKeychainDown, "the keychain's own error stays reachable")
	require.Empty(t, directoryEntries(t, dir), "the file goes whatever the keychain does")
}

func TestDeleteWithoutAFallbackFileReportsAnUnreachableKeychain(t *testing.T) {
	brokenKeychain(t)
	dir := t.TempDir()

	err := credentials.New("a", dir).Delete()

	require.ErrorIs(t, err, credentials.ErrKeychainUnavailable)
	require.ErrorIs(t, err, errKeychainDown)
	require.Empty(t, directoryEntries(t, dir))
}

func TestDeleteReportsBothFailuresWhenBothStoresFail(t *testing.T) {
	brokenKeychain(t)
	dir := t.TempDir()
	// A directory with something in it cannot be removed like a file.
	blocker := filepath.Join(dir, "credentials-a.json")
	require.NoError(t, os.Mkdir(blocker, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(blocker, "child"), []byte("x"), 0o600))

	err := credentials.New("a", dir).Delete()

	require.ErrorIs(t, err, credentials.ErrKeychainUnavailable)
	require.ErrorContains(t, err, "credentials file", "the file's failure is reported as well")
}

func TestEnvironmentsDoNotShareTokens(t *testing.T) {
	t.Run("keychain", func(t *testing.T) {
		workingKeychain(t)
		dir := t.TempDir()
		require.NoError(t, credentials.New("a", dir).Save(sampleTokens()))

		_, err := credentials.New("b", dir).Load()

		require.ErrorIs(t, err, credentials.ErrNotFound)
	})
	t.Run("file", func(t *testing.T) {
		brokenKeychain(t)
		dir := t.TempDir()
		require.NoError(t, credentials.New("a", dir).Save(sampleTokens()))

		_, err := credentials.New("b", dir).Load()

		require.ErrorIs(t, err, credentials.ErrNotFound)
		require.Equal(t, []string{"credentials-a.json"}, directoryEntries(t, dir))
	})
}

func TestKeychainSaveRemovesAStaleFile(t *testing.T) {
	brokenKeychain(t)
	dir := t.TempDir()
	store := credentials.New("a", dir)
	require.NoError(t, store.Save(sampleTokens()))
	require.True(t, store.FellBack())
	workingKeychain(t)

	rotated := sampleTokens()
	rotated.RefreshToken = "refresh-two"
	require.NoError(t, store.Save(rotated))

	require.False(t, store.FellBack(), "FellBack describes the latest Save")
	require.Empty(t, directoryEntries(t, dir), "the old token must not stay on disk")
	got, err := store.Load()
	require.NoError(t, err)
	requireSameTokens(t, rotated, got)
}

// TestSaveReplacesAnExistingFileAtomically is what pins the temporary file and
// the rename. A write into the existing file would fail on a read-only one,
// keep a loose mode on another, and truncate the document a concurrent reader
// has open; none of those can happen when the file is replaced as a whole.
func TestSaveReplacesAnExistingFileAtomically(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no file modes and cannot rename over a file that is open")
	}
	const previous = `{"access_token":"access-old","refresh_token":"refresh-old","expires_at":"2026-01-01T00:00:00Z"}`
	for _, mode := range []os.FileMode{0o400, 0o644} {
		t.Run(fmt.Sprintf("existing file %#o", mode), func(t *testing.T) {
			brokenKeychain(t)
			dir := t.TempDir()
			path := filepath.Join(dir, "credentials-a.json")
			require.NoError(t, os.WriteFile(path, []byte(previous), mode))
			require.NoError(t, os.Chmod(path, mode)) // the umask must not alter the setup
			reader, err := os.Open(path)
			require.NoError(t, err)
			t.Cleanup(func() { _ = reader.Close() })

			require.NoError(t, credentials.New("a", dir).Save(sampleTokens()))

			got, err := credentials.New("a", dir).Load()
			require.NoError(t, err)
			requireSameTokens(t, sampleTokens(), got)
			info, err := os.Stat(path)
			require.NoError(t, err)
			require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
			// A reader that opened the file before the Save still sees the old
			// document, whole: the Save built a new file and swapped it in.
			content, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.Equal(t, previous, string(content))
			require.Equal(t, []string{"credentials-a.json"}, directoryEntries(t, dir))
		})
	}
}

// TestSaveReportsAStaleFileItCannotRemove pins the one place a successful
// keychain write still ends in an error: a leftover file that stays behind
// would keep a secret on disk without anyone knowing.
func TestSaveReportsAStaleFileItCannotRemove(t *testing.T) {
	workingKeychain(t)
	dir := t.TempDir()
	// A directory with something in it cannot be removed like a file.
	blocker := filepath.Join(dir, "credentials-a.json")
	require.NoError(t, os.Mkdir(blocker, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(blocker, "child"), []byte("x"), 0o600))
	store := credentials.New("a", dir)

	err := store.Save(sampleTokens())

	require.Error(t, err)
	require.False(t, store.FellBack())
	raw, kerr := keyring.Get(keychainService, "a")
	require.NoError(t, kerr, "the keychain already holds the new tokens")
	var stored credentials.Tokens
	require.NoError(t, json.Unmarshal([]byte(raw), &stored))
	requireSameTokens(t, sampleTokens(), stored)
}

func TestFailedFileWriteLeavesNoTemporaryFile(t *testing.T) {
	brokenKeychain(t)
	dir := t.TempDir()
	// A directory where the file belongs makes the final rename fail after
	// the temporary file has been written and synced, the latest point at
	// which the write can go wrong.
	blocker := filepath.Join(dir, "credentials-a.json")
	require.NoError(t, os.Mkdir(blocker, 0o700))
	tokens := sampleTokens()
	tokens.AccessToken = "SECRET-ACCESS"

	err := credentials.New("a", dir).Save(tokens)

	require.Error(t, err)
	require.ErrorIs(t, err, errKeychainDown, "both failures are reported")
	require.NotContains(t, err.Error(), "SECRET", "an error must never carry a token")
	require.Equal(t, []string{"credentials-a.json"}, directoryEntries(t, dir))
}

// TestConcurrentFileSavesLeaveOneFileAndNoTemporaryFile runs saves against
// each other, as two commands refreshing in parallel would. It checks that
// none fails and that nothing but the one file is left. It cannot detect a
// torn write: every save writes the same document, and the final state is
// complete however the writes interleaved. Atomicity is pinned by
// TestSaveReplacesAnExistingFileAtomically. The keychain stays broken because
// go-keyring's mock is not itself safe for concurrent use.
func TestConcurrentFileSavesLeaveOneFileAndNoTemporaryFile(t *testing.T) {
	brokenKeychain(t)
	dir := t.TempDir()
	store := credentials.New("a", dir)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 20 {
				require.NoError(t, store.Save(sampleTokens()))
				_ = store.FellBack()
			}
		})
	}
	wg.Wait()

	got, err := store.Load()
	require.NoError(t, err)
	requireSameTokens(t, sampleTokens(), got)
	require.Equal(t, []string{"credentials-a.json"}, directoryEntries(t, dir))
}

func TestAnEmptyEnvironmentNameIsRefused(t *testing.T) {
	workingKeychain(t)
	dir := t.TempDir()
	store := credentials.New("", dir)

	require.Error(t, store.Save(sampleTokens()))
	_, err := store.Load()
	require.Error(t, err)
	require.NotErrorIs(t, err, credentials.ErrNotFound, "a programming error is not 'not logged in'")
	require.Error(t, store.Delete())

	require.Empty(t, store.FilePath())
	require.Empty(t, directoryEntries(t, dir), "no credentials-.json")
	_, err = keyring.Get(keychainService, "")
	require.ErrorIs(t, err, keyring.ErrNotFound, "no keychain entry with an empty user")
}

func TestAnEmptyDirectoryIsRefused(t *testing.T) {
	// Not a keychain failure, so the keychain is working: the Chain is wrong
	// whichever store it would have used, and a token file in the working
	// directory is the outcome this refusal exists to prevent.
	workingKeychain(t)
	store := credentials.New("a", "")

	require.Error(t, store.Save(sampleTokens()))
	_, err := store.Load()
	require.Error(t, err)
	require.NotErrorIs(t, err, credentials.ErrNotFound)
	require.Error(t, store.Delete())

	require.Empty(t, store.FilePath())
	_, err = keyring.Get(keychainService, "a")
	require.ErrorIs(t, err, keyring.ErrNotFound)
}

func TestChainImplementsBothInterfaces(t *testing.T) {
	var store credentials.Store = credentials.New("a", t.TempDir())
	_, isReporter := store.(credentials.FallbackReporter)
	require.True(t, isReporter)
}

func TestMemoryRoundTrip(t *testing.T) {
	mem := credentials.NewMemory()

	_, err := mem.Load()
	require.ErrorIs(t, err, credentials.ErrNotFound)

	require.NoError(t, mem.Save(sampleTokens()))
	got, err := mem.Load()
	require.NoError(t, err)
	requireSameTokens(t, sampleTokens(), got)

	require.NoError(t, mem.Delete())
	_, err = mem.Load()
	require.ErrorIs(t, err, credentials.ErrNotFound)
	require.NoError(t, mem.Delete(), "deleting nothing is not an error")
}

func TestMemoryReportsTheFallbackPathItIsGiven(t *testing.T) {
	var store credentials.Store = credentials.NewMemory()
	reporter, ok := store.(credentials.FallbackReporter)
	require.True(t, ok)
	require.False(t, reporter.FellBack())
	require.Empty(t, reporter.FilePath())

	mem := credentials.NewMemory()
	mem.FallbackPath = "/tmp/credentials-a.json"
	require.True(t, mem.FellBack())
	require.Equal(t, "/tmp/credentials-a.json", mem.FilePath())
}

// TestMemoryIsSafeForConcurrentUse is for `go test -race`: Task 6's session
// is driven from several goroutines, and its tests share one Memory.
func TestMemoryIsSafeForConcurrentUse(_ *testing.T) {
	mem := credentials.NewMemory()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 50 {
				_ = mem.Save(sampleTokens())
				_, _ = mem.Load()
				_ = mem.FellBack()
				_ = mem.Delete()
			}
		})
	}
	wg.Wait()
}
