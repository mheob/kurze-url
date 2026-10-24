package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mheob/kurze-url/apps/cli/internal/config"
	"github.com/mheob/kurze-url/apps/cli/internal/i18n"
	"github.com/mheob/kurze-url/apps/cli/internal/output"
)

// configValueColumn is the column of `config get` that gives way when a line
// is wider than the terminal: the value is the one free-form text in a row,
// since a folder can be named anything.
const configValueColumn = 1

// newConfigCmd is the `config` group. None of its commands calls the API,
// so none needs a login: a person sets their language or their default team
// before the first `login`, and on a machine with no network.
func newConfigCmd(d *Deps) *cobra.Command {
	cfg := newGroup("config", "Show and change your settings")
	cfg.AddCommand(
		newConfigGetCmd(d),
		newConfigSetCmd(d),
		newConfigUnsetCmd(d),
	)
	return cfg
}

// keyHelp is the closing paragraph of the long help of every command that
// takes a KEY. It is built from config.Keys, the one list of what the file
// understands, so a ninth setting shows up here without anyone remembering to
// say so. The help is English like the rest of it.
func keyHelp() string {
	keys := config.Keys()
	names := make([]string, 0, len(keys))
	for _, k := range keys {
		names = append(names, string(k))
	}
	return "KEY is one of: " + strings.Join(names, ", ") + ".\n" +
		"folder and domain belong to one team and need --team."
}

// parseKey turns what the person typed into a Key. A name the file does not
// know is a mistake in how the command was invoked, so it exits 2 like a
// misspelled flag; describe words it from the *config.ValidationError inside,
// in the person's language and naming the key.
//
// It runs before the config is read: a typo in the key is the same mistake
// whatever the file looks like.
func parseKey(text string) (config.Key, error) {
	key, err := config.ParseKey(text)
	if err != nil {
		return "", &UsageError{Err: err}
	}
	return key, nil
}

// completeConfigKey offers the setting names for the key that `config get`,
// `set` and `unset` all take first, and nothing for a word after it: those
// commands take no more, and offering the keys again would suggest a command
// that then fails its argument count.
//
// The names come from config.Keys, in its order, the same list the help and
// the file's validation read, so a ninth setting completes without anyone
// remembering to add it here.
func completeConfigKey(_ *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	keys := config.Keys()
	names := make([]cobra.Completion, 0, len(keys))
	for _, k := range keys {
		names = append(names, string(k))
	}
	return withPrefix(names, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// completeConfigSet offers the key for `config set`'s first word and, for the
// second, the values the chosen key accepts when they are a closed set: the
// two languages and the two image formats, which are the values the config
// package's own rules allow.
//
// A size and a colour are free text, and so offer nothing; the same is true
// for team, folder and domain until they are completed from the service. A key
// the file does not know has no values either, and past the value there is
// nothing to offer, for the reason completeConfigKey gives.
func completeConfigSet(cmd *cobra.Command, args []string, toComplete string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) != 1 {
		return completeConfigKey(cmd, args, toComplete)
	}

	var values []cobra.Completion
	switch config.Key(args[0]) {
	case config.Language:
		values = []cobra.Completion{string(i18n.EN), string(i18n.DE)}
	case config.QRFormat:
		values = []cobra.Completion{qrSVG, qrPNG}
	}
	return withPrefix(values, toComplete), cobra.ShellCompDirectiveNoFileComp
}

// newConfigGetCmd shows one setting, or all of them with where each comes from.
func newConfigGetCmd(d *Deps) *cobra.Command {
	var team string
	cmd := &cobra.Command{
		Use:   "get [KEY]",
		Short: "Show a setting, or all of them and where each comes from",
		Long: "Without KEY, lists every setting that has a value, with where it comes from.\n" +
			"With KEY, prints only its value and exits 1 when it is not set.\n\n" +
			"Settings are looked up for the team given by --team, else for your default team:\n" +
			"that team's own value first, then the global one.\n\n" + keyHelp(),
		Args:              usageArgs(cobra.MaximumNArgs(1)),
		ValidArgsFunction: completeConfigKey,
		RunE: func(_ *cobra.Command, args []string) error {
			var key config.Key
			if len(args) == 1 {
				var err error
				if key, err = parseKey(args[0]); err != nil {
					return err
				}
			}
			a, err := newApp(d)
			if err != nil {
				return err
			}
			if len(args) == 0 {
				return a.configList(team)
			}
			return a.configGet(key, team)
		},
	}
	cmd.Flags().StringVar(&team, "team", "", "look up the settings of this team (default: your default team)")
	return cmd
}

// newConfigSetCmd writes one setting to the config file.
func newConfigSetCmd(d *Deps) *cobra.Command {
	var team string
	cmd := &cobra.Command{
		Use:   "set KEY VALUE",
		Short: "Change a setting in the config file",
		Long: "Writes the setting to the top of the config file, or to one team's section with --team.\n" +
			"The team is not checked against your memberships, because those change.\n\n" + keyHelp(),
		Args:              usageArgs(cobra.ExactArgs(2)),
		ValidArgsFunction: completeConfigSet,
		RunE: func(_ *cobra.Command, args []string) error {
			key, err := parseKey(args[0])
			if err != nil {
				return err
			}
			a, err := newApp(d)
			if err != nil {
				return err
			}
			return a.configSet(key, team, args[1])
		},
	}
	cmd.Flags().StringVar(&team, "team", "", "write to this team's section instead of the global settings")
	return cmd
}

// newConfigUnsetCmd removes one setting from the config file.
func newConfigUnsetCmd(d *Deps) *cobra.Command {
	var team string
	cmd := &cobra.Command{
		Use:   "unset KEY",
		Short: "Remove a setting from the config file",
		Long: "Removes the setting from the top of the config file, or from one team's section with --team.\n\n" +
			keyHelp(),
		Args:              usageArgs(cobra.ExactArgs(1)),
		ValidArgsFunction: completeConfigKey,
		RunE: func(_ *cobra.Command, args []string) error {
			key, err := parseKey(args[0])
			if err != nil {
				return err
			}
			a, err := newApp(d)
			if err != nil {
				return err
			}
			return a.configUnset(key, team)
		},
	}
	cmd.Flags().StringVar(&team, "team", "", "remove from this team's section instead of the global settings")
	return cmd
}

// readTeam is the team a read looks settings up for: the --team flag, else
// the default team, else none, in which case only the global settings exist.
//
// Writing deliberately has no such fallback. `get` answers "what will my
// commands use", and the default team is part of that answer; `set` and
// `unset` change one named place, and a setting that landed in the default
// team's section because --team was forgotten would be easy to miss.
func (a *App) readTeam(flag string) string {
	if flag != "" {
		return flag
	}
	return a.configuredTeam()
}

// effective finds the value k has for team and where it comes from, with the
// source already worded in the person's language. ok is false when the file
// says nothing and there is no built-in to fall back on.
//
// Only the language has no "not set": a file that does not name one leaves it
// to detection, and the catalog already speaks whatever that picked, so the
// answer is its language and the source is the system. Every other setting has
// its default applied by the command that uses it, which is not this file's
// business, so an unset one is simply absent.
func (a *App) effective(k config.Key, team string) (value, source string, ok bool) {
	value, from, found := a.Config.Resolve(k, team)
	switch {
	case found && from == config.SourceTeam:
		return value, a.Cat.T("config.source.team", i18n.Args{"slug": team}), true
	case found:
		return value, a.Cat.T("config.source.global"), true
	case k == config.Language:
		return string(a.Cat.Lang()), a.Cat.T("config.source.system"), true
	}
	return "", "", false
}

// configList prints one row per setting that has a value: its name, its
// effective value and where the value comes from.
//
// A terminal gets an aligned table under a localised header, with the value
// shortened to fit. Anything else gets bare tab-separated rows and no
// header, like `team list`, so a script reads the first line as data.
//
// Without a team (no --team and no default team) only the global settings
// can be listed: folder and domain have no meaning outside a team, so they are
// absent exactly as they are for a team that has not set them.
func (a *App) configList(flag string) error {
	team := a.readTeam(flag)

	var rows [][]string
	for _, k := range config.Keys() {
		value, source, ok := a.effective(k, team)
		if !ok {
			continue
		}
		rows = append(rows, []string{string(k), value, source})
	}

	if a.d.StdoutTTY() {
		header := []string{
			a.Cat.T("config.header.key"),
			a.Cat.T("config.header.value"),
			a.Cat.T("config.header.source"),
		}
		output.Table(a.d.Stdout, header, rows, a.d.TermWidth(), configValueColumn)
		return nil
	}
	output.TSV(a.d.Stdout, rows)
	return nil
}

// configGet prints the effective value of one setting and nothing else, so
// `$(kurze-url config get folder)` is the value. A setting that is not set
// fails with exit 1, as `git config` does, so a script can branch on it.
//
// The value is printed as the file holds it, not shortened and not cleaned:
// it is the person's own text and a script wants it exactly.
func (a *App) configGet(k config.Key, flag string) error {
	value, _, ok := a.effective(k, a.readTeam(flag))
	if !ok {
		return errors.New(a.Cat.T("config.notSet", i18n.Args{"key": string(k)}))
	}
	return a.result(value)
}

// configSet validates and stores one value, then writes the file.
//
// The team is whatever --team named and is never looked up, neither among the
// person's memberships nor against the API: membership changes, and a section
// written before an invitation is accepted is what makes the setting wait for
// it. A refusal (a key in the wrong scope, a value outside its range) is the
// config package's *ValidationError, which describe words with the file's
// path, and it leaves both the file and the loaded tree as they were.
func (a *App) configSet(k config.Key, team, value string) error {
	if err := a.Config.Set(k, team, value); err != nil {
		return err
	}
	return a.saveConfig()
}

// configUnset removes one setting, then writes the file.
//
// Removing what is not there is not a failure, so a script can run it twice,
// but it is also not a write: the file stays byte for byte what it was, and a
// machine with no config file does not get an empty one. The scope is judged
// first, because "folder without a team" is a mistake to report and not an
// absence to shrug at.
func (a *App) configUnset(k config.Key, team string) error {
	_, present := a.Config.Get(k, team)
	if err := a.Config.Unset(k, team); err != nil {
		return err
	}
	if !present {
		a.notice(a.Cat.T("config.notSet", i18n.Args{"key": string(k)}))
		return nil
	}
	return a.saveConfig()
}

// saveConfig writes the edited config file and says where it went.
//
// The path is derived again, from the same environment the file was loaded
// with, because the loaded File does not give it out.
func (a *App) saveConfig() error {
	path, err := config.Path(a.d.Getenv)
	if err != nil {
		return fmt.Errorf("find the config file: %w", err)
	}
	if err := a.Config.Save(); err != nil {
		return fmt.Errorf("save the config: %w", err)
	}
	a.notice(a.Cat.T("config.saved", i18n.Args{"path": path}))
	return nil
}
