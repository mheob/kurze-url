package cli

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const authURL = "http://127.0.0.1:54321/auth/v1/oauth/authorize?client_id=x&state=y"

func TestBrowserCommandPerPlatform(t *testing.T) {
	tests := []struct {
		goos string
		name string
		args []string
	}{
		{"darwin", "open", []string{authURL}},
		{"linux", "xdg-open", []string{authURL}},
		{"freebsd", "xdg-open", []string{authURL}},
		{"windows", "rundll32", []string{"url.dll,FileProtocolHandler", authURL}},
	}
	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			name, args := browserCommand(tt.goos, authURL)

			require.Equal(t, tt.name, name)
			require.Equal(t, tt.args, args)
		})
	}
}

// The helper is this test binary run again, the way os/exec's own tests do
// it: a child that exits, fails or lingers on demand, so no real browser or
// opener is ever started.
func helperCommand(t *testing.T, mode string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestBrowserHelperProcess$")
	cmd.Env = []string{"KURZE_URL_TEST_HELPER=" + mode}
	return cmd
}

// TestBrowserHelperProcess is the child of helperCommand. Run on its own it
// does nothing.
func TestBrowserHelperProcess(*testing.T) {
	switch os.Getenv("KURZE_URL_TEST_HELPER") {
	case "exit0":
		os.Exit(0)
	case "exit3":
		os.Exit(3)
	case "linger":
		time.Sleep(time.Minute)
	}
}

func TestRunDetachedSucceedsWhenTheOpenerExitsCleanly(t *testing.T) {
	require.NoError(t, runDetached(helperCommand(t, "exit0"), 10*time.Second))
}

// xdg-open on a machine without a display exits non-zero within moments,
// and then the login has to print the URL for the person to open by hand.
func TestRunDetachedReportsAnOpenerThatFails(t *testing.T) {
	err := runDetached(helperCommand(t, "exit3"), 10*time.Second)

	require.Error(t, err)
}

func TestRunDetachedReportsAnOpenerThatIsNotThere(t *testing.T) {
	err := runDetached(exec.Command("kurze-url-no-such-opener"), time.Second)

	require.Error(t, err)
}

// An opener that stays alive is a browser that took the URL and keeps
// running. Waiting for it would hold the login up for as long as the browser
// lives.
func TestRunDetachedDoesNotWaitForABrowserThatStaysOpen(t *testing.T) {
	cmd := helperCommand(t, "linger")
	start := time.Now()

	err := runDetached(cmd, 100*time.Millisecond)
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	require.NoError(t, err)
	require.Less(t, time.Since(start), 5*time.Second)
}
