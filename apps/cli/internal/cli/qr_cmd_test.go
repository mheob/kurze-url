package cli

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mheob/kurze-url/apps/cli/internal/apiclient"
)

// The two images the fake API serves. The PNG is not an image at all, but it
// starts like one and holds bytes that no text encoding survives, which is
// what a command that writes it through a string would corrupt.
const testQRSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 2 2"><path d="M0 0h1v1H0z"/></svg>`

var testQRPNG = []byte("\x89PNG\r\n\x1a\n\x00\xff\xfe\x80 not really an image")

// qrPath is the QR route of a link, as the router sees it.
func qrPath(link apiclient.Link) string {
	return "/v1/links/" + link.ID + "/qr"
}

// serveQR makes a link's QR route answer with the SVG, or with the PNG when
// the request asks for one, as the API does.
func (h *harness) serveQR(link apiclient.Link) {
	h.t.Helper()
	h.api.handle(http.MethodGet, qrPath(link), func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") == "png" {
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write(testQRPNG)
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = w.Write([]byte(testQRSVG))
	})
}

// qrQuery returns the query of the one QR request a link received.
func (h *harness) qrQuery(link apiclient.Link) url.Values {
	h.t.Helper()
	calls := h.api.callsTo(http.MethodGet, qrPath(link))
	require.Len(h.t, calls, 1)
	return calls[0].Query
}

// qrHarness is a signed-in person in team A, with the one link `sommerfest` on
// the shared host and its QR route, working in an empty directory of their
// own, which is returned.
func qrHarness(t *testing.T, config string) (*harness, string) {
	t.Helper()
	h := linkHarness(t, config, teamGruenwald)
	h.serveLinkSearch(teamGruenwald, lookupShared)
	h.serveQR(lookupShared)
	dir := t.TempDir()
	t.Chdir(dir)
	return h, dir
}

// readFile returns a file's content, or fails the test.
func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

// entries lists the names in a directory.
func entries(t *testing.T, dir string) []string {
	t.Helper()
	items, err := os.ReadDir(dir)
	require.NoError(t, err)
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Name())
	}
	return names
}

func TestQRWritesSlugDotFormat(t *testing.T) {
	h, dir := qrHarness(t, "team: sv-gruenwald\n")

	code := h.run("link", "qr", "sommerfest")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, testQRSVG, string(readFile(t, filepath.Join(dir, "sommerfest.svg"))))
	// SVG is always asked for by name, so the file's extension is known before
	// the request, and nothing else is sent: not a size, not a colour.
	require.Equal(t, url.Values{"format": {"svg"}}, h.qrQuery(lookupShared))
	require.Empty(t, h.stdout.String(), "the image is in the file, not on stdout")
	require.Equal(t, "QR code saved to sommerfest.svg\n", h.stderr.String())
}

// The name is the stored slug, whatever way the link was typed.
func TestQRNamesTheFileAfterTheStoredSlug(t *testing.T) {
	h, dir := qrHarness(t, "team: sv-gruenwald\n")

	require.Equal(t, 0, h.run("link", "qr", "HTTPS://GO.KURZE-URL.APP/SommerFest/?qr=1"), h.stderr.String())

	require.Equal(t, []string{"sommerfest.svg"}, entries(t, dir))
}

func TestQRFileIsNotWritableByOthers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no permission bits to read back")
	}
	h, dir := qrHarness(t, "team: sv-gruenwald\n")

	require.Equal(t, 0, h.run("link", "qr", "sommerfest"), h.stderr.String())

	info, err := os.Stat(filepath.Join(dir, "sommerfest.svg"))
	require.NoError(t, err)
	// 0644 less whatever the umask takes away: never more than that.
	require.Zero(t, info.Mode().Perm()&^0o644, info.Mode().String())
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm()&0o600, "readable and writable by its owner")
}

func TestQRSizeOnlyForPNG(t *testing.T) {
	h, dir := qrHarness(t, "team: sv-gruenwald\nqr:\n  size: 1024\n")

	require.Equal(t, 0, h.run("link", "qr", "sommerfest"), h.stderr.String())
	require.Equal(t, url.Values{"format": {"svg"}}, h.qrQuery(lookupShared),
		"a configured size is a PNG's, and the API refuses one on an SVG")
	require.Equal(t, []string{"sommerfest.svg"}, entries(t, dir))

	h, dir = qrHarness(t, "team: sv-gruenwald\nqr:\n  size: 1024\n")
	require.Equal(t, 0, h.run("link", "qr", "sommerfest", "--format", "png"), h.stderr.String())
	require.Equal(t, url.Values{"format": {"png"}, "size": {"1024"}}, h.qrQuery(lookupShared))
	require.Equal(t, testQRPNG, readFile(t, filepath.Join(dir, "sommerfest.png")))
}

// A size typed on the command line is a request for exactly that size, so
// quietly dropping it for an SVG would be a lie.
func TestQRExplicitSizeWithSVGIsAUsageError(t *testing.T) {
	for name, tc := range map[string]struct {
		config string
		args   []string
	}{
		"the built-in default":    {"team: sv-gruenwald\n", []string{"--size", "512"}},
		"the format flag":         {"team: sv-gruenwald\nqr:\n  format: png\n", []string{"--size", "512", "--format", "svg"}},
		"the global config":       {"team: sv-gruenwald\nqr:\n  format: svg\n", []string{"--size", "512"}},
		"the team's config":       {"team: sv-gruenwald\nteams:\n  sv-gruenwald:\n    qr:\n      format: svg\n", []string{"--size", "512"}},
		"the team beats a global": {"team: sv-gruenwald\nqr:\n  format: png\nteams:\n  sv-gruenwald:\n    qr:\n      format: svg\n", []string{"--size", "512"}},
	} {
		t.Run(name, func(t *testing.T) {
			// No QR route is served: a request for one fails the test.
			h := linkHarness(t, tc.config, teamGruenwald)
			h.serveLinkSearch(teamGruenwald, lookupShared)
			dir := t.TempDir()
			t.Chdir(dir)

			code := h.run(append([]string{"link", "qr", "sommerfest"}, tc.args...)...)

			require.Equal(t, 2, code)
			require.Empty(t, h.stdout.String())
			require.Equal(t,
				"kurze-url: --size only applies to PNG images. Add --format png, or leave out --size for an SVG.\n"+
					"Run 'kurze-url link qr --help' for usage.\n",
				h.stderr.String())
			require.Empty(t, entries(t, dir))
		})
	}
}

func TestQRExplicitSizeWithPNGFromTheConfigIsFine(t *testing.T) {
	h, _ := qrHarness(t, "team: sv-gruenwald\nqr:\n  format: png\n")

	require.Equal(t, 0, h.run("link", "qr", "sommerfest", "--size", "512"), h.stderr.String())

	require.Equal(t, url.Values{"format": {"png"}, "size": {"512"}}, h.qrQuery(lookupShared))
}

func TestQRPrecedence(t *testing.T) {
	const config = "team: sv-gruenwald\n" +
		"qr:\n  format: png\n  size: 256\n  fg: 'aaaaaa'\n  bg: 'bbbbbb'\n" +
		"teams:\n  sv-gruenwald:\n    qr:\n      size: 512\n      fg: 'cccccc'\n"
	const globalOnly = "team: sv-gruenwald\n" +
		"qr:\n  format: png\n  size: 256\n  fg: 'aaaaaa'\n  bg: 'bbbbbb'\n"

	for name, tc := range map[string]struct {
		config string
		args   []string
		want   url.Values
		file   string
	}{
		"team beats global, global fills the rest": {
			config, nil,
			url.Values{"format": {"png"}, "size": {"512"}, "fg": {"cccccc"}, "bg": {"bbbbbb"}},
			"sommerfest.png",
		},
		"flag beats team": {
			config, []string{"--fg", "#DDDDDD", "--size", "2048"},
			url.Values{"format": {"png"}, "size": {"2048"}, "fg": {"dddddd"}, "bg": {"bbbbbb"}},
			"sommerfest.png",
		},
		"flag format beats config, and a PNG's size stays out of an SVG": {
			config, []string{"--format", "svg"},
			url.Values{"format": {"svg"}, "fg": {"cccccc"}, "bg": {"bbbbbb"}},
			"sommerfest.svg",
		},
		"global alone": {
			globalOnly, nil,
			url.Values{"format": {"png"}, "size": {"256"}, "fg": {"aaaaaa"}, "bg": {"bbbbbb"}},
			"sommerfest.png",
		},
		"flag beats global": {
			globalOnly, []string{"--bg", "000000"},
			url.Values{"format": {"png"}, "size": {"256"}, "fg": {"aaaaaa"}, "bg": {"000000"}},
			"sommerfest.png",
		},
	} {
		t.Run(name, func(t *testing.T) {
			h, dir := qrHarness(t, tc.config)

			code := h.run(append([]string{"link", "qr", "sommerfest"}, tc.args...)...)

			require.Equal(t, 0, code, h.stderr.String())
			require.Equal(t, tc.want, h.qrQuery(lookupShared))
			require.Equal(t, []string{tc.file}, entries(t, dir))
		})
	}
}

// A colour typed in capitals, or kept in capitals in the file, reaches the API
// as six lower-case digits without a "#".
func TestQRSendsColoursLowerCaseWithoutAHash(t *testing.T) {
	h, _ := qrHarness(t, "team: sv-gruenwald\nqr:\n  bg: 'ABCDEF'\n")

	require.Equal(t, 0, h.run("link", "qr", "sommerfest", "--fg", "#1A3A6E"), h.stderr.String())

	query := h.qrQuery(lookupShared)
	require.Equal(t, "1a3a6e", query.Get("fg"))
	require.Equal(t, "abcdef", query.Get("bg"))
}

// Review Focus 5: a link found by its full URL in a team that is not the
// default one takes that team's QR section. The default team's is not even
// looked at.
func TestQRUsesTheOwningTeamsSettings(t *testing.T) {
	const config = "team: sv-gruenwald\n" +
		"teams:\n" +
		"  sv-gruenwald:\n    qr:\n      format: svg\n      fg: 'aaaaaa'\n" +
		"  tc-muster:\n    qr:\n      format: png\n      size: 640\n      fg: 'bbbbbb'\n"
	want := url.Values{"format": {"png"}, "size": {"640"}, "fg": {"bbbbbb"}}

	for name, args := range map[string][]string{
		"by its full URL":                 {"https://go.kurze-url.app/fest"},
		"by its slug and the team's flag": {"fest", "--team", "tc-muster"},
	} {
		t.Run(name, func(t *testing.T) {
			h := lookupHarness(t, config)
			h.serveQR(lookupFest)
			dir := t.TempDir()
			t.Chdir(dir)

			code := h.run(append([]string{"link", "qr"}, args...)...)

			require.Equal(t, 0, code, h.stderr.String())
			require.Equal(t, want, h.qrQuery(lookupFest))
			require.Equal(t, []string{"fest.png"}, entries(t, dir))
		})
	}
}

// The default team's QR section belongs to the default team's links. It is not
// a fallback for a link some other team owns, which has no section of its own
// and gets the global one, and here that is nothing.
func TestQRDefaultTeamSettingsDoNotLeakIntoAnotherTeamsLink(t *testing.T) {
	h := lookupHarness(t, "team: sv-gruenwald\nteams:\n  sv-gruenwald:\n    qr:\n      fg: 'aaaaaa'\n")
	h.serveQR(lookupFest)
	t.Chdir(t.TempDir())

	require.Equal(t, 0, h.run("link", "qr", "https://go.kurze-url.app/fest"), h.stderr.String())

	require.Equal(t, url.Values{"format": {"svg"}}, h.qrQuery(lookupFest))
}

func TestQRWritesToTheGivenFile(t *testing.T) {
	h, dir := qrHarness(t, "team: sv-gruenwald\n")

	code := h.run("link", "qr", "sommerfest", "--output", "poster.img")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, []string{"poster.img"}, entries(t, dir))
	require.Equal(t, testQRSVG, string(readFile(t, filepath.Join(dir, "poster.img"))))
	require.Equal(t, "QR code saved to poster.img\n", h.stderr.String())
}

func TestQRRefusesToOverwrite(t *testing.T) {
	// No QR route is served: a request for one fails the test.
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveLinkSearch(teamGruenwald, lookupShared)
	dir := t.TempDir()
	t.Chdir(dir)
	existing := filepath.Join(dir, "sommerfest.svg")
	const old = "an earlier code, longer than the one that would replace it, so a missing truncation shows"
	require.NoError(t, os.WriteFile(existing, []byte(old), 0o600))

	code := h.run("link", "qr", "sommerfest")

	require.Equal(t, 1, code)
	require.Empty(t, h.stdout.String())
	require.Equal(t, "kurze-url: sommerfest.svg already exists. Use --force to overwrite it.\n", h.stderr.String())
	require.Equal(t, old, string(readFile(t, existing)))
	require.Empty(t, h.api.callsTo(http.MethodGet, qrPath(lookupShared)), "no image is fetched for a file that would be refused")

	h.serveQR(lookupShared)
	h.resetOutput()

	code = h.run("link", "qr", "sommerfest", "--force")

	require.Equal(t, 0, code, h.stderr.String())
	require.Equal(t, testQRSVG, string(readFile(t, existing)), "overwritten and cut to the new length")
	require.Equal(t, "QR code saved to sommerfest.svg\n", h.stderr.String())
}

func TestQRRefusesToOverwriteTheGivenFile(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveLinkSearch(teamGruenwald, lookupShared)
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "poster.img"), []byte("mine"), 0o600))

	code := h.run("link", "qr", "sommerfest", "--output", "poster.img")

	require.Equal(t, 1, code)
	require.Equal(t, "kurze-url: poster.img already exists. Use --force to overwrite it.\n", h.stderr.String())
	require.Equal(t, "mine", string(readFile(t, filepath.Join(dir, "poster.img"))))
}

// The check before the request is a courtesy; the open with O_EXCL is what
// refuses. A file that appears while the image is on its way is not clobbered.
func TestQRDoesNotOverwriteAFileThatAppearsDuringTheRequest(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveLinkSearch(teamGruenwald, lookupShared)
	dir := t.TempDir()
	t.Chdir(dir)
	h.api.handle(http.MethodGet, qrPath(lookupShared), func(w http.ResponseWriter, _ *http.Request) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "sommerfest.svg"), []byte("someone else's"), 0o600))
		_, _ = w.Write([]byte(testQRSVG))
	})

	code := h.run("link", "qr", "sommerfest")

	require.Equal(t, 1, code)
	require.Equal(t, "kurze-url: sommerfest.svg already exists. Use --force to overwrite it.\n", h.stderr.String())
	require.Equal(t, "someone else's", string(readFile(t, filepath.Join(dir, "sommerfest.svg"))))
}

func TestQROutputDashWritesToStdout(t *testing.T) {
	for name, tc := range map[string]struct {
		args []string
		want []byte
	}{
		"svg": {[]string{"--output", "-"}, []byte(testQRSVG)},
		"png": {[]string{"--format", "png", "--output", "-"}, testQRPNG},
	} {
		t.Run(name, func(t *testing.T) {
			h, dir := qrHarness(t, "team: sv-gruenwald\n")

			code := h.run(append([]string{"link", "qr", "sommerfest"}, tc.args...)...)

			require.Equal(t, 0, code, h.stderr.String())
			// Nothing but the image, byte for byte: not a newline after it
			// either, because a PNG piped to a viewer must stay a PNG.
			require.Equal(t, tc.want, h.stdout.Bytes())
			require.Empty(t, h.stderr.String(), "no message either, so a pipe and a terminal both see only the image")
			require.Empty(t, entries(t, dir), "and no file")
		})
	}
}

// A file that already has the default name is no obstacle to writing to
// stdout, and --force is not needed.
func TestQROutputDashIgnoresAnExistingFile(t *testing.T) {
	h, dir := qrHarness(t, "team: sv-gruenwald\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sommerfest.svg"), []byte("kept"), 0o600))

	require.Equal(t, 0, h.run("link", "qr", "sommerfest", "--output", "-"), h.stderr.String())

	require.Equal(t, testQRSVG, h.stdout.String())
	require.Equal(t, "kept", string(readFile(t, filepath.Join(dir, "sommerfest.svg"))))
}

func TestQRExplainsLowContrast(t *testing.T) {
	h := linkHarness(t, "team: sv-gruenwald\n", teamGruenwald)
	h.serveLinkSearch(teamGruenwald, lookupShared)
	h.api.problem(http.MethodGet, qrPath(lookupShared), http.StatusUnprocessableEntity, "the colours are too close",
		apiclient.ProblemDetail{Location: "query.fg", Value: "low_contrast"})
	dir := t.TempDir()
	t.Chdir(dir)

	code := h.run("link", "qr", "sommerfest", "--fg", "777777", "--bg", "888888")

	require.Equal(t, 1, code)
	require.Empty(t, h.stdout.String())
	require.Equal(t, "kurze-url: The two colours are too close together for a camera to read the code.\n", h.stderr.String())
	require.Empty(t, entries(t, dir), "a refused request leaves no empty file behind")
}

func TestQRRefusalsSpeakGerman(t *testing.T) {
	h, dir := qrHarness(t, "language: de\nteam: sv-gruenwald\n")

	require.Equal(t, 0, h.run("link", "qr", "sommerfest"), h.stderr.String())
	require.Equal(t, "QR-Code gespeichert unter sommerfest.svg\n", h.stderr.String())

	h.resetOutput()
	require.Equal(t, 1, h.run("link", "qr", "sommerfest"))
	require.Equal(t, "kurze-url: sommerfest.svg gibt es schon. Mit --force wird die Datei überschrieben.\n", h.stderr.String())

	h.resetOutput()
	require.Equal(t, 2, h.run("link", "qr", "sommerfest", "--size", "512", "--output", "x.svg"))
	require.Contains(t, h.stderr.String(), "--size gilt nur für PNG-Bilder.")
	require.Equal(t, []string{"sommerfest.svg"}, entries(t, dir))
}

func TestQRReportsAPathThatCannotBeWritten(t *testing.T) {
	h, dir := qrHarness(t, "team: sv-gruenwald\n")

	code := h.run("link", "qr", "sommerfest", "--output", filepath.Join("no", "such", "dir", "x.svg"))

	require.Equal(t, 1, code)
	require.Empty(t, h.stdout.String())
	require.Contains(t, h.stderr.String(), filepath.Join("no", "such", "dir", "x.svg"))
	require.Empty(t, entries(t, dir))
}

// A flag outside what it documents is a usage error, judged before the config
// file is read and before a single request.
func TestQRRejectsFlagValuesOutsideTheirSet(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"format", []string{"--format", "jpg"}, "--format must be svg or png, not jpg."},
		{"format in capitals", []string{"--format", "PNG"}, "--format must be svg or png, not PNG."},
		{"size below", []string{"--format", "png", "--size", "63"}, "--size must be a whole number from 64 to 2048, not 63."},
		{"size above", []string{"--format", "png", "--size", "2049"}, "--size must be a whole number from 64 to 2048, not 2049."},
		{"size zero", []string{"--format", "png", "--size", "0"}, "--size must be a whole number from 64 to 2048, not 0."},
		{"size negative", []string{"--format", "png", "--size", "-5"}, "--size must be a whole number from 64 to 2048, not -5."},
		{"fg too short", []string{"--fg", "12345"}, "--fg must be six hexadecimal digits such as 1a3a6e, not 12345."},
		{"fg not hex", []string{"--fg", "12345g"}, "--fg must be six hexadecimal digits such as 1a3a6e, not 12345g."},
		{"fg three digits", []string{"--fg", "#abc"}, "--fg must be six hexadecimal digits such as 1a3a6e, not #abc."},
		{"fg two hashes", []string{"--fg", "##123456"}, "--fg must be six hexadecimal digits such as 1a3a6e, not ##123456."},
		{"bg too long", []string{"--bg", "1234567"}, "--bg must be six hexadecimal digits such as 1a3a6e, not 1234567."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, dir := qrHarness(t, "team: sv-gruenwald\n")

			code := h.run(append([]string{"link", "qr", "sommerfest"}, tc.args...)...)

			require.Equal(t, 2, code)
			require.Empty(t, h.stdout.String())
			require.Equal(t,
				"kurze-url: "+tc.want+"\nRun 'kurze-url link qr --help' for usage.\n",
				h.stderr.String())
			require.Empty(t, h.api.requests(), "not even /v1/me is asked")
			require.Empty(t, entries(t, dir))
		})
	}
}

func TestQRSizeMustBeANumber(t *testing.T) {
	h, _ := qrHarness(t, "team: sv-gruenwald\n")

	require.Equal(t, 2, h.run("link", "qr", "sommerfest", "--size", "large"))
	require.Empty(t, h.api.requests())
}

func TestQRAcceptsTheEdgesOfItsRanges(t *testing.T) {
	for _, size := range []string{"64", "2048"} {
		t.Run(size, func(t *testing.T) {
			h, _ := qrHarness(t, "team: sv-gruenwald\n")

			code := h.run("link", "qr", "sommerfest", "--format", "png", "--size", size, "--fg", "000000", "--bg", "#FFFFFF")

			require.Equal(t, 0, code, h.stderr.String())
			require.Equal(t,
				url.Values{"format": {"png"}, "size": {size}, "fg": {"000000"}, "bg": {"ffffff"}},
				h.qrQuery(lookupShared))
		})
	}
}

func TestQRNeedsExactlyOneLink(t *testing.T) {
	for _, args := range [][]string{{"link", "qr"}, {"link", "qr", "a", "b"}} {
		t.Run(fmt.Sprint(args), func(t *testing.T) {
			h := newHarness(t)

			require.Equal(t, 2, h.run(args...))

			require.Contains(t, h.stderr.String(), "Run 'kurze-url link qr --help' for usage.")
			require.Empty(t, h.api.requests())
		})
	}
}
