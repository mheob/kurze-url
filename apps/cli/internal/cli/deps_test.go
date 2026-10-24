package cli

import (
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/cli/internal/auth"
	"github.com/mheob/kurze-url/apps/cli/internal/credentials"
)

var errNoHome = errors.New("neither $HOME nor $XDG_CONFIG_HOME is set")

// Every field a task adds to Deps needs its production value in Default, or
// main would hand the commands a nil function. Reflection finds the field a
// later task forgets.
func TestDefaultFillsEveryField(t *testing.T) {
	d := Default(testBuild)

	v := reflect.ValueOf(*d)
	for i := range v.NumField() {
		name := v.Type().Field(i).Name
		// The credentials directory comes from the user's config directory,
		// which a machine without a home does not have. It is tested on its
		// own below.
		if name == "CredentialsDir" {
			continue
		}
		require.False(t, v.Field(i).IsZero(), "Default leaves Deps.%s empty", name)
	}
	require.Equal(t, testBuild, d.Build)
}

// The auth calls and the API client both refuse redirects only when the
// client they are handed has no policy of its own, and both need a timeout
// the client does not supply.
func TestDefaultHTTPClientHasATimeoutAndNoRedirectPolicy(t *testing.T) {
	c := Default(testBuild).HTTPClient

	require.Equal(t, 30*time.Second, c.Timeout)
	require.Nil(t, c.CheckRedirect)
}

func TestDefaultLoginPortsAreACopyOfTheRegisteredOnes(t *testing.T) {
	d := Default(testBuild)

	require.Equal(t, auth.DefaultPorts, d.LoginPorts)
	require.Equal(t, auth.DefaultLoginTimeout, d.LoginTimeout)

	d.LoginPorts[0] = 1
	require.NotEqual(t, 1, auth.DefaultPorts[0], "editing the Deps must not edit the package's list")
}

func TestDefaultStoreIsKeyedByTheEnvironmentName(t *testing.T) {
	dir := t.TempDir()

	store := Default(testBuild).NewStore("example-supabase-co", dir)

	// The fallback file's name is the one place the environment name and
	// the directory are visible without touching a keychain.
	reporter, ok := store.(credentials.FallbackReporter)
	require.True(t, ok, "the production store must report where its fallback file is")
	require.Equal(t, filepath.Join(dir, "credentials-example-supabase-co.json"), reporter.FilePath())
}

func TestCredentialsDirIsKurzeURLInTheUserConfigDirectory(t *testing.T) {
	got := credentialsDir(func() (string, error) { return "/home/ada/.config", nil })

	require.Equal(t, filepath.Join("/home/ada/.config", "kurze-url"), got)
}

// An empty directory is refused by the store with its own error, and that is
// better than a token file beside whatever the person is working on.
func TestCredentialsDirIsEmptyWithoutAUserConfigDirectory(t *testing.T) {
	got := credentialsDir(func() (string, error) { return "", errNoHome })

	require.Empty(t, got)
}
