# Installs acmd on Windows, with no Scoop and no admin:
#
#   irm https://raw.githubusercontent.com/brettmostert/agent-command/main/scripts/install.ps1 | iex
#
# It installs the latest release into
# %LOCALAPPDATA%\Programs\agent-command\<version>, points the junction
# current at it, puts current on your PATH, and runs acmd init, which adds the
# logon task. Running it again upgrades acmd: it moves the junction, and the
# daemon restarts on the new version by itself. That's how Scoop installs
# acmd, so the logon task and upgrades work the same way. If acmd from Scoop
# is installed, it stops: upgrade that one with Scoop.
#
# irm … | iex passes no arguments, so the options are environment variables:
#   $env:ACMD_VERSION = "0.96.0"   install that version, not the latest
#   $env:ACMD_NO_INIT = 1          don't run acmd init
#   $env:ACMD_UNINSTALL = 1        run acmd uninstall, then remove acmd
#   $env:ACMD_INSTALL_URL          where releases are, for tests and mirrors
#
# A release's checksums.txt names its files and their SHA-256s. If gh is
# installed and logged in, the download's build attestation is checked too.
#
# It runs in Windows PowerShell 5.1 and PowerShell 7. All of it is one script
# block, so a download cut short runs nothing. Errors are thrown, never exit:
# under iex, exit would close the window.

& {
    Set-StrictMode -Version 2
    $ErrorActionPreference = 'Stop'
    # Windows PowerShell's progress bar slows a download to a crawl.
    $ProgressPreference = 'SilentlyContinue'

    $repo = 'brettmostert/agent-command'
    $releases = 'https://github.com/brettmostert/homebrew-tap/releases'
    if ($env:ACMD_INSTALL_URL) { $releases = $env:ACMD_INSTALL_URL.TrimEnd('/') }

    function Say([string]$Message) { Write-Host "acmd install: $Message" }
    # Fail stops the script. The message is printed on one line at the end,
    # as an error record would be wrapped at the window's width.
    function Fail([string]$Message) { throw $Message }

    # On is true for a variable set to anything but "", 0, false or no.
    function On([string]$Value) { $Value -and (@('0', 'false', 'no') -notcontains $Value.ToLower()) }

    if (-not $env:LOCALAPPDATA) { Fail "LOCALAPPDATA isn't set" }
    # Not %LOCALAPPDATA%\agent-command: that's where acmd keeps its own files.
    $root = Join-Path $env:LOCALAPPDATA 'Programs\agent-command'
    $current = Join-Path $root 'current'
    $acmd = Join-Path $current 'acmd.exe'

    # The machine's own architecture, which an emulated PowerShell doesn't report.
    function Get-Arch {
        $machine = (Get-ItemProperty 'HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager\Environment').PROCESSOR_ARCHITECTURE
        switch ($machine) {
            'AMD64' { 'amd64' }
            'ARM64' { 'arm64' }
            default { Fail "acmd runs on Windows amd64 and arm64, not $machine" }
        }
    }

    # The version of acmd that Scoop installed, if it did.
    function Get-ScoopAcmd {
        $dirs = @(
            $(if ($env:SCOOP) { $env:SCOOP } else { Join-Path $env:USERPROFILE 'scoop' }),
            $(if ($env:SCOOP_GLOBAL) { $env:SCOOP_GLOBAL } else { Join-Path $env:ProgramData 'scoop' })
        )
        foreach ($dir in $dirs) {
            $manifest = Join-Path $dir 'apps\acmd\current\manifest.json'
            if (Test-Path $manifest) { return (Get-Content $manifest -Raw | ConvertFrom-Json).version }
        }
    }

    # The version current points at, if this script installed it.
    function Get-Installed {
        $item = Get-Item -Force -ErrorAction SilentlyContinue $current
        if (-not $item -or -not $item.Target) { return $null }
        $target = @($item.Target)[0]
        if (-not (Test-Path (Join-Path $target 'acmd.exe'))) { return $null }
        Split-Path -Leaf $target
    }

    # Runs a program, shows its output (none with -Quiet), and returns only
    # its exit code. Windows PowerShell turns a program's stderr into errors,
    # which 'Stop' would throw.
    function Invoke-Program([string]$Path, [string[]]$Arguments, [switch]$Quiet) {
        $ErrorActionPreference = 'Continue'
        if ($Quiet) { & $Path @Arguments *> $null } else { & $Path @Arguments | Out-Host }
        $LASTEXITCODE
    }

    # The user's PATH as the registry holds it, with %VARIABLES% unexpanded.
    function Get-UserPath {
        (Get-Item 'HKCU:\Environment').GetValue('Path', '', 'DoNotExpandEnvironmentNames')
    }

    function Set-UserPath([string]$Value) {
        Set-ItemProperty 'HKCU:\Environment' -Name Path -Value $Value -Type ExpandString
        # Setting a variable through .NET tells running programs, such as
        # Explorer, that the environment changed, so new terminals see the PATH.
        [Environment]::SetEnvironmentVariable('ACMD_INSTALL_PATH_CHANGED', '1', 'User')
        [Environment]::SetEnvironmentVariable('ACMD_INSTALL_PATH_CHANGED', $null, 'User')
    }

    # Get-FileHash and Expand-Archive are script modules in Windows PowerShell,
    # and don't load when it's started from PowerShell 7 (its PSModulePath
    # carries over). .NET does the same work in both.
    function Get-Sha256([string]$Path) {
        $sha = [Security.Cryptography.SHA256]::Create()
        $stream = [IO.File]::OpenRead($Path)
        try {
            -join ($sha.ComputeHash($stream) | ForEach-Object { $_.ToString('x2') })
        } finally {
            $stream.Dispose()
            $sha.Dispose()
        }
    }

    function Expand-Zip([string]$Zip, [string]$To) {
        Add-Type -AssemblyName System.IO.Compression.FileSystem
        [IO.Compression.ZipFile]::ExtractToDirectory($Zip, $To)
    }

    function Test-Attestation([string]$Zip, [string]$Name) {
        if (-not (Get-Command gh -ErrorAction SilentlyContinue)) { return }
        if ((Invoke-Program gh @('auth', 'status') -Quiet) -ne 0) { return }
        $ErrorActionPreference = 'Continue'
        $out = & gh attestation verify $Zip --repo $repo --signer-workflow "$repo/.github/workflows/release.yml" 2>&1
        if ($LASTEXITCODE -ne 0) { Fail "$Name's build attestation doesn't check out: $out" }
        Say "checked $Name's build attestation"
    }

    function Uninstall-Acmd {
        $installed = Get-Installed
        if (-not $installed) {
            Say "$current isn't one this script installed, so there's nothing to remove"
            return
        }
        if ((Invoke-Program $acmd @('uninstall', '--yes')) -ne 0) { Fail "acmd uninstall failed, so acmd is left installed" }
        [IO.Directory]::Delete($current)
        try { Remove-Item -Recurse -Force $root } catch { Say "couldn't remove $root, so remove it once acmd has stopped: $_" }
        $parts = @((Get-UserPath) -split ';' | Where-Object { $_ -and $_ -ne $current })
        Set-UserPath ($parts -join ';')
        $env:Path = (@($env:Path -split ';' | Where-Object { $_ -ne $current })) -join ';'
        Say "removed acmd $installed"
    }

    function Install-Acmd {
        $arch = Get-Arch
        $scoop = Get-ScoopAcmd
        if ($scoop) { Fail "acmd $scoop is installed with Scoop, so this script leaves it alone. Upgrade it with: scoop update acmd" }
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

        $want = "$env:ACMD_VERSION".TrimStart('v')
        if ($want) {
            $sumsUrl = "$releases/download/agent-command-v$want/checksums.txt"
        } else {
            $sumsUrl = "$releases/latest/download/checksums.txt"
        }
        $tmp = Join-Path ([IO.Path]::GetTempPath()) ('acmd-install-' + [guid]::NewGuid())
        New-Item -ItemType Directory $tmp | Out-Null
        try {
            $sums = Join-Path $tmp 'checksums.txt'
            try { Invoke-WebRequest -UseBasicParsing -Uri $sumsUrl -OutFile $sums } catch { Fail "couldn't download ${sumsUrl}: $($_.Exception.Message)" }
            $sum = $null
            foreach ($line in Get-Content $sums) {
                if ($line -match ('^([0-9a-f]{64})  (acmd-(.+)-windows-' + $arch + '\.zip)$')) {
                    $sum, $file, $version = $Matches[1], $Matches[2], $Matches[3]
                    break
                }
            }
            if (-not $sum) { Fail "the release has no acmd for windows-$arch" }
            if ($want -and $version -ne $want) { Fail "asked for $want, but the release has acmd $version" }

            $previous = Get-Installed
            if ($previous -eq $version) {
                Say "acmd $version is already installed. Run acmd init to set it up again."
                return
            }

            Say "downloading acmd $version for windows-$arch"
            $zip = Join-Path $tmp $file
            $url = "$releases/download/agent-command-v$version/$file"
            try { Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $zip } catch { Fail "couldn't download ${url}: $($_.Exception.Message)" }
            $got = Get-Sha256 $zip
            if ($got -ne $sum) { Fail "$file's SHA-256 is $got, but the release says $sum" }
            Test-Attestation $zip $file

            # Unpacked next to where it goes, so the move is a rename. The new
            # acmd must run before anything points at it.
            New-Item -ItemType Directory -Force $root | Out-Null
            $unpack = Join-Path $root ".unpack-$PID"
            if (Test-Path $unpack) { Remove-Item -Recurse -Force $unpack }
            Expand-Zip $zip $unpack
            foreach ($exe in 'acmd.exe', 'acmdw.exe') {
                if (-not (Test-Path (Join-Path $unpack $exe))) { Fail "$file has no $exe" }
            }
            if ((Invoke-Program (Join-Path $unpack 'acmd.exe') @('version') -Quiet) -ne 0) { Fail "acmd $version doesn't run here" }
            $dir = Join-Path $root $version
            if (Test-Path $dir) { Remove-Item -Recurse -Force $dir }
            Move-Item $unpack $dir
        } finally {
            Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $tmp
        }

        # The logon task runs current\acmdw.exe, and the daemon watches
        # current\acmd.exe, so moving the junction is the upgrade.
        if (Get-Item -Force -ErrorAction SilentlyContinue $current) { [IO.Directory]::Delete($current) }
        New-Item -ItemType Junction -Path $current -Target $dir | Out-Null

        # A version still running can't be removed yet: a later run removes it.
        Get-ChildItem -Directory -Force $root | Where-Object { $_.Name -match '^\d' -and $_.Name -ne $version } | ForEach-Object {
            try { Remove-Item -Recurse -Force $_.FullName } catch { }
        }

        $parts = @((Get-UserPath) -split ';' | Where-Object { $_ })
        if ($parts -notcontains $current) { Set-UserPath ((@($parts) + $current) -join ';') }
        if (@($env:Path -split ';') -notcontains $current) { $env:Path = "$env:Path;$current" }

        if ($previous) {
            Say "upgraded acmd from $previous to $version, in $current"
        } else {
            Say "installed acmd $version in $current"
        }

        if (On $env:ACMD_NO_INIT) {
            Say "run acmd init to add the logon task"
        } elseif ((Invoke-Program $acmd @('init')) -ne 0) {
            Fail "acmd init failed: run acmd init again once the problem it names is fixed"
        }

        $found = Get-Command acmd -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($found -and $found.Source -ne $acmd) { Say "warning: $($found.Source) comes before $acmd on your PATH" }
    }

    try {
        if (On $env:ACMD_UNINSTALL) { Uninstall-Acmd } else { Install-Acmd }
    } catch {
        Write-Host "acmd install: $($_.Exception.Message)" -ForegroundColor Red
        throw 'acmd install failed: see the line above'
    }
}
