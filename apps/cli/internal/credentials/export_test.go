package credentials

import "time"

// SetClock replaces the clock the sweep of leftover temporary files judges a
// file's age by, and returns what puts the real one back.
func SetClock(clock func() time.Time) (restore func()) {
	previous := now
	now = clock
	return func() { now = previous }
}
