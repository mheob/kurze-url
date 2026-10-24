package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// newVersionCmd prints the build the person is running.
//
// The line is English in every language. It is what people paste into bug
// reports and what scripts grep, so it must not change with the locale.
func newVersionCmd(d *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(*cobra.Command, []string) error {
			b := d.Build
			if _, err := fmt.Fprintf(d.Stdout, "%s %s (%s, %s)\n", programName, b.Version, b.Commit, b.Date); err != nil {
				return fmt.Errorf("write version: %w", err)
			}
			return nil
		},
	}
}
