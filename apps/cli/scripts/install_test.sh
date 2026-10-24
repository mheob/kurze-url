#!/bin/sh
# Self-test for install.sh. Plain POSIX sh, no framework, and no network: the
# installer downloads from a file:// directory holding a fake release, and the
# cases that must not download at all put a curl shim first on PATH. It runs
# on every pull request in ci-cli.yml (Ubuntu's dash and macOS's sh), and
# locally with
#
#   sh apps/cli/scripts/install_test.sh
#
# Every case prints `ok NAME` or `FAIL NAME: reason`, and the script exits 1
# if any case failed, after running all of them.
#
# Optional environment, for trying the script under another shell or another
# file:
#
#   INSTALL_SH        the installer to test (default: ../install.sh)
#   INSTALL_TEST_SH   the shell that runs it (default: sh)
#
# Each case runs in a subshell with a directory of its own, which is its HOME
# and holds its install directory, its TMPDIR and its shims. The installer
# runs under `env -i`, so nothing of the developer's environment reaches it:
# a German LANG on this machine cannot turn an English assertion German.

set -u

here=$(cd "$(dirname "$0")" && pwd)
install_sh=${INSTALL_SH:-$here/../install.sh}
sh_bin=$(command -v "${INSTALL_TEST_SH:-sh}")

work=$(mktemp -d "${TMPDIR:-/tmp}/kurze-url-install-test.XXXXXXXX")
trap 'rm -rf "$work"' EXIT

# The platform this runs on, spelled the way a release names its archives.
# Cases that do not simulate another platform use the real `uname`, so both
# CI operating systems exercise the installer's own detection.
case $(uname -s) in
Linux) host_os=linux ;;
Darwin) host_os=darwin ;;
*)
	echo "install_test.sh runs on Linux and macOS only" >&2
	exit 1
	;;
esac
case $(uname -m) in
x86_64 | amd64) host_arch=amd64 ;;
arm64 | aarch64) host_arch=arm64 ;;
*)
	echo "install_test.sh runs on amd64 and arm64 only" >&2
	exit 1
	;;
esac
host_asset=kurze-url_${host_os}_${host_arch}.tar.gz

fake_version_line='kurze-url 9.9.9 (abc1234, 2026-10-24T00:00:00Z)'

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		out=$(sha256sum "$1")
	else
		out=$(shasum -a 256 "$1")
	fi
	printf '%s\n' "${out%% *}"
}

# build_release DIR OS ARCH VERSION packs a fake release into DIR: the archive
# for OS and ARCH, holding a shell script that prints a version line the way
# the real binary does, and a checksums.txt line for it. A second call with
# another OS or ARCH adds a second archive to the same DIR.
build_release() {
	mkdir -p "$1" "$work/stage"
	printf '#!/bin/sh\necho "kurze-url %s (abc1234, 2026-10-24T00:00:00Z)"\n' "$4" >"$work/stage/kurze-url"
	chmod 755 "$work/stage/kurze-url"
	echo MIT >"$work/stage/LICENSE"
	tar -czf "$1/kurze-url_$2_$3.tar.gz" -C "$work/stage" kurze-url LICENSE
	rm -rf "$work/stage"
	printf '%s  %s\n' "$(sha256_of "$1/kurze-url_$2_$3.tar.gz")" "kurze-url_$2_$3.tar.gz" >>"$1/checksums.txt"
}

# The release most cases install from. It is read-only: a case that needs a
# different one copies it, or builds its own, inside its directory.
release=$work/release
build_release "$release" "$host_os" "$host_arch" 9.9.9

# A uname that answers FAKE_UNAME_S and FAKE_UNAME_M when they are set and
# the real one otherwise.
shims=$work/shims
mkdir -p "$shims"
real_uname=$(command -v uname)
cat >"$shims/uname" <<EOF
#!/bin/sh
case "\${1:-}:\${FAKE_UNAME_S:-}:\${FAKE_UNAME_M:-}" in
-s:?*:*) printf '%s\n' "\$FAKE_UNAME_S"; exit 0 ;;
-m:*:?*) printf '%s\n' "\$FAKE_UNAME_M"; exit 0 ;;
esac
exec '$real_uname' "\$@"
EOF
chmod 755 "$shims/uname"

# entries_of DIR prints the names inside DIR, one per line, sorted; nothing
# for a directory that is empty or does not exist.
entries_of() {
	find "$1" -mindepth 1 -maxdepth 1 2>/dev/null | sed 's|.*/||' | LC_ALL=C sort
}

# finish is the exit trap of every case: it reports the case that ended
# without `pass` or `fail`, which means a command failed under `set -e`.
finish() {
	case $state in
	passed) printf 'ok %s\n' "$name" ;;
	failed) ;;
	*)
		printf 'FAIL %s: stopped unexpectedly\n' "$name"
		: >"$work/failed"
		;;
	esac
}

# fail REASON reports the case as failed and ends it.
fail() {
	state=failed
	printf 'FAIL %s: %s\n' "$name" "$*"
	: >"$work/failed"
	exit 1
}

# pass ends a case that succeeded. The installer works in a directory under
# TMPDIR and removes it on every path out, so a case that left anything there
# did not clean up after itself, whatever else it got right.
pass() {
	if [ -n "$(entries_of "$tmpdir")" ]; then
		fail "left behind in TMPDIR: $(entries_of "$tmpdir" | tr '\n' ' ')"
	fi
	state=passed
}

# begin NAME starts a case. It runs first inside the case's subshell, so the
# variables it sets belong to that case alone. A case ends in `pass`; `fail`
# ends it at once, and so does any command that fails unexpectedly, because
# `set -e` is on from here, in which case the exit trap reports it.
begin() {
	name=$1
	state=running
	dir=$work/cases/$name
	home=$dir/home
	tmpdir=$dir/tmp
	bin=$dir/bin
	dl="KURZE_URL_DOWNLOAD_URL=file://$release"
	to="KURZE_URL_INSTALL_DIR=$bin"
	mkdir -p "$home" "$tmpdir"
	trap finish EXIT
	set -e
}

# in_clean_env [VAR=value ...] COMMAND [ARG ...] runs COMMAND with nothing but
# a PATH that puts the case's shims first, this case's HOME and TMPDIR, and
# the variables given.
in_clean_env() {
	env -i "PATH=$dir/shim:$shims:$PATH" "HOME=$home" "TMPDIR=$tmpdir" "$@"
}

# run_install_in CWD [VAR=value ...] runs the installer in a clean
# environment, from the directory CWD, leaving its exit status in $status and
# its output in $dir/stdout and $dir/stderr.
run_install_in() {
	status=0
	run_cwd=$1
	shift
	(cd "$run_cwd" && in_clean_env "$@" "$sh_bin" "$install_sh") >"$dir/stdout" 2>"$dir/stderr" </dev/null || status=$?
}

# run_install [VAR=value ...] is run_install_in from the case's own directory.
run_install() {
	run_install_in "$dir" "$@"
}

# make_curl_shim puts a curl first on PATH that appends its arguments, one per
# line, to $dir/curl.args, each call's whole command line to
# $dir/curl.cmdline, and then fails. It stands in for the network in the cases
# that look at what the installer asks for, or that must never ask.
make_curl_shim() {
	mkdir -p "$dir/shim"
	cat >"$dir/shim/curl" <<EOF
#!/bin/sh
printf '%s\n' "\$@" >>'$dir/curl.args'
printf '%s\n' "\$*" >>'$dir/curl.cmdline'
exit 1
EOF
	chmod 755 "$dir/shim/curl"
}

expect_status() {
	[ "$status" -eq "$1" ] || fail "exit status $status, wanted $1; stderr: $(tr '\n' ' ' <"$dir/stderr")"
}

stderr_has() {
	grep -qF -- "$1" "$dir/stderr" || fail "stderr lacks '$1'; stderr: $(tr '\n' ' ' <"$dir/stderr")"
}

stderr_lacks() {
	if grep -qF -- "$1" "$dir/stderr"; then
		fail "stderr has '$1'; stderr: $(tr '\n' ' ' <"$dir/stderr")"
	fi
}

# stderr_names_profile FILE checks that the PATH hint points at ~/FILE.
stderr_names_profile() {
	stderr_has "add this line to ~/$1:"
}

nothing_installed() {
	[ -z "$(entries_of "$bin")" ] || fail "install dir holds: $(entries_of "$bin" | tr '\n' ' ')"
}

# is_755 FILE succeeds when FILE is a regular file with exactly mode 755.
is_755() {
	[ -n "$(find "$1" -type f -perm 755 2>/dev/null)" ]
}

# --- the cases -------------------------------------------------------------

(
	begin installs_into_the_given_dir
	run_install "$dl" "$to"
	expect_status 0
	[ -f "$bin/kurze-url" ] || fail "no binary at $bin/kurze-url"
	is_755 "$bin/kurze-url" || fail "the binary's mode is not 755"
	[ "$("$bin/kurze-url" version)" = "$fake_version_line" ] || fail "the installed binary does not run"
	[ "$(tail -n 1 "$dir/stdout")" = "$fake_version_line" ] || fail "stdout ends with '$(tail -n 1 "$dir/stdout")'"
	[ "$(wc -l <"$dir/stdout" | tr -d ' ')" -eq 1 ] || fail "stdout holds more than the version line"
	stderr_has "$bin/kurze-url"
	pass
)

(
	begin defaults_to_home_local_bin
	run_install "$dl"
	expect_status 0
	[ -x "$home/.local/bin/kurze-url" ] || fail "no binary at \$HOME/.local/bin/kurze-url"
	[ "$(tail -n 1 "$dir/stdout")" = "$fake_version_line" ] || fail "stdout ends with '$(tail -n 1 "$dir/stdout")'"
	pass
)

(
	begin installs_into_a_relative_dir
	mkdir "$dir/cwd"
	run_install_in "$dir/cwd" "$dl" KURZE_URL_INSTALL_DIR=rel/bin
	expect_status 0
	[ -x "$dir/cwd/rel/bin/kurze-url" ] || fail "no binary at rel/bin/kurze-url"
	# Under env -i the shell has no PWD to inherit and asks the system, which
	# answers with the physical path.
	physical=$(cd "$dir/cwd" && pwd -P)
	stderr_has "export PATH=\"$physical/rel/bin:\$PATH\""
	pass
)

(
	begin refuses_a_checksum_mismatch
	cp -R "$release" "$dir/release"
	# The first byte of a gzip file is its magic number. Overwriting it makes
	# the archive unreadable as well as different, so an installer that
	# extracted before it checked would fail on the extraction, and the
	# message below would be missing.
	printf x | dd of="$dir/release/$host_asset" bs=1 count=1 conv=notrunc 2>/dev/null
	run_install "KURZE_URL_DOWNLOAD_URL=file://$dir/release" "$to"
	expect_status 1
	stderr_has "Checksum mismatch"
	nothing_installed
	pass
)

(
	begin refuses_an_archive_missing_from_checksums
	cp -R "$release" "$dir/release"
	printf '%s  %s\n' "$(sha256_of "$dir/release/$host_asset")" kurze-url_plan9_mips.tar.gz >"$dir/release/checksums.txt"
	run_install "KURZE_URL_DOWNLOAD_URL=file://$dir/release" "$to"
	expect_status 1
	stderr_has "$host_asset"
	stderr_has "checksums.txt"
	nothing_installed
	pass
)

(
	begin refuses_a_failed_download
	cp -R "$release" "$dir/release"
	rm "$dir/release/$host_asset"
	run_install "KURZE_URL_DOWNLOAD_URL=file://$dir/release" "$to"
	expect_status 1
	stderr_has "file://$dir/release/$host_asset"
	nothing_installed
	pass
)

(
	begin refuses_an_archive_without_the_binary
	mkdir -p "$dir/release" "$dir/stage"
	echo MIT >"$dir/stage/LICENSE"
	tar -czf "$dir/release/$host_asset" -C "$dir/stage" LICENSE
	printf '%s  %s\n' "$(sha256_of "$dir/release/$host_asset")" "$host_asset" >"$dir/release/checksums.txt"
	run_install "KURZE_URL_DOWNLOAD_URL=file://$dir/release" "$to"
	expect_status 1
	stderr_has "$host_asset"
	nothing_installed
	pass
)

(
	begin maps_architectures
	# Only the archive that the mapping should pick exists in each release,
	# so a wrong mapping is a failed download.
	for pair in x86_64:amd64 amd64:amd64 aarch64:arm64 arm64:arm64; do
		machine=${pair%%:*}
		arch=${pair##*:}
		rm -rf "$dir/release" "$bin"
		build_release "$dir/release" linux "$arch" 9.9.9
		run_install "KURZE_URL_DOWNLOAD_URL=file://$dir/release" "$to" FAKE_UNAME_S=Linux "FAKE_UNAME_M=$machine"
		[ "$status" -eq 0 ] || fail "uname -m $machine did not install kurze-url_linux_$arch: $(tr '\n' ' ' <"$dir/stderr")"
		[ -x "$bin/kurze-url" ] || fail "uname -m $machine installed nothing"
	done
	pass
)

(
	begin maps_operating_systems
	for pair in Linux:linux Darwin:darwin; do
		uname_s=${pair%%:*}
		os=${pair##*:}
		rm -rf "$dir/release" "$bin"
		build_release "$dir/release" "$os" arm64 9.9.9
		run_install "KURZE_URL_DOWNLOAD_URL=file://$dir/release" "$to" "FAKE_UNAME_S=$uname_s" FAKE_UNAME_M=arm64
		[ "$status" -eq 0 ] || fail "uname -s $uname_s did not install kurze-url_${os}_arm64: $(tr '\n' ' ' <"$dir/stderr")"
		[ -x "$bin/kurze-url" ] || fail "uname -s $uname_s installed nothing"
	done
	pass
)

(
	begin refuses_an_unknown_architecture
	run_install "$dl" "$to" FAKE_UNAME_M=i686
	expect_status 1
	stderr_has "i686"
	nothing_installed
	pass
)

(
	begin refuses_an_unknown_os
	run_install "$dl" "$to" FAKE_UNAME_S=FreeBSD
	expect_status 1
	stderr_has "FreeBSD"
	nothing_installed
	pass
)

(
	begin points_git_bash_to_powershell
	run_install "$dl" "$to" FAKE_UNAME_S=MINGW64_NT-10.0
	expect_status 1
	stderr_has "install.ps1 | iex"
	stderr_has "releases/latest"
	nothing_installed
	pass
)

(
	begin reinstalls_over_an_existing_binary
	build_release "$dir/release1" "$host_os" "$host_arch" 1.0.0
	build_release "$dir/release2" "$host_os" "$host_arch" 2.0.0
	run_install "KURZE_URL_DOWNLOAD_URL=file://$dir/release1" "$to"
	expect_status 0
	[ "$(tail -n 1 "$dir/stdout")" = "kurze-url 1.0.0 (abc1234, 2026-10-24T00:00:00Z)" ] || fail "the first install is not 1.0.0"
	# A hard link keeps the first file alive the way a running process does.
	# A reinstall that wrote into that file would change what the link shows,
	# and on Linux would fail with "text file busy" on a real binary; one
	# that replaces the directory entry leaves it alone. (The fake binary is
	# a script, which Linux does not lock, so the link is the way to see it.)
	ln "$bin/kurze-url" "$dir/the-old-file"
	run_install "KURZE_URL_DOWNLOAD_URL=file://$dir/release2" "$to"
	expect_status 0
	[ "$("$dir/the-old-file" version)" = "kurze-url 1.0.0 (abc1234, 2026-10-24T00:00:00Z)" ] || fail "the reinstall wrote into the old file instead of replacing it"
	[ "$(tail -n 1 "$dir/stdout")" = "kurze-url 2.0.0 (abc1234, 2026-10-24T00:00:00Z)" ] || fail "the second install is not 2.0.0"
	[ "$("$bin/kurze-url" version)" = "kurze-url 2.0.0 (abc1234, 2026-10-24T00:00:00Z)" ] || fail "the binary in place is not 2.0.0"
	[ "$(entries_of "$bin")" = "kurze-url" ] || fail "install dir holds: $(entries_of "$bin" | tr '\n' ' ')"
	is_755 "$bin/kurze-url" || fail "the binary's mode is not 755"
	pass
)

(
	begin hints_when_the_dir_is_not_on_path
	run_install "$dl" "$to" SHELL=/bin/zsh
	expect_status 0
	stderr_has "export PATH=\"$bin:\$PATH\""
	stderr_names_profile .zshrc
	pass
)

(
	begin suggests_the_profile_of_the_shell
	for pair in /bin/zsh:.zshrc /usr/bin/bash:.bashrc /bin/fish:.profile /bin/sh:.profile; do
		shell=${pair%%:*}
		profile=${pair##*:}
		run_install "$dl" "$to" "SHELL=$shell"
		expect_status 0
		stderr_names_profile "$profile"
	done
	# An empty SHELL names no shell. (Unset would not do for this: bash
	# fills it in from the passwd entry when it starts, and macOS's sh is
	# bash.)
	run_install "$dl" "$to" SHELL=
	expect_status 0
	stderr_names_profile .profile
	pass
)

(
	begin does_not_hint_when_it_is
	run_install "$dl" "$to" "PATH=$bin:$shims:$PATH"
	expect_status 0
	stderr_lacks "export PATH"
	# The same directory spelled with a trailing slash is still that
	# directory.
	run_install "$dl" "KURZE_URL_INSTALL_DIR=$bin/" "PATH=$bin:$shims:$PATH"
	expect_status 0
	stderr_lacks "export PATH"
	pass
)

(
	begin speaks_german_with_a_german_locale
	cp -R "$release" "$dir/release"
	printf x | dd of="$dir/release/$host_asset" bs=1 count=1 conv=notrunc 2>/dev/null
	german_dl="KURZE_URL_DOWNLOAD_URL=file://$dir/release"

	run_install "$german_dl" "$to" LANG=de_DE.UTF-8
	expect_status 1
	stderr_has "Prüfsumme"
	stderr_lacks "Checksum"

	# The first of LC_ALL, LC_MESSAGES and LANG that is set decides alone.
	run_install "$german_dl" "$to" LC_MESSAGES=de_AT LANG=en_US.UTF-8
	stderr_has "Prüfsumme"

	run_install "$german_dl" "$to" LC_ALL=C LANG=de_DE
	expect_status 1
	stderr_has "Checksum mismatch"
	stderr_lacks "Prüfsumme"

	run_install "$german_dl" "$to" LANG=en_US.UTF-8
	stderr_has "Checksum mismatch"
	pass
)

(
	begin uses_the_version_in_the_url
	make_curl_shim
	run_install KURZE_URL_VERSION=v0.1.0 "$to"
	expect_status 1
	[ -f "$dir/curl.args" ] || fail "curl was never called"
	grep -qxF "https://github.com/mheob/kurze-url/releases/download/v0.1.0/$host_asset" "$dir/curl.args" ||
		fail "curl was asked for: $(tr '\n' ' ' <"$dir/curl.args")"
	pass
)

(
	begin uses_the_latest_release_without_a_version
	make_curl_shim
	run_install "$to"
	expect_status 1
	[ -f "$dir/curl.args" ] || fail "curl was never called"
	grep -qxF "https://github.com/mheob/kurze-url/releases/latest/download/$host_asset" "$dir/curl.args" ||
		fail "curl was asked for: $(tr '\n' ' ' <"$dir/curl.args")"
	pass
)

(
	begin lets_the_download_url_win_over_the_version
	run_install "$dl" "$to" KURZE_URL_VERSION=v0.1.0
	expect_status 0
	[ -x "$bin/kurze-url" ] || fail "nothing was installed from the download URL"
	pass
)

(
	begin refuses_a_download_url_that_is_not_https
	make_curl_shim
	# The checksum comes from the same place as the archive, so over plain
	# http whoever can change one can change both. The refusal comes before
	# any request: curl is never called.
	for url in http://downloads.example.test/kurze-url ftp://downloads.example.test/kurze-url downloads.example.test/kurze-url; do
		run_install "KURZE_URL_DOWNLOAD_URL=$url" "$to"
		expect_status 1
		stderr_has "$url"
		stderr_has "only https URLs"
		[ ! -e "$dir/curl.args" ] || fail "curl was called for $url"
		nothing_installed
	done
	run_install KURZE_URL_DOWNLOAD_URL=http://downloads.example.test/kurze-url "$to" LANG=de_DE.UTF-8
	expect_status 1
	stderr_has "wird abgelehnt"
	stderr_lacks "Refusing"
	pass
)

(
	begin accepts_an_https_download_url_in_any_case
	make_curl_shim
	# Only the scheme's spelling and a trailing slash differ from the plain
	# case, so the shim must be asked for the URL they spell.
	run_install KURZE_URL_DOWNLOAD_URL=HTTPS://downloads.example.test/kurze-url/ "$to"
	expect_status 1
	grep -qxF "HTTPS://downloads.example.test/kurze-url/$host_asset" "$dir/curl.args" ||
		fail "curl was asked for: $(tr '\n' ' ' <"$dir/curl.args")"
	pass
)

(
	begin downloads_over_https_only_with_curl
	make_curl_shim
	# A redirect from https to http would hand the checksum, like the archive,
	# to whoever sits on the path. curl follows redirects only when asked to
	# stay on https, and file stays allowed for the tests' own directories.
	run_install "$to"
	expect_status 1
	[ -f "$dir/curl.cmdline" ] || fail "curl was never called"
	grep -qF -- "--proto =https,file " "$dir/curl.cmdline" || fail "curl was called as: $(tr '\n' '|' <"$dir/curl.cmdline")"
	grep -qF -- "--proto-redir =https " "$dir/curl.cmdline" || fail "curl was called as: $(tr '\n' '|' <"$dir/curl.cmdline")"
	pass
)

(
	begin a_truncated_script_does_nothing
	make_curl_shim
	lines=$(wc -l <"$install_sh" | tr -d ' ')
	# curl | sh runs what has arrived. Every prefix of the script, the first
	# half included, must run nothing: not a download, not a directory, not
	# even a temporary one. Most of them are a syntax error, because main's
	# body is one function that has not closed yet; the rest are only
	# definitions. Nothing is reset between prefixes, so the first one that
	# leaves a trace is the one reported.
	n=1
	while [ "$n" -lt "$lines" ]; do
		head -n "$n" "$install_sh" | in_clean_env "$dl" "$to" "$sh_bin" >"$dir/stdout" 2>"$dir/stderr" || true
		[ ! -e "$dir/curl.args" ] || fail "the first $n of $lines lines called curl"
		[ ! -e "$bin" ] || fail "the first $n of $lines lines created the install dir"
		[ -z "$(find "$home" "$tmpdir" -mindepth 1)" ] ||
			fail "the first $n of $lines lines created: $(find "$home" "$tmpdir" -mindepth 1 | tr '\n' ' ')"
		[ ! -s "$dir/stdout" ] || fail "the first $n of $lines lines printed on stdout"
		n=$((n + 1))
	done
	pass
)

if [ -e "$work/failed" ]; then
	exit 1
fi
