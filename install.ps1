# Install termchat on Windows: https://github.com/sw4p/termchat
#
#   irm https://github.com/sw4p/termchat/releases/latest/download/install.ps1 | iex
#
# Downloads the latest release for this CPU, verifies its SHA-256 checksum,
# installs termchat.exe to %LOCALAPPDATA%\Programs\termchat and adds that
# folder to your user PATH. No admin rights needed. Works in Windows
# PowerShell 5.1 and PowerShell 7. Settings, all optional:
#
#   TERMCHAT_VERSION      install this version (e.g. 1.2.0) instead of the latest
#   TERMCHAT_INSTALL_DIR  install here instead
#   TERMCHAT_BASE_URL     download from here instead of the GitHub release
#                         (used to test against a local build: file:///C:/.../dist)
#
# Everything runs inside one function and fails with throw, never exit, so an
# error can't close the window of someone who ran it through iex.

function Install-Termchat {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue' # the progress bar makes downloads crawl on 5.1
    $repo = 'sw4p/termchat'

    function Write-Say([string]$msg) { [Console]::Error.WriteLine("termchat: $msg") }

    # Fetches url (https:// or file://) to the file at path.
    function Save-Url([string]$url, [string]$path) {
        try {
            $uri = [Uri]$url
            if ($uri.IsFile) {
                Copy-Item -LiteralPath $uri.LocalPath -Destination $path
            } else {
                Invoke-WebRequest -Uri $url -OutFile $path -UseBasicParsing
            }
        } catch {
            throw "termchat: download failed: $url ($($_.Exception.Message))"
        }
    }

    # Windows PowerShell 5.1 may default to TLS 1.0, which GitHub refuses.
    if ($PSVersionTable.PSEdition -ne 'Core') {
        [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
    }

    # --- What to download ----------------------------------------------------

    # The OS architecture, not this process's: a 32-bit or emulated x64 shell
    # on ARM64 should still get the native build.
    $cpu = $null
    try {
        $cpu = [Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString()
    } catch {
        $cpu = $null # .NET Framework before 4.7.1; use the environment instead
    }
    if (-not $cpu) {
        $cpu = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    }
    $arch = switch ($cpu) {
        { $_ -in 'X64', 'AMD64' } { 'amd64' }
        { $_ -in 'Arm64', 'ARM64' } { 'arm64' }
        default { throw "termchat: unsupported CPU $cpu; termchat is built for amd64 and arm64" }
    }

    $version = $env:TERMCHAT_VERSION
    if (-not $version) {
        # /releases/latest redirects to /releases/tag/vX.Y.Z; read the tag from it.
        $latest = $null
        try {
            $req = [Net.WebRequest]::Create("https://github.com/$repo/releases/latest")
            $req.Method = 'HEAD'
            $resp = $req.GetResponse()
            $latest = $resp.ResponseUri.AbsoluteUri
            $resp.Close()
        } catch {
            throw "termchat: couldn't look up the latest release ($($_.Exception.Message))"
        }
        $version = $latest.Substring($latest.LastIndexOf('/') + 1)
        if ($version -notmatch '^v?[0-9]') {
            throw "termchat: couldn't work out the latest version from $latest"
        }
    }
    $version = $version -replace '^v', ''

    $base = $env:TERMCHAT_BASE_URL
    if (-not $base) { $base = "https://github.com/$repo/releases/download/v$version" }
    $base = $base.TrimEnd('/')
    $archive = "termchat_${version}_windows_${arch}.zip"

    # --- Download and verify -------------------------------------------------

    $tmp = Join-Path ([IO.Path]::GetTempPath()) ('termchat-' + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        Write-Say "downloading termchat $version for windows/$arch"
        Save-Url "$base/$archive" (Join-Path $tmp $archive)
        Save-Url "$base/checksums.txt" (Join-Path $tmp 'checksums.txt')

        $want = $null
        foreach ($line in Get-Content -LiteralPath (Join-Path $tmp 'checksums.txt')) {
            $fields = $line.Trim() -split '\s+'
            if ($fields.Count -eq 2 -and $fields[1] -eq $archive) { $want = $fields[0] }
        }
        if (-not $want) { throw "termchat: $archive isn't listed in checksums.txt" }
        $got = (Get-FileHash -LiteralPath (Join-Path $tmp $archive) -Algorithm SHA256).Hash
        if ($got -ne $want) { # -ne ignores case, and Get-FileHash gives upper case
            throw "termchat: checksum mismatch for $archive (expected $want, got $got); not installing"
        }
        Write-Say 'checksum OK'

        $unpacked = Join-Path $tmp 'unpacked'
        Expand-Archive -LiteralPath (Join-Path $tmp $archive) -DestinationPath $unpacked
        $new = Join-Path $unpacked 'termchat.exe'
        if (-not (Test-Path -LiteralPath $new)) { throw "termchat: $archive has no termchat.exe" }

        # --- Install ---------------------------------------------------------

        $dir = $env:TERMCHAT_INSTALL_DIR
        if (-not $dir) { $dir = Join-Path $env:LOCALAPPDATA 'Programs\termchat' }
        New-Item -ItemType Directory -Path $dir -Force | Out-Null
        $exe = Join-Path $dir 'termchat.exe'

        # Windows can't overwrite a running exe, but it can rename one: move
        # any old copy aside first, so a running termchat doesn't block us.
        $old = "$exe.old"
        Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue
        if (Test-Path -LiteralPath $exe) { Move-Item -LiteralPath $exe -Destination $old -Force }
        try {
            Copy-Item -LiteralPath $new -Destination $exe
        } catch {
            if (Test-Path -LiteralPath $old) { Move-Item -LiteralPath $old -Destination $exe -Force }
            throw "termchat: couldn't install to $dir (try `$env:TERMCHAT_INSTALL_DIR = '...')"
        }
        Remove-Item -LiteralPath $old -Force -ErrorAction SilentlyContinue # fails if it's still running; that's fine

        # Clear the downloaded-from-the-internet mark, so SmartScreen doesn't
        # stop the first run.
        if (Get-Command Unblock-File -ErrorAction SilentlyContinue) { Unblock-File -LiteralPath $exe }
        Write-Say "installed $exe"
    } finally {
        Remove-Item -LiteralPath $tmp -Recurse -Force -ErrorAction SilentlyContinue
    }

    # Add the folder to the user PATH (not the machine one, so no admin).
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $entries = @($userPath -split ';' | Where-Object { $_ } | ForEach-Object { $_.TrimEnd('\') })
    if ($entries -notcontains $dir.TrimEnd('\')) {
        $newPath = if ($userPath) { $userPath.TrimEnd(';') + ";$dir" } else { $dir }
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
        $env:Path = $env:Path.TrimEnd(';') + ";$dir"
        Write-Say "added $dir to your PATH; open a new terminal to use termchat"
    }

    try {
        & $exe --version *> $null
        if ($LASTEXITCODE -ne 0) { Write-Say 'warning: the installed binary did not run' }
    } catch {
        Write-Say "warning: the installed binary did not run ($($_.Exception.Message))"
    }
}

Install-Termchat
