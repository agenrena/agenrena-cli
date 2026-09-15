param(
    [string]$InstallDir
)

$ErrorActionPreference = "Stop"
$repo = "agenrena/agenrena-cli"
$asset = "agenrena-windows-amd64.exe"

$nativeArchitecture = if ($env:PROCESSOR_ARCHITEW6432) {
    $env:PROCESSOR_ARCHITEW6432
} else {
    $env:PROCESSOR_ARCHITECTURE
}
if ($nativeArchitecture -ne "AMD64") {
    throw "Agenrena CLI currently supports Windows AMD64 only. Detected: $nativeArchitecture"
}

if ([string]::IsNullOrWhiteSpace($InstallDir)) {
    if (-not [string]::IsNullOrWhiteSpace($env:AGENRENA_INSTALL_DIR)) {
        $InstallDir = $env:AGENRENA_INSTALL_DIR
    } elseif ([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
        $InstallDir = Join-Path $HOME ".local\bin"
    } else {
        $InstallDir = Join-Path $env:LOCALAPPDATA "Programs\Agenrena"
    }
}

$url = "https://github.com/$repo/releases/latest/download/$asset"
$tempDir = Join-Path ([IO.Path]::GetTempPath()) ("agenrena-" + [Guid]::NewGuid().ToString("N"))
$tempExe = Join-Path $tempDir "agenrena.exe"
$destination = Join-Path $InstallDir "agenrena.exe"

try {
    New-Item -ItemType Directory -Path $tempDir | Out-Null
    Write-Host "Downloading $asset from $repo..."
    Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $tempExe
    & $tempExe version | Out-Null

    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    Move-Item -Force -Path $tempExe -Destination $destination
} finally {
    if (Test-Path $tempDir) {
        Remove-Item -Recurse -Force $tempDir
    }
}

$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
$pathEntries = @($userPath -split ";" | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
$alreadyOnPath = $pathEntries | Where-Object { $_.TrimEnd("\") -ieq $InstallDir.TrimEnd("\") }
if (-not $alreadyOnPath) {
    $newUserPath = (($pathEntries + $InstallDir) -join ";")
    [Environment]::SetEnvironmentVariable("Path", $newUserPath, "User")
    Write-Host "Added $InstallDir to your user PATH. Open a new terminal to use it."
}
if (-not (($env:Path -split ";") -contains $InstallDir)) {
    $env:Path = "$InstallDir;$env:Path"
}

Write-Host "Installed agenrena to $destination"
Write-Host "Voice calls are not supported by the Windows build yet."
& $destination version
