package credentials_test

import (
	"errors"
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

func TestDeleteWithAFallbackFileIgnoresAnUnreachableKeychain(t *testing.T) {
	// The tokens lived in the file, because that is where a Save goes when
	// the keychain is down, so a keychain that cannot even be reached held
	// none. On a headless machine this is every logout, and reporting a
	// failure there would be wrong every time.
	brokenKeychain(t)
	dir := t.TempDir()
	store := credentials.New("a", dir)
	require.NoError(t, store.Save(sampleTokens()))

	require.NoError(t, store.Delete())

	require.Empty(t, directoryEntries(t, dir))
	_, err := store.Load()
	require.ErrorIs(t, err, credentials.ErrNotFound)
}

func TestDeleteWithoutAFallbackFileReportsAnUnreachableKeychain(t *testing.T) {
	// Nothing proves the keychain holds no token: it may only be locked. A
	// logout that then answered success would let a token survive it.
	brokenKeychain(t)
	dir := t.TempDir()

	err := credentials.New("a", dir).Delete()

	require.ErrorIs(t, err, errKeychainDown)
	require.Empty(t, directoryEntries(t, dir))
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

// TestConcurrentFileSavesNeverLeaveAHalfWrittenFile covers the reason for the
// temporary file and the rename: two saves racing each other, as two commands
// refreshing in parallel would, must leave one complete document and nothing
// else behind. The keychain stays broken because go-keyring's mock is not
// itself safe for concurrent use.
func TestConcurrentFileSavesNeverLeaveAHalfWrittenFile(t *testing.T) {
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
