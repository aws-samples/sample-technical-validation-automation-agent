#!/usr/bin/env node
/**
 * Thor MCP Server — Node.js wrapper that dispatches tool calls to the
 * `thor` CLI binary. This gives us native npx compatibility while keeping
 * all validation logic in the Go binary.
 *
 * Prerequisites: `thor` binary on PATH (via `make install-local` or
 * downloaded from releases).
 */

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { execFile } from "child_process";
import { promisify } from "util";
import { z } from "zod";

const exec = promisify(execFile);

const THOR_BIN = process.env.THOR_BIN || "thor";
const TIMEOUT = 600_000; // 10 min for long validations

async function runThor(args) {
  try {
    const { stdout, stderr } = await exec(THOR_BIN, args, {
      timeout: TIMEOUT,
      maxBuffer: 50 * 1024 * 1024, // 50MB
      env: { ...process.env },
    });
    if (stderr) process.stderr.write(stderr);
    return stdout.trim();
  } catch (err) {
    if (err.stdout) return err.stdout.trim();
    throw new Error(`thor ${args[0]} failed: ${err.message}`);
  }
}

const server = new McpServer({
  name: "thor-psa-validator",
  version: "0.2.0",
});

// thor_doctor
server.tool("thor_doctor", {}, async () => {
  const output = await runThor(["doctor", "--json"]);
  return { content: [{ type: "text", text: output }] };
});

// thor_convert
server.tool(
  "thor_convert",
  {
    partner_folder: z.string().describe("Path to partner folder containing Excel checklist"),
    app_type: z.string().optional().describe("APPLICATION type: SOFTWARE or SERVICE"),
  },
  async ({ partner_folder, app_type }) => {
    const args = ["convert", partner_folder];
    if (app_type) args.push("--app-type", app_type);
    const output = await runThor(args);
    return { content: [{ type: "text", text: output }] };
  }
);

// thor_map
server.tool(
  "thor_map",
  {
    partner_folder: z.string().describe("Path to partner folder"),
    concurrency: z.number().optional().describe("Max parallel Bedrock calls"),
    model_id: z.string().optional().describe("Bedrock model ID override"),
    app_type: z.string().optional().describe("APPLICATION type: SOFTWARE or SERVICE"),
  },
  async ({ partner_folder, concurrency, model_id, app_type }) => {
    const args = ["map", partner_folder];
    if (concurrency) args.push("--concurrency", String(concurrency));
    if (model_id) args.push("--model-id", model_id);
    if (app_type) args.push("--app-type", app_type);
    const output = await runThor(args);
    return { content: [{ type: "text", text: output }] };
  }
);

// thor_validate
server.tool(
  "thor_validate",
  {
    partner_folder: z.string().describe("Path to partner folder"),
    controls: z.string().optional().describe("Space-separated control IDs to validate"),
    category: z.string().optional().describe("Designation category to filter controls"),
    app_type: z.string().optional().describe("APPLICATION type: SOFTWARE or SERVICE"),
    consensus: z.number().optional().describe("Number of consensus runs (default 1)"),
    concurrency: z.number().optional().describe("Max parallel Bedrock calls"),
    skip_conversion: z.boolean().optional().describe("Skip Excel-to-CSV conversion"),
    no_map: z.boolean().optional().describe("Skip evidence mapping pre-pass"),
    system_mode: z.string().optional().describe("System prompt mode: old, new, or revised"),
  },
  async ({ partner_folder, controls, category, app_type, consensus, concurrency, skip_conversion, no_map, system_mode }) => {
    const args = ["validate", partner_folder];
    if (controls) args.push("--controls", controls);
    if (category) args.push("--category", category);
    if (app_type) args.push("--app-type", app_type);
    if (consensus) args.push("--consensus", String(consensus));
    if (concurrency) args.push("--concurrency", String(concurrency));
    if (skip_conversion) args.push("--skip-conversion");
    if (no_map) args.push("--no-map");
    if (system_mode) args.push("--system-mode", system_mode);
    const output = await runThor(args);
    return { content: [{ type: "text", text: output }] };
  }
);

// thor_diff
server.tool(
  "thor_diff",
  {
    partner_folder: z.string().describe("Path to partner folder"),
    mode: z.string().optional().describe("Comparison mode: latest, all, or custom"),
    run1: z.string().optional().describe("Timestamp of first run (for custom mode)"),
    run2: z.string().optional().describe("Timestamp of second run (for custom mode)"),
  },
  async ({ partner_folder, mode, run1, run2 }) => {
    const args = ["diff", partner_folder];
    if (mode) args.push("--mode", mode);
    if (run1) args.push("--run1", run1);
    if (run2) args.push("--run2", run2);
    const output = await runThor(args);
    return { content: [{ type: "text", text: output }] };
  }
);

// thor_prep
server.tool(
  "thor_prep",
  {
    partner_name: z.string().describe("Name of the partner"),
    category: z.string().describe("Designation category"),
    app_type: z.string().optional().describe("APPLICATION type: SOFTWARE or SERVICE"),
    folder: z.string().optional().describe("Custom folder path"),
  },
  async ({ partner_name, category, app_type, folder }) => {
    const args = ["prep", partner_name, "--category", category];
    if (app_type) args.push("--app-type", app_type);
    if (folder) args.push("--folder", folder);
    const output = await runThor(args);
    return { content: [{ type: "text", text: output }] };
  }
);

// thor_list_controls
server.tool(
  "thor_list_controls",
  {
    category: z.string().optional().describe("Filter by designation category"),
  },
  async ({ category }) => {
    const args = ["list-controls"];
    if (category) args.push("--category", category);
    const output = await runThor(args);
    return { content: [{ type: "text", text: output }] };
  }
);

// thor_export
server.tool(
  "thor_export",
  {
    partner_folder: z.string().describe("Path to partner folder"),
    report: z.string().optional().describe("Specific report timestamp to export"),
  },
  async ({ partner_folder, report }) => {
    const args = ["export", partner_folder];
    if (report) args.push("--report", report);
    const output = await runThor(args);
    return { content: [{ type: "text", text: output }] };
  }
);

// Start
const transport = new StdioServerTransport();
await server.connect(transport);
