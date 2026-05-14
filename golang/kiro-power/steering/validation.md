# Validation Workflow

## When to Use

Use this when a user wants to validate a partner's competency
application submission with the Thor (Go) Power.

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

If `partner_responses.csv` already exists, ask if they want to re-convert
or skip conversion.

### Step 3: Determine Application Type

Ask: *"Is this a SERVICE or SOFTWARE application?"* The Go binary picks
`SOFTWARE` by default if you don't pass `app_type`.

### Step 4: Determine Designation Category

Ask: *"What competency designation category is this for?"*

- Generative AI Applications
- Foundation Models and App Development
- Infrastructure and Data
- Agentic AI Tools
- Agentic AI Applications
- Agentic AI Consulting Services
- Generative AI Consulting Services

This filters which controls are applicable. The Go binary fixes a Python
bug here (B1): the previous category filter silently passed every
control through; the Go build correctly drops controls that don't apply
to the chosen category.

### Step 5: Convert Excel to CSV

Call `thor_convert` with the partner folder and `app_type`. The output
header is the canonical `controlId,partner_response`.

### Step 6: Run Validation

Call `thor_validate` with:

- `partner_folder`
- `category` (to filter controls)
- `app_type`
- `skip_conversion: true` (since we just converted)

The first run on a partner folder also builds an `evidence_map.json`
(one Bedrock call per supporting file with the full CONTEXT.csv catalog
and prompt contexts) so each control sees only the files relevant to
it. Subsequent runs reuse the map until files under `supporting_docs/`
change. Tell the user that the extra mapping pre-pass only happens the
first time per folder and is what keeps each per-control Bedrock call
under the 100-page-per-request cap.

If the user wants to inspect or regenerate the mapping outside a
validation run, call `thor_map` directly. To skip the pre-pass and send
every supporting file to every control (legacy behaviour), pass
`no_map: true`.

Validation runs all controls in parallel (default concurrency 10) and
typically takes 4–5 minutes for ~49 controls. The user can monitor
progress by tailing `<partner_folder>/validation_progress.log`. The Go
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
as exit code 4 from the CLI, but the MCP tool just reports them in the
`errored` count.

### Step 8: Optional — Compare with a Prior Run

If the user wants to compare two runs, call `thor_diff`:

- `mode: latest` (default) — compares the two most recent runs
- `mode: all` — produces a timeline table across every run
- `mode: custom` with `run1` / `run2` (timestamp substrings) — pick any two

## Tips

- **Partial re-run**: validate a specific subset by passing space-separated
  control IDs via `controls` (e.g., `"ACCT-001 COST-001"`).
- **Consensus mode**: set `consensus: 3` for borderline cases. The Go
  build preserves the Python early-stop semantics (skip run 3 when every
  control passed runs 1 + 2).
- **Large PDFs**: the Go build splits PDFs over 40 pages automatically
  via `pdfcpu` — no `qpdf` install required (replaces the Python
  external dependency).
- **Small `.pptx` files**: the Go build extracts text from every `.pptx`,
  not just oversized ones (improvement over Python). Verdicts on partner
  folders containing small `.pptx` evidence may differ between the two
  builds — the Go behaviour is correct.
- **Credential expiry**: refresh and retry — no server restart needed.

## Versus the Python build

The Go build is a faithful port of the Python validator with several
audit-fix improvements (correct category filter, `WAIVED` instead of
auto-pass on empty DOC-006 responses, anchored N/A detection,
multi-URL Marketplace check). For partner folders that exercise these
edge cases, the Go verdicts may differ from Python — by design. See
`golang/PLAN.md` for the full audit-fix list.
