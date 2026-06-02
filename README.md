# Thor PSA Validator

AI-powered partner competency validation tool. Validates AWS partner
submissions against the PSA control catalog using Amazon Bedrock (Claude)
and produces structured PASS / FAIL / WAIVED reports.

## Quick Start

**Prerequisites:** Node.js 18+ and the `thor` CLI binary on PATH.

### 1. Install the `thor` CLI

```sh
# Requires Go 1.23+ for building from source
make build
make install-local    # installs to ~/.local/bin
thor doctor           # verify
```

### 2. Connect to your AI assistant

**Kiro:** Install as a Power from the Powers panel. See [`POWER.md`](POWER.md).

**Claude Code / Cursor / any MCP client:** Add to `.mcp.json`:
```json
{
  "mcpServers": {
    "thor": {
      "command": "npx",
      "type": "stdio",
      "args": ["-y", "--registry", "https://registry.npmjs.org", "@asp-sail/thor-mcp"],
      "env": { "AWS_REGION": "us-east-1" }
    }
  }
}
```

### 3. Get AWS credentials

```
aws sso login --profile <profile>     # SSO users
aws configure                         # static keys
```

### 4. Validate

Ask your AI assistant: "validate the partner at /path/to/folder"

## Available Tools

| Tool | Description |
|------|-------------|
| `thor_doctor` | Health check — embedded prompts, AWS credentials, Bedrock access |
| `thor_convert` | Convert Excel checklist (.xlsx) to CSV |
| `thor_map` | Build the control→files evidence map |
| `thor_validate` | Run Bedrock AI validation (parallel, with fallback) |
| `thor_diff` | Compare validation runs |
| `thor_prep` | Create a new partner folder |
| `thor_list_controls` | List available controls |
| `thor_export` | Export report to HTML |

## Supported Evidence File Types

| Extension | How it's sent to Bedrock |
|---|---|
| `.pdf` | Native document block (split if >40 pages) |
| `.xlsx`, `.xls` | Native document block |
| `.docx`, `.doc` | Native document block |
| `.csv`, `.html`, `.txt`, `.md` | Native document block |
| `.pptx` | Text-extracted (always, regardless of size) |
| `.png`, `.jpg`, `.jpeg` | Image block (resized if >8000px) |

Files over 4.5MB fall back to text extraction automatically.

## Default Model

Uses `global.anthropic.claude-sonnet-4-5-20250929-v1:0`. Override with `--model-id` flag or the MCP tool's `model_id` parameter.

## Development (contributors only)

Requires Go 1.23+.

```sh
make build        # build binaries
make test         # unit tests
make lint         # golangci-lint
make cross-compile  # all platforms
```

## Credential Handling

Uses the AWS SDK default credential chain. If credentials expire
mid-run, refresh and retry — no restart needed.

## Authors

For additional questions and support, please reach out to [Ragib Ahsan](https://www.linkedin.com/in/ragibmahsan/) and [Patrick Vassell](https://www.linkedin.com/in/patrickvassell/)
