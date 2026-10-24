# Installs the kurze-url CLI on Windows:
#
#   irm https://raw.githubusercontent.com/mheob/kurze-url/main/apps/cli/install.ps1 | iex
#
# It downloads the archive for this machine from a GitHub Release, checks it
# against the release's checksums.txt, and puts kurze-url.exe in
# %LOCALAPPDATA%\Programs\kurze-url. Unless told otherwise it then adds that
# directory to your user Path, so that every new terminal finds kurze-url, and
# to the Path of the window that ran the script, so that this one does too.
# Linux and macOS have their own installer, install.sh, next to this file.
#
# Environment:
#
#   KURZE_URL_INSTALL_DIR     where to put the binary
#                             (default: %LOCALAPPDATA%\Programs\kurze-url)
#   KURZE_URL_VERSION         a release tag such as v0.1.0 (default: the latest)
#   KURZE_URL_DOWNLOAD_URL    the base URL that holds the archive and
#                             checksums.txt, which wins over KURZE_URL_VERSION.
#                             An https URL, or a file:/// URL for a local
#                             directory. For mirrors, and for
#                             scripts/install_test.ps1.
#   KURZE_URL_NO_MODIFY_PATH  1 leaves your Path alone, and prints how to add
#                             the directory yourself instead. For people who
#                             manage their Path by hand.
#
# Any failure ends the script with a terminating error that says why, so the
# exit status is non-zero under -File and -Command. The messages are English
# or German by the CLI's own rule on Windows: the first of LC_ALL, LC_MESSAGES
# and LANG that is set decides, and German means it starts with "de"; when
# none is set, the language of the user interface does. The only thing the
# script writes to the pipeline is the `version` line of the binary it
# installed, so `$line = irm ... | iex` captures just that.
#
# It runs unchanged under Windows PowerShell 5.1, which ships with Windows 10
# and 11, and under PowerShell 7. That is why it avoids the newer syntax
# (no ternary, no `??`, no `&&`), and why this file is plain ASCII: the German
# umlauts are written as \uXXXX escapes, which Get-Message turns back into
# letters. Windows PowerShell 5.1 reads a file without a byte order mark in
# the ANSI code page, so a literal umlaut would break for anyone who saves the
# script and runs it from disk, and a byte order mark is no cure, because
# `irm | iex` hands over the text as downloaded, where it can arrive as a
# character in front of the first line.
#
# The whole body is the function Install-KurzeUrl, called on the last line.
# `irm | iex` runs whatever has arrived, so a download that stops halfway
# would otherwise run half a script. This way a cut inside the function is a
# parse error, and a cut before its call has only defined it. Keep it that
# way: nothing but this function and its call may sit at the top level, which
# also keeps the session of whoever runs `iex` free of stray variables and
# preferences. scripts/install_test.ps1 is its test.

<#
.SYNOPSIS
Installs the kurze-url CLI on Windows.

.DESCRIPTION
Downloads kurze-url_windows_<arch>.zip and checksums.txt from a GitHub Release,
refuses to go on unless the archive's SHA-256 is the one checksums.txt lists,
and installs kurze-url.exe into KURZE_URL_INSTALL_DIR, by default
%LOCALAPPDATA%\Programs\kurze-url, and onto the user's Path. See the comment
at the top of the file for the environment variables it reads.
#>
function Install-KurzeUrl {
    [Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSAvoidUsingWriteHost', '', Justification = 'These are the messages of an installer to the person at the console. Write-Output would put them in the pipeline of whoever captures the result of irm | iex, where only the final version line belongs.')]
    [CmdletBinding()]
    param()

    # These two are local to this function, so they end with it: nothing of
    # them is left in the session that ran `iex`. The progress preference is
    # not a nicety: Windows PowerShell 5.1 draws a progress bar for every
    # download and extraction, which makes both several times slower.
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'

    # Get-Message KEY [ARGUMENT ...] returns one sentence in the language the
    # environment asks for. Every sentence this script says is written here,
    # once per language, and the language is decided afresh on each call so
    # that nothing outside this function has to carry it. German text spells
    # its umlauts as \uXXXX, see the top of the file.
    function Get-Message {
        param([string]$Key, [object[]]$Argument)

        $locale = ''
        foreach ($name in 'LC_ALL', 'LC_MESSAGES', 'LANG') {
            $value = [Environment]::GetEnvironmentVariable($name)
            if ($value) {
                $locale = $value
                break
            }
        }
        if (-not $locale) {
            $locale = (Get-UICulture).TwoLetterISOLanguageName
        }

        $en = @{
            unsupported_arch    = 'Unsupported architecture: {0}. kurze-url is built for amd64 and arm64.'
            no_localappdata     = 'LOCALAPPDATA is not set. Set KURZE_URL_INSTALL_DIR to the directory to install into.'
            no_tmp              = 'Could not create a temporary directory.'
            url_not_https       = 'Refusing {0}: only https URLs are accepted, and file URLs in KURZE_URL_DOWNLOAD_URL.'
            final_url_unknown   = 'Could not tell where the download from {0} ended up, so it was refused. Nothing was installed.'
            registry_key_failed = 'The registry key {0} cannot be opened.'
            downloading         = 'Downloading {0} ...'
            download_failed     = 'Download failed: {0} ({1})'
            no_checksum         = 'checksums.txt has no entry for {0}. Nothing was installed.'
            hash_failed         = 'Could not compute the checksum of {0}. Nothing was installed.'
            checksum_mismatch   = 'Checksum mismatch for {0}: expected {1}, got {2}. Nothing was installed.'
            extract_failed      = 'Could not extract kurze-url.exe from {0}. Nothing was installed.'
            dir_in_the_way      = 'A directory named {0} is in the way. Remove it and run this script again.'
            install_failed      = 'Could not install into {0}: {1}'
            installed           = 'Installed kurze-url to {0}'
            path_added          = 'Added {0} to your user Path. New terminals pick it up, and this one has it already.'
            path_hint           = '{0} is not in your Path, and KURZE_URL_NO_MODIFY_PATH asks this script to leave your Path alone. To run kurze-url from anywhere, add that directory to your user Path.'
            path_failed         = 'Could not add {0} to your user Path: {1}\nkurze-url.exe is installed there all the same. To skip this step, run this script again with KURZE_URL_NO_MODIFY_PATH=1, or add that directory to your user Path yourself.'
            run_failed          = 'The installed {0} does not run on this machine.'
            completion_hint     = 'To turn on tab completion, run: kurze-url completion install'
        }
        $de = @{
            unsupported_arch    = 'Nicht unterst\u00fctzte Architektur: {0}. kurze-url gibt es f\u00fcr amd64 und arm64.'
            no_localappdata     = 'LOCALAPPDATA ist nicht gesetzt. Setze KURZE_URL_INSTALL_DIR auf das Verzeichnis, in das installiert werden soll.'
            no_tmp              = 'Es konnte kein tempor\u00e4res Verzeichnis angelegt werden.'
            url_not_https       = '{0} wird abgelehnt: Es werden nur https-URLs akzeptiert, und file-URLs in KURZE_URL_DOWNLOAD_URL.'
            final_url_unknown   = 'Es lie\u00df sich nicht feststellen, bei welcher Adresse der Download von {0} endete, deshalb wurde er abgelehnt. Es wurde nichts installiert.'
            registry_key_failed = 'Der Registrierungsschl\u00fcssel {0} l\u00e4sst sich nicht \u00f6ffnen.'
            downloading         = 'Lade {0} herunter ...'
            download_failed     = 'Download fehlgeschlagen: {0} ({1})'
            no_checksum         = 'In checksums.txt gibt es keinen Eintrag f\u00fcr {0}. Es wurde nichts installiert.'
            hash_failed         = 'Die Pr\u00fcfsumme von {0} konnte nicht berechnet werden. Es wurde nichts installiert.'
            checksum_mismatch   = 'Die Pr\u00fcfsumme von {0} stimmt nicht \u00fcberein: erwartet {1}, erhalten {2}. Es wurde nichts installiert.'
            extract_failed      = 'kurze-url.exe konnte nicht aus {0} entpackt werden. Es wurde nichts installiert.'
            dir_in_the_way      = 'Ein Verzeichnis namens {0} steht im Weg. Entferne es und f\u00fchre dieses Skript erneut aus.'
            install_failed      = 'Die Installation in {0} ist fehlgeschlagen: {1}'
            installed           = 'kurze-url wurde installiert nach {0}'
            path_added          = '{0} wurde zu deinem Benutzer-Path hinzugef\u00fcgt. Neue Terminals \u00fcbernehmen das, und dieses hat es schon.'
            path_hint           = '{0} ist nicht in deinem Path, und KURZE_URL_NO_MODIFY_PATH weist dieses Skript an, deinen Path nicht zu \u00e4ndern. Damit du kurze-url \u00fcberall aufrufen kannst, f\u00fcge dieses Verzeichnis zu deinem Benutzer-Path hinzu.'
            path_failed         = '{0} konnte nicht zu deinem Benutzer-Path hinzugef\u00fcgt werden: {1}\nkurze-url.exe ist trotzdem installiert. Um diesen Schritt zu \u00fcberspringen, f\u00fchre dieses Skript erneut mit KURZE_URL_NO_MODIFY_PATH=1 aus, oder f\u00fcge dieses Verzeichnis selbst zu deinem Benutzer-Path hinzu.'
            run_failed          = 'Das installierte {0} l\u00e4sst sich auf diesem Rechner nicht ausf\u00fchren.'
            completion_hint     = 'Um die Tab-Vervollst\u00e4ndigung einzuschalten, f\u00fchre aus: kurze-url completion install'
        }

        if ($locale.StartsWith('de', [StringComparison]::OrdinalIgnoreCase)) {
            $text = $de[$Key]
        }
        else {
            $text = $en[$Key]
        }
        if ($null -eq $text) {
            return $Key
        }
        # The escapes first and the arguments after: an argument is a path,
        # and a backslash in it is not an escape.
        $text = [regex]::Unescape($text)
        if ($null -ne $Argument -and $Argument.Count -gt 0) {
            $text = $text -f $Argument
        }
        $text
    }

    # Test-PathEntry PATHVALUE DIRECTORY tells whether a semicolon-separated
    # Path has an entry that is DIRECTORY, ignoring case and a trailing
    # backslash. Each entry is compared with its %VARIABLES% expanded, because
    # the user's Path is stored unexpanded and an entry such as
    # %LOCALAPPDATA%\Programs\kurze-url is that directory too. It is how this
    # script avoids adding its directory twice.
    function Test-PathEntry {
        param([string]$PathValue, [string]$Directory)

        if (-not $PathValue) {
            return $false
        }
        $wanted = $Directory.TrimEnd('\')
        foreach ($entry in $PathValue.Split(';')) {
            $expanded = [Environment]::ExpandEnvironmentVariables($entry.Trim())
            if ($expanded.TrimEnd('\') -eq $wanted) {
                return $true
            }
        }
        return $false
    }

    # Open-EnvironmentKey [-Writable] returns the user's environment in the
    # registry, HKEY_CURRENT_USER\Environment, opened read-only unless it is asked for
    # writable. It is a function of its own so that the test of
    # Add-UserPathEntry can hand that one a stand-in for the key.
    function Open-EnvironmentKey {
        param([switch]$Writable)

        $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', [bool]$Writable)
        if ($null -eq $key) {
            throw (Get-Message 'registry_key_failed' 'HKEY_CURRENT_USER\Environment')
        }
        $key
    }

    # Send-SettingChange tells the other programs on this desktop that the
    # environment changed, the way setx and the System Properties dialog do, so
    # that Explorer, and every terminal opened from it afterwards, reads the
    # new Path and not the one it started with. A program that does not answer
    # is given five seconds. A broadcast that fails is not an error: the Path
    # is written either way, and the next sign-in would pick it up.
    function Send-SettingChange {
        try {
            if (-not ('KurzeUrlInstaller.NativeMethods' -as [type])) {
                Add-Type -Namespace KurzeUrlInstaller -Name NativeMethods -MemberDefinition @'
[DllImport("user32.dll", SetLastError = true, CharSet = CharSet.Auto)]
public static extern IntPtr SendMessageTimeout(IntPtr hWnd, uint msg, UIntPtr wParam, string lParam, uint flags, uint timeout, out UIntPtr result);
'@
            }
            $result = [UIntPtr]::Zero
            # HWND_BROADCAST, WM_SETTINGCHANGE with "Environment", SMTO_ABORTIFHUNG.
            $null = [KurzeUrlInstaller.NativeMethods]::SendMessageTimeout([IntPtr]0xffff, 0x1A, [UIntPtr]::Zero, 'Environment', 2, 5000, [ref]$result)
        }
        catch {
            $null = $_
        }
    }

    # Get-UserPathUpdate KEY DIRECTORY returns the value and the type to write
    # so that the Path in KEY has an entry that is DIRECTORY, or nothing when
    # it has one already. The Path is read raw, which is why the entries that
    # name variables survive: see Add-UserPathEntry.
    function Get-UserPathUpdate {
        param($Key, [string]$Directory)

        $raw = [string]$Key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
        if (Test-PathEntry -PathValue $raw -Directory $Directory) {
            return $null
        }
        if ($Key.GetValueNames() -contains 'Path') {
            $kind = $Key.GetValueKind('Path')
        }
        else {
            $kind = [Microsoft.Win32.RegistryValueKind]::ExpandString
        }
        $head = $raw.TrimEnd(';')
        if ($head) {
            $value = $head + ';' + $Directory
        }
        else {
            $value = $Directory
        }
        [pscustomobject]@{ Value = $value; Kind = $kind }
    }

    # Add-UserPathEntry DIRECTORY puts DIRECTORY at the end of the user's Path,
    # unless an entry of it already is that directory, and says which. The user's
    # Path is a registry value that is usually an expandable string, and its
    # entries may name variables, such as
    # %USERPROFILE%\AppData\Local\Microsoft\WindowsApps on every account and
    # %JAVA_HOME%\bin on many a developer's. [Environment]::GetEnvironmentVariable
    # would hand that back expanded and SetEnvironmentVariable would store the
    # result as plain text, which freezes every such entry for good. So this
    # reads the value raw, keeps its type, and writes back exactly what it read
    # with one entry added after the last: every other entry stays as it was.
    # The key is opened read-only to find out whether there is anything to do,
    # so a reinstall of a directory that is on the Path already asks the
    # registry for nothing it might refuse, and is opened again for writing,
    # and read again, only when there is.
    function Add-UserPathEntry {
        param([string]$Directory)

        $reader = Open-EnvironmentKey
        try {
            $update = Get-UserPathUpdate -Key $reader -Directory $Directory
        }
        finally {
            $reader.Dispose()
        }
        if ($null -eq $update) {
            return $false
        }

        $writer = Open-EnvironmentKey -Writable
        try {
            $update = Get-UserPathUpdate -Key $writer -Directory $Directory
            if ($null -eq $update) {
                return $false
            }
            $writer.SetValue('Path', $update.Value, $update.Kind)
        }
        finally {
            $writer.Dispose()
        }
        Send-SettingChange
        return $true
    }

    # Save-Download URL PATH fetches one file. A file:// URL is copied, because
    # PowerShell 7's web cmdlets refuse that scheme. Anything else goes
    # through Invoke-WebRequest, whose answer says where the request ended up
    # after its redirects: GitHub sends a release download to another host,
    # and a redirect may name an http URL. Everything that came from there is
    # refused. PowerShell 7's cmdlet blocks a redirect from https to http
    # unless asked not to; Windows PowerShell 5.1's makes no such promise, so
    # this checks where the response ended up in both, and refuses when it
    # cannot tell. It runs after the download, which is early enough: nothing
    # has been read or used yet.
    function Save-Download {
        param([string]$Url, [string]$Path)

        if ($Url -match '^file://') {
            try {
                Copy-Item -LiteralPath ([Uri]$Url).LocalPath -Destination $Path
            }
            catch {
                throw (Get-Message 'download_failed' $Url, $_.Exception.Message)
            }
            return
        }

        try {
            $response = Invoke-WebRequest -Uri $Url -OutFile $Path -UseBasicParsing -PassThru
        }
        catch {
            throw (Get-Message 'download_failed' $Url, $_.Exception.Message)
        }

        $final = $null
        $inner = $response.BaseResponse
        if ($null -ne $inner) {
            if ($inner.PSObject.Properties['ResponseUri']) {
                # Windows PowerShell 5.1: an HttpWebResponse.
                $final = $inner.ResponseUri
            }
            elseif ($inner.PSObject.Properties['RequestMessage'] -and $inner.RequestMessage) {
                # PowerShell 7: an HttpResponseMessage, whose request is the last one made.
                $final = $inner.RequestMessage.RequestUri
            }
        }
        # A response that does not say where it ended up cannot be vouched
        # for, so it is refused like one that ended on http.
        if ($null -eq $final) {
            Remove-Item -LiteralPath $Path -Force -ErrorAction SilentlyContinue
            throw (Get-Message 'final_url_unknown' $Url)
        }
        if ($final.Scheme -ne 'https') {
            Remove-Item -LiteralPath $Path -Force -ErrorAction SilentlyContinue
            throw (Get-Message 'url_not_https' $final.ToString())
        }
    }

    # Get-SecurityProtocolUpdate CURRENT takes the value of
    # [Net.ServicePointManager]::SecurityProtocol as a number and returns the
    # value to set instead, or nothing when it should stay as it is. The
    # numbers are the SecurityProtocolType flags, because TLS 1.2 (3072) is
    # not a name every .NET Framework a Windows PowerShell 5.1 runs on knows.
    # A list that lacks TLS 1.2 gets it added to whatever else the machine has
    # configured, and is never replaced. 0 is SystemDefault, which lets the
    # operating system choose and so already covers TLS 1.2: OR-ing 3072 into
    # it would narrow the person's whole PowerShell session to TLS 1.2 only,
    # which is the opposite of what this is for.
    function Get-SecurityProtocolUpdate {
        param([int]$Current)

        $tls12 = 3072
        if ($Current -eq 0 -or ($Current -band $tls12) -ne 0) {
            return $null
        }
        $Current -bor $tls12
    }

    # Windows PowerShell 5.1 can be configured to offer only TLS 1.0 and 1.1,
    # which GitHub refuses, and older systems are. PowerShell 7 offers what
    # the platform does.
    if ($PSVersionTable.PSVersion.Major -lt 6) {
        $protocol = Get-SecurityProtocolUpdate -Current ([int][Net.ServicePointManager]::SecurityProtocol)
        if ($null -ne $protocol) {
            [Net.ServicePointManager]::SecurityProtocol = $protocol
        }
    }

    $repoUrl = 'https://github.com/mheob/kurze-url'

    # A 32-bit PowerShell on a 64-bit Windows reports x86 in
    # PROCESSOR_ARCHITECTURE and the real architecture in
    # PROCESSOR_ARCHITEW6432, which wins when it is set.
    $arch = $env:PROCESSOR_ARCHITEW6432
    if (-not $arch) {
        $arch = $env:PROCESSOR_ARCHITECTURE
    }
    if ($arch -eq 'AMD64') {
        $arch = 'amd64'
    }
    elseif ($arch -eq 'ARM64') {
        $arch = 'arm64'
    }
    else {
        $seen = $arch
        if (-not $seen) {
            $seen = '(none)'
        }
        throw (Get-Message 'unsupported_arch' $seen)
    }

    $dir = $env:KURZE_URL_INSTALL_DIR
    if (-not $dir) {
        if (-not $env:LOCALAPPDATA) {
            throw (Get-Message 'no_localappdata')
        }
        $dir = Join-Path -Path (Join-Path -Path $env:LOCALAPPDATA -ChildPath 'Programs') -ChildPath 'kurze-url'
    }
    # The Path entry and every message below name this directory, so make it
    # absolute and without a trailing backslash first. A relative one is read
    # against PowerShell's own location, which is not .NET's working
    # directory, so GetFullPath alone would resolve it somewhere else.
    if (-not [IO.Path]::IsPathRooted($dir)) {
        $dir = Join-Path -Path (Get-Location).ProviderPath -ChildPath $dir
    }
    $dir = [IO.Path]::GetFullPath($dir)
    if ($dir.Length -gt 3 -and $dir.EndsWith('\')) {
        $dir = $dir.TrimEnd('\')
    }

    # The archive names carry no version, so the latest release's URL is a
    # stable one. A version names its own release instead, and an explicit
    # base URL beats both.
    $version = $env:KURZE_URL_VERSION
    if ($env:KURZE_URL_DOWNLOAD_URL) {
        $base = $env:KURZE_URL_DOWNLOAD_URL.TrimEnd('/')
    }
    elseif ($version) {
        $base = "$repoUrl/releases/download/$version"
    }
    else {
        $base = "$repoUrl/releases/latest/download"
    }
    # The checksum comes from the same place as the archive, so over plain
    # http whoever can change one can change both. Only https is accepted,
    # and file for a directory on this machine.
    if ($base -notmatch '^(https|file)://') {
        throw (Get-Message 'url_not_https' $base)
    }
    $asset = "kurze-url_windows_$arch.zip"

    $tmp = Join-Path -Path ([IO.Path]::GetTempPath()) -ChildPath ('kurze-url-' + [guid]::NewGuid().ToString('N'))
    try {
        try {
            $null = New-Item -ItemType Directory -Path $tmp
        }
        catch {
            throw (Get-Message 'no_tmp')
        }

        $zip = Join-Path -Path $tmp -ChildPath $asset
        $sums = Join-Path -Path $tmp -ChildPath 'checksums.txt'
        Write-Host (Get-Message 'downloading' "$base/$asset")
        Save-Download -Url "$base/$asset" -Path $zip
        Save-Download -Url "$base/checksums.txt" -Path $sums

        # checksums.txt lines are "<sha256>  <name>", and sha256sum marks a
        # file it read in binary mode with a "*" before the name. The name
        # has to be the archive's own, in full: a line for
        # "<archive>.sbom.json" is not one, and the first line that is wins.
        $expected = $null
        foreach ($line in (Get-Content -LiteralPath $sums)) {
            $parts = $line.Trim() -split '\s+', 2
            if ($parts.Count -eq 2 -and $parts[1].TrimStart('*') -ceq $asset) {
                $expected = $parts[0]
                break
            }
        }
        if (-not $expected) {
            throw (Get-Message 'no_checksum' $asset)
        }
        try {
            $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $zip).Hash
        }
        catch {
            throw (Get-Message 'hash_failed' $asset)
        }
        # Get-FileHash writes its digest in capitals and checksums.txt in
        # lower case; they are the same digest.
        if ($actual -ne $expected) {
            throw (Get-Message 'checksum_mismatch' $asset, $expected.ToLowerInvariant(), $actual.ToLowerInvariant())
        }

        # Only now is anything opened.
        $extract = Join-Path -Path $tmp -ChildPath 'extract'
        try {
            Expand-Archive -LiteralPath $zip -DestinationPath $extract -Force
        }
        catch {
            throw (Get-Message 'extract_failed' $asset)
        }
        $fresh = Join-Path -Path $extract -ChildPath 'kurze-url.exe'
        if (-not (Test-Path -LiteralPath $fresh -PathType Leaf)) {
            throw (Get-Message 'extract_failed' $asset)
        }

        # A running .exe can be renamed but not overwritten, and not
        # replaced by a copy either. So the new binary is copied in under a
        # name of its own, the old one is renamed out of the way, and the new
        # one is renamed into place. Renaming replaces the directory entry in
        # one step, and the running process keeps the file it was started
        # from, now called kurze-url.exe.old, which the next run removes if
        # this one could not.
        $target = Join-Path -Path $dir -ChildPath 'kurze-url.exe'
        $staged = "$target.new"
        $old = "$target.old"
        foreach ($path in $target, $staged, $old) {
            if (Test-Path -LiteralPath $path -PathType Container) {
                throw (Get-Message 'dir_in_the_way' $path)
            }
        }
        try {
            $null = New-Item -ItemType Directory -Force -Path $dir
        }
        catch {
            throw (Get-Message 'install_failed' $dir, $_.Exception.Message)
        }
        $displaced = $false
        try {
            Copy-Item -LiteralPath $fresh -Destination $staged -Force
            if (Test-Path -LiteralPath $target -PathType Leaf) {
                Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue
                Move-Item -LiteralPath $target -Destination $old -Force
                $displaced = $true
            }
            Move-Item -LiteralPath $staged -Destination $target
        }
        catch {
            $reason = $_.Exception.Message
            # Never leave the person without a kurze-url.exe they had.
            if ($displaced) {
                Move-Item -LiteralPath $old -Destination $target -ErrorAction SilentlyContinue
            }
            Remove-Item -LiteralPath $staged -Force -ErrorAction SilentlyContinue
            throw (Get-Message 'install_failed' $dir, $reason)
        }
        Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue
        # Said before the Path step, so that a Path step that fails does not
        # hide that the binary is in place: path_failed says it as well.
        Write-Host (Get-Message 'installed' $target)

        # The user's Path is where a new terminal looks, and $env:Path is
        # where this one does. The user's Path lives in the registry, which
        # only Windows has, and is changed by Add-UserPathEntry, which adds
        # this directory and touches nothing else in it.
        if ($env:KURZE_URL_NO_MODIFY_PATH -eq '1') {
            if (-not (Test-PathEntry -PathValue $env:Path -Directory $dir)) {
                Write-Host (Get-Message 'path_hint' $dir)
            }
        }
        else {
            if ([Environment]::OSVersion.Platform -eq [PlatformID]::Win32NT) {
                try {
                    $added = Add-UserPathEntry -Directory $dir
                }
                catch {
                    throw (Get-Message 'path_failed' $dir, $_.Exception.Message)
                }
                if ($added) {
                    Write-Host (Get-Message 'path_added' $dir)
                }
            }
            if (-not (Test-PathEntry -PathValue $env:Path -Directory $dir)) {
                if ($env:Path) {
                    $env:Path = $env:Path.TrimEnd(';') + ';' + $dir
                }
                else {
                    $env:Path = $dir
                }
            }
        }

        try {
            $versionLine = & $target version
        }
        catch {
            throw (Get-Message 'run_failed' $target)
        }
        if ($LASTEXITCODE -ne 0 -or -not $versionLine) {
            throw (Get-Message 'run_failed' $target)
        }
        # The last of the messages, and said before the version line because
        # that has to be the last thing on stdout: a run under -File sends
        # Write-Host there too. It only names the command: `kurze-url
        # completion install` asks before it touches a profile, and this
        # script never does.
        Write-Host (Get-Message 'completion_hint')
        Write-Output $versionLine
    }
    finally {
        Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }
}

Install-KurzeUrl
