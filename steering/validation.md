# Validation Workflow

## When to Use

Use this when a user wants to validate a partner's competency
application submission with Thor.

## Step-by-Step Flow

### Step 1: Identify the Partner Folder

Ask the user for the partner folder path. If they don't have one yet,
offer to:

- Create a new folder with `thor_prep` (provide partner name +
  designation category), or
- Point to an existing folder.

### Step 2: Check for Required Files

The partner folder should contain:

- An Excel checklist (`.xlsx`) at the root level
- Supporting documents in a `supporting_docs/` subdirectory (PDFs,
  architecture diagrams, screenshots, etc.)

If `partner_responses.csv` already exists, ask if they want to
re-convert or skip conversion.

### Step 3: Determine Application Type (only if converting from Excel)

Only relevant when running `thor_convert`. If `partner_responses.csv`
already exists, skip this — the type is already baked into the control
IDs (look for `-SOFTWARE` or `-SERVICE` suffixes in the CSV).

If converting fresh: ask *"Is this a SERVICE or SOFTWARE application?"*
Default is `SOFTWARE` if unsure.

### Step 4: Determine Designation Category (optional)

Only ask if the user wants to validate a SUBSET of controls. If they
want all controls validated (the common case), do NOT pass a category.

Available categories (only if user explicitly requests filtering):
- Generative AI Applications
- Foundation Models and App Development
- Infrastructure and Data
- Agentic AI Tools
- Agentic AI Applications
- Agentic AI Consulting Services
- Generative AI Consulting Services

### Step 5: Convert Excel to CSV

Call `thor_convert` with the partner folder and `app_type`. The output
header is the canonical `controlId,partner_response`.

### Step 6: Run Validation

Call `thor_validate` with:

- `partner_folder`
- `skip_conversion: true` (since we just converted)

IMPORTANT DEFAULTS:
- Do NOT pass `category` unless the user explicitly provides one. Omitting it validates ALL controls in the partner's CSV.
- Do NOT pass `app_type` unless the user explicitly says SERVICE. Default is SOFTWARE.
- If `partner_responses.csv` already exists with `-SOFTWARE` suffixed controls, the app_type is SOFTWARE regardless of what the user says about their competency category.

The first run on a partner folder also builds an `evidence_map.json`
(one Bedrock call per supporting file with the full CONTEXT.csv
catalog and prompt contexts) so each control sees only the files
relevant to it. Subsequent runs reuse the map until files under
`supporting_docs/` change. Tell the user that the extra mapping
pre-pass only happens the first time per folder and is what keeps each
per-control Bedrock call under the 100-page-per-request cap.

If the user wants to inspect or regenerate the mapping outside a
validation run, call `thor_map` directly. To skip the pre-pass and
send every supporting file to every control, pass `no_map: true`.

Validation runs all controls in parallel (default concurrency 10) and
typically takes 4–5 minutes for ~49 controls. The user can monitor
progress by tailing `<partner_folder>/validation_progress.log`. The
binary also surfaces structured per-control log lines on stderr (Kiro
shows these in the MCP server panel).

### Step 7: Present Results

`thor_validate` returns a one-line summary
(`passed=… failed=… waived=… errored=…`). The full markdown report is
written to:

- `<partner_folder>/validation_summary.md` (root copy, overwritten each run)
- `<partner_folder>/reports/summary/validation_summary_<ts>.md` (timestamped archive)
- `<partner_folder>/reports/run_<ts>.json` (auditable run manifest)

Read the markdown summary back if the user wants the full reasoning,
or call `thor_export` to produce a self-contained HTML version.

Failed controls include specific gaps identified. ERRORED controls
(rare; transport/Bedrock failures) are bucketed separately and surface
as exit code 4 from the CLI, but the MCP tool just reports them in
the `errored` count.

### Step 8: Partial Re-Validation (after remediation)

When a partner remediates specific failed controls and wants to
re-validate only those, call `thor_validate` with the `controls`
parameter set to the space-separated list of control IDs to re-run.
For example: `controls: "POV-002 POV-003 REL-001-SOFTWARE"`.

This skips all passing controls and only re-evaluates the specified
ones. Combine with `skip_conversion: true` since the CSV hasn't
changed.

IMPORTANT: When presenting results from a full run, always tell the
user which controls failed and offer to re-validate just those after
they make changes. Example phrasing: "These 5 controls failed. Once
you update your responses or supporting docs for these, I can re-run
validation on just those controls."

### Step 9: Optional — Compare with a Prior Run

If the user wants to compare two runs, call `thor_diff`:

- `mode: latest` (default) — compares the two most recent runs
- `mode: all` — produces a timeline table across every run
- `mode: custom` with `run1` / `run2` (timestamp substrings) — pick
  any two

## Tips

- **Partial re-run**: validate a specific subset by passing
  space-separated control IDs via `controls` (e.g.,
  `"ACCT-001 COST-001"`).
- **Consensus mode**: set `consensus: 3` for borderline cases. When
  every control passes runs 1 and 2, run 3 is skipped.
- **Large PDFs**: Thor splits PDFs over 40 pages automatically via
  `pdfcpu` — no external tools required.
- **`.pptx` files**: Thor extracts text from every `.pptx` it sees,
  regardless of size, using the standard library's zip + xml
  parsers.
- **Credential expiry**: refresh and retry — no server restart
  needed.
