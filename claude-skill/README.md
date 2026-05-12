# Thor PSA Validator — Claude Code Setup

Use this if you're running Thor with **Claude Code** (CLI, desktop app, or IDE extension).
For Kiro, use the root-level `POWER.md` and `mcp.json` instead.

## Quick Start

### 1. Install dependencies (one-time)

```bash
cd <repo-root>/server
./start.sh  # creates venv + installs deps automatically
```

Or manually:
```bash
cd <repo-root>/server
python3 -m venv .venv
.venv/bin/pip install -r requirements.txt
```

### 2. Register the MCP server

Copy `mcp.json` from this directory into your project's `.mcp.json`:

```bash
cp claude-skill/mcp.json /path/to/your/project/.mcp.json
```

**Edit the paths** in `.mcp.json`:
- Replace `/EDIT_THIS/path/to/thor-power` with the actual path to this repo
- Replace `/EDIT_THIS/Users/yourname` with your home directory
- Replace `YOUR_BEDROCK_PROFILE` with your AWS profile that has Bedrock access

### 3. AWS Credentials (important — different from Kiro)

**Claude Code injects its own `AWS_PROFILE` into MCP server processes.** This overrides Thor's smart credential auto-discovery. You MUST explicitly set `AWS_PROFILE` in the `.mcp.json` env block to the profile that has Bedrock permissions.

```json
"env": {
  "HOME": "/Users/yourname",
  "AWS_PROFILE": "your-bedrock-profile-name",
  "AWS_DEFAULT_REGION": "us-east-1",
  "PATH": "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
}
```

To find your Bedrock profile:
```bash
# If you use isengard:
isengardcli assume <your-account>
# Then check which profile was created:
grep "credential_process" ~/.aws/config
# Use that profile name (e.g., "myaccount-Admin")
```

**Why this is different from Kiro:** In Kiro, the MCP server's environment is clean — Thor auto-discovers `credential_process` profiles from `~/.aws/config`. In Claude Code, the host injects its own AWS credentials into the MCP process, which may not have Bedrock access. Setting `AWS_PROFILE` explicitly overrides this.

### 4. (Optional) Add CLAUDE.md context

Copy `CLAUDE.md` to your working project root so Claude always has Thor context:

```bash
cp claude-skill/CLAUDE.md /path/to/your/project/CLAUDE.md
```

### 5. (Optional) Install the skill

```bash
mkdir -p /path/to/your/project/.claude/skills
cp claude-skill/skills/thor-validate.md /path/to/your/project/.claude/skills/
```

### 6. Verify

Start Claude Code and say: "run thor_doctor"

All 7 checks should pass. If you get `AccessDeniedException` on validation, your `AWS_PROFILE` doesn't have Bedrock permissions — double-check step 3.

## What's in this directory

```
claude-skill/
├── README.md              # This file
├── mcp.json               # MCP server config (edit paths before use)
├── CLAUDE.md              # Optional project context file
└── skills/
    └── thor-validate.md   # Claude Code skill (validation workflow)
```

## Kiro vs Claude Code — Key Differences

| | Kiro Power | Claude Code |
|---|---|---|
| Install | Powers panel → one click | Manual file copy + path editing |
| MCP config | `${powerDir}` variables (auto-resolved) | Absolute paths (you fill in) |
| AWS credentials | Auto-discovered from `~/.aws/config` | Must set `AWS_PROFILE` explicitly (Claude injects its own) |
| Workflow guides | `steering/` files (auto-triggered by keywords) | `.claude/skills/` (user-invoked) or `CLAUDE.md` |
| Context loading | Dynamic (activates on mention, unloads when done) | Always loaded |
| First-run setup | Kiro agent helps configure `HOME` etc. | Manual |

## Troubleshooting

| Error | Fix |
|-------|-----|
| `AccessDeniedException` on Converse | Wrong `AWS_PROFILE` — set it to your Bedrock-enabled profile in `.mcp.json` |
| `ExpiredTokenException` | Run `isengardcli assume` / `aws sso login` and retry (no restart needed) |
| `spawn ... ENOENT` | Path to Python venv is wrong in `.mcp.json` — use absolute path |
| Tools not found | Make sure `.mcp.json` is in the directory where you run `claude` |
