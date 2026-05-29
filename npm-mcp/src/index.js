#!/usr/bin/env node
/**
 * Thor MCP Server — self-contained Node.js implementation.
 * No external binaries required. All validation logic runs in-process.
 */

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { z } from "zod";

import { doctor } from "./tools/doctor.js";
import { convert } from "./tools/convert.js";
import { validate } from "./tools/validate.js";
import { listControls } from "./tools/list-controls.js";
import { prep } from "./tools/prep.js";
import { diff } from "./tools/diff.js";
import { exportHtml } from "./tools/export.js";
import { map } from "./tools/map.js";

const server = new McpServer({
  name: "thor-psa-validator",
  version: "1.0.0",
});

// thor_doctor
server.tool("thor_doctor", {}, async () => {
  const result = await doctor();
  return { content: [{ type: "text", text: JSON.stringify(result, null, 2) }] };
});

// thor_convert
server.tool(
  "thor_convert",
  {
    partner_folder: z.string().describe("Path to partner folder containing Excel checklist"),
    app_type: z.string().optional().describe("APPLICATION type: SOFTWARE or SERVICE (default: SOFTWARE)"),
  },
  async ({ partner_folder, app_type }) => {
    const result = await convert(partner_folder, app_type || "SOFTWARE");
    return { content: [{ type: "text", text: result }] };
  }
);

// thor_validate
server.tool(
  "thor_validate",
  {
    partner_folder: z.string().describe("Path to partner folder"),
    controls: z.string().optional().describe("Space-separated control IDs to validate"),
    skip_conversion: z.boolean().optional().describe("Skip Excel-to-CSV conversion"),
    app_type: z.string().optional().describe("APPLICATION type: SOFTWARE or SERVICE"),
    runs: z.number().optional().describe("How many times to run each control (majority-vote). Default 1. Set to 3 for more reliable results."),
  },
  async ({ partner_folder, controls, skip_conversion, app_type, runs }) => {
    // Note: agent sometimes passes "concurrency" when user means "runs" — handled gracefully
    const result = await validate(partner_folder, {
      controls: controls ? controls.split(/\s+/) : null,
      skipConversion: skip_conversion || false,
      appType: app_type || "SOFTWARE",
      concurrency: 15,
      consensus: runs || 1,
    });
    return { content: [{ type: "text", text: result }] };
  }
);

// thor_list_controls
server.tool(
  "thor_list_controls",
  {
    category: z.string().optional().describe("Filter by designation category"),
  },
  async ({ category }) => {
    const result = listControls(category);
    return { content: [{ type: "text", text: result }] };
  }
);

// thor_prep
server.tool(
  "thor_prep",
  {
    partner_name: z.string().describe("Name of the partner"),
    category: z.string().optional().describe("Designation category"),
    folder: z.string().optional().describe("Custom base folder path"),
  },
  async ({ partner_name, category, folder }) => {
    const result = await prep(partner_name, category, folder);
    return { content: [{ type: "text", text: result }] };
  }
);

// thor_diff — Compare validation runs
server.tool(
  "thor_diff",
  {
    partner_folder: z.string().describe("Path to partner folder"),
    mode: z.enum(["latest", "all", "custom"]).optional().describe("Comparison mode: latest (2 most recent), all (timeline table), custom (pick two)"),
    timestamp_a: z.string().optional().describe("First timestamp for custom mode (e.g. 20260507_115240)"),
    timestamp_b: z.string().optional().describe("Second timestamp for custom mode"),
  },
  async ({ partner_folder, mode, timestamp_a, timestamp_b }) => {
    const result = diff(partner_folder, mode || "latest", timestamp_a, timestamp_b);
    return { content: [{ type: "text", text: result }] };
  }
);

// thor_export — Render validation summary to HTML
server.tool(
  "thor_export",
  {
    partner_folder: z.string().describe("Path to partner folder containing validation_summary.md"),
    output_path: z.string().optional().describe("Custom output path for the HTML file"),
  },
  async ({ partner_folder, output_path }) => {
    const result = exportHtml(partner_folder, output_path);
    return { content: [{ type: "text", text: result }] };
  }
);

// thor_map — Standalone evidence map builder
server.tool(
  "thor_map",
  {
    partner_folder: z.string().describe("Path to partner folder with supporting_docs/"),
    force: z.boolean().optional().describe("Force rebuild even if map exists"),
    concurrency: z.number().optional().describe("Max parallel Bedrock calls for mapping (default 6)"),
  },
  async ({ partner_folder, force, concurrency }) => {
    const result = await map(partner_folder, {
      force: force || false,
      concurrency: concurrency || 6,
    });
    return { content: [{ type: "text", text: result }] };
  }
);

const transport = new StdioServerTransport();
await server.connect(transport);
