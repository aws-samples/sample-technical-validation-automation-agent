# Validation Workflow

## When to Use

Use this when a user wants to validate a partner's competency application submission.

## Step-by-Step Flow

### Step 1: Identify the Partner Folder

Ask the user for the partner folder path. If they don't have one yet, ask if they want to:
- Create a new folder with `thor_prep` (provide partner name + designation category)
- Point to an existing folder

### Step 2: Check for Required Files

The partner folder should contain:
- An Excel checklist (`.xlsx`) at the root level
- Supporting documents in a `supporting_docs/` subdirectory (PDFs, architecture diagrams, etc.)

If `partner_responses.csv` already exists, ask if they want to re-convert or skip conversion.

### Step 3: Determine Application Type

Ask: "Is this a SERVICE or SOFTWARE application?"
- Thor can auto-detect from the Excel content if unsure

### Step 4: Determine Designation Category

Ask: "What competency designation category is this for?"
- Generative AI Consulting Services
- Generative AI Software/SaaS Services
- AI/ML Consulting Services
- AI/ML Software/SaaS Services

This filters which controls are applicable.

### Step 5: Convert Excel to CSV

Call `thor_convert` with the partner folder and app type.
- Reports the detected type and number of responses extracted
- If conversion fails, check troubleshooting in POWER.md

### Step 6: Run Validation

Call `thor_validate` with:
- `partner_folder`
- `designation_category` (to filter controls)
- `app_type`
- `skip_conversion: true` (since we just converted)

Validation runs all controls in parallel (up to 15 threads) and typically takes 4-5 minutes for ~49 controls. The user can monitor progress by tailing `<partner_folder>/validation_progress.log`.

### Step 7: Present Results

The validation returns a complete summary with:
- Total controls evaluated with pass/fail/waived counts
- Per-control verdict with full reasoning for ALL controls (passed and failed)
- Failed controls include specific gaps identified

Present the summary and ask if they want to:
- Review specific controls in detail
- Re-run specific controls (pass control IDs via `controls` parameter)
- Compare results with AWS/Loki (`thor_diff`)

### Step 8: Optional — Compare with AWS Results

If the user wants to compare, ask for the SIM ticket ID and call `thor_diff`.
Shows agreements and disagreements between Thor and AWS/Loki.

## Tips

- Validate specific controls only by passing them via the `controls` parameter (space-separated IDs)
- For consensus validation (multiple runs per control), use `thor_run` with `consensus: 3`
- Large PDFs (>80 pages or >4.5MB) are automatically split if qpdf is installed
- Credentials auto-refresh via credential_process — no need to restart the server after `isengardcli assume`
- If you get `ExpiredTokenException`, just re-run `isengardcli assume <account>` and retry — no server restart needed
