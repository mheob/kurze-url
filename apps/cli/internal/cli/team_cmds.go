package cli

import (
	"context"

	"github.com/spf13/cobra"

	"github.com/mheob/kurze-url/apps/cli/internal/i18n"
	"github.com/mheob/kurze-url/apps/cli/internal/output"
)

// teamNameColumn is the column of `team list` that gives way when a line is
// wider than the terminal: the name is the one free-form text in the row.
const teamNameColumn = 2

// newTeamCmd is the `team` group.
func newTeamCmd(d *Deps) *cobra.Command {
	team := newGroup("team", "Show your teams")
	team.AddCommand(newTeamListCmd(d))
	return team
}

// newTeamListCmd lists the teams the person belongs to.
func newTeamListCmd(d *Deps) *cobra.Command {
	return &cobra.Command{
		Use:               "list",
		Short:             "List your teams, with the default team marked",
		Args:              usageArgs(cobra.NoArgs),
		ValidArgsFunction: noValues,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a, err := newApp(d)
			if err != nil {
				return err
			}
			return a.teamList(cmd.Context())
		},
	}
}

// teamList prints one row per membership: a marker for the default team, the
// slug, the name and the localised role.
//
// A terminal gets an aligned table under a localised header, with the name
// shortened to fit. Anything else gets bare tab-separated rows, with the
// marker column kept empty rather than dropped so that every row has the same
// four columns, and no header, so a script reads the first line as data.
func (a *App) teamList(ctx context.Context) error {
	me, err := a.me(ctx)
	if err != nil {
		return err
	}
	if len(me.Memberships) == 0 {
		a.notice(a.Cat.T("team.listEmpty"))
		return nil
	}

	def := a.configuredTeam()
	rows := make([][]string, 0, len(me.Memberships))
	for _, m := range me.Memberships {
		marker := ""
		if m.Slug == def {
			marker = "*"
		}
		rows = append(rows, []string{marker, m.Slug, m.Name, roleLabel(a.Cat, m.Role)})
	}

	if a.d.StdoutTTY() {
		header := []string{
			"",
			a.Cat.T("team.header.slug"),
			a.Cat.T("team.header.name"),
			a.Cat.T("team.header.role"),
		}
		output.Table(a.d.Stdout, header, rows, a.d.TermWidth(), teamNameColumn)
		return nil
	}
	output.TSV(a.d.Stdout, rows)
	return nil
}

// roleLabel words a role the way the web app does: Owner is "Eigentum" in
// German and Viewer is "Leserecht". A role the CLI does not know is shown as
// the API sent it, so a role added to the API shows up before the CLI learns
// its name.
//
// Every case calls T with its own literal key, which is what the catalog check
// needs to see.
func roleLabel(c *i18n.Catalog, role string) string {
	switch role {
	case "owner":
		return c.T("team.role.owner")
	case "admin":
		return c.T("team.role.admin")
	case "editor":
		return c.T("team.role.editor")
	case "viewer":
		return c.T("team.role.viewer")
	}
	return role
}
