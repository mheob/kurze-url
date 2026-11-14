package auth

import "time"

// This file is compiled only into this package's tests.

// SetClockForTest replaces the clock v reads when it decides whether its key
// set is due for a refresh.
func SetClockForTest(v *Verifier, now func() time.Time) {
	v.now = now
}

// KeySetMaxAgeForTest is how old the cached key set may grow before Verify
// refreshes it.
const KeySetMaxAgeForTest = keySetMaxAge

// RefreshGapForTest is the least time between two refresh attempts.
const RefreshGapForTest = refreshGap
