# Self-test for install.ps1. Plain PowerShell, no framework, and no network:
# the installer downloads from a file:// directory holding a fake release, and
# the cases that must not download at all put a function named
# Invoke-WebRequest in front of the cmdlet. It runs on every pull request in
# ci-cli.yml, under Windows PowerShell 5.1 and under PowerShell 7:
#
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts/install_test.ps1
#   pwsh -NoProfile -File scripts/install_test.ps1
#
# Every case prints `ok NAME`, `skip NAME: reason` or `FAIL NAME: reason`, and
# the script exits 1 if any case failed, after running all of them.
#
# Optional environment, for trying another file:
#
#   INSTALL_PS1   the installer to test (default: ../install.ps1)
#
# Each case runs the installer the way a person does, `irm ... | iex`, in a
# child process of the same shell as this one: the script's text goes through
# Invoke-Expression, with the case's environment variables set first. The
# child gets a directory of its own, which holds its LOCALAPPDATA, its install
# directory and its temporary directory, and every variable the installer
# reads is set or removed explicitly, so a German UI language on the machine
# running this cannot turn an English assertion German.
#
# The fixture is the real binary: the harness builds cmd/kurze-url with
# go build, stamped with a known version, and zips it as
# kurze-url_windows_<arch>.zip next to a checksums.txt. On Windows that is a
# Windows executable. On any other system, which only a developer's own
# machine is, it is a binary for the host, still named kurze-url.exe: a
# Windows executable would not run there, and the cases look at what the
# installed binary prints. The zip entries carry mode 0755 so that such a
# binary is still executable after Expand-Archive.
#
# The case that changes the real user Path runs only when CI is "true", and
# puts the old value back afterwards. Everything else leaves the machine alone:
# KURZE_URL_NO_MODIFY_PATH=1 is set unless the case is about the Path.
#
# This file is plain ASCII on purpose. Windows PowerShell 5.1 reads a file
# without a byte order mark in the system's ANSI code page, so a German
# umlaut here would be misread.

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$script:failed = $false
$script:skipReason = $null

$cliDir = Split-Path -Parent -Path $PSScriptRoot
$installPs1 = $env:INSTALL_PS1
if (-not $installPs1) {
    $installPs1 = Join-Path -Path $cliDir -ChildPath 'install.ps1'
}

$onWindows = $env:OS -eq 'Windows_NT'

# The child shell is the same flavour as this one.
if ($PSVersionTable.PSVersion.Major -ge 6) {
    $shellName = 'pwsh'
    if ($onWindows) {
        $shellName = 'pwsh.exe'
    }
}
else {
    $shellName = 'powershell.exe'
}
$shellExe = Join-Path -Path $PSHOME -ChildPath $shellName

Add-Type -AssemblyName System.IO.Compression

$fakeVersionLine = 'kurze-url 9.9.9 (abc1234, 2026-10-24T00:00:00Z)'
$fakeVersionLineNext = 'kurze-url 9.9.10 (abc1234, 2026-10-24T00:00:00Z)'
$asset = 'kurze-url_windows_amd64.zip'

# What a case appends to the child's script to learn what the installer left in
# the Path of the session it ran in.
$reportSessionPath = 'Write-Output (''SESSION_PATH='' + $env:Path)'

$work = Join-Path -Path ([IO.Path]::GetTempPath()) -ChildPath ('kurze-url-install-test-' + [guid]::NewGuid().ToString('N'))
$null = New-Item -ItemType Directory -Path $work

# --- helpers ---------------------------------------------------------------

function ConvertTo-FileUrl {
    <#
    .SYNOPSIS
    Spells a local path as a file:/// URL.
    #>
    param([string]$Path)
    # [Uri]$Path alone would read /tmp/x as a relative URI on a Unix machine.
    [Uri]::new($Path, [UriKind]::Absolute).AbsoluteUri
}

function ConvertTo-Literal {
    <#
    .SYNOPSIS
    Spells a string as a single-quoted PowerShell literal.
    #>
    param([string]$Text)
    "'" + $Text.Replace("'", "''") + "'"
}

function Get-Entry {
    <#
    .SYNOPSIS
    Lists the names inside a directory, sorted; nothing when it does not exist.
    #>
    param([string]$Path)
    @(Get-ChildItem -LiteralPath $Path -Force -Name -ErrorAction SilentlyContinue | Sort-Object)
}

function Get-Line {
    <#
    .SYNOPSIS
    Splits output into its non-empty lines.
    #>
    param([string]$Text)
    @($Text -split "\r?\n" | Where-Object { $_ -ne '' })
}

function Get-LastLine {
    <#
    .SYNOPSIS
    Returns the last non-empty line of some output, or an empty string.
    #>
    param([string]$Text)
    $lines = Get-Line -Text $Text
    if ($lines.Count -eq 0) {
        return ''
    }
    $lines[$lines.Count - 1]
}

function Format-Output {
    <#
    .SYNOPSIS
    Condenses both streams of a run into one line for a failure message.
    #>
    param($Result)
    $text = 'exit ' + $Result.ExitCode + '; stdout: ' + $Result.Stdout + '; stderr: ' + $Result.Stderr
    $text -replace '\s*\r?\n\s*', ' | '
}

function Assert-That {
    <#
    .SYNOPSIS
    Ends the case with a message unless the condition holds.
    #>
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) {
        throw $Message
    }
}

function Assert-Success {
    <#
    .SYNOPSIS
    Checks that a run exited with 0.
    #>
    param($Result)
    Assert-That -Condition ($Result.ExitCode -eq 0) -Message ('wanted exit 0: ' + (Format-Output -Result $Result))
}

function Assert-Failure {
    <#
    .SYNOPSIS
    Checks that a run exited with a non-zero status.
    #>
    param($Result)
    Assert-That -Condition ($Result.ExitCode -ne 0) -Message ('wanted a non-zero exit: ' + (Format-Output -Result $Result))
}

function Assert-Output {
    <#
    .SYNOPSIS
    Checks that stdout or stderr of a run holds a piece of text.
    #>
    param($Result, [string]$Text)
    $all = $Result.Stdout + "`n" + $Result.Stderr
    Assert-That -Condition $all.Contains($Text) -Message ("output lacks '$Text': " + (Format-Output -Result $Result))
}

function Assert-NoOutput {
    <#
    .SYNOPSIS
    Checks that neither stdout nor stderr of a run holds a piece of text.
    #>
    param($Result, [string]$Text)
    $all = $Result.Stdout + "`n" + $Result.Stderr
    Assert-That -Condition (-not $all.Contains($Text)) -Message ("output has '$Text': " + (Format-Output -Result $Result))
}

function Assert-Entry {
    <#
    .SYNOPSIS
    Checks the exact names inside a directory.
    #>
    param([string]$Path, [string[]]$Expected)
    $actual = (Get-Entry -Path $Path) -join ', '
    $wanted = ($Expected | Sort-Object) -join ', '
    Assert-That -Condition ($actual -ceq $wanted) -Message "$Path holds [$actual], wanted [$wanted]"
}

function Assert-TempClean {
    <#
    .SYNOPSIS
    Checks that the installer removed its own working directory from TEMP.
    #>
    $left = @(Get-Entry -Path $script:tmpDir | Where-Object { $_ -like 'kurze-url*' })
    Assert-That -Condition ($left.Count -eq 0) -Message ('left behind in TEMP: ' + ($left -join ', '))
}

function Skip-Case {
    <#
    .SYNOPSIS
    Marks the running case as skipped; the case then returns.
    #>
    param([string]$Reason)
    $script:skipReason = $Reason
}

function Get-SessionPath {
    <#
    .SYNOPSIS
    Reads the Path that a run reported through $reportSessionPath.
    #>
    param($Result)
    $line = @(Get-Line -Text $Result.Stdout | Where-Object { $_.StartsWith('SESSION_PATH=') })
    Assert-That -Condition ($line.Count -eq 1) -Message ('no SESSION_PATH line: ' + (Format-Output -Result $Result))
    $line[0].Substring('SESSION_PATH='.Length)
}

function Get-PathKey {
    <#
    .SYNOPSIS
    Spells a Path entry the way the installer compares them.
    #>
    param([string]$Entry)
    $Entry.Trim().TrimEnd('\')
}

function Get-PathCount {
    <#
    .SYNOPSIS
    Counts the entries of a Path that are the given directory.
    #>
    param([string]$PathValue, [string]$Directory)
    $count = 0
    foreach ($entry in ($PathValue -split ';')) {
        if ((Get-PathKey -Entry $entry) -eq (Get-PathKey -Entry $Directory)) {
            $count++
        }
    }
    $count
}

function Build-Binary {
    <#
    .SYNOPSIS
    Builds the fixture kurze-url.exe for a version, once, and returns its path.
    #>
    param([string]$Version)
    if ($script:binaries.ContainsKey($Version)) {
        return $script:binaries[$Version]
    }
    $out = Join-Path -Path (Join-Path -Path $work -ChildPath 'binaries') -ChildPath $Version
    $null = New-Item -ItemType Directory -Force -Path $out
    $file = Join-Path -Path $out -ChildPath 'kurze-url.exe'
    $ldflags = "-X main.version=$Version -X main.commit=abc1234 -X main.date=2026-10-24T00:00:00Z"
    $savedCgo = $env:CGO_ENABLED
    $env:CGO_ENABLED = '0'
    try {
        & go -C $cliDir build -trimpath -ldflags $ldflags -o $file ./cmd/kurze-url
        if ($LASTEXITCODE -ne 0) {
            throw "go build failed with exit $LASTEXITCODE"
        }
    }
    finally {
        $env:CGO_ENABLED = $savedCgo
    }
    $script:binaries[$Version] = $file
    $file
}

function Publish-Zip {
    <#
    .SYNOPSIS
    Writes a zip whose entries are the given files, each with mode 0755.
    #>
    param([string]$Path, [hashtable]$Entry)
    $stream = [IO.File]::Open($Path, [IO.FileMode]::Create)
    try {
        $zip = [IO.Compression.ZipArchive]::new($stream, [IO.Compression.ZipArchiveMode]::Create)
        try {
            foreach ($name in $Entry.Keys) {
                $item = $zip.CreateEntry($name)
                # A regular file, rwxr-xr-x, the way goreleaser writes it.
                $item.ExternalAttributes = (0x81ED -shl 16)
                $target = $item.Open()
                try {
                    $bytes = [IO.File]::ReadAllBytes($Entry[$name])
                    $target.Write($bytes, 0, $bytes.Length)
                }
                finally {
                    $target.Dispose()
                }
            }
        }
        finally {
            $zip.Dispose()
        }
    }
    finally {
        $stream.Dispose()
    }
}

function Add-Checksum {
    <#
    .SYNOPSIS
    Appends a file's line, in the shape goreleaser writes it, to a release's checksums.txt.
    #>
    param([string]$Dir, [string]$Name)
    $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path -Path $Dir -ChildPath $Name)).Hash.ToLowerInvariant()
    [IO.File]::AppendAllText((Join-Path -Path $Dir -ChildPath 'checksums.txt'), "$hash  $Name`n")
}

function Build-Release {
    <#
    .SYNOPSIS
    Packs a fake release into a directory: one zip per architecture, and checksums.txt.
    #>
    param([string]$Dir, [string]$Version, [string[]]$Arch = @('amd64', 'arm64'))
    $null = New-Item -ItemType Directory -Force -Path $Dir
    $binary = Build-Binary -Version $Version
    foreach ($a in $Arch) {
        $name = "kurze-url_windows_$a.zip"
        Publish-Zip -Path (Join-Path -Path $Dir -ChildPath $name) -Entry @{ 'kurze-url.exe' = $binary; 'LICENSE' = $licenseFile }
        Add-Checksum -Dir $Dir -Name $name
    }
}

function Edit-ZipHeader {
    <#
    .SYNOPSIS
    Overwrites the first byte of a file, which makes a zip unreadable as well as different.
    #>
    param([string]$Path)
    $bytes = [IO.File]::ReadAllBytes($Path)
    $bytes[0] = [byte][char]'x'
    [IO.File]::WriteAllBytes($Path, $bytes)
}

function Copy-Release {
    <#
    .SYNOPSIS
    Copies the default release into a directory of the case, to be damaged there.
    #>
    param([string]$Name)
    $copy = Join-Path -Path $script:dir -ChildPath $Name
    Copy-Item -LiteralPath $release -Destination $copy -Recurse
    $copy
}

function Get-WebShim {
    <#
    .SYNOPSIS
    Returns code for a child that puts a function in front of Invoke-WebRequest.
    .DESCRIPTION
    A function shadows the cmdlet. Without a FinalUri the shim records its
    arguments and throws, so no request ever leaves. With one it copies the
    file the URL names out of ServeDir and answers like the cmdlet does with
    -PassThru, saying the request ended up at FinalUri; Shape picks which
    spelling of that answer, Windows PowerShell's (ResponseUri on an
    HttpWebResponse) or PowerShell 7's (RequestMessage.RequestUri on an
    HttpResponseMessage).
    #>
    param([string]$Record, [string]$ServeDir = '', [string]$FinalUri = '', [string]$Shape = 'windows')
    if ($FinalUri -eq '') {
        return @"
function Invoke-WebRequest {
    param(`$Uri)
    Add-Content -LiteralPath $(ConvertTo-Literal -Text $Record) -Value ([string]`$Uri)
    throw 'offline shim: the tests have no network'
}
"@
    }
    if ($Shape -eq 'windows') {
        $answer = "[pscustomobject]@{ BaseResponse = [pscustomobject]@{ ResponseUri = [Uri]$(ConvertTo-Literal -Text $FinalUri) } }"
    }
    else {
        $answer = "[pscustomobject]@{ BaseResponse = [pscustomobject]@{ RequestMessage = [pscustomobject]@{ RequestUri = [Uri]$(ConvertTo-Literal -Text $FinalUri) } } }"
    }
    @"
function Invoke-WebRequest {
    param(`$Uri, `$OutFile)
    Add-Content -LiteralPath $(ConvertTo-Literal -Text $Record) -Value ([string]`$Uri + ' ' + (`$args -join ' '))
    `$name = ([string]`$Uri).Substring(([string]`$Uri).LastIndexOf('/') + 1)
    Copy-Item -LiteralPath (Join-Path -Path $(ConvertTo-Literal -Text $ServeDir) -ChildPath `$name) -Destination `$OutFile
    $answer
}
"@
}

function Invoke-Child {
    <#
    .SYNOPSIS
    Runs generated script text in a child shell of this one's flavour.
    #>
    param([string]$Source)
    $file = Join-Path -Path $script:dir -ChildPath ('run-' + [guid]::NewGuid().ToString('N') + '.ps1')
    # With a byte order mark, so Windows PowerShell 5.1 reads any non-ASCII
    # character of a path as the UTF-8 it is.
    [IO.File]::WriteAllText($file, $Source, [Text.UTF8Encoding]::new($true))
    $info = New-Object Diagnostics.ProcessStartInfo
    $info.FileName = $shellExe
    $info.Arguments = '-NoProfile -NonInteractive -ExecutionPolicy Bypass -File "' + $file + '"'
    $info.UseShellExecute = $false
    $info.CreateNoWindow = $true
    $info.RedirectStandardInput = $true
    $info.RedirectStandardOutput = $true
    $info.RedirectStandardError = $true
    $info.StandardOutputEncoding = [Text.UTF8Encoding]::new($false)
    $info.StandardErrorEncoding = [Text.UTF8Encoding]::new($false)
    $process = [Diagnostics.Process]::Start($info)
    try {
        $process.StandardInput.Close()
        $stdout = $process.StandardOutput.ReadToEndAsync()
        $stderr = $process.StandardError.ReadToEndAsync()
        if (-not $process.WaitForExit(240000)) {
            $process.Kill()
            throw 'the child shell did not finish in four minutes'
        }
        $process.WaitForExit()
        [pscustomobject]@{ ExitCode = $process.ExitCode; Stdout = $stdout.Result; Stderr = $stderr.Result }
    }
    finally {
        $process.Dispose()
    }
}

function Invoke-Installer {
    <#
    .SYNOPSIS
    Runs install.ps1 in a clean child environment, the way `irm | iex` does.
    .DESCRIPTION
    Environment overrides the defaults below; a value of $null removes the
    variable. Before and After are code that runs in the child around the
    installer, and Code replaces the installer's own line when a case needs to
    feed the child something else. The child prints what the installer prints,
    and a terminating error ends it with the error's message on stderr and
    exit status 1, which is what a person would see as a red error and what a
    CI step would see as a failure.
    #>
    param(
        [hashtable]$Environment = @{},
        [string]$Before = '',
        [string]$After = '',
        [string]$Code = '',
        [string]$InstallScript = $installPs1
    )
    $merged = @{
        LC_ALL                   = 'en_US.UTF-8'
        LC_MESSAGES              = $null
        LANG                     = $null
        KURZE_URL_VERSION        = $null
        KURZE_URL_DOWNLOAD_URL   = $script:releaseUrl
        KURZE_URL_INSTALL_DIR    = $script:bin
        KURZE_URL_NO_MODIFY_PATH = '1'
        LOCALAPPDATA             = $script:localAppData
        TEMP                     = $script:tmpDir
        TMP                      = $script:tmpDir
        TMPDIR                   = $script:tmpDir
        PROCESSOR_ARCHITECTURE   = 'AMD64'
        PROCESSOR_ARCHITEW6432   = $null
    }
    foreach ($key in $Environment.Keys) {
        $merged[$key] = $Environment[$key]
    }
    $setup = New-Object Collections.Generic.List[string]
    foreach ($key in ($merged.Keys | Sort-Object)) {
        $value = $merged[$key]
        if ($null -eq $value) {
            $literal = '$null'
        }
        else {
            $literal = ConvertTo-Literal -Text ([string]$value)
        }
        $setup.Add('[Environment]::SetEnvironmentVariable(' + (ConvertTo-Literal -Text $key) + ', ' + $literal + ')')
    }
    if ($Code -eq '') {
        $Code = 'Get-Content -Raw -LiteralPath ' + (ConvertTo-Literal -Text $InstallScript) + ' | Invoke-Expression'
    }
    $source = @"
`$ProgressPreference = 'SilentlyContinue'
try { [Console]::OutputEncoding = [Text.UTF8Encoding]::new(`$false) } catch { `$null = `$_ }
$($setup -join "`n")
Set-Location -LiteralPath $(ConvertTo-Literal -Text $script:dir)
$Before
try {
$Code
$After
}
catch {
    [Console]::Error.WriteLine(`$_.Exception.Message)
    exit 1
}
"@
    Invoke-Child -Source $source
}

function Use-SavedUserPath {
    <#
    .SYNOPSIS
    Runs a script block and then puts the user Path back as it was.
    #>
    param([scriptblock]$Body)
    $saved = [Environment]::GetEnvironmentVariable('Path', 'User')
    try {
        $null = & $Body
    }
    finally {
        [Environment]::SetEnvironmentVariable('Path', $saved, 'User')
    }
}

function Invoke-Case {
    <#
    .SYNOPSIS
    Runs one case in a directory of its own and prints ok, skip or FAIL.
    #>
    param([string]$Name, [scriptblock]$Body)
    $script:skipReason = $null
    $script:dir = Join-Path -Path (Join-Path -Path $work -ChildPath 'cases') -ChildPath $Name
    $script:bin = Join-Path -Path $script:dir -ChildPath 'bin'
    $script:tmpDir = Join-Path -Path $script:dir -ChildPath 'tmp'
    $script:localAppData = Join-Path -Path $script:dir -ChildPath 'localappdata'
    $null = New-Item -ItemType Directory -Force -Path $script:tmpDir, $script:localAppData
    try {
        $null = & $Body
        if ($null -eq $script:skipReason) {
            Assert-TempClean
        }
    }
    catch {
        $script:failed = $true
        Write-Output ('FAIL {0}: {1}' -f $Name, ($_.Exception.Message -replace '\s*\r?\n\s*', ' | '))
        return
    }
    if ($null -ne $script:skipReason) {
        Write-Output ('skip {0}: {1}' -f $Name, $script:skipReason)
    }
    else {
        Write-Output ('ok ' + $Name)
    }
}

# --- the fixtures ----------------------------------------------------------

try {
    $script:binaries = @{}
    $licenseFile = Join-Path -Path $work -ChildPath 'LICENSE'
    [IO.File]::WriteAllText($licenseFile, "MIT`n")

    # The release most cases install from. A case that needs a damaged one
    # copies it first.
    $release = Join-Path -Path $work -ChildPath 'release'
    Build-Release -Dir $release -Version '9.9.9'
    $releaseUrl = ConvertTo-FileUrl -Path $release
    $releaseNext = Join-Path -Path $work -ChildPath 'release-next'
    Build-Release -Dir $releaseNext -Version '9.9.10'
    $releaseNextUrl = ConvertTo-FileUrl -Path $releaseNext

    # --- the cases ---------------------------------------------------------

    Invoke-Case 'installs_into_the_given_dir' {
        $r = Invoke-Installer
        Assert-Success -Result $r
        $exe = Join-Path -Path $bin -ChildPath 'kurze-url.exe'
        Assert-That -Condition (Test-Path -LiteralPath $exe -PathType Leaf) -Message "no binary at $exe"
        $last = Get-LastLine -Text $r.Stdout
        Assert-That -Condition ($last -ceq $fakeVersionLine) -Message "stdout ends with '$last'"
        Assert-Output -Result $r -Text $exe
        Assert-Entry -Path $bin -Expected 'kurze-url.exe'
        $ran = & $exe version
        Assert-That -Condition ($ran -ceq $fakeVersionLine) -Message 'the installed binary does not run'
    }

    Invoke-Case 'defaults_to_localappdata_programs' {
        $r = Invoke-Installer -Environment @{ KURZE_URL_INSTALL_DIR = $null }
        Assert-Success -Result $r
        $expected = Join-Path -Path (Join-Path -Path $localAppData -ChildPath 'Programs') -ChildPath 'kurze-url'
        Assert-Entry -Path $expected -Expected 'kurze-url.exe'
        Assert-That -Condition ((Get-LastLine -Text $r.Stdout) -ceq $fakeVersionLine) -Message 'stdout does not end with the version line'
    }

    Invoke-Case 'installs_into_a_relative_dir' {
        # The child's PowerShell location is the case directory, while the
        # process's own working directory is not: a relative path has to be
        # read against the first.
        $r = Invoke-Installer -Environment @{ KURZE_URL_INSTALL_DIR = (Join-Path -Path 'rel' -ChildPath 'bin') }
        Assert-Success -Result $r
        Assert-Entry -Path (Join-Path -Path (Join-Path -Path $dir -ChildPath 'rel') -ChildPath 'bin') -Expected 'kurze-url.exe'
    }

    Invoke-Case 'refuses_when_localappdata_is_missing' {
        $r = Invoke-Installer -Environment @{ KURZE_URL_INSTALL_DIR = $null; LOCALAPPDATA = $null }
        Assert-Failure -Result $r
        Assert-Output -Result $r -Text 'KURZE_URL_INSTALL_DIR'
    }

    Invoke-Case 'refuses_a_checksum_mismatch' {
        $rel = Copy-Release -Name 'release'
        # Overwriting the first byte makes the archive unreadable as well as
        # different, so an installer that extracted before it checked would
        # fail on the extraction, and the message below would be missing.
        Edit-ZipHeader -Path (Join-Path -Path $rel -ChildPath $asset)
        $r = Invoke-Installer -Environment @{ KURZE_URL_DOWNLOAD_URL = (ConvertTo-FileUrl -Path $rel) }
        Assert-Failure -Result $r
        Assert-Output -Result $r -Text 'Checksum mismatch'
        Assert-Entry -Path $bin -Expected @()
    }

    Invoke-Case 'refuses_an_archive_missing_from_checksums' {
        $rel = Copy-Release -Name 'release'
        $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path -Path $rel -ChildPath $asset)).Hash.ToLowerInvariant()
        [IO.File]::WriteAllText((Join-Path -Path $rel -ChildPath 'checksums.txt'), "$hash  kurze-url_plan9_mips.zip`n")
        $r = Invoke-Installer -Environment @{ KURZE_URL_DOWNLOAD_URL = (ConvertTo-FileUrl -Path $rel) }
        Assert-Failure -Result $r
        Assert-Output -Result $r -Text $asset
        Assert-Output -Result $r -Text 'checksums.txt'
        Assert-Entry -Path $bin -Expected @()
    }

    Invoke-Case 'refuses_a_longer_name_that_contains_the_archive_name' {
        # A line is the archive's only when its name is the archive's name,
        # not when the name merely contains it. The right digest on such a
        # line, ahead of the archive's own wrong one, must not be taken.
        $rel = Copy-Release -Name 'release'
        $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path -Path $rel -ChildPath $asset)).Hash.ToLowerInvariant()
        $wrong = 'a' * 64
        [IO.File]::WriteAllText((Join-Path -Path $rel -ChildPath 'checksums.txt'), "$hash  $asset.sbom.json`n$wrong  $asset`n")
        $r = Invoke-Installer -Environment @{ KURZE_URL_DOWNLOAD_URL = (ConvertTo-FileUrl -Path $rel) }
        Assert-Failure -Result $r
        Assert-Output -Result $r -Text 'Checksum mismatch'
        Assert-Entry -Path $bin -Expected @()

        # And a longer name alone is no entry at all.
        [IO.File]::WriteAllText((Join-Path -Path $rel -ChildPath 'checksums.txt'), "$hash  $asset.sbom.json`n$hash  x-$asset`n")
        $r = Invoke-Installer -Environment @{ KURZE_URL_DOWNLOAD_URL = (ConvertTo-FileUrl -Path $rel) }
        Assert-Failure -Result $r
        Assert-Output -Result $r -Text 'has no entry'
        Assert-Entry -Path $bin -Expected @()
    }

    Invoke-Case 'reads_checksums_in_other_spellings' {
        $rel = Copy-Release -Name 'release'
        $hash = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path -Path $rel -ChildPath $asset)).Hash
        $spellings = @{
            'Windows line endings' = "$($hash.ToLowerInvariant())  $asset`r`n"
            'binary-mode marker'   = "$($hash.ToLowerInvariant()) *$asset`n"
            'upper-case digits'    = "$hash  $asset`n"
            'no final newline'     = "$($hash.ToLowerInvariant())  $asset"
        }
        foreach ($label in $spellings.Keys) {
            [IO.File]::WriteAllText((Join-Path -Path $rel -ChildPath 'checksums.txt'), $spellings[$label])
            $r = Invoke-Installer -Environment @{ KURZE_URL_DOWNLOAD_URL = (ConvertTo-FileUrl -Path $rel) }
            Assert-That -Condition ($r.ExitCode -eq 0) -Message "checksums.txt with $label was refused: $(Format-Output -Result $r)"
        }
    }

    Invoke-Case 'refuses_when_the_checksum_cannot_be_computed' {
        $r = Invoke-Installer -Before 'function Get-FileHash { throw ''no hashing today'' }'
        Assert-Failure -Result $r
        Assert-Output -Result $r -Text 'Could not compute the checksum'
        Assert-Entry -Path $bin -Expected @()
    }

    Invoke-Case 'refuses_a_failed_download' {
        $rel = Copy-Release -Name 'release'
        Remove-Item -LiteralPath (Join-Path -Path $rel -ChildPath $asset)
        $url = ConvertTo-FileUrl -Path $rel
        $r = Invoke-Installer -Environment @{ KURZE_URL_DOWNLOAD_URL = $url }
        Assert-Failure -Result $r
        Assert-Output -Result $r -Text "$url/$asset"
        Assert-Entry -Path $bin -Expected @()
    }

    Invoke-Case 'refuses_an_archive_without_the_binary' {
        $rel = Join-Path -Path $dir -ChildPath 'release'
        $null = New-Item -ItemType Directory -Path $rel
        Publish-Zip -Path (Join-Path -Path $rel -ChildPath $asset) -Entry @{ 'LICENSE' = $licenseFile }
        Add-Checksum -Dir $rel -Name $asset
        $r = Invoke-Installer -Environment @{ KURZE_URL_DOWNLOAD_URL = (ConvertTo-FileUrl -Path $rel) }
        Assert-Failure -Result $r
        Assert-Output -Result $r -Text $asset
        Assert-Entry -Path $bin -Expected @()
    }

    Invoke-Case 'maps_architectures' {
        # Only the archive the mapping should pick exists in each release, so
        # a wrong mapping is a failed download.
        $accepted = @(
            @{ Arch = 'AMD64'; Emulated = $null; Want = 'amd64' }
            @{ Arch = 'ARM64'; Emulated = $null; Want = 'arm64' }
            @{ Arch = 'arm64'; Emulated = $null; Want = 'arm64' }
            @{ Arch = 'x86'; Emulated = 'AMD64'; Want = 'amd64' }
            @{ Arch = 'x86'; Emulated = 'ARM64'; Want = 'arm64' }
        )
        foreach ($c in $accepted) {
            $rel = Join-Path -Path $dir -ChildPath ('release-' + $c.Want)
            if (Test-Path -LiteralPath $rel) {
                Remove-Item -LiteralPath $rel -Recurse -Force
            }
            Build-Release -Dir $rel -Version '9.9.9' -Arch @($c.Want)
            if (Test-Path -LiteralPath $bin) {
                Remove-Item -LiteralPath $bin -Recurse -Force
            }
            $r = Invoke-Installer -Environment @{
                KURZE_URL_DOWNLOAD_URL = (ConvertTo-FileUrl -Path $rel)
                PROCESSOR_ARCHITECTURE = $c.Arch
                PROCESSOR_ARCHITEW6432 = $c.Emulated
            }
            Assert-That -Condition ($r.ExitCode -eq 0) -Message "PROCESSOR_ARCHITECTURE=$($c.Arch) with PROCESSOR_ARCHITEW6432=$($c.Emulated) did not install kurze-url_windows_$($c.Want).zip: $(Format-Output -Result $r)"
            Assert-Entry -Path $bin -Expected 'kurze-url.exe'
        }
        # The default release holds both archives, so a machine this refuses
        # is refused by the mapping and not by a missing download.
        foreach ($arch in 'x86', 'ARM', 'IA64', '') {
            if (Test-Path -LiteralPath $bin) {
                Remove-Item -LiteralPath $bin -Recurse -Force
            }
            $r = Invoke-Installer -Environment @{ PROCESSOR_ARCHITECTURE = $arch }
            Assert-Failure -Result $r
            Assert-Output -Result $r -Text 'Unsupported architecture'
            if ($arch -ne '') {
                Assert-Output -Result $r -Text $arch
            }
            Assert-Entry -Path $bin -Expected @()
        }
    }

    Invoke-Case 'reinstalls_over_an_existing_binary' {
        $r = Invoke-Installer
        Assert-Success -Result $r
        Assert-That -Condition ((Get-LastLine -Text $r.Stdout) -ceq $fakeVersionLine) -Message 'the first install is not 9.9.9'
        $r = Invoke-Installer -Environment @{ KURZE_URL_DOWNLOAD_URL = $releaseNextUrl }
        Assert-Success -Result $r
        Assert-That -Condition ((Get-LastLine -Text $r.Stdout) -ceq $fakeVersionLineNext) -Message "the second install reports '$(Get-LastLine -Text $r.Stdout)', not 9.9.10"
        $ran = & (Join-Path -Path $bin -ChildPath 'kurze-url.exe') version
        Assert-That -Condition ($ran -ceq $fakeVersionLineNext) -Message 'the binary in place is not 9.9.10'
        Assert-Entry -Path $bin -Expected 'kurze-url.exe'
    }

    Invoke-Case 'cleans_up_the_leftover_of_an_earlier_run' {
        # An earlier run that could not delete kurze-url.exe.old, because the
        # process it belonged to was still running, leaves it behind. The next
        # run replaces it and removes it again.
        $r = Invoke-Installer
        Assert-Success -Result $r
        [IO.File]::WriteAllText((Join-Path -Path $bin -ChildPath 'kurze-url.exe.old'), 'stale')
        [IO.File]::WriteAllText((Join-Path -Path $bin -ChildPath 'kurze-url.exe.new'), 'stale')
        $r = Invoke-Installer -Environment @{ KURZE_URL_DOWNLOAD_URL = $releaseNextUrl }
        Assert-Success -Result $r
        Assert-That -Condition ((Get-LastLine -Text $r.Stdout) -ceq $fakeVersionLineNext) -Message 'the second install is not 9.9.10'
        Assert-Entry -Path $bin -Expected 'kurze-url.exe'
    }

    Invoke-Case 'reinstalls_while_the_old_binary_is_open' {
        $r = Invoke-Installer
        Assert-Success -Result $r
        $exe = Join-Path -Path $bin -ChildPath 'kurze-url.exe'
        # A running image is open for reading and may be renamed or deleted,
        # but not written: this share mode is the one Windows gives it.
        $share = [IO.FileShare]'Read, Delete'
        $handle = [IO.File]::Open($exe, [IO.FileMode]::Open, [IO.FileAccess]::Read, $share)
        try {
            $r = Invoke-Installer -Environment @{ KURZE_URL_DOWNLOAD_URL = $releaseNextUrl }
        }
        finally {
            $handle.Dispose()
        }
        Assert-Success -Result $r
        Assert-That -Condition ((Get-LastLine -Text $r.Stdout) -ceq $fakeVersionLineNext) -Message 'the new version is not in place'
        $ran = & $exe version
        Assert-That -Condition ($ran -ceq $fakeVersionLineNext) -Message 'the binary in place is not 9.9.10'
        $left = @(Get-Entry -Path $bin | Where-Object { $_ -like '*.new' })
        Assert-That -Condition ($left.Count -eq 0) -Message 'a .new file was left behind'
    }

    Invoke-Case 'rolls_back_when_the_new_binary_cannot_move_in' {
        $r = Invoke-Installer
        Assert-Success -Result $r
        # The shim refuses the last step, the rename of the staged copy over
        # kurze-url.exe, and lets every other move through.
        $shim = @'
function Move-Item {
    param($LiteralPath, $Destination, [switch]$Force)
    if ($LiteralPath -like '*.new') { throw 'shim: the new binary cannot move in' }
    Microsoft.PowerShell.Management\Move-Item -LiteralPath $LiteralPath -Destination $Destination -Force:$Force
}
'@
        $r = Invoke-Installer -Environment @{ KURZE_URL_DOWNLOAD_URL = $releaseNextUrl } -Before $shim
        Assert-Failure -Result $r
        Assert-Output -Result $r -Text 'Could not install into'
        $ran = & (Join-Path -Path $bin -ChildPath 'kurze-url.exe') version
        Assert-That -Condition ($ran -ceq $fakeVersionLine) -Message 'the old binary is not back in place'
        Assert-Entry -Path $bin -Expected 'kurze-url.exe'
    }

    Invoke-Case 'refuses_a_directory_named_like_the_binary' {
        $inTheWay = Join-Path -Path $bin -ChildPath 'kurze-url.exe'
        $null = New-Item -ItemType Directory -Path $inTheWay
        $r = Invoke-Installer
        Assert-Failure -Result $r
        # The refusal itself, not a later failure: a rename would otherwise
        # move the staged copy into the directory and the version call would
        # be what fails.
        Assert-Output -Result $r -Text 'is in the way'
        Assert-Output -Result $r -Text $inTheWay
        Assert-Entry -Path $bin -Expected 'kurze-url.exe'
        Assert-Entry -Path $inTheWay -Expected @()
    }

    Invoke-Case 'adds_the_dir_to_the_user_path_once' {
        if (-not $onWindows) {
            Skip-Case -Reason 'the user Path is a Windows setting, and .NET keeps none elsewhere'
            return
        }
        if ($env:CI -ne 'true') {
            Skip-Case -Reason 'changes the real user Path, so it runs only when CI is true'
            return
        }
        Use-SavedUserPath {
            $first = Invoke-Installer -Environment @{ KURZE_URL_NO_MODIFY_PATH = $null }
            Assert-Success -Result $first
            Assert-Output -Result $first -Text 'Added'
            $once = [Environment]::GetEnvironmentVariable('Path', 'User')
            Assert-That -Condition ((Get-PathCount -PathValue $once -Directory $bin) -eq 1) -Message "the user Path holds the directory $(Get-PathCount -PathValue $once -Directory $bin) times after one install"
            # The same directory, spelled with another case and a trailing
            # backslash, is the same directory.
            $second = Invoke-Installer -Environment @{ KURZE_URL_NO_MODIFY_PATH = $null; KURZE_URL_INSTALL_DIR = ($bin.ToUpperInvariant() + '\') }
            Assert-Success -Result $second
            Assert-NoOutput -Result $second -Text 'Added'
            $twice = [Environment]::GetEnvironmentVariable('Path', 'User')
            Assert-That -Condition ((Get-PathCount -PathValue $twice -Directory $bin) -eq 1) -Message "the user Path holds the directory $(Get-PathCount -PathValue $twice -Directory $bin) times after two installs"
            Assert-That -Condition ($twice -ceq $once) -Message 'the second install changed the user Path'
        }
    }

    Invoke-Case 'adds_the_dir_to_the_session_path' {
        if ($onWindows -and $env:CI -ne 'true') {
            Skip-Case -Reason 'changes the real user Path as well, so on Windows it runs only when CI is true'
            return
        }
        Use-SavedUserPath {
            $other = 'C:\other'
            $r = Invoke-Installer -Environment @{ KURZE_URL_NO_MODIFY_PATH = $null; Path = $other } -After $reportSessionPath
            Assert-Success -Result $r
            $session = Get-SessionPath -Result $r
            Assert-That -Condition ((Get-PathCount -PathValue $session -Directory $bin) -eq 1) -Message "the session Path is '$session'"
            Assert-That -Condition ((Get-PathCount -PathValue $session -Directory $other) -eq 1) -Message "the session Path lost its own entry: '$session'"

            # A directory the session already has, in another spelling, is
            # not added a second time.
            $spelled = $bin.ToUpperInvariant() + '\'
            $r = Invoke-Installer -Environment @{ KURZE_URL_NO_MODIFY_PATH = $null; Path = "$other;$spelled" } -After $reportSessionPath
            Assert-Success -Result $r
            $session = Get-SessionPath -Result $r
            Assert-That -Condition ((Get-PathCount -PathValue $session -Directory $bin) -eq 1) -Message "the session Path is '$session'"
        }
    }

    Invoke-Case 'leaves_the_path_alone_when_asked' {
        $userBefore = [Environment]::GetEnvironmentVariable('Path', 'User')
        $other = 'C:\other'
        $r = Invoke-Installer -Environment @{ Path = $other } -After $reportSessionPath
        Assert-Success -Result $r
        $userAfter = [Environment]::GetEnvironmentVariable('Path', 'User')
        Assert-That -Condition ($userAfter -ceq $userBefore) -Message 'the user Path changed'
        Assert-That -Condition ((Get-SessionPath -Result $r) -ceq $other) -Message 'the session Path changed'
        Assert-Output -Result $r -Text 'KURZE_URL_NO_MODIFY_PATH'
        Assert-Output -Result $r -Text $bin
        Assert-That -Condition ((Get-LastLine -Text $r.Stdout) -clike 'SESSION_PATH=*') -Message 'the report line is missing'

        # With the directory on the Path already, there is nothing to hint.
        $r = Invoke-Installer -Environment @{ Path = "$other;" + $bin.ToUpperInvariant() + '\' }
        Assert-Success -Result $r
        Assert-NoOutput -Result $r -Text 'KURZE_URL_NO_MODIFY_PATH'
    }

    Invoke-Case 'speaks_german_with_a_german_locale' {
        $rel = Copy-Release -Name 'release'
        Edit-ZipHeader -Path (Join-Path -Path $rel -ChildPath $asset)
        $url = ConvertTo-FileUrl -Path $rel
        # Each message is checked through a fragment of plain ASCII, because
        # the streams of a child shell take the code page of the console.
        # That the umlauts themselves come out right is checked, without a
        # stream in between, by every_message_has_both_languages.
        $german = 'stimmt nicht'
        $english = 'Checksum mismatch'
        $variants = @(
            @{ Label = 'LANG=de_DE.UTF-8'; Env = @{ LC_ALL = $null; LANG = 'de_DE.UTF-8' }; Want = 'de' }
            @{ Label = 'LC_MESSAGES=de_AT with an English LANG'; Env = @{ LC_ALL = $null; LC_MESSAGES = 'de_AT'; LANG = 'en_US.UTF-8' }; Want = 'de' }
            @{ Label = 'LC_ALL=DE_de'; Env = @{ LC_ALL = 'DE_de' }; Want = 'de' }
            # The first variable that is set decides alone: LC_ALL=C keeps a
            # German LANG out of it.
            @{ Label = 'LC_ALL=C with a German LANG'; Env = @{ LC_ALL = 'C'; LANG = 'de_DE' }; Want = 'en' }
            @{ Label = 'LANG=en_US.UTF-8'; Env = @{ LC_ALL = $null; LANG = 'en_US.UTF-8' }; Want = 'en' }
        )
        foreach ($v in $variants) {
            $environment = @{ KURZE_URL_DOWNLOAD_URL = $url }
            foreach ($key in $v.Env.Keys) {
                $environment[$key] = $v.Env[$key]
            }
            $r = Invoke-Installer -Environment $environment
            Assert-Failure -Result $r
            if ($v.Want -eq 'de') {
                Assert-That -Condition ($r.Stderr.Contains($german) -and -not $r.Stderr.Contains($english)) -Message "$($v.Label) did not speak German: $(Format-Output -Result $r)"
                Assert-Output -Result $r -Text 'herunter'
            }
            else {
                Assert-That -Condition ($r.Stderr.Contains($english) -and -not $r.Stderr.Contains($german)) -Message "$($v.Label) did not speak English: $(Format-Output -Result $r)"
                Assert-Output -Result $r -Text 'Downloading'
            }
        }
    }

    Invoke-Case 'falls_back_to_the_ui_culture' {
        $rel = Copy-Release -Name 'release'
        Edit-ZipHeader -Path (Join-Path -Path $rel -ChildPath $asset)
        $none = @{ KURZE_URL_DOWNLOAD_URL = (ConvertTo-FileUrl -Path $rel); LC_ALL = $null; LC_MESSAGES = $null; LANG = $null }
        $culture = 'function Get-UICulture { [pscustomobject]@{ TwoLetterISOLanguageName = ''LANGUAGE'' } }'

        $r = Invoke-Installer -Environment $none -Before $culture.Replace('LANGUAGE', 'de')
        Assert-Failure -Result $r
        Assert-Output -Result $r -Text 'stimmt nicht'

        $r = Invoke-Installer -Environment $none -Before $culture.Replace('LANGUAGE', 'en')
        Assert-Failure -Result $r
        Assert-Output -Result $r -Text 'Checksum mismatch'

        # The environment comes first: a set LC_ALL beats a German UI culture.
        $set = @{ KURZE_URL_DOWNLOAD_URL = (ConvertTo-FileUrl -Path $rel); LC_ALL = 'C' }
        $r = Invoke-Installer -Environment $set -Before $culture.Replace('LANGUAGE', 'de')
        Assert-Failure -Result $r
        Assert-Output -Result $r -Text 'Checksum mismatch'
    }

    Invoke-Case 'uses_the_version_in_the_url' {
        $record = Join-Path -Path $dir -ChildPath 'requests.txt'
        $r = Invoke-Installer -Environment @{ KURZE_URL_DOWNLOAD_URL = $null; KURZE_URL_VERSION = 'v0.1.0' } -Before (Get-WebShim -Record $record)
        Assert-Failure -Result $r
        Assert-That -Condition (Test-Path -LiteralPath $record) -Message "the web cmdlet was never called: $(Format-Output -Result $r)"
        $asked = @(Get-Content -LiteralPath $record)
        $want = "https://github.com/mheob/kurze-url/releases/download/v0.1.0/$asset"
        Assert-That -Condition ($asked -contains $want) -Message ('asked for: ' + ($asked -join ' '))
        Assert-Output -Result $r -Text $want
        Assert-Entry -Path $bin -Expected @()
    }

    Invoke-Case 'uses_the_latest_release_without_a_version' {
        $record = Join-Path -Path $dir -ChildPath 'requests.txt'
        $r = Invoke-Installer -Environment @{ KURZE_URL_DOWNLOAD_URL = $null } -Before (Get-WebShim -Record $record)
        Assert-Failure -Result $r
        Assert-That -Condition (Test-Path -LiteralPath $record) -Message "the web cmdlet was never called: $(Format-Output -Result $r)"
        $asked = @(Get-Content -LiteralPath $record)
        $want = "https://github.com/mheob/kurze-url/releases/latest/download/$asset"
        Assert-That -Condition ($asked -contains $want) -Message ('asked for: ' + ($asked -join ' '))
    }

    Invoke-Case 'lets_the_download_url_win_over_the_version' {
        $r = Invoke-Installer -Environment @{ KURZE_URL_VERSION = 'v0.1.0' }
        Assert-Success -Result $r
        Assert-Entry -Path $bin -Expected 'kurze-url.exe'
    }

    Invoke-Case 'refuses_a_download_url_that_is_not_https' {
        $record = Join-Path -Path $dir -ChildPath 'requests.txt'
        foreach ($url in 'http://downloads.example.test/kurze-url', 'ftp://downloads.example.test/kurze-url', 'downloads.example.test/kurze-url') {
            $r = Invoke-Installer -Environment @{ KURZE_URL_DOWNLOAD_URL = $url } -Before (Get-WebShim -Record $record)
            Assert-Failure -Result $r
            Assert-Output -Result $r -Text $url
            Assert-That -Condition (-not (Test-Path -LiteralPath $record)) -Message "a request was made for $url"
            Assert-Entry -Path $bin -Expected @()
        }
    }

    Invoke-Case 'downloads_through_the_web_cmdlet' {
        foreach ($shape in 'windows', 'core') {
            $record = Join-Path -Path $dir -ChildPath "requests-$shape.txt"
            $shim = Get-WebShim -Record $record -ServeDir $release -FinalUri 'https://objects.example.test/final' -Shape $shape
            if (Test-Path -LiteralPath $bin) {
                Remove-Item -LiteralPath $bin -Recurse -Force
            }
            $r = Invoke-Installer -Environment @{ KURZE_URL_DOWNLOAD_URL = 'https://downloads.example.test/kurze-url/' } -Before $shim
            Assert-Success -Result $r
            $asked = @(Get-Content -LiteralPath $record)
            Assert-That -Condition (@($asked | Where-Object { $_.StartsWith("https://downloads.example.test/kurze-url/$asset ") }).Count -eq 1) -Message ('asked for: ' + ($asked -join ' | '))
            Assert-That -Condition (@($asked | Where-Object { $_.StartsWith('https://downloads.example.test/kurze-url/checksums.txt ') }).Count -eq 1) -Message ('asked for: ' + ($asked -join ' | '))
            # Windows PowerShell 5.1 parses a page with Internet Explorer's
            # engine unless it is told not to.
            Assert-That -Condition (@($asked | Where-Object { $_ -notlike '* -UseBasicParsing*' }).Count -eq 0) -Message ('a request without -UseBasicParsing: ' + ($asked -join ' | '))
            Assert-Entry -Path $bin -Expected 'kurze-url.exe'
            Assert-That -Condition ((Get-LastLine -Text $r.Stdout) -ceq $fakeVersionLine) -Message 'stdout does not end with the version line'
        }
    }

    Invoke-Case 'refuses_a_redirect_to_plain_http' {
        # The cmdlet follows a redirect, and a download that began on https
        # may end on http. What arrived that way is refused, whichever way the
        # shell spells where a response ended up.
        foreach ($shape in 'windows', 'core') {
            $record = Join-Path -Path $dir -ChildPath "requests-$shape.txt"
            $shim = Get-WebShim -Record $record -ServeDir $release -FinalUri 'http://evil.example.test/final' -Shape $shape
            $r = Invoke-Installer -Environment @{ KURZE_URL_DOWNLOAD_URL = 'https://downloads.example.test/kurze-url' } -Before $shim
            Assert-Failure -Result $r
            Assert-Output -Result $r -Text 'http://evil.example.test/final'
            Assert-Entry -Path $bin -Expected @()
        }
    }

    Invoke-Case 'reads_where_a_real_response_ended_up' {
        # The cases above stand in for Invoke-WebRequest, so none of them
        # shows that the real cmdlet's answer has the shape the installer
        # reads it by, which differs between Windows PowerShell 5.1 and
        # PowerShell 7. This one runs the installer's own Save-Download,
        # taken out of install.ps1, against a server on this machine that
        # redirects once. The server speaks plain http, so the download must
        # be refused, and the refusal must name the URL the redirect ended
        # on and not the one that was asked for: if the answer's final
        # address were read wrongly there would be no refusal at all.
        $tokens = $null
        $errors = $null
        $ast = [Management.Automation.Language.Parser]::ParseFile($installPs1, [ref]$tokens, [ref]$errors)
        $definitions = New-Object Collections.Generic.List[string]
        foreach ($name in 'Get-Message', 'Save-Download') {
            $function = $ast.Find({ param($node) ($node -is [Management.Automation.Language.FunctionDefinitionAst]) -and ($node.Name -ceq $name) }, $true)
            Assert-That -Condition ($null -ne $function) -Message "install.ps1 has no $name function"
            $definitions.Add($function.Extent.Text)
        }
        $saved = Join-Path -Path $dir -ChildPath 'saved.bin'
        $code = @'
$ErrorActionPreference = 'Stop'
@@DEFINITIONS@@
$listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
$listener.Start()
$port = $listener.LocalEndpoint.Port
$server = [powershell]::Create()
$null = $server.AddScript({
        param($listener)
        $port = $listener.LocalEndpoint.Port
        try {
            while ($true) {
                $client = $listener.AcceptTcpClient()
                try {
                    $stream = $client.GetStream()
                    $stream.ReadTimeout = 5000
                    $buffer = New-Object byte[] 4096
                    $request = ''
                    while (-not $request.Contains("`r`n`r`n")) {
                        $count = $stream.Read($buffer, 0, $buffer.Length)
                        if ($count -le 0) { break }
                        $request += [Text.Encoding]::ASCII.GetString($buffer, 0, $count)
                    }
                    if ($request.StartsWith('GET /start')) {
                        $reply = "HTTP/1.1 302 Found`r`nLocation: http://127.0.0.1:$port/final`r`nContent-Length: 0`r`nConnection: close`r`n`r`n"
                    }
                    else {
                        $reply = "HTTP/1.1 200 OK`r`nContent-Type: application/octet-stream`r`nContent-Length: 5`r`nConnection: close`r`n`r`nhello"
                    }
                    $bytes = [Text.Encoding]::ASCII.GetBytes($reply)
                    $stream.Write($bytes, 0, $bytes.Length)
                    $stream.Flush()
                }
                finally {
                    $client.Close()
                }
            }
        }
        catch {
            $null = $_
        }
    }).AddArgument($listener)
$pending = $server.BeginInvoke()
try {
    foreach ($path in 'start', 'final') {
        try {
            Save-Download -Url "http://127.0.0.1:$port/$path" -Path @@SAVED@@
            Write-Output "NOT REFUSED: $path"
        }
        catch {
            Write-Output ("REFUSED $path`: " + $_.Exception.Message)
        }
        Write-Output ("LEFT $path`: " + (Test-Path -LiteralPath @@SAVED@@))
    }
}
finally {
    $listener.Stop()
    $server.Dispose()
}
'@
        $code = $code.Replace('@@DEFINITIONS@@', ($definitions -join "`n")).Replace('@@SAVED@@', (ConvertTo-Literal -Text $saved))
        $r = Invoke-Installer -Code $code
        Assert-Success -Result $r
        foreach ($path in 'start', 'final') {
            $refused = @(Get-Line -Text $r.Stdout | Where-Object { $_.StartsWith("REFUSED ${path}: ") })
            Assert-That -Condition ($refused.Count -eq 1) -Message "the download of /$path was not refused: $(Format-Output -Result $r)"
            Assert-That -Condition ($refused[0] -match 'Refusing http://127\.0\.0\.1:\d+/final: only https') -Message "the refusal does not name where the response ended up: $($refused[0])"
            Assert-That -Condition (@(Get-Line -Text $r.Stdout | Where-Object { $_ -ceq "LEFT ${path}: False" }).Count -eq 1) -Message "the refused download of /$path was left on disk: $(Format-Output -Result $r)"
        }
    }

    Invoke-Case 'a_truncated_script_does_nothing' {
        # irm | iex runs what has arrived. Every prefix of the script, the
        # first half included, must run nothing: not a download, not a
        # directory, not even a temporary one. Most of them are a parse
        # error, because the one function that is the whole body has not
        # closed yet; the rest only define it. The child tries every prefix
        # in turn, and nothing is reset between them, so the first one that
        # leaves a trace is the one reported.
        $record = Join-Path -Path $dir -ChildPath 'requests.txt'
        $code = @"
`$lines = [IO.File]::ReadAllLines($(ConvertTo-Literal -Text $installPs1))
for (`$n = 1; `$n -lt `$lines.Count; `$n++) {
    `$text = (`$lines[0..(`$n - 1)]) -join [Environment]::NewLine
    try { Invoke-Expression `$text } catch { `$null = `$_ }
    if ((Test-Path -LiteralPath $(ConvertTo-Literal -Text $record)) -or (Test-Path -LiteralPath $(ConvertTo-Literal -Text $bin))) {
        Write-Output "trace after `$n of `$(`$lines.Count) lines"
        break
    }
}
Write-Output "tried `$(`$lines.Count - 1) prefixes"
"@
        $r = Invoke-Installer -Before (Get-WebShim -Record $record) -Code $code
        Assert-Success -Result $r
        Assert-NoOutput -Result $r -Text 'trace after'
        $lines = @(Get-Line -Text $r.Stdout)
        Assert-That -Condition ($lines.Count -eq 1 -and $lines[0] -clike 'tried * prefixes') -Message ('a prefix printed something: ' + (Format-Output -Result $r))
        Assert-That -Condition (-not (Test-Path -LiteralPath $record)) -Message 'a prefix downloaded something'
        Assert-That -Condition (-not (Test-Path -LiteralPath $bin)) -Message 'a prefix created the install directory'
        Assert-Entry -Path $script:localAppData -Expected @()
    }

    Invoke-Case 'has_nothing_at_the_top_level_but_the_function_and_its_call' {
        $tokens = $null
        $errors = $null
        $ast = [Management.Automation.Language.Parser]::ParseFile($installPs1, [ref]$tokens, [ref]$errors)
        Assert-That -Condition (@($errors).Count -eq 0) -Message ('install.ps1 does not parse: ' + (@($errors | ForEach-Object { $_.Message }) -join '; '))
        $statements = @($ast.EndBlock.Statements)
        Assert-That -Condition ($statements.Count -eq 2) -Message "install.ps1 has $($statements.Count) top-level statements, not 2"
        Assert-That -Condition ($statements[0] -is [Management.Automation.Language.FunctionDefinitionAst]) -Message 'the first statement is not a function definition'
        $call = $statements[1]
        Assert-That -Condition (($call -is [Management.Automation.Language.PipelineAst]) -and ($call.PipelineElements[0].GetCommandName() -ceq $statements[0].Name)) -Message "the last statement is not a call of $($statements[0].Name)"
        # Windows PowerShell 5.1 reads a file without a byte order mark in the
        # ANSI code page, and a byte order mark in front of `irm | iex` is not
        # read as one: the file stays ASCII, and the German umlauts are
        # written as escapes.
        $wide = @([IO.File]::ReadAllBytes($installPs1) | Where-Object { $_ -gt 127 })
        Assert-That -Condition ($wide.Count -eq 0) -Message "install.ps1 holds $($wide.Count) bytes outside ASCII"
    }

    Invoke-Case 'every_message_has_both_languages' {
        $tokens = $null
        $errors = $null
        $ast = [Management.Automation.Language.Parser]::ParseFile($installPs1, [ref]$tokens, [ref]$errors)
        $function = $ast.Find({ param($node) ($node -is [Management.Automation.Language.FunctionDefinitionAst]) -and ($node.Name -ceq 'Get-Message') }, $true)
        Assert-That -Condition ($null -ne $function) -Message 'install.ps1 has no Get-Message function'

        # The two tables, one per language, hold the same keys.
        $tables = @($function.FindAll({ param($node) $node -is [Management.Automation.Language.HashtableAst] }, $true))
        Assert-That -Condition ($tables.Count -eq 2) -Message "Get-Message holds $($tables.Count) hashtables, not one per language"
        $first = @(foreach ($pair in $tables[0].KeyValuePairs) { $pair.Item1.Value }) | Sort-Object
        $second = @(foreach ($pair in $tables[1].KeyValuePairs) { $pair.Item1.Value }) | Sort-Object
        Assert-That -Condition (($first -join ', ') -ceq ($second -join ', ')) -Message ("the languages disagree on the keys: [" + ($first -join ', ') + '] against [' + ($second -join ', ') + ']')

        # Every key the script asks for exists, and every key it has is asked for.
        $calls = @($ast.FindAll({ param($node) ($node -is [Management.Automation.Language.CommandAst]) -and ($node.GetCommandName() -ceq 'Get-Message') }, $true))
        $used = @(foreach ($call in $calls) {
                $key = $call.CommandElements[1]
                Assert-That -Condition ($key -is [Management.Automation.Language.StringConstantExpressionAst]) -Message "Get-Message is called with a key that is not a plain string: $($call.Extent.Text)"
                $key.Value
            }) | Sort-Object -Unique
        foreach ($key in $used) {
            Assert-That -Condition ($first -contains $key) -Message "Get-Message is asked for '$key', which has no text"
        }
        foreach ($key in $first) {
            Assert-That -Condition ($used -contains $key) -Message "'$key' has a text that nothing asks for"
        }

        # Both texts exist, are different, and mention the same arguments.
        . ([scriptblock]::Create($function.Extent.Text))
        $saved = $env:LC_ALL
        try {
            foreach ($key in $first) {
                $env:LC_ALL = 'en'
                $en = Get-Message $key '<0>', '<1>', '<2>', '<3>'
                $env:LC_ALL = 'de'
                $de = Get-Message $key '<0>', '<1>', '<2>', '<3>'
                Assert-That -Condition (($en -ne '') -and ($de -ne '')) -Message "'$key' has an empty text"
                Assert-That -Condition (($en -cne $key) -and ($de -cne $key)) -Message "'$key' falls back to its own name"
                Assert-That -Condition ($en -cne $de) -Message "'$key' reads the same in English and German"
                Assert-That -Condition (($en + $de) -notmatch '\\u[0-9a-fA-F]{4}|\{\d\}') -Message "'$key' has an escape or a placeholder left over"
                $enArgs = @([regex]::Matches($en, '<\d>') | ForEach-Object { $_.Value } | Sort-Object -Unique) -join ''
                $deArgs = @([regex]::Matches($de, '<\d>') | ForEach-Object { $_.Value } | Sort-Object -Unique) -join ''
                Assert-That -Condition ($enArgs -ceq $deArgs) -Message "'$key' mentions $enArgs in English and $deArgs in German"
            }
            # The escapes become the umlauts they stand for.
            $env:LC_ALL = 'de'
            $mismatch = Get-Message 'checksum_mismatch' 'a', 'b', 'c'
            Assert-That -Condition ($mismatch.Contains('Pr' + [string][char]0xFC + 'fsumme')) -Message "the German mismatch message reads: $mismatch"
        }
        finally {
            if ($null -eq $saved) {
                Remove-Item -LiteralPath 'Env:\LC_ALL' -ErrorAction SilentlyContinue
            }
            else {
                $env:LC_ALL = $saved
            }
        }
    }
}
finally {
    Remove-Item -LiteralPath $work -Recurse -Force -ErrorAction SilentlyContinue
}

if ($script:failed) {
    exit 1
}
