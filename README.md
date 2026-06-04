# Technical Validation Automation Agent

Preview [Partner Program Validation](https://aws.amazon.com/partners/programs/specializations/) (for AI competency) using the technical validation power to accelerate program approval.

## Quick Start

**Prerequisites:** Node.js 20+ (for `npx`) and an AWS account with
Amazon Bedrock access.

### 1. Connect to your AI assistant

**Kiro:** Install as a Power from the Powers panel. See [`POWER.md`](POWER.md).
After installing, open `~/.kiro/settings/mcp.json` and add your AWS
credentials to the power's `env` block:

```json
"power-thor-power-thor": {
  "command": "npx",
  "transport": "stdio",
  "args": ["-y", "--registry", "https://registry.npmjs.org", "@asp-sail/thor-mcp"],
  "timeout": 600000,
  "env": {
    "AWS_REGION": "us-east-1",
    "AWS_ACCESS_KEY_ID": "<your-access-key>",
    "AWS_SECRET_ACCESS_KEY": "<your-secret-key>",
    "AWS_SESSION_TOKEN": "<your-session-token>",
    "PATH": "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:${env:HOME}/.local/bin"
  }
}
```

> **Note:** Kiro MCP processes don't inherit your shell environment.
> Credentials must be pasted directly into the `env` block. When they
> expire, update the values and save — Kiro reconnects automatically.
> If `npx` is not found, replace `"command": "npx"` with the absolute
> path (run `which npx`).

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

These clients inherit your shell environment, so `aws sso login` or
`aws configure` is sufficient.

### 2. Get AWS credentials

```sh
# Get credentials from your identity provider, then either:
# - Paste into mcp.json env block (Kiro)
# - Or export to your shell (Claude Code / Cursor)
aws configure export-credentials --format env
```

### 3. Validate

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

Uses `global.anthropic.claude-sonnet-4-5-20250929-v1:0`. Override with the MCP tool's `model_id` parameter.

## Credential Handling

**Kiro:** Paste `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, and
`AWS_SESSION_TOKEN` into the MCP config `env` block. When they expire,
update the values and save — Kiro reconnects automatically.

**Claude Code / Cursor / other MCP clients:** These inherit your shell
environment, so the standard AWS SDK credential chain works (env vars,
`AWS_PROFILE`, SSO, `~/.aws/credentials`, instance metadata). If
credentials expire mid-run, refresh and retry — no restart needed.

## Authors

For additional questions and support, please reach out to [Ragib Ahsan](https://www.linkedin.com/in/ragibmahsan/) and [Patrick Vassell](https://www.linkedin.com/in/patrickvassell/)

## Security

See [CONTRIBUTING](CONTRIBUTING.md#security-issue-notifications) for more information.

## License

This library is licensed under the MIT-0 License. See the [LICENSE](LICENSE) file.
