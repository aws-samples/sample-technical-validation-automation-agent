# Thor PSA Validator

AI-powered partner competency validation tool for the PSA team. Validates AWS partner submissions against control requirements using Bedrock Claude, compares results with AWS/Loki, and generates structured reports.

## Choose Your IDE/Agent

This repo supports two AI coding environments. Each has its own configuration — the MCP server and core logic are shared.

```
thor-power/
├── server/                 # Shared MCP server + validation engine
│   ├── thor_mcp_server.py  # MCP server (used by both Kiro and Claude Code)
│   ├── thor/               # Core logic: validator, Excel parser, control mapping
│   ├── start.sh            # Auto-bootstrap script (creates venv on first run)
│   └── requirements.txt    # Python dependencies
│
├── POWER.md                # ← Kiro: Power manifest + docs
├── mcp.json                # ← Kiro: MCP config (uses ${powerDir} variables)
├── steering/               # ← Kiro: Auto-triggered workflows
│   ├── setup.md
│   └── validation.md
│
└── claude-skill/           # ← Claude Code: Skill + config files
    ├── README.md           # Setup instructions for Claude Code users
    ├── mcp.json            # MCP config (edit paths before use)
    ├── CLAUDE.md           # Project context file (copy to your project)
    └── skills/
        └── thor-validate.md  # Validation workflow skill
```

### Using with Kiro

Install via the Kiro Powers panel. See [`POWER.md`](POWER.md) for full setup instructions.

### Using with Claude Code

See [`claude-skill/README.md`](claude-skill/README.md) for setup. In short:
1. Run `server/start.sh` once to install deps
2. Copy `claude-skill/mcp.json` → your project's `.mcp.json` (edit paths)
3. Copy `claude-skill/skills/thor-validate.md` → your project's `.claude/skills/`
4. Get AWS credentials and run `thor_doctor` to verify

## What Thor Can Do

- **Convert** Excel checklists to structured CSV for validation
- **Validate** partner submissions control-by-control using Bedrock AI (parallel, ~5 min for 49 controls)
- **Run** end-to-end workflows: download from S3 → convert → validate → generate report
- **Compare** validation runs over time (diff)
- **Export** reports to HTML/PDF for stakeholders
- **Prep** set up new partner folders with the right structure

## Available MCP Tools

| Tool | Description |
|------|-------------|
| `thor_doctor` | Health check — verifies Python, AWS credentials, dependencies |
| `thor_convert` | Convert Excel checklist (.xlsx) to CSV |
| `thor_validate` | Run Bedrock AI validation (parallel, all controls, full reasoning) |
| `thor_run` | Full end-to-end: download from S3 → convert → validate → report |
| `thor_diff` | Compare validation runs to see what changed |
| `thor_prep` | Create a new partner folder with the right structure |
| `thor_list_controls` | List available controls, optionally filtered by category |
| `thor_export` | Export validation report to HTML/PDF |

## Credential Handling

Thor uses lazy credential loading — fresh boto3 session on every API call:
- If credentials expire mid-run, just refresh and retry (no server restart)
- Auto-discovers `credential_process` profiles in `~/.aws/config`
- Never hardcode keys in the MCP env block
