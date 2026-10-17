package api

import (
	"context"

	"github.com/google/uuid"

	"github.com/mheob/kurze-url/apps/api/internal/scanning"
)

// This file is compiled only into this package's tests. It hands package
// api_test, where the database fixtures live, the parts of the scan pipeline
// no route exposes on a test's terms: applyVerdict with a verdict the test
// chooses, and — added with the sweep — a sweep with a batch smaller than
// scanBatchSize.

// VerdictOutcome is applyVerdict's verdictOutcome, for assertions.
type VerdictOutcome = verdictOutcome

// ApplyVerdictForTest runs applyVerdict for one link.
func (d Deps) ApplyVerdictForTest(
	ctx context.Context, linkID, teamID uuid.UUID, url string, result scanning.Result,
) (VerdictOutcome, error) {
	return d.applyVerdict(ctx, scanTarget{LinkID: linkID, TeamID: teamID, URL: url}, result)
}

// ScanLockTimeout is how long applyVerdict waits for a link's row lock.
const ScanLockTimeout = scanLockTimeout

// ScanReport is the sweep's report, for assertions.
type ScanReport = scanReport

// SweepForTest runs one sweep with a batch limit the test chooses.
func (d Deps) SweepForTest(ctx context.Context, limit int) (ScanReport, error) {
	return d.sweep(ctx, limit)
}

// StoppedAtBudget is the report's unexported stoppedAtBudget.
func (r ScanReport) StoppedAtBudget() bool {
	return r.stoppedAtBudget
}
