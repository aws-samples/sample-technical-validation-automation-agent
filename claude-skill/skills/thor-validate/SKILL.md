---
name: thor-validate
description: Validate AWS partner competency applications. Use when the user wants to validate a partner submission, check health, convert checklists, build an evidence map, compare results, or export reports.
---

# Technical Validation Automation Agent

## Available MCP Tools

| Tool | Use when |
|------|----------|
| `thor_doctor` | Health check, first-time setup, troubleshooting |
| `thor_convert` | User has an Excel checklist to convert to CSV |
| `thor_map` | Build the control→files evidence map (auto-built by `thor_validate`) |
| `thor_validate` | Run AI validation on partner submissions |
| `thor_diff` | Compare validation runs or check what changed |
| `thor_prep` | Set up a new partner folder |
| `thor_list_controls` | List controls, optionally filtered by category |
| `thor_export` | Export the markdown report to HTML for sharing |

## Setup (if `thor_doctor` fails)

1. Call `thor_doctor`. It returns a structured report covering embedded
   prompts, AWS credentials, and Bedrock access.
2. If credentials are missing or expired, ask the user to refresh:
   ```sh
   eval "$(aws configure export-credentials --format env)"
   ```
   Then reconnect the MCP server (`/mcp`).
3. If the server isn't connected, verify the project's `.mcp.json`
   has the correct config:
   ```json
   {
     "mcpServers": {
       "Thor MCP Server": {
         "command": "npx",
         "type": "stdio",
         "args": ["-y", "--registry", "https://registry.npmjs.org", "@asp-sail/thor-mcp@latest"],
         "env": { "AWS_REGION": "us-east-1" }
       }
     }
   }
   ```

## Validation Workflow

### Step 1: Identify partner folder
- Ask for the path, or offer `thor_prep` to create one.
- Folder should contain: Excel checklist (`.xlsx`) at root, supporting
  docs in `supporting_docs/`.

### Step 2: Determine parameters
- **Application type**: CRITICAL — Thor auto-detects this from the
  Excel's Introduction sheet content ("service offering" → SERVICE,
  "partner offering" or "software" → SOFTWARE). Do NOT pass `app_type`
  unless overriding the auto-detection. Default is SOFTWARE if
  detection fails. If `partner_responses.csv` already exists, skip —
  the type is baked into the control ID suffixes.
- **Designation category**: Do NOT pass this unless the user explicitly
  asks to filter controls. Omitting it validates ALL controls in the
  partner's CSV (the common case).

### Step 3: Convert Excel to CSV
- Call `thor_convert` with `partner_folder` and `app_type`.
- Skip if `partner_responses.csv` already exists and the user confirms.

### Step 4: Run validation
- Call `thor_validate` with `partner_folder` and `skip_conversion: true`.
- Do NOT pass `category` unless the user explicitly asks to filter.
  Omitting it validates ALL controls in the CSV.
- Do NOT pass `app_type` unless converting fresh from Excel. Default is
  SOFTWARE.
- The first run on a partner folder also builds an `evidence_map.json`
  (one Bedrock call per supporting file with the full CONTEXT.csv
  catalog) so each control sees only relevant evidence. Subsequent runs
  reuse the map until files under `supporting_docs/` change. Tell the
  user this is normal and only happens once per folder.
- Runs all applicable controls in parallel. The MCP server writes a
  per-control structured log line to stderr (and to
  `<partner_folder>/validation_progress.log`) — the user can `tail -f`
  the file to watch progress.
- Pass `no_map: true` only if the user explicitly wants to skip the
  evidence map (e.g., the supporting_docs are tiny and they want to
  avoid the extra Bedrock calls). Default behaviour is to use the map.

### Step 5: Present results
- Show pass / fail / waived counts.
- Show reasoning for ALL controls (passed and failed).
- Offer next steps: review specific controls, re-run a subset, compare
  with a prior run, export the HTML report.

## Tips

- **Evidence map**: `thor_map` builds `evidence_map.json` for a partner
  folder. Useful as a standalone step when you want to inspect the
  control→files mapping before validation, or to surface
  `unmapped` files (no control assigned — review for relevance).
- **Partial re-run**: pass specific control IDs via `controls`
  (space-separated).
- **Consensus mode**: set `consensus: 3` for borderline cases (runs
  3x, majority vote; if every control passes runs 1 and 2, run 3 is
  skipped).
- **Credential expiry**: Export fresh credentials
  (`eval "$(aws configure export-credentials --format env)"`) and
  reconnect the MCP server (`/mcp`). No restart needed.
- **Compare runs**: `thor_diff` with `mode: latest` (default),
  `mode: all` (timeline table), or `mode: custom` with `run1` /
  `run2` timestamps.
- **HTML export**: `thor_export` writes a self-contained HTML next to
  the markdown report. Pass `report` to pick a specific timestamped
  run.
