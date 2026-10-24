#!/bin/sh
# Installs the kurze-url CLI on Linux and macOS:
#
#   curl -fsSL https://raw.githubusercontent.com/mheob/kurze-url/main/apps/cli/install.sh | sh
#
# It downloads the archive for this machine from a GitHub Release, checks it
# against the release's checksums.txt, and puts the binary in ~/.local/bin.
# Windows has its own installer, install.ps1, next to this file.
#
# Environment:
#
#   KURZE_URL_INSTALL_DIR   where to put the binary (default: $HOME/.local/bin)
#   KURZE_URL_VERSION       a release tag such as v0.1.0 (default: the latest)
#   KURZE_URL_DOWNLOAD_URL  the base URL that holds the archive and
#                           checksums.txt, which wins over KURZE_URL_VERSION.
#                           An https URL, or a file:// URL for a local
#                           directory. For mirrors, and for
#                           scripts/install_test.sh.
#
# Exits 0 on success and 1 on any failure. Every message goes to stderr, in
# English or in German by the CLI's own rule (the first of LC_ALL, LC_MESSAGES
# and LANG that is set decides, and German means it starts with "de"). The
# only thing on stdout is the `version` line of the binary it installed.
#
# The whole body is the function `main`, called on the last line. `curl | sh`
# runs whatever has arrived, so a download that stops halfway would otherwise
# run half a script. This way a cut inside `main` is a syntax error, and a cut
# before its call has only defined functions. Keep it that way: nothing but
# `set` and function definitions may sit above that last line.
#
# POSIX sh, and clean under shellcheck. scripts/install_test.sh is its test.

set -eu

# msg KEY [ARG...] prints one message to stderr in the language main chose.
# Every sentence this script says is written here, once per language, and a
# key shared by both languages starts with *: instead.
msg() {
	case $lang:$1 in
	en:unsupported_os) text="Unsupported operating system: $2. This installer supports Linux and macOS." ;;
	de:unsupported_os) text="Nicht unterstütztes Betriebssystem: $2. Dieses Installationsskript unterstützt Linux und macOS." ;;
	en:windows)
		text="This installer is for Linux and macOS. On Windows, run this in PowerShell:
  irm https://raw.githubusercontent.com/mheob/kurze-url/main/apps/cli/install.ps1 | iex
Or download the archive from $2"
		;;
	de:windows)
		text="Dieses Installationsskript ist für Linux und macOS. Unter Windows führst du in PowerShell Folgendes aus:
  irm https://raw.githubusercontent.com/mheob/kurze-url/main/apps/cli/install.ps1 | iex
Oder lade das Archiv von $2 herunter."
		;;
	en:unsupported_arch) text="Unsupported architecture: $2. kurze-url is built for x86_64 (amd64) and arm64." ;;
	de:unsupported_arch) text="Nicht unterstützte Architektur: $2. kurze-url gibt es für x86_64 (amd64) und arm64." ;;
	en:no_downloader) text="Neither curl nor wget was found. Install one of them and run this script again." ;;
	de:no_downloader) text="Weder curl noch wget gefunden. Installiere eines von beiden und führe dieses Skript erneut aus." ;;
	en:no_hasher) text="Neither sha256sum nor shasum was found, so the download cannot be verified. Install one of them and run this script again." ;;
	de:no_hasher) text="Weder sha256sum noch shasum gefunden, der Download lässt sich also nicht prüfen. Installiere eines von beiden und führe dieses Skript erneut aus." ;;
	en:no_tar) text="tar was not found. Install it and run this script again." ;;
	de:no_tar) text="tar nicht gefunden. Installiere es und führe dieses Skript erneut aus." ;;
	en:no_home) text="HOME is not set. Set KURZE_URL_INSTALL_DIR to the directory to install into." ;;
	de:no_home) text="HOME ist nicht gesetzt. Setze KURZE_URL_INSTALL_DIR auf das Verzeichnis, in das installiert werden soll." ;;
	en:no_tmp) text="Could not create a temporary directory." ;;
	de:no_tmp) text="Es konnte kein temporäres Verzeichnis angelegt werden." ;;
	en:url_not_https) text="Refusing $2: only https URLs are accepted, and file URLs in KURZE_URL_DOWNLOAD_URL." ;;
	de:url_not_https) text="$2 wird abgelehnt: Es werden nur https-URLs akzeptiert, und file-URLs in KURZE_URL_DOWNLOAD_URL." ;;
	en:downloading) text="Downloading $2 ..." ;;
	de:downloading) text="Lade $2 herunter ..." ;;
	en:download_failed) text="Download failed: $2" ;;
	de:download_failed) text="Download fehlgeschlagen: $2" ;;
	en:no_checksum) text="checksums.txt has no entry for $2. Nothing was installed." ;;
	de:no_checksum) text="In checksums.txt gibt es keinen Eintrag für $2. Es wurde nichts installiert." ;;
	en:hash_failed) text="Could not compute the checksum of $2. Nothing was installed." ;;
	de:hash_failed) text="Die Prüfsumme von $2 konnte nicht berechnet werden. Es wurde nichts installiert." ;;
	en:checksum_mismatch) text="Checksum mismatch for $2: expected $3, got $4. Nothing was installed." ;;
	de:checksum_mismatch) text="Die Prüfsumme von $2 stimmt nicht überein: erwartet $3, erhalten $4. Es wurde nichts installiert." ;;
	en:extract_failed) text="Could not extract kurze-url from $2. Nothing was installed." ;;
	de:extract_failed) text="kurze-url konnte nicht aus $2 entpackt werden. Es wurde nichts installiert." ;;
	en:install_failed) text="Could not install into $2." ;;
	de:install_failed) text="Die Installation in $2 ist fehlgeschlagen." ;;
	en:installed) text="Installed kurze-url to $2" ;;
	de:installed) text="kurze-url wurde installiert nach $2" ;;
	en:run_failed) text="The installed $2 does not run on this machine." ;;
	de:run_failed) text="Das installierte $2 lässt sich auf diesem Rechner nicht ausführen." ;;
	en:path_hint) text="$2 is not in your PATH. To run kurze-url from anywhere, add this line to ~/$3:" ;;
	de:path_hint) text="$2 ist nicht in deinem PATH. Damit du kurze-url überall aufrufen kannst, füge diese Zeile zu ~/$3 hinzu:" ;;
	*:path_export) text="  export PATH=\"$2:\$PATH\"" ;;
	en:completion_hint) text="To turn on tab completion, run: kurze-url completion install" ;;
	de:completion_hint) text="Um die Tab-Vervollständigung einzuschalten, führe aus: kurze-url completion install" ;;
	*) text=$1 ;;
	esac
	printf '%s\n' "$text" >&2
}

# die KEY [ARG...] says why and ends the script with status 1.
die() {
	msg "$@"
	exit 1
}

# cleanup runs on every way out, and must not turn a success into a failure.
cleanup() {
	if [ -n "$tmp" ]; then
		rm -rf "$tmp" || :
	fi
	if [ -n "$staged" ]; then
		rm -f "$staged" || :
	fi
}

# fetch URL DEST downloads one file with the tool main found. main has
# refused any URL that does not start on https (or file), and curl is told to
# follow a redirect only to https as well: GitHub sends a release download to
# another host, and an http redirect would hand the archive and its checksum
# to whoever sits on the path. install.ps1 checks where its download ended up
# for the same reason.
#
# The wget fallback has no such protection. GNU wget's --https-only governs
# recursive retrieval alone, and a plain fetch that begins on https still
# follows a redirect to http (tried against wget 1.25); BusyBox's wget does not
# know the option and exits with an error. So a machine with wget and no curl
# is protected only against a start URL that is not https.
fetch() {
	if [ "$fetcher" = curl ]; then
		curl -fsSL --proto '=https,file' --proto-redir '=https' -o "$2" "$1"
	else
		wget -qO "$2" "$1"
	fi
}

# sha256_of FILE prints FILE's SHA-256 as hex digits, with the tool main found.
sha256_of() {
	if [ "$hasher" = sha256sum ]; then
		sum_line=$(sha256sum "$1")
	else
		sum_line=$(shasum -a 256 "$1")
	fi
	printf '%s\n' "${sum_line%% *}"
}

main() {
	# German when the first of these that is set starts with "de", the way
	# the CLI decides, so LC_ALL=C keeps a German LANG out of it.
	lang=en
	for locale in "${LC_ALL:-}" "${LC_MESSAGES:-}" "${LANG:-}"; do
		if [ -n "$locale" ]; then
			case $locale in
			[Dd][Ee]*) lang=de ;;
			esac
			break
		fi
	done

	repo_url=https://github.com/mheob/kurze-url

	os=$(uname -s)
	case $os in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	MINGW* | MSYS* | CYGWIN*) die windows "$repo_url/releases/latest" ;;
	*) die unsupported_os "$os" ;;
	esac

	arch=$(uname -m)
	case $arch in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) die unsupported_arch "$arch" ;;
	esac

	if command -v curl >/dev/null 2>&1; then
		fetcher=curl
	elif command -v wget >/dev/null 2>&1; then
		fetcher=wget
	else
		die no_downloader
	fi
	if command -v sha256sum >/dev/null 2>&1; then
		hasher=sha256sum
	elif command -v shasum >/dev/null 2>&1; then
		hasher=shasum
	else
		die no_hasher
	fi
	command -v tar >/dev/null 2>&1 || die no_tar

	dir=${KURZE_URL_INSTALL_DIR:-}
	if [ -z "$dir" ]; then
		[ -n "${HOME:-}" ] || die no_home
		dir=$HOME/.local/bin
	fi
	# The PATH hint at the end compares and prints this, so make it absolute
	# and without a trailing slash first.
	case $dir in
	/*) ;;
	*) dir=$PWD/$dir ;;
	esac
	case $dir in
	?*/) dir=${dir%/} ;;
	esac

	# The archive names carry no version, so the latest release's URL is a
	# stable one. A version names its own release instead, and an explicit
	# base URL beats both.
	if [ -n "${KURZE_URL_DOWNLOAD_URL:-}" ]; then
		base=${KURZE_URL_DOWNLOAD_URL%/}
	elif [ -n "${KURZE_URL_VERSION:-}" ]; then
		base=$repo_url/releases/download/$KURZE_URL_VERSION
	else
		base=$repo_url/releases/latest/download
	fi
	# The checksum comes from the same place as the archive, so over plain
	# http whoever can change one can change both. Only https is accepted,
	# and file for a directory on this machine.
	case $base in
	[Hh][Tt][Tt][Pp][Ss]://* | [Ff][Ii][Ll][Ee]://*) ;;
	*) die url_not_https "$base" ;;
	esac
	asset=kurze-url_${os}_${arch}.tar.gz

	tmp=
	staged=
	trap cleanup EXIT
	# A signal ends the script through the exit trap too, or the download
	# directory would stay behind.
	trap 'exit 1' HUP INT TERM
	tmp=$(mktemp -d "${TMPDIR:-/tmp}/kurze-url.XXXXXXXX") || die no_tmp

	msg downloading "$asset"
	fetch "$base/$asset" "$tmp/$asset" || die download_failed "$base/$asset"
	fetch "$base/checksums.txt" "$tmp/checksums.txt" || die download_failed "$base/checksums.txt"

	# checksums.txt lines are "<sha256>  <name>", and sha256sum marks a file
	# it read in binary mode with a "*" before the name.
	expected=
	digest=
	while read -r digest name || [ -n "$digest" ]; do
		name=${name#\*}
		if [ "$name" = "$asset" ]; then
			expected=$digest
			break
		fi
	done <"$tmp/checksums.txt"
	[ -n "$expected" ] || die no_checksum "$asset"
	actual=$(sha256_of "$tmp/$asset") || die hash_failed "$asset"
	[ "$actual" = "$expected" ] || die checksum_mismatch "$asset" "$expected" "$actual"

	# Only now is anything opened. Only the binary is taken out of the
	# archive, and nothing is asked of its owner or mode: the copy below is
	# made by this user and gets its mode from chmod.
	mkdir "$tmp/extract" || die extract_failed "$asset"
	tar -xzf "$tmp/$asset" -C "$tmp/extract" kurze-url || die extract_failed "$asset"
	[ -f "$tmp/extract/kurze-url" ] || die extract_failed "$asset"

	# The binary lands under a temporary name inside the install directory
	# and is renamed over kurze-url. A rename replaces the directory entry
	# in one step, so a running kurze-url keeps its old file, where copying
	# over it would fail with "text file busy" on Linux.
	mkdir -p "$dir" || die install_failed "$dir"
	staged=$(mktemp "$dir/.kurze-url.XXXXXXXX") || die install_failed "$dir"
	cp "$tmp/extract/kurze-url" "$staged" || die install_failed "$dir"
	chmod 0755 "$staged" || die install_failed "$dir"
	mv -f "$staged" "$dir/kurze-url" || die install_failed "$dir"

	msg installed "$dir/kurze-url"
	"$dir/kurze-url" version || die run_failed "$dir/kurze-url"

	case ":$PATH:" in
	*":$dir:"*) ;;
	*)
		case ${SHELL:-} in
		*/zsh) profile=.zshrc ;;
		*/bash) profile=.bashrc ;;
		*) profile=.profile ;;
		esac
		msg path_hint "$dir" "$profile"
		msg path_export "$dir"
		;;
	esac

	# Last, so it is what the person reads last. It only names the command:
	# `kurze-url completion install` asks before it touches a shell's
	# configuration, and this script never does.
	msg completion_hint
}

main "$@"
