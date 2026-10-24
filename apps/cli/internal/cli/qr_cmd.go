package cli

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/mheob/kurze-url/apps/cli/internal/apiclient"
	"github.com/mheob/kurze-url/apps/cli/internal/config"
	"github.com/mheob/kurze-url/apps/cli/internal/i18n"
)

const (
	// qrSVG and qrPNG are the two formats the API renders. SVG is its default,
	// and the CLI sends it by name all the same, so the extension of the file
	// is known before the request and never depends on a default the API could
	// change.
	qrSVG = "svg"
	qrPNG = "png"

	// qrMinSize and qrMaxSize bound --size, in pixels. They are the API's own
	// bounds, and the config file's: a value this command accepts is one the
	// API will not refuse for its range.
	qrMinSize = 64
	qrMaxSize = 2048

	// qrStdout is the --output value that means standard output.
	qrStdout = "-"

	// qrFileMode is the mode a new image file is created with, before the
	// umask takes its share. The image is not a secret: it is what gets
	// printed on a poster.
	qrFileMode fs.FileMode = 0o644
)

// qrColour is what --fg and --bg accept: six hexadecimal digits, with the
// leading "#" of a CSS colour tolerated because that is how people write and
// paste one.
var qrColour = regexp.MustCompile(`^#?[0-9a-fA-F]{6}$`)

// qrOptions are the flags of `link qr`.
type qrOptions struct {
	format, fg, bg, output, domain, team string
	size                                 int
	// sizeGiven is whether --size was on the command line. Its zero value is
	// not a size anyone may give, so it cannot also mean "not given": a typed
	// --size 0 is a mistake to report and not a request to use the default.
	sizeGiven bool
	force     bool
}

// newLinkQRCmd saves a link's QR code as an image.
func newLinkQRCmd(d *Deps) *cobra.Command {
	var o qrOptions
	cmd := &cobra.Command{
		Use:   "qr <slug|short-url>",
		Short: "Save a link's QR code as an SVG or PNG file",
		Long: "Saves the QR code of a link as <slug>.svg, or <slug>.png with --format png, in the\n" +
			"current directory, or as the file --output names. An existing file is not overwritten\n" +
			"without --force. With --output - the image goes to standard output and nothing else\n" +
			"does, so it can be piped:\n\n" +
			"  kurze-url link qr sommerfest --format png --size 1024 --output - | lpr\n\n" +
			"The code always holds the short URL, never the destination, so changing a link's\n" +
			"destination later does not invalidate a printed code.\n\n" +
			"Format, size and the two colours default to the `qr` settings of the team the link\n" +
			"belongs to, then to the global ones (see `kurze-url config`). --size is for PNG only.\n" +
			"The API refuses two colours too close together for a camera to tell apart.",
		Args:              usageArgs(cobra.ExactArgs(1)),
		ValidArgsFunction: completeLinkSlugs(d),
		RunE: func(cmd *cobra.Command, args []string) error {
			o.sizeGiven = cmd.Flags().Changed("size")
			// What the flags alone can show is wrong is a usage error before
			// the config file is read, like a --limit out of range.
			if err := checkQRFlags(d, o); err != nil {
				return err
			}
			a, err := newApp(d)
			if err != nil {
				return err
			}
			return a.linkQR(cmd.Context(), args[0], o)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&o.format, "format", "", "svg or png (default: the qr.format setting, else svg)")
	flags.IntVar(&o.size, "size", 0, "the largest side in pixels, from 64 to 2048, PNG only (default: the qr.size setting)")
	flags.StringVar(&o.fg, "fg", "", "the code's colour as RRGGBB (default: the qr.fg setting)")
	flags.StringVar(&o.bg, "bg", "", "the background colour as RRGGBB (default: the qr.bg setting)")
	flags.StringVar(&o.output, "output", "", "the file to write, or - for standard output (default: <slug>.<format>)")
	flags.BoolVar(&o.force, "force", false, "overwrite the file if it exists")
	addLookupFlags(cmd, d, &o.domain, &o.team)
	registerFlagCompletion(cmd, "format", fixedValues(qrSVG, qrPNG))
	registerFlagCompletion(cmd, "size", noValues)
	registerFlagCompletion(cmd, "fg", noValues)
	registerFlagCompletion(cmd, "bg", noValues)
	return cmd
}

// checkQRFlags refuses a flag value outside what the flag documents, as a
// usage error in the person's language.
//
// It words the refusal through catalogFor because it runs before an App
// exists: the same reasoning as checkLimit. What it cannot judge is --size
// against a format that comes from the config, which needs the link's team and
// is left to linkQR.
func checkQRFlags(d *Deps, o qrOptions) error {
	if o.format != "" && o.format != qrSVG && o.format != qrPNG {
		text := catalogFor(d).T("qr.formatInvalid", i18n.Args{"value": o.format})
		return &UsageError{Err: errors.New(text)}
	}
	if o.sizeGiven && (o.size < qrMinSize || o.size > qrMaxSize) {
		text := catalogFor(d).T("qr.sizeRange", i18n.Args{"value": strconv.Itoa(o.size)})
		return &UsageError{Err: errors.New(text)}
	}
	if o.fg != "" && !qrColour.MatchString(o.fg) {
		text := catalogFor(d).T("qr.colourInvalid", i18n.Args{"flag": "--fg", "value": o.fg})
		return &UsageError{Err: errors.New(text)}
	}
	if o.bg != "" && !qrColour.MatchString(o.bg) {
		text := catalogFor(d).T("qr.colourInvalid", i18n.Args{"flag": "--bg", "value": o.bg})
		return &UsageError{Err: errors.New(text)}
	}
	return nil
}

// qrQuery works out the request for a link's QR code: each of format, size,
// foreground and background is the flag, else the setting of the team that
// owns the link, else the global setting, else nothing at all and the API's
// own default.
//
// The owner is the link's own team and not the one --team named or the default
// one: a link found by its short URL in a team that is not the default must
// not take the colours of the default team's section. Format is the one
// setting with a default of its own here, SVG, for the reason qrSVG gives.
//
// Size goes with PNG only. A size is a count of pixels, and an SVG has none:
// the API refuses a size on one with a 422. A size the person typed is
// therefore a usage error when the image is an SVG, because quietly dropping
// it would hand them a file that is not what they asked for, while a size from
// the config is a preference that meets a format it does not fit, and it
// simply is not sent. Colours go out as the API takes them, lower-case and
// without a "#".
func (a *App) qrQuery(o qrOptions, owner apiclient.Membership) (apiclient.QRQuery, error) {
	format := a.settingOr(o.format, config.QRFormat, owner)
	if format == "" {
		format = qrSVG
	}
	query := apiclient.QRQuery{
		Format: format,
		FG:     qrColourValue(a.settingOr(o.fg, config.QRFG, owner)),
		BG:     qrColourValue(a.settingOr(o.bg, config.QRBG, owner)),
	}

	switch {
	case format == qrPNG && o.sizeGiven:
		query.Size = o.size
	case format == qrPNG:
		// Load has judged a size in the file already, and an unset one is "",
		// which is not a number and is not sent.
		query.Size, _ = strconv.Atoi(a.settingOr("", config.QRSize, owner))
	case o.sizeGiven:
		return apiclient.QRQuery{}, &UsageError{Err: errors.New(a.Cat.T("qr.sizeNeedsPNG"))}
	}
	return query, nil
}

// qrColourValue puts a colour the way the API takes it.
func qrColourValue(colour string) string {
	return strings.ToLower(strings.TrimPrefix(colour, "#"))
}

// linkQR fetches a link's QR code and saves it, or writes it to stdout.
//
// The image is requested only once everything that can refuse the run has had
// its say: the link, the settings, and whether the file may be written. Then a
// refused request, such as colours too close together, leaves no empty file
// behind, and a file that would be refused does not cost a request. The file is
// created after the image is in hand, for the same reason.
//
// With --output - the bytes are the whole of stdout, with no newline after
// them and no message, because a PNG piped to a viewer must stay a PNG. In
// every other case stdout is empty and the confirmation goes to stderr.
func (a *App) linkQR(ctx context.Context, arg string, o qrOptions) error {
	link, owner, err := a.findLink(ctx, arg, o.domain, o.team)
	if err != nil {
		return err
	}
	query, err := a.qrQuery(o, owner)
	if err != nil {
		return err
	}

	// The slug is lower-case letters, digits, hyphens and underscores, which is
	// safe in a file name on every system this runs on.
	file := o.output
	if file == "" {
		file = link.Slug + "." + query.Format
	}
	toStdout := file == qrStdout
	if !toStdout && !o.force {
		if err := a.checkFree(file); err != nil {
			return err
		}
	}

	image, err := a.API.LinkQR(ctx, link.ID, query)
	if err != nil {
		return err
	}
	if toStdout {
		if _, err := a.d.Stdout.Write(image); err != nil {
			return fmt.Errorf("write the QR code: %w", err)
		}
		return nil
	}
	if err := a.saveQR(file, image, o.force); err != nil {
		return err
	}
	a.notice(a.Cat.T("qr.written", i18n.Args{"file": file}))
	return nil
}

// checkFree refuses a path where something already is, before any request is
// made. It looks at the path itself and not at what a link there points to
// (Lstat), so a symbolic link, dangling or not, counts as being in the way.
//
// It is only the early answer. saveQR's O_EXCL is what actually decides, which
// is why a file that appears in between is still not overwritten.
func (a *App) checkFree(file string) error {
	_, err := os.Lstat(file)
	switch {
	case err == nil:
		return a.existsError(file)
	case errors.Is(err, fs.ErrNotExist):
		return nil
	}
	return fmt.Errorf("check the QR code file: %w", err)
}

// saveQR writes the image to file. Without force it creates the file with
// O_EXCL, so it fails rather than replace one that is there, whoever made it
// and whenever. With force it opens the file and cuts it to nothing first.
//
// A write that fails halfway leaves a file nobody wants, and when this call
// created it, it is removed again. A forced overwrite has already destroyed
// the old content and has nothing to go back to.
func (a *App) saveQR(file string, image []byte, force bool) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(file, flags, qrFileMode)
	if errors.Is(err, fs.ErrExist) {
		return a.existsError(file)
	}
	if err != nil {
		return fmt.Errorf("save the QR code: %w", err)
	}

	_, err = f.Write(image)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		if !force {
			_ = os.Remove(file)
		}
		return fmt.Errorf("save the QR code: %w", err)
	}
	return nil
}

// existsError is the refusal to overwrite a file, with the way out.
func (a *App) existsError(file string) error {
	return errors.New(a.Cat.T("qr.exists", i18n.Args{"file": file}))
}
