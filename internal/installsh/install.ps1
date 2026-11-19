# Installs AgentBox on Windows, in PowerShell:
#
#   irm https://downloads.agentbox.linting.dev/install.ps1 | iex
#
# It checks this computer first and downloads nothing until every check
# passes: 64-bit Windows 10 version 2004 or later (or Windows 11), 8 GB of
# memory, and 30 GB free where AgentBox keeps its WSL distro. Then it
# downloads the latest release's installer, checks it against the release's
# SHA256SUMS, installs it for your user alone (no administrator), and opens
# AgentBox, whose Setup does the rest: it installs WSL when it's missing,
# with one administrator prompt, and makes the distro your agents run in.
#
# A download made by PowerShell isn't marked as coming from the internet, so
# SmartScreen doesn't stop the installer, which isn't signed yet.
#
# Options are parameters when the script is run from a file, and environment
# variables when it is piped into iex, which takes none:
#
#   -Check     $env:AGENTBOX_INSTALL_CHECK = 1      only the checks
#   -Native    $env:AGENTBOX_INSTALL_NATIVE = 1     the native app (experimental) instead
#   -Channel   $env:AGENTBOX_CHANNEL = 'nightly'    the newest nightly, not the newest release
#   -NoOpen    $env:AGENTBOX_INSTALL_NO_OPEN = 1    don't open AgentBox afterwards
#
#   & ([scriptblock]::Create((irm https://downloads.agentbox.linting.dev/install.ps1))) -Check
#
# $env:DOWNLOADS_URL points it at another copy of the downloads. It runs in
# Windows PowerShell 5.1, which every Windows has, and in PowerShell 7.
#
# All of it is one function, called on the last line: a download cut off
# half-way runs nothing.
param(
    [switch]$Check,
    [switch]$Native,
    [switch]$NoOpen,
    [string]$Channel
)

function Install-AgentBox {
    param([bool]$Check, [bool]$Native, [bool]$NoOpen, [string]$Channel)

    $ErrorActionPreference = 'Stop'
    # Invoke-WebRequest draws a progress bar that makes a download many times
    # slower in Windows PowerShell.
    $ProgressPreference = 'SilentlyContinue'
    try { [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12 } catch { }

    $downloads = 'https://downloads.agentbox.linting.dev'
    if ($env:DOWNLOADS_URL) { $downloads = $env:DOWNLOADS_URL.TrimEnd('/') }
    if (-not $Channel) { $Channel = $env:AGENTBOX_CHANNEL }
    if (-not $Channel) { $Channel = 'stable' }
    if ($env:AGENTBOX_INSTALL_CHECK) { $Check = $true }
    if ($env:AGENTBOX_INSTALL_NATIVE) { $Native = $true }
    if ($env:AGENTBOX_INSTALL_NO_OPEN) { $NoOpen = $true }

    # --- This computer ------------------------------------------------------

    # What the checks go by. AGENTBOX_INSTALL_FAKE is a JSON file with the
    # same fields, for the tests, which run where there is no Windows to ask.
    if ($env:AGENTBOX_INSTALL_FAKE) {
        $machine = Get-Content -Raw $env:AGENTBOX_INSTALL_FAKE | ConvertFrom-Json
    } else {
        $onWindows = [Environment]::OSVersion.Platform -eq 'Win32NT'
        if (-not $onWindows) {
            Write-Host "This script installs AgentBox on Windows. On Linux and on a Mac:"
            Write-Host "  curl -fsSL $downloads/install.sh | sh"
            return 1
        }
        $system = Get-CimInstance Win32_ComputerSystem
        $cpu = @(Get-CimInstance Win32_Processor)[0]
        $data = $env:LOCALAPPDATA
        $drive = Get-CimInstance Win32_LogicalDisk -Filter ("DeviceID='{0}'" -f $data.Substring(0, 2))
        $wsl = $false
        try {
            # Only the Store's WSL answers --version; the stub Windows ships
            # before WSL is installed fails it.
            & "$env:SystemRoot\System32\wsl.exe" --version *> $null
            $wsl = ($LASTEXITCODE -eq 0)
        } catch { }
        $machine = [pscustomobject]@{
            arch           = $env:PROCESSOR_ARCHITECTURE
            build          = [Environment]::OSVersion.Version.Build
            memory         = [double]$system.TotalPhysicalMemory
            free           = [double]$drive.FreeSpace
            data           = $data
            # Windows reports the firmware's virtualization as off once a
            # hypervisor (Hyper-V, WSL 2) is using it.
            virtualization = [bool]($cpu.VirtualizationFirmwareEnabled -or $system.HypervisorPresent)
            wsl            = $wsl
            startMenu      = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs'
            calls          = ''
        }
    }

    # --- The checks ---------------------------------------------------------

    $problems = @()
    $notes = @()
    if ($machine.arch -eq 'ARM64') {
        $problems += 'This is an ARM (arm64) computer. AgentBox runs only on x64 Windows for now.'
    } elseif ($machine.arch -ne 'AMD64') {
        $problems += "This computer's processor is $($machine.arch). AgentBox runs only on 64-bit (x64) Windows."
    }
    # WSL 2 with `wsl --install`, which AgentBox's Setup runs, came with
    # Windows 10 version 2004 (build 19041).
    if ($machine.build -lt 19041) {
        $problems += "This is Windows build $($machine.build), and AgentBox needs Windows 10 version 2004 (build 19041) or later, or Windows 11: it runs in WSL 2. Update Windows first."
    }
    $gb = 1024 * 1024 * 1024
    # 8 GB, a little under, since 8 GB computers report less.
    if ($machine.memory -lt 7.2 * $gb) {
        $problems += "This computer has $([math]::Round($machine.memory / $gb)) GB of memory, and AgentBox needs at least 8 GB: its agents take their memory from it."
    }
    if ($machine.free -lt 30 * 1000 * 1000 * 1000) {
        $problems += "The drive with $($machine.data) has $([math]::Floor($machine.free / 1000 / 1000 / 1000)) GB free, and AgentBox needs at least 30 GB: its WSL distro, and every agent's machine, go there."
    }
    if (-not $machine.virtualization) {
        $notes += "Windows says virtualization is off in this computer's firmware. If AgentBox's Setup can't start WSL, turn on Intel VT-x or AMD SVM in the BIOS/UEFI settings (Task Manager > Performance > CPU shows `"Virtualization: Enabled`" when it is)."
    }
    if (-not $machine.wsl) {
        $notes += "WSL isn't installed yet. That's fine: AgentBox's Setup installs it, with one administrator prompt, and Windows may need a restart to finish."
    }

    if ($notes.Count -gt 0) {
        Write-Host 'Note:'
        foreach ($n in $notes) { Write-Host "  - $n" }
    }
    if ($problems.Count -gt 0) {
        Write-Host "AgentBox can't be installed on this computer yet:"
        foreach ($p in $problems) { Write-Host "  - $p" }
        return 1
    }
    $what = 'AgentBox'
    if ($Native) { $what = "AgentBox's native app (experimental)" }
    Write-Host "This computer can run AgentBox: it would install $what, for your user alone, with no administrator."
    if ($Check) { return 0 }

    # --- Download and check -------------------------------------------------

    $tmp = Join-Path ([IO.Path]::GetTempPath()) ("agentbox-install-" + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $tmp | Out-Null
    try {
        # releases.json is newest first, in GitHub's shape. A nightly is a
        # prerelease whose tag says nightly.
        $releases = Invoke-RestMethod -Uri "$downloads/releases.json" -UseBasicParsing
        $tag = $null
        foreach ($r in @($releases)) {
            $nightly = [bool]$r.prerelease -and ($r.tag_name -match '-nightly\.')
            if (($Channel -eq 'nightly' -and $nightly) -or ($Channel -ne 'nightly' -and -not $r.prerelease)) {
                $tag = $r.tag_name
                break
            }
        }
        if (-not $tag) {
            Write-Host "Couldn't find a $Channel release in $downloads/releases.json."
            return 1
        }
        $version = $tag.TrimStart('v')
        $asset = "AgentBox-$version-x64-setup.exe"
        if ($Native) { $asset = "AgentBox-Native-experimental-$version-windows-x64-setup.exe" }

        Write-Host "==> Downloading AgentBox $version ($asset)"
        $sums = (Invoke-WebRequest -Uri "$downloads/releases/$tag/SHA256SUMS" -UseBasicParsing).Content
        if ($sums -is [byte[]]) { $sums = [Text.Encoding]::UTF8.GetString($sums) }
        $want = $null
        foreach ($line in ($sums -split "`n")) {
            $fields = $line.Trim() -split '\s+', 2
            if ($fields.Count -eq 2 -and $fields[1].TrimStart('*') -eq $asset) { $want = $fields[0].ToLower() }
        }
        if (-not $want) {
            if ($Native) {
                Write-Host "AgentBox $version has no native app for Windows ($asset isn't in its SHA256SUMS): try the nightly, with `$env:AGENTBOX_CHANNEL = 'nightly'. Nothing was installed."
            } else {
                Write-Host "The release's SHA256SUMS has no line for $asset`: nothing was installed."
            }
            return 1
        }
        $setup = Join-Path $tmp $asset
        Invoke-WebRequest -Uri "$downloads/releases/$tag/$asset" -OutFile $setup -UseBasicParsing
        $got = (Get-FileHash -Algorithm SHA256 -Path $setup).Hash.ToLower()
        if ($got -ne $want) {
            Write-Host "The download doesn't match the release's SHA256SUMS (want $want, got $got): nothing was installed."
            return 1
        }
        Write-Host "==> Checked against the release's SHA256SUMS"

        # --- Install --------------------------------------------------------

        # /S: silently, which neither installer follows by opening the app.
        Write-Host '==> Installing it, for your user alone'
        if ($machine.calls) {
            Add-Content -Path $machine.calls -Value "$asset /S"
        } else {
            $run = Start-Process -FilePath $setup -ArgumentList '/S' -Wait -PassThru
            if ($run.ExitCode -ne 0) {
                Write-Host "The installer failed (exit status $($run.ExitCode)): nothing was installed. Run it yourself to see why: $downloads/releases/$tag/$asset"
                return 1
            }
        }

        $name = 'AgentBox'
        if ($Native) { $name = 'AgentBox Native (experimental)' }
        $shortcut = Join-Path $machine.startMenu "$name.lnk"
        if ($NoOpen -or -not (Test-Path $shortcut)) {
            Write-Host "AgentBox $version is installed. Open $name from the Start menu: its Setup makes the WSL distro your agents run in."
            return 0
        }
        Write-Host "AgentBox $version is installed, and opening: its Setup makes the WSL distro your agents run in."
        if ($machine.calls) {
            Add-Content -Path $machine.calls -Value "open $name.lnk"
        } else {
            Start-Process -FilePath $shortcut
        }
        return 0
    } finally {
        Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
    }
}

$agentboxInstallExit = Install-AgentBox -Check $Check.IsPresent -Native $Native.IsPresent -NoOpen $NoOpen.IsPresent -Channel $Channel
# Piped into iex this is the user's own session: say how it ended, and leave
# the session open. Run as a file, it is the exit status.
if ($MyInvocation.MyCommand.Path) { exit $agentboxInstallExit }
$global:LASTEXITCODE = $agentboxInstallExit
