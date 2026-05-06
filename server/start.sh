#!/bin/bash
# Thor MCP Server — self-bootstrapping launcher
# Creates venv and installs dependencies on first run automatically.

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
VENV_DIR="$SCRIPT_DIR/.venv"
REQUIREMENTS="$SCRIPT_DIR/requirements.txt"

# Ensure HOME is set (MCP servers may not inherit it)
if [ -z "$HOME" ]; then
    export HOME=$(eval echo ~)
fi

# Auto-create venv if it doesn't exist
if [ ! -f "$VENV_DIR/bin/python3" ]; then
    echo "First run — setting up Python environment..." >&2
    python3 -m venv "$VENV_DIR" 2>&1 >&2
    if [ $? -ne 0 ]; then
        # Try common Python locations if python3 isn't in PATH
        for PY in /opt/homebrew/bin/python3 /usr/local/bin/python3 /usr/bin/python3; do
            if [ -x "$PY" ]; then
                "$PY" -m venv "$VENV_DIR" 2>&1 >&2
                break
            fi
        done
    fi
    if [ ! -f "$VENV_DIR/bin/python3" ]; then
        echo "ERROR: Could not create Python venv. Install Python 3.8+ and retry." >&2
        exit 1
    fi
    "$VENV_DIR/bin/pip" install --quiet -r "$REQUIREMENTS" 2>&1 >&2
    echo "Setup complete." >&2
fi

# Launch the MCP server
exec "$VENV_DIR/bin/python3" -m server.thor_mcp_server
