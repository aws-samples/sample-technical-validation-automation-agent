---
name: thor-validate
description: Validate AWS partner competency applications using Thor. Use when the user wants to validate a partner submission, run thor, check Thor health, convert checklists, compare results, or export reports.
---

# Thor PSA Validator

## Available MCP Tools

| Tool | Use when |
|------|----------|
| `thor_doctor` | Health check, first-time setup, troubleshooting |
| `thor_convert` | User has an Excel checklist to convert to CSV |
| `thor_validate` | Run AI validation on partner submissions |
| `thor_run` | Full end-to-end workflow from SIM ticket |
| `thor_diff` | Compare validation runs or check what changed |
| `thor_prep` | Set up a new partner folder |
| `thor_list_controls` | List controls, optionally filtered by category |
| `thor_export` | Export report to HTML/PDF for sharing |

## Setup (if thor_doctor fails)

1. Call `thor_doctor` to diagnose
2. If credentials missing: user needs to run `isengardcli assume <account>` or `aws sso login --profile <profile>` in their terminal
3. If server not connected: verify `.mcp.json` paths point to the correct thor-power clone location
4. After credential refresh, retry — no server restart needed

## Validation Workflow

### Step 1: Identify partner folder
- Ask for the path, or offer `thor_prep` to create one
- Folder should contain: Excel checklist (.xlsx) at root, supporting docs in `supporting_docs/`

### Step 2: Determine parameters
- **Application type**: SERVICE or SOFTWARE (Thor can auto-detect from Excel if unsure)
- **Designation category**: Generative AI Applications, Foundation Models and App Development, Infrastructure and Data, Agentic AI Tools, Agentic AI Applications, Agentic AI Consulting Services, or Generative AI Consulting Services

### Step 3: Convert Excel to CSV
- Call `thor_convert` with `partner_folder` and `app_type`
- Skip if `partner_responses.csv` already exists and user confirms

### Step 4: Run validation
- Call `thor_validate` with: `partner_folder`, `designation_category`, `app_type`, `skip_conversion: true`
- Runs all applicable controls in parallel (~5 min for ~49 controls)
- User can monitor: `tail -f <partner_folder>/validation_progress.log`

### Step 5: Present results
- Show pass/fail/waived counts
- Show reasoning for ALL controls (passed and failed)
- Offer next steps: review specific controls, re-run subset, compare with prior run, export report

## Tips

- **Partial re-run**: pass specific control IDs via `controls` parameter (space-separated)
- **Consensus mode**: set `consensus: 3` for borderline cases (runs 3x, majority vote)
- **Credential expiry**: just re-run `isengardcli assume` and retry — no restart needed
- **Large PDFs**: install `qpdf` (`brew install qpdf`) for auto-splitting PDFs over 80 pages
- **Compare runs**: use `thor_diff` with `mode: latest` (default), `mode: all` (timeline), or `mode: custom` with specific timestamps
