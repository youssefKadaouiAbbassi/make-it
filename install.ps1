# Install makeit on Windows: download the release binary, check it, then run
# `makeit self install --yes`, which puts it in %USERPROFILE%\.local\bin and records the install.
#   irm https://github.com/<repo>/releases/download/v<version>/install.ps1 | iex
# The copy attached to a release carries that release's version, repository and public key.
# MAKEIT_VERSION installs another version; MAKEIT_RELEASES_URL replaces https://github.com.
# Then it runs `makeit setup` (MAKEIT_YES=1: without asking; MAKEIT_NO_SETUP=1: not at all).
# irm | iex runs in the caller's session: everything lives in a script block, and failures throw
# instead of exit, so the window stays open and no variable or preference leaks.
& {
    $ErrorActionPreference = 'Stop'

    $version = if ($env:MAKEIT_VERSION) { $env:MAKEIT_VERSION } else { '@VERSION@' }
    $repo = '@REPO@'
    $pubkey = '@PUBKEY@'
    $base = if ($env:MAKEIT_RELEASES_URL) { $env:MAKEIT_RELEASES_URL } else { 'https://github.com' }
    if (-not $base.StartsWith('https://')) { throw 'makeit install: MAKEIT_RELEASES_URL must be https' }

    function Fail($msg) { throw "makeit install: $msg" }
    if (($version + $repo + $pubkey).Contains('@')) { Fail 'use the install.ps1 attached to a release' }
    $version = $version.TrimStart('v')

    $arch = switch ($env:PROCESSOR_ARCHITECTURE) { 'AMD64' { 'x64' } 'ARM64' { 'arm64' } default { Fail "no makeit build for $env:PROCESSOR_ARCHITECTURE" } }
    $name = "makeit-$version-windows-$arch.exe"

    $dir = Join-Path ([IO.Path]::GetTempPath()) ([IO.Path]::GetRandomFileName())
    New-Item -ItemType Directory -Path $dir | Out-Null
    try {
        foreach ($f in @($name, 'checksums.txt', 'checksums.txt.minisig')) {
            Invoke-WebRequest -UseBasicParsing -Uri "$base/$repo/releases/download/v$version/$f" -OutFile (Join-Path $dir $f)
        }
        $sums = Join-Path $dir 'checksums.txt'

        # The signature first, with this script's own key: a replaced binary must not vouch for itself.
        if (Get-Command minisign -ErrorAction SilentlyContinue) {
            $out = & minisign -V -P $pubkey -m $sums -x "$sums.minisig" 2>&1 | Out-String
            if ($LASTEXITCODE -ne 0) { Fail 'the signature on checksums.txt does not verify' }
            if (-not $out.Contains("makeit $version checksums.txt")) { Fail 'checksums.txt is signed for another release' }
            Write-Output 'signature verified'
        } else {
            Write-Output 'signature not checked: install minisign to verify (sha256 only)'
        }

        $want = (Get-Content $sums | ForEach-Object { $p = $_ -split '\s+'; if ($p[1] -eq $name) { $p[0] } }) | Select-Object -First 1
        if (-not $want) { Fail "$name is not in checksums.txt" }
        $got = (Get-FileHash -Algorithm SHA256 (Join-Path $dir $name)).Hash.ToLower()
        if ($got -ne $want.ToLower()) { Fail "$name does not match checksums.txt" }

        & (Join-Path $dir $name) self install --yes
        if ($LASTEXITCODE -ne 0) { Fail "self install failed (exit $LASTEXITCODE)" }
    } finally {
        Remove-Item -Recurse -Force $dir -ErrorAction SilentlyContinue
    }

    # Then setup, asking its one question in this console. MAKEIT_YES=1 sets up without asking;
    # MAKEIT_NO_SETUP=1 installs makeit only.
    $installed = Join-Path $env:USERPROFILE '.local\bin\makeit.exe'
    if ($env:MAKEIT_NO_SETUP) { Write-Output 'next: makeit setup'; return }
    if ($env:MAKEIT_YES) { & $installed setup --yes } else { & $installed setup }
}
