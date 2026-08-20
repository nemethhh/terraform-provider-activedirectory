<#
.SYNOPSIS
    Install the Go toolchain and Terraform on the member server.
.DESCRIPTION
    The acceptance suite's provider block declares `local {}`, so the tests must
    run on a domain-joined Windows host rather than on the workstation. That
    means this host needs the Go toolchain (to compile and run the test binary)
    and the Terraform CLI (which terraform-plugin-testing drives directly).

    Both artefacts are pinned and verified against the SHA256 their vendor
    publishes, because an unverified download onto a domain-joined host is a
    supply-chain hole, not a convenience.

    Both track the latest stable release. Terraform is therefore newer than the
    1.11.4 that CI pins; the suite's own gate is `SkipBelow(1.11)`, so every
    version-sensitive test still runs, but it is a genuine difference between
    this host and CI and is worth remembering when triaging a failure.

    Idempotent, and the check is a real one. An interrupted extraction leaves a
    go.exe that reports the right version on top of a truncated standard library,
    which then fails every build with "package unsafe is not in std". Verifying
    the binary alone would call that installed and skip the repair, so the probe
    compiles a throwaway program instead, and extraction lands in a temporary
    directory that is renamed into place only once it has completed.

    Git is deliberately not installed. The source arrives by scp, which keeps
    every artefact on this host traceable to a published checksum.
.EXAMPLE
    ./psrun.sh s-client 10-install-dev-tools.ps1 1200
#>
param(
    [string]$GoVersion        = '1.27.0',
    [string]$GoSha256         = 'f0c0a0d33ba94f4d2c5dbc887334ce678b21813504ddb3aafcb06e60a5a667c4',
    [string]$TerraformVersion = '1.15.9',
    [string]$TerraformSha256  = 'b0fcd57e2abd19fc6d8e64b86a22f5f3fb734b0407385553cdcffc64677f18b6'
)
$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12

# Windows PowerShell 5.1 renders a progress bar for every chunk Invoke-WebRequest
# receives, which throttles a large download to tens of KB/s. Silencing it is
# not cosmetic: without this the Go archive alone takes the better part of an
# hour, and an SSH session that dies meanwhile leaves an orphan still writing to
# the file, which then fails the checksum read as a sharing violation.
$ProgressPreference = 'SilentlyContinue'

$binDir = 'C:\tools\bin'
New-Item -ItemType Directory -Force -Path $binDir | Out-Null

function Get-Verified {
    param([string]$Url, [string]$Sha256, [string]$OutFile)
    if (-not (Test-Path $OutFile)) {
        Write-Output "  downloading $Url"
        # WebClient streams straight to disk. Downloading to a temporary name and
        # renaming means an interrupted transfer can never be mistaken for a
        # complete one on the next run.
        $partial = "$OutFile.partial"
        Remove-Item $partial -Force -ErrorAction SilentlyContinue
        $sw = [Diagnostics.Stopwatch]::StartNew()
        (New-Object Net.WebClient).DownloadFile($Url, $partial)
        $sw.Stop()
        Move-Item $partial $OutFile -Force
        $mb = [math]::Round((Get-Item $OutFile).Length / 1MB, 1)
        Write-Output ("  {0} MB in {1:n0}s" -f $mb, $sw.Elapsed.TotalSeconds)
    }
    $actual = (Get-FileHash -Path $OutFile -Algorithm SHA256).Hash.ToLower()
    if ($actual -ne $Sha256.ToLower()) {
        Remove-Item $OutFile -Force
        throw "checksum mismatch for $Url`n  expected $Sha256`n  got      $actual"
    }
    Write-Output '  checksum ok'
}

# --- Go ---------------------------------------------------------------------
# A version string proves the binary exists; it does not prove the standard
# library beside it is intact. Compiling something does.
function Test-GoHealthy {
    param([string]$GoExe, [string]$Version)
    if (-not (Test-Path $GoExe)) { return $false }
    if (((& $GoExe version) 2>&1) -notmatch [regex]::Escape("go$Version")) { return $false }
    $probe = Join-Path $env:TEMP 'gohealth'
    Remove-Item $probe -Recurse -Force -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force -Path $probe | Out-Null
    Set-Content "$probe\go.mod"  "module gohealth`ngo 1.21`n"
    Set-Content "$probe\main.go" 'package main; import ("fmt";"os";"time"); func main(){ fmt.Fprint(os.Stderr, time.Now()) }'
    Push-Location $probe
    # Routed through cmd deliberately. Under $ErrorActionPreference = 'Stop',
    # `native 2>&1 | ...` promotes every stderr line to a terminating error, so a
    # broken toolchain would abort this script instead of reporting unhealthy —
    # which is the one thing this function exists to avoid.
    try   { cmd /c "`"$GoExe`" build -o `"$probe\out.exe`" . >NUL 2>&1"; return ($LASTEXITCODE -eq 0) }
    finally { Pop-Location; Remove-Item $probe -Recurse -Force -ErrorAction SilentlyContinue }
}

$goExe = 'C:\go\bin\go.exe'
if (Test-GoHealthy $goExe $GoVersion) {
    Write-Output "GO_ALREADY $(& $goExe version)"
} else {
    if (Test-Path $goExe) { Write-Output '  existing Go failed its health check; reinstalling' }
    $zip = "C:\Windows\Temp\go$GoVersion.windows-amd64.zip"
    Get-Verified "https://go.dev/dl/go$GoVersion.windows-amd64.zip" $GoSha256 $zip
    # Extract beside the target and rename, so a killed run can never leave a
    # half-populated C:\go that the next run mistakes for a good install.
    $staging = 'C:\go.staging'
    Remove-Item $staging -Recurse -Force -ErrorAction SilentlyContinue
    Expand-Archive -Path $zip -DestinationPath $staging -Force
    Remove-Item 'C:\go' -Recurse -Force -ErrorAction SilentlyContinue
    Move-Item "$staging\go" 'C:\go'
    Remove-Item $staging -Recurse -Force -ErrorAction SilentlyContinue
    Remove-Item $zip -Force
    if (-not (Test-GoHealthy $goExe $GoVersion)) { throw 'Go still fails its health check after reinstall' }
    Write-Output "GO_INSTALLED $(& $goExe version)"
}

# --- Terraform --------------------------------------------------------------
$tfExe = Join-Path $binDir 'terraform.exe'
$haveTf = (Test-Path $tfExe) -and ((& $tfExe version) -match [regex]::Escape("v$TerraformVersion"))
if ($haveTf) {
    Write-Output "TERRAFORM_ALREADY $((& $tfExe version)[0])"
} else {
    $zip = "C:\Windows\Temp\terraform_$TerraformVersion.zip"
    Get-Verified "https://releases.hashicorp.com/terraform/$TerraformVersion/terraform_${TerraformVersion}_windows_amd64.zip" $TerraformSha256 $zip
    Expand-Archive -Path $zip -DestinationPath $binDir -Force
    Remove-Item $zip -Force
    Write-Output "TERRAFORM_INSTALLED $((& $tfExe version)[0])"
}

# --- PATH -------------------------------------------------------------------
# Machine scope, so a later SSH session sees it without extra setup.
$machinePath = [Environment]::GetEnvironmentVariable('Path', 'Machine')
$added = @()
foreach ($dir in @('C:\go\bin', $binDir)) {
    if ($machinePath -split ';' -notcontains $dir) { $machinePath = "$machinePath;$dir"; $added += $dir }
}
if ($added) {
    [Environment]::SetEnvironmentVariable('Path', $machinePath, 'Machine')
    Write-Output ('PATH_ADDED ' + ($added -join ' '))
} else {
    Write-Output 'PATH_ALREADY'
}
$env:Path = [Environment]::GetEnvironmentVariable('Path', 'Machine')

# --- Verify -----------------------------------------------------------------
# Resolve through PATH, not by absolute path: that is what the next SSH session
# will do, and a PATH that did not take is the failure worth catching here.
foreach ($tool in 'go', 'terraform') {
    $cmd = Get-Command $tool -ErrorAction SilentlyContinue
    if (-not $cmd) { throw "$tool is not on PATH after install" }
    Write-Output "VERIFIED $tool $($cmd.Source)"
}
