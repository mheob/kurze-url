package cli

import (
	"fmt"
	"os/exec"
	"runtime"
	"time"
)

// browserSettle is how long an opener may run before login takes it for a
// browser that is up and keeps running. A desktop opener (open, rundll32)
// hands the URL over and exits at once, so the wait is only ever spent on the
// ones that stay, and xdg-open on a machine without a display fails within
// moments.
const browserSettle = 2 * time.Second

// openBrowser opens url in the person's default browser, with the platform's
// own opener: open on macOS, rundll32 on Windows, xdg-open everywhere else.
// An error means the URL was not opened, and login then prints it.
func openBrowser(url string) error {
	name, args := browserCommand(runtime.GOOS, url)
	return runDetached(exec.Command(name, args...), browserSettle)
}

// browserCommand is the opener for a platform. url is passed as an argument
// and never through a shell, so its "&" and "?" need no quoting and cannot
// run anything.
func browserCommand(goos, url string) (name string, args []string) {
	switch goos {
	case "darwin":
		return "open", []string{url}
	case "windows":
		return "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		return "xdg-open", []string{url}
	}
}

// runDetached starts cmd and waits at most settle for it to finish. Exiting
// cleanly inside that time is success and exiting with an error is a failure
// to report. Still running after it is success: the command is a browser
// that took the URL and is staying open, and waiting for it would hold the
// login up for as long as the browser lives. Its output is discarded, so an
// opener's complaint cannot garble the terminal.
func runDetached(cmd *exec.Cmd, settle time.Duration) error {
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start the browser opener: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("the browser opener failed: %w", err)
		}
		return nil
	case <-time.After(settle):
		return nil
	}
}
