# Technical Validation Automation Agent — Claude Code Integration

Claude Code integration for the technical validation agent: an MCP
config, a project context file, and a validation skill.

## Setup

**Prerequisites:** Node.js 20+ (for `npx`) and AWS credentials with
Bedrock access.

### Step 1 — Add MCP config

Create `.mcp.json` in your project root:

```json
{
  "mcpServers": {
    "Thor MCP Server": {
      "command": "npx",
      "type": "stdio",
      "args": ["-y", "--registry", "https://registry.npmjs.org", "@asp-sail/thor-mcp@latest"],
      "env": {
        "AWS_REGION": "us-east-1"
      }
    }
  }
}
```

### Step 2 — Set AWS credentials

The MCP server inherits your shell environment at startup. Export
credentials before launching Claude Code:

```sh
# Option 1: Export directly (recommended)
eval "$(aws configure export-credentials --format env)"

# Option 2: SSO login
aws sso login --profile <profile>
export AWS_PROFILE=<profile>

# Option 3: Static keys
aws configure
```

### Step 3 — Verify

Run `claude` and say "run thor_doctor". You should see all checks
pass: embedded prompts, AWS credentials, and Bedrock model access.

### Step 4 — Add project context (optional)

Copy `CLAUDE.md` to give Claude always-on context about the tools:

```sh
cp claude-skill/CLAUDE.md /path/to/your/project/CLAUDE.md
```

### Step 5 — Install the validation skill (optional)

```sh
mkdir -p /path/to/your/project/.claude/skills
cp -R claude-skill/skills/thor-validate /path/to/your/project/.claude/skills/
```

Claude Code discovers skills as `.claude/skills/<name>/SKILL.md`.

## Credential refresh

If credentials expire mid-session:

1. Export fresh credentials:
   ```sh
   eval "$(aws configure export-credentials --format env)"
   ```

2. Reconnect the MCP server:
   ```
   /mcp
   ```
   Select the Thor server and reconnect.

## What's in this directory

```
claude-skill/
├── README.md              # this file
├── mcp.json               # Claude Code MCP config — copy as .mcp.json
├── CLAUDE.md              # project context — copy as CLAUDE.md
└── skills/
    └── thor-validate/
        └── SKILL.md       # validation workflow skill
```

## Troubleshooting

| Error | Fix |
|-------|-----|
| `spawn npx ENOENT` | `npx` isn't on PATH. Use the absolute path or ensure Node.js 20+ is installed. |
| `ExpiredTokenException` / "credentials expired" | Export fresh credentials and reconnect the MCP server (`/mcp`). |
| `AccessDeniedException` on Converse | Your AWS credentials don't have Bedrock access in the configured region. Check `aws bedrock list-inference-profiles --region <region>`. |
| Tools not appearing | Make sure `.mcp.json` is in the directory you launch `claude` from, then reconnect MCP servers. |
