"""
Thor PSA Validator — MCP Server

Exposes Thor CLI validation capabilities as MCP tools that Kiro can invoke.
Bundles Thor source directly and calls functions without subprocess.
"""

import sys
import os
import json
import shutil
from pathlib import Path
from typing import Optional

# Add the bundled thor source to the Python path
SERVER_DIR = Path(__file__).parent
THOR_SOURCE_DIR = SERVER_DIR / "thor"
sys.path.insert(0, str(SERVER_DIR))

from mcp.server import Server, NotificationOptions
from mcp.server.stdio import stdio_server
from mcp.types import Tool, TextContent

# Import Thor modules (bundled in server/thor/)
from thor.thor_validator import ThorValidator
from thor.extract_partner_responses import extract_responses
from thor.control_category_mapping import get_applicable_controls


EXPIRED_CREDS_MSG = (
    "⚠️ **AWS credentials have expired.**\n\n"
    "Please refresh your credentials and try again:\n"
    "- **Isengard**: Run `isengardcli assume <account>` in your terminal\n"
    "- **SSO**: Run `aws sso login --profile your-profile` in your terminal\n"
    "- **Ada**: Run `ada credentials update` in your terminal\n\n"
    "Thor will automatically pick up the fresh credentials on the next call — no server restart needed."
)


def _is_expired_creds_error(e: Exception) -> bool:
    """Check if an exception is due to expired AWS credentials."""
    error_str = str(e).lower()
    return any(phrase in error_str for phrase in [
        "expiredtokenexception",
        "expired",
        "security token",
        "token has expired",
    ])


app = Server("thor-psa-validator")


def _find_excel_file(partner_folder: str) -> Optional[str]:
    """Find the Excel checklist in a partner folder."""
    folder = Path(partner_folder)
    for ext in ["*.xlsx", "*.xls"]:
        files = list(folder.glob(ext))
        if files:
            return str(files[0])
    return None


def _get_tools_dir() -> str:
    """Get the bundled tools directory path."""
    return str(THOR_SOURCE_DIR / "tools")


@app.list_tools()
async def list_tools():
    """Return the list of available Thor tools."""
    return [
        Tool(
            name="thor_convert",
            description="Convert an Excel checklist to CSV format for validation. Auto-detects SERVICE/SOFTWARE type from Excel content.",
            inputSchema={
                "type": "object",
                "properties": {
                    "partner_folder": {
                        "type": "string",
                        "description": "Path to the partner folder containing the Excel checklist file"
                    },
                    "app_type": {
                        "type": "string",
                        "enum": ["SERVICE", "SOFTWARE"],
                        "description": "Application type (auto-detected if not specified)"
                    }
                },
                "required": ["partner_folder"]
            }
        ),
        Tool(
            name="thor_validate",
            description="Run Bedrock AI validation on partner submissions. Evaluates each control against partner evidence and returns PASS/FAIL/WAIVED with reasoning.",
            inputSchema={
                "type": "object",
                "properties": {
                    "partner_folder": {
                        "type": "string",
                        "description": "Path to partner folder with partner_responses.csv and supporting_docs/"
                    },
                    "controls": {
                        "type": "string",
                        "description": "Space-separated list of control IDs to validate (e.g., 'ACCT-001 COST-001'). Validates all if omitted."
                    },
                    "app_type": {
                        "type": "string",
                        "enum": ["SERVICE", "SOFTWARE"],
                        "description": "Application type"
                    },
                    "skip_conversion": {
                        "type": "boolean",
                        "description": "Skip Excel-to-CSV conversion (use if partner_responses.csv already exists)",
                        "default": False
                    },
                    "designation_category": {
                        "type": "string",
                        "description": "Competency designation category to filter applicable controls"
                    },
                    "consensus": {
                        "type": "integer",
                        "description": "Number of consensus runs per control (default 1)",
                        "default": 1
                    }
                },
                "required": ["partner_folder"]
            }
        ),
        Tool(
            name="thor_run",
            description="Full end-to-end workflow: download partner files from S3 via ticket ID, convert Excel, validate all controls, generate report.",
            inputSchema={
                "type": "object",
                "properties": {
                    "ticket_id": {
                        "type": "string",
                        "description": "SIM ticket ID to pull partner files from"
                    },
                    "working_dir": {
                        "type": "string",
                        "description": "Working directory for downloads (default: ~/Documents/PSA_Validations)"
                    },
                    "controls": {
                        "type": "string",
                        "description": "Space-separated list of specific control IDs to validate"
                    },
                    "consensus": {
                        "type": "integer",
                        "description": "Number of consensus runs (default 1)",
                        "default": 1
                    },
                    "skip_download": {
                        "type": "boolean",
                        "description": "Skip S3 download step (use existing files)",
                        "default": False
                    }
                },
                "required": ["ticket_id"]
            }
        ),
        Tool(
            name="thor_diff",
            description="Compare validation runs for the same partner to show what changed over time. Use when the user asks 'what changed between runs', 'what improved', 'show me the diff', or 'compare the partial run'. By default compares two most recent full runs. IMPORTANT: If the user asks to compare a specific or partial run, use mode='custom' with run1/run2 timestamps.",
            inputSchema={
                "type": "object",
                "properties": {
                    "partner_folder": {
                        "type": "string",
                        "description": "Path to partner folder containing reports/summary/ with timestamped validation reports"
                    },
                    "mode": {
                        "type": "string",
                        "enum": ["latest", "all", "custom"],
                        "description": "Comparison mode: 'latest' (default) compares two most recent full runs. 'all' shows timeline. 'custom' compares specific runs including partial ones — use when user mentions a specific or partial run."
                    },
                    "run1": {
                        "type": "string",
                        "description": "Timestamp or filename of first (older) report for mode='custom'. e.g. '20260506_095108' or full filename."
                    },
                    "run2": {
                        "type": "string",
                        "description": "Timestamp or filename of second (newer) report for mode='custom'. e.g. '20260506_101530' or full filename."
                    }
                },
                "required": ["partner_folder"]
            }
        ),
        Tool(
            name="thor_prep",
            description="Set up a new partner folder for validation. Creates folder structure and prepares for file placement.",
            inputSchema={
                "type": "object",
                "properties": {
                    "partner_name": {
                        "type": "string",
                        "description": "Name of the partner (used for folder name)"
                    },
                    "designation_category": {
                        "type": "string",
                        "description": "Competency designation category"
                    },
                    "folder": {
                        "type": "string",
                        "description": "Custom path for the partner folder (optional)"
                    },
                    "app_type": {
                        "type": "string",
                        "enum": ["SERVICE", "SOFTWARE"],
                        "description": "Application type"
                    }
                },
                "required": ["partner_name", "designation_category"]
            }
        ),
        Tool(
            name="thor_list_controls",
            description="List available validation controls, optionally filtered by designation category.",
            inputSchema={
                "type": "object",
                "properties": {
                    "category": {
                        "type": "string",
                        "description": "Designation category to filter controls by"
                    }
                },
                "required": []
            }
        ),
        Tool(
            name="thor_export",
            description="Export the latest validation report to a clean HTML file that can be shared with stakeholders. Use when the user asks to 'export results', 'generate a report', 'create a PDF', or 'make it shareable'.",
            inputSchema={
                "type": "object",
                "properties": {
                    "partner_folder": {
                        "type": "string",
                        "description": "Path to partner folder containing validation_summary.md"
                    },
                    "report": {
                        "type": "string",
                        "description": "Specific report filename/timestamp to export (optional — defaults to latest)"
                    }
                },
                "required": ["partner_folder"]
            }
        ),
        Tool(
            name="thor_doctor",
            description="Health check — verifies Python version, AWS credentials, dependencies, and configuration.",
            inputSchema={
                "type": "object",
                "properties": {},
                "required": []
            }
        ),
    ]


@app.call_tool()
async def call_tool(name: str, arguments: dict):
    """Handle tool invocations."""

    if name == "thor_convert":
        return await _handle_convert(arguments)
    elif name == "thor_validate":
        return await _handle_validate(arguments)
    elif name == "thor_diff":
        return await _handle_diff(arguments)
    elif name == "thor_prep":
        return await _handle_prep(arguments)
    elif name == "thor_list_controls":
        return await _handle_list_controls(arguments)
    elif name == "thor_export":
        return await _handle_export(arguments)
    elif name == "thor_doctor":
        return await _handle_doctor(arguments)
    else:
        return [TextContent(type="text", text=f"Unknown tool: {name}")]


async def _handle_convert(args: dict):
    """Convert Excel checklist to CSV."""
    partner_folder = args["partner_folder"]
    app_type = args.get("app_type", "SOFTWARE")

    excel_file = _find_excel_file(partner_folder)
    if not excel_file:
        return [TextContent(type="text", text=f"Error: No Excel file (.xlsx) found in {partner_folder}")]

    output_path = str(Path(partner_folder) / "partner_responses.csv")

    try:
        extract_responses(excel_file, output_path, application_type=app_type)
        return [TextContent(type="text", text=f"Successfully converted Excel to CSV.\nOutput: {output_path}\nApplication type: {app_type}")]
    except Exception as e:
        return [TextContent(type="text", text=f"Error during conversion: {str(e)}")]


async def _handle_validate(args: dict):
    """Run validation on partner folder."""
    partner_folder = args["partner_folder"]
    controls_str = args.get("controls", "")
    app_type = args.get("app_type", "SOFTWARE")
    skip_conversion = args.get("skip_conversion", False)
    designation_category = args.get("designation_category", "")
    consensus = args.get("consensus", 1)

    # Convert if needed
    if not skip_conversion:
        excel_file = _find_excel_file(partner_folder)
        if excel_file:
            output_path = str(Path(partner_folder) / "partner_responses.csv")
            try:
                extract_responses(excel_file, output_path, application_type=app_type)
            except Exception as e:
                return [TextContent(type="text", text=f"Conversion error: {str(e)}")]

    # Determine controls to validate
    csv_path = Path(partner_folder) / "partner_responses.csv"
    if not csv_path.exists():
        return [TextContent(type="text", text=f"Error: partner_responses.csv not found in {partner_folder}. Run convert first.")]

    import pandas as pd
    df = pd.read_csv(csv_path)
    # Support both 'controlId' and 'control_id' column names
    if "controlId" in df.columns:
        all_controls = df["controlId"].tolist()
    elif "control_id" in df.columns:
        all_controls = df["control_id"].tolist()
    else:
        all_controls = []

    # Filter to only valid control IDs that exist in CONTEXT.csv
    tools_dir = _get_tools_dir()
    context_path = Path(tools_dir) / "CONTEXT.csv"
    valid_control_ids = set()
    if context_path.exists():
        context_df = pd.read_csv(context_path)
        if "controlId" in context_df.columns:
            valid_control_ids = set(context_df["controlId"].tolist())
        elif "control_id" in context_df.columns:
            valid_control_ids = set(context_df["control_id"].tolist())
    
    all_controls = list(dict.fromkeys(c for c in all_controls if c in valid_control_ids))

    if controls_str:
        control_ids = controls_str.split()
    elif designation_category:
        control_ids = get_applicable_controls(designation_category, all_controls)
    else:
        control_ids = all_controls

    if not control_ids:
        return [TextContent(type="text", text="Error: No controls found to validate.")]

    # Run validation in parallel chunks with progress file
    try:
        validator = ThorValidator(prompts_dir=tools_dir)

        import asyncio
        loop = asyncio.get_event_loop()

        results = {}
        total = len(control_ids)
        chunk_size = 10  # Process 10 controls at a time in parallel
        
        # Write progress to a file the user can tail
        progress_file = Path(partner_folder) / "validation_progress.log"
        
        def log_progress(msg):
            with open(progress_file, "a") as f:
                from datetime import datetime
                f.write(f"[{datetime.now().strftime('%H:%M:%S')}] {msg}\n")

        log_progress(f"🚀 Starting validation of {total} controls")
        log_progress(f"Controls: {', '.join(control_ids)}")

        for chunk_start in range(0, total, chunk_size):
            chunk = control_ids[chunk_start:chunk_start + chunk_size]
            chunk_end = min(chunk_start + len(chunk), total)

            log_progress(f"⚡ Validating controls {chunk_start + 1}-{chunk_end} of {total}: {', '.join(chunk)}")

            def run_chunk(c, pf):
                """Run validation with stdout suppressed to avoid corrupting MCP stdio transport."""
                import io
                old_stdout = sys.stdout
                sys.stdout = io.StringIO()  # Suppress all print() from validator
                try:
                    return validator.validate_batch(c, pf, system_mode="revised")
                finally:
                    sys.stdout = old_stdout

            chunk_results = await loop.run_in_executor(
                None,
                lambda c=chunk: run_chunk(c, partner_folder)
            )
            results.update(chunk_results)

            # Log chunk results
            for cid, res in chunk_results.items():
                status_icon = "✅" if str(res).upper().startswith("YES") else "❌"
                log_progress(f"  {status_icon} {cid}")
            
            passed_so_far = sum(1 for r in results.values() if str(r).upper().startswith("YES"))
            log_progress(f"📊 Progress: {len(results)}/{total} done, {passed_so_far} passed so far")

        log_progress(f"🏁 Validation complete!")

        # Format results
        output_lines = [f"# Validation Results — {Path(partner_folder).name}", ""]
        passed = 0
        failed = 0
        waived = 0

        for control_id, result in results.items():
            # Results can be strings like "YES ..." or "NO ..." or dicts
            if isinstance(result, dict):
                status = result.get("status", "UNKNOWN")
                reasoning = result.get("reasoning", "")
            else:
                result_str = str(result).strip()
                if result_str.upper().startswith("YES"):
                    status = "YES"
                    reasoning = result_str[3:].strip().lstrip("—-:").strip()
                elif result_str.upper().startswith("WAIVED"):
                    status = "WAIVED"
                    reasoning = result_str[6:].strip().lstrip("—-:").strip()
                elif result_str.upper().startswith("NO"):
                    status = "NO"
                    reasoning = result_str[2:].strip().lstrip("—-:").strip()
                else:
                    status = "UNKNOWN"
                    reasoning = result_str

            if status == "YES":
                passed += 1
            elif status == "NO":
                failed += 1
            elif status == "WAIVED":
                waived += 1

            icon = "✅" if status == "YES" else "❌" if status == "NO" else "⏭️"
            output_lines.append(f"{icon} **{control_id}**: {status}")
            if reasoning and status != "YES":
                output_lines.append(f"   → {reasoning[:200]}")
            output_lines.append("")

        # Insert summary after header
        summary = f"**Total: {len(results)} controls** | ✅ Passed: {passed} | ❌ Failed: {failed} | ⏭️ Waived: {waived}"
        output_lines.insert(2, summary)
        output_lines.insert(3, "")

        response_text = "\n".join(output_lines)
        log_progress(f"📤 Returning response ({len(response_text)} chars)")

        return [TextContent(type="text", text=response_text)]

    except Exception as e:
        log_progress(f"❌ ERROR: {str(e)}")
        import traceback
        log_progress(traceback.format_exc())
        return [TextContent(type="text", text=f"Validation error: {str(e)}")]


async def _handle_run(args: dict):
    """Full end-to-end workflow."""
    ticket_id = args["ticket_id"]
    working_dir = args.get("working_dir", str(Path.home() / "Documents" / "PSA_Validations"))
    controls_str = args.get("controls", "")
    consensus = args.get("consensus", 1)
    skip_download = args.get("skip_download", False)

    steps_completed = []

    # Step 1: Download (if not skipping)
    if not skip_download:
        try:
            from thor.s3_integration import S3AppDownloader
            downloader = S3AppDownloader()
            success, partner_folder = downloader.download_application_files(
                app_id=None, output_dir=Path(working_dir), folder_name=None
            )
            if not success:
                # Try getting app_id from ticket
                app_id, partner_name = downloader.get_app_id_from_ticket(ticket_id)
                if app_id:
                    success, partner_folder = downloader.download_application_files(
                        app_id=app_id, output_dir=Path(working_dir), folder_name=partner_name
                    )
            if success:
                steps_completed.append(f"✅ Downloaded files to: {partner_folder}")
            else:
                return [TextContent(type="text", text="Error: Could not download partner files from S3.")]
        except Exception as e:
            return [TextContent(type="text", text=f"Download error: {str(e)}")]
    else:
        # Find existing partner folder
        partner_folder = working_dir
        steps_completed.append("⏭️ Skipped download (using existing files)")

    # Step 2: Validate
    validate_args = {
        "partner_folder": str(partner_folder),
        "controls": controls_str,
        "skip_conversion": False,
        "consensus": consensus
    }
    result = await _handle_validate(validate_args)
    steps_completed.append("✅ Validation complete")

    # Combine output
    header = f"# Thor Run — Ticket {ticket_id}\n\n" + "\n".join(steps_completed) + "\n\n---\n\n"
    return [TextContent(type="text", text=header + result[0].text)]


async def _handle_diff(args: dict):
    """Compare validation runs for the same partner."""
    partner_folder = args["partner_folder"]
    mode = args.get("mode", "latest")
    run1_name = args.get("run1", "")
    run2_name = args.get("run2", "")

    try:
        import re
        summary_dir = Path(partner_folder) / "reports" / "summary"
        
        if not summary_dir.exists():
            return [TextContent(type="text", text=f"Error: No reports/summary/ directory found in {partner_folder}. Run validation at least twice first.")]

        reports = sorted(summary_dir.glob("validation_summary_*.md"))
        
        if len(reports) < 2:
            return [TextContent(type="text", text=f"Error: Need at least 2 validation reports to compare. Found {len(reports)}. Run validation again to create a second report.")]

        # Select reports based on mode
        if mode == "custom" and (run1_name or run2_name):
            # Support partial timestamp matching (e.g. "20260506_095108" matches "validation_summary_20260506_095108.md")
            def find_report(name_or_ts):
                if not name_or_ts:
                    return None
                for r in reports:
                    if name_or_ts in r.name or name_or_ts in r.stem:
                        return r
                # Try exact path
                exact = summary_dir / name_or_ts
                if exact.exists():
                    return exact
                return None

            run1_path = find_report(run1_name) if run1_name else reports[0]
            run2_path = find_report(run2_name) if run2_name else reports[-1]
            
            if run1_name and not run1_path:
                available = ", ".join(f"`{r.stem.replace('validation_summary_', '')}`" for r in reports)
                return [TextContent(type="text", text=f"Error: No report matching '{run1_name}' found.\n\nAvailable reports: {available}")]
            if run2_name and not run2_path:
                available = ", ".join(f"`{r.stem.replace('validation_summary_', '')}`" for r in reports)
                return [TextContent(type="text", text=f"Error: No report matching '{run2_name}' found.\n\nAvailable reports: {available}")]
        elif mode == "all":
            return await _handle_diff_timeline(reports, partner_folder)
        else:
            # "latest" — compare the two most recent reports (any size)
            run1_path = reports[-2]
            run2_path = reports[-1]

        def parse_report(path):
            content = path.read_text()
            controls = {}
            current_control = None
            current_status = None
            in_reason = False
            reason_lines = []

            for line in content.splitlines():
                ctrl_match = re.match(r'###\s+[✅❌⏭️]\s+([A-Z]+-\d+(?:-[A-Z]+)?)', line)
                if ctrl_match:
                    if current_control:
                        controls[current_control] = {"status": current_status, "reasoning": "\n".join(reason_lines).strip()}
                    current_control = ctrl_match.group(1)
                    current_status = "PASS" if "✅" in line else "FAIL" if "❌" in line else "WAIVED"
                    in_reason = False
                    reason_lines = []
                elif line.strip() == "**Reason**:":
                    in_reason = True
                elif line.strip() == "```" and in_reason:
                    if reason_lines:
                        in_reason = False
                elif in_reason:
                    reason_lines.append(line)

            if current_control:
                controls[current_control] = {"status": current_status, "reasoning": "\n".join(reason_lines).strip()}
            return controls

        results1 = parse_report(run1_path)
        results2 = parse_report(run2_path)

        all_controls = sorted(set(list(results1.keys()) + list(results2.keys())))
        changed, unchanged_pass, unchanged_fail, new_in_run2, removed_in_run2 = [], [], [], [], []

        for ctrl in all_controls:
            r1 = results1.get(ctrl)
            r2 = results2.get(ctrl)
            if r1 is None:
                new_in_run2.append((ctrl, r2))
            elif r2 is None:
                removed_in_run2.append((ctrl, r1))
            elif r1["status"] != r2["status"]:
                changed.append((ctrl, r1["status"], r2["status"], r1.get("reasoning", ""), r2.get("reasoning", "")))
            elif r1["status"] == "PASS":
                unchanged_pass.append(ctrl)
            else:
                unchanged_fail.append(ctrl)

        run1_ts = run1_path.stem.replace("validation_summary_", "")
        run2_ts = run2_path.stem.replace("validation_summary_", "")

        output = [
            f"# Validation Diff — {Path(partner_folder).name}",
            "",
            f"**Comparing:** `{run1_ts}` ({len(results1)} controls) → `{run2_ts}` ({len(results2)} controls)",
            f"**Changed:** {len(changed)} | **Unchanged Pass:** {len(unchanged_pass)} | **Unchanged Fail:** {len(unchanged_fail)}",
            "",
        ]

        if changed:
            output.append("## 🔄 Changed Controls")
            output.append("")
            for ctrl, old_status, new_status, old_reason, new_reason in changed:
                icon = "🟢" if new_status == "PASS" else "🔴" if new_status == "FAIL" else "⚪"
                output.append(f"### {icon} {ctrl}: {old_status} → {new_status}")
                output.append("")
                if old_reason:
                    output.append(f"**Previous reasoning:** {old_reason}")
                    output.append("")
                if new_reason:
                    output.append(f"**Current reasoning:** {new_reason}")
                    output.append("")

        if unchanged_fail:
            output.append("## ❌ Still Failing")
            output.append("")
            for ctrl in unchanged_fail:
                r = results2.get(ctrl, {})
                reason = r.get("reasoning", "")
                output.append(f"### {ctrl}")
                if reason:
                    output.append(f"→ {reason}")
                output.append("")

        if new_in_run2:
            output.append(f"## 🆕 New in latest run ({len(new_in_run2)} controls)")
            output.append("")
            for ctrl, data in new_in_run2:
                status = data["status"] if data else "UNKNOWN"
                icon = "✅" if status == "PASS" else "❌"
                output.append(f"- {icon} {ctrl}: {status}")
            output.append("")

        if removed_in_run2:
            output.append(f"## ⚠️ Not in latest run ({len(removed_in_run2)} controls)")
            output.append("")
            for ctrl, data in removed_in_run2:
                output.append(f"- {ctrl} (was {data['status']})")
            output.append("")

        if unchanged_pass:
            output.append(f"## ✅ Still Passing ({len(unchanged_pass)} controls)")
            output.append("")
            output.append(", ".join(unchanged_pass))
            output.append("")

        output.append("---")
        output.append(f"**Available reports ({len(reports)}):** " + ", ".join(f"`{r.stem.replace('validation_summary_', '')}`" for r in reports))

        return [TextContent(type="text", text="\n".join(output))]

    except Exception as e:
        return [TextContent(type="text", text=f"Diff error: {str(e)}")]


async def _handle_diff_timeline(reports, partner_folder):
    """Show a timeline summary across all validation runs."""
    output = [
        f"# Validation Timeline — {Path(partner_folder).name}",
        "",
        "| Run | Date | Controls | Passed | Failed | Pass Rate |",
        "|-----|------|----------|--------|--------|-----------|",
    ]

    for i, report in enumerate(reports, 1):
        ts = report.stem.replace("validation_summary_", "")
        date_str = f"{ts[:4]}-{ts[4:6]}-{ts[6:8]}"
        if len(ts) >= 13:
            date_str += f" {ts[9:11]}:{ts[11:13]}"
        
        content = report.read_text()
        passed = content.count("### ✅")
        failed = content.count("### ❌")
        total = passed + failed
        rate = f"{passed/total*100:.0f}%" if total > 0 else "N/A"
        output.append(f"| #{i} | {date_str} | {total} | {passed} | {failed} | {rate} |")

    output.append("")
    output.append("Use `mode: custom` with `run1`/`run2` filenames to compare any two specific runs.")

    return [TextContent(type="text", text="\n".join(output))]


async def _handle_prep(args: dict):
    """Create a new partner folder for validation."""
    partner_name = args["partner_name"]
    designation_category = args["designation_category"]
    base_folder = args.get("folder", str(Path.home() / "Documents" / "PSA_Validations"))
    app_type = args.get("app_type", "SERVICE")

    # Create folder structure
    partner_folder = Path(base_folder) / partner_name
    supporting_docs = partner_folder / "supporting_docs"
    reports_dir = partner_folder / "reports" / "summary"

    partner_folder.mkdir(parents=True, exist_ok=True)
    supporting_docs.mkdir(parents=True, exist_ok=True)
    reports_dir.mkdir(parents=True, exist_ok=True)

    # Get applicable controls for the category
    from thor.control_category_mapping import get_applicable_controls
    # Load all control IDs from tools
    tools_dir = _get_tools_dir()
    context_path = Path(tools_dir) / "CONTEXT.csv"
    all_controls = []
    if context_path.exists():
        import pandas as pd
        ctx_df = pd.read_csv(context_path)
        if "control_id" in ctx_df.columns:
            all_controls = ctx_df["control_id"].tolist()

    applicable = get_applicable_controls(designation_category, all_controls)

    output = [
        f"# Partner Folder Created: {partner_name}",
        "",
        f"**Location:** {partner_folder}",
        f"**Designation:** {designation_category}",
        f"**App Type:** {app_type}",
        f"**Applicable Controls:** {len(applicable)}",
        "",
        "## Next Steps",
        "",
        "1. Place the Excel checklist (`.xlsx`) in the root of the partner folder",
        "2. Place supporting documents (PDFs, architecture diagrams) in `supporting_docs/`",
        "3. Run `thor_convert` to extract responses from the Excel",
        "4. Run `thor_validate` to validate against controls",
        "",
        "## Folder Structure",
        f"```",
        f"{partner_name}/",
        f"├── (place checklist.xlsx here)",
        f"├── supporting_docs/",
        f"│   └── (place PDFs, docs here)",
        f"└── reports/",
        f"    └── summary/",
        f"```",
    ]

    return [TextContent(type="text", text="\n".join(output))]


async def _handle_list_controls(args: dict):
    """List available controls."""
    category = args.get("category", "")

    tools_dir = _get_tools_dir()
    context_path = Path(tools_dir) / "CONTEXT.csv"

    if not context_path.exists():
        return [TextContent(type="text", text=f"Error: CONTEXT.csv not found at {context_path}")]

    import pandas as pd
    df = pd.read_csv(context_path)

    # Support both 'controlId' and 'control_id' column names
    if "controlId" in df.columns:
        all_controls = df["controlId"].tolist()
    elif "control_id" in df.columns:
        all_controls = df["control_id"].tolist()
    else:
        all_controls = []

    if category:
        filtered = get_applicable_controls(category, all_controls)
        header = f"# Controls for: {category}\n\n**{len(filtered)} controls:**\n"
        control_list = "\n".join(f"- {c}" for c in filtered)
    else:
        header = f"# All Available Controls\n\n**{len(all_controls)} controls:**\n"
        control_list = "\n".join(f"- {c}" for c in all_controls)

    return [TextContent(type="text", text=header + control_list)]


async def _handle_export(args: dict):
    """Export validation report to HTML."""
    partner_folder = args["partner_folder"]
    report_name = args.get("report", "")

    try:
        folder = Path(partner_folder).expanduser().resolve()
        summary_dir = folder / "reports" / "summary"
        
        # Find the report to export
        target = None
        
        if report_name:
            # Fuzzy match in reports/summary/
            if summary_dir.exists():
                for r in sorted(summary_dir.glob("validation_summary_*.md")):
                    if report_name in r.name or report_name in r.stem:
                        target = r
                        break
        
        if not target:
            # Try root validation_summary.md first
            root_summary = folder / "validation_summary.md"
            if root_summary.exists():
                target = root_summary
            # Then try latest in reports/summary/
            elif summary_dir.exists():
                reports = sorted(summary_dir.glob("validation_summary_*.md"))
                if reports:
                    target = reports[-1]

        if not target or not target.exists():
            return [TextContent(type="text", text=f"Error: No validation report found in {folder}\n\nExpected either:\n- `{folder}/validation_summary.md`\n- `{folder}/reports/summary/validation_summary_*.md`")]

        md_content = target.read_text()
        partner_name = folder.name

        # Convert markdown to HTML then to PDF
        html = _markdown_to_html(md_content, partner_name)
        pdf_path = folder / f"validation_report_{partner_name}.pdf"
        
        try:
            from weasyprint import HTML
            HTML(string=html).write_pdf(str(pdf_path))
            return [TextContent(type="text", text=f"✅ Exported PDF report to:\n`{pdf_path}`")]
        except ImportError:
            # Fallback to HTML if weasyprint not installed
            html_path = folder / f"validation_report_{partner_name}.html"
            html_path.write_text(html)
            return [TextContent(type="text", text=f"✅ Exported HTML report to:\n`{html_path}`\n\nFor PDF export, install weasyprint: `pip install weasyprint`")]
        except Exception as e:
            # weasyprint installed but failed — fall back to HTML
            html_path = folder / f"validation_report_{partner_name}.html"
            html_path.write_text(html)
            return [TextContent(type="text", text=f"⚠️ PDF generation failed ({str(e)[:100]}). Exported HTML instead:\n`{html_path}`")]

    except Exception as e:
        return [TextContent(type="text", text=f"Export error: {str(e)}")]


def _markdown_to_html(md_content: str, partner_name: str) -> str:
    """Convert validation markdown to a clean, styled HTML report."""
    import re
    
    # Basic markdown to HTML conversion
    lines = md_content.split("\n")
    html_lines = []
    in_code_block = False
    
    for line in lines:
        if line.strip().startswith("```"):
            if in_code_block:
                html_lines.append("</pre>")
                in_code_block = False
            else:
                html_lines.append("<pre>")
                in_code_block = True
            continue
        
        if in_code_block:
            html_lines.append(line)
            continue
        
        # Headers
        if line.startswith("### "):
            html_lines.append(f"<h3>{line[4:]}</h3>")
        elif line.startswith("## "):
            html_lines.append(f"<h2>{line[3:]}</h2>")
        elif line.startswith("# "):
            html_lines.append(f"<h1>{line[2:]}</h1>")
        elif line.startswith("- "):
            html_lines.append(f"<li>{line[2:]}</li>")
        elif line.startswith("**") and line.endswith("**"):
            html_lines.append(f"<p><strong>{line[2:-2]}</strong></p>")
        elif line.strip() == "---":
            html_lines.append("<hr>")
        elif line.strip():
            # Bold inline
            line = re.sub(r'\*\*(.+?)\*\*', r'<strong>\1</strong>', line)
            html_lines.append(f"<p>{line}</p>")
        else:
            html_lines.append("")
    
    body = "\n".join(html_lines)
    
    return f"""<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Validation Report — {partner_name}</title>
    <style>
        body {{ font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 900px; margin: 0 auto; padding: 40px 20px; line-height: 1.6; color: #333; }}
        h1 {{ color: #1a1a1a; border-bottom: 2px solid #e0e0e0; padding-bottom: 10px; }}
        h2 {{ color: #2c3e50; margin-top: 30px; }}
        h3 {{ margin-top: 20px; }}
        pre {{ background: #f5f5f5; padding: 15px; border-radius: 6px; overflow-x: auto; font-size: 13px; white-space: pre-wrap; }}
        li {{ margin: 4px 0; }}
        hr {{ border: none; border-top: 1px solid #e0e0e0; margin: 30px 0; }}
        .pass {{ color: #27ae60; }}
        .fail {{ color: #e74c3c; }}
        @media print {{ body {{ max-width: 100%; padding: 20px; }} }}
    </style>
</head>
<body>
{body}
<footer style="margin-top: 40px; padding-top: 20px; border-top: 1px solid #e0e0e0; color: #888; font-size: 12px;">
    Generated by Thor PSA Validator
</footer>
</body>
</html>"""


async def _handle_doctor(args: dict):
    """Health check."""
    checks = []

    # Python version
    py_version = f"{sys.version_info.major}.{sys.version_info.minor}.{sys.version_info.micro}"
    py_ok = sys.version_info >= (3, 8)
    checks.append(f"{'✅' if py_ok else '❌'} Python: {py_version} {'(OK)' if py_ok else '(Need 3.8+)'}")

    # AWS credentials
    aws_key = os.environ.get("AWS_ACCESS_KEY_ID", "")
    aws_secret = os.environ.get("AWS_SECRET_ACCESS_KEY", "")
    aws_profile = os.environ.get("AWS_PROFILE", "")
    aws_region = os.environ.get("AWS_DEFAULT_REGION", os.environ.get("AWS_REGION", ""))

    # Check if credentials are available via env vars, profile, or boto3 credential chain
    aws_ok = bool(aws_key and aws_secret)
    aws_method = "env vars"
    if not aws_ok and aws_profile:
        try:
            import boto3
            session = boto3.Session(profile_name=aws_profile)
            creds = session.get_credentials()
            if creds:
                aws_ok = True
                aws_method = f"profile '{aws_profile}'"
                if not aws_region:
                    aws_region = session.region_name or ""
        except Exception:
            pass
    if not aws_ok:
        try:
            import boto3
            session = boto3.Session()
            creds = session.get_credentials()
            if creds:
                aws_ok = True
                aws_method = "default credential chain"
                if not aws_region:
                    aws_region = session.region_name or ""
        except Exception:
            pass

    checks.append(f"{'✅' if aws_ok else '❌'} AWS Credentials: {'Configured via ' + aws_method if aws_ok else 'Missing'}")
    checks.append(f"{'✅' if aws_region else '⚠️'} AWS Region: {aws_region or 'Not set (will use default)'}")

    # Bedrock access
    if aws_ok:
        try:
            import boto3
            session = boto3.Session(profile_name=aws_profile or None)
            client = session.client("bedrock-runtime", region_name=aws_region or "us-east-1")
            checks.append("✅ Bedrock client: Initialized")
        except Exception as e:
            checks.append(f"❌ Bedrock client: {str(e)}")
    else:
        checks.append("⏭️ Bedrock client: Skipped (no credentials)")

    # qpdf
    qpdf_path = shutil.which("qpdf")
    checks.append(f"{'✅' if qpdf_path else '⚠️'} qpdf: {'Installed' if qpdf_path else 'Not found (optional, for large PDFs)'}")

    # Tools directory
    tools_dir = _get_tools_dir()
    tools_ok = Path(tools_dir).exists() and (Path(tools_dir) / "CONTEXT.csv").exists()
    checks.append(f"{'✅' if tools_ok else '❌'} Tools directory: {'OK' if tools_ok else 'Missing'}")

    # Dependencies
    deps_ok = True
    for dep in ["pandas", "openpyxl", "boto3", "yaml", "PyPDF2", "PIL"]:
        try:
            __import__(dep)
        except ImportError:
            checks.append(f"❌ Dependency: {dep} not installed")
            deps_ok = False
    if deps_ok:
        checks.append("✅ Python dependencies: All installed")

    passed = sum(1 for c in checks if c.startswith("✅"))
    total = len(checks)

    header = f"# Thor Health Check\n\n**{passed}/{total} checks passed**\n\n"
    return [TextContent(type="text", text=header + "\n".join(checks))]


async def main():
    """Run the MCP server."""
    async with stdio_server() as (read_stream, write_stream):
        await app.run(read_stream, write_stream, app.create_initialization_options())


if __name__ == "__main__":
    import asyncio
    asyncio.run(main())
