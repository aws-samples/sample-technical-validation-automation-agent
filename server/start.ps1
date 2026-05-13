# Thor MCP Server — self-bootstrapping launcher (Windows)
# Creates venv and installs dependencies on first run automatically.

$ErrorActionPreference = "Stop"
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$VenvDir = Join-Path $ScriptDir ".venv"
$Requirements = Join-Path $ScriptDir "requirements.txt"
$VenvPython = Join-Path $VenvDir "Scripts\python.exe"

# Auto-create venv if it doesn't exist
if (-not (Test-Path $VenvPython)) {
    Write-Host "First run - setting up Python environment..." -ForegroundColor Yellow
    python -m venv $VenvDir
    if ($LASTEXITCODE -ne 0) {
        # Try py launcher as fallback
        py -3 -m venv $VenvDir
    }
    if (-not (Test-Path $VenvPython)) {
        Write-Error "ERROR: Could not create Python venv. Install Python 3.8+ and retry."
        exit 1
    }
    & $VenvPython -m pip install --quiet -r $Requirements
    Write-Host "Setup complete." -ForegroundColor Green
}

# Launch the MCP server
& $VenvPython -m server.thor_mcp_server
